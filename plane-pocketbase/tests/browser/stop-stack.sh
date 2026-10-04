#!/usr/bin/env bash
# Stop the Plane-on-PocketBase dev stack started by start-stack.sh.
#
#   bash stop-stack.sh            # stop every service
#   bash stop-stack.sh web        # stop only web
#   bash stop-stack.sh backend web   # stop several specific services
#
# Companion to start-stack.sh; keep the two in sync when adding a service.
#
# Why a script and not "just close the terminal":
#
#   start-stack.sh launches every service with `nohup … &` + `disown`, so the
#   services deliberately OUTLIVE the shell that started them. Closing the
#   terminal therefore does not stop anything — the node/fe.exe processes keep
#   their ports. Without an explicit stop, the next `start-stack.sh` run either
#   reuses the stale service or has to fight it for the port, and a dev server
#   that died mid-startup leaves a *bound but dead* port that looks healthy.
#
#   Under the agent sandbox this matters less (the Job Object reaps everything
#   when the tool call returns), but the moment a human runs the stack from a
#   real terminal it matters a lot.
#
# Design notes:
#   * Kills by PORT, not by image name. `taskkill /IM node.exe` would massacre
#     every unrelated node process on the machine — including the agent's own
#     tooling. Ports 3000-3002, 3100 and 8000 are ours.
#   * Uses `taskkill //T` (kill the process TREE). The Vite dev servers and the
#     Node live server fork children; killing only the listener leaves orphans
#     holding the port or the file watchers alive.
#   * Verifies the port is actually free afterwards. A kill that silently fails
#     is worse than no kill, because the next start then fails with EADDRINUSE
#     and the cause is invisible.
#
# Adapted from the taskkill //PID //F loop in start-stack.sh's kill_port().
set -u

# --- interpreter guard ------------------------------------------------------
#
# Identical to start-stack.sh. Running this under WSL (which is what
# `bash stop-stack.sh` in cmd.exe / PowerShell resolves to) would make every
# /c/... path below meaningless — and worse, `netstat`/`taskkill` would not be
# the Windows ones, so it would silently find nothing and report a clean stop.
_sys="$(uname -s 2>/dev/null || echo unknown)"
case "$_sys" in
  MINGW*|MSYS*|CYGWIN*) : ;;   # good: MSYS2 / Git Bash
  Linux)
    cat >&2 <<'EOF'
[stop-stack] FATAL: this script is running under WSL (uname says "Linux").

  You almost certainly typed `bash stop-stack.sh` in cmd.exe or
  PowerShell, and Windows resolved `bash` to C:\Windows\System32\bash.exe —
  the WSL launcher, not Git Bash. There, `netstat` and `taskkill` are not the
  Windows ones, so this script would find nothing and falsely report success.

  Run it from a real Git Bash prompt instead, e.g.:

    "C:\Program Files\Git\bin\bash.exe" -lc "cd /c/Users/cao/Desktop/asl-work/plane_pocket/plane-pocketbase/tests/browser && bash stop-stack.sh"
EOF
    exit 3 ;;
  *)
    echo "[stop-stack] FATAL: unsupported shell environment (uname -s='$_sys')." >&2
    echo "[stop-stack] This script requires Git Bash (MSYS2/MINGW64) on Windows." >&2
    exit 3 ;;
esac

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PB="$(cd "$HERE/../.." && pwd)"
LOGS="$PB/tests/logs"

# port:name:probe-path — the single source of truth for "what is our stack".
# Probed only when reporting whether a service was still answering.
SERVICES=(
  "8000:backend:/api/instances/"
  "3000:web:/"
  "3001:admin:/god-mode/"
  "3002:space:/"
  "3100:live:/live/"
)

# --- argument handling ------------------------------------------------------
#
# No args  -> every service.
# Named    -> only those (so you can restart just the frontend).
want_all=1
selected=""
for arg in "$@"; do
  case "$arg" in
    -h|--help|help)
      echo "usage: bash stop-stack.sh [backend|web|admin|space|live ...]" >&2
      echo "       (no arguments stops every service)" >&2
      exit 0 ;;
    all) : ;;
    backend|web|admin|space|live)
      want_all=0
      selected="$selected $arg" ;;
    *)
      echo "[stop-stack] unknown service: $arg" >&2
      echo "[stop-stack] valid: backend web admin space live (or no arguments for all)" >&2
      exit 2 ;;
  esac
done

want() {
  [ "$want_all" = "1" ] && return 0
  case " $selected " in *" $1 "*) return 0 ;; esac
  return 1
}

# --- helpers ----------------------------------------------------------------

