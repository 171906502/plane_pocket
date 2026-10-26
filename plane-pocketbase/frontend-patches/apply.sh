#!/usr/bin/env bash
# Apply / revert the frontend patch set.
#
# The vendored `plane-preview/` tree is kept **byte-identical to upstream**.
# Everything this deployment needs on top of upstream lives here as a patch, so
# an upstream bump is "replace the tree, re-apply the patches, build" instead of
# merging a fork. See docs/12-FRONTEND-DIFF.md.
#
# Patches are rooted at the Plane repository root (`apps/...`, `packages/...`),
# so the same files apply both to the vendored tree and to a fresh
# `git clone https://github.com/makeplane/plane`.
#
# Usage:
#   bash apply.sh                     # apply (no-op when already applied)
#   bash apply.sh status              # clean | applied | conflict
#   bash apply.sh check               # dry-run, exits non-zero if it would fail
#   bash apply.sh revert              # undo (no-op when already clean)
#   bash apply.sh apply --target DIR  # operate on another tree (a fresh clone)
#
# Exit codes: 0 ok, 1 failure, 2 conflict (tree is neither clean nor fully
# patched — someone edited a patched file by hand).
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PB="$(cd "$HERE/.." && pwd)"
TARGET="$(cd "$PB/../plane-preview" 2>/dev/null && pwd || echo "$PB/../plane-preview")"

ACTION="apply"
while [ $# -gt 0 ]; do
  case "$1" in
    apply|revert|check|status) ACTION="$1"; shift ;;
    --target) TARGET="$(cd "$2" && pwd)"; shift 2 ;;
    -h|--help) sed -n '2,26p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *) echo "[patches] unknown argument: $1" >&2; exit 1 ;;
  esac
done

[ -d "$TARGET" ] || { echo "[patches] FATAL: target not found: $TARGET" >&2; exit 1; }

mapfile -t PATCHES < <(ls "$HERE"/[0-9][0-9][0-9][0-9]-*.patch 2>/dev/null | sort)
[ "${#PATCHES[@]}" -gt 0 ] || { echo "[patches] FATAL: no *.patch in $HERE" >&2; exit 1; }

# git apply must run with the target as the working directory: patch paths are
# relative to the Plane repo root. `-p1` drops the a/ b/ prefixes.
#
# Two subtleties, both discovered the hard way:
#
#  1. `git apply` resolves patch paths against the **repository top level**, not
#     the current directory. The vendored tree lives at `<repo>/plane-preview`,
#     so it has to be driven from the top level with `--directory=plane-preview`.
#     Run from inside the subdirectory, git looks for `<repo>/packages/...`,
#     finds nothing, prints "Skipped patch", and **still exits 0**.
#  2. Because of (1), a successful exit code proves nothing. Every apply is
#     verified by reversing it with `--check`: if the tree did not actually
#     change, that reverse check fails and we abort loudly.
#
# And one more, which made the EXIT-trap revert fail *only* after a build:
#
#  3. Every path handed to git is converted with `cygpath -w` first. Git for
#     Windows is a native binary and cannot open an MSYS path like
#     `/d/ai-work/…/0004-*.patch`. Normally Git Bash rewrites such arguments on
#     the way out — but `build-frontends.sh` exports `MSYS_NO_PATHCONV=1`
#     (docs/06-PITFALLS.md C-20, for Vite's `base`), and the EXIT trap inherits
#     it. Result: `git apply` reports
#         error: can't open patch '/d/…/0004-…patch': No such file or directory
#     which `state_of()` then reports as a generic "conflicting state". Same
#     family as C-21: with that variable set, nothing converts anything.
to_native() {
  if command -v cygpath >/dev/null 2>&1; then
    cygpath -w "$1" 2>/dev/null || printf '%s' "$1"
  else
    printf '%s' "$1"
  fi
}

TARGET_NATIVE="$(to_native "$TARGET")"
TOP="$(git -C "$TARGET_NATIVE" rev-parse --show-toplevel 2>/dev/null || true)"
PREFIX="$(git -C "$TARGET_NATIVE" rev-parse --show-prefix 2>/dev/null || true)"

run_apply() {  # run_apply <patch> [extra args...]
  local p; p="$(to_native "$1")"; shift
  if [ -n "$PREFIX" ] && [ -n "$TOP" ]; then
    ( cd "$TOP" && git apply -p1 --whitespace=nowarn --directory="$PREFIX" "$@" "$p" )
  else
    ( cd "$TARGET" && git apply -p1 --whitespace=nowarn "$@" "$p" )
  fi
}

