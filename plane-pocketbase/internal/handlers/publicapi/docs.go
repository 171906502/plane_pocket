package publicapi

import (
	"fmt"
	"html"
	"net/http"
	"strings"

	"github.com/pocketbase/pocketbase/core"
)

// L-8 · 公共 API 文档页.
//
// A zero-dependency docs page for the open API. Deployment is offline-first,
// so no CDN assets, web fonts or scripts are allowed: the whole page is a
// static grouped HTML table rendered from the endpoint table below, with all
// styling inlined in a single <style> tag. There is no global CSP middleware
// on /api/v1 routes (see internal/router/static.go — security headers are only
// set for the /spaces static mount), so the inline style needs no header
// adjustments.
//
// The endpoint table mirrors internal/router/publicapi.go one-to-one. That
// sync is enforced by TestDocsTableMatchesRouterRegistry in docs_test.go,
// which parses the router source (including the `work-items`/`issues` loop)
// and fails if a registered public route is missing from (or stale in) this
// table — update both together.

// Path prefixes used by the router when registering the open API.
const (
	apiPrefix = "/api/v1"
	wsPrefix  = "/api/v1/workspaces/{slug}"
	pjPrefix  = "/api/v1/workspaces/{slug}/projects/{project_id}"
)

// Auth requirements shown in the docs. The group middleware (middleware.APIKey)
// passes requests through when no key is sent; the handlers under "public"
// never call requireUser, everything else rejects without a valid api_tokens
// row.
const (
	authPublic = "public"
	authKey    = "X-Api-Key"
)

// DocEndpoint describes one registered /api/v1 route in the docs table.
type DocEndpoint struct {
	Method      string // GET, POST, PUT, PATCH, DELETE
	Path        string // full registered path, including the `{$}` anchor
	Group       string // section heading on the docs page
	Auth        string // authPublic or authKey
	Description string
}

// Docs table groups, in render order.
const (
	gMeta     = "Meta"
	gUsers    = "Users"
	gAssets   = "Assets"
	gWorksp   = "Workspace"
	gProjects = "Projects"
	gStates   = "States"
	gLabels   = "Labels"
	gCycles   = "Cycles"
	gModules  = "Modules"
	gMembers  = "Project members"
	gEstims   = "Estimates"
	gIntake   = "Intake"
	gItems    = "Work items"
	gComments = "Comments"
	gLinks    = "Links"
	gActs     = "Activity"
	gRels     = "Relations"
	gAttach   = "Attachments"
)

