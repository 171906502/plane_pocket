package view

import (
	"database/sql"
	"errors"
	"net/http"
	"time"

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

// visitWriteThrottle bounds the write amplification of visit recording
// (tasklist L-7): a revisit whose existing row's visited_at is less than this
// old is swallowed instead of rewritten. A first visit always writes.
const visitWriteThrottle = time.Minute

// RecordVisit upserts the user_recent_visits row for one successful detail
// fetch: exactly one row per (user, entity_name, entity_identifier), where
// entity_identifier is the entity's Plane UUID — the value the read side and
// the serializer resolve it back by (FindByUUID). A revisit within the
// throttle window is a no-op; after it, the save alone refreshes visited_at
// (the column is an autodate field with OnUpdate).
//
// It must never fail the GET that triggered it: any error — including the
// panic this body could theoretically raise — is logged and swallowed, same
// philosophy as internal/hooks.contain (docs/06-PITFALLS.md D-12 premise 3).
func RecordVisit(app core.App, user *core.Record, workspaceID, projectID, entityName, entityUUID string) {
	defer func() {
		if r := recover(); r != nil {
			app.Logger().Error(
				"recent-visits: panic while recording visit (request unaffected)",
				"entity_name", entityName,
				"panic", r,
			)
		}
	}()
	if user == nil || entityName == "" || entityUUID == "" || workspaceID == "" {
		return
	}

	existing, err := app.FindFirstRecordByFilter(
		"user_recent_visits",
		"user = {:user} && entity_name = {:name} && entity_identifier = {:ident} && deleted_at = ''",
		dbx.Params{"user": user.Id, "name": entityName, "ident": entityUUID},
	)
	switch {
	case err == nil:
	case errors.Is(err, sql.ErrNoRows):
		existing = nil
	default:
		app.Logger().Error("recent-visits: lookup failed (visit not recorded)", "error", err.Error())
		return
	}

	if existing != nil {
		if time.Since(existing.GetDateTime("visited_at").Time()) < visitWriteThrottle {
			return
		}
		// The visited_at autodate field refreshes itself on update, so the
		// save alone is the revisit's timestamp bump.
		if err := app.Save(existing); err != nil {
			app.Logger().Error("recent-visits: revisit refresh failed", "record", existing.Id, "error", err.Error())
		}
		return
	}

	collection, err := app.FindCollectionByNameOrId("user_recent_visits")
	if err != nil {
		app.Logger().Error("recent-visits: collection missing", "error", err.Error())
		return
	}
	row := core.NewRecord(collection)
	row.Set("uuid", serializers.NewUUID())
	row.Set("user", user.Id)
	row.Set("workspace", workspaceID)
	if projectID != "" {
		row.Set("project", projectID)
	}
	row.Set("entity_name", entityName)
	row.Set("entity_identifier", entityUUID)
	// visited_at is an autodate field with OnCreate=false, so record.Set is
	// swallowed by the field's noopSetter — the initial timestamp has to go
	// in via SetRaw.
	row.SetRaw("visited_at", serializers.Now())
	if err := app.Save(row); err != nil {
		app.Logger().Error("recent-visits: insert failed", "error", err.Error())
	}
}
