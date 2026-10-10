// Package dashboard implements Plane's home-dashboard endpoints.
//
//	GET   /api/workspaces/{slug}/dashboard/                          ?dashboard_type=home
//	GET   /api/workspaces/{slug}/dashboard/{dashboard_id}/           ?widget_key=…
//	GET   /api/dashboard/{dashboard_id}/                             ?widget_key=…
//	PATCH /api/dashboard/{dashboard_id}/widgets/{widget_id}/         {is_visible?, filters?}
//
// CONTRACT PROVENANCE (there is no Django model or URL for any of these paths):
// the shapes are taken from the frontend's own types —
// plane-preview/packages/types/src/dashboard.ts — THomeDashboardResponse (:184),
// TDeprecatedDashboard (:170), TWidget (:66) and TWidgetStatsResponse (:159).
// The persistence tables `dashboards` + `dashboard_widgets` are created by
// pb_migrations/1759000013_init_dashboards.go.
//
// AUTHORIZATION (the delicate part): the last two paths are NOT workspace-scoped
// (no ResolveWorkspace), so this handler performs authentication AND an explicit
// ownership check itself — a caller may only read/patch a dashboard whose
// `owned_by` is the caller. A non-owner (or anonymous caller) gets 404/401, so
// the dashboard of another user is never exposed.
package dashboard

import (
	"net/http"
	"strings"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"

	"github.com/plane-pocketbase/plane-pocketbase/internal/middleware"
	"github.com/plane-pocketbase/plane-pocketbase/internal/serializers"
)

// defaultWidgetKeys are the TWidgetKeys seeded onto a freshly created home
// dashboard (packages/types/src/dashboard.ts:17). Order matters: it becomes the
// default `sort_order` (descending, so the first key sorts first).
var defaultWidgetKeys = []string{
	"overview_stats",
	"assigned_issues",
	"created_issues",
	"issues_by_state_groups",
	"issues_by_priority",
	"recent_activity",
	"recent_projects",
	"recent_collaborators",
}

// Handler holds the app dependency for the dashboard routes.
type Handler struct {
	App core.App
}

// New returns a dashboard handler bound to app.
func New(app core.App) *Handler {
	return &Handler{App: app}
}

// ---------------------------------------------------------------------------
// GET /api/workspaces/{slug}/dashboard/
// ---------------------------------------------------------------------------

// HomeDashboard serves the home dashboard get-or-create
// (THomeDashboardResponse = {dashboard, widgets}).
func (h *Handler) HomeDashboard(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Workspace == nil {
		return serializers.NotFound(e, "")
	}
	user := middleware.CurrentUser(e)
	if user == nil {
		return serializers.Unauthorized(e, "Authentication credentials were not provided.")
	}

	dashboardType := e.Request.URL.Query().Get("dashboard_type")
	if dashboardType == "" {
		dashboardType = "home"
	}

	dashboard, err := h.ensureDashboard(scope.Workspace.Id, user.Id, dashboardType)
	if err != nil {
		return err
	}
	if err := h.ensureWidgets(dashboard, user.Id); err != nil {
		return err
	}

	widgets, err := h.widgetsFor(dashboard.Id)
	if err != nil {
		return err
	}
	out := make([]any, 0, len(widgets))
	for _, w := range widgets {
		out = append(out, widgetJSON(h.App, w))
	}

	return e.JSON(http.StatusOK, map[string]any{
		"dashboard": dashboardJSON(h.App, dashboard),
		"widgets":   out,
	})
}

// ---------------------------------------------------------------------------
// GET …/dashboard/{dashboard_id}/  and  GET /api/dashboard/{dashboard_id}/
// ---------------------------------------------------------------------------

