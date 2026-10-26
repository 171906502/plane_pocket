#!/usr/bin/env bash
# One entry point for the Plane-on-PocketBase dev stack.
#
#   bash stack.sh start   [service...]   # start  (all services if none named)
#   bash stack.sh stop    [service...]   # stop
#   bash stack.sh restart [service...]   # stop, then start
#   bash stack.sh reset   [--start]      # wipe the database (two confirmations)
#   bash stack.sh status                 # what is up right now
#   bash stack.sh logs    [service]      # tail the dev logs
#
# Services: backend web admin space live
#
# This is a thin dispatcher, on purpose. The real work lives in
#   start-stack.sh   stop-stack.sh   reset-data.sh
# and this script only routes to them. Keeping one implementation of each
# operation means there is nothing to keep in sync — which matters here, because
# start-stack.sh encodes a long list of hard-won gotchas (CORS origins, the
# `curl -o /dev/null` trap, the crashed-but-bound port case) that must not be
# duplicated or paraphrased.
#
# Requires Git Bash (MSYS2/MINGW64) — see the interpreter guard below.
set -u

# --- interpreter guard ------------------------------------------------------
#
# Identical to the three scripts this dispatches to, and here for the same
# reason: `bash stack.sh start` typed into cmd.exe or PowerShell resolves
# `bash` to C:\Windows\System32\bash.exe (the WSL launcher), where none of the
# /c/... paths below exist. Without this check the dispatcher would hand off to
# a child that fails for a completely different-looking reason.
_sys="$(uname -s 2>/dev/null || echo unknown)"
case "$_sys" in
  MINGW*|MSYS*|CYGWIN*) : ;;   # good: MSYS2 / Git Bash
  Linux)
    cat >&2 <<'EOF'
[stack] FATAL: this script is running under WSL (uname says "Linux").

  You almost certainly typed `bash stack.sh ...` in cmd.exe or PowerShell, and
  Windows resolved `bash` to C:\Windows\System32\bash.exe — the WSL launcher,
  not Git Bash.

  Run it from a real Git Bash prompt instead, e.g.:

    "C:\Program Files\Git\bin\bash.exe" -lc "cd /d/ai-work/plane_pocket/plane-pocketbase/tests/browser && bash stack.sh status"
EOF
    exit 3 ;;
  *)
    echo "[stack] FATAL: unsupported shell environment (uname -s='$_sys')." >&2
    echo "[stack] This script requires Git Bash (MSYS2/MINGW64) on Windows." >&2
    exit 3 ;;
esac

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PB="$(cd "$HERE/../.." && pwd)"
LOGS="$PB/tests/logs"

# name:port:probe-path — the single source of truth for "what is our stack".
# Mirrors the table in stop-stack.sh; keep the two in step when adding a service.
SPECS=(
  "backend:8000:/api/instances/"
  "web:3000:/"
  "admin:3001:/god-mode/"
  "space:3002:/"
  "live:3100:/live/"
)
VALID="backend web admin space live"

# Platform null device. Must be set before any curl call: `MSYS2_ARG_CONV_EXCL`
# (exported by run-all.sh / run-one.sh) also disables POSIX→Windows path
# conversion, so a Windows curl.exe reads `-o /dev/null` as `C:\dev\null` and
# exits 23 (CURLE_WRITE_ERROR) even though the request succeeded. That silently
# breaks every health check. Same trap as in start-stack.sh.
case "$(uname -s 2>/dev/null)" in
  MINGW*|MSYS*|CYGWIN*) NULL_DEV="NUL" ;;
  *)                    NULL_DEV="/dev/null" ;;
esac

# --- helpers ----------------------------------------------------------------

usage() {
  cat >&2 <<'EOF'
usage: bash stack.sh <command> [service...]

  start   [service...]   start all services, or just the named ones
  stop    [service...]   stop all services, or just the named ones
  restart [service...]   stop, then start
  reset   [--start]      wipe the database (asks twice, then backs it up)
  status                 show what is up right now
  logs    [service]      tail the dev log(s)

  services: backend web admin space live
EOF
}

# Reject unknown service names before handing off, so a typo produces one clear
# message instead of a child script's exit code.
validate_services() {
  local s
  for s in "$@"; do
    case " $VALID " in
      *" $s "*) ;;
      *) echo "[stack] unknown service: $s" >&2
         echo "[stack] valid: $VALID" >&2
         exit 2 ;;
    esac
  done
}

# Is something holding the port? (bound != answering — a dev server that died
# mid-startup still holds its socket.)
bound() {
  netstat -ano 2>/dev/null | grep LISTENING | grep -q ":$1 "
}

