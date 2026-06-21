# Session List Sections Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Split the terminal session list into lightweight `Active Sessions` and `Idle Sessions` sections while preserving the existing cards, selection, sorting, and parent/child behavior.

**Architecture:** Keep `visibleCards()` and `sortCards()` as the source of selectable card order. Add a small presentation helper in `internal/cli/ui/view.go` that classifies the visible card slice into rendered sections, then make `renderCards()` and visible range height accounting use those sections. Section headings are display-only and never participate in selection movement.

**Tech Stack:** Go, Bubble Tea UI model, Charmbracelet Lip Gloss styling, existing `go test ./internal/cli/ui` tests.

---

## File Structure

- Modify `internal/cli/ui/view.go`: add section metadata and render section headings in the session pane.
- Modify `internal/cli/ui/view_test.go`: add focused render tests for section headings and ensure child expansion remains adjacent to parents.
- Leave `internal/cli/ui/sort.go` unchanged: sorting already ranks active/waiting before idle, and sectioning is a presentation concern.
- Leave `internal/cli/ui/actions.go` unchanged: selection still uses `visibleCards()` so headings remain non-selectable.

### Task 1: Add Section Rendering Tests

**Files:**
- Modify: `internal/cli/ui/view_test.go`

- [ ] **Step 1: Write failing tests for section headings**

Append these tests near the existing `renderCards` tests in `internal/cli/ui/view_test.go`, after `TestRenderCardsUsesCompactRows`:

```go
func TestRenderCardsShowsActiveAndIdleSections(t *testing.T) {
	m := uiModel{
		width:      90,
		height:     24,
		selectedID: "active",
		cards: []sessionview.SessionCard{
			{SessionID: "active", Agent: "codex", Status: "active", Title: "active", Age: "1h"},
			{SessionID: "waiting", Agent: "codex", Status: "waiting", Title: "waiting"},
			{SessionID: "idle", Agent: "codex", Status: "idle", Title: "idle"},
		},
	}

	out := m.renderCards(36, "active")
	if !strings.Contains(out, "Active Sessions") {
		t.Fatalf("renderCards() missing active section heading; output:\n%s", out)
	}
	if !strings.Contains(out, "Idle Sessions") {
		t.Fatalf("renderCards() missing idle section heading; output:\n%s", out)
	}
	if strings.Index(out, "Active Sessions") > strings.Index(out, "active") {
		t.Fatalf("active heading should appear before active cards; output:\n%s", out)
	}
	if strings.Index(out, "Idle Sessions") > strings.Index(out, "idle") {
		t.Fatalf("idle heading should appear before idle cards; output:\n%s", out)
	}
	if strings.Index(out, "idle") < strings.Index(out, "Idle Sessions") {
		t.Fatalf("idle card should appear under idle section; output:\n%s", out)
	}
}

func TestRenderCardsOmitsEmptySectionHeadings(t *testing.T) {
	m := uiModel{
		width:      90,
		height:     24,
		selectedID: "active",
		cards: []sessionview.SessionCard{
			{SessionID: "active", Agent: "codex", Status: "active", Title: "active"},
		},
	}

	out := m.renderCards(36, "active")
	if !strings.Contains(out, "Active Sessions") {
		t.Fatalf("renderCards() missing active section heading; output:\n%s", out)
	}
	if strings.Contains(out, "Idle Sessions") {
		t.Fatalf("renderCards() should omit empty idle section; output:\n%s", out)
	}

	m.cards = []sessionview.SessionCard{
		{SessionID: "idle", Agent: "codex", Status: "idle", Title: "idle"},
	}
	m.selectedID = "idle"
	out = m.renderCards(36, "idle")
	if strings.Contains(out, "Active Sessions") {
		t.Fatalf("renderCards() should omit empty active section; output:\n%s", out)
	}
	if !strings.Contains(out, "Idle Sessions") {
		t.Fatalf("renderCards() missing idle section heading; output:\n%s", out)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/cli/ui -run 'TestRenderCards(ShowsActiveAndIdleSections|OmitsEmptySectionHeadings)' -count=1`

Expected: FAIL because `renderCards()` does not render `Active Sessions` or `Idle Sessions` yet.

### Task 2: Implement Minimal Section Rendering

**Files:**
- Modify: `internal/cli/ui/view.go:127-200`

- [ ] **Step 1: Add section data helpers**

In `internal/cli/ui/view.go`, add these declarations after `cardHeight`:

