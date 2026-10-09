#!/usr/bin/env bash
# Shared Node resolver. `source` this file, then call `resolve_node` — it sets $NODE.
#
# Why this exists instead of a literal path in each script:
#
#   The managed toolchain lives in a *versioned* directory whose suffix changes on
#   every runtime upgrade. Three scripts had this hardcoded:
#
#     /c/Users/cao/.workbuddy-ai/binaries/node/versions/22.22.2-3/node.exe
#
#   When the runtime moved to 22.22.2-6 that path stopped existing, and
#   start-stack.sh degraded *quietly*: it printed a one-line warning and carried
#   on without launching `apps/live`. Nothing failed loudly — but `apps/live` is
#   required by the Pages rich-text editor. Without it Plane falls back to
#   `usePageFallback`, whose effect re-runs on every `page.description_html`
#   change, so "save on disconnect" never converges and the app loops
#   PATCH/GET on `.../pages/<id>/description/` forever (defect #10 in
#   docs/07-STATE-AND-ROADMAP.md).
#
#   Resolve it instead of hardcoding it. Preference order:
#     1. $PLANE_NODE                  — an explicit override always wins
#     2. newest dir under versions/   — `sort -V`, so 22.22.2-10 beats 22.22.2-6
#     3. versions/current
#     4. `node` on PATH
#     5. the standard Windows install location
#
#   Sets NODE to the resolved path, or to "" when nothing usable was found (and
#   says so on stderr). Callers should test for empty rather than assume.

resolve_node() {
  local root="/c/Users/cao/.workbuddy-ai/binaries/node/versions"
  local cand=""

  # 1. explicit override
  if [ -n "${PLANE_NODE:-}" ] && [ -x "${PLANE_NODE:-}" ]; then
    NODE="$PLANE_NODE"
    return 0
  fi

  # 2. newest managed version that actually has a node.exe.
  #
  # `sort -V` (version sort) is essential: a plain lexical sort puts 22.22.2-10
  # *before* 22.22.2-6, so the "last" entry would be the older runtime. The glob
  # keeps a literal `*` when the directory is empty, hence the 2>/dev/null.
  if [ -d "$root" ]; then
    cand="$(ls -1d "$root"/*/ 2>/dev/null \
            | while IFS= read -r d; do
                # $d already ends in "/", so this avoids a doubled slash
                [ -x "${d}node.exe" ] && printf '%s\n' "${d}node.exe"
              done \
            | sort -V | tail -1)"
  fi

  # 3. the `current` alias
  if [ -z "$cand" ] && [ -x "$root/current/node.exe" ]; then
    cand="$root/current/node.exe"
  fi

  # 4. whatever is on PATH
  if [ -z "$cand" ] && command -v node >/dev/null 2>&1; then
    cand="$(command -v node)"
  fi

  # 5. the usual Windows install location
  if [ -z "$cand" ] && [ -x "/c/Program Files/nodejs/node.exe" ]; then
    cand="/c/Program Files/nodejs/node.exe"
  fi

  NODE="$cand"
  if [ -z "$NODE" ]; then
    echo "[node-env] WARNING: no usable node found — apps/live and the browser tests will not run." >&2
    echo "[node-env] Set PLANE_NODE=/path/to/node.exe to override." >&2
  fi
  return 0
}
