#!/usr/bin/env bash
#
# Forwards an agent hook payload (read from stdin) to the agent-status
# collector. Endpoint resolution, in order:
#   1. $AGENT_STATUS_ENDPOINT  (e.g. "127.0.0.1:7878")
#   2. Contents of the endpoint file written by the running collector
#      ($XDG_STATE_HOME/agent-status/endpoint, default
#      ~/.local/state/agent-status/endpoint)
#   3. 127.0.0.1:7878
#
# A short --max-time guards against blocking the agent if the collector
# is down or slow. Errors are swallowed so a hook never fails the agent.

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

endpoint_file="${XDG_STATE_HOME:-$HOME/.local/state}/agent-status/endpoint"
if [ -n "${AGENT_STATUS_ENDPOINT:-}" ]; then
    endpoint="$AGENT_STATUS_ENDPOINT"
elif [ -r "$endpoint_file" ]; then
    endpoint="$(head -n1 "$endpoint_file" | tr -d '[:space:]')"
fi
endpoint="${endpoint:-127.0.0.1:7878}"

args=(-sS --max-time 1)
if [ -n "$agent" ]; then
    args+=(-H "X-Agent: $agent")
fi

curl "${args[@]}" --data-binary @- "http://${endpoint}/hook" >/dev/null 2>&1 || true
