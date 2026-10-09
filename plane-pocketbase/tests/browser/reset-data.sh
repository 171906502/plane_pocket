#!/usr/bin/env bash
# Reset the Plane-on-PocketBase database to a clean, empty state.
#
#   bash reset-data.sh            # interactive, asks twice
#   bash reset-data.sh --start    # ...and bring the stack back up afterwards
#
# WHAT IT DOES
#   1. Stops the stack. Not optional: the SQLite files are locked while the
#      backend holds them, so on Windows an `mv` of a live .fe-run fails with
#      a sharing violation.
#   2. Moves the whole data directory aside. It never deletes anything.
#   3. Creates a fresh, empty directory. PocketBase runs its compiled-in
#      migrations on the next `serve`, so you end up with a schema-complete,
#      data-free database.
#
# WHAT IT DOES NOT TOUCH
#   Source code, node_modules, the built .fe.exe, or any .env file.
#
# WHERE THE OLD DATA GOES
#   _scratch/db-backups/.fe-run-<epoch>/
#
#   `_scratch/` is gitignored (root .gitignore), so a multi-megabyte database
#   can never land in the repo, and the working tree stays free of stray copies
#   of a live database.
#
#   A sibling backup next to the original would also be ignored:
#   plane-pocketbase/.gitignore line 3 is `.fe-run.*/`, which matches
#   `.fe-run.bak-*` and `.fe-run.pre-run-*` (the latter is what
#   `run-all.sh --fresh` creates).
#
#   Careful reading that rule: it is directory-scoped, so
#     git check-ignore -v plane-pocketbase/.fe-run.bak-123     -> "not ignored"
#     git check-ignore -v plane-pocketbase/.fe-run.bak-123/    -> "ignored"
#   The bare name reports "not ignored" purely because of the missing slash.
#   That false negative is easy to act on — this comment originally claimed the
#   backup would leak because of it. `_scratch/` remains the better home anyway:
#   it is the project's designated scratch area, ignored by a rule that does not
#   depend on what the backup is called.
#
# WHY TWO CONFIRMATIONS
#   This is the only script here that destroys state, and it destroys it
#   irreversibly. A single `y/N` is not enough — it is one keystroke, it is
#   what muscle memory types, and it is the same key pressed at every other
#   prompt in this repo. So:
#
#     confirm 1 -> type the word `reset`     (proves the warning was read)
#     confirm 2 -> type a random 6-char code (proves someone is reading *now*)
#
#   The code is generated fresh on every run, so it cannot be scripted, cached,
#   or pasted from a previous invocation. Anything other than an exact match at
#   either step aborts before a single file is touched.
#
#   There is deliberately NO --force / --yes flag. A flag would defeat the whole
#   point and would inevitably end up in a shell history or a CI config. If you
#   need a fresh database non-interactively, use `run-all.sh --fresh`, which is
#   built for that and does not pretend to be interactive.
#
# Requires Git Bash (MSYS2/MINGW64) — see the interpreter guard below.
set -u

# --- interpreter guard ------------------------------------------------------
#
# Same reasoning as start-stack.sh / stop-stack.sh: `bash reset-data.sh` typed
# into cmd.exe or PowerShell resolves `bash` to C:\Windows\System32\bash.exe,
# the WSL launcher. There, /c/... is a directory under the Linux root and
# `netstat`/`taskkill` are not the Windows ones — so the stop step would find
# nothing and report a clean stop while the backend kept running and holding
# the database.
_sys="$(uname -s 2>/dev/null || echo unknown)"
case "$_sys" in
  MINGW*|MSYS*|CYGWIN*) : ;;   # good: MSYS2 / Git Bash
  Linux)
    cat >&2 <<'EOF'
[reset-data] FATAL: this script is running under WSL (uname says "Linux").

  You almost certainly typed `bash reset-data.sh` in cmd.exe or PowerShell,
  and Windows resolved `bash` to C:\Windows\System32\bash.exe — the WSL
  launcher, not Git Bash.

  Run it from a real Git Bash prompt instead, e.g.:

    "C:\Program Files\Git\bin\bash.exe" -lc "cd /c/Users/cao/Desktop/asl-work/plane_pocket/plane-pocketbase/tests/browser && bash reset-data.sh"
