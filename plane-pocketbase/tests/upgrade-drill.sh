#!/usr/bin/env bash
# Upgrade drill — what actually happens when you deploy a new build over a
# populated database, and how to roll back. Evidence behind docs/03 §3/§4 and
# the N-9 row in docs/04 §9.
#
#   bash tests/upgrade-drill.sh [--keep-artifacts]
#
# What it establishes, with assertions rather than opinion:
#   A. re-running `migrate up` on an up-to-date DB is a no-op (deploy is safe)
#   B. the app boots and serves traffic against the migrated DB
#   C. `migrate down` DESTROYS data — so it is not a rollback strategy
#   D. restoring the pre-upgrade backup IS the rollback, and reproduces the
#      pre-upgrade state exactly
#   E. a lost migration-history row (partial restore / botched deploy) is
#      recoverable, and what `migrate up` does when it happens
#   F. `migrate history-sync` drops references to migrations that no longer exist
set -u

HERE="$(cd "$(dirname "$0")" && pwd)"
PB="$(cd "$HERE/.." && pwd)"
cd "$PB" || exit 1

KEEP_ARTIFACTS=0
[ "${1:-}" = "--keep-artifacts" ] && KEEP_ARTIFACTS=1

WORK=".upgrade-$(date +%s)-$$"
DATA="$WORK/data"
DEST="$WORK/backups"
BIN="./.upgrade-bin.exe"
EVIDENCE="$HERE/evidence"
PORT=8187

PASS=0
FAIL=0
SERVER=""
cleanup() { [ -n "$SERVER" ] && kill "$SERVER" 2>/dev/null; return 0; }
trap cleanup EXIT

step() { printf '\n\033[1m== %s ==\033[0m\n' "$*"; }
ok()   { printf '\033[32mPASS\033[0m %s\n' "$*"; PASS=$((PASS + 1)); }
bad()  { printf '\033[31mFAIL\033[0m %s\n' "$*"; FAIL=$((FAIL + 1)); }
note() { printf '     %s\n' "$*"; }
die()  { printf '\033[31mABORT\033[0m %s\n' "$*" >&2; exit 1; }
rm_path() { [ -e "$1" ] && rm -rf "$1"; return 0; }

resolve_python() {
  if [ -n "${PYTHON:-}" ] && command -v "$PYTHON" >/dev/null 2>&1; then echo "$PYTHON"; return; fi
  if command -v python >/dev/null 2>&1 && python -c "" </dev/null 2>/dev/null; then echo python
  elif command -v py >/dev/null 2>&1; then echo py
  else echo ""; fi
}
PY="$(resolve_python)"
[ -n "$PY" ] || die "python not found"
DBTOOL() { "$PY" "$HERE/pbdata.py" "$@"; }

# Row count of a table in the live data dir; -1 when the table is gone.
rows() { "$PY" "$HERE/pbdata.py" counts "$DATA/data.db" "$1" | sed 's/.*=//'; }
mig_count() {
  "$PY" -c "import sqlite3,sys; print(sqlite3.connect(sys.argv[1]).execute('select count(*) from _migrations').fetchone()[0])" \
    "$DATA/data.db" 2>/dev/null || echo 0
}

rm_path "$WORK"
rm_path "$BIN"
mkdir -p "$WORK" "$EVIDENCE"

LOG="$EVIDENCE/upgrade-drill-$(date -u +%Y%m%dT%H%M%SZ).txt"
exec > >(tee "$LOG") 2>&1

echo "plane-pocketbase upgrade drill"
echo "started: $(date -u +%Y-%m-%dT%H:%M:%SZ)"
echo "migrations in tree: $(ls -1 pb_migrations/*.go 2>/dev/null | wc -l | tr -d ' ')"

# ── 1. a deployed, populated installation ──────────────────────────────────
step "1. build + migrate + seed (simulates a running installation)"
go build -o "$BIN" . || die "build failed"
"$BIN" migrate up --dir="$DATA" >"$WORK/migrate-1.log" 2>&1 || die "initial migrate failed"
FIRST_APPLIED="$(grep -c '^Applied' "$WORK/migrate-1.log" || true)"
DBTOOL seed "$DATA/data.db" "$DATA/storage" >/dev/null || die "seed failed"
DBTOOL fingerprint "$DATA/data.db" "$DATA/storage" > "$WORK/fp-before.txt"
F0_DB="$(sed -n 's/^db_sha256=//p' "$WORK/fp-before.txt")"
F0_ST="$(sed -n 's/^storage_sha256=//p' "$WORK/fp-before.txt")"
note "applied=$FIRST_APPLIED migrations, _migrations rows=$(mig_count), issues=$(rows issues)"
[ "$FIRST_APPLIED" -gt 0 ] && ok "installation created ($FIRST_APPLIED migrations)" \
                           || bad "no migrations were applied"
