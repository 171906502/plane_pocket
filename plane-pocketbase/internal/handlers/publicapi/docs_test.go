package publicapi

import (
	"go/ast"
	"go/parser"
	"go/token"
	"html"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/pocketbase/pocketbase/core"
)

// --- Evidence 1+2: GET /api/v1/ serves the docs HTML ---------------------------

// TestDocsPageServesEveryRegisteredRoute proves GET /api/v1/ returns the docs
// page: 200, the docs marker, one table row per documented endpoint (method +
// path present), and — the hard offline requirement — zero external resource
// references (no CDN, fonts, scripts, images, or CSS url() loads).
func TestDocsPageServesEveryRegisteredRoute(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/", nil)
	rec := httptest.NewRecorder()
	e := &core.RequestEvent{Request: req, Response: rec}

	if err := (&Handler{}).Docs(e); err != nil {
		t.Fatalf("Docs: %v", err)
	}
	res := rec.Result()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.StatusCode)
	}
	if ct := res.Header.Get("Content-Type"); ct != "text/html; charset=utf-8" {
		t.Errorf("Content-Type = %q, want text/html; charset=utf-8", ct)
	}
	body := rec.Body.String()

	if !strings.Contains(body, docsMarker) {
		t.Errorf("docs marker %q missing from response body", docsMarker)
	}

	// Every documented endpoint must appear as method + path.
	groups := map[string]bool{}
	for _, ep := range docEndpoints {
		groups[ep.Group] = true
	}
	rows := strings.Count(body, "<tr>") - len(groups)
	if rows != len(docEndpoints) {
		t.Errorf("rendered table rows = %d, want %d (one per documented endpoint)", rows, len(docEndpoints))
	}
	for _, ep := range docEndpoints {
		path := html.EscapeString(displayPath(ep.Path))
		if !strings.Contains(body, path) {
			t.Errorf("%s %s missing from docs HTML", ep.Method, ep.Path)
		}
		if !strings.Contains(body, ">"+ep.Method+"</td>") {
			t.Errorf("method badge %s missing from docs HTML", ep.Method)
		}
	}

	// Zero external requests: no absolute URLs, no script/link/style/img
	// loads, no CSS url(). The page must render with no network at all.
	for _, forbidden := range []string{"http://", "https://", "<script", "<link", "src=", "url("} {
		if strings.Contains(body, forbidden) {
			t.Errorf("docs HTML references external resources: found %q", forbidden)
		}
	}
}

// --- Evidence 3: docs stay in sync with the router registry ---------------------