// WidgetStats serves GET /api/workspaces/{slug}/dashboard/{dashboard_id}/
// (workspace-scoped). Ownership is verified against the resolved workspace.
func (h *Handler) WidgetStats(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Workspace == nil {
		return serializers.NotFound(e, "")
	}
	user := middleware.CurrentUser(e)
	if user == nil {
		return serializers.Unauthorized(e, "Authentication credentials were not provided.")
	}
	dashboard, err := h.ownedDashboard(e.Request.PathValue("dashboard_id"), user.Id)
	if err != nil {
		return err
	}
	// A dashboard from another workspace must look absent.
	if dashboard == nil || dashboard.GetString("workspace") != scope.Workspace.Id {
		return serializers.NotFound(e, "")
	}
	return e.JSON(http.StatusOK, h.widgetStats(e, user.Id, scope.Workspace.Id))
}

// WidgetStatsByID serves GET /api/dashboard/{dashboard_id}/ — NOT
// workspace-scoped, so it authenticates and ownership-checks on its own.
func (h *Handler) WidgetStatsByID(e *core.RequestEvent) error {
	user := middleware.CurrentUser(e)
	if user == nil {
		return serializers.Unauthorized(e, "Authentication credentials were not provided.")
	}
	dashboard, err := h.ownedDashboard(e.Request.PathValue("dashboard_id"), user.Id)
	if err != nil {
		return err
	}
	if dashboard == nil {
		return serializers.NotFound(e, "")
	}
	return e.JSON(http.StatusOK, h.widgetStats(e, user.Id, dashboard.GetString("workspace")))
}

// ---------------------------------------------------------------------------
// PATCH /api/dashboard/{dashboard_id}/widgets/{widget_id}/
// ---------------------------------------------------------------------------

// UpdateWidget serves the single-widget PATCH. Returns the updated TWidget.
func (h *Handler) UpdateWidget(e *core.RequestEvent) error {
	user := middleware.CurrentUser(e)
	if user == nil {
		return serializers.Unauthorized(e, "Authentication credentials were not provided.")
	}
	dashboard, err := h.ownedDashboard(e.Request.PathValue("dashboard_id"), user.Id)
	if err != nil {
		return err
	}
	if dashboard == nil {
		return serializers.NotFound(e, "")
	}
	widget, _ := serializers.FindByUUID(h.App, "dashboard_widgets", e.Request.PathValue("widget_id"))
	if widget == nil || widget.GetString("dashboard") != dashboard.Id || widget.GetString("deleted_at") != "" {
		return serializers.NotFound(e, "")
	}

	body := map[string]any{}
	if err := e.BindBody(&body); err != nil {
		return serializers.BadRequest(e, "Invalid request body.")
	}
	if v, ok := body["is_visible"]; ok {
		widget.Set("is_visible", v)
	}
	if v, ok := body["filters"]; ok {
		widget.Set("filters", v)
	}
	if v, ok := body["sort_order"]; ok {
		widget.Set("sort_order", v)
	}
	widget.Set("updated_by", user.Id)
	if err := h.App.Save(widget); err != nil {
		return serializers.Validation(e, "detail", err.Error())
	}
	return e.JSON(http.StatusOK, widgetJSON(h.App, widget))
}

// ---------------------------------------------------------------------------
// persistence
// ---------------------------------------------------------------------------

// ensureDashboard returns the caller's dashboard for (workspace, owner, type),
// creating it on first access.
func (h *Handler) ensureDashboard(workspaceID, ownerID, dashboardType string) (*core.Record, error) {
	existing, err := h.App.FindFirstRecordByFilter(
		"dashboards",
		"workspace = {:workspace} && owned_by = {:owner} && type = {:type} && deleted_at = ''",
		dbx.Params{"workspace": workspaceID, "owner": ownerID, "type": dashboardType},
	)
	if err == nil && existing != nil {
		return existing, nil
	}
	collection, err := h.App.FindCollectionByNameOrId("dashboards")
	if err != nil {
		return nil, err
	}
	record := core.NewRecord(collection)
	record.Set("uuid", serializers.NewUUID())
	record.Set("name", dashboardTitle(dashboardType))
	record.Set("identifier", dashboardType)
	record.Set("type", dashboardType)
	record.Set("is_default", true)
	record.Set("owned_by", ownerID)
	record.Set("workspace", workspaceID)
	record.Set("created_by", ownerID)
	if err := h.App.Save(record); err != nil {
		return nil, err
	}
	return record, nil
}

