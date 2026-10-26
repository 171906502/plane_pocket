#!/usr/bin/env bash
# Build the `packages/*` workspace libraries (`@plane/editor`, `@plane/types`, …).
#
# The three apps import them through their `exports` map, which points at
# `dist/` — so without this step `react-router build` dies with
#
#     [postcss] ENOENT … apps/web/@plane/editor/styles
#
# `dist/` is gitignored, so a fresh checkout (or a fresh clone) has to build it
# once.
#
# ── Why not turbo / `pnpm -r run build` ────────────────────────────────────
#
# Both eventually execute `pnpm run build`, and pnpm >= 10 verifies the
# dependency tree before every run and re-installs when the recorded state does
# not match. Measured on this machine (docs/06-PITFALLS.md C-14b):
#
#   * `pnpm -r --filter "./packages/*" run build` wiped `node_modules`,
#     re-resolved the whole workspace, took 15 minutes and ended half-installed;
#   * turbo 2 (with `--env-mode=loose` so the C-14b env var survives its strict
#     env filter) ran five concurrent `pnpm install --verify-deps-before-run-install`
#     children against one lock: 20+ minutes, zero `dist/`.
#
# So the actual build is done by `build-packages.mjs`, which walks `packages/*`
# in dependency order and runs each `build` script directly with
# `node_modules/.bin` on PATH. No package manager, no install, no lock.
#
# Usage:
#   bash plane-pocketbase/tests/frontend/build-workspace-packages.sh
#   PLANE_FE_DIR=/path/to/clone bash …        # build a fresh clone's packages
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PB="$(cd "$HERE/../.." && pwd)"
FE="$(cd "${PLANE_FE_DIR:-$PB/../plane-preview}" && pwd)"

cd "$FE" || exit 1
[ -d node_modules ] || { echo "[pkgs] FATAL: run install-deps.sh first" >&2; exit 1; }

# Pick a node binary: the managed runtime is not necessarily on PATH when this
# script is called from a plain Git Bash window.
NODE_BIN="${NODE_BIN:-node}"

"$NODE_BIN" "$HERE/build-packages.mjs"
