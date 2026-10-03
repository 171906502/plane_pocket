"""Anchor every Plane route pattern with ServeMux's `{$}`.

Why: in Go 1.22+ `http.ServeMux`, a pattern ending in "/" is a *subtree*
pattern. PocketBase's router concatenates group prefixes + route paths verbatim
and hands them to ServeMux, so `group.GET("/", h.Detail)` on
`Group("/api/workspaces/{slug}")` produces `/api/workspaces/{slug}/`, which
matches **every** path under it. An unregistered sub-path is then answered by
whatever handler owns the prefix instead of 404ing.

That is not hypothetical: it is how `/api/workspaces/{slug}/user-profile/{id}/`
returned a workspace object with HTTP 200, and how
`/api/workspaces/{slug}/projects/{id}/anything/` returns the project list.

`{$}` anchors a pattern to the exact path. Adding it can only ever *remove*
ServeMux conflicts (an anchored pattern is always a strict subset of the
subtree it replaces), never create new ones.
"""

import re
import sys
from pathlib import Path

ROUTER_DIR = Path(__file__).resolve().parents[2] / "internal" / "router"

ROUTE_RE = re.compile(r"\.(?:GET|POST|PUT|PATCH|DELETE|OPTIONS|HEAD)\(")
LIT_RE = re.compile(r'"(?:[^"\\]|\\.)*"')


def first_argument(args: str) -> tuple[str, str]:
    """Split `args` into (first top-level argument, the rest)."""
    depth = 0
    in_str = False
    esc = False
    for i, c in enumerate(args):
        if in_str:
            if esc:
                esc = False
            elif c == "\\":
                esc = True
            elif c == '"':
                in_str = False
            continue
        if c == '"':
            in_str = True
        elif c in "([{":
            depth += 1
        elif c in ")]}":
            depth -= 1
        elif c == "," and depth == 0:
            return args[:i], args[i:]
    return args, ""


def anchor(first_arg: str) -> tuple[str, bool]:
    """Append `{$}` to the last string literal of a path expression."""
    lits = list(LIT_RE.finditer(first_arg))
    if not lits:
        return first_arg, False
    last = lits[-1]
    body = last.group(0)[1:-1]
    if not body.endswith("/") or body.endswith("{$}"):
        return first_arg, False
    replacement = '"' + body + '{$}"'
    return first_arg[: last.start()] + replacement + first_arg[last.end() :], True


def transform(path: Path) -> int:
    # newline="" is mandatory: the default text-mode writer translates "\n" to
    # os.linesep, which on Windows rewrites every LF in the file to CRLF and
    # turns a 4-line change into a whole-file diff.
    with path.open("r", encoding="utf-8", newline="") as fh:
        src = fh.read()

    # Scan the whole file, not line by line: a route call whose handler is an
    # inline `func(...) error { ... }` literal spans many lines, and a per-line
    # paren scan runs off the end of the first line and silently skips it. That
    # is how `e.Router.GET("/api/plane-meta/health/", func(re *core.RequestEvent) error {`
    # survived the first sweep.
    edits = []
    for m in ROUTE_RE.finditer(src):
        i = m.end()
        depth = 1
        in_str = False
        esc = False
        while i < len(src) and depth > 0:
            c = src[i]
            if in_str:
                if esc:
                    esc = False
                elif c == "\\":
                    esc = True
                elif c == '"':
                    in_str = False
            elif c == '"':
                in_str = True
            elif c == "(":
                depth += 1
            elif c == ")":
                depth -= 1
            i += 1
        if depth != 0:
            continue

        first, _ = first_argument(src[m.end() : i - 1])
        new_first, did = anchor(first)
        if did:
            edits.append((m.end(), m.end() + len(first), new_first))

    if not edits:
        return 0

    out = []
    cursor = 0
    for start, end, replacement in edits:
        if start < cursor:
            # Nested/overlapping match; the outer edit already covers it.
            continue
        out.append(src[cursor:start])
        out.append(replacement)
        cursor = end
    out.append(src[cursor:])

    with path.open("w", encoding="utf-8", newline="") as fh:
        fh.write("".join(out))
    return len(edits)


def main() -> int:
    total = 0
    for path in sorted(ROUTER_DIR.glob("*.go")):
        n = transform(path)
        if n:
            print(f"  {path.name}: {n} pattern(s) anchored")
            total += n
    print(f"total: {total}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
