package router

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// Every route path in this package must be anchored with ServeMux's `{$}`.
//
// Why this is a test and not a comment: PocketBase's router concatenates group
// prefixes and route paths verbatim into `http.ServeMux` patterns, and in Go
// 1.22+ a pattern ending in "/" is a *subtree* pattern. A `GET` registered with
// the path "/" on `Group("/api/workspaces/{slug}")` therefore produces
// `/api/workspaces/{slug}/`, which matches **every** path beneath it — so any
// unregistered sub-path is answered by whichever handler owns the prefix
// instead of 404ing.
//
// This has already cost the project three separate debugging sessions:
//
//   - `/api/users/me/workspaces/{slug}/project-roles/` returned the workspace
//     list, because `/api/users/me/workspaces/` was a subtree.
//   - `/api/workspaces/{slug}/user-profile/{id}/` returned the *workspace*
//     object with HTTP 200, which crashed the web app's profile page on
//     `userProfile.state_distribution.find(...)` — a 404 would have been a
//     clean, self-explanatory failure.
//   - `/api/workspaces/{slug}/projects/{id}/…` returned the project list.
//
// The rule below is deliberately static and syntactic: it needs no server, runs
// in milliseconds, and fails at the exact line that introduces the problem.
func TestRoutePatternsAreAnchored(t *testing.T) {
	routes := map[string]bool{"GET": true, "POST": true, "PUT": true, "PATCH": true, "DELETE": true, "OPTIONS": true, "HEAD": true}

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}

	fset := token.NewFileSet()
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}

		file, err := parser.ParseFile(fset, filepath.Join(".", name), nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}

		ast.Inspect(file, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok || len(call.Args) == 0 {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}

			pos := fset.Position(call.Pos())

			switch {
			case routes[sel.Sel.Name]:
				path, ok := trailingLiteral(call.Args[0])
				if !ok {
					t.Errorf("%s: %s route path is not a static literal; anchor it with `{$}` manually and extend this test",
						pos, sel.Sel.Name)
					return true
				}
				// Only a trailing "/" makes a pattern a subtree. A path like
				// "/stickies/{pk}" (no trailing slash) is already an exact
				// match and needs no anchor.
				if strings.HasSuffix(path, "/") {
					t.Errorf("%s: %s(%q) is not anchored.\n"+
						"    A path ending in \"/\" is a ServeMux SUBTREE pattern: it matches every deeper path,\n"+
						"    so unregistered sub-paths get this handler's response instead of a 404.\n"+
						"    Append `{$}` (e.g. \"/members/{$}\").",
						pos, sel.Sel.Name, path)
				}

			case sel.Sel.Name == "Group":
				prefix, ok := trailingLiteral(call.Args[0])
				if !ok {
					return true
				}
				if strings.HasSuffix(prefix, "/") {
					t.Errorf("%s: Group(%q) prefix ends with \"/\"; every route under it becomes a subtree pattern",
						pos, prefix)
				}
			}
			return true
		})
	}
}

// trailingLiteral returns the last string literal of a path expression.
//
// Paths are sometimes built by concatenation (`"/" + base + "/{pk}/{$}"`), where
// only the trailing segment carries the anchor, so the trailing literal is the
// right thing to inspect.
func trailingLiteral(expr ast.Expr) (string, bool) {
	switch v := expr.(type) {
	case *ast.BasicLit:
		if v.Kind != token.STRING {
			return "", false
		}
		s, err := strconv.Unquote(v.Value)
		if err != nil {
			return "", false
		}
		return s, true
	case *ast.BinaryExpr:
		if v.Op != token.ADD {
			return "", false
		}
		return trailingLiteral(v.Y)
	case *ast.ParenExpr:
		return trailingLiteral(v.X)
	}
	return "", false
}
