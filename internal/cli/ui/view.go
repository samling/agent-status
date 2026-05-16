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
	dividerStyle     = lipgloss.NewStyle().Faint(true).Foreground(lipgloss.Color("240"))
	errorStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
	borderStyle      = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(0, 1)
	roleStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	lineSelectStyle  = lipgloss.NewStyle().Background(lipgloss.Color("240"))
	lineTextStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("252"))

	connectedStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("10"))
	disconnectedStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("9"))
)

func keyHint(key, desc string) string {
	return accentStyle.Render(key) + " " + dimStyle.Render(desc)
}

const (
	minLeftPane      = 24
	maxLeftPane      = 46
	paneGap          = 1
	paneDividerCols  = 1
	cardGapRows      = 1
	cardLeftPadding  = 1
	cardRightPadding = 1
	cardBodyLines    = 2
	childIndentCols  = 2
	roleLabelWidth   = 5
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

func cardAccentColor(status string, selected bool) lipgloss.Color {
	switch status {
	case "active":
		return lipgloss.Color("10")
	case "waiting":
		return lipgloss.Color("11")
	}
	if selected {
		return lipgloss.Color("12")
	}
	return lipgloss.Color("240")
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
	return cardBodyLines
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
	fillCards := m.focusMode == focusCards
	for _, card := range cards[start:end] {
		if len(lines) > 1 {
			for i := 0; i < cardGapRows; i++ {
				lines = append(lines, "")
			}
		}
		selected := card.SessionID == selectedID
		rendered := renderCard(card, cardWidth(width, card), selected, fillCards, m.detail, m.expandedParents[card.SessionID])
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

func renderCard(card sessionview.SessionCard, width int, selected, filled bool, _ sessionview.SessionDetail, expanded bool) string {
	if width < 10 {
		width = 10
	}
	railSelected := selected && !filled
	contentWidth := max(width-cardLeftPadding-cardRightPadding, 4)
	if railSelected {
		contentWidth = max(contentWidth-1, 4)
	}
	agent := cardAgent(card, expanded)
	status := card.Status
	if card.ChildStatus != "" {
		status = card.ChildStatus
	}
	fillSelected := filled && selected
	statusText := statusStyle(status).Render(status)
	if fillSelected {
		statusText = status
	}
	selectionMarker := ""
	if fillSelected {
		selectionMarker = "> "
	}
	agentWidth := max(contentWidth-lipgloss.Width(selectionMarker)-lipgloss.Width(statusText)-1, 1)
	agent = truncate(agent, agentWidth)
	top := selectionMarker + fmt.Sprintf("%-*s %s", agentWidth, agent, statusText)
	if fillSelected {
		return renderFilledSelectedCard(card, top, contentWidth, width)
	}
	title := compactCardLine(card.Title, cardAge(card), contentWidth)
	if railSelected {
		return renderRailSelectedCard(top, title, status)
	}
	pad := strings.Repeat(" ", cardLeftPadding)
	return strings.Join([]string{pad + top, pad + title}, "\n")
}

func renderFilledSelectedCard(card sessionview.SessionCard, top string, contentWidth, width int) string {
	fill := selectedCardFillColor(card)
	lines := []string{
		selectedPlainCardLine(top, width, fill),
		selectedTitleCardLine(card.Title, cardAge(card), contentWidth, width, fill),
	}
	return strings.Join(lines, "\n")
}

func selectedPlainCardLine(line string, width int, fill lipgloss.Color) string {
	visible := strings.Repeat(" ", cardLeftPadding) + line
	return selectedCardTextStyle(fill, false).Render(rightPad(visible, width))
}

func selectedTitleCardLine(title, subtitle string, contentWidth, width int, fill lipgloss.Color) string {
	titlePart, rest := plainCardLineParts(title, subtitle, contentWidth)
	pad := strings.Repeat(" ", cardLeftPadding)
	visible := pad + titlePart + rest
	rest += strings.Repeat(" ", max(width-len([]rune(visible)), 0))
	return selectedCardTextStyle(fill, false).Render(pad) +
		selectedCardTextStyle(fill, true).Render(titlePart) +
		selectedCardTextStyle(fill, false).Render(rest)
}

func renderRailSelectedCard(top, title, status string) string {
	return strings.Join([]string{
		railSelectedCardLine(top, status),
		railSelectedCardLine(title, status),
	}, "\n")
}

func railSelectedCardLine(line, status string) string {
	return statusStyle(status).Render("▌") + strings.Repeat(" ", cardLeftPadding) + line
}

func selectedCardTextStyle(fill lipgloss.Color, bold bool) lipgloss.Style {
	return lipgloss.NewStyle().
		Bold(bold).
		Foreground(lipgloss.Color("0")).
		Background(fill).
		ColorWhitespace(true)
}

func selectedCardFillColor(card sessionview.SessionCard) lipgloss.Color {
	return cardAccentColor(card.Status, true)
}

func cardAge(card sessionview.SessionCard) string {
	if card.Age == "-" {
		return ""
	}
	return card.Age
}

func cardAgent(card sessionview.SessionCard, expanded bool) string {
	agent := strings.ToLower(card.Agent)
	if card.ParentSessionID == "" && card.ChildCount > 0 {
		agent += " (" + strconv.Itoa(card.ChildCount) + " " + plural(card.ChildCount, "child", "children") + ")"
	}
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

func plainCardLineParts(title, subtitle string, width int) (string, string) {
	if subtitle == "" {
		return truncate(title, width), ""
	}
	subtitle = truncate(subtitle, max(width-5, 1))
	subtitleWidth := len([]rune(subtitle))
	titleWidth := width - subtitleWidth - 1
	if titleWidth < 4 {
		return "", leftPad(truncate(subtitle, width), width)
	}
	title = truncate(title, titleWidth)
	padding := strings.Repeat(" ", max(titleWidth-len([]rune(title)), 0))
	return title, padding + " " + subtitle
}

func leftPad(s string, width int) string {
	padding := width - len([]rune(s))
	if padding <= 0 {
		return s
	}
	return strings.Repeat(" ", padding) + s
}

func rightPad(s string, width int) string {
	padding := width - len([]rune(s))
	if padding <= 0 {
		return s
	}
	return s + strings.Repeat(" ", padding)
}

func renderSessionDetail(detail sessionview.SessionDetail, width int) string {
	return renderSessionDetailWithHeight(detail, width, 0)
}

func renderSessionDetailWithHeight(detail sessionview.SessionDetail, width, height int) string {
	if detail.SessionID == "" {
		return dimStyle.Render("select a session")
	}
	lines := renderDetailMetadata(detail, width)
	lines = append(lines, sectionDivider(width), headerStyle.Render("Conversation"))
	if detail.TranscriptError != "" {
		lines = append(lines, errorStyle.Render("transcript: "+detail.TranscriptError))
		return strings.Join(lines, "\n")
	}
	if len(detail.Conversation) == 0 {
		lines = append(lines, dimStyle.Render("(no conversation preview)"))
		return strings.Join(lines, "\n")
	}
	limit := len(detail.Conversation)
	if height > 0 {
		used := lipgloss.Height(strings.Join(lines, "\n"))
		limit = min(limit, max(height-used-1, 1))
	}
	for _, msg := range detail.Conversation[:limit] {
		lines = append(lines, renderConversationLine(msg, width))
	}
	return strings.Join(lines, "\n")
}

func (m uiModel) renderSessionDetailPreview(detail sessionview.SessionDetail, width, height int) string {
	if detail.SessionID == "" {
		return dimStyle.Render("select a session")
	}
	lines := renderDetailMetadata(detail, width)
	lines = append(lines, sectionDivider(width), headerStyle.Render("Conversation"))
	used := lipgloss.Height(strings.Join(lines, "\n"))
	bodyHeight := max(height-used-1, 1)
	lines = append(lines, m.renderMessagePreviewList(width, bodyHeight)...)
	return strings.Join(lines, "\n")
}

func (m uiModel) renderMessagePreviewList(width, height int) []string {
	if m.messageListFor != m.selectedID {
		return []string{dimStyle.Render("(loading messages)")}
	}
	if m.messageListErr != nil {
		return []string{errorStyle.Render("messages: " + m.messageListErr.Error())}
	}
	messages := m.previewMessages()
	if len(messages) == 0 {
		return []string{dimStyle.Render("(no messages)")}
	}
	limit := len(messages)
	if height > 0 {
		limit = min(limit, height)
	}
	lines := make([]string, 0, limit)
	for _, msg := range messages[:limit] {
		lines = append(lines, renderMessageSummaryLine(msg, width, false))
	}
	return lines
}

func renderDetailMetadata(detail sessionview.SessionDetail, width int) []string {
	lines := []string{
		headerStyle.Render("Metadata"),
	}
	lines = append(lines, renderMetadata(metadataFields(detail.Metadata), width)...)
	return lines
}

func (m uiModel) renderFocusedDetail(detail sessionview.SessionDetail, width, height int) string {
	if detail.SessionID == "" {
		return dimStyle.Render("select a session")
	}
	lines := renderDetailMetadata(detail, width)
	switch m.focusMode {
	case focusMessageBody:
		title := "Message"
		if m.messageRaw {
			title = "Message (raw)"
		}
		lines = append(lines, sectionDivider(width), headerStyle.Render(title))
		used := lipgloss.Height(strings.Join(lines, "\n"))
		lines = append(lines, m.renderMessageBody(width, max(height-used-1, 3))...)
	default:
		lines = append(lines, sectionDivider(width), headerStyle.Render("Conversation"))
		if m.messageSearchMode || m.messageQuery != "" {
			lines = append(lines, renderMessageSearch(m.messageQuery, m.messageSearchMode, width))
		}
		used := lipgloss.Height(strings.Join(lines, "\n"))
		lines = append(lines, m.renderMessageList(width, max(height-used-1, 1))...)
	}
	return strings.Join(lines, "\n")
}

func (m uiModel) renderMessageList(width, height int) []string {
	if m.messageListErr != nil {
		return []string{errorStyle.Render("messages: " + m.messageListErr.Error())}
	}
	messages := m.visibleMessages()
	if len(messages) == 0 {
		if m.messageQuery != "" {
			return []string{dimStyle.Render("(no matches)")}
		}
		if m.messageListFor != m.selectedID {
			return []string{dimStyle.Render("(loading messages)")}
		}
		return []string{dimStyle.Render("(no messages)")}
	}
	start, end := messageListWindow(len(messages), height, m.messageIndex)
	lines := make([]string, 0, end-start)
	for i, msg := range messages[start:end] {
		absoluteIndex := start + i
		lines = append(lines, renderMessageSummaryLine(msg, width, absoluteIndex == m.messageIndex))
	}
	return lines
}

func messageListWindow(total, height, selected int) (int, int) {
	if total <= 0 {
		return 0, 0
	}
	if height <= 0 || height > total {
		height = total
	}
	if selected < 0 {
		selected = 0
	}
	if selected >= total {
		selected = total - 1
	}
	start := 0
	if selected >= height {
		start = selected - height + 1
	}
	end := start + height
	if end > total {
		end = total
	}
	return start, end
}

func renderMessageSummaryLine(msg sessionview.MessageSummary, width int, selected bool) string {
	if width <= 0 {
		return ""
	}
	label := conversationLabel(msg.Role)
	labelText := fmt.Sprintf("%-*s", roleLabelWidth, label)
	previewWidth := max(width-roleLabelWidth-1, 0)
	preview := truncate(msg.Preview, previewWidth)
	if !selected {
		line := roleStyle.Render(labelText)
		if previewWidth > 0 {
			line += " " + preview
		}
		return truncate(line, width)
	}
	role := roleStyle.Background(lipgloss.Color("240")).Render(labelText)
	gap := lineSelectStyle.Render(" ")
	text := lineTextStyle.Background(lipgloss.Color("240")).Render(preview)
	fill := lineSelectStyle.Width(max(width-roleLabelWidth-1-lipgloss.Width(preview), 0)).Render("")
	return role + gap + text + fill
}

func renderMessageSearch(query string, active bool, width int) string {
	cursor := ""
	if active {
		cursor = "▏"
	}
	return truncate(dimStyle.Render("search: ")+query+cursor, width)
}

func (m uiModel) renderMessageBody(width, height int) []string {
	if m.messageDetailErr != nil {
		return []string{errorStyle.Render("message: " + m.messageDetailErr.Error())}
	}
	if m.messageDetail.ID == "" {
		return []string{dimStyle.Render("(loading message)")}
	}
	_, truncated := m.messageBodyText()
	lines := m.messageBodyLines(width)
	if len(lines) == 0 {
		lines = []string{""}
	}
	start := m.messageScroll
	if start < 0 {
		start = 0
	}
	if start >= len(lines) {
		start = max(len(lines)-1, 0)
	}
	end := min(start+max(height, 1), len(lines))
	out := make([]string, 0, end-start+1)
	for _, line := range lines[start:end] {
		out = append(out, truncate(line, width))
	}
	if truncated && end == len(lines) {
		out = append(out, dimStyle.Render("(truncated)"))
	}
	return out
}

func (m uiModel) messageBodyLines(width int) []string {
	text, _ := m.messageBodyText()
	return wrapMessageLines(text, width)
}

func (m uiModel) messageBodyText() (string, bool) {
	if m.messageRaw && m.messageDetail.RawText != "" {
		return m.messageDetail.RawText, m.messageDetail.RawTruncated
	}
	return m.messageDetail.Text, m.messageDetail.Truncated
}

func (m uiModel) messageBodyWidth() int {
	_, rightW := m.paneWidths()
	if rightW > 0 {
		return rightW
	}
	return m.width
}

func wrapMessageLines(text string, width int) []string {
	if width <= 0 {
		width = 1
	}
	lines := strings.Split(text, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		out = append(out, wrapMessageLine(line, width)...)
	}
	return out
}

func wrapMessageLine(line string, width int) []string {
	r := []rune(line)
	if len(r) == 0 {
		return []string{""}
	}
	out := make([]string, 0, len(r)/width+1)
	for len(r) > width {
		out = append(out, string(r[:width]))
		r = r[width:]
	}
	out = append(out, string(r))
	return out
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
	switch role {
	case "assistant":
		return "Agent"
	case "tool_call":
		return "Tool"
	case "tool_result":
		return "Result"
	default:
		return "User"
	}
}

func sectionDivider(width int) string {
	if width < 8 {
		width = 8
	}
	return dividerStyle.Render(strings.Repeat("─", width))
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
		{Label: "entrypoint", Value: valueOrDash(meta.Entrypoint)},
		{Label: "version", Value: valueOrDash(meta.Version)},
		{Label: "model", Value: valueOrDash(meta.Model)},
		{Label: "session", Value: valueOrDash(meta.Session)},
		{Label: "session id", Value: valueOrDash(meta.SessionID)},
		{Label: "created", Value: valueOrDash(meta.Created)},
		{Label: "updated", Value: valueOrDash(meta.Updated)},
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

func plural(n int, singular, plural string) string {
	if n == 1 {
		return singular
	}
	return plural
}

func sessionCounts(cards []sessionview.SessionCard) (parents, children int) {
	for _, card := range cards {
		if card.ParentSessionID == "" {
			parents++
		} else {
			children++
		}
	}
	return parents, children
}

func metadataGroupLen(fields []metadataItem) int {
	for _, group := range [][]string{
		{"agent", "entrypoint", "version", "model"},
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

func activePaneRail(height int, leftEdge bool) string {
	if height <= 0 {
		return ""
	}
	if height == 1 {
		return accentStyle.Render("│")
	}
	top, bottom := "╮", "╯"
	if leftEdge {
		top, bottom = "╭", "╰"
	}
	lines := make([]string, height)
	lines[0] = accentStyle.Render(top)
	for i := 1; i < height-1; i++ {
		lines[i] = accentStyle.Render("│")
	}
	lines[height-1] = accentStyle.Render(bottom)
	return strings.Join(lines, "\n")
}

func paneSeparator(height int, mode focusMode) string {
	if height <= 0 {
		return ""
	}
	spacer := blankColumn(height)
	switch mode {
	case focusMessages, focusMessageBody:
		rail := activePaneRail(height, true)
		return lipgloss.JoinHorizontal(lipgloss.Top, spacer, rail, spacer)
	default:
		rail := activePaneRail(height, false)
		return lipgloss.JoinHorizontal(lipgloss.Top, rail, spacer, spacer)
	}
}

func blankColumn(height int) string {
	lines := make([]string, height)
	for i := range lines {
		lines[i] = " "
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
	parents, children := sessionCounts(m.cards)
	summary := fmt.Sprintf(", %d %s", parents, plural(parents, "session", "sessions"))
	if children > 0 {
		summary += fmt.Sprintf(", %d %s", children, plural(children, "child", "children"))
	}
	head.WriteString(accentStyle.Render(summary))
	head.WriteString("\n\n")

	selectedID := m.selectedID
	if selectedID == "" && len(m.cards) > 0 {
		selectedID = m.cards[0].SessionID
	}

	if m.err != nil {
		head.WriteString(errorStyle.Render("error: " + m.err.Error()))
	} else {
		leftW, rightW := m.paneWidths()
		paneHeight := m.cardPaneHeight()
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
			if m.focusMode == focusMessages || m.focusMode == focusMessageBody {
				rightContent = m.renderFocusedDetail(rightDetail, rightW, paneHeight)
			} else if m.showExtraMessages {
				rightContent = m.renderSessionDetailPreview(rightDetail, rightW, paneHeight)
			} else {
				rightContent = renderSessionDetailWithHeight(rightDetail, rightW, paneHeight)
			}
		}
		right := lipgloss.NewStyle().Width(rightW).Render(rightContent)
		separator := paneSeparator(paneHeight, m.focusMode)
		head.WriteString(lipgloss.JoinHorizontal(
			lipgloss.Top,
			left,
			separator,
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
	} else if m.messageSearchMode {
		b.WriteString(dimStyle.Render("search: ") + m.messageQuery + "▏")
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
		switch m.focusMode {
		case focusMessages:
			toolHint := "show tools"
			if m.showExtraMessages {
				toolHint = "hide tools"
			}
			keymap = strings.Join([]string{
				keyHint("↑/↓", "message"),
				keyHint("ctrl-u/d", "page"),
				keyHint("/", "search"),
				keyHint("t", toolHint),
				keyHint("enter", "open"),
				keyHint("tab/shift-tab", "sessions"),
				keyHint("esc", "sessions"),
				keyHint("q", "quit"),
			}, "   ")
		case focusMessageBody:
			keymap = strings.Join([]string{
				keyHint("↑/↓", "scroll"),
				keyHint("ctrl-u/d", "page"),
				keyHint("t", "raw"),
				keyHint("tab/shift-tab", "sessions"),
				keyHint("esc", "messages"),
				keyHint("q", "quit"),
			}, "   ")
		default:
			toolHint := "show tools"
			if m.showExtraMessages {
				toolHint = "hide tools"
			}
			keymap = strings.Join([]string{
				keyHint("↑/↓", "select"),
				keyHint("space", "expand"),
				keyHint("enter", "focus"),
				keyHint("tab/shift-tab", "detail"),
				keyHint("t", toolHint),
				keyHint("n", "note"),
				keyHint("s", "sort:"+m.sort.String()),
				keyHint("?", "config"),
				keyHint("q", "quit"),
			}, "   ")
		}
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
