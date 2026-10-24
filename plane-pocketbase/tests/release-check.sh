#!/usr/bin/env bash
# Release check — chains docs/05-TESTING.md §9's pre-release acceptance flow.
#
# Static layer (default, no services needed):
#   1. go build ./...
#   2. go vet ./internal/...
#   3. go test ./internal/...            (skipped with --fast; the router
#                                         anchor guard always runs)
#   4. migration self-check on a fresh data dir
#   5. both gap audits compared against recorded baselines
#
# Dynamic layer (opt-in):
#   --full         additionally runs tests/browser/run-all.sh --fresh  (dev stack:
#                  starts 5 services on 8000/3000/3001/3002/3100, moves .fe-run aside)
#   --full-single  additionally runs tests/browser/run-chain-single.sh --fresh
#                  (single-port: starts serve-single.sh itself against a **scratch**
#                   data dir, runs run1→run5, stops it. Needs :8000 free; your
#                   .fe-run is never touched.)
#
# Audit baselines are the classified remainder from 2026-10-11 (excluded
# endpoints + `epics` ghosts + ternary artifacts — see docs/07 §3.1/§3.3).
# A higher UNSERVED count than baseline means a regression; override via env:
#   PATH_UNSERVED_BASELINE=26 METHOD_UNSERVED_BASELINE=49 METHOD_MISSING_BASELINE=0
#
# Usage:
#   bash tests/release-check.sh               # static layer
#   bash tests/release-check.sh --fast        # skip the full unit suite
#   bash tests/release-check.sh --full        # static layer + dev-stack browser chain
#   bash tests/release-check.sh --full-single # static layer + single-port browser chain
set -u

HERE="$(cd "$(dirname "$0")" && pwd)"
PB="$(cd "$HERE/.." && pwd)"
cd "$PB" || exit 1

FAST=0
FULL=0
SINGLE=0
for arg in "$@"; do
  case "$arg" in
    --fast) FAST=1 ;;
    --full) FULL=1 ;;
    --full-single) FULL=1; SINGLE=1 ;;
    *) echo "unknown flag: $arg"; exit 2 ;;
  esac
done

PATH_UNSERVED_BASELINE="${PATH_UNSERVED_BASELINE:-26}"
METHOD_UNSERVED_BASELINE="${METHOD_UNSERVED_BASELINE:-49}"
METHOD_MISSING_BASELINE="${METHOD_MISSING_BASELINE:-0}"
# The audits walk the whole frontend tree (~31k files). 120s is fine on an idle
# machine (~25s observed) but a concurrent `go build`/`go test` storm can starve
# it; when the timeout fires the output is empty and the parse looks like a
# regression. Override with AUDIT_TIMEOUT=<seconds> if you must run in parallel.
AUDIT_TIMEOUT="${AUDIT_TIMEOUT:-180}"

PASS=0
FAIL=0

step() { printf '\n\033[1m== %s ==\033[0m\n' "$1"; }
ok()   { printf '\033[32mPASS\033[0m %s\n' "$1"; PASS=$((PASS + 1)); }
bad()  { printf '\033[31mFAIL\033[0m %s\n' "$1"; FAIL=$((FAIL + 1)); }

resolve_python() {
  # Explicit override first. Note the `echo`s below: `then python` would
  # execute bare python and drop into the interactive REPL.
  if [ -n "${PYTHON:-}" ] && command -v "$PYTHON" >/dev/null 2>&1; then echo "$PYTHON"; return; fi
  if command -v python >/dev/null 2>&1 && python -c "" </dev/null 2>/dev/null; then echo python
  elif command -v py >/dev/null 2>&1; then echo py
  else echo ""; fi
}

# ── 1. build ────────────────────────────────────────────────────────────────
step "L1 build"
if go build ./...; then ok "go build ./..."; else bad "go build ./..."; fi

# ── 2. vet ──────────────────────────────────────────────────────────────────
step "go vet"
if go vet ./internal/...; then ok "go vet ./internal/..."; else bad "go vet ./internal/..."; fi

# ── 3. unit tests ───────────────────────────────────────────────────────────
step "L2/L4 unit tests"
if [ "$FAST" -eq 1 ]; then
  if go test ./internal/router/ -run TestRoutePatternsAreAnchored; then
    ok "router anchor guard (--fast)"
  else
    bad "router anchor guard (--fast)"
  fi
else
  if go test ./internal/...; then ok "go test ./internal/..."; else bad "go test ./internal/..."; fi
fi

# ── 4. migration self-check on a fresh data dir ─────────────────────────────
step "L3 migration self-check"
BIN="./.release-check.exe"
TMPDIR_DATA=".tmp-release-check"
rm -f "$BIN"
rm -rf "$TMPDIR_DATA"
if go build -o "$BIN" . && "$BIN" migrate up --dir="$TMPDIR_DATA"; then
  ok "migrate up on fresh data dir"
