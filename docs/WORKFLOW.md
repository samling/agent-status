# Server Workflow

A concrete, step-by-step list of what the agent-status server does
during a session's lifecycle. Function names and file paths are
included so each step is one click from the running code.

For the higher-level picture, see [WORKFLOW_DIAGRAM.md](WORKFLOW_DIAGRAM.md).

## 0. Server boot

1. `cmd/agent-status/main.go` calls `cli.Execute()`.
2. Cobra's `PersistentPreRunE` runs `bootstrap` in `internal/cli/root.go`:
   1. `loadConfig` reads `$XDG_CONFIG_HOME/agent-status/config.yaml` into viper.
   2. `logging.Setup` installs `log/slog` as the default logger and, if
      `LOG_TRACES` is not `off`, builds an OTel `TracerProvider` with
      the configured exporter (`stdout`, `otlp-http`, or `otlp-grpc`).
3. The `server` subcommand's `runServer` (`internal/cli/server.go`):
   1. `state.Open(statePath)` reads the JSON state file into memory
      (creating an empty store if the file doesn't exist).
   2. Spawns `discovery.Watch(ctx, store)` in a goroutine.
   3. If `server.notify.enabled`, builds and runs `notify.NewWatcher(...)`.
   4. Calls `http.ListenAndServe(addr, server.Handler(store))`.
4. `discovery.Watch` (`internal/discovery/watcher.go`):

   The discovery system has two ways to learn about an agent's state:

   - **The slow path (always on)**: a 2 s poll that calls every
     source's `scan` to enumerate the full alive set. Catches every
     change eventually, but only at tick granularity.
   - **The fast path (per-agent, optional)**: a long-lived goroutine
     that watches an agent-specific event source and pushes updates
     directly into the store as they happen. For claude-code this
     means an fsnotify watcher on `~/.claude/sessions/`, so a
     SessionStart that creates a new JSON file shows up in the store
     within milliseconds instead of waiting up to 2 s for the next
     poll. There is no separate event from the agent itself; the
     fast path is just "watch the place the agent already writes
     state to, and react immediately."

   An agent that has nothing useful to watch (like codex, which
   stores everything in shared SQLite that gets rewritten constantly
   for unrelated reasons) simply omits the fast path and lives on
   the poll alone. Correctness never depends on the fast path - it
   only changes how quickly a change is observed.

   Concretely:

   1. Calls `liveSources()` (`internal/discovery/discovery.go`) to
      get the registered backends. Each `liveSource` has a required
      `scan` (driven by the 2 s poll) and an optional `watch`
      (the fast path). Today claude-code sets
      `watch=watchClaudeFiles`; codex's `watch` is nil.
   2. For each source with a non-nil `watch`, spawns a goroutine
      that runs `src.watch(ctx, store)` - claude-code's
      `watchClaudeFiles` opens an fsnotify watcher and stays in a
      Create/Write/Remove select for the lifetime of the server.
      Errors from a fast path are logged but never block the poll.
   3. Runs an initial `syncDiscovered("initial")` to populate the
      store from whatever's already alive on disk.
   4. Enters a loop on a 2 s ticker, calling `syncDiscovered("tick")`
      on each tick. `syncDiscovered` covers every agent:
      1. Fires every source's `scan` in parallel goroutines
         (claude-code reads `~/.claude/sessions/*.json`; codex opens
         the two SQLite files read-only and joins `threads` against
         `logs.process_uuid`). Both filter by `pidAlive(pid)`.
      2. For each successful scan, applies the alive sessions via
         `applyLiveSession` (which routes to `ApplyDiscovered` for
         claude-code, `ReconcileDiscovered` for codex, or
         `RecordObserved` for any future agent).
      3. Calls `store.ReapAbsentForAgent(ctx, src.agent, aliveSet)`
         per source - the inline reap. Per-agent so a transient
         scan error from one source can never drop another source's
         rows. Dead sessions are dropped within ~2 s without
         needing a separate periodic reap ticker.

## 1. Claude Code: session start

When the user launches `claude` (or another claude-code entrypoint):

1. Claude writes `~/.claude/sessions/<pid>.json` containing
   `{sessionId, pid, status:"idle", entrypoint, version, ...}`.
2. fsnotify fires a Create event inside `watchClaudeFiles`
   (`internal/discovery/claude_code.go`).
3. `processClaudeSessionFile` reads and parses the file.
4. `applyClaudeSessionFile` calls
   `store.ApplyDiscovered(ctx, "claude-code", sessionID, "idle", createdAt)`,
   which under one mutex acquisition:
   1. Inserts a new `Session` row with `LastEvent="Discovered"`.
   2. Sets `JSONLStatus="idle"`.
   3. Persists the state file (`json.MarshalIndent` -> tmp -> rename).
5. Logs `INFO discovery: new claude-code session`.
6. Claude's `SessionStart` hook forwarder POSTs `/hook`.
7. `traceMiddleware` (`internal/server/server.go`) extracts the W3C
   `traceparent` header (if present) and starts an HTTP span.
8. `makeHookHandler`:
   1. Opens a `server.hook` span.
   2. Reads the body, decodes the JSON envelope.
   3. `inferAgent` resolves the agent (`claude-code` here).
   4. `state.NormalizeHookEvent` leaves `SessionStart` unchanged.
   5. `store.RecordEvent(ctx, agent, sessionID, "SessionStart", turnID, receivedAt)`
      finds the existing row (created in step 4), updates
      `LastEvent`, persists. `deriveStatus` is unchanged because
      `JSONLStatus="idle"` pins the row to `idle`.
   6. Logs `INFO hook recorded`.

## 2. Claude Code: turn

The user submits a prompt:

1. Claude writes `status:"busy"` to its session file.
2. fsnotify Write → `applyClaudeSessionFile` →
   `ApplyDiscovered(..., "busy", ...)`. `deriveStatus` flips
   `idle` → `active`. Logs `INFO discovery: claude-code status transitioned`.
3. Claude's `UserPromptSubmit` hook POSTs `/hook`.
4. `RecordEvent` upserts the row. `LastEvent` becomes
   `UserPromptSubmit`, but `deriveStatus` keeps `active` because
   `JSONLStatus="busy"` is still authoritative.
5. During tool calls, Claude fires `PreToolUse` and `PostToolUse`
   hooks. Each goes through the same `/hook` -> `RecordEvent` path;
   `deriveStatus` keeps the row at `active`.
6. If Claude needs the user's attention it fires `Notification` or
   `PermissionRequest`. `deriveStatus`:
   - `PermissionRequest` -> `waiting` even when `JSONLStatus="idle"`
     (the engine being idle while a permission prompt is open just
     means "blocked on user").
   - `Notification` -> `waiting`, but `JSONLStatus="idle"` overrides
     it (intentional: once the engine is back to idle the user has
     either resolved or ignored the prompt).
7. Claude finishes, writes `status:"idle"`. fsnotify Write →
   `ApplyDiscovered(..., "idle", ...)`. `deriveStatus` flips
   `active` → `idle`.
8. Claude fires `Stop` hook. `RecordEvent("Stop")` updates
   `LastEvent`; status stays `idle`.

## 3. Claude Code: session end

### Clean exit

1. Claude deletes its session file on shutdown.
2. fsnotify Remove fires inside `watchClaudeFiles`.
3. The handler runs `scanClaudeLive` (claude-only) to get the fresh
   alive set, then calls
   `store.ReapAbsentForAgent(ctx, "claude-code", aliveSet)`.
4. The exited session's row is deleted; the file is rewritten.
5. The `SessionEnd` hook usually arrives shortly after. The handler
   path is the same as step 1.7, but `RecordEvent` returns
   `applied=false` because the row is already gone. The server logs
   `DEBUG hook ignored (no state change)`.

### Hard exit (crash, kill -9, no Remove fires)

1. The session file lingers; fsnotify sees nothing.
2. Next 2 s `syncDiscovered` tick: `scanClaudeLive` filters by
   `pidAlive(pid)`, which returns false. The dead session is absent
   from the alive set.
3. The inline `ReapAbsentForAgent` inside `syncDiscovered` drops the
   row within ~2 s.

## 4. Codex: session start

Codex has no per-session files; everything is in shared SQLite
(`~/.codex/state_*.sqlite` and `logs_*.sqlite`).

1. The user launches `codex`. Codex inserts a `threads` row and a
   `process_uuid=pid:N` log entry.
2. Codex's `SessionStart` hook POSTs `/hook?agent=codex`.
3. `makeHookHandler` runs the same path as Claude's (read, decode,
   `inferAgent` resolves `codex` from the query param).
