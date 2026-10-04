#!/usr/bin/env bash
# End-to-end workflow runner: bring the stack up, drive the whole browser chain,
# then let everything exit together.
#
# Why this exists as one script instead of "start the servers, then run each
# script in its own tool call":
#
#   The agent sandbox puts every spawned shell into a Windows Job Object that is
#   torn down when the tool call returns. `nohup`, `disown` and `&` do NOT escape
#   a job object, so a backend started in one tool call is dead by the next one.
#   Services that were already running from a previous session survive (they
#   belong to no live job), which is why the web/admin dev servers keep working
#   while anything started here does not.
#
#   Running the stack and the whole test chain inside a single process tree keeps
#   every service alive for exactly as long as it is needed.
#
# Usage:
#   bash run-all.sh            # reuse the existing database
#   bash run-all.sh --fresh    # move .fe-run aside first (fresh account setup)
set -u

HERE="$(cd "$(dirname "$0")" && pwd)"
PB="$(cd "$HERE/../.." && pwd)"
NODE="${PLANE_NODE:-/c/Users/cao/.workbuddy-ai/binaries/node/versions/22.22.2-3/node.exe}"
export MSYS_NO_PATHCONV=1 MSYS2_ARG_CONV_EXCL='*'
LOGS="$PB/tests/logs"
mkdir -p "$LOGS"

FRESH=0
FROM=1
PROJECT_ARG=""
while [ $# -gt 0 ]; do
  case "$1" in
    --fresh) FRESH=1 ;;
    --from) shift; FROM="${1:-1}" ;;
    *) PROJECT_ARG="$1" ;;
  esac
  shift
done

if [ "$FRESH" = "1" ]; then
  TS=$(date +%s)
  mv "$PB/.fe-run" "$PB/.fe-run.pre-run-$TS"
  mkdir -p "$PB/.fe-run"
  echo "[run-all] fresh database (previous -> .fe-run.pre-run-$TS)"
fi

# ---------------------------------------------------------------- stack up
#
# `start-stack.sh` now exits non-zero if any requested service fails to answer,
# so this propagates instead of running the whole chain against a dead stack.
# It used to print "all requested services are up" unconditionally, which meant
# a launcher environment that started nothing still produced a green-looking run.
if ! bash "$HERE/start-stack.sh" all; then
  echo "[run-all] ABORT: start-stack.sh reported the stack is not up (exit $?)" >&2
  exit 1
fi

# Wait for the stack to answer, then confirm the backend on a real route.
#
# Two things here are deliberate and were both learned the hard way:
#
#  * `--max-time 5`, not 2. A Vite dev server answers its first request only
#    after compiling, and even warm requests here land around 1.3s; a 2s budget
#    made an all-five-`&&` check flaky, so the loop burned its whole iteration
#    budget every run and the chain started ~9 minutes late.
#  * Print the probe result each time. A silent wait loop that never satisfies
#    itself is indistinguishable from a hung script, and costs far more to
#    diagnose than it saves in output.
#
# The backend answers `/` with 404 by design, so probe a real route for it.
# `MSYS2_ARG_CONV_EXCL='*'` (exported below) also disables MSYS2's POSIX→Windows
# path conversion, so a Windows curl.exe would read `-o /dev/null` as
# `C:\dev\null` and exit 23 (CURLE_WRITE_ERROR) — with the request having
# succeeded. That silently starved every `&&` health check for months' worth of
# debugging; use the platform null device explicitly.
case "$(uname -s 2>/dev/null)" in
  MINGW*|MSYS*|CYGWIN*) NULL_DEV="NUL" ;;
  *)                    NULL_DEV="/dev/null" ;;
esac
probe() { curl -s --noproxy '*' --max-time 5 -o "$NULL_DEV" -w '%{http_code}' "$1"; }

echo "[run-all] waiting for services..."
: > "$LOGS/run-all-wait.log"
ready=0
for i in $(seq 1 60); do
  b=$(probe http://127.0.0.1:8000/api/instances/)
  w=$(probe http://127.0.0.1:3000/)
  a=$(probe http://127.0.0.1:3001/god-mode/)
  s=$(probe http://127.0.0.1:3002/)
  l=$(probe http://127.0.0.1:3100/live/)
  # Also to a file: stdout here is a pipe into `tail`, so nothing is visible
  # until the whole run ends.
  echo "t=${i}s backend=$b web=$w admin=$a space=$s live=$l" >> "$LOGS/run-all-wait.log"
  echo "[run-all] t=${i}s  backend=$b web=$w admin=$a space=$s live=$l"
  if [ "$b" = "200" ] && [ "$w" = "200" ] && [ "$a" = "200" ] && [ "$s" = "302" ] && [ "$l" = "404" ]; then
    ready=1
    break
  fi
  sleep 1
done
if [ "$ready" != "1" ]; then
  # This is a hard failure, not a warning. Each run script does gate on its own
  # probes, but letting the chain proceed anyway means the failure surfaces as a
  # confusing cascade of per-script timeouts instead of one clear "the stack is
  # not up" line. Print what is actually answering so the cause is visible.
  echo "[run-all] ABORT: services not all ready after 60s" >&2
  echo "[run-all]   backend=$b web=$w admin=$a space=$s live=$l" >&2
  echo "[run-all]   expected: backend=200 web=200 admin=200 space=302 live=404" >&2
  echo "[run-all]   see $LOGS/stack-wait.log and $LOGS/run-all-wait.log" >&2
  exit 1
fi
echo "[run-all] backend /api/instances/ -> $b"

# ---------------------------------------------------------------- run chain
run() {
  local name="$1"; shift
  echo ""
  echo "########## $name ##########"
  # Node is a Windows binary: a Git-Bash path like /c/Users/... is read as a
  # relative path and resolved against the drive root (C:\c\Users\...). Convert.
  local script
  script="$(cygpath -w "$HERE/$name" 2>/dev/null || echo "$HERE/$name")"
  "$NODE" "$script" "$@" > "$LOGS/$name.log" 2>&1
  echo "[run-all] $name exit=$?"
  tail -40 "$LOGS/$name.log"
}

[ "$FROM" -le 1 ] && run run1-bootstrap.mjs
[ "$FROM" -le 2 ] && run run2-project.mjs
[ "$FROM" -le 3 ] && run run3-issue.mjs

# run3 prints the project id it discovered; every later script needs it.
PROJECT_ID=$(grep -oE 'PROJECT_ID=[0-9a-f-]{36}' "$LOGS/run3-issue.mjs.log" 2>/dev/null | head -1 | cut -d= -f2)
if [ -z "${PROJECT_ID:-}" ]; then
  PROJECT_ID=$(grep -oE 'projectId=[0-9a-f-]{36}' "$LOGS/run3-issue.mjs.log" 2>/dev/null | head -1 | cut -d= -f2)
fi
if [ -z "${PROJECT_ID:-}" ]; then PROJECT_ID="$PROJECT_ARG"; fi
echo "[run-all] projectId=$PROJECT_ID"

[ "$FROM" -le 4 ] && run run4-project-pages.mjs "$PROJECT_ID"
[ "$FROM" -le 5 ] && run run5-publish-space.mjs "$PROJECT_ID"

echo ""
echo "[run-all] done. screenshots:"
ls -1 "$PB/tests/evidence/screenshots"
