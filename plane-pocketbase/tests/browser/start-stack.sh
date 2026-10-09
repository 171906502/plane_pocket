#!/usr/bin/env bash
# Start the whole Plane-on-PocketBase stack for the browser workflow test.
#
#   bash start-stack.sh            # start backend + web + admin
#   bash start-stack.sh backend    # start only the backend
#
# Logs go to tests/logs/{pb-backend,web-dev,admin-dev,space-dev,live-dev}.log
#
# They deliberately do NOT go to /tmp: the agent sandbox began rejecting writes
# to the system temp directory mid-session (`/tmp/x.log: Permission denied`, and
# even `mv` of an existing temp file failed), which silently broke every
# redirect here and left all five services failing to launch.
#
# Gotchas this script exists to encode (each one cost a debugging session):
#
#  1. `--origins` is mandatory. Plane's web app calls the API cross-origin with
#     `axios withCredentials: true`, so PocketBase must echo the exact Origin.
#     Without it the built-in CORS middleware answers `Access-Control-Allow-
#     Origin: *`, which browsers reject in credentialed mode, and the app dies
#     on the "Plane didn't start up correctly" error boundary with zero API
#     calls in the log.
#  2. `APPDATA` must be exported for the Go toolchain/proxy config to resolve.
#  3. The frontends must be launched from their own `node_modules/.bin`; the
#     globally installed pnpm (v12) cannot satisfy the repo's pinned
#     `packageManager: pnpm@11.10.0` and fails to bootstrap its own wrapper.
#  4. `CODEBUDDY_SAFE_DELETE_ENABLED=0` stops react-router's typegen from
#     tripping the sandbox bulk-delete guard.
#
# ###########################################################################
# #  THE SCRIPT ONLY RUNS UNDER GIT BASH (MSYS2/MINGW64), NOT cmd/PowerShell  #
# #  AND NOT WSL.  See the interpreter guard below — it exists because       #
# #  typing `bash start-stack.sh all` in cmd.exe resolves `bash` to          #
# #  C:\Windows\System32\bash.exe, which is the WSL launcher. WSL cannot     #
# #  see /c/Users/... (that is a path under the Linux root) and has no       #
# #  MSYS2 path translation, so every `cd` fails, nothing starts — and the   #
# #  health check used to still print "all requested services are up".       #
# ###########################################################################
set -u

# --- interpreter guard ------------------------------------------------------
#
# Refuse to run anywhere the paths below cannot resolve, instead of producing a
# cascade of `cd: No such file or directory` followed by a false success.
#
# `uname -s` is the discriminator: Git Bash reports MINGW64_NT-* / MSYS_NT-*,
# WSL reports `Linux`, and native Windows shells never get this far (they hand
# the script to whatever `bash` the PATH resolves to).
_sys="$(uname -s 2>/dev/null || echo unknown)"
case "$_sys" in
  MINGW*|MSYS*|CYGWIN*) : ;;   # good: MSYS2 / Git Bash
  Linux)
    cat >&2 <<'EOF'
[start-stack] FATAL: this script is running under WSL (uname says "Linux").

  You almost certainly typed `bash start-stack.sh ...` in cmd.exe or
  PowerShell, and Windows resolved `bash` to C:\Windows\System32\bash.exe —
  the WSL launcher, not Git Bash.

  WSL cannot see the /c/... paths this script uses (in WSL, /c is a directory
  under the Linux root), so every `cd` fails, no service starts, and you also
  see `mkdir: cannot create directory '/c': Permission denied`.

  Run it from a real Git Bash prompt instead, e.g.:

    "C:\Program Files\Git\bin\bash.exe" -lc "cd /c/Users/cao/Desktop/asl-work/plane_pocket/plane-pocketbase/tests/browser && bash start-stack.sh all"

  Or open "Git Bash" from the Start menu and cd there manually.
EOF
    exit 3 ;;
  *)
    echo "[start-stack] FATAL: unsupported shell environment (uname -s='$_sys')." >&2
    echo "[start-stack] This script requires Git Bash (MSYS2/MINGW64) on Windows or a POSIX shell elsewhere." >&2
    exit 3 ;;
esac

# Derive locations from this script's own path instead of hardcoding them, so a
# clone in a different directory (or a different user) still works.
#
# tests/browser/start-stack.sh -> here is tests/browser, PB is the repo root.
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PB="$(cd "$HERE/../.." && pwd)"
ROOT="$(cd "$PB/.." && pwd)"
FE="$ROOT/plane-preview"

# Resolve the Node toolchain instead of hardcoding it — the managed runtime
# lives in a versioned directory that changes on every upgrade, and a stale
# literal here silently disabled apps/live (see node-env.sh for the full story).
# shellcheck source=node-env.sh
. "$HERE/node-env.sh"
resolve_node
[ -n "$NODE" ] || echo "[start-stack] WARNING: no usable node; apps/live will not start" >&2

