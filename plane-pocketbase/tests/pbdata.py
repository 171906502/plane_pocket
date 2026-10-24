#!/usr/bin/env python3
"""Data helpers for the backup/restore drill (tests/backup-drill.sh).

Subcommands:
  seed <db> <storage-dir> [live-dir]
                                 insert a deterministic dataset + storage files
                                 (+ one live log when live-dir is given)
  fingerprint <db> <storage-dir> print a content fingerprint (sha256) of the DB
                                 and the storage tree, plus per-table row counts
  hashdir <dir>                  print `dir_sha256`/`files`/`bytes` for a tree
                                 (used to compare live/ before and after restore)
  counts <db> [table ...]        print `count.<table>=<n>` for a fixed table set
  hold-wal <db> <rows> <marker> <seconds>
                                 open the DB in WAL mode with autocheckpoint OFF,
                                 insert <rows> probe rows, write <marker>, then
                                 hold the connection open for <seconds> so the
                                 rows stay in the -wal file rather than the main
                                 DB file. This is what makes the drill decisive:
                                 a naive `cp data.db` must MISS those rows while
                                 an online backup must have them.

Everything is deterministic so fingerprints compare across a backup/restore round
trip. Nothing here talks to the app; it is pure SQLite.
"""

import hashlib
import os
import sqlite3
import sys
import time

PROBE_TABLE = "_drill_wal_probe"

# Tables whose row counts land in the backup manifest (readable, meaningful).
MANIFEST_TABLES = [
    "users",
    "workspaces",
    "workspace_members",
    "projects",
    "states",
    "issues",
    "pages",
    "issue_comments",
    "cycles",
    "modules",
    "labels",
]


def connect(path, **kw):
    return sqlite3.connect(path, **kw)


def tables(cur):
    return [r[0] for r in cur.execute(
        "select name from sqlite_master where type='table' and name not like 'sqlite_%' "
        "order by name")]


def canon(value):
    """Stable, unambiguous text form for a single SQLite value."""
    if value is None:
        return "N"
    if isinstance(value, bytes):
        return "B" + value.hex()
    if isinstance(value, float):
        return "F" + repr(value)
    if isinstance(value, int):
        return "I" + str(value)
    return "T" + str(value)


def db_fingerprint(path):
    """sha256 over every row of every table, ordered by rowid.

    Ordering by rowid (not by a business key) keeps the fingerprint sensitive to
    insert order too, so a restore that reshuffled rows would be caught.
    """
    conn = connect(path, uri=path.startswith("file:"))
    conn.row_factory = None
    cur = conn.cursor()
    h = hashlib.sha256()
    counts = {}
    for table in tables(cur):
        n = 0
        # rowid is absent on WITHOUT ROWID tables; fall back to unordered scan.
        try:
            rows = cur.execute('select * from "%s" order by rowid' % table)
        except sqlite3.OperationalError:
            rows = cur.execute('select * from "%s"' % table)
        h.update(("T" + table).encode())
        for row in rows:
            h.update("\x1e".join(canon(v) for v in row).encode())
            h.update(b"\n")
            n += 1
        counts[table] = n
    conn.close()
    return h.hexdigest(), counts


def storage_fingerprint(root):
    """sha256 over the storage tree: relative path + contents, sorted."""
    h = hashlib.sha256()
    n = 0
    total = 0
    if not os.path.isdir(root):
        return h.hexdigest(), 0, 0
    for dirpath, dirnames, filenames in os.walk(root):
        dirnames.sort()
        for name in sorted(filenames):
            full = os.path.join(dirpath, name)
            rel = os.path.relpath(full, root).replace(os.sep, "/")
            h.update(rel.encode())
            h.update(b"\x1f")
            with open(full, "rb") as fh:
                data = fh.read()
            h.update(hashlib.sha256(data).digest())
            h.update(b"\n")
            n += 1
            total += len(data)
    return h.hexdigest(), n, total


def uvarint(n):
    """Go's binary.PutUvarint — the live log's record length prefix."""
    out = bytearray()
    while True:
        b = n & 0x7F
        n >>= 7
        if n:
            out.append(b | 0x80)
        else:
            out.append(b)
            return bytes(out)


