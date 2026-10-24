"""Method-level audit: which frontend calls hit a registered path with an HTTP
method the router does not serve?

Why this exists: `audit-endpoints.py` diffs *paths* only, so a path that is
registered with one method (say GET) looks fully served even when the frontend
also calls POST/PATCH/DELETE on it — the exact defect class behind bugs #23
(#issue-labels) and #24 (#archive). This script diffs (method, path) pairs.

Two extraction improvements over the path-level audit, both needed to make the
`{serviceType}` ternaries in the frontend services comparable at all:

1. URLs are read from backtick template literals with `[^`]`, so a ternary
   inside `${...}` (e.g. `${this.serviceType === EPICS ? "issues" : "sub-issues"}`)
   is not truncated at its first embedded quote.
2. Each `${cond ? "a" : "b"}` placeholder is expanded into BOTH literal
   branches, so `/issues/.../sub-issues/` and the epics ghost variant are
   audited as the distinct URLs the client can actually build.

Usage: python tests/browser/audit-methods.py
"""

import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
FE = ROOT.parent / "plane-preview" / "apps" / "web"
ROUTER = ROOT / "internal" / "router"

ROUTE_METHODS = ("GET", "POST", "PUT", "PATCH", "DELETE")
FE_VERB = re.compile(r"\.(get|post|put|patch|delete)\s*\(")

TEMPLATE = re.compile(r"`(/api/[^`]*)`")
PLAIN = re.compile(r"['\"](/api/[^'\"\n]*)['\"]")
PLACEHOLDER = re.compile(r"\$\{([^}]*)\}")
GOPARAM = re.compile(r"\{[^}]*\}")
TERNARY = re.compile(r"^[^?]+\?\s*['\"]([^'\"]*)['\"]\s*:\s*['\"]([^'\"]*)['\"]$")

# Frontend variables whose value domain the services fix in code. Expanding them
# keeps their URLs comparable with the router's literal segments; everything
# else collapses to `{}` and is matched against route placeholders.
VAR_DOMAINS = {
    "this.serviceType": ["issues", "epics"],
}


def normalise(path: str) -> str:
    path = path.replace("{$}", "")
    path = PLACEHOLDER.sub("{}", path)
    path = GOPARAM.sub("{}", path)
    path = path.split("?")[0]
    if not path.endswith("/"):
        path += "/"
    return path


def expand_template(body: str) -> list[str]:
    """Expand `${cond ? "a" : "b"}` and known-domain placeholders into their
    branches; every other placeholder collapses to `{}`. Capped so pathological
    templates cannot blow up the cartesian product."""
    parts = re.split(r"(\$\{[^}]*\})", body)
    choices: list[list[str]] = []
    for part in parts:
        if part.startswith("${") and part.endswith("}"):
            inner = part[2:-1].strip()
            if inner in VAR_DOMAINS:
                choices.append(list(VAR_DOMAINS[inner]))
                continue
            m = TERNARY.match(inner)
            choices.append([m.group(1), m.group(2)] if m else ["{}"])
        else:
            choices.append([part])
    out: list[str] = [""]
    for choice in choices:
        if len(out) >= 8:  # cap: keep the first branch of any further placeholder
            out = [o + choice[0] for o in out]
            continue
        out = [o + c for o in out for c in choice]
    return [normalise(u) for u in out if "/api" in u or u.startswith("/api")]


def fe_statements(text: str) -> list[str]:
    """Join physical lines into statements (parens/brackets balanced). Comments
    are dropped but not the `//` in `http://`. Over-joining on braces is
    harmless here: a merged statement still carries its verb before the URL."""
    out: list[str] = []
    buf = ""
    depth = 0
    for raw in text.splitlines():
        line = re.sub(r"(?<!:)//.*$", "", raw).strip()
        if not line:
            continue
        depth += line.count("(") + line.count("[") - line.count(")") - line.count("]")
        buf = f"{buf} {line}".strip() if buf else line
        if depth > 0:
            continue
        out.append(buf)
        buf = ""
        depth = 0
    if buf:
        out.append(buf)
    return out


def frontend_calls() -> dict[tuple[str, str], set[str]]:
    """Collect (METHOD, path-shape) pairs the web app asks for.

    A statement that carries URL literals but no verb (the `const path = …`
    idiom) parks them; the next verb-bearing statement without literals of its
    own claims them."""
    calls: dict[tuple[str, str], set[str]] = {}
    for path in FE.rglob("*.ts*"):
        if "node_modules" in path.parts:
            continue
        try:
            src = path.read_text(encoding="utf-8", errors="ignore")
        except OSError:
            continue
        rel = str(path.relative_to(FE))
        pending: list[str] = []
        for stmt in fe_statements(src):
            shapes: list[str] = []
            for m in TEMPLATE.finditer(stmt):
                shapes.extend(expand_template(m.group(1)))
            for m in PLAIN.finditer(stmt):
                shapes.append(normalise(m.group(1)))
            verbs = [(m.start(), m.group(1).upper()) for m in FE_VERB.finditer(stmt)]
            if shapes and verbs:
                for shape in shapes:
                    # last verb before the first literal wins (chained verbs are rare)
                    calls.setdefault((verbs[-1][1], shape), set()).add(rel)
                pending = []
            elif shapes:
                pending.extend(shapes)
            elif verbs and pending:
                for shape in pending:
                    calls.setdefault((verbs[-1][1], shape), set()).add(rel)
                pending = []
    return calls


