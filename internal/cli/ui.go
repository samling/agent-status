package cli

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"agent-status/internal/discovery"
	"agent-status/internal/focus"
	"agent-status/internal/store"
)

var uiCmd = &cobra.Command{
	Use:   "ui",
	Short: "Live TUI of active sessions",
	RunE:  runUI,
}

func init() {
	uiCmd.Flags().Duration("interval", 500*time.Millisecond, "refresh interval")
}

func runUI(cmd *cobra.Command, _ []string) error {
	db, err := store.Open(viper.GetString("db"))
	if err != nil {
		return err
	}
	defer db.Close()
	// Cycle the SQLite connection so reads pick up writes that happened
	// across a server restart (WAL shared-memory state can otherwise stick).
	db.SetConnMaxLifetime(2 * time.Second)

	interval, _ := cmd.Flags().GetDuration("interval")
	p := tea.NewProgram(uiModel{db: db, interval: interval}, tea.WithAltScreen())
	_, err = p.Run()
	return err
}

type tickMsg time.Time
type snapshotMsg struct {
	sessions []store.Session
	meta     map[string]discovery.SessionMeta
}
type errMsg struct{ err error }

type uiModel struct {
	db         *sql.DB
	interval   time.Duration
	sessions   []store.Session
	meta       map[string]discovery.SessionMeta
	selectedID string
	status     string // ephemeral footer message (e.g. focus result)
	err        error
}

func (m uiModel) Init() tea.Cmd {
	return tea.Batch(loadSnapshot(m.db), tickEvery(m.interval))
}

func loadSnapshot(db *sql.DB) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		sessions, err := store.QuerySessions(ctx, db)
		if err != nil {
			return errMsg{err}
		}
		meta, _ := discovery.LiveSessionMeta()
		return snapshotMsg{sessions: sessions, meta: meta}
	}
}

func tickEvery(d time.Duration) tea.Cmd {
	return tea.Tick(d, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func (m uiModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c", "esc":
			return m, tea.Quit
		case "up", "k":
			m.moveSelection(-1)
		case "down", "j":
			m.moveSelection(+1)
		case "enter":
			m = m.focusSelected()
		}
	case tickMsg:
		return m, tea.Batch(loadSnapshot(m.db), tickEvery(m.interval))
	case snapshotMsg:
		m.sessions = msg.sessions
		m.meta = msg.meta
		// If the previously-selected session disappeared, fall back to first alive.
		if m.selectedID != "" && !sessionsContain(m.aliveSessions(), m.selectedID) {
			m.selectedID = ""
		}
		m.err = nil
	case errMsg:
		m.err = msg.err
	}
	return m, nil
}

func (m uiModel) aliveSessions() []store.Session {
	out := make([]store.Session, 0, len(m.sessions))
	for _, s := range m.sessions {
		if s.Status != "ended" {
			out = append(out, s)
		}
	}
	return out
}

func sessionsContain(ss []store.Session, id string) bool {
	for _, s := range ss {
		if s.SessionID == id {
			return true
		}
	}
	return false
}

func (m *uiModel) moveSelection(delta int) {
	alive := m.aliveSessions()
	if len(alive) == 0 {
		m.selectedID = ""
		return
	}
	// If selectedID is unset or stale, treat the visible default (row 0)
	// as the starting point so the delta applies on the first keystroke.
	cur := 0
	for i, s := range alive {
		if s.SessionID == m.selectedID {
			cur = i
			break
		}
	}
	next := cur + delta
	if next < 0 {
		next = 0
	} else if next >= len(alive) {
		next = len(alive) - 1
	}
	m.selectedID = alive[next].SessionID
}

