package cli

import (
	"fmt"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"agent-status/internal/discovery"
	"agent-status/internal/focus"
	"agent-status/internal/state"
)

type sortMode int

const (
	sortStatus sortMode = iota
	sortActivity
	sortCreated
)

func (m sortMode) String() string {
	switch m {
	case sortActivity:
		return "activity"
	case sortCreated:
		return "created"
	case sortStatus:
		return "status"
	}
	return "?"
}

// sortCycle orders sort modes left-to-right by their highlighted column,
// so pressing `s` walks the highlight across the header in a predictable
// direction rather than jumping around.
var sortCycle = []sortMode{sortStatus, sortActivity, sortCreated}

func (m sortMode) next() sortMode {
	for i, s := range sortCycle {
		if s == m {
			return sortCycle[(i+1)%len(sortCycle)]
		}
	}
	return sortCycle[0]
}

var uiCmd = &cobra.Command{
	Use:   "ui",
	Short: "Live TUI of active sessions",
	RunE:  runUI,
}

func init() {
	uiCmd.Flags().Duration("interval", 500*time.Millisecond, "refresh interval")
}

func runUI(cmd *cobra.Command, _ []string) error {
	statePath := viper.GetString("state")
	interval, _ := cmd.Flags().GetDuration("interval")
	p := tea.NewProgram(uiModel{statePath: statePath, interval: interval}, tea.WithAltScreen())
	_, err := p.Run()
	return err
}

type tickMsg time.Time
type snapshotMsg struct {
	sessions  []state.Session
	meta      map[string]discovery.SessionMeta
	detail    discovery.TranscriptInfo
	detailFor string
}
type errMsg struct{ err error }

type uiModel struct {
	statePath  string
	interval   time.Duration
	sessions   []state.Session
	meta       map[string]discovery.SessionMeta
	selectedID string
	sort       sortMode
	width      int
	height     int
	detail     discovery.TranscriptInfo
	detailFor  string // session id that detail belongs to
	status     string // ephemeral footer message (e.g. focus result)
	err        error
}

func (m uiModel) Init() tea.Cmd {
	return tea.Batch(loadSnapshot(m.statePath, m.selectedID, m.sort), tickEvery(m.interval))
}

func loadSnapshot(path, selectedID string, mode sortMode) tea.Cmd {
	return func() tea.Msg {
		sessions, err := state.Load(path)
		if err != nil {
			return errMsg{err}
		}
		meta, _ := discovery.LiveSessionMeta()
		// Sort here so the focus picked below matches what the View
		// renders as the implicit first-row selection.
		sortSessions(sessions, mode)
		focus := selectedID
		if focus == "" && len(sessions) > 0 {
			focus = sessions[0].SessionID
		}
		var detail discovery.TranscriptInfo
		if focus != "" {
			if md, ok := meta[focus]; ok && md.Cwd != "" {
				detail, _ = discovery.LoadTranscript(focus, md.Cwd)
			}
		}
		return snapshotMsg{sessions: sessions, meta: meta, detail: detail, detailFor: focus}
	}
}