else
  bad "migrate up on fresh data dir"
fi
rm -f "$BIN"
rm -rf "$TMPDIR_DATA"

# ── 5. gap audits vs baselines ──────────────────────────────────────────────
step "L5 gap audits"
PY="$(resolve_python)"
if [ -z "$PY" ]; then
  bad "no python found (need it for the audit scripts)"
else
  AUDIT_OUT="$(timeout "$AUDIT_TIMEOUT" "$PY" tests/browser/audit-endpoints.py </dev/null 2>&1)"
  REGISTERED="$(printf '%s\n' "$AUDIT_OUT" | sed -n 's/^registered patterns[ :]*\([0-9]*\).*/\1/p')"
  PATH_UNSERVED="$(printf '%s\n' "$AUDIT_OUT" | sed -n 's/^UNSERVED[ :]*\([0-9]*\).*/\1/p')"
  echo "registered patterns: $REGISTERED, path UNSERVED: $PATH_UNSERVED (baseline $PATH_UNSERVED_BASELINE)"
  if [ -n "$PATH_UNSERVED" ] && [ "$PATH_UNSERVED" -le "$PATH_UNSERVED_BASELINE" ]; then
    ok "path audit within baseline"
  else
    # An empty parse means the audit did not produce parsable output (timeout,
    # python error, …) — that is NOT a path regression, so say so loudly instead
    # of reporting a phantom baseline breach.
    if [ -z "$PATH_UNSERVED" ]; then
      echo "--- audit-endpoints.py raw output (unparsable) ---"
      printf '%s\n' "$AUDIT_OUT" | head -40
      echo "--- end ---"
      bad "path audit produced no parsable output (timeout=${AUDIT_TIMEOUT}s; rerun without concurrent builds)"
    else
      bad "path audit regressed (see output above; new shapes are NOT auto-classified)"
    fi
  fi

  AUDIT_OUT="$(timeout "$AUDIT_TIMEOUT" "$PY" tests/browser/audit-methods.py </dev/null 2>&1)"
  METHOD_UNSERVED="$(printf '%s\n' "$AUDIT_OUT" | sed -n 's/^UNSERVED paths[ :]*\([0-9]*\).*/\1/p')"
  METHOD_MISSING="$(printf '%s\n' "$AUDIT_OUT" | sed -n 's/^METHOD-MISSING[ :]*\([0-9]*\).*/\1/p')"
  echo "method UNSERVED: $METHOD_UNSERVED (baseline $METHOD_UNSERVED_BASELINE), METHOD-MISSING: $METHOD_MISSING (baseline $METHOD_MISSING_BASELINE)"
  if [ -n "$METHOD_UNSERVED" ] && [ "$METHOD_UNSERVED" -le "$METHOD_UNSERVED_BASELINE" ] \
    && [ -n "$METHOD_MISSING" ] && [ "$METHOD_MISSING" -le "$METHOD_MISSING_BASELINE" ]; then
    ok "method audit within baseline"
  elif [ -z "$METHOD_UNSERVED" ] || [ -z "$METHOD_MISSING" ]; then
    echo "--- audit-methods.py raw output (unparsable) ---"
    printf '%s\n' "$AUDIT_OUT" | head -40
    echo "--- end ---"
    bad "method audit produced no parsable output (timeout=${AUDIT_TIMEOUT}s; rerun without concurrent builds)"
  else
    bad "method audit regressed (see output above)"
  fi
fi

# ── 6. dynamic layer (opt-in) ───────────────────────────────────────────────
if [ "$FULL" -eq 1 ]; then
  if [ "$SINGLE" -eq 1 ]; then
    step "L6/L7 browser chain (run-chain-single.sh --fresh, single-port :8000)"
    echo "(needs :8000 free — it starts serve-single.sh against a scratch data dir; .fe-run untouched)"
    if bash tests/browser/run-chain-single.sh --fresh; then
      ok "browser workflow chain (single-port)"
    else
      bad "browser workflow chain (single-port; check tests/logs/)"
    fi
  else
    step "L6/L7 browser chain (run-all.sh --fresh)"
    if bash tests/browser/run-all.sh --fresh; then
      ok "browser workflow chain"
    else
      bad "browser workflow chain (check tests/logs/ and evidence/)"
    fi
  fi
  echo "(human review: walk tests/evidence/screenshots/ — 'screenshot without error' proves nothing, docs/05 §5.2)"
fi

# ── verdict ─────────────────────────────────────────────────────────────────
printf '\n\033[1m== %d passed, %d failed ==\033[0m\n' "$PASS" "$FAIL"
[ "$FAIL" -eq 0 ]
