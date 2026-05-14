package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/samling/agent-status/internal/sessionview"
	"github.com/samling/agent-status/internal/version"
)

var (
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

const (
	minLeftPane  = 24
	maxLeftPane  = 46
	paneGap      = 2
	cardPadWidth = 2
)

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

func (m uiModel) paneWidths() (int, int) {
	inner := m.width - 4
	if inner < 50 {
		left := minLeftPane
		right := inner - left - paneGap
		if right < 20 {
			right = 20
		}
		return left, right
	}
	left := inner * 38 / 100
	if left < minLeftPane {
		left = minLeftPane
	}
	if left > maxLeftPane {
		left = maxLeftPane
	}
	right := inner - left - paneGap
	if right < 20 {
		right = 20
	}
	return left, right
}

func (m uiModel) renderCards(width int, selectedID string) string {
	lines := []string{headerStyle.Render("Sessions")}
	if len(m.cards) == 0 {
		lines = append(lines, dimStyle.Render("(no live sessions)"))
		return strings.Join(lines, "\n")
	}
	for _, card := range m.cards {
		selected := card.SessionID == selectedID
		lines = append(lines, renderCard(card, width, selected, m.detailFor == card.SessionID, m.detail))
	}
	return strings.Join(lines, "\n")
}

func renderCard(card sessionview.SessionCard, width int, selected, hasDetail bool, detail sessionview.SessionDetail) string {
	if width < 10 {
		width = 10
	}
	agent := strings.ToUpper(card.Agent)
	status := card.Status
	title := truncate(card.Title, width-cardPadWidth)
	subtitle := truncate(card.Subtitle, width-cardPadWidth)
	top := fmt.Sprintf("%-*s %s", max(width-len(status)-1, 1), agent, status)
	parts := []string{top, title, subtitle}
	if selected && hasDetail && len(detail.Conversation) > 0 {
		preview := detail.Conversation[0].Role + ": " + detail.Conversation[0].Text
		parts = append(parts, truncate(preview, width-cardPadWidth))
	}
	body := strings.Join(parts, "\n")
	return rowStyle(card.Status, selected).Render(body)
}

func renderSessionDetail(detail sessionview.SessionDetail, width int) string {
	if detail.SessionID == "" {
		return dimStyle.Render("select a session")
	}
	lines := []string{
		accentStyle.Render(detail.Title),
		rowStyle(detail.Status, false).Render(detail.Agent + " " + detail.Status),
		"",
		headerStyle.Render("Metadata"),
	}
	lines = append(lines, renderMetadata(detail.Metadata, width)...)
	lines = append(lines, "", headerStyle.Render("Conversation"))
	if detail.TranscriptError != "" {
		lines = append(lines, errorStyle.Render("transcript: "+detail.TranscriptError))
		return strings.Join(lines, "\n")
	}
	if len(detail.Conversation) == 0 {
		lines = append(lines, dimStyle.Render("(no conversation preview)"))
		return strings.Join(lines, "\n")
	}
	for _, msg := range detail.Conversation {
		label := "User"
		if msg.Role == "assistant" {
			label = "AI"
		}
		line := fmt.Sprintf("%-4s %s", label, msg.Text)
		lines = append(lines, truncate(line, width))
	}
	return strings.Join(lines, "\n")
}

func renderDetailUnavailable(err error, width int) string {
	lines := []string{errorStyle.Render("detail unavailable")}
	if err != nil {
		lines = append(lines, truncate(err.Error(), width))
	}
	return strings.Join(lines, "\n")
}

func renderMetadata(fields []sessionview.Field, width int) []string {
	if len(fields) == 0 {
		return []string{dimStyle.Render("(no metadata)")}
	}
	lines := make([]string, 0, (len(fields)+1)/2)
	for i := 0; i < len(fields); i += 2 {
		left := metadataField(fields[i], width)
		if i+1 >= len(fields) {
			lines = append(lines, left)
			continue
		}
		right := metadataField(fields[i+1], width)
		line := left + "   " + right
		if lipgloss.Width(line) <= width {
			lines = append(lines, line)
		} else {
			lines = append(lines, left, right)
		}
	}
	return lines
}

func metadataField(field sessionview.Field, width int) string {
	if width <= 0 {
		return ""
	}
	value := field.Value
	if value == "" {
		value = "-"
	}
	label := truncate(field.Label+": ", width)
	labelWidth := lipgloss.Width(label)
	if labelWidth >= width {
		return dimStyle.Render(label)
	}
	return dimStyle.Render(label) + truncate(value, width-labelWidth)
}

func (m uiModel) View() string {
	var head strings.Builder

	head.WriteString(accentStyle.Render("agent-status"))
	head.WriteString(" " + dimStyle.Render(version.Get()))
	if m.serverAddr != "" {
		dot, label, style := "●", "connected", connectedStyle
		if !m.serverUp {
			label, style = "disconnected", disconnectedStyle
		}
		head.WriteString(" " + style.Render(dot) + " " + dimStyle.Render(label))
	}
	head.WriteString(accentStyle.Render(fmt.Sprintf(", %d session(s)", len(m.cards))))
	head.WriteString("\n\n")

	selectedID := m.selectedID
	if selectedID == "" && len(m.cards) > 0 {
		selectedID = m.cards[0].SessionID
	}

	if m.err != nil {
		head.WriteString(errorStyle.Render("error: " + m.err.Error()))
	} else {
		leftW, rightW := m.paneWidths()
		left := lipgloss.NewStyle().Width(leftW).Render(m.renderCards(leftW, selectedID))
		rightContent := ""
		switch {
		case m.showConfig:
			rightContent = m.renderConfig()
		case m.detailFor == selectedID && m.detailErr != nil:
			rightContent = renderDetailUnavailable(m.detailErr, rightW)
		default:
			rightDetail := sessionview.SessionDetail{}
			if m.detailFor == selectedID {
				rightDetail = m.detail
			}
			rightContent = renderSessionDetail(rightDetail, rightW)
		}
		right := lipgloss.NewStyle().Width(rightW).Render(rightContent)
		head.WriteString(lipgloss.JoinHorizontal(lipgloss.Top, left, strings.Repeat(" ", paneGap), right))
	}

	inner := head.String()

	var b strings.Builder
	box := borderStyle
	if m.width > 0 {
		box = box.Width(m.width - 2)
		// Clip per-line so rows wider than the box content area don't wrap
		// onto a second visual line and push the header off the top.
		inner = lipgloss.NewStyle().MaxWidth(m.width - 4).Render(inner)
	}
	if m.height > 0 {
		box = box.Height(max(m.height-4, 1))
	}
	b.WriteString(box.Render(inner))
	b.WriteString("\n")
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

func labeledField(label, value string) string {
	if value == "" {
		value = "-"
	}
	return dimStyle.Render(label+": ") + value
}

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
