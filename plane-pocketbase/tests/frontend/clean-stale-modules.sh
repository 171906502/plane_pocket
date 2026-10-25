#!/usr/bin/env bash
# Remove dependency trees left behind by interrupted pnpm installs.
#
# Why this exists instead of a Vite setting: Vite's default watcher ignore is
# `**/node_modules/**`, which does NOT cover siblings named `node_modules.broken-*`,
# `node_modules.dead-*` or `node_modules.partial-*` (pnpm's retry leftovers).
# If the watcher crawls one it re-reads thousands of nested tsconfig.json files
# and can OOM the dev server. Upstream's config has no ignore for them, and
# adding one means a line in `apps/*/vite.config.ts` — a file upstream edits
# often. Deleting the leftovers is the actual fix, and it lives here instead.
#
# Safe by construction:
#   * only directories whose name starts with `node_modules.` are touched
#     (a real `node_modules` is never matched);
#   * the search root is plane-preview, never $HOME or a system directory;
#   * each path is printed before it is removed.
#
# Usage:
#   bash plane-pocketbase/tests/frontend/clean-stale-modules.sh          # remove
#   bash plane-pocketbase/tests/frontend/clean-stale-modules.sh --dry-run
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PB="$(cd "$HERE/../.." && pwd)"
FE="$(cd "$PB/../plane-preview" && pwd)"
DRY=0
[ "${1:-}" = "--dry-run" ] && DRY=1

mapfile -t stale < <(find "$FE" -maxdepth 3 -type d -name 'node_modules.*' -not -path '*/node_modules/*' 2>/dev/null)

if [ "${#stale[@]}" -eq 0 ]; then
  echo "[clean] no stale dependency trees under $FE"
  exit 0
fi

for d in "${stale[@]}"; do
  size="$(du -sh "$d" 2>/dev/null | cut -f1)"
  if [ "$DRY" -eq 1 ]; then
    echo "[clean] would remove $d  ($size)"
  else
    echo "[clean] removing $d  ($size)"
    rm -rf "$d"
  fi
done

echo "[clean] ${#stale[@]} stale tree(s) $([ "$DRY" -eq 1 ] && echo listed || echo removed)"
