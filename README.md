# agent-status

An API and live status board for Claude Code session management.

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
cd <repo>
make install
```

## How It Works

Claude Code fires [hooks](https://code.claude.com/docs/en/hooks) on session events (start, prompt submit, tool use, stop, etc.). The collector (`agent-status server`) receives those events over local HTTP and writes a per-session state file; the TUI (`agent-status ui`) reads that file and renders a live, navigable status board.

The data read by `agent-status` is **local** and **only data provided by claude-code**. The data comes from two places:

- The data sent via the hook
- The data in `~/.claude`, specifically `sessions` and `projects`

This tool simply aggregates that data, tracks the state via the hooks correlated with the session id, and presents it in a terminal UI.

## Compatability

I made this tool for me initially, which means currently it supports **Niri** on **Linux x86_64**. It builds for ARM and so I presume it will work there too; it does not (yet) work on other compositors.

## Setup

> **Requirements:** `jq make`

The fastest path, from a clone of this repo:

```sh
make bootstrap
```

`scripts/bootstrap.sh` will:

1. Copy `scripts/post-agent-status.sh` to `~/.claude/scripts/`.
2. Render `hooks.json` with the absolute path to the forwarder.
3. Merge the rendered hooks into `~/.claude/settings.json` (or create it if missing). If the file already exists, the merge leaves a `.bak` next to the original.

Set `CLAUDE_CONFIG_DIR` to point bootstrap at a different config directory.

### Manual setup

If you don't want to run the script, do it by hand:

```sh
mkdir -p ~/.claude/scripts
cp scripts/post-agent-status.sh ~/.claude/scripts/
sed -i "s|path-to-post-agent-status|$HOME/.claude/scripts/post-agent-status.sh|g" hooks.json
```

Copy/merge the contents of `hooks.json` into `~/.claude/settings.json`

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

Bind the UI to a tmux popup so it overlays the current pane and dismisses itself once you focus a session:

```tmux
bind o display-popup -E "agent-status ui --quit-after-focus"
```

The `--quit-after-focus` flag exits the TUI after `enter` focuses a session, which lets `display-popup -E` close the popup automatically. Omit the flag if you'd rather the popup stay open until you press `q`.

## Develop

```sh
make build      # bin/agent-status
make test       # go test ./...
make clean
```