LOGS="$PB/tests/logs"
mkdir -p "$LOGS"
: > "$LOGS/stack-wait.log"   # see wait_for_services() below

# Every origin the frontends can realistically be opened on.
#
# `127.0.0.1` and `localhost` are DIFFERENT origins to a browser even though they
# resolve to the same host. Opening the admin at http://127.0.0.1:3001 while this
# list only has http://localhost:3001 means PocketBase sends no
# `Access-Control-Allow-Origin` at all, the credentialed request is rejected, and
# the admin renders "We were unable to fetch the details of the instance."
# List both forms for every frontend port.
ORIGINS="http://localhost:3000,http://localhost:3001,http://localhost:3002,\
http://127.0.0.1:3000,http://127.0.0.1:3001,http://127.0.0.1:3002"

# NEVER write `curl -o /dev/null` here.
#
# `run-all.sh` exports `MSYS2_ARG_CONV_EXCL='*'` (so that `*` and other
# curl-looking arguments are not mangled). That switch also disables MSYS2's
# POSIX→Windows path conversion, so the Windows `curl.exe` takes `/dev/null`
# literally and tries to write to `C:\dev\null`. The request itself succeeds —
# but curl exits **23 (CURLE_WRITE_ERROR)**.
#
# Every `curl … || ok=0` health check therefore failed forever: the wait loops
# below could never satisfy themselves and burned their whole iteration budget,
# delaying the start of every run by ~9 minutes while looking exactly like a
# hung script. Use the platform's null device explicitly.
case "$(uname -s 2>/dev/null)" in
  MINGW*|MSYS*|CYGWIN*) NULL_DEV="NUL" ;;
  *)                    NULL_DEV="/dev/null" ;;
esac

want="${1:-all}"

# Set by any `start_*` whose launch subshell failed (almost always a failed
# `cd`). Checked before the wait loop so we do not sit through a 90-second
# timeout for a service that never even reached its executable.
LAUNCH_FAILED=0

# Every service is launched with `nohup … &` + `disown` so it survives the shell
# that started it. Without this the service dies the moment the launching
# command's process group is reaped — which is exactly what happens when the
# launcher is itself run as a managed background task.
#
# `bound` = something holds the port. `answering` = it actually replies.
#
# These must not be conflated. A dev server that crashes *while starting* still
# leaves its port bound: react-router's typegen does
# `rmdir .react-router/types/app/+types`, and when the sandbox delete guard
# refuses it the process dies with `EPERM` — after the socket was already
# listening. curl then gets no response at all and reports code `000`.
#
# With a port-only check, that dead-but-bound server looked "already up", was
# never restarted, and the whole chain sat waiting on it (this cost a full
# debugging session: `up 3000` in run-all.sh could never succeed, so its
# `&&` chain burned its entire iteration budget and every run started ~9
# minutes late).
bound() {
  netstat -ano 2>/dev/null | grep LISTENING | grep -q ":$1 "
}

answering() {
  local code
  code=$(curl -s -o "$NULL_DEV" -w '%{http_code}' --noproxy '*' --max-time 5 "http://127.0.0.1:$1${2:-/}")
  [ "$code" != "000" ] && [ -n "$code" ]
}

# A crashed dev server keeps its port bound, so a plain relaunch would just hit
# EADDRINUSE. Clear the stale listener first.
kill_port() {
  local pids pid
  pids=$(netstat -ano 2>/dev/null | grep LISTENING | grep ":$1 " | awk '{print $NF}' | sort -u)
  for pid in $pids; do
    echo "[stack] clearing stale listener on $1 (pid $pid)" >&2
    taskkill //PID "$pid" //F >/dev/null 2>&1 || true
  done
  [ -n "$pids" ] && sleep 1
  return 0
}

start_backend() {
  if answering 8000 /api/instances/; then echo "[stack] backend  already up on 8000"; return; fi
  bound 8000 && kill_port 8000
  echo "[stack] backend  -> http://127.0.0.1:8000  (data: .fe-run)"
  (
    cd "$PB" || { echo "[stack] FATAL: cannot cd to $PB (backend not started)" >&2; exit 1; }
    export APPDATA="C:\\Users\\cao\\AppData\\Roaming"
    export PLANE_PB_DEV=1
    export PLANE_ADMIN_BASE_URL="http://localhost:3001"
    nohup ./.fe.exe serve \
      --http=127.0.0.1:8000 \
      --dir=.fe-run \
      --origins="$ORIGINS" \
      > "$LOGS/pb-backend.log" 2>&1 &
    disown
  ) || LAUNCH_FAILED=1
}