def seed_live(live, document_name):
    """Write one realistic live log + its `.name` sidecar.

    Format (internal/live/store.go): a flat sequence of `[varuint len][bytes]`.
    The sidecar holds the document name so the orphan scan can tell whether the
    log still belongs to a page — the filename itself is sanitised and hashed.
    """
    os.makedirs(live, exist_ok=True)
    log_path = os.path.join(live, document_name + "-drillhash.log")
    with open(log_path, "wb") as fh:
        for rec in (b"yjs-update-one", b"yjs-update-two"):
            fh.write(uvarint(len(rec)))
            fh.write(rec)
    with open(log_path + ".name", "w", encoding="utf-8") as fh:
        fh.write(document_name)


def cmd_seed(db, storage, live=None):
    conn = connect(db)
    cur = conn.cursor()
    now = "2026-10-15T00:00:00Z"

    # tokenKey is uniquely indexed with no partial WHERE clause, so empty
    # strings collide — every seeded user needs its own.
    cur.execute(
        "insert into users (id,uuid,email,display_name,first_name,last_name,username,"
        "tokenKey,is_active,password,created_at,updated_at,last_active) "
        "values ('u1','11111111-1111-4111-8111-111111111111','alice@example.com',"
        "'Alice','Alice','A','alice','tk-alice-0001',1,'x','%s','%s','%s')"
        % (now, now, now))
    cur.execute(
        "insert into users (id,uuid,email,display_name,username,tokenKey,is_active,"
        "password,created_at,updated_at,last_active) "
        "values ('u2','22222222-2222-4222-8222-222222222222','bob@example.com',"
        "'Bob','bob','tk-bob-0002',1,'x','%s','%s','%s')" % (now, now, now))

    cur.execute(
        "insert into workspaces (id,uuid,name,slug,owner,created_by,created_at,updated_at) "
        "values ('w1','aaaaaaaa-1111-4111-8111-aaaaaaaaaaaa','Drill WS','drill-ws',"
        "'u1','u1','%s','%s')" % (now, now))
    cur.execute(
        "insert into workspace_members (id,uuid,workspace,member,role,is_active,"
        "created_at,updated_at) values "
        "('wm1','bbbbbbbb-1111-4111-8111-bbbbbbbbbbbb','w1','u1',20,1,'%s','%s')"
        % (now, now))

    cur.execute(
        "insert into projects (id,uuid,workspace,name,identifier,created_by,"
        "created_at,updated_at) values "
        "('p1','cccccccc-1111-4111-8111-cccccccccccc','w1','Drill Project','DRL','u1',"
        "'%s','%s')" % (now, now))

    for i, (gname, g) in enumerate([("backlog", "backlog"), ("started", "started"),
                                    ("completed", "completed")]):
        cur.execute(
            "insert into states (id,uuid,workspace,project,name,slug,\"group\",color,"
            "sequence,created_at,updated_at) values "
            "('s%d','dddddddd-000%d-4000-8000-dddddddddddd','w1','p1','%s','%s','%s',"
            "'#fff',%d,'%s','%s')" % (i, i, gname, gname, g, i, now, now))

    for i in range(1, 13):
        cur.execute(
            "insert into issues (id,uuid,workspace,project,name,state,priority,"
            "sequence_id,sort_order,created_by,created_at,updated_at) values "
            "('i%d','eeeeeeee-0000-4000-8000-%012d','w1','p1','Issue %d','s0',"
            "'medium',%d,%d,'u1','%s','%s')" % (i, i, i, i, i, now, now))

    cur.execute(
        "insert into pages (id,uuid,workspace,name,owned_by,access,created_by,"
        "created_at,updated_at) values "
        "('pg1','ffffffff-1111-4111-8111-ffffffffffff','w1','Private Page','u1',1,"
        "'u1','%s','%s')" % (now, now))

    conn.commit()
    conn.close()

    # Storage tree: a top-level file, a nested dir, and a binary blob.
    os.makedirs(os.path.join(storage, "drill", "nested"), exist_ok=True)
    with open(os.path.join(storage, "drill", "note.txt"), "w", encoding="utf-8") as fh:
        fh.write("attachment payload\n")
    with open(os.path.join(storage, "drill", "nested", "blob.bin"), "wb") as fh:
        fh.write(bytes(range(256)) * 4)

    # Collaborative page body: the log is the source of truth for a page's
    # content, so a backup that skips live/ restores a page whose body is only
    # as recent as its last snapshot.
    if live:
        seed_live(live, "ffffffff-1111-4111-8111-ffffffffffff")
    print("seeded")


