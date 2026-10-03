package workspaces

import (
	"net/http"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"

	"github.com/plane-pocketbase/plane-pocketbase/internal/middleware"
	"github.com/plane-pocketbase/plane-pocketbase/internal/serializers"
)

// This file implements the workspace-scoped read/preference endpoints the web
// app calls on every workspace load:
//
//	GET   /api/workspaces/{slug}/states/               WorkspaceStatesEndpoint
//	GET   /api/workspaces/{slug}/user-properties/      WorkspaceUserPropertiesEndpoint
//	PATCH /api/workspaces/{slug}/user-properties/      WorkspaceUserPropertiesEndpoint
//	GET   /api/workspaces/{slug}/sidebar-preferences/  WorkspaceUserPreferenceViewSet
//	PATCH /api/workspaces/{slug}/sidebar-preferences/  WorkspaceUserPreferenceViewSet
//
// All five were missing, and all five *appeared* to work: before the router was
// anchored with `{$}` (internal/router/patterns_test.go), the subtree pattern
// `/api/workspaces/{slug}/` answered every unregistered path with the workspace
// object and HTTP 200. The frontend accepted those objects as truth. Anchoring
// turned them into honest 404s — which is how these gaps were found.

// States serves GET /api/workspaces/{slug}/states/ (WorkspaceStatesEndpoint).
//
// Unlike the project-scoped `…/projects/{id}/states/`, this spans every project
// in the workspace that the caller is an active member of. Django also computes
// a per-group `order` (1/n, 2/n, …) rather than using `sequence`, so the
// frontends can lay the states out proportionally within their group.
func (h *Handler) States(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Workspace == nil {
		return serializers.NotFound(e, "")
	}
	user := middleware.CurrentUser(e)
	if user == nil {
		return serializers.NotFound(e, "")
	}

	// Triage is excluded by Django's StateManager (`exclude(group="triage")`)
	// *and* by the endpoint's own `is_triage=False` filter. See defect #15 in
	// tests/WORKFLOW_TEST_REPORT.md: the seeded Triage state has
	// `is_triage=False`, so the group check is the one that actually matters.
	var rows []struct {
		ID string `db:"id"`
	}
	err := h.App.DB().NewQuery(`
		SELECT s.id AS id
		FROM states s
		JOIN projects p ON p.id = s.project
		JOIN project_members pm
			ON pm.project = p.id AND pm.is_active = 1 AND pm.deleted_at = ''
		WHERE s.workspace = {:workspace}
			AND s.deleted_at = ''
			AND p.deleted_at = ''
			AND p.archived_at = ''
			AND pm.member = {:member}
			AND s."group" != 'triage'
			AND s.is_triage = 0
		ORDER BY s.sequence`).Bind(dbx.Params{
		"workspace": scope.Workspace.Id,
		"member":    user.Id,
	}).All(&rows)
	if err != nil {
		return err
	}

	records := make([]*core.Record, 0, len(rows))
	for _, row := range rows {
		if state, _ := h.App.FindRecordById("states", row.ID); state != nil {
			records = append(records, state)
		}
	}

	// order = index / count within the state's group.
	groupCounts := map[string]int{}
	for _, state := range records {
		groupCounts[state.GetString("group")]++
	}
	groupSeen := map[string]int{}
	results := make([]any, 0, len(records))
	for _, state := range records {
		group := state.GetString("group")
		groupSeen[group]++
		order := float64(groupSeen[group]) / float64(groupCounts[group])
		results = append(results, serializers.State(h.App, state, order))
	}
	return e.JSON(http.StatusOK, results)
}