start_web() {
  if answering 3000; then echo "[stack] web      already up on 3000"; return; fi
  bound 3000 && kill_port 3000
  echo "[stack] web      -> http://localhost:3000"
  (
    cd "$FE/apps/web" || { echo "[stack] FATAL: cannot cd to $FE/apps/web (web not started)" >&2; exit 1; }
    export CODEBUDDY_SAFE_DELETE_ENABLED=0
    nohup ./node_modules/.bin/react-router dev --port 3000 > "$LOGS/web-dev.log" 2>&1 &
    disown
  ) || LAUNCH_FAILED=1
}

start_admin() {
  if answering 3001 /god-mode/; then echo "[stack] admin    already up on 3001"; return; fi
  bound 3001 && kill_port 3001
  echo "[stack] admin    -> http://127.0.0.1:3001/god-mode/"
  (
    cd "$FE/apps/admin" || { echo "[stack] FATAL: cannot cd to $FE/apps/admin (admin not started)" >&2; exit 1; }
    export CODEBUDDY_SAFE_DELETE_ENABLED=0
    nohup ./node_modules/.bin/react-router dev --port 3001 > "$LOGS/admin-dev.log" 2>&1 &
    disown
  ) || LAUNCH_FAILED=1
}

# The `space` app is Plane's public, read-only project view. It is reached at
# /spaces/<anchor>/... and is the target of the "Publish" flow, so the workflow
# test needs it running alongside web/admin.
start_space() {
  if answering 3002; then echo "[stack] space    already up on 3002"; return; fi
  bound 3002 && kill_port 3002
  echo "[stack] space    -> http://localhost:3002"
  (
    cd "$FE/apps/space" || { echo "[stack] FATAL: cannot cd to $FE/apps/space (space not started)" >&2; exit 1; }
    export CODEBUDDY_SAFE_DELETE_ENABLED=0
    nohup ./node_modules/.bin/react-router dev --port 3002 > "$LOGS/space-dev.log" 2>&1 &
    disown
  ) || LAUNCH_FAILED=1
}

# `apps/live` is the Yjs collaboration server behind the rich-text editor.
#
# It is NOT optional for Pages. When the editor cannot reach it, Plane falls back
# to `usePageFallback`, whose effect re-runs on every `page.description_html`
# change — so "save on disconnect" never converges and the app loops
# PATCH/GET on `.../pages/<id>/description/` forever, wedging the renderer. With
# the live server up, the editor stays in `connected` state and no fallback runs.
#
# It needs Redis; the stock Windows Redis service on 6379 is enough.
#
# Note: `/live/` answers 404 by design, so "answering" here means "returned a
# real HTTP status", not "returned 200".
start_live() {
  if answering 3100 /live/; then echo "[stack] live     already up on 3100"; return; fi
  bound 3100 && kill_port 3100
  echo "[stack] live     -> http://localhost:3100/live/"
  (
    cd "$FE/apps/live" || { echo "[stack] FATAL: cannot cd to $FE/apps/live (live not started)" >&2; exit 1; }
    nohup "$NODE" --env-file=.env . > "$LOGS/live-dev.log" 2>&1 &
    disown
  ) || LAUNCH_FAILED=1
}

case "$want" in
  backend) start_backend ;;
  web) start_web ;;
  admin) start_admin ;;
  space) start_space ;;
  live) start_live ;;
  all)
    start_backend
    start_web
    start_admin
    start_space
    start_live
    ;;
  *)
    echo "usage: bash start-stack.sh [all|backend|web|admin|space|live]" >&2
    exit 2
    ;;
esac

