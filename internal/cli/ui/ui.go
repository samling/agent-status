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
	statePath         string
	notesPath         string
	configPath        string
	interval          time.Duration
	cards             []sessionview.SessionCard
	notes             map[string]string
	selectedID        string
	scrollOffset      int
	expandedParents   map[string]bool
	sort              sortMode
	width             int
	height            int
	detail            sessionview.SessionDetail
	detailFor         string // session id that detail belongs to
	detailErr         error
	focusMode         focusMode
	messageList       sessionview.MessageList
	messageListFor    string
	messageListErr    error
	messageIndex      int
	showExtraMessages bool
	messageSearchMode bool
	messageQuery      string
	messageDetail     sessionview.MessageDetail
	messageDetailFor  string
	messageDetailErr  error
	messageScroll     int
	messageRaw        bool
	inputMode         bool
	inputBuf          string
	inputForID        string
	showConfig        bool
	quitAfterFocus    bool
	serverAddr        string
	serverUp          bool
	status            string
	err               error
}

type focusMode int

const (
	focusCards focusMode = iota
	focusMessages
	focusMessageBody
)

func (m uiModel) Init() tea.Cmd {
	return tea.Batch(loadSnapshot(m.serverAddr, m.selectedID, m.sort, cardOrder(m.cards)), tickEvery(m.interval))
}

