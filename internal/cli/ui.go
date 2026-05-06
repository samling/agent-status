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
	uiCmd.Flags().Bool("quit-after-focus", false, "exit the TUI after focusing a session (useful when launched in a tmux popup)")
}

func runUI(cmd *cobra.Command, _ []string) error {
	statePath := viper.GetString("state")
	notesPath := state.NotesPath(statePath)
	notes, _ := state.LoadNotes(notesPath)
	interval, _ := cmd.Flags().GetDuration("interval")
	quitAfterFocus, _ := cmd.Flags().GetBool("quit-after-focus")
	p := tea.NewProgram(uiModel{
		statePath:      statePath,
		notesPath:      notesPath,
		notes:          notes,
		interval:       interval,
		quitAfterFocus: quitAfterFocus,
	}, tea.WithAltScreen())
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
	status         string // ephemeral footer message (e.g. focus result)
	err            error
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

func (m uiModel) activeSelectionID() string {
	if m.selectedID != "" {
		return m.selectedID
	}
	if len(m.sessions) > 0 {
		return m.sessions[0].SessionID
	}
	return ""
}

func (m uiModel) beginNote() uiModel {
	id := m.activeSelectionID()
	if id == "" {
		m.status = "no session to note"
		return m
	}
	m.inputMode = true
	m.inputForID = id
	m.inputBuf = m.notes[id]
	m.status = ""
	return m
}

func (m uiModel) commitNote() uiModel {
	id := m.inputForID
	text := strings.TrimSpace(m.inputBuf)
	m.inputMode = false
	m.inputBuf = ""
	m.inputForID = ""
	if id == "" {
		return m
	}
	if err := state.SaveNote(m.notesPath, id, text); err != nil {
		m.status = "save note error: " + err.Error()
		return m
	}
	if m.notes == nil {
		m.notes = map[string]string{}
	}
	if text == "" {
		delete(m.notes, id)
	} else {
		m.notes[id] = text
	}
	return m
}

func (m uiModel) focusSelected() (uiModel, tea.Cmd) {
	if m.selectedID == "" {
		if len(m.sessions) == 0 {
			m.status = "no sessions to focus"
			return m, nil
		}
		m.selectedID = m.sessions[0].SessionID
	}
	meta, ok := m.meta[m.selectedID]
	if !ok || meta.PID <= 0 {
		m.status = "no live PID for selected session"
		return m, nil
	}
	msg, err := focus.PID(meta.PID)
	if err != nil {
		m.status = "focus error: " + err.Error()
		return m, nil
	}
	m.status = msg
	if m.quitAfterFocus {
		return m, tea.Quit
	}
	return m, nil
}

var (
	titleStyle        = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12"))
	headerStyle       = lipgloss.NewStyle().Bold(true)
	activeHeaderStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12"))
	panelHeaderStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12"))
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
// horizontal space inside the border. The session hash isn't a column
// any more; it's surfaced in the detail block at the bottom.
const (
	colStatus     = 8
	colVersion    = 10
	colLastEvent  = 20
	colTransition = 15
	colCreated    = 19 // length of "2026-05-05 15:04:05"
	colNote       = 30
)

// fixedCols sums the non-CWD column widths plus the 12 chars of
// inter-column separators (6 gaps × 2 spaces) and the 4 chars of border
// + padding. cwdWidth() returns whatever is left of the terminal width.
const fixedCols = colStatus + colVersion + colLastEvent + colTransition + colCreated + colNote + 12 + 4

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
		{"VERSION", colVersion, -1},
		{"CWD", cwd, -1},
		{"LAST EVENT", colLastEvent, -1},
		{"LAST TRANSITION", colTransition, sortActivity},
		{"CREATED", colCreated, sortCreated},
		{"NOTE", 0, -1},
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
	// head holds the title and either the error/empty-state or the
	// session table. foot holds the per-session detail block. They are
	// rendered separately so we can pad between them and anchor the
	// detail block to the bottom of the inner box area.
	var head, foot strings.Builder

	head.WriteString(titleStyle.Render(fmt.Sprintf("agent-status, %d session(s)", len(m.sessions))))
	head.WriteString("\n\n")

	selectedID := m.selectedID
	if selectedID == "" && len(m.sessions) > 0 {
		selectedID = m.sessions[0].SessionID
	}

	if m.err != nil {
		head.WriteString(errorStyle.Render("error: " + m.err.Error()))
	} else {
		cwdWidth := m.cwdWidth()
		if len(m.sessions) == 0 {
			head.WriteString(dimStyle.Render("(no live sessions)"))
		} else {
			head.WriteString(renderHeader(m.sort, cwdWidth))
			for _, s := range m.sessions {
				head.WriteString("\n")
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
				note := truncate(collapseWS(m.notes[s.SessionID]), colNote)
				rowText := fmt.Sprintf("%-*s  %-*s  %-*s  %-*s  %-*s  %-*s  %-*s",
					colStatus, s.Status,
					colVersion, ver,
					cwdWidth, cwd,
					colLastEvent, s.LastEvent,
					colTransition, relTime(s.StatusAt),
					colCreated, absTime(s.FirstSeenAt),
					colNote, note,
				)
				selected := s.SessionID == selectedID
				head.WriteString(rowStyle(s.Status, selected).Render(rowText))
			}
			if selectedID != "" && !m.showConfig {
				var info discovery.TranscriptInfo
				if m.detailFor == selectedID {
					info = m.detail
				}
				foot.WriteString(renderDetail(selectedID, m.notes[selectedID], info, m.meta[selectedID]))
			}
		}
	}
	if m.showConfig {
		foot.Reset()
		foot.WriteString(m.renderConfig())
	}

	inner := head.String()
	if footStr := foot.String(); footStr != "" {
		// Anchor the detail block to the bottom of the inner box. The
		// box content area is m.height-4 rows tall (matching the
		// box.Height set below); pad with blank rows between head and
		// foot so foot's last line lands on the box's bottom row.
		boxH := max(m.height-4, 1)
		headLines := lineCount(inner)
		footLines := lineCount(footStr)
		// Always keep at least one blank row between table and
		// detail; if the terminal is too short we'll overflow,
		// which is preferable to running them together.
		pad := max(boxH-headLines-footLines, 1)
		inner = inner + strings.Repeat("\n", pad+1) + footStr
	}

	var b strings.Builder
	box := borderStyle
	if m.width > 0 {
		box = box.Width(m.width - 2)
	}
	if m.height > 0 {
		// Reserve 4 rows below the box content: 2 for the box's own
		// border (top + bottom), 1 for the status row, 1 for the keymap.
		box = box.Height(max(m.height-4, 1))
	}
	b.WriteString(box.Render(inner))
	b.WriteString("\n")
	// Status row. In note input mode the prompt replaces any ephemeral
	// status; otherwise we always emit the row (even empty) so the
	// keymap stays pinned to the bottom regardless of state.
	if m.inputMode {
		b.WriteString(dimStyle.Render("note: ") + m.inputBuf + "▏")
	} else {
		b.WriteString(dimStyle.Render(m.status))
	}
	b.WriteString("\n")
	var keymap string
	if m.inputMode {
		keymap = strings.Join([]string{
			keyHint("enter", "save"),
			keyHint("esc", "cancel"),
		}, "   ")
	} else {
		keymap = strings.Join([]string{
			keyHint("↑/↓", "select"),
			keyHint("enter", "focus"),
			keyHint("n", "note"),
			keyHint("s", "sort:"+m.sort.String()),
			keyHint("?", "config"),
			keyHint("q", "quit"),
		}, "   ")
	}
	b.WriteString(keymap)
	return b.String()
}

// renderConfig formats the UI's runtime configuration for the bottom
// block. Mirrors renderDetail's faint-label / value layout so the box
// looks consistent whether the foot is showing config or a session
// detail.
func (m uiModel) renderConfig() string {
	labelStyle := lipgloss.NewStyle().Faint(true)
	field := func(label, value string) string {
		if value == "" {
			value = "-"
		}
		return labelStyle.Render(label+": ") + value
	}
	return strings.Join([]string{
		panelHeaderStyle.Render("Config"),
		field("state", m.statePath),
		field("notes", m.notesPath),
		field("refresh", m.interval.String()),
	}, "\n")
}

// lineCount returns the number of visual rows in s (a string with no
// trailing newline counts its last line).
func lineCount(s string) int {
	if s == "" {
		return 0
	}
	return strings.Count(s, "\n") + 1
}

func short(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

func renderDetail(sessionID, note string, info discovery.TranscriptInfo, meta discovery.SessionMeta) string {
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
	header := panelHeaderStyle.Render("Metadata")
	line1 := strings.Join([]string{
		field("session", short(sessionID)),
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
	line4 := field("note", note)
	line5 := field("last", prompt)
	return strings.Join([]string{header, line1, line2, line3, line4, line5}, "\n")
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
