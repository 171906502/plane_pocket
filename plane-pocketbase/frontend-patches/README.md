# frontend-patches

Everything this deployment needs on top of upstream Plane, expressed as patches
against a **pristine** `plane-preview/` checkout. Nothing in the vendored tree
is edited in place any more: the build applies these, builds, and reverts.

See `docs/12-FRONTEND-DIFF.md` for why each one exists.

## The four patches

| # | Patch | Files | Why |
|---|---|---|---|
| 0001 | `tailwind-import-subpath.patch` | 1 | Vite 8's vendored `postcss-import` cannot resolve the bare `tailwindcss` specifier during the SSR pass (`ENOENT …/apps/web/tailwindcss`). Upstream hits it too. The subpath is semantically identical. |
| 0002 | `space-spa-mode.patch` | 4 | `apps/space` must build as a static SPA: `ssr:false`, and the `headers`/`loader` route exports that SPA mode rejects are gone (the Go binary sends those headers instead). |
| 0003 | `collab-description-sync.patch` | 2 | The Go collaboration relay is a byte relay — it never parses CRDT, so it cannot derive `description_html`. The editor writes the three formats back itself, debounced 1.5 s, off the same tiptap transaction that produces the WebSocket frame. |
| 0004 | `csrf-header-injection.patch` | 2 | Our auth middleware does Django-style CSRF double-submit (`X-CSRFTOKEN`), so the axios client must echo the `csrftoken` cookie on unsafe methods. |

Total: **9 files, 4 hunks of behaviour.** Everything else in `plane-preview/`
is byte-identical to upstream.

## Usage

```bash
bash plane-pocketbase/frontend-patches/apply.sh            # apply (idempotent)
bash plane-pocketbase/frontend-patches/apply.sh status     # clean | applied | conflict
bash plane-pocketbase/frontend-patches/apply.sh check      # dry run
bash plane-pocketbase/frontend-patches/apply.sh revert     # back to pristine

# against another tree, e.g. a fresh clone used for verification
bash apply.sh apply --target /path/to/fresh/plane
```

`build-frontends.sh` does this automatically: it applies, builds, and reverts —
but only if the tree was clean when it started, so it will not clobber a dev
session that is running with the patches applied.

## Applying them to a fresh upstream clone

```bash
git clone --depth 1 git@github.com:makeplane/plane.git /tmp/plane
cd /tmp/plane && git checkout e72bf10fa     # the commit plane-preview is pinned to
bash /path/to/frontend-patches/apply.sh apply --target /tmp/plane
git status --short                          # exactly the 9 files above
```

Or let the script do the whole thing — clone at `BASE`, materialise the one
symlink Windows cannot create, apply, assert the file set, install, build all
three SPAs, and assert the tree is clean again:

```bash
bash plane-pocketbase/tests/frontend/verify-upstream-patches.sh
bash plane-pocketbase/tests/frontend/verify-upstream-patches.sh --no-build   # clone + apply only
```

## Bumping upstream

```bash
# 1. re-vendor the new commit (see docs/12 §4 for the exact procedure)
# 2. try to re-apply
bash apply.sh check
# 3. if a patch no longer applies, refresh just that one:
cd plane-preview && git apply -R 0002-space-spa-mode.patch   # or hand-edit
# ... make the change again, then regenerate:
#    git -C <clone-at-new-base> diff -- <paths> > 0002-space-spa-mode.patch
```

A patch that no longer applies **fails the build loudly** — that is the point of
doing it this way instead of carrying a fork.
