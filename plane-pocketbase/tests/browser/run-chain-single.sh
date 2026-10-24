#!/usr/bin/env bash
# Run the whole browser chain (run1 → run5) against the **single-port** server.
#
# Why this exists next to run-all.sh:
#
#   run-all.sh owns the dev stack — it starts five services (8000/3000/3001/3002/3100)
#   and gates on all of them answering. Single-port mode has **one** process on 8000
#   serving all three SPAs, so that gate can never pass there. Everything else about
#   the chain is identical: the scripts only need a base URL, which they read from
#   `PLANE_WEB_URL` (lib.mjs).
#
#   It also avoids `run-one.sh`, which starts the stack and would fight for port 8000.
#
# Usage:
#   bash serve-single.sh            # in another shell / background task, first
#   bash run-chain-single.sh        # then this (reuses :8000)
#   bash run-chain-single.sh --from 4 --project <uuid>   # just pages + publish
#
#   bash run-chain-single.sh --fresh
#       Run the chain against a **scratch data dir** (`PLANE_DATA_DIR`), starting
#       serve-single.sh itself and stopping it at the end. Your `.fe-run` is never
#       touched.
#       **run1 and run2 assume an empty instance** (they walk onboarding, "create
#       the first project"), so on a populated DB they legitimately fail:
#         run1 A1 → the admin session is still valid, /god-mode/ shows the
#                    dashboard, so there is no sign-in form to fill;
#         run1 A4 → already onboarded, so the workspace form never renders;
#         run2 B2 → the empty-state "create your first project" entry is gone.
#       `--fresh` is the single-port equivalent of `run-all.sh --fresh`. It needs
#       :8000 to be free (it refuses to kill a server it did not start).
#
# Exit code is 0 only when every run script exits 0 **and** logs no failed step.
# Logs land in tests/logs/<script>.log (same names run-all.sh uses).
set -u

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PB="$(cd "$HERE/../.." && pwd)"
# shellcheck source=node-env.sh
. "$HERE/node-env.sh"
resolve_node
export MSYS_NO_PATHCONV=1 MSYS2_ARG_CONV_EXCL='*'

WEB="${PLANE_WEB_URL:-http://localhost:8000}"
export PLANE_WEB_URL="$WEB"
# Single-port mode serves all three SPAs from the same origin (/, /god-mode/,
# /spaces/), so admin and space default to the same base as web. Without this,
# run1 still reaches for the dev stack's :3001 and dies with ERR_CONNECTION_REFUSED
# — which then cascades into "workspace not created" failures in run1/run2.
export PLANE_ADMIN_URL="${PLANE_ADMIN_URL:-$WEB}"
export PLANE_SPACE_URL="${PLANE_SPACE_URL:-$WEB}"
LOGS="$PB/tests/logs"
mkdir -p "$LOGS"

FROM=1
FRESH=0
PROJECT_ARG=""
while [ $# -gt 0 ]; do
  case "$1" in
    --from) shift; FROM="${1:-1}" ;;
    --fresh) FRESH=1 ;;
    *) PROJECT_ARG="$1" ;;
  esac
  shift
done

case "$(uname -s 2>/dev/null)" in
  MINGW*|MSYS*|CYGWIN*) NULL_DEV="NUL" ;;
  *)                    NULL_DEV="/dev/null" ;;
esac
probe() { curl -s --noproxy '*' --max-time 5 -o "$NULL_DEV" -w '%{http_code}' "$1"; }

# ── optional: own the server so the DB can start empty ──────────────────────
#
# `PLANE_DATA_DIR`, not `mv .fe-run`: the running instance's data must survive a
# release check. (Also, in the agent sandbox a server started with `&` inside a
# tool call writes into an isolated view that is discarded when the call ends, so
# moving `.fe-run` would "pass" while leaving the real instance displaced.)
SERVER_PID=""
if [ "$FRESH" = "1" ]; then
  if [ "$(probe "$WEB/api/health")" = "200" ]; then
    echo "[chain] ABORT: --fresh needs a free :8000, but something is already serving $WEB" >&2
    echo "[chain]   stop it first (Ctrl-C / TaskStop the serve-single.sh task), then re-run." >&2
    exit 1
  fi
  CHAIN_DATA="${PLANE_CHAIN_DATA_DIR:-$PB/.fe-run-chain-$(date +%Y%m%d-%H%M%S)}"
  mkdir -p "$CHAIN_DATA"
  export PLANE_DATA_DIR="$CHAIN_DATA"
  echo "[chain] scratch data dir: $CHAIN_DATA  (your .fe-run is untouched)"
  echo "[chain] starting serve-single.sh (pid will be stopped on exit)"
  bash "$HERE/serve-single.sh" > "$LOGS/serve-single-chain.log" 2>&1 &
  SERVER_PID=$!
  # shellcheck disable=SC2064
  trap '[ -n "$SERVER_PID" ] && kill "$SERVER_PID" 2>/dev/null' EXIT INT TERM