4. `state.NormalizeHookEvent("codex", "SessionStart")` leaves it
   unchanged.
5. `store.RecordEvent` inserts a new row with
   `LastEvent="SessionStart"`, `JSONLStatus=""`. `deriveStatus`
   returns `idle` because `SessionStart` is in the idle list.
6. Logs `INFO hook recorded`.
7. Up to 2 s later, `syncDiscovered` ticks. `scanCodexLive`
   (`internal/discovery/codex.go`):
   1. Opens `state_*.sqlite` read-only, queries the `threads` table
      filtered by `archived=0`.
   2. Opens `logs_*.sqlite` read-only, queries log rows whose
      `process_uuid` starts with `pid:` to map `thread_id -> pid`.
   3. For each thread, checks `pidAlive(pid)`; drops dead ones.
8. `applyLiveSession` calls
   `store.ReconcileDiscovered(ctx, "codex", sessionID, createdAt, event)`,
   which corrects `FirstSeenAt` to the SQLite-derived creation
   time. The `event` argument is `"SessionStart"` when the thread's
   `created_at` is within `codexFreshSessionWindow` (30 s), else
   `"Discovered"`; it is recorded on first insert only. The
   reconcile path deliberately does NOT touch `LastEvent` or
   `JSONLStatus` on existing rows, so the hook-driven status
   survives subsequent polls.