// docEndpoints is the route table rendered on the docs page. Keep in sync with
// internal/router/publicapi.go (enforced by test).
var docEndpoints = func() []DocEndpoint {
	base := []DocEndpoint{
		// --- Meta ---------------------------------------------------------------
		{http.MethodGet, apiPrefix + "/{$}", gMeta, authPublic, "This documentation page."},
		{http.MethodGet, apiPrefix + "/schema/{$}", gMeta, authPublic, "Minimal OpenAPI 3.0 document."},
		{http.MethodGet, apiPrefix + "/schema/swagger-ui/{$}", gMeta, authPublic, "Docs page (swagger-ui alias)."},
		{http.MethodGet, apiPrefix + "/schema/redoc/{$}", gMeta, authPublic, "Docs page (redoc alias)."},

		// --- Users --------------------------------------------------------------
		{http.MethodGet, apiPrefix + "/users/me/{$}", gUsers, authKey, "Profile of the API-key user."},

		// --- Assets -------------------------------------------------------------
		{http.MethodPost, apiPrefix + "/assets/user-assets/{$}", gAssets, authKey, "Upload a user asset."},
		{http.MethodPatch, apiPrefix + "/assets/user-assets/{asset_id}/{$}", gAssets, authKey, "Update user asset attributes."},
		{http.MethodDelete, apiPrefix + "/assets/user-assets/{asset_id}/{$}", gAssets, authKey, "Delete a user asset."},
		{http.MethodPost, apiPrefix + "/assets/user-assets/server/{$}", gAssets, authKey, "Upload a user asset (server variant)."},
		{http.MethodGet, wsPrefix + "/assets/{$}", gAssets, authKey, "List workspace assets (legacy v1 paths)."},
		{http.MethodPost, wsPrefix + "/assets/{$}", gAssets, authKey, "Create a workspace asset (legacy v1 paths)."},
		{http.MethodGet, wsPrefix + "/assets/{asset_id}/{$}", gAssets, authKey, "Workspace asset detail."},
		{http.MethodPatch, wsPrefix + "/assets/{asset_id}/{$}", gAssets, authKey, "Update workspace asset attributes."},
		{http.MethodDelete, wsPrefix + "/assets/{asset_id}/{$}", gAssets, authKey, "Delete a workspace asset."},

		// --- Workspace ----------------------------------------------------------
		{http.MethodGet, wsPrefix + "/members/{$}", gWorksp, authKey, "List workspace members."},
		{http.MethodGet, wsPrefix + "/members-lite/{$}", gWorksp, authKey, "Lightweight member list (id, role, display name)."},
		{http.MethodGet, wsPrefix + "/projects-lite/{$}", gWorksp, authKey, "Lightweight project list."},
		{http.MethodGet, wsPrefix + "/projects/{$}", gWorksp, authKey, "List projects."},
		{http.MethodPost, wsPrefix + "/projects/{$}", gWorksp, authKey, "Create a project."},
		{http.MethodGet, wsPrefix + "/issues/search/{$}", gWorksp, authKey, "Search issues by name."},
		{http.MethodGet, wsPrefix + "/work-items/search/{$}", gWorksp, authKey, "Search work items by name (alias of issues/search)."},
		{http.MethodGet, wsPrefix + "/work-items/{identifier}/{$}", gWorksp, authKey, "Work item by PROJ-12 locator."},
		{http.MethodGet, wsPrefix + "/issues/{identifier}/{$}", gWorksp, authKey, "Issue by PROJ-12 locator (alias)."},
		{http.MethodGet, wsPrefix + "/stickies/{$}", gWorksp, authKey, "List the current user's stickies."},
		{http.MethodPost, wsPrefix + "/stickies/{$}", gWorksp, authKey, "Create a sticky."},
		{http.MethodGet, wsPrefix + "/stickies/{pk}/{$}", gWorksp, authKey, "Sticky detail."},
		{http.MethodPut, wsPrefix + "/stickies/{pk}/{$}", gWorksp, authKey, "Update a sticky (full)."},
		{http.MethodPatch, wsPrefix + "/stickies/{pk}/{$}", gWorksp, authKey, "Update a sticky (partial)."},
		{http.MethodDelete, wsPrefix + "/stickies/{pk}/{$}", gWorksp, authKey, "Delete a sticky."},
		{http.MethodGet, wsPrefix + "/invitations/{$}", gWorksp, authKey, "List workspace invitations."},
		{http.MethodPost, wsPrefix + "/invitations/{$}", gWorksp, authKey, "Create workspace invitations."},
		{http.MethodGet, wsPrefix + "/invitations/{pk}/{$}", gWorksp, authKey, "Invitation detail."},
		{http.MethodPut, wsPrefix + "/invitations/{pk}/{$}", gWorksp, authKey, "Update an invitation (full)."},
		{http.MethodPatch, wsPrefix + "/invitations/{pk}/{$}", gWorksp, authKey, "Update an invitation (partial)."},
		{http.MethodDelete, wsPrefix + "/invitations/{pk}/{$}", gWorksp, authKey, "Delete an invitation."},

		// --- Projects -----------------------------------------------------------
		{http.MethodGet, pjPrefix + "/{$}", gProjects, authKey, "Project detail."},
		{http.MethodPatch, pjPrefix + "/{$}", gProjects, authKey, "Update a project (partial)."},
		{http.MethodDelete, pjPrefix + "/{$}", gProjects, authKey, "Delete (soft) a project."},
		{http.MethodGet, pjPrefix + "/projects/{pk}/{$}", gProjects, authKey, "Project detail (nested alias)."},
		{http.MethodPatch, pjPrefix + "/projects/{pk}/{$}", gProjects, authKey, "Update a project (nested alias)."},
		{http.MethodDelete, pjPrefix + "/projects/{pk}/{$}", gProjects, authKey, "Delete (soft) a project (nested alias)."},
		{http.MethodGet, pjPrefix + "/summary/{$}", gProjects, authKey, "Project summary counts."},

		// --- States -------------------------------------------------------------
		{http.MethodGet, pjPrefix + "/states/{$}", gStates, authKey, "List project states."},
		{http.MethodPost, pjPrefix + "/states/{$}", gStates, authKey, "Create a state."},
		{http.MethodGet, pjPrefix + "/states/{state_id}/{$}", gStates, authKey, "State detail."},
		{http.MethodPatch, pjPrefix + "/states/{state_id}/{$}", gStates, authKey, "Update a state."},
		{http.MethodDelete, pjPrefix + "/states/{state_id}/{$}", gStates, authKey, "Delete (soft) a state."},

		// --- Labels -------------------------------------------------------------
		{http.MethodGet, pjPrefix + "/labels/{$}", gLabels, authKey, "List project labels."},
		{http.MethodPost, pjPrefix + "/labels/{$}", gLabels, authKey, "Create a label."},
		{http.MethodGet, pjPrefix + "/labels/{pk}/{$}", gLabels, authKey, "Label detail."},
		{http.MethodPatch, pjPrefix + "/labels/{pk}/{$}", gLabels, authKey, "Update a label."},
		{http.MethodDelete, pjPrefix + "/labels/{pk}/{$}", gLabels, authKey, "Delete (soft) a label."},

		// --- Cycles -------------------------------------------------------------
		{http.MethodGet, pjPrefix + "/cycles/{$}", gCycles, authKey, "List project cycles."},
		{http.MethodPost, pjPrefix + "/cycles/{$}", gCycles, authKey, "Create a cycle."},
		{http.MethodGet, pjPrefix + "/cycles/{pk}/{$}", gCycles, authKey, "Cycle detail."},
		{http.MethodPatch, pjPrefix + "/cycles/{pk}/{$}", gCycles, authKey, "Update a cycle."},
		{http.MethodDelete, pjPrefix + "/cycles/{pk}/{$}", gCycles, authKey, "Delete (soft) a cycle."},
		{http.MethodGet, pjPrefix + "/cycles/{cycle_id}/cycle-issues/{$}", gCycles, authKey, "List issues bridged to a cycle."},
		{http.MethodPost, pjPrefix + "/cycles/{cycle_id}/cycle-issues/{$}", gCycles, authKey, "Bridge an issue into a cycle."},
		{http.MethodGet, pjPrefix + "/cycles/{cycle_id}/cycle-issues/{issue_id}/{$}", gCycles, authKey, "Cycle-issue bridge detail."},
		{http.MethodDelete, pjPrefix + "/cycles/{cycle_id}/cycle-issues/{issue_id}/{$}", gCycles, authKey, "Remove an issue from a cycle."},
		{http.MethodPost, pjPrefix + "/cycles/{cycle_id}/transfer-issues/{$}", gCycles, authKey, "Move issues between cycles."},
		{http.MethodPost, pjPrefix + "/cycles/{cycle_id}/archive/{$}", gCycles, authKey, "Archive a cycle."},
		{http.MethodDelete, pjPrefix + "/cycles/{cycle_id}/archive/{$}", gCycles, authKey, "Unarchive a cycle."},
		{http.MethodGet, pjPrefix + "/archived-cycles/{$}", gCycles, authKey, "List archived cycles."},
		{http.MethodPost, pjPrefix + "/archived-cycles/{cycle_id}/unarchive/{$}", gCycles, authKey, "Unarchive a cycle (alias)."},
		{http.MethodGet, pjPrefix + "/cycles-lite/{$}", gCycles, authKey, "Lightweight cycle list."},

		// --- Modules ------------------------------------------------------------
		{http.MethodGet, pjPrefix + "/modules/{$}", gModules, authKey, "List project modules."},
		{http.MethodPost, pjPrefix + "/modules/{$}", gModules, authKey, "Create a module."},
		{http.MethodGet, pjPrefix + "/modules/{pk}/{$}", gModules, authKey, "Module detail."},
		{http.MethodPatch, pjPrefix + "/modules/{pk}/{$}", gModules, authKey, "Update a module."},
		{http.MethodDelete, pjPrefix + "/modules/{pk}/{$}", gModules, authKey, "Delete (soft) a module."},
		{http.MethodGet, pjPrefix + "/modules/{module_id}/module-issues/{$}", gModules, authKey, "List issues bridged to a module."},
		{http.MethodPost, pjPrefix + "/modules/{module_id}/module-issues/{$}", gModules, authKey, "Bridge an issue into a module."},
		{http.MethodGet, pjPrefix + "/modules/{module_id}/module-issues/{issue_id}/{$}", gModules, authKey, "Module-issue bridge detail."},
		{http.MethodDelete, pjPrefix + "/modules/{module_id}/module-issues/{issue_id}/{$}", gModules, authKey, "Remove an issue from a module."},
		{http.MethodGet, pjPrefix + "/modules-lite/{$}", gModules, authKey, "Lightweight module list."},

		// --- Project members ------------------------------------------------------
		{http.MethodGet, pjPrefix + "/project-members/{$}", gMembers, authKey, "List project members."},
		{http.MethodPost, pjPrefix + "/project-members/{$}", gMembers, authKey, "Add a project member."},
		{http.MethodGet, pjPrefix + "/project-members/{pk}/{$}", gMembers, authKey, "Project member detail."},
		{http.MethodPatch, pjPrefix + "/project-members/{pk}/{$}", gMembers, authKey, "Update a project member."},
		{http.MethodDelete, pjPrefix + "/project-members/{pk}/{$}", gMembers, authKey, "Remove a project member."},
		{http.MethodGet, pjPrefix + "/members/{$}", gMembers, authKey, "List project members (alias)."},
		{http.MethodPost, pjPrefix + "/members/{$}", gMembers, authKey, "Add a project member (alias)."},
		{http.MethodGet, pjPrefix + "/members/{pk}/{$}", gMembers, authKey, "Project member detail (alias)."},
		{http.MethodPatch, pjPrefix + "/members/{pk}/{$}", gMembers, authKey, "Update a project member (alias)."},
		{http.MethodDelete, pjPrefix + "/members/{pk}/{$}", gMembers, authKey, "Remove a project member (alias)."},
		{http.MethodGet, pjPrefix + "/project-members-lite/{$}", gMembers, authKey, "Lightweight project member list."},

		// --- Estimates ------------------------------------------------------------
		{http.MethodGet, pjPrefix + "/estimates/{$}", gEstims, authKey, "List estimate systems."},
		{http.MethodPost, pjPrefix + "/estimates/{$}", gEstims, authKey, "Create an estimate system."},
		{http.MethodPatch, pjPrefix + "/estimates/{$}", gEstims, authKey, "Update an estimate system."},
		{http.MethodDelete, pjPrefix + "/estimates/{$}", gEstims, authKey, "Delete an estimate system."},
		{http.MethodGet, pjPrefix + "/estimates/{estimate_id}/estimate-points/{$}", gEstims, authKey, "List estimate points."},
		{http.MethodPost, pjPrefix + "/estimates/{estimate_id}/estimate-points/{$}", gEstims, authKey, "Create an estimate point."},
		{http.MethodPatch, pjPrefix + "/estimates/{estimate_id}/estimate-points/{estimate_point_id}/{$}", gEstims, authKey, "Update an estimate point."},
		{http.MethodDelete, pjPrefix + "/estimates/{estimate_id}/estimate-points/{estimate_point_id}/{$}", gEstims, authKey, "Delete an estimate point."},

		// --- Intake ---------------------------------------------------------------
		{http.MethodGet, pjPrefix + "/intake-issues/{$}", gIntake, authKey, "List intake issues."},
		{http.MethodPost, pjPrefix + "/intake-issues/{$}", gIntake, authKey, "Create an intake issue."},
		{http.MethodGet, pjPrefix + "/intake-issues/{issue_id}/{$}", gIntake, authKey, "Intake issue detail."},
		{http.MethodPatch, pjPrefix + "/intake-issues/{issue_id}/{$}", gIntake, authKey, "Update an intake issue."},
		{http.MethodDelete, pjPrefix + "/intake-issues/{issue_id}/{$}", gIntake, authKey, "Delete an intake issue."},

		// --- Work items (plus the /issues/ alias base) -----------------------------
		{http.MethodGet, pjPrefix + "/work-items/{$}", gItems, authKey, "List work items."},
		{http.MethodPost, pjPrefix + "/work-items/{$}", gItems, authKey, "Create a work item."},
		{http.MethodGet, pjPrefix + "/issues/{$}", gItems, authKey, "List work items (alias)."},
		{http.MethodPost, pjPrefix + "/issues/{$}", gItems, authKey, "Create a work item (alias)."},
	}
	// The router registers the per-issue sub-resources once per base
	// ("work-items" and the "issues" alias); mirror that here.
	base = append(base, workItemSubroutes("work-items")...)
	return append(base, workItemSubroutes("issues")...)
}()

