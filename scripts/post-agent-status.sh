#!/usr/bin/env bash
set -euo pipefail

url="http://127.0.0.1:7878/hook"
if [ "${1:-}" != "" ]; then
    url="$url?agent=$1"
fi

curl -sS --max-time 1 --data-binary @- "$url" >/dev/null 2>&1 || true
