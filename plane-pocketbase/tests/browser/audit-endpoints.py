"""Audit: which endpoints does the Plane frontend call that our router does not serve?

Why this exists: before every route was anchored with `{$}` (see
internal/router/patterns_test.go), any unregistered `/api/workspaces/{slug}/…`
path was silently answered by the *workspace detail* handler with HTTP 200.
Several endpoints the frontend depends on were therefore never implemented and
nobody noticed — the client happily consumed a workspace object where it expected
something else. Anchoring the patterns turned those into honest 404s, which is
correct but exposes the real gap.

This script diffs the two sides statically so the whole gap is visible at once,
instead of discovering one 404 per browser run.

Usage: python tests/browser/audit-endpoints.py
"""

import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
FE = ROOT.parent / "plane-preview" / "apps" / "web"
ROUTER = ROOT / "internal" / "router"

ROUTE_METHODS = ("GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS", "HEAD")

# `${expr}` / `{param}` / `:id` all collapse to a single placeholder so the two
# sides can be compared structurally.
PLACEHOLDER = re.compile(r"\$\{[^}]*\}|\{[^}]*\}")
FE_CALL = re.compile(r"""["'`](/api/[^"'`\n]*)["'`]""")


def normalise(path: str) -> str:
    # `{$}` is ServeMux's exact-path anchor, not a path segment — drop it before
    # splitting so `/api/users/me/{$}` compares equal to `/api/users/me/`.
    path = path.replace("{$}", "")
    path = PLACEHOLDER.sub("{}", path)
    path = path.split("?")[0]
    if not path.endswith("/"):
        path += "/"
    return path


def frontend_calls() -> dict[str, set[str]]:
    """Collect `/api/...` literals the web app asks for, with their source files."""
    calls: dict[str, set[str]] = {}
    for path in FE.rglob("*.ts*"):
        if "node_modules" in path.parts:
            continue
        try:
            src = path.read_text(encoding="utf-8", errors="ignore")
        except OSError:
            continue
        for raw in FE_CALL.findall(src):
            # Only paths whose literal part is meaningful: a template that ends
            # in `${...}` tells us nothing about the shape.
            if "${" in raw and raw.rstrip("/").endswith("}"):
                continue
            calls.setdefault(normalise(raw), set()).add(str(path.relative_to(FE)))
    return calls


def logical_lines(text: str) -> list[str]:
    """Join physical lines into statements.

    Group declarations are routinely wrapped:

        proj := workspaceGroup(e, app).
            Group("/projects/{project_id}").
            BindFunc(middleware.ResolveProject(app))

    A per-line scan sees `proj := workspaceGroup(e, app).` and gives up, so every
    route registered on `proj` loses its prefix and looks unserved. Joining on
    trailing "." / unbalanced parens recovers the real declaration.
    """
    out: list[str] = []
    buf = ""
    for raw in text.splitlines():
        # Drop line comments, but not the "//" in "http://".
        line = re.sub(r"(?<!:)//.*$", "", raw).strip()
        if not line:
            continue
        buf = f"{buf} {line}".strip() if buf else line
        if buf.endswith(".") or buf.count("(") > buf.count(")"):
            continue
        out.append(buf)
        buf = ""
    if buf:
        out.append(buf)
    return out


def registered_routes() -> set[str]:
    """Resolve group prefixes + route paths into full ServeMux patterns."""
    prefixes: dict[str, str] = {}
    routes: set[str] = set()

    # `\s*` around the dot: logical_lines joins wrapped statements with a space,
    # so the declaration reads `workspaceGroup(e, app). Group("...")`.
    group_decl = re.compile(r"(\w+)\s*:=\s*(.+?)\.\s*Group\(\s*\"([^\"]*)\"\s*\)")
    plain_decl = re.compile(r"(\w+)\s*:=\s*(e\.Router)\b")
    ws_decl = re.compile(r"(\w+)\s*:=\s*workspaceGroup\(")
    # `workspaceGroup(e, app).GET(...)` — the receiver is a call, not a variable.
    # Without this case the generic pattern below matches the *last* identifier
    # before the dot (`app`), whose prefix is unknown, so the route is recorded
    # as bare `/cycles/` and every workspace-scoped call looks unserved.
    ws_group_call = re.compile(r"workspaceGroup\([^)]*\)\.(" + "|".join(ROUTE_METHODS) + r")\(")
    call_start = re.compile(r"(\w+|e\.Router)\.(" + "|".join(ROUTE_METHODS) + r")\(")
    string_lit = re.compile(r'"((?:[^"\\]|\\.)*)"')

    for path in sorted(ROUTER.glob("*.go")):
        if path.name.endswith("_test.go"):
            continue
        for line in logical_lines(path.read_text(encoding="utf-8")):
            if m := group_decl.search(line):
                var, parent, suffix = m.group(1), m.group(2).strip(), m.group(3)
                # `parent` may be a variable (`ws`, `proj`) or the
                # `workspaceGroup(e, app)` call itself, which always resolves to
                # the workspace prefix.
                if parent.startswith("workspaceGroup("):
                    base = "/api/workspaces/{slug}"
                else:
                    base = prefixes.get(parent, "")
                prefixes[var] = base + suffix
                continue
            if m := plain_decl.search(line):
                prefixes[m.group(1)] = ""
                continue
            if m := ws_decl.search(line):
                prefixes[m.group(1)] = "/api/workspaces/{slug}"
                continue

            if ws_group_call.search(line):
                prefix = "/api/workspaces/{slug}"
                first_arg = line[ws_group_call.search(line).end() :].split(",", 1)[0]
                route_path = "".join(string_lit.findall(first_arg))
                if route_path:
                    routes.add(normalise(prefix + route_path))
                continue

            if m := call_start.search(line):
                recv = m.group(1)
                # The path is the first argument; it may be a concatenation
                # (`"/" + base + "/{pk}/{$}"`), so join every literal in it. The
                # interleaved identifiers are the loop variables those literals
                # are stitched around, and the doubled slashes they leave behind
                # collapse when the path is split into segments.
                first_arg = line[m.end() :].split(",", 1)[0]
                route_path = "".join(string_lit.findall(first_arg))
                if not route_path:
                    continue
                prefix = "" if recv == "e.Router" else prefixes.get(recv, "")
                routes.add(normalise(prefix + route_path))

    return routes


def matches(call: str, routes: set[str]) -> bool:
    """A call is served if some registered pattern matches it segment by segment."""
    call_segs = [s for s in call.split("/") if s]
    for route in routes:
        route_segs = [s for s in route.split("/") if s]
        if len(route_segs) != len(call_segs):
            continue
        if all(r == "{}" or r == c for r, c in zip(route_segs, call_segs)):
            return True
    return False


def main() -> int:
    routes = registered_routes()
    calls = frontend_calls()

    missing = sorted(c for c in calls if not matches(c, routes))

    print(f"registered patterns : {len(routes)}")
    print(f"frontend call shapes: {len(calls)}")
    print(f"UNSERVED            : {len(missing)}\n")

    for call in missing:
        sources = sorted(calls[call])
        print(f"  {call}")
        for s in sources[:2]:
            print(f"      <- {s}")

    return 0


if __name__ == "__main__":
    sys.exit(main())
