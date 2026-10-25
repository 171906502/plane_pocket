#!/usr/bin/env bash
# Install plane-preview dependencies with the two flags this machine needs.
#
# Why a script instead of keeping them in `.npmrc` / `pnpm-workspace.yaml`:
# those files live in the vendored upstream tree, so every local line in them
# becomes an upgrade conflict. Both settings are *machine* concerns, not project
# concerns, so they belong on the command line. See docs/12-FRONTEND-DIFF.md.
#
#   --node-linker hoisted   docs/06-PITFALLS.md C-14
#       Upstream pins `isolated`, which needs pnpm to create junctions/symlinks
#       inside node_modules. On this machine those creations are silently
#       dropped: pnpm prints "added N, done", exits 0, and every app's
#       node_modules stays empty, so the dev servers die with
#       ERR_MODULE_NOT_FOUND. `hoisted` writes real directories only.
#
#   --store-dir <path>      docs/06-PITFALLS.md C-16
#       pnpm 11 reads settings from pnpm-workspace.yaml (camelCase), so a
#       `store-dir` line in .npmrc is silently ignored. On Windows the default
#       already lands on the project's own drive; the flag is insurance against
#       someone cloning the repo to C:.
#
# Usage:
#   bash plane-pocketbase/tests/frontend/install-deps.sh
#   PNPM_STORE_DIR=D:/.pnpm-store bash plane-pocketbase/tests/frontend/install-deps.sh
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PB="$(cd "$HERE/../.." && pwd)"
FE="$(cd "$PB/../plane-preview" && pwd)"
STORE="${PNPM_STORE_DIR:-D:/.pnpm-store}"

echo "[deps] plane-preview -> $FE"
echo "[deps] node-linker=hoisted  store-dir=$STORE"

cd "$FE"
pnpm install --node-linker hoisted --store-dir "$STORE" "$@"

echo "[deps] done"
