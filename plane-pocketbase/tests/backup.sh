#!/usr/bin/env bash
# Backup — consistent snapshot of a plane-pocketbase data directory.
#
#   bash tests/backup.sh [--data-dir DIR] [--dest DIR] [--keep N] [--label NAME]
#                        [--tar] [--quiet]
#
# Defaults: --data-dir pb_data  --dest backups  --keep 7
#
# What it does
#   1. Snapshots every *.db with SQLite's ONLINE BACKUP API (sqlite3 `.backup`,
#      or the python equivalent). This reads through the WAL, so it is safe
#      while the server is running — `cp data.db` is NOT (docs/04 §6.2).
#   2. Copies storage/ (attachments, avatars, logos). Miss it and every
#      attachment is gone.
#   3. Copies live/ (the Yjs relay's append-only page-body logs). A page's
#      content lives HERE, not in the database: pages.description_binary is only
#      a periodic snapshot. Miss it and restored pages open blank or revert to
#      their last compaction (docs/04 §4.4).
#   4. Writes MANIFEST.txt: sha256 of each snapshot, key row counts, storage
#      file count/bytes, live file count, and the tool used. restore.sh verifies
#      against it.
#   5. Prunes old backups beyond --keep (only inside --dest, only our own
#      timestamped directories).
#
# --tar skips the online snapshot and archives the whole directory instead.
# Use it only when the service is stopped (the doc's "method 2"), e.g. when
# neither sqlite3 nor python is available on the host.
set -u

HERE="$(cd "$(dirname "$0")" && pwd)"
PB="$(cd "$HERE/.." && pwd)"
cd "$PB" || exit 1

DATA_DIR="pb_data"
DEST="backups"
KEEP=7
LABEL=""
TAR_MODE=0
QUIET=0

while [ $# -gt 0 ]; do
  case "$1" in
    --data-dir) DATA_DIR="$2"; shift 2 ;;
    --dest)     DEST="$2"; shift 2 ;;
    --keep)     KEEP="$2"; shift 2 ;;
    --label)    LABEL="$2"; shift 2 ;;
    --tar)      TAR_MODE=1; shift ;;
    --quiet)    QUIET=1; shift ;;
    *) echo "unknown flag: $1" >&2; exit 2 ;;
  esac
done

log() { [ "$QUIET" -eq 1 ] || printf '%s\n' "$*"; }
die() { printf '\033[31mFAIL\033[0m %s\n' "$*" >&2; exit 1; }

# ── python (used for hashing/counts, and for snapshots when sqlite3 is absent) ─
resolve_python() {
  if [ -n "${PYTHON:-}" ] && command -v "$PYTHON" >/dev/null 2>&1; then echo "$PYTHON"; return; fi
  if command -v python >/dev/null 2>&1 && python -c "" </dev/null 2>/dev/null; then echo python
  elif command -v py >/dev/null 2>&1; then echo py
  else echo ""; fi
}
PY="$(resolve_python)"

# ── validate inputs ─────────────────────────────────────────────────────────
[ -d "$DATA_DIR" ] || die "data dir not found: $DATA_DIR"
[ -f "$DATA_DIR/data.db" ] || die "no data.db in $DATA_DIR — wrong --data-dir?"

SQLITE3=""
command -v sqlite3 >/dev/null 2>&1 && SQLITE3="sqlite3"

if [ "$TAR_MODE" -eq 0 ] && [ -z "$SQLITE3" ] && [ -z "$PY" ]; then
  die "need either the sqlite3 CLI or python for a consistent snapshot; re-run with --tar (service stopped) if neither is available"
fi

STAMP="$(date -u +%Y%m%dT%H%M%SZ)"
NAME="$STAMP"
[ -n "$LABEL" ] && NAME="$STAMP-$LABEL"
BK="$DEST/$NAME"

mkdir -p "$BK" || die "cannot create $BK"

# ── snapshot ────────────────────────────────────────────────────────────────
snapshot() {
  src="$1"; dst="$2"
  if [ -n "$SQLITE3" ]; then
    "$SQLITE3" "$src" ".backup '$dst'" >/dev/null 2>&1
  else
    "$PY" "$HERE/pbdata.py" snapshot "$src" "$dst" >/dev/null
  fi
}

if [ "$TAR_MODE" -eq 1 ]; then
  log "archiving $DATA_DIR (tar mode — service must be stopped)"
  tar czf "$BK/data-dir.tar.gz" "$DATA_DIR" || die "tar failed"
  TOOL="tar"
