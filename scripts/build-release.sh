#!/usr/bin/env bash
# Build a single-file release artifact under dist/.
#
# Layout produced:
#   dist/symphony            # Go daemon + CLI binary
#   dist/web/dist/...        # Dashboard static assets (already-built Vite output)
#   dist/INSTALL.md          # Short notes about the layout and supported platforms
#
# Discovery is wired in internal/httpapi.httpapi.go: when the binary is launched
# from this layout, dashboardDist() resolves dist/web/dist via os.Executable()'s
# parent directory without requiring SYMPHONY_DASHBOARD_DIST.
#
# Usage:
#   bash scripts/build-release.sh              # build for the host's GOOS/GOARCH
#   GOOS=darwin GOARCH=arm64 bash scripts/build-release.sh
#   OUT_DIR=/tmp/release bash scripts/build-release.sh
#
# This script is best-effort: the dashboard build is skipped if `npm` is not on
# PATH. In that case the Go binary is still produced and the user can drop a
# pre-built dist/web/dist/ into the layout (or set SYMPHONY_DASHBOARD_DIST).
set -euo pipefail

ROOT=$(cd "$(dirname "$0")/.." && pwd)
cd "$ROOT"

OUT_DIR="${OUT_DIR:-$ROOT/dist}"
BIN_NAME="${BIN_NAME:-symphony}"
GOOS_VALUE="${GOOS:-$(go env GOOS)}"
GOARCH_VALUE="${GOARCH:-$(go env GOARCH)}"
EXT=""
if [ "$GOOS_VALUE" = "windows" ]; then EXT=".exe"; fi
BIN_PATH="$OUT_DIR/${BIN_NAME}${EXT}"

# Refuse OUT_DIR that resolves to/under $ROOT. Without this guard, the
# destructive `rm -rf "$OUT_DIR/web"` later in this script would
# silently wipe the checked-in `web/` source tree the rest of the
# build relies on. (D5 codex review F1 [P1].)
ROOT_RESOLVED=$(cd "$ROOT" && pwd -P)
OUT_DIR_RESOLVED=$(mkdir -p "$OUT_DIR" && cd "$OUT_DIR" && pwd -P)
if [ "$OUT_DIR_RESOLVED" = "$ROOT_RESOLVED" ] || \
   { [ "${OUT_DIR_RESOLVED#$ROOT_RESOLVED/}" != "$OUT_DIR_RESOLVED" ] && [ -n "$OUT_DIR_RESOLVED" ]; }; then
  echo "[build-release] refusing to run: OUT_DIR=$OUT_DIR_RESOLVED overlaps with source ROOT=$ROOT_RESOLVED" >&2
  echo "[build-release] pick an OUT_DIR that is a sibling of the source tree (e.g. /tmp/release) so the build cannot delete the checked-in web/ source." >&2
  exit 2
fi

mkdir -p "$OUT_DIR"

echo "[build-release] target=${GOOS_VALUE}/${GOARCH_VALUE}"
echo "[build-release] go build -> $BIN_PATH"
GOOS="$GOOS_VALUE" GOARCH="$GOARCH_VALUE" CGO_ENABLED="${CGO_ENABLED:-1}" \
  go build -trimpath -o "$BIN_PATH" ./cmd/symphony

if [ "${SKIP_WEB:-0}" = "1" ]; then
  echo "[build-release] SKIP_WEB=1 set, skipping dashboard build"
else
  if command -v npm >/dev/null 2>&1; then
    echo "[build-release] npm run build (dashboard)"
    # Use a frozen install against the committed web/package-lock.json.
    # `npm install` would re-resolve `latest`-pinned versions and break
    # the release's reproducibility contract. (D5 codex review F2 [P2].)
    if [ ! -d "$ROOT/web/node_modules" ]; then
      (cd "$ROOT/web" && npm ci --no-audit --no-fund)
    fi
    (cd "$ROOT/web" && npm run build)
    # Only clean the script's own $OUT_DIR/web/dist target. A blanket
    # `rm -rf "$OUT_DIR/web"` would erase unrelated user data the
    # caller may keep in $OUT_DIR/web. (D5 codex review F1 [P1] —
    # scoped rm.)
    rm -rf "$OUT_DIR/web/dist"
    mkdir -p "$OUT_DIR/web/dist"
    cp -R "$ROOT/web/dist/." "$OUT_DIR/web/dist/"
    echo "[build-release] dashboard copied to $OUT_DIR/web/dist"
  else
    echo "[build-release] npm not on PATH; dashboard NOT bundled. The release artifact will still run, but the embedded dashboard will only appear if you pre-populate $OUT_DIR/web/dist or set SYMPHONY_DASHBOARD_DIST." >&2
  fi
fi

# Drop a short install hint next to the binary.
cat > "$OUT_DIR/INSTALL.md" <<EOF
# Local Symphony v1.1 WIP release layout

This directory was produced by \`scripts/build-release.sh\`.

\`\`\`
dist/
  ${BIN_NAME}${EXT}    # daemon + CLI entry point
  web/dist/...         # React/Vite dashboard static assets
\`\`\`

To run:

\`\`\`bash
./${BIN_NAME}${EXT} serve --project /path/to/your/repo --no-open
\`\`\`

Dashboard assets are auto-discovered from the \`web/dist/\` directory next to
the executable. To override, set \`SYMPHONY_DASHBOARD_DIST\` to an absolute
path.

Target supported by this build: ${GOOS_VALUE}/${GOARCH_VALUE}

See \`docs/RELEASE_NOTES.md\` for supported platform / dependency versions
and Windows best-effort caveats.
EOF

echo "[build-release] done: $BIN_PATH"