// UserProperties serves GET/PATCH /api/workspaces/{slug}/user-properties/
// (WorkspaceUserPropertiesEndpoint) — the caller's saved filters and display
// preferences for this workspace.
//
// Django's `get_or_create` means GET has a side effect: the row is created on
// first read. That matters, because PATCH is issued against a row the frontend
// never explicitly created.
func (h *Handler) UserProperties(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Workspace == nil {
		return serializers.NotFound(e, "")
	}
	user := middleware.CurrentUser(e)
	if user == nil {
		return serializers.Unauthorized(e, "Authentication credentials were not provided.")
	}

	record, err := h.App.FindFirstRecordByFilter(
		"workspace_user_properties",
		"workspace = {:workspace} && user = {:user} && deleted_at = ''",
		dbx.Params{"workspace": scope.Workspace.Id, "user": user.Id},
	)
	if err != nil || record == nil {
		record, err = h.newUserProperties(scope.Workspace.Id, user.Id)
		if err != nil {
			return err
		}
	}

	if e.Request.Method == http.MethodPatch {
		body := map[string]any{}
		if err := e.BindBody(&body); err != nil {
			return serializers.BadRequest(e, "Invalid request body.")
		}
		for _, field := range []string{
			"filters", "display_filters", "display_properties", "rich_filters",
			"navigation_project_limit", "navigation_control_preference",
		} {
			if v, ok := body[field]; ok {
				record.Set(field, v)
			}
		}
		record.Set("updated_by", user.Id)
		if err := h.App.Save(record); err != nil {
			return serializers.Validation(e, "display_filters", err.Error())
		}
	}

	return e.JSON(http.StatusOK, serializers.WorkspaceUserProperties(h.App, record))
}

// newUserProperties creates the caller's properties row with Django's defaults.
// PocketBase has no field-level defaults, so every value is set explicitly.
func (h *Handler) newUserProperties(workspaceID, userID string) (*core.Record, error) {
	collection, err := h.App.FindCollectionByNameOrId("workspace_user_properties")
	if err != nil {
		return nil, err
	}
	props := serializers.DefaultWorkspaceProps()
	record := core.NewRecord(collection)
	record.Set("uuid", serializers.NewUUID())
	record.Set("workspace", workspaceID)
	record.Set("user", userID)
	record.Set("filters", props["filters"])
	record.Set("display_filters", props["display_filters"])
	record.Set("display_properties", props["display_properties"])
	record.Set("rich_filters", map[string]any{})
	record.Set("navigation_project_limit", 10)
	record.Set("navigation_control_preference", "ACCORDION")
	record.Set("created_by", userID)
	record.Set("updated_by", userID)
	if err := h.App.Save(record); err != nil {
		return nil, err
	}
	return record, nil
}

// SidebarPreferences serves GET/PATCH /api/workspaces/{slug}/sidebar-preferences/
// (WorkspaceUserPreferenceViewSet).
//
// The GET response is a **map keyed by preference key**, not a list — the
// frontend reads `sidebarPreferences[key].is_pinned`. Django seeds any missing
// key on read (pinning drafts / your_work / stickies), so the response always
// carries all seven keys.
//
// The PATCH body is a **list** of `{key, is_pinned, sort_order}`; unknown keys
// are skipped rather than rejected.
func (h *Handler) SidebarPreferences(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Workspace == nil {
		return serializers.NotFound(e, "")
	}
	user := middleware.CurrentUser(e)
	if user == nil {
		return serializers.Unauthorized(e, "Authentication credentials were not provided.")
	}

	existing, err := h.App.FindRecordsByFilter(
		"workspace_user_preferences",
		"workspace = {:workspace} && user = {:user} && deleted_at = ''",
		"sort_order", 0, 0,
		dbx.Params{"workspace": scope.Workspace.Id, "user": user.Id},
	)
	if err != nil {
		return err
	}
	byKey := make(map[string]*core.Record, len(existing))
	for _, r := range existing {
		byKey[r.GetString("key")] = r
	}

	if e.Request.Method == http.MethodPatch {
		var body []map[string]any
		if err := e.BindBody(&body); err != nil {
			return serializers.BadRequest(e, "Invalid request body.")
		}
		for _, entry := range body {
			key := asString(entry["key"])
			if key == "" {
				continue
			}
			record := byKey[key]
			if record == nil {
				continue
			}
			if v, ok := entry["is_pinned"]; ok {
				record.Set("is_pinned", v)
			}
			if v, ok := entry["sort_order"]; ok {
				record.Set("sort_order", v)
			}
			record.Set("updated_by", user.Id)
			if err := h.App.Save(record); err != nil {
				return err
			}
		}
	} else {
		// Seed any key the caller has never had a row for. Django assigns
		// `sort_order = 65535 + i*10000` by position among the *missing* keys,
		// and pins drafts / your_work / stickies.
		collection, err := h.App.FindCollectionByNameOrId("workspace_user_preferences")
		if err != nil {
			return err
		}
		index := 0
		for _, key := range serializers.SidebarPreferenceKeys {
			if _, ok := byKey[key]; ok {
				continue
			}
			record := core.NewRecord(collection)
			record.Set("uuid", serializers.NewUUID())
			record.Set("workspace", scope.Workspace.Id)
			record.Set("user", user.Id)
			record.Set("key", key)
			record.Set("is_pinned", serializers.SidebarPreferencePinnedByDefault(key))
			record.Set("sort_order", float64(65535+index*10000))
			record.Set("created_by", user.Id)
			record.Set("updated_by", user.Id)
			if err := h.App.Save(record); err != nil {
				return err
			}
			byKey[key] = record
			index++
		}
	}

	out := map[string]any{}
	for _, key := range serializers.SidebarPreferenceKeys {
		if record, ok := byKey[key]; ok {
			out[key] = serializers.WorkspaceUserPreference(record)
		}
	}
	return e.JSON(http.StatusOK, out)
}