else
  TOOL="sqlite3-cli"
  [ -z "$SQLITE3" ] && TOOL="python-backup-api"
  log "snapshotting databases from $DATA_DIR (tool: $TOOL)"
  for src in "$DATA_DIR"/*.db; do
    [ -e "$src" ] || continue
    base="$(basename "$src")"
    snapshot "$src" "$BK/$base" || die "snapshot failed for $base"
  done
  [ -f "$BK/data.db" ] || die "snapshot produced no data.db"
fi

# ── storage/ ────────────────────────────────────────────────────────────────
STORAGE_FILES=0
if [ -d "$DATA_DIR/storage" ]; then
  mkdir -p "$BK/storage"
  cp -R "$DATA_DIR/storage/." "$BK/storage/" || die "storage copy failed"
  STORAGE_FILES="$(find "$BK/storage" -type f | wc -l | tr -d ' ')"
  log "copied storage/ ($STORAGE_FILES files)"
else
  log "no storage/ directory — nothing to copy"
fi

# ── live/ ───────────────────────────────────────────────────────────────────
# A plain copy is safe here, unlike data.db: the log format is
# `[varuint len][bytes]` appended to the end, and the reader skips a truncated
# final record (internal/live/store.go). Worst case the newest update is missing;
# the page still opens with everything before it.
LIVE_FILES=0
if [ "$TAR_MODE" -eq 1 ]; then
  # The archive already holds live/; just report what went in.
  if [ -d "$DATA_DIR/live" ]; then
    LIVE_FILES="$(find "$DATA_DIR/live" -type f | wc -l | tr -d ' ')"
    log "live/ is inside data-dir.tar.gz ($LIVE_FILES files)"
  fi
elif [ -d "$DATA_DIR/live" ]; then
  mkdir -p "$BK/live"
  cp -R "$DATA_DIR/live/." "$BK/live/" || die "live copy failed"
  LIVE_FILES="$(find "$BK/live" -type f | wc -l | tr -d ' ')"
  log "copied live/ ($LIVE_FILES files)"
else
  log "no live/ directory — no collaborative page logs to copy"
fi

# ── manifest ────────────────────────────────────────────────────────────────
[ -n "$PY" ] || die "python is required to write the manifest"

MANIFEST="$BK/MANIFEST.txt"
{
  echo "# plane-pocketbase backup manifest"
  echo "created_at=$STAMP"
  echo "host=$(hostname 2>/dev/null || echo unknown)"
  echo "data_dir=$DATA_DIR"
  echo "tool=$TOOL"
  echo "storage_files=$STORAGE_FILES"
  echo "live_files=$LIVE_FILES"
  # One hash over the whole live tree: a file count would not notice a log that
  # was copied short. restore.sh recomputes this after copying.
  if [ -d "$BK/live" ]; then
    echo "live_sha256=$("$PY" "$HERE/pbdata.py" hashdir "$BK/live" | sed -n 's/^dir_sha256=//p')"
  fi
  echo "# snapshots (sha256 of each file; verified by tests/restore.sh)"
  for f in "$BK"/*.db; do
    [ -e "$f" ] || continue
    printf 'sha256:%s  %s\n' "$("$PY" "$HERE/pbdata.py" sha256 "$f")" "$(basename "$f")"
  done
  echo "# row counts (data.db)"
  if [ -f "$BK/data.db" ]; then
    "$PY" "$HERE/pbdata.py" counts "$BK/data.db"
  fi
} > "$MANIFEST" || die "manifest write failed"

log "manifest: $MANIFEST"

# ── retention ───────────────────────────────────────────────────────────────
# Only prune directories we created (timestamp-shaped names) inside --dest.
PRUNED=0
if [ "$KEEP" -gt 0 ] && [ -d "$DEST" ]; then
  OLD="$(ls -1 "$DEST" 2>/dev/null \
    | grep -E '^[0-9]{8}T[0-9]{6}Z(-[A-Za-z0-9._-]+)?$' \
    | sort -r | tail -n +$((KEEP + 1)))"
  for old in $OLD; do
    case "$old" in
      "$NAME") continue ;;
      */*|..|.) continue ;;
    esac
    [ -d "$DEST/$old" ] || continue
    rm -rf "$DEST/$old"
    PRUNED=$((PRUNED + 1))
    log "pruned old backup: $old"
  done
fi

log "backup complete: $BK (kept $KEEP, pruned $PRUNED)"
printf 'BACKUP_DIR=%s\n' "$BK"
