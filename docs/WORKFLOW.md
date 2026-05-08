# Server Workflow

A concrete, step-by-step list of what the agent-status server does
during a session's lifecycle. Function names and file paths are
included so each step is one click from the running code.

For the higher-level picture, see [WORKFLOW_DIAGRAM.md](WORKFLOW_DIAGRAM.md).

## 0. Server boot

1. `cmd/agent-status/main.go` calls `cli.Execute()`.
2. Cobra's `PersistentPreRunE` runs `bootstrap` in `internal/cli/root.go`:
   1. `loadConfig` reads `$XDG_CONFIG_HOME/agent-status/config.yaml`
      into viper. Precedence is CLI flags, then `AGENT_STATUS_*`
      env vars, then the config file, then defaults.
   2. `logging.Setup` (`internal/logging/logging.go`) installs
      `log/slog` as the default logger using the resolved
      `log.level` and `log.format`.
3. The `server` subcommand's `runServer` (`internal/cli/server.go`):
   1. `state.Open(statePath)` reads the JSON state file into memory
      (creating an empty store if the file doesn't exist).
   2. Spawns `discovery.Watch(ctx, store)` in a goroutine.
   3. If `--notify` is set, builds and runs `notify.NewWatcher(...)`.
   4. Calls `http.ListenAndServe(addr, server.Handler(store))`.
4. `discovery.Watch` (`internal/discovery/watcher.go`):

   The discovery system has two ways to learn about an agent's state:

   - **The slow path (always on)**: a 2 s poll that calls every
     backend's `Scan` to enumerate the full alive set. Catches every
     change eventually, but only at tick granularity.
   - **The fast path (per-agent)**: a long-lived goroutine that
     watches an agent-specific event source and pushes updates
     directly into the store as they happen. For claude-code the
     fast path is `claudecode.Watch`, an fsnotify watcher on
     `~/.claude/sessions/`. For codex the fast path is `codex.Watch`,
     an fsnotify watcher on `~/.codex/shell_snapshots/`; codex drops
     a snapshot file at session open before any turns, so a
     synthetic SessionStart shows up in the store within
     milliseconds instead of waiting up to 2 s for the next poll.

   Correctness never depends on the fast path; the poll is the
   backstop for both backends.

   Concretely:

   1. Calls `liveSources()` (`internal/discovery/discovery.go`) to
      get the registered backends. Each `liveSource` carries a
      required `scan` (driven by the 2 s poll) and an optional
      `watch` (the fast path), plus per-agent `apply` and
      `transcript` callbacks. Today both claudecode and codex set
      `watch` to a non-nil function.
   2. For each source with a non-nil `watch`, spawns a goroutine
      that runs `src.watch(ctx, store)` for the lifetime of the
      server. Errors from a fast path are logged but never block
      the poll.
   3. Runs an initial `syncDiscovered("initial")` to populate the
      store from whatever's already alive on disk.
   4. Enters a loop on a 2 s ticker, calling `syncDiscovered("tick")`
      on each tick. `syncDiscovered` covers every agent:
      1. Fires every source's `scan` in parallel goroutines.
         claudecode reads `~/.claude/sessions/*.json`; codex merges
         the newest `state_*.sqlite` thread table, recent rollout
         JSONLs (30 min grace), and `shell_snapshots/`, then joins
         against `logs_*.sqlite` to map `thread_id -> pid`. Both
         filter by `source.PIDAlive(pid)`.
      2. For each successful scan, applies the alive sessions via
         the per-agent upsert (`claudecode.Apply` in
         `internal/discovery/claudecode/claudecode.go`,
         `codex.Apply` in `internal/discovery/codex/codex.go`).
      3. Calls `store.ReapAbsentForAgent(ctx, src.agent, aliveSet)`
         per source: the inline reap. Per-agent so a transient
         scan error from one source can never drop another
         source's rows. Dead sessions are dropped within ~2 s
         without needing a separate periodic reap ticker.

## 1. Claude Code: session start

When the user launches `claude` (or another claude-code entrypoint):

1. Claude writes `~/.claude/sessions/<pid>.json` containing
   `{sessionId, pid, status:"idle", entrypoint, version, ...}`.
2. fsnotify Create fires inside `claudecode.Watch`
   (`internal/discovery/claudecode/watch.go`).
3. `processFile` reads and unmarshals the JSON.
4. `applySessionFile` calls `claudecode.Apply`, which inserts a
   new `Session` row with `LastEvent="Discovered"`,
   `EngineStatus="idle"`, `Agent="claude-code"`, and persists.
5. Logs `INFO discovery: claude-code session inserted`.
6. Claude's `SessionStart` hook forwarder POSTs `/hook` with header
   `X-Agent: claude-code`.
7. `logMiddleware` (`internal/server/server.go`) records method,
   path, status, and duration for the request.
8. `makeHookHandler`:
   1. Reads the body and decodes the JSON envelope (`session_id`,
      `hook_event_name`, `turn_id`, `tool_name`, `tool_use_id`,
      `model`, `permission_mode`, `agent_id`, `agent_type`, ...).
   2. Resolves the agent from the `X-Agent` header (defaults to
      `unidentified` if absent).
   3. `store.RecordEvent(ctx, HookEvent{...})` finds the existing
      row, updates `LastEvent="SessionStart"`, persists.
      `DeriveStatus` is unchanged because `EngineStatus="idle"`
      takes precedence over the hook event.
   4. Logs `INFO hook recorded` (or `DEBUG hook ignored` when
      `RecordEvent` reports `applied=false`).

## 2. Claude Code: turn

The user submits a prompt:

1. Claude writes `status:"busy"` to its session file.
2. fsnotify Write -> `claudecode.Apply(..., EngineStatus="busy", ...)`.
   `DeriveStatus` flips `idle -> active`. Logs
   `INFO discovery: claude-code status transitioned`.
3. Claude's `UserPromptSubmit` hook POSTs `/hook`.
4. `RecordEvent` updates the row. `LastEvent` becomes
   `UserPromptSubmit`, but `DeriveStatus` keeps `active` because
   `EngineStatus="busy"` takes precedence over the hook event.
5. During tool calls, Claude fires `PreToolUse` and `PostToolUse`
   hooks. Each goes through the same `/hook` -> `RecordEvent`
   path; `DeriveStatus` keeps the row at `active`.
6. If Claude needs the user's attention it fires `Notification` or
   `PermissionRequest`. `DeriveStatus`:
   - `PermissionRequest` -> `waiting` even when `EngineStatus="idle"`
     (the engine being idle while a permission prompt is open just
     means "blocked on user").
   - `Notification` -> `waiting`, but `EngineStatus="idle"`
     overrides it (intentional: once the engine is back to idle
     the user has either resolved or ignored the prompt).
7. Claude finishes, writes `status:"idle"`. fsnotify Write ->
   `claudecode.Apply(..., EngineStatus="idle", ...)`. `DeriveStatus`
   flips `active -> idle`.
8. Claude fires `Stop` hook. `RecordEvent("Stop")` updates
   `LastEvent`; `DeriveStatus` stays `idle` because `Stop` is in
   the idle event list. There is no hook-event renaming on the
   server side.

## 3. Claude Code: session end

### Clean exit

1. Claude deletes its session file on shutdown.
2. fsnotify Remove fires inside `claudecode.Watch`.
3. The handler runs `claudecode.Scan` (claude-only) to get the
   fresh alive set, then calls
   `store.ReapAbsentForAgent(ctx, "claude-code", aliveSet)`.
4. The exited session's row is deleted; `state.json` is rewritten.
5. The `SessionEnd` hook usually arrives shortly after.
   `RecordEvent` short-circuits when it sees
   `Event == "SessionEnd"` and deletes the row. If the row is
   already gone (the watcher won the race), `RecordEvent` returns
   `applied=false` and the server logs
   `DEBUG hook ignored (no state change)`.

### Hard exit (crash, kill -9, no Remove fires)

1. The session file lingers; fsnotify sees nothing.
2. Next 2 s `syncDiscovered` tick: `claudecode.Scan` filters by
   `source.PIDAlive(pid)`, which returns false for the dead
   process. The dead session is absent from the alive set.
3. The inline `ReapAbsentForAgent` inside `syncDiscovered` drops
   the row within ~2 s.

## 4. Codex: session start

Codex has no per-session JSON files, but it writes a shell-snapshot
file at session open, well before any turns:

1. The user launches `codex`. Codex creates
   `~/.codex/shell_snapshots/<sessionID>.<nanoTs>.sh`.
2. fsnotify Create fires inside `codex.Watch`
   (`internal/discovery/codex/watch.go`).
3. `applySessionFromShellSnapshot` parses the filename to extract
   the session UUID and start timestamp (falling back to mtime),
   synthesizes a minimal `LiveSession` with `Event="SessionStart"`
   and `Entrypoint="cli"`, and dispatches it through `codex.Apply`.
4. `codex.Apply` inserts a new row with `LastEvent="SessionStart"`,
   `Agent="codex"`, `EngineStatus=""` (codex has no engine-status
   signal). `DeriveStatus` returns `idle` because `SessionStart` is
   in the idle event list.
5. Codex's `SessionStart` hook POSTs `/hook` with header
   `X-Agent: codex`. `makeHookHandler` runs the same path as
   Claude's; `RecordEvent` finds the existing row and either
   applies the update or returns `applied=false`.
6. Up to 2 s later, the periodic `syncDiscovered` tick fires
   `codex.Scan` (`internal/discovery/codex/codex.go`), which:
   1. Reads the newest `~/.codex/state_*.sqlite` thread table
      (filtered to `archived=0`) and the matching `logs_*.sqlite`
      for the `thread_id -> pid` mapping.
   2. Reads recent rollout JSONLs under `~/.codex/sessions/`
      (30 min grace) for cwd, model, version, git branch.
   3. Reads `shell_snapshots/` for any unmapped UUIDs still inside
      the grace window.
   4. Filters threads by `source.PIDAlive(pid)`.
7. `codex.Apply` is called for each scan hit. On an existing row
   it deliberately does NOT touch `LastEvent`, `LastEventAt`,
   `TurnID`, or `EngineStatus`, so the hook-driven status survives
   subsequent polls. It only refines durable metadata (e.g.
   `FirstSeenAt` if discovery found an earlier timestamp, `Agent`
   if still `unidentified`, `StatusAt` if never stamped).

## 5. Codex: turn

1. User submits a prompt. Codex fires `UserPromptSubmit` (with a
   `turn_id`). POST `/hook` with `X-Agent: codex`.
2. `RecordEvent("UserPromptSubmit")`. Codex has no `EngineStatus`,
   so `DeriveStatus` falls through to the default `active` branch.
   Status flips `idle -> active`.
3. Tool-call hooks (`PreToolUse`, `PostToolUse`) fire identically;
   the row stays `active`.
4. `PermissionRequest` flips status to `waiting`.
5. Codex finishes the turn, fires `Stop`. `RecordEvent("Stop")`
   updates `LastEvent`; `DeriveStatus` flips `active -> idle`
   because `Stop` is in the idle event list. There is no
   `Stop -> TurnComplete` rewrite anymore; the server records the
   event as it was emitted.

## 6. Codex: session end

Codex does not emit a `SessionEnd` hook. Exit looks like this:

1. The user hits ctrl-c (or codex crashes). The process exits;
   the SQLite thread row remains.
2. Next 2 s `syncDiscovered` tick: `codex.Scan` finds the thread
   row but `source.PIDAlive(pid)` returns false. The session is
   missing from the alive set.
3. The inline `ReapAbsentForAgent(ctx, "codex", aliveSet)` inside
   `syncDiscovered` drops the row.

## 7. Read paths (no HTTP)

There are no read-side HTTP endpoints. The collector is the single
writer of `state.json` (via tmpfile + atomic rename in
`internal/state/state.go`), so any reader can `os.ReadFile` the
file and get a consistent snapshot. Clients use this directly.

### Session list (TUI, `agent-status state`, `agent-status statusline`)

1. `state.Load(path)` reads `state.json` and parses derived fields
   (`FirstSeenTime`, `StatusTime`).
2. `discovery.LiveSessionMeta()` (`internal/discovery/discovery.go`)
   fans the per-agent `Scan` calls out in parallel and returns a
   fresh `id -> SessionMeta` map (PID, entrypoint, cwd, model,
   version, transcript path) read from the agents' own home dirs.
3. The TUI additionally calls
   `discovery.LoadTranscript(sessionID, agent, meta)` for the
   focused row to render the detail panel; the per-agent loaders
   (`claudecode.Transcript`, `codex.Transcript`) go through
   `source.LoadTranscriptPath`, which stat-caches the parsed
   `TranscriptInfo` by path/mtime/size.

### Connectivity indicator

`server.Reachable(addr)` (`internal/server/probe.go`) does a 100 ms
TCP dial against the listen address. It proves the listener is up;
it deliberately does not exercise any handler, so the cost is a
fraction of an HTTP round trip. Used by both the TUI tick and the
`statusline` template's `Connected` field.

### Focus

The TUI's `enter` handler
(`internal/cli/ui/actions.go::focusSelected`) picks the active
session, looks up its PID via `discovery.LiveSessionMeta()`, and
invokes `focus.PID(pid)`. No server involvement: compositor IPC
must run on the host that owns the window, which is always the
client.

`focus.PID(pid)` (`internal/focus/focus.go`):

1. Picks a `Focuser` for the platform (`focus_linux.go`,
   `focus_darwin.go`, ...) via `New(ctx)`.
2. Walks the process ancestry (`walkAncestors`).
3. Calls the focuser's `Focus(ctx, Target{...})` to bring the
   window to the foreground.
4. Optionally drills into a tmux pane via `findAndFocusTmuxPane`.

## 8. Notify watcher (when enabled)

Runs in its own goroutine alongside `discovery.Watch`
(`internal/notify/watcher.go::(*Watcher).Run`):

1. Polls the in-process `store.Sessions()` once per second and
   counts rows where `state.DeriveStatus(s) == "waiting"` via
   `countWaiting()`.
2. On a `0 -> 1+` transition, arms an `initial` timer
   (`notify.initial-delay`).
3. When the initial timer fires, the watcher calls `fire("initial")`,
   which builds `TemplateData`, renders the `title` and `body` Go
   templates, calls the platform `Notifier` (libnotify on Linux, ...),
   and arms a repeat timer if `notify.repeat > 0`.
4. If `notify.activation.enabled`, attaches an action button whose
   click runs `focusFirstWaiting(store)`: it picks the freshest
   waiting session straight from the in-process store, resolves
   its PID via `discovery.LiveSessionMeta()`, and calls `focus.PID`
   in the same process (the daemon and desktop are always the same
   machine).
5. On `1+ -> 0`, stops both timers.

## 9. Cross-cutting: logging

- `logging.Setup` (`internal/logging/logging.go`) installs `log/slog`
  as the default logger using the resolved `log.level` and
  `log.format`.
- Every code path uses `slog.*Context(ctx, ...)` so log records carry
  the request context if there is one. There is no separate tracing
  pipeline; correlate events across components via `session` (short
  id) and `turn` fields.