def logical_lines(text: str) -> list[str]:
    """Same statement joiner as audit-endpoints.py: wrap trailing-`.` and
    unbalanced-paren declarations back into one logical line."""
    out: list[str] = []
    buf = ""
    for raw in text.splitlines():
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


def registered_routes() -> set[tuple[str, str]]:
    """(METHOD, normalised pattern) pairs, resolving group prefixes."""
    prefixes: dict[str, str] = {}
    routes: set[tuple[str, str]] = set()

    group_decl = re.compile(r"(\w+)\s*:=\s*(.+?)\.\s*Group\(\s*\"([^\"]*)\"\s*\)")
    plain_decl = re.compile(r"(\w+)\s*:=\s*(e\.Router)\b")
    ws_decl = re.compile(r"(\w+)\s*:=\s*workspaceGroup\(")
    ws_group_call = re.compile(r"workspaceGroup\([^)]*\)\.(" + "|".join(ROUTE_METHODS) + r")\(")
    call_start = re.compile(r"(\w+|e\.Router)\.(" + "|".join(ROUTE_METHODS) + r")\(")
    string_lit = re.compile(r'"((?:[^"\\]|\\.)*)"')

    for path in sorted(ROUTER.glob("*.go")):
        if path.name.endswith("_test.go"):
            continue
        for line in logical_lines(path.read_text(encoding="utf-8")):
            if m := group_decl.search(line):
                var, parent, suffix = m.group(1), m.group(2).strip(), m.group(3)
                base = "/api/workspaces/{slug}" if parent.startswith("workspaceGroup(") else prefixes.get(parent, "")
                prefixes[var] = base + suffix
                continue
            if m := plain_decl.search(line):
                prefixes[m.group(1)] = ""
                continue
            if m := ws_decl.search(line):
                prefixes[m.group(1)] = "/api/workspaces/{slug}"
                continue

            if m := ws_group_call.search(line):
                first_arg = line[m.end():].split(",", 1)[0]
                route_path = "".join(string_lit.findall(first_arg))
                if route_path:
                    routes.add((m.group(1), normalise("/api/workspaces/{slug}" + route_path)))
                continue

            if m := call_start.search(line):
                recv = m.group(1)
                first_arg = line[m.end():].split(",", 1)[0]
                route_path = "".join(string_lit.findall(first_arg))
                if not route_path:
                    continue
                prefix = "" if recv == "e.Router" else prefixes.get(recv, "")
                routes.add((m.group(2), normalise(prefix + route_path)))

    return routes


def matches(call: str, route: str) -> bool:
    call_segs = [s for s in call.split("/") if s]
    route_segs = [s for s in route.split("/") if s]
    if len(route_segs) != len(call_segs):
        return False
    return all(r == "{}" or r == c for r, c in zip(route_segs, call_segs))


def main() -> int:
    calls = frontend_calls()
    routes = registered_routes()

    by_shape: dict[str, dict[str, set[str]]] = {}
    for (method, shape), sources in calls.items():
        by_shape.setdefault(shape, {}).setdefault(method, set()).update(sources)

    unserved: list[str] = []
    method_missing: list[tuple[str, str, set[str]]] = []
    served = 0
    for shape, wants in sorted(by_shape.items()):
        served_methods = {m for (m, route) in routes if matches(shape, route)}
        if not served_methods:
            unserved.append(shape)
            continue
        for method, sources in sorted(wants.items()):
            if method in served_methods:
                served += 1
            else:
                method_missing.append((shape, method, served_methods))

    print(f"registered (method, pattern) pairs : {len(routes)}")
    print(f"frontend (method, shape) calls     : {len(calls)}")
    print(f"served                             : {served}")
    print(f"UNSERVED paths                     : {len(unserved)}")
    print(f"METHOD-MISSING                     : {len(method_missing)}\n")

    print("-- UNSERVED (no method answers this path) --")
    for shape in unserved:
        print(f"  {shape}")
        for method, sources in sorted(by_shape[shape].items()):
            for s in sorted(sources)[:2]:
                print(f"      <- {method} {s}")

    print("\n-- METHOD-MISSING (path served, method not) --")
    for shape, method, served_methods in method_missing:
        print(f"  {method} {shape}   (served: {', '.join(sorted(served_methods))})")
        for s in sorted(by_shape[shape][method])[:2]:
            print(f"      <- {s}")

    return 0


if __name__ == "__main__":
    sys.exit(main())