def cmd_fingerprint(db, storage):
    dbf, counts = db_fingerprint(db)
    stf, nfiles, nbytes = storage_fingerprint(storage)
    print("db_sha256=%s" % dbf)
    print("storage_sha256=%s" % stf)
    print("storage_files=%d" % nfiles)
    print("storage_bytes=%d" % nbytes)
    print("total_rows=%d" % sum(counts.values()))
    for t in sorted(counts):
        if counts[t]:
            print("count.%s=%d" % (t, counts[t]))


def cmd_counts(db, wanted):
    conn = connect(db)
    cur = conn.cursor()
    for t in wanted:
        try:
            n = cur.execute('select count(*) from "%s"' % t).fetchone()[0]
        except sqlite3.OperationalError:
            n = -1
        print("count.%s=%d" % (t, n))
    conn.close()


def cmd_probe_count(db):
    conn = connect(db)
    try:
        n = conn.execute('select count(*) from "%s"' % PROBE_TABLE).fetchone()[0]
    except sqlite3.OperationalError:
        n = -1
    conn.close()
    print("probe_rows=%d" % n)


def cmd_hold_wal(db, rows, marker, seconds):
    conn = connect(db)
    cur = conn.cursor()
    cur.execute("pragma journal_mode=WAL")
    # Without this the rows would be checkpointed into data.db on commit and the
    # naive-copy negative control would stop discriminating.
    cur.execute("pragma wal_autocheckpoint=0")
    cur.execute(
        'create table if not exists "%s" (id integer primary key autoincrement, v text)'
        % PROBE_TABLE)
    cur.executemany('insert into "%s" (v) values (?)' % PROBE_TABLE,
                    [("probe-%d" % i,) for i in range(int(rows))])
    conn.commit()
    wal = db + "-wal"
    with open(marker, "w", encoding="utf-8") as fh:
        fh.write("rows=%s\nwal_bytes=%s\n" % (rows, os.path.getsize(wal)
                                              if os.path.exists(wal) else 0))
    time.sleep(float(seconds))
    conn.close()


def cmd_snapshot(src, dst):
    """Consistent online snapshot via SQLite's backup API.

    This is the python equivalent of `sqlite3 src ".backup dst"`. It reads
    through the WAL, so it is safe while the server is running — unlike `cp`.
    """
    if os.path.exists(dst):
        os.remove(dst)
    source = connect(src, uri=src.startswith("file:"))
    target = connect(dst)
    with target:
        source.backup(target)
    target.close()
    source.close()
    print("snapshot %s -> %s" % (src, dst))


def cmd_sha256(path):
    h = hashlib.sha256()
    with open(path, "rb") as fh:
        for chunk in iter(lambda: fh.read(1 << 20), b""):
            h.update(chunk)
    print(h.hexdigest())


def cmd_hashdir(path):
    """Content hash of any directory tree — used for storage/ and live/.

    Deliberately the same walk as storage_fingerprint, so "the backup has the
    same files with the same bytes" is one comparison rather than a file count.
    """
    h, n, total = storage_fingerprint(path)
    print("dir_sha256=%s" % h)
    print("files=%d" % n)
    print("bytes=%d" % total)


def main(argv):
    if len(argv) < 2:
        print(__doc__)
        return 2
    cmd = argv[1]
    if cmd == "seed":
        # The optional third argument is the live/ directory (collaborative page
        # logs). Older callers pass only db + storage and get no live tree.
        cmd_seed(argv[2], argv[3], argv[4] if len(argv) > 4 else None)
    elif cmd == "fingerprint":
        cmd_fingerprint(argv[2], argv[3])
    elif cmd == "hashdir":
        cmd_hashdir(argv[2])
    elif cmd == "counts":
        cmd_counts(argv[2], argv[3:] or MANIFEST_TABLES)
    elif cmd == "probe-count":
        cmd_probe_count(argv[2])
    elif cmd == "hold-wal":
        cmd_hold_wal(argv[2], argv[3], argv[4], argv[5])
    elif cmd == "snapshot":
        cmd_snapshot(argv[2], argv[3])
    elif cmd == "sha256":
        cmd_sha256(argv[2])
    else:
        print("unknown subcommand: %s" % cmd)
        return 2
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
