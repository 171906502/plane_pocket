#!/usr/bin/env bash
# Backup/restore drill — proves the backup is restorable, on a real database.
#
#   bash tests/backup-drill.sh [--keep-artifacts]
#
# This is the evidence behind docs/04 §6 and the "备份任务已配置，且验证过恢复
# 流程" line in the go-live checklist. It does not mock anything: it migrates a
# real data dir, seeds real rows, then breaks things and restores them.
#
# The drill's decisive step is the WAL test. A writer holds a connection open
# with wal_autocheckpoint=0 so committed rows live ONLY in data.db-wal. Then:
#   * a naive `cp data.db` must MISS those rows   (negative control — proves the
#     drill discriminates, i.e. it would catch the "just copy the file" mistake)
#   * an online backup must HAVE them             (the actual requirement)
#
# Assertions
#   1. migrate up on a fresh dir succeeds
#   2. naive copy of a live DB loses WAL-only rows
#   3. online backup preserves them
#   4. backup manifest checksums verify
#   5. corruption after the backup is real (fingerprint must change)
#   6. restore reproduces the pre-backup fingerprint EXACTLY
#   7. the restored DB is a valid app database (migrate up is a no-op)
#   8. a tampered backup is rejected (no silent corrupt restore)
#   9. retention keeps exactly --keep backups
#
# It also covers live/ (the Yjs relay's page-body logs). A page's content lives
# there, not in the database, so the drill deletes live/ along with the rest and
# requires the restore to bring it back byte-for-byte. Without that assertion a
# backup script that skipped live/ would still pass every other step.
set -u

HERE="$(cd "$(dirname "$0")" && pwd)"
PB="$(cd "$HERE/.." && pwd)"
cd "$PB" || exit 1

KEEP_ARTIFACTS=0
[ "${1:-}" = "--keep-artifacts" ] && KEEP_ARTIFACTS=1

# A per-run scratch dir: deleting a fixed path first is unreliable on hosts
# where `rm -rf` is wrapped by a fail-closed trash shim (it errors instead of
# being a no-op, and can leave stale state behind).
WORK=".drill-$(date +%s)-$$"
DATA="$WORK/data"
DEST="$WORK/backups"
BIN="./.drill-bin.exe"
EVIDENCE="$HERE/evidence"
KEEP=3
HOLD_SECONDS=30
PROBE_ROWS=200

PASS=0
FAIL=0
HOLDER=""
# The WAL holder is a background process; make sure an early abort does not
# leave it holding the database.
cleanup() { [ -n "$HOLDER" ] && kill "$HOLDER" 2>/dev/null; return 0; }
trap cleanup EXIT

step() { printf '\n\033[1m== %s ==\033[0m\n' "$*"; }
ok()   { printf '\033[32mPASS\033[0m %s\n' "$*"; PASS=$((PASS + 1)); }
bad()  { printf '\033[31mFAIL\033[0m %s\n' "$*"; FAIL=$((FAIL + 1)); }
note() { printf '     %s\n' "$*"; }
die()  { printf '\033[31mABORT\033[0m %s\n' "$*" >&2; exit 1; }

resolve_python() {
  if [ -n "${PYTHON:-}" ] && command -v "$PYTHON" >/dev/null 2>&1; then echo "$PYTHON"; return; fi
  if command -v python >/dev/null 2>&1 && python -c "" </dev/null 2>/dev/null; then echo python
  elif command -v py >/dev/null 2>&1; then echo py
  else echo ""; fi
}
PY="$(resolve_python)"
[ -n "$PY" ] || die "python not found"

DBTOOL() { "$PY" "$HERE/pbdata.py" "$@"; }

mkdir -p "$EVIDENCE"
LOG="$EVIDENCE/backup-drill-$(date -u +%Y%m%dT%H%M%SZ).txt"
# Tee the whole drill into the evidence file.
exec > >(tee "$LOG") 2>&1

echo "plane-pocketbase backup/restore drill"
echo "started: $(date -u +%Y-%m-%dT%H:%M:%SZ)"
echo "python:  $($PY -V 2>&1)"
echo "sqlite3: $(command -v sqlite3 >/dev/null 2>&1 && sqlite3 --version || echo 'not installed (python path will be used)')"

# Guarded removal: on some hosts `rm -rf <missing-path>` is wrapped by a
# fail-closed trash shim that errors instead of being a no-op.
rm_path() { [ -e "$1" ] && rm -rf "$1"; return 0; }

