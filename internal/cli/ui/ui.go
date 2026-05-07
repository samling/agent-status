// Package ui implements the `agent-status ui` Bubble Tea TUI. It is a
// sibling of the cobra-only subcommands in internal/cli; root.go in
// that parent package registers Command() as one of its subcommands.
//
// The package is split by concern:
//
//   - ui.go        cobra command, runUI, uiModel, Init, Update
//   - commands.go  tea.Cmd factories (snapshot load, tick, server probe)
//   - actions.go   model mutations triggered by key events
//   - view.go      View() and rendering helpers (header, rows, detail, config)
//   - sort.go      sortMode and stable session ordering
//   - format.go    string/time/path formatters
package ui

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/samling/agent-status/internal/discovery"
	"github.com/samling/agent-status/internal/logging"
	"github.com/samling/agent-status/internal/state"
)

// serverEndpoint mirrors cli.ServerEndpoint. Duplicated (rather than
// imported) because the parent cli package imports this one, so a
// reverse import would cycle. The composition is two viper reads;
// keeping them inline beats inventing a shared helper package.
func serverEndpoint() string {
	addr := viper.GetString("server.addr")
	port := viper.GetString("server.port")
	if port == "" {
		return addr
	}
	return addr + ":" + port
}

// Command returns the cobra subcommand registered by the parent cli
// package. Constructed each call so the parent owns the lifetime;
// flags bind into the shared viper instance under the "ui." prefix
// so they pick up values from config and AGENT_STATUS_UI_* env vars.
func Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ui",
		Short: "Live TUI of active sessions",
		RunE:  runUI,
	}
	cmd.Flags().Duration("interval", 500*time.Millisecond, "refresh interval")
	cmd.Flags().Bool("quit-after-focus", false, "exit the TUI after focusing a session (useful when launched in a tmux popup)")

	bindings := map[string]string{
		"ui.interval":         "interval",
		"ui.quit-after-focus": "quit-after-focus",
	}
	for key, flag := range bindings {
		_ = viper.BindPFlag(key, cmd.Flags().Lookup(flag))
	}
	return cmd
}

// openTUILog routes slog to a per-user log file so debug output does
// not bleed into the alt-screen TUI. Returns the active writer and a
// restore func; both are nil if no usable destination could be opened
// (in which case the caller should fall back to discarding logs).
func openTUILog() (io.Writer, func()) {
	path := tuiLogPath()
	if path == "" {
		// No state dir resolvable: fully silence rather than risk
		// writing to stderr and corrupting the screen.
		restore := logging.Redirect(io.Discard, logging.Resolve())
		return io.Discard, restore
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		restore := logging.Redirect(io.Discard, logging.Resolve())
		slog.Warn("ui: log dir create failed, discarding logs", "path", path, "err", err)
		return io.Discard, restore
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		restore := logging.Redirect(io.Discard, logging.Resolve())
		slog.Warn("ui: log open failed, discarding logs", "path", path, "err", err)
		return io.Discard, restore
	}
	restore := logging.Redirect(f, logging.Resolve())
	return f, func() {
		restore()
		_ = f.Close()
	}
}

// tuiLogPath returns $XDG_STATE_HOME/agent-status/ui.log, falling
// back to ~/.local/state/agent-status/ui.log. Empty when neither is
// resolvable (the caller will silence logs instead).
func tuiLogPath() string {
	if base := os.Getenv("XDG_STATE_HOME"); base != "" {
		return filepath.Join(base, "agent-status", "ui.log")
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".local", "state", "agent-status", "ui.log")
}

func runUI(_ *cobra.Command, _ []string) error {
	statePath := viper.GetString("state")
	notesPath := state.NotesPath(statePath)
	notes, _ := state.LoadNotes(notesPath)

	// Bubble Tea takes over the alt screen, so any slog write to
	// stderr (e.g. focus.PID's debug lines) corrupts the display.
	// Divert slog to a per-user log file for the lifetime of the TUI
	// so debug output is still recoverable post-mortem.
	_, restoreLogs := openTUILog()
	if restoreLogs != nil {
		defer restoreLogs()
	}

	p := tea.NewProgram(uiModel{
		statePath:      statePath,
		notesPath:      notesPath,
		configPath:     viper.ConfigFileUsed(),
		notes:          notes,
		interval:       viper.GetDuration("ui.interval"),
		quitAfterFocus: viper.GetBool("ui.quit-after-focus"),
		serverAddr:     serverEndpoint(),
	}, tea.WithAltScreen())
	_, err := p.Run()
	return err
}