EOF
    exit 3 ;;
  *)
    echo "[reset-data] FATAL: unsupported shell environment (uname -s='$_sys')." >&2
    echo "[reset-data] This script requires Git Bash (MSYS2/MINGW64) on Windows." >&2
    exit 3 ;;
esac

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PB="$(cd "$HERE/../.." && pwd)"
ROOT="$(cd "$PB/.." && pwd)"

DATA="$PB/.fe-run"
BACKUP_ROOT="$ROOT/_scratch/db-backups"
LOGS="$PB/tests/logs"

# --- arguments --------------------------------------------------------------
#
# The only flag is --start. Nothing here can skip a confirmation.
START_AFTER=0
for arg in "$@"; do
  case "$arg" in
    --start) START_AFTER=1 ;;
    -h|--help|help)
      cat <<'EOF'
usage: bash reset-data.sh [--start]

  --start   start the stack again once the database has been recreated
            (default: leave it stopped)

Moves the database aside to _scratch/db-backups/ and creates an empty one.
Asks for two confirmations first; there is no way to skip them.
EOF
      exit 0 ;;
    *)
      echo "[reset-data] unknown argument: $arg" >&2
      echo "[reset-data] usage: bash reset-data.sh [--start]" >&2
      exit 2 ;;
  esac
done

# --- refuse to run without a human ------------------------------------------
#
# Without a terminal there is nobody to answer the two prompts, and a script
# that silently proceeds on EOF would be the worst possible outcome for the one
# destructive operation in the repo.
if [ ! -t 0 ]; then
  cat >&2 <<'EOF'
[reset-data] FATAL: stdin is not a terminal.

  This script destroys data and therefore requires two interactive
  confirmations. It will not run from a pipe, a CI job, or an agent sandbox.

  If you genuinely want a fresh database non-interactively, use:

    bash run-all.sh --fresh

EOF
  exit 2
fi

# --- what are we about to destroy -------------------------------------------
if [ ! -e "$DATA" ]; then
  echo "[reset-data] nothing to do: $DATA does not exist (already empty)."
  exit 0
fi

size="$(du -sh "$DATA" 2>/dev/null | cut -f1)"
[ -n "$size" ] || size="unknown size"
ts="$(date +%s)"
DEST="$BACKUP_ROOT/.fe-run-$ts"

# Which services are up right now? Reported so the user knows a stop is coming.
running=""
case "$(uname -s 2>/dev/null)" in
  MINGW*|MSYS*|CYGWIN*) NULL_DEV="NUL" ;;
  *)                    NULL_DEV="/dev/null" ;;
esac
for spec in "backend:8000" "web:3000" "admin:3001" "space:3002" "live:3100"; do
  name="${spec%%:*}"
  port="${spec#*:}"
  if netstat -ano 2>/dev/null | grep LISTENING | grep -q ":$port "; then
    running="$running $name"
  fi
done

echo ""
echo "================================================================"
echo " reset-data: this will DESTROY the current database"
echo "================================================================"
echo ""
echo "  data directory : $DATA"
echo "  size           : $size"
echo "  backed up to   : $DEST"
echo ""
echo "  The old data is moved, never deleted, so it can be restored by"
echo "  moving the directory back. But everything in it — accounts,"
echo "  workspaces, projects, issues, pages — is gone from the running"
echo "  instance, and you will have to sign up again from scratch."
echo ""
if [ -n "$running" ]; then
  echo "  These services are running and will be STOPPED first:$running"
else
  echo "  No stack services are currently running."
fi
echo ""
echo "================================================================"
echo ""

# --- confirmation 1 ---------------------------------------------------------
printf '[reset-data] Step 1/2 — type the word  reset  to continue: '
IFS= read -r answer1 || answer1=""
if [ "$answer1" != "reset" ]; then
  echo ""
  echo "[reset-data] aborted at step 1 (got '$answer1', expected 'reset')."
  echo "[reset-data] nothing was touched."
  exit 1