[ "$(rows issues)" = "12" ] && ok "seeded data present (12 issues)" || bad "seed data missing"

# ── 2. pre-upgrade backup = the rollback point ─────────────────────────────
step "2. pre-upgrade backup"
bash "$HERE/backup.sh" --data-dir "$DATA" --dest "$DEST" --keep 5 --label pre-upgrade --quiet \
  > "$WORK/backup.out" 2>&1
BK="$(sed -n 's/^BACKUP_DIR=//p' "$WORK/backup.out")"
[ -n "$BK" ] && [ -f "$BK/MANIFEST.txt" ] && ok "rollback point: $BK" \
                                         || { bad "pre-upgrade backup failed"; cat "$WORK/backup.out"; }

# ── A. the common upgrade: new build, same schema ──────────────────────────
step "A. re-run 'migrate up' with the new build (no-op expected)"
if "$BIN" migrate up --dir="$DATA" >"$WORK/migrate-2.log" 2>&1; then
  REAPPLIED="$(grep -c '^Applied' "$WORK/migrate-2.log" || true)"
  [ "${REAPPLIED:-0}" -eq 0 ] && ok "0 migrations re-applied — deploy is idempotent" \
                              || bad "$REAPPLIED migrations re-applied unexpectedly"
else
  bad "'migrate up' failed on an up-to-date database"
fi
DBTOOL fingerprint "$DATA/data.db" "$DATA/storage" > "$WORK/fp-after-up.txt"
if [ "$(sed -n 's/^db_sha256=//p' "$WORK/fp-after-up.txt")" = "$F0_DB" ]; then
  ok "data untouched by the upgrade"
else
  bad "data changed during a no-op upgrade"
fi

# ── B. the app actually serves traffic on the migrated DB ──────────────────
step "B. boot the app against the migrated database"
# Origins must be explicit: with no --origins the server falls back to PocketBase's
# "*" default, and the production CORS guard (internal/config/startup.go) refuses
# to boot — a wildcard cannot be combined with credentialed requests. localhost and
# 127.0.0.1 are two different origins to a browser, so list both (D-8).
"$BIN" serve --http="127.0.0.1:$PORT" --dir="$DATA" \
  --origins="http://127.0.0.1:$PORT,http://localhost:$PORT" >"$WORK/serve.log" 2>&1 &
SERVER=$!
CODE=""
for _ in $(seq 1 60); do
  CODE="$(curl -s -m 3 -o "$WORK/instances.json" -w '%{http_code}' \
    "http://127.0.0.1:$PORT/api/instances/" 2>/dev/null || true)"
  [ "$CODE" = "200" ] && break
  sleep 0.5
done
if [ "$CODE" = "200" ]; then
  ok "server booted and answered 200 on /api/instances/ ($(wc -c < "$WORK/instances.json" | tr -d ' ') bytes)"
else
  bad "server did not answer 200 (last status '$CODE') — see $WORK/serve.log"
  sed -n '1,15p' "$WORK/serve.log" | sed 's/^/     /'
fi
kill "$SERVER" 2>/dev/null; wait "$SERVER" 2>/dev/null; SERVER=""

# ── C. `migrate down` is destructive — not a rollback strategy ─────────────
step "C. 'migrate down' as a rollback strategy (expected: DESTRUCTIVE)"
N="$(mig_count)"
# `migrate down` asks for interactive confirmation, so it must be fed a 'y' —
# which is itself a finding: it is not usable in an unattended rollback.
# `timeout` guards the drill against a future prompt we do not anticipate.
if yes y 2>/dev/null | timeout 180 "$BIN" migrate down "$N" --dir="$DATA" \
     >"$WORK/migrate-down.log" 2>&1; then
  note "'migrate down' prompted for confirmation and accepted 'y' (interactive-only)"
  AFTER_DOWN="$(rows issues)"
  if [ "$AFTER_DOWN" != "12" ]; then
    ok "reverting $N migrations destroyed application data (issues=$AFTER_DOWN, was 12)"
    note "=> rollback must be 'restore the backup', never 'migrate down'"
  else
    bad "revert did not touch application data — conclusion C is unsupported"
  fi
else
  note "'migrate down' exited non-zero; tail:"
  tail -5 "$WORK/migrate-down.log" | sed 's/^/     /'
  bad "could not characterise 'migrate down' behaviour"
fi

