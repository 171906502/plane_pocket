package router

import (
	"github.com/pocketbase/pocketbase/core"

	"github.com/plane-pocketbase/plane-pocketbase/internal/handlers/search"
	"github.com/plane-pocketbase/plane-pocketbase/internal/middleware"
)

// RegisterSearch wires global search and the analytics/dashboard endpoints
// (`/api/workspaces/{slug}/search/`, `/api/workspaces/{slug}/analytics/…`).
// Contract: spec/api-spec.yaml, groups `search` and `analytics`.
//
// Every route runs through workspaceGroup, so a caller without an active
// workspace membership gets a 404 (never a 403), and search results can never
// leak rows from a workspace the caller is not in.
func RegisterSearch(e *core.ServeEvent, app core.App) error {
	h := search.New(app)

	ws := workspaceGroup(e, app)

	// search group.
	ws.GET("/search/{$}", h.GlobalSearch)
	ws.GET("/entity-search/{$}", h.EntitySearch)

	// analytics group.
	ws.GET("/analytics/{$}", h.Analytics)
	ws.GET("/analytic-view/{$}", h.AnalyticViewList)
	ws.POST("/analytic-view/{$}", h.AnalyticViewCreate)
	ws.GET("/analytic-view/{pk}/{$}", h.AnalyticViewDetail)
	ws.PATCH("/analytic-view/{pk}/{$}", h.AnalyticViewUpdate)
	ws.DELETE("/analytic-view/{pk}/{$}", h.AnalyticViewDelete)
	ws.GET("/saved-analytic-view/{analytic_id}/{$}", h.SavedAnalyticView)
	ws.GET("/default-analytics/{$}", h.DefaultAnalytics)
	ws.GET("/project-stats/{$}", h.ProjectStats)
	ws.GET("/advance-analytics/{$}", h.AdvanceAnalytics)
	ws.GET("/advance-analytics-stats/{$}", h.AdvanceAnalyticsStats)
	ws.GET("/advance-analytics-charts/{$}", h.AdvanceAnalyticsCharts)

	// Project-scoped search + analytics.
	proj := ws.Group("/projects/{project_id}").BindFunc(middleware.ResolveProject(app))
	proj.GET("/search-issues/{$}", h.IssueSearch)
	proj.GET("/advance-analytics/{$}", h.ProjectAdvanceAnalytics)
	proj.GET("/advance-analytics-stats/{$}", h.ProjectAdvanceAnalyticsStats)
	proj.GET("/advance-analytics-charts/{$}", h.ProjectAdvanceAnalyticsCharts)

	return nil
}