fi

# --- confirmation 2 ---------------------------------------------------------
#
# A fresh random code each run. This is the step that cannot be muscle-memory
# or a copy-paste from a previous session — you have to read the line above.
gen_code() {
  local c
  c="$(LC_ALL=C tr -dc 'A-Z2-9' < /dev/urandom 2>/dev/null | head -c 6)"
  if [ "${#c}" -ne 6 ]; then
    c=""
    while [ "${#c}" -lt 6 ]; do c="$c$RANDOM"; done
    c="${c:0:6}"
  fi
  printf '%s' "$c"
}
CODE="$(gen_code)"

echo ""
echo "[reset-data] Step 2/2 — final check."
echo ""
echo "                 ┌──────────┐"
echo "                 │  $CODE  │"
echo "                 └──────────┘"
echo ""
printf '[reset-data] type that code to confirm: '
IFS= read -r answer2 || answer2=""
answer2u="$(printf '%s' "$answer2" | tr '[:lower:]' '[:upper:]')"
if [ "$answer2u" != "$CODE" ]; then
  echo ""
  echo "[reset-data] aborted at step 2 (codes do not match)."
  echo "[reset-data] nothing was touched."
  exit 1
fi

echo ""
echo "[reset-data] confirmed. proceeding."
echo ""

# --- stop the stack ---------------------------------------------------------
#
# Must succeed before we touch the database: a running backend keeps
# data.db / auxiliary.db open, and on Windows moving an open file fails.
echo "[reset-data] stopping the stack..."
if ! bash "$HERE/stop-stack.sh"; then
  echo "[reset-data] FATAL: could not stop the stack; the database is still locked." >&2
  echo "[reset-data] Nothing was moved. Resolve the port conflict and retry." >&2
  exit 1
fi

# Belt and braces: confirm nothing holds the data directory any more.
still=""
for port in 8000 3000 3001 3002 3100; do
  if netstat -ano 2>/dev/null | grep LISTENING | grep -q ":$port "; then
    still="$still $port"
  fi
done
if [ -n "$still" ]; then
  echo "[reset-data] FATAL: ports still held after stop:$still" >&2
  echo "[reset-data] Nothing was moved." >&2
  exit 1
fi

# --- back up, then recreate -------------------------------------------------
echo ""
echo "[reset-data] moving the database aside..."
mkdir -p "$BACKUP_ROOT"
if ! mv "$DATA" "$DEST"; then
  echo "[reset-data] FATAL: could not move $DATA -> $DEST" >&2
  echo "[reset-data] The existing database is untouched." >&2
  exit 1
fi

if ! mkdir -p "$DATA"; then
  echo "[reset-data] FATAL: moved the old data but could not create $DATA" >&2
  echo "[reset-data] Your old data is safe at: $DEST" >&2
  echo "[reset-data] Recreate the directory manually, then retry." >&2
  exit 1
fi

# Prove the result rather than trusting the mkdir exit status.
if [ ! -d "$DATA" ]; then
  echo "[reset-data] FATAL: $DATA is not a directory after the reset." >&2
  echo "[reset-data] Your old data is safe at: $DEST" >&2
  exit 1
fi
leftover="$(ls -A "$DATA" 2>/dev/null)"
if [ -n "$leftover" ]; then
  echo "[reset-data] WARNING: the new data directory is not empty:" >&2
  echo "[reset-data]   $leftover" >&2
fi

echo "[reset-data] done."
echo ""
echo "  old data : $DEST"
echo "  new data : $DATA (empty — PocketBase will migrate on next serve)"
echo ""

# --- optionally restart -----------------------------------------------------
if [ "$START_AFTER" = "1" ]; then
  echo "[reset-data] starting the stack..."
  if bash "$HERE/start-stack.sh" all; then
    echo "[reset-data] stack is up."
  else
    echo "[reset-data] WARNING: the stack did not come up cleanly (see above)." >&2
    exit 1
  fi
else
  echo "  next: bash stack.sh start        (or: bash start-stack.sh all)"
  echo "  then: sign up again at http://localhost:3000 — the account is gone too."
fi