rm_path "$WORK"
rm_path "$BIN"
mkdir -p "$WORK" "$EVIDENCE"

# ── 1. fresh migrate ────────────────────────────────────────────────────────
step "1. migrate up on a fresh data dir"
if go build -o "$BIN" . && "$BIN" migrate up --dir="$DATA" >"$WORK/migrate.log" 2>&1; then
  ok "binary built and schema applied ($(ls -1 pb_migrations/*.go | wc -l | tr -d ' ') migrations)"
else
  bad "migrate up failed — see $WORK/migrate.log"
  exit 1
fi

# ── 2. seed real rows + storage files ───────────────────────────────────────
step "2. seed data"
DBTOOL seed "$DATA/data.db" "$DATA/storage" "$DATA/live" >/dev/null || die "seed failed"
note "issues=$(DBTOOL counts "$DATA/data.db" issues | sed 's/.*=//') storage files=$(find "$DATA/storage" -type f | wc -l | tr -d ' ') live files=$(find "$DATA/live" -type f | wc -l | tr -d ' ')"
ok "seeded users/workspace/project/states/issues/page + storage tree + a live log"

# ── 3. hold a WAL open so committed rows live only in -wal ──────────────────
step "3. hold an uncheckpointed WAL open (the decisive setup)"
MARKER="$WORK/wal-marker"
"$PY" "$HERE/pbdata.py" hold-wal "$DATA/data.db" "$PROBE_ROWS" "$MARKER" "$HOLD_SECONDS" &
HOLDER=$!
for _ in $(seq 1 100); do [ -f "$MARKER" ] && break; sleep 0.1; done
[ -f "$MARKER" ] || die "WAL holder never signalled"
note "$(tr '\n' ' ' < "$MARKER")"
WAL_BYTES="$(sed -n 's/^wal_bytes=//p' "$MARKER")"
if [ "${WAL_BYTES:-0}" -gt 0 ]; then
  ok "data.db-wal is non-empty ($WAL_BYTES bytes) — rows are NOT in data.db"
else
  bad "WAL is empty; the naive-copy control below cannot discriminate"
fi

# ── 4. fingerprint the live state (seen through the WAL) ────────────────────
step "4. fingerprint live state"
DBTOOL fingerprint "$DATA/data.db" "$DATA/storage" > "$WORK/fp-live.txt"
LIVE_DB="$(sed -n 's/^db_sha256=//p' "$WORK/fp-live.txt")"
LIVE_ST="$(sed -n 's/^storage_sha256=//p' "$WORK/fp-live.txt")"
LIVE_ROWS="$(sed -n 's/^total_rows=//p' "$WORK/fp-live.txt")"
note "db=${LIVE_DB:0:16}… storage=${LIVE_ST:0:16}… total_rows=$LIVE_ROWS"
DBTOOL hashdir "$DATA/live" > "$WORK/fp-live-logs.txt"
LIVE_LV="$(sed -n 's/^dir_sha256=//p' "$WORK/fp-live-logs.txt")"
LIVE_LV_FILES="$(sed -n 's/^files=//p' "$WORK/fp-live-logs.txt")"
note "live=${LIVE_LV:0:16}… ($LIVE_LV_FILES files)"
[ "$(DBTOOL probe-count "$DATA/data.db" | sed 's/.*=//')" = "$PROBE_ROWS" ] \
  && ok "live DB exposes all $PROBE_ROWS WAL-only rows" \
  || bad "live DB does not expose the WAL-only rows"

# ── 5. negative control: a naive copy must lose them ───────────────────────
step "5. negative control — naive 'cp data.db' of a live DB"
cp "$DATA/data.db" "$WORK/naive-copy.db"
NAIVE="$(DBTOOL probe-count "$WORK/naive-copy.db" | sed 's/.*=//')"
if [ "$NAIVE" = "-1" ]; then
  NAIVE_DESC="table absent entirely (its CREATE also lived in the -wal)"
elif [ "$NAIVE" != "$PROBE_ROWS" ]; then
  NAIVE_DESC="probe_rows=$NAIVE of $PROBE_ROWS"
else
  NAIVE_DESC="probe_rows=$NAIVE of $PROBE_ROWS"
fi
if [ "$NAIVE" != "$PROBE_ROWS" ]; then
  ok "naive copy lost rows ($NAIVE_DESC) — the drill discriminates"