// TestDocsTableMatchesRouterRegistry parses internal/router/publicapi.go and
// reconstructs the exact set of (method, path) pairs registered under the
// public API — including the `work-items`/`issues` alias loop — then checks it
// 1:1 against the docs table. Registering a new public route without a docs
// entry fails this test, as does a stale docs entry.
//
// The approach is the same static/syntactic one as the sibling
// internal/router/patterns_test.go: no server, no DB, fails at the registry.
func TestDocsTableMatchesRouterRegistry(t *testing.T) {
	src := filepath.Join("..", "..", "router", "publicapi.go")
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, src, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", src, err)
	}

	// env maps variable names to their possible string expansions: group
	// prefixes (`api`, `ws`, `proj`) and range variables (`base`).
	env := map[string][]string{}
	registered := map[string]bool{}

	ast.Inspect(file, func(node ast.Node) bool {
		switch n := node.(type) {
		case *ast.RangeStmt:
			// for _, base := range []string{"work-items", "issues"}
			if n.Tok != token.DEFINE || n.Value == nil {
				return true
			}
			ident, ok := n.Value.(*ast.Ident)
			if !ok || ident.Name == "_" {
				return true
			}
			lit, ok := n.X.(*ast.CompositeLit)
			if !ok {
				return true
			}
			var vals []string
			for _, elt := range lit.Elts {
				s, ok := basicString(elt)
				if !ok {
					return true
				}
				vals = append(vals, s)
			}
			env[ident.Name] = vals
		case *ast.AssignStmt:
			if n.Tok != token.DEFINE || len(n.Lhs) != 1 || len(n.Rhs) != 1 {
				return true
			}
			name, ok := n.Lhs[0].(*ast.Ident)
			if !ok {
				return true
			}
			if prefix, ok := groupPrefix(n.Rhs[0], env); ok {
				env[name.Name] = []string{prefix}
			}
		case *ast.CallExpr:
			sel, ok := n.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			method, ok := routeMethod(sel.Sel.Name)
			if !ok {
				return true
			}
			rec, ok := sel.X.(*ast.Ident)
			if !ok {
				return true
			}
			prefixes, ok := env[rec.Name]
			if !ok || len(prefixes) == 0 || len(n.Args) < 1 {
				return true
			}
			for _, p := range evalPath(t, n.Args[0], env) {
				for _, prefix := range prefixes {
					registered[method+" "+prefix+p] = true
				}
			}
		}
		return true
	})

	if len(registered) < 100 {
		t.Fatalf("only %d public routes recovered from the registry — the AST walker broke; fix it before trusting this test", len(registered))
	}

	// documented set
	docs := make(map[string]bool, len(docEndpoints))
	for _, ep := range docEndpoints {
		docs[ep.Method+" "+ep.Path] = true
	}

	for r := range registered {
		if !docs[r] {
			t.Errorf("registered public route %s has no docs entry — add it to docEndpoints (internal/handlers/publicapi/docs.go)", r)
		}
	}
	for d := range docs {
		if !registered[d] {
			t.Errorf("docs entry %s is not registered in internal/router/publicapi.go — remove or fix it", d)
		}
	}
	if len(registered) != len(docs) {
		t.Errorf("route counts diverge: %d registered vs %d documented", len(registered), len(docs))
	}
}

func basicString(expr ast.Expr) (string, bool) {
	lit, ok := expr.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	s, err := strconv.Unquote(lit.Value)
	if err != nil {
		return "", false
	}
	return s, true
}

func routeMethod(name string) (string, bool) {
	switch name {
	case "GET", "POST", "PUT", "PATCH", "DELETE", "SEARCH", "HEAD", "OPTIONS":
		return name, true
	case "Any":
		return "ANY", true
	}
	return "", false
}

// groupPrefix recognizes `X := <expr>.Group("<lit>")…` chains (possibly with
// BindFunc appended) and returns the fully-resolved prefix.
func groupPrefix(expr ast.Expr, env map[string][]string) (string, bool) {
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return "", false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return "", false
	}
	if sel.Sel.Name != "Group" {
		// Chained call (e.g. Group(...).BindFunc(...)): descend into receiver.
		return groupPrefix(sel.X, env)
	}
	if len(call.Args) != 1 {
		return "", false
	}
	suffix, ok := basicString(call.Args[0])
	if !ok {
		return "", false
	}
	switch parent := sel.X.(type) {
	case *ast.SelectorExpr: // e.Router.Group(...)
		return suffix, true
	case *ast.Ident: // ws := api.Group(...)
		prefixes, ok := env[parent.Name]
		if !ok || len(prefixes) != 1 {
			return "", false
		}
		return prefixes[0] + suffix, true
	}
	return "", false
}

// evalPath resolves a route path expression to its literal expansions:
// string literals, identifier lookups (range vars), and + concatenation.
func evalPath(t *testing.T, expr ast.Expr, env map[string][]string) []string {
	t.Helper()
	switch v := expr.(type) {
	case *ast.BasicLit:
		if s, ok := basicString(v); ok {
			return []string{s}
		}
	case *ast.Ident:
		if vals, ok := env[v.Name]; ok {
			return vals
		}
		t.Fatalf("unresolvable identifier %q in route path", v.Name)
	case *ast.BinaryExpr:
		if v.Op == token.ADD {
			var out []string
			for _, l := range evalPath(t, v.X, env) {
				for _, r := range evalPath(t, v.Y, env) {
					out = append(out, l+r)
				}
			}
			return out
		}
	}
	t.Fatalf("unsupported route path expression %T; extend TestDocsTableMatchesRouterRegistry", expr)
	return nil
}