// workItemSubroutes enumerates the per-issue sub-resources the router
// registers once per base ("work-items" and the "issues" alias).
func workItemSubroutes(base string) []DocEndpoint {
	p := pjPrefix + "/" + base
	return []DocEndpoint{
		{http.MethodGet, p + "/{pk}/{$}", gItems, authKey, "Work item detail."},
		{http.MethodPut, p + "/{pk}/{$}", gItems, authKey, "Update a work item (full)."},
		{http.MethodPatch, p + "/{pk}/{$}", gItems, authKey, "Update a work item (partial)."},
		{http.MethodDelete, p + "/{pk}/{$}", gItems, authKey, "Delete (soft) a work item."},
		{http.MethodGet, p + "/{issue_id}/comments/{$}", gComments, authKey, "List comments."},
		{http.MethodPost, p + "/{issue_id}/comments/{$}", gComments, authKey, "Add a comment."},
		{http.MethodGet, p + "/{issue_id}/comments/{pk}/{$}", gComments, authKey, "Comment detail."},
		{http.MethodPatch, p + "/{issue_id}/comments/{pk}/{$}", gComments, authKey, "Update a comment."},
		{http.MethodDelete, p + "/{issue_id}/comments/{pk}/{$}", gComments, authKey, "Delete a comment."},
		{http.MethodGet, p + "/{issue_id}/links/{$}", gLinks, authKey, "List issue links."},
		{http.MethodPost, p + "/{issue_id}/links/{$}", gLinks, authKey, "Create an issue link."},
		{http.MethodGet, p + "/{issue_id}/links/{pk}/{$}", gLinks, authKey, "Issue link detail."},
		{http.MethodPatch, p + "/{issue_id}/links/{pk}/{$}", gLinks, authKey, "Update an issue link."},
		{http.MethodDelete, p + "/{issue_id}/links/{pk}/{$}", gLinks, authKey, "Delete an issue link."},
		{http.MethodGet, p + "/{issue_id}/activities/{$}", gActs, authKey, "List activity log entries."},
		{http.MethodGet, p + "/{issue_id}/activities/{pk}/{$}", gActs, authKey, "Activity entry detail."},
		{http.MethodGet, p + "/{issue_id}/relations/{$}", gRels, authKey, "List issue relations."},
		{http.MethodPost, p + "/{issue_id}/relations/{$}", gRels, authKey, "Create an issue relation."},
		{http.MethodGet, p + "/{issue_id}/attachments/{$}", gAttach, authKey, "List issue attachments."},
		{http.MethodPost, p + "/{issue_id}/attachments/{$}", gAttach, authKey, "Upload an issue attachment."},
		{http.MethodGet, p + "/{issue_id}/attachments/{pk}/{$}", gAttach, authKey, "Issue attachment detail."},
		{http.MethodPatch, p + "/{issue_id}/attachments/{pk}/{$}", gAttach, authKey, "Update an issue attachment."},
		{http.MethodDelete, p + "/{issue_id}/attachments/{pk}/{$}", gAttach, authKey, "Delete an issue attachment."},
	}
}

