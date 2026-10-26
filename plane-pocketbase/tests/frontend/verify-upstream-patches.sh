#!/usr/bin/env bash
# Prove that `frontend-patches/` reconstructs this deployment's frontend from a
# clean upstream clone — no vendored tree, no manual edits.
#
#   clone makeplane/plane at the pinned commit (frontend-patches/BASE)
#   → materialise the one symlink Windows cannot create
#   → apply the patch set
#   → assert git reports exactly the expected 9 files
#   → install deps and build all three SPAs out of the clone
#   → revert, assert clean again
#
# Usage:
#   bash plane-pocketbase/tests/frontend/verify-upstream-patches.sh            # full
#   bash plane-pocketbase/tests/frontend/verify-upstream-patches.sh --no-build # clone+apply only
#   WORK_DIR=/tmp/x bash .../verify-upstream-patches.sh                       # pick the clone path
#
# The clone is kept afterwards so the result can be inspected; delete WORK_DIR
# to reclaim the space.
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PB="$(cd "$HERE/../.." && pwd)"
PATCHES="$PB/frontend-patches"

DO_BUILD=1
[ "${1:-}" = "--no-build" ] && DO_BUILD=0

BASE_SHA="$(grep '^sha=' "$PATCHES/BASE" | cut -d= -f2)"
BASE_BRANCH="$(grep '^branch=' "$PATCHES/BASE" | cut -d= -f2)"
REPO_URL="${PLANE_REPO_URL:-git@github.com:makeplane/plane.git}"
WORK="${WORK_DIR:-$PB/tests/logs/upstream-verify}"

EXPECTED="apps/space/app/issues/[anchor]/layout.tsx
apps/space/app/page.tsx
apps/space/app/root.tsx
apps/space/react-router.config.ts
apps/web/components/pages/editor/page-root.tsx
apps/web/hooks/use-collab-description-sync.ts
apps/web/services/api.service.ts
packages/services/src/api.service.ts
packages/tailwind-config/index.css"

say() { printf '\n[verify] %s\n' "$*"; }
die() { printf '\n[verify] FATAL: %s\n' "$*" >&2; exit 1; }

# Content-level change set: tracked modifications + untracked files.
#
# ⚠️ Deliberately NOT `git status --porcelain`. Once `packages/i18n/locales` is
# a real directory where upstream has a symlink, the index refresh for that one
# path fails, and git then reports *stale stat info* for unrelated paths as
# well — after a build we got 10 `packages/*/package.json` listed as modified
# while `git diff` on them was completely empty (same bytes, same line endings).
# `diff --name-only` and `ls-files -o` compare content, so they cannot lie that
# way.
changed_paths() {
  {
    git -C "$WORK/plane" diff --name-only
    git -C "$WORK/plane" ls-files --others --exclude-standard
  } 2>/dev/null | grep -v '^packages/i18n/locales' | sort || true
}

# ── 1. clone ────────────────────────────────────────────────────────────────
# An existing clone is **reused**, not deleted: `rm -rf` of a 5000-file tree
# trips the sandbox's bulk-delete guard, and re-cloning every run just burns
# bandwidth. `checkout -f` + `clean -fd` put it back to pristine — note the
# missing `-x`, so the (gitignored) `node_modules` survives and a second run
# skips the slow install.
export GIT_SSH_COMMAND="ssh -o BatchMode=yes -o ConnectTimeout=20"

if [ -d "$WORK/plane/.git" ]; then
  say "reusing the clone at $WORK/plane (checkout -f + clean -fd)"
  git -C "$WORK/plane" checkout -q -f "$BASE_SHA" \
    || die "the existing clone has no commit $BASE_SHA — delete $WORK and rerun"
  git -C "$WORK/plane" clean -qfd
else
  say "cloning $REPO_URL @ $BASE_SHA"
  mkdir -p "$WORK"
  git clone -q --depth 1 --branch "$BASE_BRANCH" "$REPO_URL" "$WORK/plane"
  if ! git -C "$WORK/plane" cat-file -e "$BASE_SHA^{commit}" 2>/dev/null; then
    say "commit not in the shallow window; fetching it directly"
    git -C "$WORK/plane" fetch -q --depth 1 origin "$BASE_SHA" \
      || die "cannot fetch $BASE_SHA — pass PLANE_REPO_URL=https://… if SSH is blocked"
  fi
  git -C "$WORK/plane" checkout -q "$BASE_SHA"
fi
say "checked out $(git -C "$WORK/plane" log -1 --format='%h %ad %s' --date=short | cut -c1-70)"

# ── 2. the one thing a Windows clone cannot reproduce ───────────────────────
# `packages/i18n/locales` is a symlink to `src/locales` upstream. Without
# developer mode git materialises it as a text file, and every locale import
# fails. Materialise it as a real directory instead. `git clean -fd` above
# removes it (it is untracked), so this runs on every pass.
if [ -f "$WORK/plane/packages/i18n/locales" ]; then
  say "materialising packages/i18n/locales (symlink → real directory)"
  rm -f "$WORK/plane/packages/i18n/locales"
fi
if [ ! -d "$WORK/plane/packages/i18n/locales" ]; then
  cp -a "$WORK/plane/packages/i18n/src/locales" "$WORK/plane/packages/i18n/locales"
fi

# ── 3. apply ────────────────────────────────────────────────────────────────
say "applying $(ls "$PATCHES"/0*.patch | wc -l) patches"
bash "$PATCHES/apply.sh" apply --target "$WORK/plane"

# ── 4. assert the change set is exactly the expected one ────────────────────
actual="$(changed_paths)"
expected="$(printf '%s\n' "$EXPECTED" | sort)"
if [ "$actual" != "$expected" ]; then
  echo "--- expected ---" >&2; printf '%s\n' "$expected" >&2
  echo "--- actual ---" >&2;   printf '%s\n' "$actual" >&2
  die "the patched tree does not match the expected file set"
fi
say "patch set touches exactly the expected $(printf '%s\n' "$EXPECTED" | wc -l) files"

if [ "$DO_BUILD" = "0" ]; then
  say "clone kept at $WORK/plane (--no-build)"
  exit 0
fi

# ── 5. revert before building ───────────────────────────────────────────────
# `build-frontends.sh` applies the patches itself and reverts them on exit —
# but only when it finds the tree clean. Leaving them applied here would send
# it down the "already applied, this must be a dev session, leave them" branch
# and the post-build assertion below would fail. So hand the tree back pristine
# and let the build do the apply/build/revert cycle for real.
say "reverting — build-frontends.sh will apply/build/revert on its own"
bash "$PATCHES/apply.sh" revert --target "$WORK/plane"
[ "$(bash "$PATCHES/apply.sh" status --target "$WORK/plane")" = "clean" ] \
  || die "tree is not clean after revert"

# ── 6. deps + build, out of the clone ───────────────────────────────────────
say "installing dependencies (this is the slow part)"
PLANE_FE_DIR="$WORK/plane" bash "$PB/tests/frontend/install-deps.sh"

say "building all three SPAs from the clone"
PLANE_FE_DIR="$WORK/plane" bash "$PB/tests/browser/build-frontends.sh"

# ── 7. the build reverts the patches itself; assert that happened ───────────
say "post-build status (patches must be gone again)"
leftover="$(changed_paths)"
if [ -n "$leftover" ]; then
  printf '%s\n' "$leftover" >&2
  die "the build left the tree patched — the EXIT trap did not fire"
fi

say "PASS — a clean clone + frontend-patches/ builds all three bundles"
say "clone kept at $WORK/plane"