else
  bad "naive copy kept every row; this drill would NOT catch a file-copy backup"
fi

# ── 6. run the real backup while the writer is still open ───────────────────
step "6. backup.sh while the server-equivalent writer is open"
bash "$HERE/backup.sh" --data-dir "$DATA" --dest "$DEST" --keep "$KEEP" --quiet > "$WORK/backup.out" 2>&1
BK="$(sed -n 's/^BACKUP_DIR=//p' "$WORK/backup.out")"
if [ -n "$BK" ] && [ -f "$BK/MANIFEST.txt" ]; then
  ok "backup created: $BK"
  sed -n 's/^tool=/     tool: /p' "$BK/MANIFEST.txt"
else
  bad "backup.sh produced no manifest"; cat "$WORK/backup.out"; exit 1
fi
BKPROBE="$(DBTOOL probe-count "$BK/data.db" | sed 's/.*=//')"
[ "$BKPROBE" = "$PROBE_ROWS" ] \
  && ok "online backup HAS all $PROBE_ROWS WAL-only rows (naive copy: $NAIVE_DESC)" \
  || bad "online backup lost WAL rows (probe_rows=$BKPROBE of $PROBE_ROWS)"

# The database only holds a page's last snapshot; its body lives in live/.
if [ -d "$BK/live" ]; then
  DBTOOL hashdir "$BK/live" > "$WORK/fp-backup-logs.txt"
  BK_LV="$(sed -n 's/^dir_sha256=//p' "$WORK/fp-backup-logs.txt")"
  [ "$BK_LV" = "$LIVE_LV" ] \
    && ok "backup captured live/ byte-for-byte ($LIVE_LV_FILES files)" \
    || bad "backup live/ differs from the source"
else
  bad "backup has no live/ — a restored page would lose its body"
fi

# ── 7. checksums verify ─────────────────────────────────────────────────────
step "7. manifest checksums"
DBTOOL fingerprint "$BK/data.db" "$DATA/storage" > "$WORK/fp-backup.txt"
BK_DB="$(sed -n 's/^db_sha256=//p' "$WORK/fp-backup.txt")"
[ "$BK_DB" = "$LIVE_DB" ] \
  && ok "backup content fingerprint equals the live state" \
  || bad "backup content differs from live (backup=${BK_DB:0:16}… live=${LIVE_DB:0:16}…)"

# ── 8. stop the writer, then corrupt the live dir ───────────────────────────
step "8. corrupt the live data dir"
kill "$HOLDER" 2>/dev/null; wait "$HOLDER" 2>/dev/null
"$PY" - "$DATA/data.db" "$DATA/storage/drill/nested/blob.bin" "$DATA/live" <<'PYEOF'
import os, shutil, sqlite3, sys
db, blob, live = sys.argv[1], sys.argv[2], sys.argv[3]
c = sqlite3.connect(db)
c.execute("delete from issues where sequence_id > 5")   # rows vanish
c.execute("update users set display_name='CORRUPTED' where id='u1'")  # content changes
c.commit(); c.close()
os.remove(blob)                                          # an attachment vanishes
if os.path.isdir(live):
    shutil.rmtree(live)                                  # every page body vanishes
print("corrupted: 7 issues deleted, 1 user renamed, 1 storage file removed, live/ removed")
PYEOF
DBTOOL fingerprint "$DATA/data.db" "$DATA/storage" > "$WORK/fp-corrupt.txt"
COR_DB="$(sed -n 's/^db_sha256=//p' "$WORK/fp-corrupt.txt")"
COR_ST="$(sed -n 's/^storage_sha256=//p' "$WORK/fp-corrupt.txt")"
COR_LV="$(DBTOOL hashdir "$DATA/live" | sed -n 's/^dir_sha256=//p')"
if [ "$COR_DB" != "$LIVE_DB" ] && [ "$COR_ST" != "$LIVE_ST" ] && [ "$COR_LV" != "$LIVE_LV" ]; then
  ok "corruption is real (db, storage and live fingerprints all changed)"
else
  bad "corruption did not change every fingerprint — later assertions are vacuous"
fi

