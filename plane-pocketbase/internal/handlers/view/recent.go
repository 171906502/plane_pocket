package view

import (
	"net/http"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"

	"github.com/plane-pocketbase/plane-pocketbase/internal/middleware"
	"github.com/plane-pocketbase/plane-pocketbase/internal/serializers"
)

// RecentVisitsList serves GET /api/workspaces/{slug}/recent-visits/ — a plain
// array of the caller's 20 most recent visits, restricted to the entity names
// Django's UserRecentVisitViewSet allows (issue, page, project) and optionally
// narrowed by an `entity_name` query parameter.
func (h *Handler) RecentVisitsList(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	user := middleware.CurrentUser(e)
	if scope == nil || scope.Workspace == nil {
		return serializers.NotFound(e, "")
	}
	if user == nil {
		return serializers.Unauthorized(e, "Authentication credentials were not provided.")
	}

	filter := "workspace = {:workspace} && user = {:user} && deleted_at = '' && (entity_name = 'issue' || entity_name = 'page' || entity_name = 'project')"
	params := dbx.Params{"workspace": scope.Workspace.Id, "user": user.Id}
	if entityName := e.Request.URL.Query().Get("entity_name"); entityName != "" {
		filter += " && entity_name = {:entity_name}"
		params["entity_name"] = entityName
	}

	records, err := h.App.FindRecordsByFilter("user_recent_visits", filter, "-visited_at", 20, 0, params)
	if err != nil {
		return err
	}
	results := make([]any, 0, len(records))
	for _, record := range records {
		results = append(results, serializers.UserRecentVisit(h.App, record))
	}
	return e.JSON(http.StatusOK, results)
}
