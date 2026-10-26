#!/usr/bin/env bash
# Create the workspace-package links pnpm silently skips on this machine.
#
# docs/06-PITFALLS.md C-14: creating symlinks here fails without an error —
# `ln -s` exits 0 and writes nothing, and pnpm's own `link:`/workspace linking
# goes the same way, leaving `node_modules/@plane/` empty. The build then dies
# with `Tsconfig not found` (apps/web/tsconfig.json extends
# `@plane/typescript-config/react-router.json`) or `Cannot find module
# '@plane/editor'`.
#
# A **directory junction** (`mklink /J`) does work, needs no elevation, and is
# what pnpm 11 used. This script creates the same junctions after an install:
#
#     node_modules/@plane/editor  ->  packages/editor
#
# Run it from install-deps.sh; safe to re-run (existing links are left alone).
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PB="$(cd "$HERE/../.." && pwd)"
FE="$(cd "${PLANE_FE_DIR:-$PB/../plane-preview}" && pwd)"

cd "$FE" || exit 1
[ -d node_modules ] || { echo "[link] FATAL: run install-deps.sh first" >&2; exit 1; }

created=0
skipped=0

link_one() {  # link_one <package-dir>
  local dir="$1" name target
  [ -f "$dir/package.json" ] || return 0
  name="$(node -e "process.stdout.write(require('./$dir/package.json').name)" 2>/dev/null || true)"
  [ -n "$name" ] || return 0
  target="node_modules/$name"
  if [ -e "$target" ]; then
    skipped=$((skipped + 1))
    return 0
  fi
  mkdir -p "$(dirname "$target")"
  # Relative junction targets keep the tree portable if it is ever moved.
  if cmd //c "mklink /J $(cygpath -w "$target") $(cygpath -w "$dir")" >/dev/null 2>&1; then
    created=$((created + 1))
  else
    echo "[link] WARN: could not link $name" >&2
  fi
}

for dir in apps/*/ packages/*/; do
  [ -d "$dir" ] || continue
  link_one "${dir%/}"
done

echo "[link] workspace junctions: $created created, $skipped already present"

# Sanity check: the two packages every build needs must resolve from an app.
# `@plane/editor` is source-only until it is built, so probe its package.json
# rather than its `exports` entry point.
for probe in "@plane/typescript-config/react-router.json" "@plane/editor/package.json"; do
  ( cd apps/web && node -e "require.resolve('$probe')" 2>/dev/null ) \
    || { echo "[link] FATAL: $probe still does not resolve from apps/web" >&2; exit 1; }
done
echo "[link] resolution OK"
