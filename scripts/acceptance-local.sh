#!/usr/bin/env bash
set -euo pipefail
ROOT=$(cd "$(dirname "$0")/.." && pwd)
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT
BIN="$TMP/symphony"
(cd "$ROOT" && go build -o "$BIN" ./cmd/symphony)
cd "$TMP"
git init -q
git config user.email symphony@example.invalid
git config user.name Symphony
printf 'hello\n' > README.md
git add README.md
git commit -q -m init
"$BIN" init --issue-prefix LOC >/dev/null
"$BIN" issue create --title "Add greeting" --description "Implement a greeting helper" >/dev/null
"$BIN" issue transition LOC-1 Ready >/dev/null
"$BIN" issue dispatch LOC-1 >/dev/null
"$BIN" review LOC-1 >/dev/null
"$BIN" review mark-done LOC-1 --reason "Accepted" >/dev/null
echo "acceptance-local passed"