# Platform null device. Must be defined before any curl call: `MSYS2_ARG_CONV_EXCL`
# (set by run-all.sh / run-one.sh) also disables POSIX→Windows path conversion,
# so a Windows curl.exe reads `-o /dev/null` as `C:\dev\null` and exits 23 —
# with the request having succeeded. Same trap as in start-stack.sh.
case "$(uname -s 2>/dev/null)" in
  MINGW*|MSYS*|CYGWIN*) NULL_DEV="NUL" ;;
  *)                    NULL_DEV="/dev/null" ;;
esac

# PIDs currently LISTENING on a port. Empty when nothing holds it.
pids_on_port() {
  netstat -ano 2>/dev/null | grep LISTENING | grep ":$1 " | awk '{print $NF}' | sort -u
}

port_free() {
  [ -z "$(pids_on_port "$1")" ]
}

# Was the service responding before we killed it? Used only for reporting, so a
# curl failure here is not an error — it is often the reason we are stopping.
#
# Careful with the `-w '%{http_code}'` + `|| echo 000` idiom used elsewhere in
# this repo: curl writes its `000` to stdout AND then exits non-zero for
# connection failures, so the fallback appends a *second* `000` and the variable
# becomes `"000000"` — which passes a naive `!= "000"` test and reports a dead
# service as answering. Capture the exit status separately instead.
was_answering() {
  local code status
  code=$(curl -s -o "$NULL_DEV" -w '%{http_code}' --noproxy '*' --max-time 3 "http://127.0.0.1:$1${2:-/}" 2>/dev/null)
  status=$?
  [ "$status" -eq 0 ] && [ "$code" != "000" ] && [ -n "$code" ]
}

# --- stop -------------------------------------------------------------------

if [ "$want_all" = "1" ]; then
  echo "[stop-stack] stopping the Plane-on-PocketBase stack"
else
  echo "[stop-stack] stopping selected services:$selected"
fi
echo ""

stopped=0
already=0
failed=0

for spec in "${SERVICES[@]}"; do
  port="${spec%%:*}"
  rest="${spec#*:}"
  name="${rest%%:*}"
  path="${rest#*:}"

  want "$name" || continue

  pids=$(pids_on_port "$port")
  if [ -z "$pids" ]; then
    echo "[stop-stack] $name  : nothing on port $port"
    already=$((already + 1))
    continue
  fi

  answering="no"
  was_answering "$port" "$path" && answering="yes"
  echo "[stop-stack] $name  : port $port held by pid(s) $(echo $pids | tr '\n' ' ') (answering=$answering) — killing"

  for pid in $pids; do
    # //T = also kill the process tree. //F = force. The doubled slashes stop
    # MSYS2 from rewriting `/T` and `/F` into paths.
    taskkill //PID "$pid" //T //F >/dev/null 2>&1 || true
  done

  # Wait briefly for the socket to actually be released. taskkill returning 0
  # does not guarantee the port is free yet.
  for _ in 1 2 3 4 5 6 7 8 9 10; do
    port_free "$port" && break
    sleep 0.5
  done

  if port_free "$port"; then
    echo "[stop-stack] $name  : stopped (port $port free)"
    stopped=$((stopped + 1))
  else
    still=$(pids_on_port "$port" | tr '\n' ' ')
    echo "[stop-stack] $name  : FAILED to release port $port (still held by $still)" >&2
    failed=$((failed + 1))
  fi
done

# --- summary ----------------------------------------------------------------

echo ""
echo "[stop-stack] stopped=$stopped  already-stopped=$already  failed=$failed"

if [ "$failed" != "0" ]; then
  echo "[stop-stack] some ports are still held. Common causes:" >&2
  echo "[stop-stack]   * the process is protected or owned by another user" >&2
  echo "[stop-stack]   * a child survived and re-bound the port" >&2
  echo "[stop-stack] Inspect with:  netstat -ano | grep LISTENING | grep ':PORT '" >&2
  exit 1
fi

# Final independent sweep: read the ports back rather than trusting our own
# bookkeeping. This is the same "prove it, don't assume it" rule the start
# script's health check follows.
leftover=""
for spec in "${SERVICES[@]}"; do
  port="${spec%%:*}"
  rest="${spec#*:}"
  name="${rest%%:*}"
  want "$name" || continue
  pids=$(pids_on_port "$port")
  [ -n "$pids" ] && leftover="$leftover $name($port)"
done

if [ -n "$leftover" ]; then
  echo "[stop-stack] WARNING: ports still held after stop:$leftover" >&2
  exit 1
fi

echo "[stop-stack] all requested services are stopped"
