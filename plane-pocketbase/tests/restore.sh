#!/usr/bin/env bash
# Restore — put a backup back in place, verifying it first.
#
#   bash tests/restore.sh <backup-dir> [--data-dir DIR] [--force]
#
# Safety rules (project convention: never destroy data)
#   * Every *.db is checked against the sha256 in MANIFEST.txt BEFORE anything is
#     touched. A mismatch aborts unless --force.
#   * The existing data directory is MOVED ASIDE to <data-dir>.bak-<epoch>,
#     never deleted. Recovering from a bad restore is therefore a `mv` back.
#   * Row counts are re-checked after the restore against the manifest.
set -u

HERE="$(cd "$(dirname "$0")" && pwd)"
PB="$(cd "$HERE/.." && pwd)"
cd "$PB" || exit 1

BK=""
DATA_DIR="pb_data"
FORCE=0

while [ $# -gt 0 ]; do
  case "$1" in
    --data-dir) DATA_DIR="$2"; shift 2 ;;
    --force)    FORCE=1; shift ;;
    -*) echo "unknown flag: $1" >&2; exit 2 ;;
    *)  BK="$1"; shift ;;
  esac
done

log() { printf '%s\n' "$*"; }
die() { printf '\033[31mFAIL\033[0m %s\n' "$*" >&2; exit 1; }
ok()  { printf '\033[32mOK\033[0m %s\n' "$*"; }

resolve_python() {
  if [ -n "${PYTHON:-}" ] && command -v "$PYTHON" >/dev/null 2>&1; then echo "$PYTHON"; return; fi
  if command -v python >/dev/null 2>&1 && python -c "" </dev/null 2>/dev/null; then echo python
  elif command -v py >/dev/null 2>&1; then echo py
  else echo ""; fi
}
PY="$(resolve_python)"
[ -n "$PY" ] || die "python is required (manifest parsing, hashing, counts)"

[ -n "$BK" ] || die "usage: restore.sh <backup-dir> [--data-dir DIR] [--force]"
[ -d "$BK" ] || die "backup dir not found: $BK"
MANIFEST="$BK/MANIFEST.txt"
[ -f "$MANIFEST" ] || die "no MANIFEST.txt in $BK — refusing to restore an unverified backup"

TAR_FILE="$BK/data-dir.tar.gz"
IS_TAR=0
[ -f "$TAR_FILE" ] && [ ! -f "$BK/data.db" ] && IS_TAR=1

# ── 1. verify checksums before touching anything ────────────────────────────
log "verifying $MANIFEST"
BAD=0
COUNT=0
while read -r hash name; do
  case "$hash" in sha256:*) ;; *) continue ;; esac
  [ -f "$BK/$name" ] || { echo "  MISSING $name"; BAD=$((BAD + 1)); continue; }
  want="${hash#sha256:}"
  got="$("$PY" "$HERE/pbdata.py" sha256 "$BK/$name")"
  if [ "$want" != "$got" ]; then
    echo "  MISMATCH $name (want ${want:0:12}…, got ${got:0:12}…)"
    BAD=$((BAD + 1))
  else
    COUNT=$((COUNT + 1))
  fi
done < "$MANIFEST"

if [ "$IS_TAR" -eq 0 ] && [ "$COUNT" -eq 0 ]; then
  die "manifest lists no snapshots — nothing to restore"
fi
if [ "$BAD" -gt 0 ] && [ "$FORCE" -ne 1 ]; then
  die "$BAD file(s) failed verification; re-run with --force only if you accept a corrupt restore"
fi
[ "$BAD" -gt 0 ] && log "WARNING: proceeding past $BAD failed checks (--force)"
[ "$COUNT" -gt 0 ] && ok "verified $COUNT snapshot(s)"

# ── 2. move the current data dir aside (never delete) ───────────────────────
if [ -e "$DATA_DIR" ]; then
  ASIDE="$DATA_DIR.bak-$(date +%s)"
  mv "$DATA_DIR" "$ASIDE" || die "could not move $DATA_DIR aside"
  ok "existing data dir moved aside: $ASIDE"
fi
mkdir -p "$DATA_DIR" || die "could not create $DATA_DIR"

# ── 3. restore ──────────────────────────────────────────────────────────────
if [ "$IS_TAR" -eq 1 ]; then
  tar xzf "$TAR_FILE" || die "tar extraction failed"
  ok "restored from $TAR_FILE"
else
  for f in "$BK"/*.db; do
    [ -e "$f" ] || continue
    cp "$f" "$DATA_DIR/" || die "copy failed: $f"
  done
  [ -f "$DATA_DIR/data.db" ] || die "restored dir has no data.db"
  ok "restored $(ls -1 "$BK"/*.db | wc -l | tr -d ' ') database file(s)"
fi

if [ -d "$BK/storage" ]; then
  mkdir -p "$DATA_DIR/storage"
  cp -R "$BK/storage/." "$DATA_DIR/storage/" || die "storage restore failed"
  ok "restored storage/ ($(find "$DATA_DIR/storage" -type f | wc -l | tr -d ' ') files)"
else
  log "backup has no storage/ — leaving storage/ empty"
fi

# Page bodies: without live/, a page opens from its last description_binary
# snapshot (or blank if it never compacted). Restoring it is not optional.
if [ -d "$BK/live" ]; then
  mkdir -p "$DATA_DIR/live"
  cp -R "$BK/live/." "$DATA_DIR/live/" || die "live restore failed"
  ok "restored live/ ($(find "$DATA_DIR/live" -type f | wc -l | tr -d ' ') files)"
  WANT_LIVE="$(sed -n 's/^live_sha256=//p' "$MANIFEST")"
  if [ -n "$WANT_LIVE" ]; then
    GOT_LIVE="$("$PY" "$HERE/pbdata.py" hashdir "$DATA_DIR/live" | sed -n 's/^dir_sha256=//p')"
    if [ "$WANT_LIVE" = "$GOT_LIVE" ]; then
      ok "live/ tree matches the manifest checksum"
    elif [ "$FORCE" -eq 1 ]; then
      log "WARNING: live/ differs from the manifest (--force)"
    else
      die "live/ does not match the manifest checksum — page bodies may be truncated"
    fi
  fi
else
  log "backup has no live/ — page bodies fall back to their stored snapshots"
fi

# ── 4. re-check row counts against the manifest ─────────────────────────────
if [ "$IS_TAR" -eq 0 ]; then
  WANT="$(grep '^count\.' "$MANIFEST" | sort)"
  GOT="$("$PY" "$HERE/pbdata.py" counts "$DATA_DIR/data.db" | grep '^count\.' | sort)"
  if [ "$WANT" = "$GOT" ]; then
    ok "row counts match the manifest ($(printf '%s\n' "$WANT" | grep -c '^count\.') tables)"
  else
    echo "--- expected ---"; printf '%s\n' "$WANT"
    echo "--- actual ---";   printf '%s\n' "$GOT"
    die "row counts differ from the manifest"
  fi
fi

ok "restore complete into $DATA_DIR"