# ── 9. restore ──────────────────────────────────────────────────────────────
step "9. restore.sh"
bash "$HERE/restore.sh" "$BK" --data-dir "$DATA" > "$WORK/restore.out" 2>&1
RESTORE_RC=$?
sed -n 's/^/     /p' "$WORK/restore.out"
if [ "$RESTORE_RC" -eq 0 ]; then ok "restore exited 0"; else bad "restore exited $RESTORE_RC"; fi
ASIDE="$(ls -d "$DATA".bak-* 2>/dev/null | tail -1)"
[ -n "$ASIDE" ] && ok "old data dir preserved, not deleted: $ASIDE" \
                || bad "old data dir was not preserved"

# ── 10. the decisive assertion: exact reproduction ─────────────────────────
step "10. post-restore fingerprint must equal the pre-backup fingerprint"
DBTOOL fingerprint "$DATA/data.db" "$DATA/storage" > "$WORK/fp-restored.txt"
RES_DB="$(sed -n 's/^db_sha256=//p' "$WORK/fp-restored.txt")"
RES_ST="$(sed -n 's/^storage_sha256=//p' "$WORK/fp-restored.txt")"
if [ "$RES_DB" = "$LIVE_DB" ]; then
  ok "restored database is byte-for-byte equivalent in content"
else
  bad "restored database differs from pre-backup state"
  diff "$WORK/fp-live.txt" "$WORK/fp-restored.txt" | head -20 | sed 's/^/     /'
fi
[ "$RES_ST" = "$LIVE_ST" ] \
  && ok "restored storage tree matches (attachment recovered)" \
  || bad "restored storage tree differs"

RES_LV="$(DBTOOL hashdir "$DATA/live" | sed -n 's/^dir_sha256=//p')"
[ "$RES_LV" = "$LIVE_LV" ] \
  && ok "restored live/ matches (page bodies recoverable)" \
  || bad "restored live/ differs — page bodies would be lost"

# ── 11. restored DB is a valid app database ────────────────────────────────
step "11. migrate up on the restored dir (schema sanity)"
if "$BIN" migrate up --dir="$DATA" > "$WORK/migrate-after.log" 2>&1; then
  APPLIED="$(grep -c '^Applied' "$WORK/migrate-after.log" || true)"
  if [ "${APPLIED:-0}" -eq 0 ]; then
    ok "no migrations re-applied — schema is intact"
  else
    bad "$APPLIED migrations re-applied on the restored DB (schema drift)"
  fi
else
  bad "migrate up failed on the restored DB"
fi

# ── 12. a tampered backup must be rejected ─────────────────────────────────
step "12. tamper detection"
TAMPER="$WORK/tampered"
mkdir -p "$TAMPER"
cp -R "$BK/." "$TAMPER/"
printf 'x' >> "$TAMPER/data.db"
if bash "$HERE/restore.sh" "$TAMPER" --data-dir "$WORK/should-not-exist" > "$WORK/tamper.out" 2>&1; then
  bad "restore accepted a tampered backup"
else
  grep -q MISMATCH "$WORK/tamper.out" \
    && ok "tampered backup rejected on checksum mismatch" \
    || bad "restore failed but not on a checksum mismatch"
fi
[ -e "$WORK/should-not-exist" ] && bad "tamper test still wrote a data dir" \
                               || ok "nothing was written for the rejected backup"

# ── 13. retention ───────────────────────────────────────────────────────────
step "13. retention (--keep $KEEP)"
for lbl in a b c d; do
  bash "$HERE/backup.sh" --data-dir "$DATA" --dest "$DEST" --keep "$KEEP" --label "$lbl" --quiet >/dev/null 2>&1
done
COUNT="$(ls -1d "$DEST"/*/ 2>/dev/null | wc -l | tr -d ' ')"
[ "$COUNT" -eq "$KEEP" ] \
  && ok "exactly $KEEP backups retained after 5 runs" \
  || bad "expected $KEEP backups, found $COUNT"

# ── verdict ─────────────────────────────────────────────────────────────────
step "verdict"
printf '\033[1m%d passed, %d failed\033[0m\n' "$PASS" "$FAIL"
echo "evidence: $LOG"

if [ "$FAIL" -eq 0 ]; then
  rm_path "$WORK"
  rm_path "$BIN"
  # Cleanup is best-effort: some hosts route `rm -rf` through a trash shim that
  # declines bulk deletes. Report what is actually still on disk.
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

# Let the tee process flush before we exit, or the log loses its tail.
sleep 0.5

[ "$FAIL" -eq 0 ]
