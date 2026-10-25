#!/usr/bin/env bash
# Start the **single-port** Plane: one Go binary, one port, zero Node processes.
#
#   bash serve-single.sh          # 127.0.0.1:8000 (default)
#   bash serve-single.sh 9000     # another port — see the warning below
#
# Why a wrapper instead of a bare `./.fe.exe serve`:
#
#   1. The three static dirs are **opt-in**. With PLANE_WEB_DIR / PLANE_ADMIN_DIR /
#      PLANE_SPACE_DIR unset the binary is API-only: `/api/*` and `/_/` answer,
#      every SPA route answers 404 JSON. That is the intended default (see
#      internal/router/static.go), but it is easy to mistake for a routing bug.
#   2. `--origins` must list **both** localhost and 127.0.0.1 — the browser treats
#      them as two different origins, and PocketBase refuses the "*" wildcard in
#      production because it cannot be combined with credentialed requests.
#   3. A missing build dir is a hard error here, not a 404 at runtime.
#
# The frontends must have been built for the **same origin** you serve them on:
#   cd tests/browser && bash build-frontends.sh     # BASE_URL inside that script
# The API base URL is baked into the bundles at build time, so serving a
# :8000-built bundle on :9000 gives a page that loads and then fails every call.
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PB="$(cd "$HERE/../.." && pwd)"
FE="$(cd "$PB/../plane-preview" && pwd)"

PORT="${1:-8000}"
DATA="${PLANE_DATA_DIR:-$PB/.fe-run}"

# Windows builds get a .exe suffix; keep both names working.
if [ -n "${PLANE_BIN:-}" ]; then
  BIN="$PLANE_BIN"
elif [ -f "$PB/.fe.exe" ]; then
  BIN="$PB/.fe.exe"
else
  BIN="$PB/plane"
fi

WEB="$FE/apps/web/build/client"
ADMIN="$FE/apps/admin/build/client"
SPACE="$FE/apps/space/build/client"

for pair in "web:$WEB" "admin:$ADMIN" "space:$SPACE"; do
  name="${pair%%:*}"
  dir="${pair#*:}"
  if [ ! -f "$dir/index.html" ]; then
    cat >&2 <<EOF
[serve-single] FATAL: $name build missing: $dir/index.html

  Build the frontends first:

    cd "$HERE" && bash build-frontends.sh
EOF
    exit 1
  fi
done

if [ ! -f "$BIN" ]; then
  cat >&2 <<EOF
[serve-single] FATAL: binary not found: $BIN

  Build it (and rebuild after any Go change — a stale binary is the usual  reason "my fix does nothing"):

    cd "$PB" && go build -o .fe.exe .
EOF
  exit 1
fi

# Warn, do not fail: a binary older than the newest Go source means the server
# is about to run code that does not match the working tree. That has bitten
# acceptance runs repeatedly (route added today, binary from yesterday →
# "my endpoint 404s" is really "the binary is stale"). Rebuild and retry.
if [ -z "${PLANE_BIN:-}" ]; then
  NEWEST_GO="$(find "$PB" -maxdepth 3 -name '*.go' -newer "$BIN" -not -path '*/.scratch/*' -print -quit 2>/dev/null)"
  if [ -n "$NEWEST_GO" ]; then
    echo "[serve-single] WARNING: $BIN is older than $NEWEST_GO." >&2
    echo "[serve-single]          The server will run stale Go code. Rebuild:" >&2
    echo "[serve-single]            cd $PB && go build -o .fe.exe ." >&2
  fi
fi

# Warn, do not fail: the bundle may legitimately have been built for another
# host spelling (127.0.0.1 vs localhost). A wrong *port* is never legitimate.
if ! grep -q ":$PORT" "$WEB/index.html" && ! grep -rql ":$PORT" "$WEB/assets" 2>/dev/null; then
  echo "[serve-single] WARNING: the web bundle does not reference port $PORT." >&2
  echo "[serve-single]          It was probably built for another origin; the UI" >&2
  echo "[serve-single]          will load but every API call will fail. Rebuild:" >&2
  echo "[serve-single]            cd $HERE && bash build-frontends.sh" >&2
fi

# The binary is a native Windows executable: it wants `D:\ai-work\...`, not
# `/d/ai-work/...`. Normally Git Bash converts path-like env values on spawn, but
# that conversion is **disabled** whenever the caller exported MSYS_NO_PATHCONV=1
# (which build-frontends.sh and the test runners do, for the opposite reason —
# see C-20). Convert explicitly so this script does not depend on ambient MSYS
# flags: with the flag on, the binary received `/d/...` and refused to start with
# "PLANE_ADMIN_DIR: … has no index.html" even though the file was right there.
to_native() {
  case "$(uname -s 2>/dev/null)" in
    MINGW*|MSYS*|CYGWIN*) cygpath -w "$1" 2>/dev/null || printf '%s' "$1" ;;
    *)                    printf '%s' "$1" ;;
  esac
}

export PLANE_WEB_DIR="$(to_native "$WEB")"
export PLANE_ADMIN_DIR="$(to_native "$ADMIN")"
export PLANE_SPACE_DIR="$(to_native "$SPACE")"

echo "[serve-single] bin    $BIN"
echo "[serve-single] data   $DATA"
echo "[serve-single] web    $WEB    -> /"
echo "[serve-single] admin  $ADMIN  -> /god-mode/"
echo "[serve-single] space  $SPACE  -> /spaces/"
echo "[serve-single] listen http://127.0.0.1:$PORT  (no Node, no nginx)"
echo ""

exec "$BIN" serve \
  --http="127.0.0.1:$PORT" \
  --dir="$DATA" \
  --origins="http://localhost:$PORT,http://127.0.0.1:$PORT"