```go
type cardSection struct {
	Title string
	Cards []sessionview.SessionCard
}

func cardSections(cards []sessionview.SessionCard) []cardSection {
	if len(cards) == 0 {
		return nil
	}
	sections := make([]cardSection, 0, 2)
	active := cardSection{Title: "Active Sessions"}
	idle := cardSection{Title: "Idle Sessions"}
	parentSection := ""
	for _, card := range cards {
		sectionStatus := cardSectionStatus(card, parentSection)
		if card.ParentSessionID == "" {
			parentSection = sectionStatus
		}
		if sectionStatus == "idle" {
			idle.Cards = append(idle.Cards, card)
			continue
		}
		active.Cards = append(active.Cards, card)
	}
	if len(active.Cards) > 0 {
		sections = append(sections, active)
	}
	if len(idle.Cards) > 0 {
		sections = append(sections, idle)
	}
	return sections
}

func cardSectionStatus(card sessionview.SessionCard, parentSection string) string {
	if card.ParentSessionID != "" {
		return parentSection
	}
	if card.ChildStatus != "" {
		return card.ChildStatus
	}
	return card.Status
}
```

- [ ] **Step 2: Update `renderCards()` to render headings**

Replace the loop in `renderCards()` with this section-aware loop:

```go
	visible := cards[start:end]
	fillCards := m.focusMode == focusCards
	for _, section := range cardSections(visible) {
		if len(lines) > 1 {
			lines = append(lines, "")
		}
		lines = append(lines, headerStyle.Render(section.Title))
		for _, card := range section.Cards {
			lines = append(lines, "")
			selected := card.SessionID == selectedID
			rendered := renderCard(card, cardWidth(width, card), selected, fillCards, m.detail, m.expandedParents[card.SessionID])
			if card.ParentSessionID != "" {
				rendered = indentBlock(rendered, childIndentCols)
			}
			lines = append(lines, rendered)
		}
	}
```

The complete `renderCards()` body should still keep the existing title, empty-state behavior, range calculation, selected fill, and card rendering calls.

- [ ] **Step 3: Run focused tests**

Run: `go test ./internal/cli/ui -run 'TestRenderCards(ShowsActiveAndIdleSections|OmitsEmptySectionHeadings|UsesCompactRows)' -count=1`

Expected: the new section tests PASS. `TestRenderCardsUsesCompactRows` may fail because headings intentionally add height; update that test in Task 3.

### Task 3: Preserve Parent/Child Adjacency And Update Existing Expectations

**Files:**
- Modify: `internal/cli/ui/view.go`
- Modify: `internal/cli/ui/view_test.go`

- [ ] **Step 1: Add a parent/child adjacency test**

Append this test after `TestRenderCardsExpandsChildren` in `internal/cli/ui/view_test.go`:

```go
func TestRenderCardsKeepsExpandedChildrenWithParentSection(t *testing.T) {
	m := uiModel{
		width:           90,
		height:          24,
		expandedParents: map[string]bool{"parent": true},
		cards: []sessionview.SessionCard{
			{SessionID: "parent", Agent: "codex", Status: "active", Title: "parent", ChildCount: 1},
			{SessionID: "child", ParentSessionID: "parent", Agent: "codex", Status: "idle", Title: "child"},
			{SessionID: "idle", Agent: "codex", Status: "idle", Title: "idle"},
		},
		selectedID: "parent",
	}

	out := m.renderCards(36, "parent")
	parentIndex := strings.Index(out, "parent")
	childIndex := strings.Index(out, "child")
	idleHeadingIndex := strings.Index(out, "Idle Sessions")
	if parentIndex < 0 || childIndex < 0 || idleHeadingIndex < 0 {
		t.Fatalf("renderCards() missing expected content; output:\n%s", out)
	}
	if !(parentIndex < childIndex && childIndex < idleHeadingIndex) {
		t.Fatalf("expanded child should stay with active parent before idle section; output:\n%s", out)
	}
}

func TestRenderCardsKeepsExpandedChildrenWithIdleParentSection(t *testing.T) {
	m := uiModel{
		width:           90,
		height:          24,
		expandedParents: map[string]bool{"parent": true},
		cards: []sessionview.SessionCard{
			{SessionID: "active", Agent: "codex", Status: "active", Title: "active"},
			{SessionID: "parent", Agent: "codex", Status: "idle", Title: "parent", ChildCount: 1},
			{SessionID: "child", ParentSessionID: "parent", Agent: "codex", Status: "active", Title: "child"},
		},
		selectedID: "active",
	}

	out := m.renderCards(36, "active")
	idleHeadingIndex := strings.Index(out, "Idle Sessions")
	parentIndex := strings.Index(out, "parent")
	childIndex := strings.Index(out, "child")
	if idleHeadingIndex < 0 || parentIndex < 0 || childIndex < 0 {
		t.Fatalf("renderCards() missing expected content; output:\n%s", out)
	}
	if !(idleHeadingIndex < parentIndex && parentIndex < childIndex) {
		t.Fatalf("expanded child should stay with idle parent under idle section; output:\n%s", out)
	}
}
```

- [ ] **Step 2: Run the new parent/child tests and verify they fail if children are split**

Run: `go test ./internal/cli/ui -run 'TestRenderCardsKeepsExpandedChildrenWith(Parent|IdleParent)Section' -count=1`