# Wait until each requested service answers.
#
# The probe timeout is deliberately 10s, not 2s: a Vite dev server answers the
# very first request only after it has compiled the route, which routinely takes
# several seconds. A 2s probe reports a perfectly healthy server as down, the
# loop never satisfies itself, and it burns the full retry budget every time.
#
# curl exits non-zero (7 / 28) when a server is dead-but-bound, which is exactly
# the case this loop must catch, so exit status is the right signal here.
#
# The budget is 90 iterations (≈15 min worst case) because a cold start after
# source changes has to re-optimise dependencies in four Vite servers at once;
# 40 iterations was not enough and produced a spurious "TIMED OUT" line while
# every service was in fact still coming up.
wait_for_services() {
  local i
  for i in $(seq 1 90); do
    ok=1
    # Log every attempt to a file, not just to stdout: stdout is a pipe into
    # `tail`, so a loop that never satisfies itself is invisible until the very
    # end — which is exactly what made this take a full debugging session to
    # find. `$LOGS/stack-wait.log` makes it readable while the run is in flight.
    {
      printf 't=%s ' "$i"
      if [ "$want" = "all" ] || [ "$want" = "backend" ]; then
        curl -s -o "$NULL_DEV" --noproxy '*' --max-time 10 http://127.0.0.1:8000/api/instances/ && printf 'backend=OK ' || { printf 'backend=FAIL(%s) ' "$?"; ok=0; }
      fi
      if [ "$want" = "all" ] || [ "$want" = "web" ]; then
        curl -s -o "$NULL_DEV" --noproxy '*' --max-time 10 http://127.0.0.1:3000/ && printf 'web=OK ' || { printf 'web=FAIL(%s) ' "$?"; ok=0; }
      fi
      if [ "$want" = "all" ] || [ "$want" = "admin" ]; then
        curl -s -o "$NULL_DEV" --noproxy '*' --max-time 10 http://127.0.0.1:3001/god-mode/ && printf 'admin=OK ' || { printf 'admin=FAIL(%s) ' "$?"; ok=0; }
      fi
      if [ "$want" = "all" ] || [ "$want" = "space" ]; then
        curl -s -o "$NULL_DEV" --noproxy '*' --max-time 10 http://127.0.0.1:3002/ && printf 'space=OK ' || { printf 'space=FAIL(%s) ' "$?"; ok=0; }
      fi
      if [ "$want" = "all" ] || [ "$want" = "live" ]; then
        curl -s -o "$NULL_DEV" --noproxy '*' --max-time 10 http://127.0.0.1:3100/live/ && printf 'live=OK ' || { printf 'live=FAIL(%s) ' "$?"; ok=0; }
      fi
      printf 'ok=%s\n' "$ok"
    } >> "$LOGS/stack-wait.log"
    if [ "$ok" = "1" ]; then
      echo "[stack] all requested services are up (after ${i}s)"
      return 0
    fi
    sleep 1
  done
  return 1
}

# ------------------------------------------------------------------ failures
#
# Turn "something is wrong" into "here is what is wrong": which service is down,
# whether its port is at least bound (→ it launched and crashed) or not bound at
# all (→ it never reached its executable), plus the tail of its log.
#
# Defined before the checks below so it is available to every failure path.
report_failure() {
  echo "" >&2
  echo "[stack] ===== FAILURE REPORT =====" >&2
  for spec in "backend:8000:/api/instances/" "web:3000:/" "admin:3001:/god-mode/" "space:3002:/" "live:3100:/live/"; do
    name="${spec%%:*}"
    rest="${spec#*:}"
    port="${rest%%:*}"
    path="${rest#*:}"
    case "$want" in
      all) ;;
      "$name") ;;
      *) continue ;;
    esac
    if answering "$port" "$path"; then
      echo "[stack]   $name : OK" >&2
    elif bound "$port"; then
      echo "[stack]   $name : DOWN (port $port is bound but not answering — it launched and crashed)" >&2
    else
      echo "[stack]   $name : DOWN (port $port not bound — it never launched)" >&2
    fi
  done
  for log in "$LOGS/pb-backend.log" "$LOGS/web-dev.log" "$LOGS/admin-dev.log" "$LOGS/space-dev.log" "$LOGS/live-dev.log"; do
    [ -f "$log" ] || continue
    echo "[stack]   --- last 12 lines of $(basename "$log") ---" >&2
    tail -12 "$log" | sed 's/^/[stack]   | /' >&2
  done
  echo "[stack] =========================" >&2
  echo "[stack] full logs: $LOGS/" >&2
}

# A launcher whose `cd` failed can never answer. Fail immediately with the
# report instead of burning the full 90-iteration budget (≈15 min) waiting for
# a process that was never started.
if [ "$LAUNCH_FAILED" = "1" ]; then
  echo "[stack] one or more services failed to launch (see the FATAL lines above)" >&2
  report_failure
  exit 1
fi

if ! wait_for_services; then
  # A frontend whose typegen died on `EPERM rmdir .react-router/types/...`
  # leaves a bound-but-dead port. Restart just those, once, and wait again —
  # otherwise an intermittent crash takes the whole chain down with it.
  echo "[stack] some services did not come up; restarting the frontends once" >&2
  for spec in "web:3000:/" "admin:3001:/god-mode/" "space:3002:/"; do
    name="${spec%%:*}"
    rest="${spec#*:}"
    port="${rest%%:*}"
    path="${rest#*:}"
    if ! answering "$port" "$path"; then
      echo "[stack] restarting $name (port $port is bound but not answering)" >&2
      bound "$port" && kill_port "$port"
      "start_$name"
    fi
  done
  if ! wait_for_services; then
    echo "[stack] TIMED OUT waiting for services." >&2
    report_failure
    exit 1
  fi
fi
