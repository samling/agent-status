#!/usr/bin/env bash
#
# Bootstrap agent-status hooks for Claude Code and Codex:
#   1. Copy the forwarder script into each agent config dir
#   2. Render hook configs with the forwarder's absolute path
#   3. Merge the rendered hooks into the agent hook config files
#
# Set CLAUDE_CONFIG_DIR to override ~/.claude.
# Set CODEX_HOME to override ~/.codex.

set -euo pipefail

CLAUDE_DIR="${CLAUDE_CONFIG_DIR:-$HOME/.claude}"
CLAUDE_SCRIPT_DEST="$CLAUDE_DIR/scripts/post-agent-status.sh"
CLAUDE_SETTINGS="$CLAUDE_DIR/settings.json"

CODEX_DIR="${CODEX_HOME:-$HOME/.codex}"
CODEX_SCRIPT_DEST="$CODEX_DIR/scripts/post-agent-status.sh"
CODEX_HOOKS="$CODEX_DIR/hooks.json"

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SRC_SCRIPT="$REPO_ROOT/scripts/post-agent-status.sh"
SRC_CLAUDE_HOOKS="$REPO_ROOT/hooks/claude-code.json"
SRC_CODEX_HOOKS="$REPO_ROOT/hooks/codex.json"

if [ ! -f "$SRC_SCRIPT" ] || [ ! -f "$SRC_CLAUDE_HOOKS" ] || [ ! -f "$SRC_CODEX_HOOKS" ]; then
    echo "error: must be run from the agent-status repo (missing scripts/post-agent-status.sh, hooks/claude-code.json, or hooks/codex.json)" >&2
    exit 1
fi

render_and_merge() {
    local src_hooks="$1"
    local script_dest="$2"
    local target="$3"
    local label="$4"

    local rendered
    rendered="$(mktemp)"
    sed "s|path-to-post-agent-status|$script_dest|g" "$src_hooks" > "$rendered"

    if [ -f "$target" ]; then
        if ! command -v jq >/dev/null 2>&1; then
            echo
            echo "existing $target detected, but 'jq' is not installed."
            echo "manually merge the rendered hooks at:"
            echo "  $rendered"
            echo "into the \"hooks\" object in $target, then delete the temp file."
            return 1
        fi
        local backup
        backup="$target.bak.$(date -u +%Y%m%dT%H%M%SZ)"
        if [ -e "$backup" ]; then
            backup="$backup.$$"
        fi
        cp "$target" "$backup"
        local tmp
        tmp="$(mktemp)"
        jq -s '
          def uniq_hooks:
            reduce .[] as $item
              ([]; if any(.[]; tostring == ($item | tostring)) then . else . + [$item] end);
          .[0] as $base | .[1] as $add |
          ($base * $add) |
          .hooks = (
            ($base.hooks // {}) as $old |
            ($add.hooks // {}) as $new |
            reduce ((($old | keys_unsorted) + ($new | keys_unsorted)) | unique[]) as $k
              ({}; .[$k] = ((($old[$k] // []) + ($new[$k] // [])) | uniq_hooks))
          )
        ' "$target" "$rendered" > "$tmp"
        mv "$tmp" "$target"
        rm -f "$rendered"
        echo "merged $label hooks into $target (backup at $backup)"
    else
        mkdir -p "$(dirname "$target")"
        cp "$rendered" "$target"
        rm -f "$rendered"
        echo "wrote $target"
    fi
}

mkdir -p "$(dirname "$CLAUDE_SCRIPT_DEST")" "$(dirname "$CODEX_SCRIPT_DEST")"
install -m 0755 "$SRC_SCRIPT" "$CLAUDE_SCRIPT_DEST"
install -m 0755 "$SRC_SCRIPT" "$CODEX_SCRIPT_DEST"
echo "installed Claude Code forwarder to $CLAUDE_SCRIPT_DEST"
echo "installed Codex forwarder to $CODEX_SCRIPT_DEST"

if ! render_and_merge "$SRC_CLAUDE_HOOKS" "$CLAUDE_SCRIPT_DEST" "$CLAUDE_SETTINGS" "Claude Code"; then
    exit 1
fi
if ! render_and_merge "$SRC_CODEX_HOOKS" "$CODEX_SCRIPT_DEST" "$CODEX_HOOKS" "Codex"; then
    exit 1
fi

cat <<'MSG'

bootstrap complete. start the collector and the TUI:
  agent-status server
  agent-status ui          # in another terminal
MSG
