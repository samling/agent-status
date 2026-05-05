#!/usr/bin/env bash
#
# Bootstrap agent-status hooks for Claude Code:
#   1. Copy the forwarder script into ~/.claude/scripts/
#   2. Render hooks.json with the forwarder's absolute path
#   3. Merge the rendered hooks into ~/.claude/settings.json (or create it)
#
# Set CLAUDE_CONFIG_DIR to override ~/.claude.

set -euo pipefail

CLAUDE_DIR="${CLAUDE_CONFIG_DIR:-$HOME/.claude}"
SCRIPT_DEST="$CLAUDE_DIR/scripts/post-agent-status.sh"
SETTINGS="$CLAUDE_DIR/settings.json"

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SRC_SCRIPT="$REPO_ROOT/scripts/post-agent-status.sh"
SRC_HOOKS="$REPO_ROOT/hooks.json"

if [ ! -f "$SRC_SCRIPT" ] || [ ! -f "$SRC_HOOKS" ]; then
    echo "error: must be run from the agent-status repo (missing scripts/ or hooks.json)" >&2
    exit 1
fi

mkdir -p "$(dirname "$SCRIPT_DEST")"
install -m 0755 "$SRC_SCRIPT" "$SCRIPT_DEST"
echo "installed forwarder to $SCRIPT_DEST"

rendered="$(mktemp)"
trap 'rm -f "$rendered"' EXIT
sed "s|path-to-post-agent-status|$SCRIPT_DEST|g" "$SRC_HOOKS" > "$rendered"

if [ -f "$SETTINGS" ]; then
    if ! command -v jq >/dev/null 2>&1; then
        echo
        echo "existing $SETTINGS detected, but 'jq' is not installed."
        echo "manually merge the rendered hooks at:"
        echo "  $rendered"
        echo "into the \"hooks\" object in $SETTINGS, then delete the temp file."
        trap - EXIT
        exit 1
    fi
    cp "$SETTINGS" "$SETTINGS.bak"
    tmp="$(mktemp)"
    jq -s '.[0] * .[1]' "$SETTINGS" "$rendered" > "$tmp"
    mv "$tmp" "$SETTINGS"
    echo "merged hooks into $SETTINGS (backup at $SETTINGS.bak)"
else
    mkdir -p "$(dirname "$SETTINGS")"
    cp "$rendered" "$SETTINGS"
    echo "wrote $SETTINGS"
fi

cat <<'MSG'

bootstrap complete. start the collector and the TUI:
  agent-status server
  agent-status ui          # in another terminal
MSG