func (m uiModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.keepSelectionVisible()
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
		if m.messageSearchMode {
			switch msg.String() {
			case "ctrl+c":
				return m, tea.Quit
			case "enter":
				m.messageSearchMode = false
			case "esc":
				m.messageSearchMode = false
				m.messageQuery = ""
				m.messageIndex = 0
			case "backspace":
				if r := []rune(m.messageQuery); len(r) > 0 {
					m.messageQuery = string(r[:len(r)-1])
					m.messageIndex = 0
				}
			default:
				if len(msg.Runes) > 0 {
					m.messageQuery += string(msg.Runes)
					m.messageIndex = 0
				}
			}
			m.clampMessageSelection()
			return m, nil
		}
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "esc":
			if m.focusMode == focusMessages && m.messageQuery != "" {
				m.messageSearchMode = false
				m.messageQuery = ""
				m.messageIndex = 0
				m.clampMessageSelection()
				return m, nil
			}
			if m.focusMode == focusMessageBody {
				m.focusMode = focusMessages
				m.messageScroll = 0
				return m, nil
			}
			if m.focusMode == focusMessages {
				m.focusMode = focusCards
				m.messageSearchMode = false
				m.messageQuery = ""
				return m, nil
			}
			return m, tea.Quit
		case "up", "k":
			if m.focusMode == focusMessages {
				m.moveMessageSelection(-1)
				return m, nil
			}
			if m.focusMode == focusMessageBody {
				m.scrollMessage(-1)
				return m, nil
			}
			prev := m.selectedID
			m.moveSelection(-1)
			if m.selectedID != "" && m.selectedID != prev {
				return m, loadDetail(m.serverAddr, m.selectedID)
			}
		case "down", "j":
			if m.focusMode == focusMessages {
				m.moveMessageSelection(+1)
				return m, nil
			}
			if m.focusMode == focusMessageBody {
				m.scrollMessage(+1)
				return m, nil
			}
			prev := m.selectedID
			m.moveSelection(+1)
			if m.selectedID != "" && m.selectedID != prev {
				return m, loadDetail(m.serverAddr, m.selectedID)
			}
		case "ctrl+u":
			if m.focusMode == focusMessages {
				m.moveMessageSelection(-halfPage)
				return m, nil
			}
			if m.focusMode == focusMessageBody {
				m.scrollMessage(-halfPage)
				return m, nil
			}
		case "ctrl+d":
			if m.focusMode == focusMessages {
				m.moveMessageSelection(halfPage)
				return m, nil
			}
			if m.focusMode == focusMessageBody {
				m.scrollMessage(halfPage)
				return m, nil
			}
		case "t":
			switch m.focusMode {
			case focusCards:
				id := m.activeSelectionID()
				if id == "" {
					m.status = "no session selected"
					return m, nil
				}
				m.selectedID = id
				m.showExtraMessages = !m.showExtraMessages
				if m.showExtraMessages {
					return m, m.messageListRefreshCmd()
				}
				return m, nil
			case focusMessages:
				m.showExtraMessages = !m.showExtraMessages
				m.clampMessageSelection()
				if m.showExtraMessages {
					return m, m.messageListRefreshCmd()
				}
				return m, nil
			case focusMessageBody:
				m.messageRaw = !m.messageRaw
				m.messageScroll = 0
				return m, nil
			}
		case " ":
			if m.focusMode != focusCards {
				return m, nil
			}
			if m.toggleExpanded(m.selectedID) {
				return m, nil
			}
		case "right", "l":
			if m.focusMode != focusCards {
				return m, nil
			}
			if m.expandSelected() {
				return m, nil
			}
		case "left", "h":
			if m.focusMode != focusCards {
				return m, nil
			}
			prev := m.selectedID
			if m.collapseSelected() {
				if m.selectedID != "" && m.selectedID != prev {
					return m, loadDetail(m.serverAddr, m.selectedID)
				}
				return m, nil
			}
		case "enter":
			if m.focusMode == focusCards {
				var cmd tea.Cmd
				m, cmd = m.focusSelected()
				if cmd != nil {
					return m, cmd
				}
				return m, nil
			}
			if m.focusMode == focusMessages {
				return m.openSelectedMessage()
			}
		case "tab", "shift+tab":
			switch m.focusMode {
			case focusCards:
				id := m.activeSelectionID()
				if id == "" {
					m.status = "no session selected"
					return m, nil
				}
				m.selectedID = id
				m.enterMessageList(id)
				return m, loadMessages(m.serverAddr, id)
			case focusMessages, focusMessageBody:
				m.focusMode = focusCards
				m.messageSearchMode = false
				m.messageScroll = 0
				m.messageRaw = false
				return m, nil
			}
		case "/":
			if m.focusMode == focusMessages {
				m.messageSearchMode = true
				m.messageQuery = ""
				m.messageIndex = 0
				return m, nil
			}
		case "s":
			if m.focusMode != focusCards {
				return m, nil
			}
			if m.selectedID == "" {
				m.selectedID = m.activeSelectionID()
			}
			previousOrder := cardOrder(m.cards)
			m.sort = m.sort.next()
			sortCards(m.cards, m.sort, previousOrder)
			m.keepSelectionVisible()
		case "n":
			if m.focusMode != focusCards {
				return m, nil
			}
			m = m.beginNote()
		case "?":
			m.showConfig = !m.showConfig
		}
	case tickMsg:
		return m, tea.Batch(loadSnapshot(m.serverAddr, m.selectedID, m.sort, cardOrder(m.cards)), tickEvery(m.interval), m.messageListRefreshCmd())
	case snapshotMsg:
		previousOrder := cardOrder(m.cards)
		m.cards = msg.cards
		m.serverUp = msg.serverUp
		m.pruneExpandedParents()
		if msg.sortedBy != m.sort {
			sortCards(m.cards, m.sort, previousOrder)
		}
		if m.selectedID != "" && !cardsContain(m.cards, m.selectedID) {
			m.selectedID = ""
			m.resetMessageState()
		}
		if m.selectedID != "" && !cardsContain(m.visibleCards(), m.selectedID) {
			if parentID := parentIDFor(m.cards, m.selectedID); parentID != "" {
				m.selectedID = parentID
				m.resetMessageState()
			} else {
				m.selectedID = ""
				m.resetMessageState()
			}
		}
		adoptedFocus := m.selectedID == "" && msg.detailFor != ""
		if adoptedFocus {
			m.selectedID = msg.detailFor
		}
		if adoptedFocus {
			m.scrollOffset = cardIndex(m.cards, m.selectedID)
		}
		m.keepSelectionVisible()
		activeID := m.selectedID
		if activeID == "" && len(m.cards) > 0 {
			activeID = m.cards[0].SessionID
		}
		if msg.detailFor == activeID {
			m.detail = msg.detail
			m.detailFor = msg.detailFor
			m.detailErr = msg.detailErr
		}
		m.err = nil
	case detailMsg:
		if msg.detailFor == m.selectedID {
			m.detail = msg.detail
			m.detailFor = msg.detailFor
			m.detailErr = msg.detailErr
		}
	case messageListMsg:
		if msg.sessionID == m.selectedID {
			m.messageList = msg.messages
			m.messageListFor = msg.sessionID
			m.messageListErr = msg.err
			m.clampMessageSelection()
		}
	case messageDetailMsg:
		if msg.sessionID == m.selectedID && msg.messageID == m.messageDetailFor {
			m.messageDetail = msg.detail
			m.messageDetailFor = msg.messageID
			m.messageDetailErr = msg.err
			m.messageScroll = 0
		}
	case errMsg:
		m.err = msg.err
	}
	return m, nil
}