# ── D. the real rollback: restore the pre-upgrade backup ───────────────────
step "D. rollback by restoring the pre-upgrade backup"
bash "$HERE/restore.sh" "$BK" --data-dir "$DATA" > "$WORK/restore.out" 2>&1
RC=$?
[ "$RC" -eq 0 ] && ok "restore exited 0" || { bad "restore exited $RC"; sed 's/^/     /' "$WORK/restore.out"; }
DBTOOL fingerprint "$DATA/data.db" "$DATA/storage" > "$WORK/fp-rolled.txt"
if [ "$(sed -n 's/^db_sha256=//p' "$WORK/fp-rolled.txt")" = "$F0_DB" ] \
   && [ "$(sed -n 's/^storage_sha256=//p' "$WORK/fp-rolled.txt")" = "$F0_ST" ]; then
  ok "rollback reproduced the pre-upgrade state exactly (db + storage)"
else
  bad "rollback did not reproduce the pre-upgrade state"
  diff "$WORK/fp-before.txt" "$WORK/fp-rolled.txt" | head -10 | sed 's/^/     /'
fi
[ "$(rows issues)" = "12" ] && ok "application data recovered (12 issues)" || bad "issues not recovered"

# ── E. lost migration-history row ─────────────────────────────────────────
step "E. recovery when a migration-history row is missing"
LAST="$(ls -1 pb_migrations/*.go | sort | tail -1 | xargs -n1 basename)"
note "simulating: '$LAST' applied in the DB but its history row is gone"
"$PY" -c "
import sqlite3,sys
c=sqlite3.connect(sys.argv[1]); c.execute('delete from _migrations where file=?',(sys.argv[2],)); c.commit()
print('deleted rows:', c.total_changes)
" "$DATA/data.db" "$LAST"
if "$BIN" migrate up --dir="$DATA" >"$WORK/migrate-reapply.log" 2>&1; then
  RE="$(grep -c '^Applied' "$WORK/migrate-reapply.log" || true)"
  note "'migrate up' exited 0 and re-applied $RE migration(s) — idempotent for this migration"
else
  note "'migrate up' exited non-zero (re-applying a snapshot migration on an existing schema):"
  tail -4 "$WORK/migrate-reapply.log" | sed 's/^/     /'
fi
bash "$HERE/restore.sh" "$BK" --data-dir "$DATA" > "$WORK/restore2.out" 2>&1 \
  || bad "could not recover from the lost-history state"
DBTOOL fingerprint "$DATA/data.db" "$DATA/storage" > "$WORK/fp-e.txt"
if [ "$(sed -n 's/^db_sha256=//p' "$WORK/fp-e.txt")" = "$F0_DB" ]; then
  ok "backup restore recovers from a lost history row"
else
  bad "restore did not recover from the lost-history state"
fi

# ── F. history-sync drops dangling references ─────────────────────────────
step "F. 'migrate history-sync'"
"$PY" -c "
import sqlite3,sys
c=sqlite3.connect(sys.argv[1])
c.execute(\"insert into _migrations (file, applied) values ('0000000000_ghost_migration.go', 1)\")
c.commit(); c.close()
" "$DATA/data.db"
BEFORE="$(mig_count)"
if "$BIN" migrate history-sync --dir="$DATA" >"$WORK/history-sync.log" 2>&1; then
  AFTER="$(mig_count)"
  if [ "$AFTER" -lt "$BEFORE" ]; then
    ok "history-sync dropped the dangling reference ($BEFORE -> $AFTER rows)"
  else
    bad "history-sync left the dangling reference ($BEFORE -> $AFTER)"
  fi
else
  bad "history-sync failed"
  tail -4 "$WORK/history-sync.log" | sed 's/^/     /'
fi

# ── verdict ─────────────────────────────────────────────────────────────────
step "verdict"
printf '\033[1m%d passed, %d failed\033[0m\n' "$PASS" "$FAIL"
echo "evidence: $LOG"

if [ "$FAIL" -eq 0 ] && [ "$KEEP_ARTIFACTS" -eq 0 ]; then
  rm_path "$WORK"
  rm_path "$BIN"
  LEFT=""
  [ -e "$WORK" ] && LEFT="$LEFT $WORK"
  [ -e "$BIN" ] && LEFT="$LEFT $BIN"
  if [ -n "$LEFT" ]; then
    echo "(scratch not fully removed on this host — safe to delete by hand:$LEFT)"
  else
    echo "(scratch removed; evidence kept)"
  fi
else
  echo "(scratch kept at $WORK for inspection)"
fi

sleep 0.5
[ "$FAIL" -eq 0 ]
