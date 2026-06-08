#!/usr/bin/env bash
set -euo pipefail
ROOT=$(cd "$(dirname "$0")/.." && pwd)
TMP=$(mktemp -d)
cleanup() { rm -rf "$TMP"; }
trap cleanup EXIT
BIN="$TMP/symphony"
export HOME="$TMP/home"
export SYMPHONY_WORKSPACE_ROOT="$TMP/workspaces"
mkdir -p "$HOME" "$SYMPHONY_WORKSPACE_ROOT"
(cd "$ROOT" && go build -o "$BIN" ./cmd/symphony)
cd "$TMP"
git init -q
git config user.email symphony@example.invalid
git config user.name Symphony
printf 'hello\n' > README.md
git add README.md
git commit -q -m init
"$BIN" init --issue-prefix LOC >/dev/null
# C4 (codex review 2): mutating operator commands require a
# running daemon. Spin one up in the background so the
# acceptance script exercises the daemon-backed path end to end.
# The serve command binds to 127.0.0.1:0 so the OS picks a free
# port; the runtime descriptor written by the daemon tells the
# CLI where to find the daemon.
SYMPHONY_DAEMON_LOG="$TMP/symphony.log"
"$BIN" serve --project "$TMP" --addr 127.0.0.1:0 --no-open >"$SYMPHONY_DAEMON_LOG" 2>&1 &
DAEMON_PID=$!
# Tear down the daemon on exit regardless of which trap fires.
cleanup_extended() { kill "$DAEMON_PID" 2>/dev/null || true; cleanup; }
trap cleanup_extended EXIT
# Wait for the daemon to be reachable. The runtime descriptor is
# written under $HOME/.symphony/runtime/ by db.RuntimeDir(); the
# project_id is the SHA256-derived prj_<hash> string. We poll the
# descriptor's existence and a successful /api/v1/health response
# so the readiness check is correct on every startup latency.
DAEMON_READY=0
for _ in $(seq 1 100); do
  if ls "$HOME/.symphony/runtime"/prj_*.json >/dev/null 2>&1; then
    # Pick the first descriptor, extract its api_url, and probe.
    API_URL=$(python3 -c '
import json, glob, sys
for p in glob.glob("'"$HOME"'/.symphony/runtime/prj_*.json"):
    try:
        with open(p) as f:
            d = json.load(f)
        if d.get("api_url"):
            print(d["api_url"]); break
    except Exception:
        pass
' 2>/dev/null)
    if [ -n "$API_URL" ]; then
      if curl -fsS "$API_URL/api/v1/health" >/dev/null 2>&1; then
        DAEMON_READY=1
        break
      fi
    fi
  fi
  sleep 0.1
done
if [ "$DAEMON_READY" -ne 1 ]; then
  echo "acceptance-local: daemon failed to come up" >&2
  cat "$SYMPHONY_DAEMON_LOG" >&2 || true
  exit 1
fi
unset SYMPHONY_DAEMON_URL  # ensure discovery falls back to runtime descriptor
"$BIN" issue create --title "Add greeting" --description "Implement a greeting helper" >/dev/null
"$BIN" issue transition LOC-1 Ready >/dev/null
"$BIN" issue dispatch LOC-1 >/dev/null
"$BIN" review LOC-1 >/dev/null
"$BIN" review mark-done LOC-1 --reason "Accepted" >/dev/null
echo "acceptance-local passed"