# Per-patch state: 0 = can apply (clean), 1 = can revert (applied), 2 = neither.
#
# Retried: on Windows both `git apply --check` calls can fail *transiently* —
# a freshly written file still open in another process (esbuild service, the
# antivirus scanner) or a `.git/index.lock` held for a moment gives the exact
# same "neither direction works" signal as a genuine hand edit. Observed once at
# the end of a successful build: the EXIT-trap revert reported a conflict, and
# the very next invocation reverted all four patches without complaint.
state_of() {
  local p="$1" i
  for i in 1 2 3 4 5 6; do
    if run_apply "$p" --check >/dev/null 2>&1; then echo 0; return; fi
    if run_apply "$p" --reverse --check >/dev/null 2>&1; then echo 1; return; fi
    [ "$i" -lt 6 ] && sleep 0.5
  done
  echo 2
}

overall_state() {
  local clean=0 applied=0 bad=0 s
  for p in "${PATCHES[@]}"; do
    s="$(state_of "$p")"
    case "$s" in
      0) clean=$((clean + 1)) ;;
      1) applied=$((applied + 1)) ;;
      *) bad=$((bad + 1)) ;;
    esac
  done
  if [ "$bad" -gt 0 ]; then echo conflict
  elif [ "$applied" -eq 0 ]; then echo clean
  elif [ "$clean" -eq 0 ]; then echo applied
  else echo conflict
  fi
}

case "$ACTION" in
  status)
    overall_state
    ;;

  check)
    rc=0
    for p in "${PATCHES[@]}"; do
      name="$(basename "$p")"
      case "$(state_of "$p")" in
        0) echo "[patches] $name  applies cleanly" ;;
        1) echo "[patches] $name  already applied" ;;
        *) echo "[patches] $name  CONFLICT" >&2; rc=1 ;;
      esac
    done
    exit "$rc"
    ;;

  apply)
    for p in "${PATCHES[@]}"; do
      name="$(basename "$p")"
      case "$(state_of "$p")" in
        0)
          run_apply "$p" || { echo "[patches] FATAL: $name failed to apply" >&2; exit 1; }
          # Exit status alone is not proof: see the note on run_apply.
          run_apply "$p" --reverse --check >/dev/null 2>&1 || {
            echo "[patches] FATAL: $name reported success but the tree is unchanged." >&2
            echo "[patches]        (git apply skipped it — usually a path-prefix mismatch)" >&2
            exit 1
          }
          echo "[patches] applied  $name"
          ;;
        1) echo "[patches] skip     $name (already applied)" ;;
        *)
           echo "[patches] FATAL: $name does not apply and is not applied — the tree" >&2
           echo "[patches]        was edited by hand or is on a different upstream commit." >&2
           # Show git's own words: "patch does not apply" (real conflict) reads
           # very differently from "Permission denied" / "index.lock exists"
           # (a file still open, a concurrent git). Both used to look identical
           # because state_of() swallows stderr.
           echo "[patches]        git says:" >&2
           run_apply "$p" --reverse --check 2>&1 | sed 's/^/[patches]          /' >&2 || true
           exit 2 ;;
      esac
    done
    ;;

  revert)
    # Reverse order: later patches may touch files earlier ones introduced.
    for (( i=${#PATCHES[@]}-1; i>=0; i-- )); do
      p="${PATCHES[$i]}"; name="$(basename "$p")"
      case "$(state_of "$p")" in
        1)
          run_apply "$p" --reverse || { echo "[patches] FATAL: $name failed to revert" >&2; exit 1; }
          run_apply "$p" --check >/dev/null 2>&1 || {
            echo "[patches] FATAL: $name reported a revert but the tree is unchanged." >&2
            exit 1
          }
          echo "[patches] reverted $name"
          ;;
        0) echo "[patches] skip     $name (not applied)" ;;
        *)
           echo "[patches] FATAL: $name is in a conflicting state" >&2
           echo "[patches]        git says:" >&2
           run_apply "$p" --reverse --check 2>&1 | sed 's/^/[patches]          /' >&2 || true
           exit 2 ;;
      esac
    done
    # Patch 0003 adds a file; git apply -R deletes it, but an empty directory can
    # be left behind if it was created solely for it. Nothing to do today.
    ;;
esac