type uiModel struct {
	statePath  string
	notesPath  string
	// configPath is the YAML config file viper actually loaded, or
	// empty when no config was found / `--config` was unset.
	configPath string
	interval   time.Duration
	sessions   []state.Session
	meta       map[string]discovery.SessionMeta
	notes      map[string]string
	selectedID string
	sort       sortMode
	width      int
	height     int
	detail     discovery.TranscriptInfo
	detailFor  string // session id that detail belongs to
	// Note input mode: when active, key presses go into inputBuf and
	// `enter` saves the note for inputForID. Captured at entry so a
	// subsequent selection change can't redirect the save target.
	inputMode  bool
	inputBuf   string
	inputForID string
	// When true, the bottom block shows the UI's config (state path,
	// notes path, refresh interval) instead of the per-session detail.
	showConfig bool
	// When true, the program exits after a successful focus action.
	// Useful when the TUI is launched in a tmux popup that should
	// close itself once the user has picked a session.
	quitAfterFocus bool
	// serverAddr is the host:port used to probe whether the collector
	// is up. Empty disables the probe and hides the indicator.
	serverAddr string
	// serverUp tracks the result of the most recent probe. Defaults to
	// false so the indicator only shows green once we've confirmed
	// reachability rather than misreporting at startup.
	serverUp bool
	status   string // ephemeral footer message (e.g. focus result)
	err      error
}

func (m uiModel) Init() tea.Cmd {
	return tea.Batch(loadSnapshot(m.statePath, m.serverAddr, m.selectedID, m.sort), tickEvery(m.interval))
}

func (m uiModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
	case tea.KeyMsg:
		if m.inputMode {
			switch msg.String() {
			case "ctrl+c":
				return m, tea.Quit
			case "enter":
				m = m.commitNote()
			case "esc":
				m.inputMode = false
				m.inputBuf = ""
				m.inputForID = ""
			case "backspace":
				if r := []rune(m.inputBuf); len(r) > 0 {
					m.inputBuf = string(r[:len(r)-1])
				}
			default:
				if len(msg.Runes) > 0 {
					m.inputBuf += string(msg.Runes)
				}
			}
			return m, nil
		}
		switch msg.String() {
		case "q", "ctrl+c", "esc":
			return m, tea.Quit
		case "up", "k":
			m.moveSelection(-1)
		case "down", "j":
			m.moveSelection(+1)
		case "enter":
			var cmd tea.Cmd
			m, cmd = m.focusSelected()
			if cmd != nil {
				return m, cmd
			}
		case "s":
			m.sort = m.sort.next()
			sortSessions(m.sessions, m.sort)
		case "n":
			m = m.beginNote()
		case "?":
			m.showConfig = !m.showConfig
		}
	case tickMsg:
		return m, tea.Batch(loadSnapshot(m.statePath, m.serverAddr, m.selectedID, m.sort), tickEvery(m.interval))
	case snapshotMsg:
		m.sessions = msg.sessions
		m.meta = msg.meta
		m.detail = msg.detail
		m.detailFor = msg.detailFor
		m.serverUp = msg.serverUp
		// Snapshots are sorted at load time; only re-sort if the user
		// flipped the mode while the load was in flight.
		if msg.sortedBy != m.sort {
			sortSessions(m.sessions, m.sort)
		}
		// If the previously-selected session disappeared, drop the selection.
		if m.selectedID != "" && !sessionsContain(m.sessions, m.selectedID) {
			m.selectedID = ""
		}
		m.err = nil
	case errMsg:
		m.err = msg.err
	}
	return m, nil
}
