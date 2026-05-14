# Dual-Pane Session View Design

## Summary

`agent-status ui` will move from a wide single-table layout with a bottom detail block to a two-pane layout:

- Left pane: compact session cards for scanning and selection.
- Right pane: selected-session detail with metadata at the top and newest-first conversation previews below.

The implementation will add a server-side presentation model so the daemon remains the source of truth and the TUI renders display-ready data instead of reassembling state, metadata, transcripts, and notes locally.

## Goals

- Make the UI useful in smaller tmux panes.
- Keep the session kind visible at a glance on every session card.
- Preserve `enter` to focus the selected session as a primary workflow.
- Show a lightweight conversation preview with both user and assistant messages.
- Avoid copying lazyagent's presentation directly while borrowing the useful dual-pane information architecture.

## Non-Goals

- No full chat transcript viewer in this slice.
- No mouse support requirement.
- No full provider architecture refactor.
- No change to focus backends or daemon lifecycle.
- No removal of existing low-level `/state`, `/meta`, or `/transcript` endpoints.

## Architecture

Add a server-side presentation layer, likely `internal/sessionview`, that builds a richer view from existing sources:

- `state.Session`
- discovery `source.SessionMeta`
- transcript summaries
- notes
- derived status and formatting helpers

The daemon exposes this view through new read endpoints, for example:

- `GET /views/sessions`
- `GET /views/sessions/{session_id}`

The `internal/client` package gains matching methods. The TUI consumes these methods and no longer has to merge `/state`, `/meta`, and `/transcript` directly for the main render path.

Focus continues through `client.Focus`, so all existing focus behavior stays intact.

## Data Model

### SessionCard

The left pane uses compact cards with fields such as:

- `SessionID`
- `Agent`
- `Status`
- `Title` or short project path
- `Subtitle` for waiting/tool/activity hint
- `ActivityTime`
- `Preview` for the selected session's extra one-line conversation hint

Cards are compact by default. The selected card may render one extra preview line.

### SessionDetail

The right pane contains:

- `SessionID`
- `Agent`
- `Status`
- `Metadata []Field`
- `Conversation []ConversationMessage`
- optional parse error text for transcript failures

Metadata appears at the top in a compact horizontal block. Conversation appears below, newest first.

### ConversationMessage

Conversation messages are capped previews:

- `Role`: user or assistant
- `Text`: one collapsed single-line preview
- `Timestamp`

The transcript parsers should retain only a small recent window, such as the last 8 to 12 user and assistant messages.

## Data Flow

On refresh, the TUI asks the daemon for session cards. The daemon uses the store and current discovery metadata to build cards cheaply.

For the selected session, the TUI asks for one detail view. The daemon loads the transcript summary for only that session and uses the existing stat-based transcript cache to avoid repeated parsing work.

If the selected session disappears, the TUI falls back to the first visible session.

## UI Layout

The TUI renders:

- Header with `agent-status`, version, connectivity, and session count.
- Left pane with compact session cards.
- Right pane with selected session detail.
- Footer with status message and keymap.

Right pane layout:

1. Compact metadata section at the top.
2. Newest-first conversation preview below.

Left pane session cards:

- Agent kind is prominent, for example `CLAUDE-CODE` or `CODEX`.
- Status uses color and concise text.
- Project/cwd is visible but truncated.
- Waiting/tool/activity hint appears below.
- Selected card adds one conversation preview line.

The layout should degrade for narrow terminals by preserving agent, status, title, and focus action before secondary metadata.

## Error Handling

- If the daemon is unreachable, keep the current disconnected behavior.
- If transcript parsing fails for detail, render metadata and show a short conversation error line.
- If transcript data is missing, render metadata and an empty conversation state.
- If notes fail to load, omit notes from the view rather than failing the whole response.
- If a selected session is gone, select the first available session.

## Testing

Add focused tests before implementation:

- `sessionview` card construction includes visible agent kind, status, title, and hint.
- selected card can include a preview line without forcing transcript parsing for every card.
- detail metadata renders default values for missing fields.
- conversation messages are newest first and capped.
- transcript parsers capture both user and assistant one-line previews.
- server endpoints return session views and preserve existing endpoints.
- client methods decode new view payloads.
- TUI render tests show session cards, right-pane metadata, and newest-first conversation.

## Rollout Plan

Implement in small slices:

1. Add transcript conversation preview support.
2. Add `sessionview` builder with tests.
3. Add server and client endpoints.
4. Switch TUI refresh to session views.
5. Replace the table/detail-bottom render with the dual-pane layout.

Existing endpoint behavior remains available throughout the rollout.
