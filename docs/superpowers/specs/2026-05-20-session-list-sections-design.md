# Session List Sections Design

## Context

The session list currently sorts active and waiting sessions above idle sessions, but each card still shows its age. In practice this can look inconsistent: an active session with an old age such as `1h` appears at the top because activity status outranks age.

## Decision

Split the session list into lightweight sections using headings only:

- `Active Sessions` for sessions whose effective status is not `idle`.
- `Idle Sessions` for sessions whose effective status is `idle`.

Do not add a divider rule or counts. The headings are enough to explain the grouping while keeping the terminal pane compact.

## Rendering

The main title remains `Sessions`, including the existing selected-position/count text when cards exist. Below it, render the visible cards in section order:

1. Active sessions section, if at least one active or waiting card is visible.
2. Idle sessions section, if at least one idle card is visible.

Cards keep their existing compact two-line rendering, status rail, selection fill, age display, and parent/child indentation.

## Sorting And Selection

Keep the existing card sort modes. Sectioning is a presentation layer over the already-sorted visible card list, not a replacement for sorting.

Selection and scrolling should continue to operate over the same ordered card sequence as before. Section headings add rendered height but are not selectable cards.

## Parent And Child Sessions

Parent/child expansion behavior remains unchanged. Children stay adjacent to their parent when expanded and inherit the section placement from their rendered position in the visible list. This avoids separating children from parents across section boundaries.

## Testing

Add focused UI rendering tests for:

- Rendering `Active Sessions` and `Idle Sessions` headings when both groups exist.
- Omitting a section heading when that group is empty.
- Preserving selectable cards, compact card spacing, and parent/child expansion behavior.