## 5. Codex: turn

1. User submits a prompt. Codex fires `UserPromptSubmit` (with a
   `turn_id`). POST `/hook?agent=codex`.
2. `RecordEvent("UserPromptSubmit")`. Codex has no `JSONLStatus`, so
   `deriveStatus` falls through to the `default: active` branch.
   Status flips `idle` → `active`.
3. Tool-call hooks (`PreToolUse`, `PostToolUse`) fire identically;
   the row stays `active`.
4. Codex finishes the turn, fires `Stop`.
5. `state.NormalizeHookEvent("codex", "Stop")` rewrites the event
   to `TurnComplete` (claude uses `Stop` directly; codex's `Stop`
   semantically matches a turn boundary, not a session end).
6. `RecordEvent("TurnComplete")`. `deriveStatus` flips
   `active` → `idle`.

## 6. Codex: session end

Codex does not emit a `SessionEnd` hook. Exit looks like this:

1. The user hits ctrl-c (or codex crashes). The process exits; the
   SQLite thread row remains.
2. Next 2 s `syncDiscovered` tick: `scanCodexLive` finds the thread
   row but `pidAlive(pid)` returns false. The session is missing
   from the alive set.
3. The inline `ReapAbsentForAgent(ctx, "codex", aliveSet)` inside
   `syncDiscovered` drops the row.

## 7. Read paths (no HTTP)

There are no read-side HTTP endpoints. The collector is the single
writer of `state.json` (via tmpfile + atomic rename in
`internal/state/state.go`), which means any reader can `os.ReadFile`
the file and get a consistent snapshot. Clients use this directly.

### Session list (TUI, `agent-status state`, `agent-status statusline`)

1. `state.Load(path)` reads `state.json` and runs `materialize` to
   fill derived fields (`SessionID`, `Status`, parsed timestamps).
2. `discovery.LiveSessionMeta()` runs the per-agent scans in
   parallel and returns a fresh `id -> SessionMeta` map (PID, cwd,
   model, version) read from the agents' own home dirs.
3. The TUI additionally calls `discovery.LoadTranscriptForMeta(...)`
   for the focused row to render the detail panel.

### Connectivity indicator

`server.Reachable(addr)` (`internal/server/probe.go`) does a 100 ms
TCP dial against the listen address. It proves the listener is up;
it deliberately does not exercise any handler, so the cost is a
fraction of an HTTP round trip. Used by both the TUI tick and the
`statusline` template's `Connected` field.

### Focus

The TUI's `enter` handler (`internal/cli/ui/actions.go::focusSelected`)
picks the active session, looks up its PID in the `meta` map already
loaded by the tick (or re-runs `discovery.LiveSessionMeta()` if the
session is brand new), and invokes `focus.PID(pid)`. No server
involvement: compositor IPC must run on the host that owns the
window, which is always the client.

`focus.PID(pid)` (`internal/focus/focus.go`):

1. Picks a `Focuser` for the platform (`focus_linux.go`,
   `focus_darwin.go`, ...) via `New(ctx)`.
2. Walks the process ancestry (`walkAncestors`).
3. Calls the focuser's `Focus(ctx, Target{...})` to bring the
   window to the foreground.
4. Optionally drills into a tmux pane via `findAndFocusTmuxPane`.

## 8. Notify watcher (when enabled)

Runs in its own goroutine alongside `discovery.Watch`:

1. Polls `store.Sessions()` once per second; counts those with
   `Status=="waiting"`.
2. On a `0 -> 1+` transition, arms an initial timer
   (`server.notify.initial-delay`).
3. When that timer fires, builds `TemplateData`, renders the
   `title` and `body` Go templates, calls the platform `Notifier`
   (libnotify on Linux, ...) and arms a repeat timer if
   `server.notify.repeat > 0`.
4. If `server.notify.activation.enabled`, attaches an action button
   whose click runs `focusFirstWaiting(store)`: it picks the
   freshest waiting session straight from the in-process store,
   resolves its PID via `discovery.LiveSessionMeta()`, and calls
   `focus.PID` in the same process (the daemon and desktop are
   always the same machine).
5. On `1+ -> 0`, stops both timers.

## 9. Cross-cutting: tracing and logging

- Each public entry point on the server (the `/hook` handler,
  discovery scans, notify fires, focus calls) opens a span via
  `logging.Start`.
- `traceHandler` (in `internal/logging/handler.go`) decorates the
  default slog handler so every log record auto-attaches `trace_id`
  and `span_id` whenever a span is in scope on the context.
- Cross-process trace propagation lives only on the hook path:
  agents that include a W3C `traceparent` header on their POST
  become parents of the server-side `server.hook` span. There is
  no longer a CLI->server HTTP read path, so reads don't show up
  in the trace stream.
- When `LOG_TRACES=otlp[-grpc]` is set, all spans are exported via
  the standard OTel SDK; configure the destination with the usual
  `OTEL_EXPORTER_OTLP_ENDPOINT` / `OTEL_EXPORTER_OTLP_HEADERS` env
  vars.