func (m uiModel) focusSelected() uiModel {
	if m.selectedID == "" {
		alive := m.aliveSessions()
		if len(alive) == 0 {
			m.status = "no sessions to focus"
			return m
		}
		m.selectedID = alive[0].SessionID
	}
	meta, ok := m.meta[m.selectedID]
	if !ok || meta.PID <= 0 {
		m.status = "no live PID for selected session"
		return m
	}
	msg, err := focus.PID(meta.PID)
	if err != nil {
		m.status = "focus error: " + err.Error()
		return m
	}
	m.status = msg
	return m
}

var (
	titleStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12"))
	headerStyle = lipgloss.NewStyle().Bold(true).Underline(true)
	dimStyle    = lipgloss.NewStyle().Faint(true)
	errorStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))

	selectedBG = lipgloss.Color("237")
)

// rowStyle composes the foreground for a row's status with an optional
// background when the row is selected. Returning a single style avoids
// nested-ANSI breakage when a background is layered over inner colors.
func rowStyle(status string, selected bool) lipgloss.Style {
	s := lipgloss.NewStyle()
	switch status {
	case "active":
		s = s.Bold(true).Foreground(lipgloss.Color("10"))
	case "waiting":
		s = s.Bold(true).Foreground(lipgloss.Color("9"))
	case "ended":
		s = s.Faint(true)
	}
	if selected {
		s = s.Background(selectedBG)
	}
	return s
}

func (m uiModel) View() string {
	var b strings.Builder

	alive := m.aliveSessions()

	b.WriteString(titleStyle.Render(fmt.Sprintf("agent-status, %d session(s)", len(alive))))
	b.WriteString("\n\n")

	if m.err != nil {
		b.WriteString(errorStyle.Render("error: " + m.err.Error()))
		b.WriteString("\n")
		return b.String()
	}

	// Default selection to the first alive session if nothing chosen yet.
	selectedID := m.selectedID
	if selectedID == "" && len(alive) > 0 {
		selectedID = alive[0].SessionID
	}

	if len(alive) == 0 {
		b.WriteString(dimStyle.Render("(no live sessions)"))
	} else {
		b.WriteString(headerStyle.Render(fmt.Sprintf("%-8s  %-12s  %-14s  %-30s  %-20s  %-15s  %s", "STATUS", "SESSION", "ENTRYPOINT", "CWD", "LAST EVENT", "LAST TRANSITION", "CREATED")))
		b.WriteString("\n")
		for _, s := range alive {
			meta := m.meta[s.SessionID]
			ep := meta.Entrypoint
			if ep == "" {
				ep = "-"
			}
			cwd := meta.Cwd
			if cwd == "" {
				cwd = "-"
			} else {
				cwd = shortPath(cwd, 30)
			}
			rowText := fmt.Sprintf("%-8s  %-12s  %-14s  %-30s  %-20s  %-15s  %s",
				s.Status,
				short(s.SessionID),
				ep,
				cwd,
				s.LastEvent,
				relTime(s.LastEventAt),
				absTime(s.FirstSeenAt),
			)
			selected := s.SessionID == selectedID
			b.WriteString(rowStyle(s.Status, selected).Render(rowText))
			b.WriteString("\n")
		}
	}

	b.WriteString("\n")
	if m.status != "" {
		b.WriteString(dimStyle.Render(m.status))
		b.WriteString("\n")
	}
	b.WriteString(dimStyle.Render("↑/↓ select  enter focus  q quit"))
	return b.String()
}

func short(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// shortPath truncates a path with an "..." prefix if it exceeds max,
// keeping the trailing portion (project basename, etc.) which is the
// most informative end of a working directory. ASCII ellipsis is used
// so the resulting byte length matches the visual width and printf
// padding stays correct.
func shortPath(p string, max int) string {
	if len(p) <= max {
		return p
	}
	if max <= 3 {
		return p[len(p)-max:]
	}
	return "..." + p[len(p)-max+3:]
}

func absTime(s string) string {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return s
	}
	return t.Local().Format("2006-01-02 15:04:05")
}

func relTime(s string) string {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return s
	}
	d := time.Since(t)
	switch {
	case d < time.Second:
		return "<1s ago"
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}
