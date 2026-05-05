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
	db       *sql.DB
	interval time.Duration
	sessions []store.Session
	meta     map[string]discovery.SessionMeta
	err      error
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
		}
	case tickMsg:
		return m, tea.Batch(loadSnapshot(m.db), tickEvery(m.interval))
	case snapshotMsg:
		m.sessions = msg.sessions
		m.meta = msg.meta
		m.err = nil
	case errMsg:
		m.err = msg.err
	}
	return m, nil
}

var (
	titleStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12"))
	headerStyle  = lipgloss.NewStyle().Bold(true).Underline(true)
	dimStyle     = lipgloss.NewStyle().Faint(true)
	errorStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
	waitingStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("9"))
	activeStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("10"))

	iconStyles = map[string]lipgloss.Style{
		"active":  lipgloss.NewStyle().Foreground(lipgloss.Color("10")),
		"idle":    lipgloss.NewStyle().Foreground(lipgloss.Color("8")),
		"waiting": lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("9")),
		"ended":   lipgloss.NewStyle().Faint(true),
	}
)

func statusIcon(status string) string {
	if style, ok := iconStyles[status]; ok {
		return style.Render("●")
	}
	return " "
}

func (m uiModel) View() string {
	var b strings.Builder

	alive := []store.Session{}
	for _, s := range m.sessions {
		if s.Status != "ended" {
			alive = append(alive, s)
		}
	}

	b.WriteString(titleStyle.Render(fmt.Sprintf("agent-status, %d session(s)", len(alive))))
	b.WriteString("\n\n")

	if m.err != nil {
		b.WriteString(errorStyle.Render("error: " + m.err.Error()))
		b.WriteString("\n")
		return b.String()
	}

	if len(alive) == 0 {
		b.WriteString(dimStyle.Render("(no live sessions)"))
	} else {
		b.WriteString("  ")
		b.WriteString(headerStyle.Render(fmt.Sprintf("%-8s  %-12s  %-14s  %-30s  %-20s  %-14s  %s", "STATUS", "SESSION", "ENTRYPOINT", "CWD", "LAST EVENT", "EVENT DURATION", "CREATED")))
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
			row := fmt.Sprintf("%-8s  %-12s  %-14s  %-30s  %-20s  %-14s  %s",
				s.Status,
				short(s.SessionID),
				ep,
				cwd,
				s.LastEvent,
				relTime(s.LastEventAt),
				absTime(s.FirstSeenAt),
			)
			switch s.Status {
			case "waiting":
				row = waitingStyle.Render(row)
			case "active":
				row = activeStyle.Render(row)
			}
			b.WriteString(statusIcon(s.Status))
			b.WriteString(" ")
			b.WriteString(row)
			b.WriteString("\n")
		}
	}

	b.WriteString("\n")
	b.WriteString(dimStyle.Render("q quit"))
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