# The HTTP status, or empty if nothing answered.
#
# Note the shape: the exit status is captured separately rather than written as
# `code=$(curl ...) || echo 000`. curl prints its own `000` to stdout *and* exits
# non-zero on a connection failure, so the `||` fallback appends a second `000`
# and the variable becomes "000000" — which passes a naive `!= "000"` test and
# reports a dead service as alive. That exact bug shipped in this repo once.
probe_code() {
  local code status
  code=$(curl -s -o "$NULL_DEV" -w '%{http_code}' --noproxy '*' --max-time 3 \
         "http://127.0.0.1:$1$2" 2>/dev/null)
  status=$?
  [ "$status" -eq 0 ] && [ "$code" != "000" ] && [ -n "$code" ] && printf '%s' "$code"
  return 0
}

start_one() {
  bash "$HERE/start-stack.sh" "$1"
}

start_many() {
  # start-stack.sh takes exactly one service name, so fan out. Each call waits
  # for its own service, which is why this is a loop and not a single call.
  if [ "$#" -eq 0 ]; then
    bash "$HERE/start-stack.sh" all
    return $?
  fi
  local s rc=0
  for s in "$@"; do
    start_one "$s" || rc=1
  done
  return $rc
}

# --- dispatch ---------------------------------------------------------------

cmd="${1:-}"
[ "$#" -gt 0 ] && shift

case "$cmd" in
  start)
    validate_services "$@"
    # Stale `node_modules.broken-*` / `.dead-*` / `.partial-*` trees are not
    # covered by Vite's default `**/node_modules/**` ignore; if the watcher
    # crawls one it re-reads thousands of nested tsconfig.json files and can OOM
    # the dev server. Upstream's config has no ignore for them and adding one
    # means editing `apps/*/vite.config.ts`, so the leftovers are deleted here
    # instead. See docs/12-FRONTEND-DIFF.md (group 1).
    bash "$PB/tests/frontend/clean-stale-modules.sh" || true
    # dev serves from source, so the frontend patches have to be in the tree for
    # the whole session (unlike a build, which applies them and reverts). They
    # stay applied until `frontend-patches/apply.sh revert` is run by hand.
    if ! bash "$PB/frontend-patches/apply.sh" apply; then
      echo "[stack] ABORT: frontend patches do not apply; refusing to start a" >&2
      echo "[stack]        dev server that silently differs from a release build." >&2
      exit 1
    fi
    start_many "$@"
    ;;

  stop)
    validate_services "$@"
    bash "$HERE/stop-stack.sh" "$@"
    ;;

  restart)
    validate_services "$@"
    # Stop first and insist it succeeded: starting on top of a stale listener
    # gives EADDRINUSE, and starting on top of a *crashed* listener silently
    # reuses a dead server.
    echo "[stack] restart: stopping first"
    if ! bash "$HERE/stop-stack.sh" "$@"; then
      echo "[stack] ABORT: stop failed; not starting on top of a live port." >&2
      exit 1
    fi
    echo ""
    echo "[stack] restart: starting again"
    start_many "$@"
    ;;

  reset)
    # All flags and confirmations live in reset-data.sh — deliberately, so there
    # is exactly one place that decides whether the database gets wiped.
    bash "$HERE/reset-data.sh" "$@"
    ;;

  status)
    printf '%-8s %-6s %-6s %s\n' SERVICE PORT STATE URL
    printf '%-8s %-6s %-6s %s\n' ------- ------ ----- ---
    down=0
    for spec in "${SPECS[@]}"; do
      name="${spec%%:*}"
      rest="${spec#*:}"
      port="${rest%%:*}"
      path="${rest#*:}"
      code="$(probe_code "$port" "$path")"
      if [ -n "$code" ]; then
        state="UP"
      elif bound "$port"; then
        state="DEAD"        # port held but nothing answered: it crashed
        down=$((down + 1))
      else
        state="down"
        down=$((down + 1))
      fi
      printf '%-8s %-6s %-6s http://127.0.0.1:%s%s\n' "$name" "$port" "$state" "$port" "$path"
    done
    echo ""
    if [ "$down" = "0" ]; then
      echo "[stack] all 5 services are up."
    else
      echo "[stack] $down of 5 services are not answering."
      echo "[stack] DEAD means the port is bound but nothing replied — a crashed"
      echo "[stack] dev server. 'bash stack.sh restart <service>' clears it."
    fi
    # status is a read-only probe; reporting "something is down" is not an error.
    ;;

  logs)
    validate_services "$@"
    if [ "$#" -eq 0 ]; then
      for spec in "${SPECS[@]}"; do
        name="${spec%%:*}"
        echo ""
        echo "===== $name ====="
        tail -n 15 "$LOGS/$name-dev.log" 2>/dev/null || echo "(no log yet)"
      done
    else
      for s in "$@"; do
        echo ""
        echo "===== $s ====="
        tail -n 40 "$LOGS/$s-dev.log" 2>/dev/null || echo "(no log yet)"
      done
    fi
    echo ""
    echo "[stack] logs directory: $LOGS"
    ;;

  ""|-h|--help|help)
    usage
    [ -z "$cmd" ] && exit 2
    exit 0
    ;;

  *)
    echo "[stack] unknown command: $cmd" >&2
    usage
    exit 2
    ;;
esac

exit $?
