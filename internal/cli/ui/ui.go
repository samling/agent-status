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
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/samling/agent-status/internal/discovery"
	"github.com/samling/agent-status/internal/state"
)

// Command returns the cobra subcommand registered by the parent cli
// package. Constructed each call so the parent owns the lifetime.
func Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ui",
		Short: "Live TUI of active sessions",
		RunE:  runUI,
	}
	cmd.Flags().Duration("interval", 500*time.Millisecond, "refresh interval")
	cmd.Flags().Bool("quit-after-focus", false, "exit the TUI after focusing a session (useful when launched in a tmux popup)")
	cmd.Flags().String("server", "127.0.0.1:7878", "host:port of the agent-status server, used for the connection indicator")
	return cmd
}

func runUI(cmd *cobra.Command, _ []string) error {
	statePath := viper.GetString("state")
	notesPath := state.NotesPath(statePath)
	notes, _ := state.LoadNotes(notesPath)
	interval, _ := cmd.Flags().GetDuration("interval")
	quitAfterFocus, _ := cmd.Flags().GetBool("quit-after-focus")
	serverAddr, _ := cmd.Flags().GetString("server")
	p := tea.NewProgram(uiModel{
		statePath:      statePath,
		notesPath:      notesPath,
		notes:          notes,
		interval:       interval,
		quitAfterFocus: quitAfterFocus,
		serverAddr:     serverAddr,
	}, tea.WithAltScreen())
	_, err := p.Run()
	return err
}

type uiModel struct {
	statePath  string
	notesPath  string
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
