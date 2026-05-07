# agent-status

A hook collector and live status board for local coding-agent sessions.

![Screenshot](./media/screenshot.png)

## Install

### Release tarball
```sh
curl -L https://github.com/samling/agent-status/releases/latest/download/agent-status_<version>_linux-amd64.tar.gz | tar xz
sudo install agent-status_*/agent-status /usr/local/bin/
```

### From source
```sh
go install github.com/samling/agent-status/cmd/agent-status@latest
```

### Build it yourself
```sh
git clone https://github.com/samling/agent-status.git
cd agent-status
make install
```

## How It Works

Claude Code and Codex fire hooks on session events (start, prompt submit, tool use, stop, etc.). The collector (`agent-status server`) receives those events over local HTTP and writes a per-session state file. Agent state files and databases are also scanned locally to discover live sessions and reap stale ones. The TUI (`agent-status ui`) reads the aggregated state file and renders a live, navigable status board.

The data read by `agent-status` is **local** and **only data provided by the supported agents**. The data comes from:

- Claude Code hook payloads
- Claude Code data in `~/.claude`, specifically `sessions` and `projects`
- Codex hook payloads
- Codex data in `~/.codex`, specifically `state_*.sqlite`, `logs_*.sqlite`, and `sessions`

This tool aggregates that data, tracks state by agent and session id, and presents it in a terminal UI.

## Compatability

I made this tool for me initially, which means currently it supports **Niri** on **Linux x86_64**. It builds for ARM and so I presume it will work there too; it does not (yet) work on other compositors.

## Setup

> **Requirements:** `jq make`

The fastest path, from a clone of this repo:

```sh
make bootstrap
```

`scripts/bootstrap.sh` configures Claude Code and Codex and will:

1. Copy `scripts/post-agent-status.sh` to each agent config dir.
2. Render `hooks/claude-code.json` and `hooks/codex.json` with the absolute path to the forwarder.
3. Merge the rendered Claude Code hooks into `~/.claude/settings.json` and Codex hooks into `~/.codex/hooks.json`. If a file already exists, the merge leaves a `.bak` next to the original.

Set `CLAUDE_CONFIG_DIR` to point bootstrap at a different config directory.
Set `CODEX_HOME` if your Codex config directory lives somewhere other than `~/.codex`.

### Manual setup

If you don't want to run the script, do it by hand:

```sh
mkdir -p ~/.claude/scripts
cp scripts/post-agent-status.sh ~/.claude/scripts/
sed -i "s|path-to-post-agent-status|$HOME/.claude/scripts/post-agent-status.sh|g" hooks/claude-code.json
```

Copy/merge the contents of `hooks/claude-code.json` into `~/.claude/settings.json`

For Codex:

```sh
mkdir -p ~/.codex/scripts
cp scripts/post-agent-status.sh ~/.codex/scripts/
sed "s|path-to-post-agent-status|$HOME/.codex/scripts/post-agent-status.sh|g" hooks/codex.json > ~/.codex/hooks.json
```

## Configure

Configuration is stored in `$XDG_CONFIG_HOME/agent-status/config.yaml`. On most systems `XDG_CONFIG_HOME=$HOME/.config`.

The state file is stored in `$XDG_STATE_HOME/agent-status/state.json`. On most systems `XDG_STATE_HOME=$HOME/.local/state`.

## Run

Start the server:

```sh
agent-status server
```

Open the UI:

```sh
agent-status ui
```

See `agent-status -h` and subcommand `-h` output for configuration.

### Run the collector as a user service

A reference systemd user unit lives at `contrib/systemd/user/agent-status.service`. To install it:

```sh
make install-service
systemctl --user enable --now agent-status
```

Or by hand:

```sh
mkdir -p ~/.config/systemd/user
cp contrib/systemd/user/agent-status.service ~/.config/systemd/user/
systemctl --user daemon-reload
systemctl --user enable --now agent-status
```

The unit assumes `agent-status` is on `PATH` at `/usr/bin/agent-status`. Edit `ExecStart=` if yours lives elsewhere (e.g. `~/go/bin/agent-status`).

### Launch from a tmux popup

Bind the UI to a tmux popup so it overlays the current pane and dismisses itself once you focus a session (requires `tmux >= 3.2` for popup support):

```tmux
bind o if-shell -F "#{==:#{session_name},agent-status}" {
  detach-client
} {
  display-popup -E "tmux new-session -A -s agent-status 'agent-status ui --quit-after-focus'"
}
```

The `--quit-after-focus` flag exits the TUI after `enter` focuses a session, which lets `display-popup -E` close the popup automatically. Omit the flag if you'd rather the popup stay open until you press `q`.

## Logging and tracing

Logging is `log/slog`-based and gated by `LOG_LEVEL` (or `log.level`
in config / `--log-level`). Set `LOG_FORMAT=json` for machine-friendly
output. Every log record carries `trace_id` / `span_id` whenever a
span is in scope, so you can correlate a hook write with the request
that drove it.

OpenTelemetry tracing is opt-in via `LOG_TRACES`:

| value       | exporter                                                        |
| ----------- | --------------------------------------------------------------- |
| `off`       | no exporter (default; spans still create trace/span IDs in logs) |
| `stdout`    | pretty-prints spans to stderr                                   |
| `otlp`      | OTLP/HTTP (default port 4318)                                   |
| `otlp-grpc` | OTLP/gRPC (default port 4317)                                   |

OTLP modes honor the standard `OTEL_EXPORTER_OTLP_*` environment
variables (`ENDPOINT`, `HEADERS`, `INSECURE`, ...) and `OTEL_SERVICE_NAME`
/ `OTEL_RESOURCE_ATTRIBUTES`. To kick the tires against a local Jaeger:

```sh
docker compose -f contrib/jaeger/docker-compose.yml up -d
LOG_TRACES=otlp OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4318 \
  agent-status server
# Jaeger UI: http://localhost:16686  (service = agent-status)
```

Point at any OTLP-capable backend (Tempo, Honeycomb, an OTel Collector, ...) by setting `OTEL_EXPORTER_OTLP_ENDPOINT` accordingly.

## Develop

```sh
make build      # bin/agent-status
make test       # go test ./...
make clean
```
