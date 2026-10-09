#!/usr/bin/env bash
# Start the stack, run ONE browser script, exit.
#
# `run-all.sh` drives the whole chain; this is the tight loop for iterating on a
# single script (recon + fix + re-run) without paying for runs 1-4 every time.
#
# Usage: bash run-one.sh <script.mjs> [args...]
set -u

HERE="$(cd "$(dirname "$0")" && pwd)"
PB="$(cd "$HERE/../.." && pwd)"
# shellcheck source=node-env.sh
. "$HERE/node-env.sh"
resolve_node
LOGS="$PB/tests/logs"
export MSYS_NO_PATHCONV=1 MSYS2_ARG_CONV_EXCL='*'
mkdir -p "$LOGS"

SCRIPT="$1"; shift || true
[ -z "${SCRIPT:-}" ] && { echo "usage: run-one.sh <script.mjs> [args]" >&2; exit 2; }

bash "$HERE/start-stack.sh" all || { echo "[run-one] ABORT: stack did not come up" >&2; exit 1; }

# Generous per-probe timeout: the Vite dev servers compile on first request and
# can take >2s to answer, which made a 2s probe report a healthy server as down.
# 120 iterations because a cold start after source changes re-optimises deps in
# four Vite servers at once — 40 was hit exactly ("services ready after 40s").
# `MSYS2_ARG_CONV_EXCL='*'` (exported above) disables MSYS2's POSIX→Windows path
# conversion, so a Windows curl.exe would read `-o /dev/null` as `C:\dev\null`
# and exit 23 (CURLE_WRITE_ERROR). Use the platform null device explicitly.
case "$(uname -s 2>/dev/null)" in
  MINGW*|MSYS*|CYGWIN*) NULL_DEV="NUL" ;;
  *)                    NULL_DEV="/dev/null" ;;
esac
up() { curl -s --noproxy '*' --max-time 10 -o "$NULL_DEV" "http://127.0.0.1:$1${2:-/}"; }
echo "[run-one] waiting for services..."
# `ready` is tracked explicitly. The old form just `break`-ed out of the loop
# and then printed "services ready after ${i}s" unconditionally — so a run where
# nothing ever answered still announced success with i=120.
ready=0
for i in $(seq 1 120); do
  if up 8000 && up 3000 && up 3001 && up 3002 && up 3100 /live/; then ready=1; break; fi
  sleep 1
done
if [ "$ready" != "1" ]; then
  echo "[run-one] ABORT: services not answering after 120s" >&2
  echo "[run-one]   see $LOGS/stack-wait.log and $LOGS/*-dev.log" >&2
  exit 1
fi
echo "[run-one] services ready after ${i}s"

script_win="$(cygpath -w "$HERE/$SCRIPT" 2>/dev/null || echo "$HERE/$SCRIPT")"
"$NODE" "$script_win" "$@" 2>&1 | tee "$LOGS/$SCRIPT.log"
echo "[run-one] $SCRIPT exit=${PIPESTATUS[0]}"
