package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/samling/agent-status/internal/discovery"
	"github.com/samling/agent-status/internal/state"
	"github.com/samling/agent-status/internal/version"
)

var (
	// accentStyle is the bold-blue used for titles, active sort headers,
	// panel headers, and key hints. They were four separate styles with
	// identical settings; one alias keeps them in lockstep.
	accentStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12"))
	headerStyle = lipgloss.NewStyle().Bold(true)
	dimStyle    = lipgloss.NewStyle().Faint(true)
	errorStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
	borderStyle = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(0, 1)

	connectedStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("10"))
	disconnectedStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("9"))

	selectedBG = lipgloss.Color("237")
)

func keyHint(key, desc string) string {
	return accentStyle.Render(key) + " " + dimStyle.Render(desc)
}

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

// renderHeader builds the column-header row. sortKey is the sortMode
// whose active state highlights its column; -1 means the column does
// not correspond to any sortable field.
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
			parts = append(parts, accentStyle.Render(text))
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

	head.WriteString(accentStyle.Render("agent-status"))
	head.WriteString(" " + dimStyle.Render(version.Get()))
	if m.serverAddr != "" {
		dot, label, style := "●", "connected", connectedStyle
		if !m.serverUp {
			label, style = "disconnected", disconnectedStyle
		}
		head.WriteString(" " + style.Render(dot) + " " + dimStyle.Render(label))
	}
	head.WriteString(accentStyle.Render(fmt.Sprintf(", %d session(s)", len(m.sessions))))
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
					colTransition, relTime(s.StatusTime),
					colCreated, absTime(s.FirstSeenTime),
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

// labeledField renders "label: value" with a faint label and falls back
// to "-" when the value is empty. Shared between the detail and config
// blocks so both panels stay visually consistent.
func labeledField(label, value string) string {
	if value == "" {
		value = "-"
	}
	return dimStyle.Render(label+": ") + value
}

// renderConfig formats the UI's runtime configuration for the bottom
// block. Mirrors renderDetail's faint-label / value layout so the box
// looks consistent whether the foot is showing config or a session
// detail.
func (m uiModel) renderConfig() string {
	return strings.Join([]string{
		accentStyle.Render("Config"),
		labeledField("version", version.Get()),
		labeledField("config", m.configPath),
		labeledField("state", m.statePath),
		labeledField("notes", m.notesPath),
		labeledField("server", m.serverAddr),
		labeledField("refresh", m.interval.String()),
	}, "\n")
}

func renderDetail(sessionID, note string, info discovery.TranscriptInfo, meta discovery.SessionMeta) string {
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
	header := accentStyle.Render("Metadata")
	pid := "-"
	if meta.PID > 0 {
		pid = fmt.Sprintf("%d", meta.PID)
	}
	line1 := strings.Join([]string{
		labeledField("session", state.ShortID(sessionID)),
		labeledField("pid", pid),
		labeledField("model", info.Model),
		labeledField("branch", info.GitBranch),
		labeledField("mode", info.PermissionMode),
		labeledField("entrypoint", meta.Entrypoint),
	}, "   ")
	line2 := strings.Join([]string{
		labeledField("turns", turns),
		labeledField("in", num(info.InputTokens)),
		labeledField("out", num(info.OutputTokens)),
		labeledField("cache", cache),
	}, "   ")
	line3 := labeledField("cwd", meta.Cwd)
	line4 := labeledField("note", note)
	line5 := labeledField("last", prompt)
	return strings.Join([]string{header, line1, line2, line3, line4, line5}, "\n")
}
