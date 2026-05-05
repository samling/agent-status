#!/usr/bin/env bash
set -euo pipefail

curl -sS --max-time 1 --data-binary @- http://127.0.0.1:7878/hook >/dev/null 2>&1 || true
