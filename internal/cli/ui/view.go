package ui

import (
	"fmt"
	"strconv"
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

type metadataItem struct {
	Label string
	Value string
}

func statusStyle(status string) lipgloss.Style {
	switch status {
	case "active":
		return lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("10"))
	case "waiting":
		return lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("11"))
	case "idle":
		return lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	default:
		return dimStyle
	}
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
	statusText := statusStyle(status).Render(status)
	selectionMarker := ""
	if selected {
		selectionMarker = accentStyle.Render(">") + " "
	}
	agentWidth := max(contentWidth-lipgloss.Width(selectionMarker)-lipgloss.Width(statusText)-1, 1)
	agent = truncate(agent, agentWidth)
	title := compactCardLine(card.Title, "", contentWidth)
	top := selectionMarker + fmt.Sprintf("%-*s %s", agentWidth, agent, statusText)
	parts := []string{top, title}
	style := lipgloss.NewStyle().
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
		headerStyle.Render("Metadata"),
	}
	lines = append(lines, renderMetadata(metadataFields(detail.Metadata), width)...)
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

func renderMetadata(fields []metadataItem, width int) []string {
	if len(fields) == 0 {
		return []string{dimStyle.Render("(no metadata)")}
	}
	lines := make([]string, 0, len(fields))
	for i := 0; i < len(fields); {
		if groupLen := metadataGroupLen(fields[i:]); groupLen > 0 {
			lines = append(lines, renderMetadataGroup(fields[i:i+groupLen], width)...)
			i += groupLen
			continue
		}
		line := metadataField(fields[i], width)
		i++
		for i < len(fields) && metadataGroupLen(fields[i:]) == 0 {
			next := metadataField(fields[i], width)
			candidate := line + "   " + next
			if lipgloss.Width(candidate) > width {
				break
			}
			line = candidate
			i++
		}
		lines = append(lines, line)
	}
	return lines
}

func metadataFields(meta sessionview.DetailMetadata) []metadataItem {
	fields := []metadataItem{
		{Label: "agent", Value: valueOrDash(meta.Agent)},
		{Label: "version", Value: valueOrDash(meta.Version)},
		{Label: "model", Value: valueOrDash(meta.Model)},
		{Label: "session", Value: valueOrDash(meta.Session)},
		{Label: "session id", Value: valueOrDash(meta.SessionID)},
		{Label: "cwd", Value: valueOrDash(meta.Cwd)},
		{Label: "branch", Value: valueOrDash(meta.Branch)},
	}
	if hasTokenStats(meta) {
		fields = append(fields,
			metadataItem{Label: "input tokens", Value: formatCompactCount(meta.InputTokens)},
			metadataItem{Label: "output tokens", Value: formatCompactCount(meta.OutputTokens)},
			metadataItem{Label: "cache create", Value: formatCompactCount(meta.CacheCreationTokens)},
			metadataItem{Label: "cache read", Value: formatCompactCount(meta.CacheReadTokens)},
		)
	}
	if hasMessageStats(meta) {
		fields = append(fields,
			metadataItem{Label: "user msgs", Value: formatMessageCount(meta.UserMessages)},
			metadataItem{Label: "agent msgs", Value: formatMessageCount(meta.AgentMessages)},
		)
	}
	fields = append(fields,
		metadataItem{Label: "pid", Value: pidValue(meta.PID)},
		metadataItem{Label: "parent", Value: valueOrDash(meta.ParentSessionID)},
		metadataItem{Label: "children", Value: childCountValue(meta.ChildCount, meta.OpenChildCount)},
		metadataItem{Label: "last event", Value: valueOrDash(meta.LastEvent)},
		metadataItem{Label: "waiting", Value: valueOrDash(meta.Waiting)},
		metadataItem{Label: "note", Value: valueOrDash(meta.Note)},
	)
	return fields
}

func hasTokenStats(meta sessionview.DetailMetadata) bool {
	return meta.InputTokens > 0 ||
		meta.OutputTokens > 0 ||
		meta.CacheCreationTokens > 0 ||
		meta.CacheReadTokens > 0
}

func hasMessageStats(meta sessionview.DetailMetadata) bool {
	return meta.UserMessages > 0 || meta.AgentMessages > 0
}

func pidValue(pid int) string {
	if pid <= 0 {
		return "-"
	}
	return strconv.Itoa(pid)
}

func formatCompactCount(n int64) string {
	if n <= 0 {
		return "-"
	}
	return formatPositiveCompactCount(n)
}

func formatMessageCount(n int) string {
	if n <= 0 {
		return "0"
	}
	return formatPositiveCompactCount(int64(n))
}

func formatPositiveCompactCount(n int64) string {
	units := []struct {
		value  int64
		suffix string
	}{
		{1_000_000_000, "B"},
		{1_000_000, "M"},
		{1_000, "k"},
	}
	for _, unit := range units {
		if n >= unit.value {
			whole := n / unit.value
			decimal := (n % unit.value) * 10 / unit.value
			if whole >= 100 || decimal == 0 {
				return strconv.FormatInt(whole, 10) + unit.suffix
			}
			return strconv.FormatInt(whole, 10) + "." + strconv.FormatInt(decimal, 10) + unit.suffix
		}
	}
	return strconv.FormatInt(n, 10)
}

func childCountValue(total, open int) string {
	if total <= 0 {
		return "-"
	}
	if open > 0 {
		return strconv.Itoa(total) + " (" + strconv.Itoa(open) + " open)"
	}
	return strconv.Itoa(total)
}

func valueOrDash(v string) string {
	if v == "" {
		return "-"
	}
	return v
}

func metadataGroupLen(fields []metadataItem) int {
	for _, group := range [][]string{
		{"agent", "version", "model"},
		{"cwd", "branch"},
		{"pid", "parent", "children"},
	} {
		if len(fields) < len(group) {
			continue
		}
		matched := true
		for i, label := range group {
			if fields[i].Label != label {
				matched = false
				break
			}
		}
		if matched {
			return len(group)
		}
	}
	return 0
}

func renderMetadataGroup(fields []metadataItem, width int) []string {
	lines := []string{metadataField(fields[0], width)}
	for _, field := range fields[1:] {
		next := metadataField(field, width)
		candidate := lines[len(lines)-1] + "   " + next
		if lipgloss.Width(candidate) <= width {
			lines[len(lines)-1] = candidate
			continue
		}
		lines = append(lines, next)
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

func metadataField(field metadataItem, width int) string {
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
