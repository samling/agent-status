// Package ui implements the Bubble Tea TUI.
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

	"github.com/samling/agent-status/internal/logging"
	"github.com/samling/agent-status/internal/sessionview"
	"github.com/samling/agent-status/internal/state"
)

func serverEndpoint() string {
	addr := viper.GetString("server.addr")
	port := viper.GetString("server.port")
	if port == "" {
		return addr
	}
	return addr + ":" + port
}

// Command returns the ui subcommand.
func Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ui",
		Short: "Live TUI of active sessions",
		RunE:  runUI,
	}
	cmd.Flags().Duration("interval", 500*time.Millisecond, "refresh interval")
	cmd.Flags().Bool("quit-after-focus", false, "exit the TUI after focusing a session (useful when launched in a tmux popup)")
	cmd.Flags().Bool("log", false, "write TUI debug logs to $XDG_STATE_HOME/agent-status/ui.log")

	bindings := map[string]string{
		"ui.interval":         "interval",
		"ui.quit-after-focus": "quit-after-focus",
		"ui.log.enabled":      "log",
	}
	for key, flag := range bindings {
		_ = viper.BindPFlag(key, cmd.Flags().Lookup(flag))
	}
	return cmd
}

// openTUILog keeps slog output off the alt screen. When ui.log.enabled is
// false (the default) the logger is swapped for slog.DiscardHandler so log
// calls short-circuit before formatting; that also prevents stray writes
// from bleeding into the TUI render buffer.
func openTUILog() (io.Writer, func()) {
	if !viper.GetBool("ui.log.enabled") {
		return io.Discard, logging.Silence()
	}
	path := tuiLogPath()
	if path == "" {
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
	statePath      string
	notesPath      string
	configPath     string
	interval       time.Duration
	cards          []sessionview.SessionCard
	notes          map[string]string
	selectedID     string
	sort           sortMode
	width          int
	height         int
	detail         sessionview.SessionDetail
	detailFor      string // session id that detail belongs to
	inputMode      bool
	inputBuf       string
	inputForID     string
	showConfig     bool
	quitAfterFocus bool
	serverAddr     string
	serverUp       bool
	status         string
	err            error
}

func (m uiModel) Init() tea.Cmd {
	return tea.Batch(loadSnapshot(m.serverAddr, m.selectedID, m.sort), tickEvery(m.interval))
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
			sortCards(m.cards, m.sort)
		case "n":
			m = m.beginNote()
		case "?":
			m.showConfig = !m.showConfig
		}
	case tickMsg:
		return m, tea.Batch(loadSnapshot(m.serverAddr, m.selectedID, m.sort), tickEvery(m.interval))
	case snapshotMsg:
		m.cards = msg.cards
		m.detail = msg.detail
		m.detailFor = msg.detailFor
		m.serverUp = msg.serverUp
		if msg.sortedBy != m.sort {
			sortCards(m.cards, m.sort)
		}
		if m.selectedID != "" && !cardsContain(m.cards, m.selectedID) {
			m.selectedID = ""
		}
		m.err = nil
	case errMsg:
		m.err = msg.err
	}
	return m, nil
}
