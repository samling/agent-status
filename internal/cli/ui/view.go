package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/samling/agent-status/internal/sessionview"
	"github.com/samling/agent-status/internal/version"
)

var (
	accentStyle      = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12"))
	sessionNameStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("14"))
	headerStyle      = lipgloss.NewStyle().Bold(true)
	dimStyle         = lipgloss.NewStyle().Faint(true)
	errorStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
	borderStyle      = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(0, 1)
	roleStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))

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
	paneGap         = 1
	paneDividerCols = 1
	cardPaddingCols = 2
	cardBodyLines   = 2
	cardBorderRows  = 2
	childIndentCols = 2
	roleLabelWidth  = 5
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

func cardBorderColor(status string, selected bool) lipgloss.Color {
	if selected {
		return lipgloss.Color("12")
	}
	switch status {
	case "active":
		return lipgloss.Color("10")
	case "waiting":
		return lipgloss.Color("11")
	default:
		return lipgloss.Color("240")
	}
}

func (m uiModel) paneWidths() (int, int) {
	inner := m.width - 4
	separatorWidth := paneGap*2 + paneDividerCols
	if inner < 50 {
		left := minLeftPane
		right := inner - left - separatorWidth
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
	right := inner - left - separatorWidth
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
	cards := m.visibleCards()
	if len(cards) == 0 {
		return 0, 0
	}
	if offset < 0 {
		offset = 0
	}
	if offset >= len(cards) {
		offset = len(cards) - 1
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
	for end < len(cards) {
		nextHeight := m.cardHeight(cards[end], selectedID)
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
	cards := m.visibleCards()
	title := "Sessions"
	if len(cards) > 0 {
		if idx := cardIndex(cards, selectedID); idx >= 0 {
			title = fmt.Sprintf("Sessions %d/%d", idx+1, len(cards))
		} else {
			title = fmt.Sprintf("Sessions %d", len(cards))
		}
	}
	lines := []string{headerStyle.Render(title)}
	if len(cards) == 0 {
		lines = append(lines, dimStyle.Render("(no live sessions)"))
		return strings.Join(lines, "\n")
	}
	start, end := m.visibleCardRangeFrom(m.scrollOffset, selectedID)
	for _, card := range cards[start:end] {
		selected := card.SessionID == selectedID
		rendered := renderCard(card, cardWidth(width, card), selected, m.detailFor == card.SessionID, m.detail, m.expandedParents[card.SessionID])
		if card.ParentSessionID != "" {
			rendered = indentBlock(rendered, childIndentCols)
		}
		lines = append(lines, rendered)
	}
	return strings.Join(lines, "\n")
}

func cardWidth(width int, card sessionview.SessionCard) int {
	if card.ParentSessionID == "" {
		return width
	}
	return width - childIndentCols
}

func indentBlock(text string, cols int) string {
	if cols <= 0 || text == "" {
		return text
	}
	prefix := strings.Repeat(" ", cols)
	lines := strings.Split(text, "\n")
	for i := range lines {
		lines[i] = prefix + lines[i]
	}
	return strings.Join(lines, "\n")
}

func renderCard(card sessionview.SessionCard, width int, selected, _ bool, _ sessionview.SessionDetail, expanded bool) string {
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
	agent := cardAgent(card, expanded)
	status := card.Status
	if card.ChildStatus != "" {
		status = card.ChildStatus
	}
	agentWidth := max(contentWidth-len(status)-1, 1)
	agent = truncate(agent, agentWidth)
	title := compactCardLine(card.Title, cardSubtitle(card), contentWidth)
	top := fmt.Sprintf("%-*s %s", agentWidth, agent, status)
	parts := []string{top, title}
	style := rowStyle(card.Status, false).
		Width(boxWidth).
		Padding(0, 1).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(cardBorderColor(card.Status, selected))
	return style.Render(strings.Join(parts, "\n"))
}

func cardAgent(card sessionview.SessionCard, expanded bool) string {
	agent := strings.ToLower(card.Agent)
	if marker := cardMarker(card, expanded); marker != "" {
		return marker + " " + agent
	}
	return agent
}

func cardMarker(card sessionview.SessionCard, expanded bool) string {
	switch {
	case card.ParentSessionID != "":
		return ""
	case card.ChildCount > 0 && expanded:
		return "-"
	case card.ChildCount > 0:
		return "+"
	default:
		return ""
	}
}

func cardSubtitle(card sessionview.SessionCard) string {
	if card.Subtitle == "-" {
		return ""
	}
	return card.Subtitle
}

func compactCardLine(title, subtitle string, width int) string {
	if subtitle == "" {
		return sessionNameStyle.Render(truncate(title, width))
	}
	subtitle = truncate(subtitle, max(width-5, 1))
	subtitleWidth := len([]rune(subtitle))
	titleWidth := width - subtitleWidth - 1
	if titleWidth < 4 {
		return leftPad(truncate(subtitle, width), width)
	}
	title = truncate(title, titleWidth)
	padding := strings.Repeat(" ", max(titleWidth-len([]rune(title)), 0))
	return sessionNameStyle.Render(title) + padding + " " + subtitle
}

func leftPad(s string, width int) string {
	padding := width - len([]rune(s))
	if padding <= 0 {
		return s
	}
	return strings.Repeat(" ", padding) + s
}

func renderSessionDetail(detail sessionview.SessionDetail, width int) string {
	if detail.SessionID == "" {
		return dimStyle.Render("select a session")
	}
	lines := []string{
		sessionNameStyle.Render(detail.Title),
		rowStyle(detail.Status, false).Render(detail.Agent + " " + detail.Status),
		sectionDivider(width),
		headerStyle.Render("Metadata"),
	}
	lines = append(lines, renderMetadata(detail.Metadata, width)...)
	lines = append(lines, sectionDivider(width), headerStyle.Render("Conversation"))
	if detail.TranscriptError != "" {
		lines = append(lines, errorStyle.Render("transcript: "+detail.TranscriptError))
		return strings.Join(lines, "\n")
	}
	if len(detail.Conversation) == 0 {
		lines = append(lines, dimStyle.Render("(no conversation preview)"))
		return strings.Join(lines, "\n")
	}
	for _, msg := range detail.Conversation {
		lines = append(lines, renderConversationLine(msg, width))
	}
	return strings.Join(lines, "\n")
}

func renderConversationLine(msg sessionview.ConversationMessage, width int) string {
	if width <= 0 {
		return ""
	}
	label := conversationLabel(msg.Role)
	labelText := fmt.Sprintf("%-*s", roleLabelWidth, label)
	labelWidth := lipgloss.Width(labelText)
	if labelWidth >= width {
		return roleStyle.Render(truncate(labelText, width))
	}
	return roleStyle.Render(labelText) + " " + truncate(msg.Text, width-labelWidth-1)
}

func conversationLabel(role string) string {
	if role == "assistant" {
		return "Agent"
	}
	return "User"
}

func sectionDivider(width int) string {
	if width < 8 {
		width = 8
	}
	return dimStyle.Render(strings.Repeat("─", width))
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

func verticalDivider(height int) string {
	if height <= 0 {
		return ""
	}
	lines := make([]string, height)
	for i := range lines {
		lines[i] = dimStyle.Render("│")
	}
	return strings.Join(lines, "\n")
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
		divider := verticalDivider(m.cardPaneHeight())
		head.WriteString(lipgloss.JoinHorizontal(
			lipgloss.Top,
			left,
			strings.Repeat(" ", paneGap),
			divider,
			strings.Repeat(" ", paneGap),
			right,
		))
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
			keyHint("space", "expand"),
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
