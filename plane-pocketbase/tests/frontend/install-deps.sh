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
# PLANE_FE_DIR lets the upstream-verification script install into a fresh clone.
FE="$(cd "${PLANE_FE_DIR:-$PB/../plane-preview}" && pwd)"
STORE="${PNPM_STORE_DIR:-D:/.pnpm-store}"

echo "[deps] plane-preview -> $FE"
echo "[deps] node-linker=hoisted  store-dir=$STORE"

cd "$FE"

# `--frozen-lockfile` on purpose: the vendored tree must stay byte-identical to
# upstream, and a rewritten pnpm-lock.yaml would silently undo that.
#
# `npm_config_manage_package_manager_versions=false` (C-14b): the project pins
# `packageManager: pnpm@11.10.0`, and pnpm tries to download that exact version
# into `%LOCALAPPDATA%\pnpm\package-manager-store` before doing anything. That
# download cannot complete here, so every install died with
# "resolve the installed @pnpm/exe … os error 2". Turning the self-management
# off makes it use the pnpm already on PATH (v12), which handles the same
# lockfile fine.
npm_config_manage_package_manager_versions=false \
  pnpm install --frozen-lockfile --node-linker hoisted --store-dir "$STORE" "$@"

# pnpm cannot create the workspace symlinks here (C-14); junctions are made by a
# separate script. Packages then have to be built once, because the apps import
# them through their `dist/` exports.
bash "$HERE/link-workspace-packages.sh"
bash "$HERE/build-workspace-packages.sh"

echo "[deps] done"
