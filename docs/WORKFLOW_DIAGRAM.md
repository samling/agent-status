# Session Lifecycle: Per-Agent Workflow

Two top-down flowcharts, one per supported agent, with each lifecycle
phase as its own subgraph so the differences pop out. Both flows
converge on `state.Store`, which is the single source of truth consumed
by the TUI, statusline, `state` CLI, and the notify watcher.

## Claude Code

Blue nodes are actions taken by the `claude-code` process (or its files
on disk); orange nodes are agent-status components (fsnotify watcher,
hook server, `state.Store`, poller).

```mermaid
flowchart TB
    Start([claude-code starts])

    subgraph Phase1["1. Session start"]
        direction TB
        P1A[Create ~/.claude/sessions/pid.json] --> P1B[fsnotify Create/Write]
        P1B --> P1C[ApplyDiscovered<br/>insert row, jsonl_status=idle]
        P1D[Emit SessionStart hook] --> P1E[POST /hook]
        P1E --> P1F[RecordEvent SessionStart]
    end

    subgraph Phase2["2. User prompt + tool calls"]
        direction TB
        P2A[JSONL status=busy] --> P2B[fsnotify Write]
        P2B --> P2C[ApplyDiscovered<br/>idle &rarr; active]
        P2D[UserPromptSubmit, PreToolUse,<br/>PostToolUse, Notification,<br/>PermissionRequest, ...] --> P2E[POST /hook]
        P2E --> P2F[RecordEvent<br/>no transition, prefer JSONL status]
    end

    subgraph Phase3["3. Turn complete"]
        direction TB
        P3A[Emit Stop hook] --> P3B[POST /hook]
        P3B --> P3C[RecordEvent Stop]
        P3D[JSONL status=idle] --> P3E[fsnotify Write]
        P3E --> P3F[ApplyDiscovered<br/>active &rarr; idle]
    end

    subgraph Phase4["4. Session end"]
        direction TB
        P4A[pid.json deleted] --> P4B[fsnotify Remove]
        P4B --> P4C[scanClaudeLive +<br/>ReapAbsentForAgent claude-code]
        P4D[SessionEnd hook<br/>sometimes AFTER the reap] --> P4E[POST /hook]
        P4E --> P4F[RecordEvent<br/>applied=false, logged DEBUG]
    end

    Start --> Phase1
    Phase1 --> Phase2
    Phase2 --> Phase3
    Phase3 -->|next turn| Phase2
    Phase3 -->|exit| Phase4

    Poll[(syncDiscovered: every 2s<br/>ApplyDiscovered + inline ReapAbsent)]
    Poll -.poll.-> Phase1
    Poll -.poll.-> Phase4

    classDef agentAction fill:#e1f5ff,stroke:#0288d1,color:#000
    classDef agentStatus fill:#fff4e1,stroke:#f57c00,color:#000

    class Start,P1A,P1D,P2A,P2D,P3A,P3D,P4A,P4D agentAction
    class P1B,P1C,P1E,P1F,P2B,P2C,P2E,P2F,P3B,P3C,P3E,P3F,P4B,P4C,P4E,P4F,Poll agentStatus
```

## Codex

Blue nodes are actions taken by the `codex` process (or its SQLite
files); orange nodes are agent-status components (hook server,
`state.Store`, poller).

```mermaid
flowchart TB
    Start([codex starts])

    subgraph Phase1["1. Session start"]
        direction TB
        P1A[Insert thread row in<br/>~/.codex/state_*.sqlite] --> P1B[Log process_uuid=pid:N<br/>in logs_*.sqlite]
        P1C[Emit SessionStart hook<br/>agent=codex] --> P1D[POST /hook]
        P1D --> P1E[RecordEvent SessionStart<br/>new=true]
        P1F[~2s later: scanCodexLive] --> P1G[Read threads JOIN logs<br/>filter by pidAlive]
        P1G --> P1H[ReconcileDiscovered<br/>durable metadata, no status clobber]
    end

    subgraph Phase2["2. User prompt + tool calls"]
        direction TB
        P2A[UserPromptSubmit with turn_id] --> P2B[POST /hook]
        P2B --> P2C[RecordEvent<br/>idle &rarr; active]
        P2D[PreToolUse, PostToolUse,<br/>PermissionRequest, ...] --> P2E[POST /hook]
    end

    subgraph Phase3["3. Turn complete"]
        direction TB
        P3A[Emit Stop hook<br/>agent=codex] --> P3B[POST /hook]
        P3B --> P3C[NormalizeHookEvent<br/>rewrites Stop to TurnComplete]
        P3C --> P3D[RecordEvent<br/>active &rarr; idle]
    end

    subgraph Phase4["4. Session end (ctrl-c, crash, no SessionEnd hook)"]
        direction TB
        P4A[PID dies] --> P4B[Thread row remains in DB]
        P4C[~2s poll: scanCodexLive] --> P4D[pidAlive=false]
        P4D --> P4E[Inline ReapAbsentForAgent within ~2s]
    end

    Start --> Phase1
    Phase1 --> Phase2
    Phase2 --> Phase3
    Phase3 -->|next turn| Phase2
    Phase3 -->|exit| Phase4

    Poll[(syncDiscovered: every 2s<br/>only path that learns codex exit)]
    Poll -.poll.-> Phase1
    Poll -.poll.-> Phase4

    classDef agentAction fill:#e1f5ff,stroke:#0288d1,color:#000
    classDef agentStatus fill:#fff4e1,stroke:#f57c00,color:#000

    class Start,P1A,P1B,P1C,P2A,P2D,P3A,P4A,P4B agentAction
    class P1D,P1E,P1F,P1G,P1H,P2B,P2C,P2E,P3B,P3C,P3D,P4C,P4D,P4E,Poll agentStatus
```

## Key contrasts

The asymmetry between the two flows comes from what each agent exposes
locally:

- Claude has a per-session JSON file, so we get fsnotify-driven status
  updates and a fast deletion-driven reap (claude-only re-scan). Codex
  has only shared SQLite, so everything goes through the 2 s poll.
- Claude emits a `SessionEnd` hook (sometimes); codex doesn't. The
  inline reap inside `syncDiscovered` is the only way we learn that
  codex exited.
- Claude exposes a busy/idle JSONL status; codex doesn't, so codex's
  derived status comes purely from `LastEvent`. For claude, `deriveStatus`
  pins to `idle` whenever JSONL says idle, so user-prompt-style hooks
  don't flip the status until JSONL flips to busy. The one exception is
  `PermissionRequest`, which forces `waiting` even over `JSONL=idle`
  because the engine going idle while a permission prompt is open just
  means "blocked on user."
- Both share the same `RecordEvent` / `Sessions` / `ReapAbsentForAgent`
  paths in `state.Store`, so anything downstream (TUI, statusline,
  notify) is agent-agnostic.

## Adding a new agent

A new agent slots into this picture by adding a `liveSource` entry in
`internal/discovery/discovery.go`:

- `scan` is required: returns the live sessions found in the agent's
  local state files (one read every 2 s).
- `watch` is optional: spawn a fast-path watcher (analogous to
  `watchClaudeFiles`) when the agent has per-session files we can hook
  fsnotify into. Agents without one rely on the poll.

Hook payloads from the new agent reach `state.Store` through the same
`POST /hook` path as the existing agents; tag them with `agent=<name>`
in the URL or the JSON body so `inferAgent` resolves them correctly.