// ensureWidgets seeds any missing default widget onto the dashboard, so the
// home widget area is never empty.
func (h *Handler) ensureWidgets(dashboard *core.Record, userID string) error {
	existing, err := h.widgetsFor(dashboard.Id)
	if err != nil {
		return err
	}
	seen := make(map[string]bool, len(existing))
	for _, w := range existing {
		seen[w.GetString("key")] = true
	}
	collection, err := h.App.FindCollectionByNameOrId("dashboard_widgets")
	if err != nil {
		return err
	}
	// Descending sort_order so the first default key sorts first.
	top := len(defaultWidgetKeys) * 10000
	for i, key := range defaultWidgetKeys {
		if seen[key] {
			continue
		}
		record := core.NewRecord(collection)
		record.Set("uuid", serializers.NewUUID())
		record.Set("dashboard", dashboard.Id)
		record.Set("workspace", dashboard.GetString("workspace"))
		record.Set("key", key)
		record.Set("is_visible", true)
		record.Set("sort_order", top-i*10000)
		record.Set("filters", map[string]any{})
		record.Set("created_by", userID)
		if err := h.App.Save(record); err != nil {
			return err
		}
	}
	return nil
}

// widgetsFor returns the dashboard's widgets ordered by `-sort_order`.
func (h *Handler) widgetsFor(dashboardID string) ([]*core.Record, error) {
	return h.App.FindRecordsByFilter(
		"dashboard_widgets",
		"dashboard = {:dashboard} && deleted_at = ''",
		"-sort_order", 0, 0,
		dbx.Params{"dashboard": dashboardID},
	)
}

// ownedDashboard resolves a dashboard UUID and enforces that the caller owns it.
// Returns (nil, nil) when the dashboard is absent or owned by someone else — the
// caller then answers 404, so ownership is never leaked.
func (h *Handler) ownedDashboard(uuid, userID string) (*core.Record, error) {
	record, err := serializers.FindByUUID(h.App, "dashboards", uuid)
	if err != nil || record == nil {
		return nil, nil
	}
	if record.GetString("owned_by") != userID || record.GetString("deleted_at") != "" {
		return nil, nil
	}
	return record, nil
}

// ---------------------------------------------------------------------------
// serialization
// ---------------------------------------------------------------------------

// dashboardJSON mirrors TDeprecatedDashboard (dashboard.ts:170).
func dashboardJSON(app core.App, r *core.Record) map[string]any {
	if r == nil {
		return nil
	}
	return map[string]any{
		"id":               serializers.UUID(r),
		"name":             serializers.Text(r, "name"),
		"description_html": serializers.Text(r, "description_html"),
		"identifier":       serializers.NullableText(r, "identifier"),
		"is_default":       r.GetBool("is_default"),
		"owned_by":         serializers.RelationUUID(app, r, "owned_by"),
		"type":             serializers.Text(r, "type"),
		"created_at":       serializers.DateTime(r, "created_at"),
		"updated_at":       serializers.DateTime(r, "updated_at"),
		"created_by":       serializers.RelationUUID(app, r, "created_by"),
		"updated_by":       serializers.RelationUUID(app, r, "updated_by"),
	}
}

// widgetJSON mirrors TWidget (dashboard.ts:66). `filters` is the stored value;
// it is echoed under both the read key (`widget_filters`) and the write key
// (`filters`) so either consumer shape is satisfied.
func widgetJSON(app core.App, r *core.Record) map[string]any {
	if r == nil {
		return nil
	}
	filters := serializers.JSONOrDefault(r, "filters", map[string]any{})
	return map[string]any{
		"id":             serializers.UUID(r),
		"is_visible":     r.GetBool("is_visible"),
		"key":            serializers.Text(r, "key"),
		"sort_order":     r.GetInt("sort_order"),
		"widget_filters": filters,
		"filters":        filters,
	}
}

// dashboardTitle gives a human name to a dashboard type.
func dashboardTitle(dashboardType string) string {
	if dashboardType == "" {
		return "Home"
	}
	return strings.ToUpper(dashboardType[:1]) + dashboardType[1:]
}