func tickEvery(d time.Duration) tea.Cmd {
	return tea.Tick(d, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func (m uiModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
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
		case "s":
			m.sort = m.sort.next()
			sortSessions(m.sessions, m.sort)
		}
	case tickMsg:
		return m, tea.Batch(loadSnapshot(m.statePath, m.selectedID, m.sort), tickEvery(m.interval))
	case snapshotMsg:
		m.sessions = msg.sessions
		m.meta = msg.meta
		m.detail = msg.detail
		m.detailFor = msg.detailFor
		sortSessions(m.sessions, m.sort)
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

func sortSessions(ss []state.Session, mode sortMode) {
	sort.SliceStable(ss, func(i, j int) bool {
		a, b := ss[i], ss[j]
		switch mode {
		case sortCreated:
			if a.FirstSeenAt != b.FirstSeenAt {
				return a.FirstSeenAt > b.FirstSeenAt
			}
		case sortStatus:
			ra, rb := statusRank(a.Status), statusRank(b.Status)
			if ra != rb {
				return ra < rb
			}
			// Within a status bucket, order by when the session
			// started so rows don't churn on every JSONL blip.
			if a.FirstSeenAt != b.FirstSeenAt {
				return a.FirstSeenAt < b.FirstSeenAt
			}
		default: // sortActivity
			if a.StatusAt != b.StatusAt {
				return a.StatusAt > b.StatusAt
			}
		}
		return a.SessionID < b.SessionID
	})
}

func statusRank(status string) int {
	switch status {
	case "waiting":
		return 0
	case "active":
		return 1
	case "idle":
		return 2
	}
	return 3
}

func sessionsContain(ss []state.Session, id string) bool {
	for _, s := range ss {
		if s.SessionID == id {
			return true
		}
	}
	return false
}

func (m *uiModel) moveSelection(delta int) {
	if len(m.sessions) == 0 {
		m.selectedID = ""
		return
	}
	// If selectedID is unset or stale, treat the visible default (row 0)
	// as the starting point so the delta applies on the first keystroke.
	cur := 0
	for i, s := range m.sessions {
		if s.SessionID == m.selectedID {
			cur = i
			break
		}
	}
	next := cur + delta
	if next < 0 {
		next = 0
	} else if next >= len(m.sessions) {
		next = len(m.sessions) - 1
	}
	m.selectedID = m.sessions[next].SessionID
}

func (m uiModel) focusSelected() uiModel {
	if m.selectedID == "" {
		if len(m.sessions) == 0 {
			m.status = "no sessions to focus"
			return m
		}
		m.selectedID = m.sessions[0].SessionID
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
	titleStyle        = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12"))
	headerStyle       = lipgloss.NewStyle().Bold(true)
	activeHeaderStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12"))
	dimStyle          = lipgloss.NewStyle().Faint(true)
	errorStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
	borderStyle       = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(0, 1)
	keyStyle          = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12"))

	selectedBG = lipgloss.Color("237")
)

func keyHint(key, desc string) string {
	return keyStyle.Render(key) + " " + dimStyle.Render(desc)
}

// columns describes the table layout. sortKey is the sortMode whose
// active state highlights this header; -1 means the column does not
// correspond to any sortable field.
// Column widths excluding CWD, which flexes to fill the remaining
// horizontal space inside the border.
const (
	colStatus     = 8
	colSession    = 12
	colVersion    = 10
	colLastEvent  = 20
	colTransition = 15
	colCreated    = 19 // length of "2026-05-05 15:04:05"
)

// fixedCols sums the non-CWD column widths plus the 12 chars of
// inter-column separators (6 gaps × 2 spaces) and the 4 chars of border
// + padding. cwdWidth() returns whatever is left of the terminal width.
const fixedCols = colStatus + colSession + colVersion + colLastEvent + colTransition + colCreated + 12 + 4

func (m uiModel) cwdWidth() int {
	if m.width <= 0 {
		return 30
	}
	w := m.width - fixedCols
	if w < 10 {
		return 10
	}
	if w > 80 {
		return 80
	}
	return w
}

func renderHeader(active sortMode, cwd int) string {
	cols := []struct {
		title   string
		width   int
		sortKey sortMode
	}{
		{"STATUS", colStatus, sortStatus},
		{"SESSION", colSession, -1},
		{"VERSION", colVersion, -1},
		{"CWD", cwd, -1},
		{"LAST EVENT", colLastEvent, -1},
		{"LAST TRANSITION", colTransition, sortActivity},
		{"CREATED", 0, sortCreated},
	}
	parts := make([]string, 0, len(cols))
	for _, c := range cols {
		text := c.title
		if c.width > 0 {
			text = fmt.Sprintf("%-*s", c.width, text)
		}
		if c.sortKey == active {
			parts = append(parts, activeHeaderStyle.Render(text))
		} else {
			parts = append(parts, headerStyle.Render(text))
		}
	}
	return strings.Join(parts, "  ")
}

// rowStyle composes the foreground for a row's status with an optional
// background when the row is selected. Returning a single style avoids
// nested-ANSI breakage when a background is layered over inner colors.
func rowStyle(status string, selected bool) lipgloss.Style {
	s := lipgloss.NewStyle()
	switch status {
	case "active":
		s = s.Bold(true).Foreground(lipgloss.Color("10"))
	case "waiting":
		s = s.Bold(true).Foreground(lipgloss.Color("11"))
	}
	if selected {
		s = s.Background(selectedBG)
	}
	return s
}

func (m uiModel) View() string {
	var inner strings.Builder

	inner.WriteString(titleStyle.Render(fmt.Sprintf("agent-status, %d session(s)", len(m.sessions))))
	inner.WriteString("\n\n")

	if m.err != nil {
		inner.WriteString(errorStyle.Render("error: " + m.err.Error()))
	} else {
		selectedID := m.selectedID
		if selectedID == "" && len(m.sessions) > 0 {
			selectedID = m.sessions[0].SessionID
		}

		cwdWidth := m.cwdWidth()
		if len(m.sessions) == 0 {
			inner.WriteString(dimStyle.Render("(no live sessions)"))
		} else {
			inner.WriteString(renderHeader(m.sort, cwdWidth))
			for _, s := range m.sessions {
				inner.WriteString("\n")
				meta := m.meta[s.SessionID]
				ver := meta.Version
				if ver == "" {
					ver = "-"
				}
				cwd := meta.Cwd
				if cwd == "" {
					cwd = "-"
				} else {
					cwd = shortPath(cwd, cwdWidth)
				}
				rowText := fmt.Sprintf("%-*s  %-*s  %-*s  %-*s  %-*s  %-*s  %s",
					colStatus, s.Status,
					colSession, short(s.SessionID),
					colVersion, ver,
					cwdWidth, cwd,
					colLastEvent, s.LastEvent,
					colTransition, relTime(s.StatusAt),
					absTime(s.FirstSeenAt),
				)
				selected := s.SessionID == selectedID
				inner.WriteString(rowStyle(s.Status, selected).Render(rowText))
			}
			if selectedID != "" {
				inner.WriteString("\n\n")
				var info discovery.TranscriptInfo
				if m.detailFor == selectedID {
					info = m.detail
				}
				inner.WriteString(renderDetail(info, m.meta[selectedID]))
			}
		}
	}

	var b strings.Builder
	box := borderStyle
	if m.width > 0 {
		box = box.Width(m.width - 2)
	}
	b.WriteString(box.Render(inner.String()))
	b.WriteString("\n")
	if m.status != "" {
		b.WriteString(dimStyle.Render(m.status))
		b.WriteString("\n")
	}
	keymap := strings.Join([]string{
		keyHint("↑/↓", "select"),
		keyHint("enter", "focus"),
		keyHint("s", "sort:"+m.sort.String()),
		keyHint("q", "quit"),
	}, "   ")
	b.WriteString(keymap)
	return b.String()
}

func short(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

func renderDetail(info discovery.TranscriptInfo, meta discovery.SessionMeta) string {
	labelStyle := lipgloss.NewStyle().Faint(true)
	field := func(label, value string) string {
		if value == "" {
			value = "-"
		}
		return labelStyle.Render(label+": ") + value
	}
	num := func(n int64) string {
		if n <= 0 {
			return "-"
		}
		return humanTokens(n)
	}
	turns := "-"
	if info.TurnCount > 0 {
		turns = fmt.Sprintf("%d", info.TurnCount)
	}
	cache := "-"
	if info.CacheReadTokens > 0 || info.CacheCreationTokens > 0 {
		cache = fmt.Sprintf("%s read / %s create", num(info.CacheReadTokens), num(info.CacheCreationTokens))
	}
	prompt := truncate(collapseWS(info.LastUserPrompt), 100)
	line1 := strings.Join([]string{
		field("model", info.Model),
		field("branch", info.GitBranch),
		field("mode", info.PermissionMode),
		field("entrypoint", meta.Entrypoint),
	}, "   ")
	line2 := strings.Join([]string{
		field("turns", turns),
		field("in", num(info.InputTokens)),
		field("out", num(info.OutputTokens)),
		field("cache", cache),
	}, "   ")
	line3 := field("cwd", meta.Cwd)
	line4 := field("last", prompt)
	return strings.Join([]string{line1, line2, line3, line4}, "\n")
}

func collapseWS(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func truncate(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	if max <= 3 {
		return string(r[:max])
	}
	return string(r[:max-3]) + "..."
}

func humanTokens(n int64) string {
	switch {
	case n < 1000:
		return fmt.Sprintf("%d", n)
	case n < 1_000_000:
		return fmt.Sprintf("%.1fK", float64(n)/1000)
	case n < 1_000_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	}
	return fmt.Sprintf("%.1fB", float64(n)/1_000_000_000)
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