// HomePreferences serves the workspace home-page widget preferences:
//
//	GET   /api/workspaces/{slug}/home-preferences/         -> list
//	PATCH /api/workspaces/{slug}/home-preferences/{key}/   -> object
//
// (Django: WorkspaceHomePreferenceViewSet.)
//
// The GET seeds missing rows on read, exactly like user-properties, and returns
// a **list** of `{key, is_enabled, config, sort_order}`. Note the deliberate
// asymmetry with the sidebar: this one is a list, the sidebar is a map.
//
// Django's seeding loop assigns `sort_order = 1000 - counter` and re-bulk-creates
// the accumulated set each iteration with `ignore_conflicts=True`, so the net
// effect is that the three keys come out at 999, 998, 997 in declaration order.
func (h *Handler) HomePreferences(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Workspace == nil {
		return serializers.NotFound(e, "")
	}
	user := middleware.CurrentUser(e)
	if user == nil {
		return serializers.Unauthorized(e, "Authentication credentials were not provided.")
	}

	existing, err := h.App.FindRecordsByFilter(
		"workspace_home_preferences",
		"workspace = {:workspace} && user = {:user} && deleted_at = ''",
		"sort_order", 0, 0,
		dbx.Params{"workspace": scope.Workspace.Id, "user": user.Id},
	)
	if err != nil {
		return err
	}
	byKey := make(map[string]*core.Record, len(existing))
	for _, r := range existing {
		byKey[r.GetString("key")] = r
	}

	if e.Request.Method == http.MethodPatch {
		key := e.Request.PathValue("key")
		record := byKey[key]
		if record == nil {
			// Django returns 400 (not 404) here, with this exact body.
			return serializers.BadRequest(e, "Preference not found")
		}
		body := map[string]any{}
		if err := e.BindBody(&body); err != nil {
			return serializers.BadRequest(e, "Invalid request body.")
		}
		if v, ok := body["is_enabled"]; ok {
			record.Set("is_enabled", v)
		}
		if v, ok := body["sort_order"]; ok {
			record.Set("sort_order", v)
		}
		record.Set("updated_by", user.Id)
		if err := h.App.Save(record); err != nil {
			return serializers.Validation(e, "is_enabled", err.Error())
		}
		return e.JSON(http.StatusOK, serializers.WorkspaceHomePreferencePatched(record))
	}

	// Seed any missing key. The two tutorial keys are never seeded — the view
	// filters them out before the loop.
	collection, err := h.App.FindCollectionByNameOrId("workspace_home_preferences")
	if err != nil {
		return err
	}
	for i, key := range serializers.HomeWidgetSeededKeys {
		if _, ok := byKey[key]; ok {
			continue
		}
		record := core.NewRecord(collection)
		record.Set("uuid", serializers.NewUUID())
		record.Set("workspace", scope.Workspace.Id)
		record.Set("user", user.Id)
		record.Set("key", key)
		record.Set("is_enabled", true)
		record.Set("config", map[string]any{})
		record.Set("sort_order", float64(999-i))
		record.Set("created_by", user.Id)
		record.Set("updated_by", user.Id)
		if err := h.App.Save(record); err != nil {
			return err
		}
		byKey[key] = record
	}

	out := make([]any, 0, len(serializers.HomeWidgetSeededKeys))
	for _, key := range serializers.HomeWidgetSeededKeys {
		if record, ok := byKey[key]; ok {
			out = append(out, serializers.WorkspaceHomePreference(record))
		}
	}
	return e.JSON(http.StatusOK, out)
}