Expected before the child classification fix: FAIL if expanded children are grouped by their own status instead of staying with their parent.

- [ ] **Step 3: Ensure children classify with their parent rendering position**

If the test fails, update `cardSections()` and `cardSectionStatus()` in `internal/cli/ui/view.go` so child cards return the current parent section, as shown in Task 2. This keeps expanded children adjacent to parents and avoids cross-section separation.

```go
func cardSectionStatus(card sessionview.SessionCard, parentSection string) string {
	if card.ParentSessionID != "" {
		return parentSection
	}
	if card.ChildStatus != "" {
		return card.ChildStatus
	}
	return card.Status
}
```

- [ ] **Step 4: Update compact height expectation**

In `TestRenderCardsUsesCompactRows`, change the expected height from `6` to `7` because one `Active Sessions` heading is now rendered above the two active/waiting cards:

```go
	if lipgloss.Height(out) != 7 {
		t.Fatalf("renderCards() height = %d, want 7; output:\n%s", lipgloss.Height(out), out)
	}
```

- [ ] **Step 5: Run focused tests**

Run: `go test ./internal/cli/ui -run 'TestRenderCards(ShowsActiveAndIdleSections|OmitsEmptySectionHeadings|KeepsExpandedChildrenWith(Parent|IdleParent)Section|UsesCompactRows|CollapsesChildrenByDefault|ExpandsChildren|IndentsExpandedChildren)' -count=1`

Expected: PASS.

### Task 4: Adjust Scroll Height Accounting

**Files:**
- Modify: `internal/cli/ui/view.go`

- [ ] **Step 1: Write a failing test for scrolling with section headings**

Append this test near the other `renderCards` tests in `internal/cli/ui/view_test.go`:

```go
func TestRenderCardsScrollBudgetIncludesSectionHeadings(t *testing.T) {
	m := uiModel{
		width:      90,
		height:     12,
		selectedID: "active",
		cards: []sessionview.SessionCard{
			{SessionID: "active", Agent: "codex", Status: "active", Title: "active"},
			{SessionID: "idle", Agent: "codex", Status: "idle", Title: "idle"},
		},
	}

	out := m.renderCards(36, "active")
	if lipgloss.Height(out) > m.cardPaneHeight() {
		t.Fatalf("renderCards() height = %d, want <= %d; output:\n%s", lipgloss.Height(out), m.cardPaneHeight(), out)
	}
}
```

- [ ] **Step 2: Run the scroll test to verify it fails**

Run: `go test ./internal/cli/ui -run TestRenderCardsScrollBudgetIncludesSectionHeadings -count=1`

Expected: FAIL if `visibleCardRangeFrom()` only budgets for card bodies and blank rows.

- [ ] **Step 3: Implement section-aware rendered height calculation**

Add this helper in `internal/cli/ui/view.go` near `visibleCardRangeFrom`:

```go
func (m uiModel) renderedCardListHeight(cards []sessionview.SessionCard, selectedID string) int {
	height := 0
	for _, section := range cardSections(cards) {
		if height > 0 {
			height += cardGapRows
		}
		height++
		for _, card := range section.Cards {
			height += cardGapRows
			height += m.cardHeight(card, selectedID)
		}
	}
	return height
}
```

Then update the loop inside `visibleCardRangeFrom()` to test the rendered height of the candidate slice:

```go
	end := offset
	for end < len(cards) {
		nextEnd := end + 1
		if end > offset && m.renderedCardListHeight(cards[offset:nextEnd], selectedID) > budget {
			break
		}
		end = nextEnd
	}
```

Keep the existing `if end == offset { end = offset + 1 }` guard.

- [ ] **Step 4: Run focused scroll and rendering tests**

Run: `go test ./internal/cli/ui -run 'TestRenderCards(ScrollBudgetIncludesSectionHeadings|ShowsActiveAndIdleSections|OmitsEmptySectionHeadings|KeepsExpandedChildrenWith(Parent|IdleParent)Section)' -count=1`

Expected: PASS.

### Task 5: Full Verification

**Files:**
- No code changes unless tests expose a regression.

- [ ] **Step 1: Run all UI package tests**

Run: `go test ./internal/cli/ui -count=1`

Expected: PASS.

- [ ] **Step 2: Run all repository tests**

Run: `go test ./... -count=1`

Expected: PASS.

- [ ] **Step 3: Review diff**

Run: `git diff -- internal/cli/ui/view.go internal/cli/ui/view_test.go`

Expected: diff only includes section rendering helpers, render/range updates, and focused tests.

- [ ] **Step 4: Commit implementation**

Run:

```bash
git add internal/cli/ui/view.go internal/cli/ui/view_test.go
git commit -m "feat(ui): split session list into sections"
```

Expected: one commit containing the implementation and tests.
