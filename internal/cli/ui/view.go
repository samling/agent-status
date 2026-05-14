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
	minLeftPane     = 24
	maxLeftPane     = 46
	paneGap         = 2
	cardPaddingCols = 2
	cardBodyLines   = 3
	cardBorderRows  = 2
	cardGapRows     = 1
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

func (m uiModel) cardPaneHeight() int {
	if m.height <= 0 {
		return 0
	}
	height := m.height - 6
	if height < 2 {
		return 2
	}
	return height
}

func (m uiModel) cardHeight(_ sessionview.SessionCard, _ string) int {
	return cardBodyLines + cardBorderRows
}

func (m uiModel) visibleCardRangeFrom(offset int, selectedID string) (int, int) {
	if len(m.cards) == 0 {
		return 0, 0
	}
	if offset < 0 {
		offset = 0
	}
	if offset >= len(m.cards) {
		offset = len(m.cards) - 1
	}
	height := m.cardPaneHeight()
	if height == 0 {
		return 0, len(m.cards)
	}
	budget := height - 1
	if budget < 1 {
		return offset, offset + 1
	}
	used := 0
	end := offset
	for end < len(m.cards) {
		nextHeight := m.cardHeight(m.cards[end], selectedID)
		if end > offset {
			nextHeight += cardGapRows
		}
		if end > offset && used+nextHeight > budget {
			break
		}
		used += nextHeight
		end++
	}
	if end == offset {
		end = offset + 1
	}
	return offset, end
}

func (m uiModel) renderCards(width int, selectedID string) string {
	title := "Sessions"
	if len(m.cards) > 0 {
		if idx := cardIndex(m.cards, selectedID); idx >= 0 {
			title = fmt.Sprintf("Sessions %d/%d", idx+1, len(m.cards))
		} else {
			title = fmt.Sprintf("Sessions %d", len(m.cards))
		}
	}
	lines := []string{headerStyle.Render(title)}
	if len(m.cards) == 0 {
		lines = append(lines, dimStyle.Render("(no live sessions)"))
		return strings.Join(lines, "\n")
	}
	start, end := m.visibleCardRangeFrom(m.scrollOffset, selectedID)
	for i, card := range m.cards[start:end] {
		if i > 0 {
			lines = append(lines, "")
		}
		selected := card.SessionID == selectedID
		lines = append(lines, renderCard(card, width, selected, m.detailFor == card.SessionID, m.detail))
	}
	return strings.Join(lines, "\n")
}

func renderCard(card sessionview.SessionCard, width int, selected, _ bool, _ sessionview.SessionDetail) string {
	if width < 10 {
		width = 10
	}
	boxWidth := width - 2
	if boxWidth < 8 {
		boxWidth = 8
	}
	contentWidth := boxWidth - cardPaddingCols
	if contentWidth < 4 {
		contentWidth = 4
	}
	agent := strings.ToUpper(card.Agent)
	status := card.Status
	agentWidth := max(contentWidth-len(status)-1, 1)
	agent = truncate(agent, agentWidth)
	title := truncate(card.Title, contentWidth)
	subtitle := truncate(card.Subtitle, contentWidth)
	top := fmt.Sprintf("%-*s %s", agentWidth, agent, status)
	parts := []string{top, title, subtitle}
	style := rowStyle(card.Status, selected).
		Width(boxWidth).
		Padding(0, 1).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("240"))
	if selected {
		style = style.BorderForeground(lipgloss.Color("12"))
	}
	return style.Render(strings.Join(parts, "\n"))
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