fi

# The single-port server answers /api/health; it also has to serve the web SPA,
# otherwise every script fails at `waitForShell` with a confusing timeout.
echo "[chain] target $WEB"
ready=0
for _ in $(seq 1 60); do
  h=$(probe "$WEB/api/health")
  r=$(probe "$WEB/")
  if [ "$h" = "200" ] && [ "$r" = "200" ]; then ready=1; break; fi
  sleep 1
done
if [ "$ready" != "1" ]; then
  echo "[chain] ABORT: single-port server not ready (health=$h root=$r)" >&2
  echo "[chain]   start it first:  bash $HERE/serve-single.sh" >&2
  exit 1
fi
echo "[chain] health=200 root=200"

FAILED=0
run() {
  local name="$1"; shift
  echo ""
  echo "########## $name ##########"
  local script
  script="$(cygpath -w "$HERE/$name" 2>/dev/null || echo "$HERE/$name")"
  "$NODE" "$script" "$@" > "$LOGS/$name.log" 2>&1
  local code=$?
  # Exit code alone is NOT a verdict: every run script exits 0 even when steps
  # fail (it logs `!! <step> FAILED: …` and carries on). Treating exit 0 as
  # "pass" produced a green chain over a run where run1 never created the
  # workspace. Count the markers too.
  local steps fails
  steps=$(grep -c '^===== ' "$LOGS/$name.log" 2>/dev/null || true)
  fails=$(grep -c '^  !! ' "$LOGS/$name.log" 2>/dev/null || true)
  steps=${steps:-0}; fails=${fails:-0}
  echo "[chain] $name exit=$code  steps=$steps failed_steps=$fails"
  if [ "$code" -ne 0 ] || [ "$fails" -gt 0 ]; then FAILED=1; fi
  grep -E '^  !! ' "$LOGS/$name.log" 2>/dev/null | head -5
  # Surface the mutations so a "passed" run is auditable without opening the log.
  grep -E '^[0-9]{3} +(POST|PATCH|DELETE)' "$LOGS/$name.log" 2>/dev/null | head -8
  return 0
}

[ "$FROM" -le 1 ] && run run1-bootstrap.mjs
[ "$FROM" -le 2 ] && run run2-project.mjs
[ "$FROM" -le 3 ] && run run3-issue.mjs

# run3 prints the project it discovered; run4/run5 need it. Same extraction as
# run-all.sh so the two paths stay interchangeable.
PROJECT_ID=""
if [ "$FROM" -le 3 ]; then
  PROJECT_ID=$(grep -oE 'PROJECT_ID=[0-9a-f-]{36}' "$LOGS/run3-issue.mjs.log" 2>/dev/null | head -1 | cut -d= -f2)
  [ -z "${PROJECT_ID:-}" ] && \
    PROJECT_ID=$(grep -oE 'projectId=[0-9a-f-]{36}' "$LOGS/run3-issue.mjs.log" 2>/dev/null | head -1 | cut -d= -f2)
fi
[ -z "${PROJECT_ID:-}" ] && PROJECT_ID="$PROJECT_ARG"
echo ""
echo "[chain] projectId=${PROJECT_ID:-<none>}"

if [ -z "${PROJECT_ID:-}" ]; then
  echo "[chain] ABORT: no project id (run3 did not print one, and none passed)" >&2
  exit 1
fi

[ "$FROM" -le 4 ] && run run4-project-pages.mjs "$PROJECT_ID"
[ "$FROM" -le 5 ] && run run5-publish-space.mjs "$PROJECT_ID"

echo ""
echo "[chain] screenshots:"
ls -1 "$PB/tests/evidence/screenshots" 2>/dev/null | tail -12
echo ""
if [ "$FAILED" = "0" ]; then
  echo "[chain] PASS: every run exited 0 with zero failed steps"
else
  echo "[chain] FAIL: a run exited non-zero or a step logged '!! … FAILED' — see $LOGS/*.log" >&2
fi
exit "$FAILED"