// docsMarker is a stable string tests (and the lead's DOM assertions) can
// grep for to prove the docs page — not a placeholder — was served.
const docsMarker = "plane-pocketbase public api docs (L-8)"

// displayPath strips the ServeMux `{$}` exact-match anchor for display; the
// registered path always ends in a slash afterwards.
func displayPath(p string) string { return strings.TrimSuffix(p, "{$}") }

// renderDocsHTML builds the self-contained docs page: no scripts, no external
// stylesheets, no fonts, no images — hence no http(s):// substring anywhere.
func renderDocsHTML() string {
	var b strings.Builder
	b.WriteString(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Plane API — public v1</title>
<!-- ` + docsMarker + ` -->
<style>
:root { color-scheme: light dark; }
body { font-family: system-ui, -apple-system, "Segoe UI", Roboto, sans-serif;
       margin: 0 auto; max-width: 60rem; padding: 0 1rem 3rem; line-height: 1.5; }
h1 small { font-weight: normal; color: #6b7280; }
header { border-bottom: 1px solid #d1d5db; padding: 1.5rem 0 1rem; }
nav { margin-top: .75rem; font-size: .9rem; }
nav a { margin-right: .9rem; }
table { border-collapse: collapse; width: 100%; margin: .5rem 0 2rem; }
th, td { border: 1px solid #d1d5db; padding: .35rem .6rem; text-align: left;
         vertical-align: top; }
th { background: #f3f4f6; }
code { font-family: ui-monospace, Consolas, monospace; font-size: .875rem; }
td.m { white-space: nowrap; font-family: ui-monospace, Consolas, monospace;
       font-size: .75rem; font-weight: bold; }
td.m-GET { color: #047857; } td.m-POST { color: #1d4ed8; }
td.m-PUT { color: #b45309; } td.m-PATCH { color: #b45309; }
td.m-DELETE { color: #b91c1c; }
td.auth { white-space: nowrap; font-size: .8rem; color: #6b7280; }
footer { border-top: 1px solid #d1d5db; padding-top: 1rem; font-size: .85rem;
         color: #6b7280; }
@media (prefers-color-scheme: dark) {
  th { background: #1f2937; }
  th, td { border-color: #374151; }
  header, footer { border-color: #374151; }
  h1 small, td.auth, footer { color: #9ca3af; }
}
</style>
</head>
<body>
<header>
<h1>Plane API <small>public v1</small></h1>
<p>Open (third-party) API authenticated with an <code>X-Api-Key: &lt;token&gt;</code>
request header against API tokens. All endpoints are prefixed with
<code>` + apiPrefix + `</code>. Endpoints marked <code>public</code> work without a key.</p>
`)
	b.WriteString("<nav>")
	seen := map[string]bool{}
	for _, ep := range docEndpoints {
		if seen[ep.Group] {
			continue
		}
		seen[ep.Group] = true
		fmt.Fprintf(&b, `<a href="#%s">%s</a>`, anchor(ep.Group), html.EscapeString(ep.Group))
	}
	b.WriteString("</nav>\n</header>\n<main>\n")

	for _, g := range orderedGroups(seen) {
		fmt.Fprintf(&b, "<section id=%q>\n<h2>%s</h2>\n<table>\n<tr><th>Method</th><th>Path</th><th>Auth</th><th>Description</th></tr>\n",
			anchor(g), html.EscapeString(g))
		for _, ep := range docEndpoints {
			if ep.Group != g {
				continue
			}
			fmt.Fprintf(&b, "<tr><td class=%q>%s</td><td><code>%s</code></td><td class=\"auth\">%s</td><td>%s</td></tr>\n",
				"m m-"+ep.Method, html.EscapeString(ep.Method),
				html.EscapeString(displayPath(ep.Path)),
				html.EscapeString(ep.Auth), html.EscapeString(ep.Description))
		}
		b.WriteString("</table>\n</section>\n")
	}
	b.WriteString(`</main>
<footer>Generated from the route registry (L-8). Machine-readable stub:
<code>` + apiPrefix + `/schema/</code>. This page is fully self-contained —
zero external requests.</footer>
</body>
</html>
`)
	return b.String()
}

// anchor maps a group heading to a DOM id.
func anchor(group string) string {
	return strings.ToLower(strings.ReplaceAll(group, " ", "-"))
}

// orderedGroups returns the groups in first-appearance order of the table.
func orderedGroups(seen map[string]bool) []string {
	var out []string
	g := map[string]bool{}
	for _, ep := range docEndpoints {
		if seen[ep.Group] && !g[ep.Group] {
			g[ep.Group] = true
			out = append(out, ep.Group)
		}
	}
	return out
}

// renderedDocsHTML is computed once at process start; the table is static.
var renderedDocsHTML = renderDocsHTML()

// Docs serves GET /api/v1/ — the zero-dependency documentation page.
func (h *Handler) Docs(e *core.RequestEvent) error {
	e.Response.Header().Set("Content-Type", "text/html; charset=utf-8")
	return e.String(http.StatusOK, renderedDocsHTML)
}
