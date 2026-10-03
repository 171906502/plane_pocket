import sqlite3
import sys

db = sys.argv[1] if len(sys.argv) > 1 else "file:.fe-run/data.db?mode=ro"
c = sqlite3.connect(db, uri=True)
cur = c.cursor()
for t in ["users", "workspaces", "projects", "issues", "states", "issue_comments", "pages", "views"]:
    try:
        print(t, "=", cur.execute("select count(*) from " + t).fetchone()[0])
    except Exception as e:
        print(t, "ERR", e)
print("--- workspaces ---")
for r in cur.execute("select id,uuid,name,slug from workspaces"):
    print(r)
print("--- projects ---")
for r in cur.execute("select id,uuid,name,identifier,workspace from projects"):
    print(r)
print("--- states ---")
for r in cur.execute("select uuid,name,group_name,project from states"):
    print(r)
print("--- issues ---")
for r in cur.execute("select uuid,name,sequence_id,project from issues"):
    print(r)
