#!/usr/bin/env bash
set -euo pipefail

agent=""
while [ $# -gt 0 ]; do
    case "$1" in
        --agent)
            agent="${2:-}"
            shift 2
            ;;
        *)
            shift
            ;;
    esac
done

url="http://127.0.0.1:7878/hook"
args=(-sS --max-time 1)
if [ -n "$agent" ]; then
    args+=(-H "X-Agent: $agent")
fi

curl "${args[@]}" --data-binary @- "$url" >/dev/null 2>&1 || true
