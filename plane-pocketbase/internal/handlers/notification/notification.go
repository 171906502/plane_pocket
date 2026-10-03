// Package notification implements Plane's notification endpoints:
//
//	GET       /api/workspaces/{slug}/users/notifications/            (NotificationViewSet.list)
//	GET/PATCH/DELETE …/users/notifications/{pk}/                     (retrieve/partial_update/destroy)
//	POST/DELETE …/users/notifications/{pk}/read/                     (mark_read / mark_unread)
//	POST/DELETE …/users/notifications/{pk}/archive/                  (archive / unarchive)
//	GET       …/users/notifications/unread/                          (UnreadNotificationEndpoint)
//	POST      …/users/notifications/mark-all-read/                   (MarkAllReadNotificationViewSet)
//	GET/PATCH /api/users/me/notification-preferences/                (UserNotificationPreferenceEndpoint)
//
// Every notification is scoped to its receiver: another user's row is a 404,
// never a 403 (Django's `Notification.objects.get(receiver=request.user, …)`
// raises DoesNotExist, which the base view maps to 404).
//
// `{pk}` is a Plane UUID.
package notification

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"

	"github.com/plane-pocketbase/plane-pocketbase/internal/middleware"
	"github.com/plane-pocketbase/plane-pocketbase/internal/serializers"
)

// Handler holds the app dependency for the notification routes.
type Handler struct {
	App core.App
}

// New returns a notification handler bound to app.
func New(app core.App) *Handler {
	return &Handler{App: app}
}

// List serves GET …/users/notifications/. Django returns a cursor-paginated
// envelope only when both `per_page` and `cursor` are present; otherwise it
// returns a plain array. Both shapes are reproduced because Plane's web client
// always sends both (so it always sees the envelope) while other callers may not.
func (h *Handler) List(e *core.RequestEvent) error {
	scope, user, err := h.context(e)
	if err != nil {
		return err
	}
	query := e.Request.URL.Query()

	filter := "workspace = {:workspace} && receiver = {:receiver} && entity_name = 'issue' && deleted_at = ''"
	params := dbx.Params{"workspace": scope.Workspace.Id, "receiver": user.Id}

	now := serializers.Now()
	switch query.Get("snoozed") {
	case "true":
		filter += " && (snoozed_till < {:now} || snoozed_till != '')"
		params["now"] = now
	default:
		filter += " && (snoozed_till >= {:now} || snoozed_till = '')"
		params["now"] = now
	}

	if query.Get("archived") == "true" {
		filter += " && archived_at != ''"
	} else {
		filter += " && archived_at = ''"
	}

	switch query.Get("read") {
	case "false":
		filter += " && read_at = ''"
	case "true":
		filter += " && read_at != ''"
	}

	if query.Get("mentioned") != "" {
		filter += " && sender ~ 'mentioned'"
	} else {
		filter += " && sender !~ 'mentioned'"
	}

	// `type` is a comma-separated list; any of subscribed/assigned/created adds
	// an OR branch on `entity_identifier`. An unrecognised value (the default
	// "all") leaves the queryset untouched.
	requested := false
	ids := make([]string, 0)
	for _, kind := range strings.Split(defaultString(query.Get("type"), "all"), ",") {
		kind = strings.TrimSpace(kind)
		if !isListType(kind) {
			continue
		}
		requested = true
		branchIDs, zeroOut := h.issueUUIDsForType(scope, user, kind)
		if zeroOut {
			return h.emptyList(e, query)
		}
		ids = append(ids, branchIDs...)
	}
	if requested {
		if len(ids) == 0 {
			params["__none"] = ""
			filter += " && entity_identifier = {:__none}"
		} else {
			filter += " && " + orUUIDExpr("entity_identifier", dedupe(ids), params)
		}
	}

	if query.Get("per_page") != "" && query.Get("cursor") != "" {
		perPage, cursor := serializers.ListParams(e, serializers.MaxPerPage)
		envelope, err := serializers.Paginate(h.App, serializers.ListQuery{
			Collection: "notifications",
			Filter:     filter,
			Params:     params,
			Sort:       notificationOrderBy(query.Get("order_by")),
			PerPage:    perPage,
			Cursor:     cursor,
		}, func(record *core.Record) any {
			return serializers.Notification(h.App, record)
		})
		if err != nil {
			return err
		}
		return e.JSON(http.StatusOK, envelope)
	}

	records, err := h.App.FindRecordsByFilter(
		"notifications", filter, "snoozed_till,-created_at", 0, 0, params,
	)
	if err != nil {
		return err
	}
	results := make([]any, 0, len(records))
	for _, record := range records {
		results = append(results, serializers.Notification(h.App, record))
	}
	return e.JSON(http.StatusOK, results)
}

// emptyList answers with an empty page in whichever shape List would have used.
func (h *Handler) emptyList(e *core.RequestEvent, query map[string][]string) error {
	perPage, cursor := serializers.ListParams(e, serializers.MaxPerPage)
	if query["per_page"] != nil && query["cursor"] != nil {
		envelope, err := serializers.Paginate(h.App, serializers.ListQuery{
			Collection: "notifications",
			Filter:     "id = {:none}",
			Params:     dbx.Params{"none": ""},
			PerPage:    perPage,
			Cursor:     cursor,
		}, func(record *core.Record) any { return serializers.Notification(h.App, record) })
		if err != nil {
			return err
		}
		return e.JSON(http.StatusOK, envelope)
	}
	return e.JSON(http.StatusOK, []any{})
}

// Detail serves GET …/users/notifications/{pk}/.
func (h *Handler) Detail(e *core.RequestEvent) error {
	record, err := h.find(e)
	if err != nil {
		return err
	}
	return e.JSON(http.StatusOK, serializers.Notification(h.App, record))
}

// Update serves PATCH …/users/notifications/{pk}/. Django's partial_update only
// ever writes `snoozed_till` (defaulting to null when the key is absent), so a
// PATCH clears the snooze unless the body supplies a new value.
func (h *Handler) Update(e *core.RequestEvent) error {
	record, err := h.find(e)
	if err != nil {
		return err
	}
	body := map[string]any{}
	if err := e.BindBody(&body); err != nil {
		return serializers.BadRequest(e, "Invalid request body.")
	}
	if v, ok := body["snoozed_till"]; ok && v != nil {
		record.Set("snoozed_till", v)
	} else {
		record.Set("snoozed_till", nil)
	}
	if err := h.App.Save(record); err != nil {
		return serializers.Validation(e, "detail", err.Error())
	}
	return e.JSON(http.StatusOK, serializers.Notification(h.App, record))
}

// Delete serves DELETE …/users/notifications/{pk}/ (soft delete, matching
// DRF's ModelViewSet.destroy over Plane's SoftDeleteModel).
func (h *Handler) Delete(e *core.RequestEvent) error {
	record, err := h.find(e)
	if err != nil {
		return err
	}
	record.Set("deleted_at", serializers.Now())
	if err := h.App.Save(record); err != nil {
		return err
	}
	return e.NoContent(http.StatusNoContent)
}

// MarkRead serves POST …/users/notifications/{pk}/read/.
func (h *Handler) MarkRead(e *core.RequestEvent) error {
	return h.setRead(e, true)
}

// MarkUnread serves DELETE …/users/notifications/{pk}/read/.
func (h *Handler) MarkUnread(e *core.RequestEvent) error {
	return h.setRead(e, false)
}

func (h *Handler) setRead(e *core.RequestEvent, read bool) error {
	record, err := h.find(e)
	if err != nil {
		return err
	}
	if read {
		record.Set("read_at", serializers.Now())
	} else {
		record.Set("read_at", nil)
	}
	if err := h.App.Save(record); err != nil {
		return err
	}
	return e.JSON(http.StatusOK, serializers.Notification(h.App, record))
}

// Archive serves POST …/users/notifications/{pk}/archive/.
func (h *Handler) Archive(e *core.RequestEvent) error {
	return h.setArchived(e, true)
}

// Unarchive serves DELETE …/users/notifications/{pk}/archive/.
func (h *Handler) Unarchive(e *core.RequestEvent) error {
	return h.setArchived(e, false)
}

func (h *Handler) setArchived(e *core.RequestEvent, archived bool) error {
	record, err := h.find(e)
	if err != nil {
		return err
	}
	if archived {
		record.Set("archived_at", serializers.Now())
	} else {
		record.Set("archived_at", nil)
	}
	if err := h.App.Save(record); err != nil {
		return err
	}
	return e.JSON(http.StatusOK, serializers.Notification(h.App, record))
}

// UnreadCount serves GET …/users/notifications/unread/. It counts notifications
// that are unread, unarchived and unsnoozed, split into mention and non-mention.
func (h *Handler) UnreadCount(e *core.RequestEvent) error {
	scope, user, err := h.context(e)
	if err != nil {
		return err
	}
	base := "workspace = {:workspace} && receiver = {:receiver} && read_at = '' && archived_at = '' && snoozed_till = '' && deleted_at = ''"
	params := dbx.Params{"workspace": scope.Workspace.Id, "receiver": user.Id}

	mentions, err := h.App.FindRecordsByFilter(
		"notifications", base+" && sender ~ 'mentioned'", "", 0, 0, params,
	)
	if err != nil {
		return err
	}
	others, err := h.App.FindRecordsByFilter(
		"notifications", base+" && sender !~ 'mentioned'", "", 0, 0, params,
	)
	if err != nil {
		return err
	}
	return e.JSON(http.StatusOK, map[string]any{
		"total_unread_notifications_count":   len(others),
		"mention_unread_notifications_count": len(mentions),
	})
}

// MarkAllRead serves POST …/users/notifications/mark-all-read/. Body:
// `{snoozed, archived, type}` where `type` is a single kind (all/watching/
// assigned/created).
func (h *Handler) MarkAllRead(e *core.RequestEvent) error {
	scope, user, err := h.context(e)
	if err != nil {
		return err
	}
	body := map[string]any{}
	if err := e.BindBody(&body); err != nil {
		return serializers.BadRequest(e, "Invalid request body.")
	}

	filter := "workspace = {:workspace} && receiver = {:receiver} && read_at = '' && deleted_at = ''"
	params := dbx.Params{"workspace": scope.Workspace.Id, "receiver": user.Id}
	now := serializers.Now()

	if truthy(body["snoozed"]) {
		filter += " && (snoozed_till < {:now} || snoozed_till != '')"
	} else {
		filter += " && (snoozed_till >= {:now} || snoozed_till = '')"
	}
	params["now"] = now

	if truthy(body["archived"]) {
		filter += " && archived_at != ''"
	} else {
		filter += " && archived_at = ''"
	}

	kind := asString(body["type"])
	if kind != "" && kind != "all" {
		branchIDs, zeroOut := h.issueUUIDsForType(scope, user, kind)
		if zeroOut {
			return e.JSON(http.StatusOK, map[string]any{"message": "Successful"})
		}
		if len(branchIDs) == 0 {
			params["__none"] = ""
			filter += " && entity_identifier = {:__none}"
		} else {
			filter += " && " + orUUIDExpr("entity_identifier", dedupe(branchIDs), params)
		}
	}

	records, err := h.App.FindRecordsByFilter("notifications", filter, "", 0, 0, params)
	if err != nil {
		return err
	}
	for _, record := range records {
		record.Set("read_at", now)
		if err := h.App.Save(record); err != nil {
			return err
		}
	}
	return e.JSON(http.StatusOK, map[string]any{"message": "Successful"})
}

// PrefsGet serves GET /api/users/me/notification-preferences/.
//
// Django's view does `UserNotificationPreference.objects.get(user=request.user)`,
// relying on a post_save receiver to create the row with all booleans true. That
// receiver is out of this slice, so the row is seeded lazily here with the same
// defaults instead of 404ing.
func (h *Handler) PrefsGet(e *core.RequestEvent) error {
	record, err := h.preference(e)
	if err != nil {
		return err
	}
	return e.JSON(http.StatusOK, serializers.UserNotificationPreference(h.App, record))
}

// PrefsPatch serves PATCH /api/users/me/notification-preferences/.
func (h *Handler) PrefsPatch(e *core.RequestEvent) error {
	record, err := h.preference(e)
	if err != nil {
		return err
	}
	body := map[string]any{}
	if err := e.BindBody(&body); err != nil {
		return serializers.BadRequest(e, "Invalid request body.")
	}
	for _, field := range []string{"property_change", "state_change", "comment", "mention", "issue_completed"} {
		if v, ok := body[field]; ok {
			record.Set(field, v)
		}
	}
	if err := h.App.Save(record); err != nil {
		return serializers.Validation(e, "detail", err.Error())
	}
	return e.JSON(http.StatusOK, serializers.UserNotificationPreference(h.App, record))
}

// ---------------------------------------------------------------------------
// shared helpers
// ---------------------------------------------------------------------------

// context resolves the workspace scope and the current user, or returns the
// appropriate error response.
func (h *Handler) context(e *core.RequestEvent) (*middleware.Scope, *core.Record, error) {
	scope := middleware.ScopeOf(e)
	user := middleware.CurrentUser(e)
	if scope == nil || scope.Workspace == nil {
		return nil, nil, serializers.NotFound(e, "")
	}
	if user == nil {
		return nil, nil, serializers.Unauthorized(e, "Authentication credentials were not provided.")
	}
	return scope, user, nil
}

// find resolves a notification that belongs to the caller and the resolved
// workspace. Absent, foreign, or soft-deleted rows all surface as Plane's 404.
func (h *Handler) find(e *core.RequestEvent) (*core.Record, error) {
	scope, user, err := h.context(e)
	if err != nil {
		return nil, err
	}
	record, err := serializers.FindByUUID(h.App, "notifications", e.Request.PathValue("pk"))
	if err != nil || record == nil {
		return nil, serializers.NotFound(e, "The required object does not exist.")
	}
	if record.GetString("workspace") != scope.Workspace.Id ||
		record.GetString("receiver") != user.Id ||
		record.GetString("deleted_at") != "" {
		return nil, serializers.NotFound(e, "The required object does not exist.")
	}
	return record, nil
}

// preference returns (creating if needed) the caller's preference row.
func (h *Handler) preference(e *core.RequestEvent) (*core.Record, error) {
	user := middleware.CurrentUser(e)
	if user == nil {
		return nil, serializers.Unauthorized(e, "Authentication credentials were not provided.")
	}
	existing, err := h.App.FindFirstRecordByFilter(
		"user_notification_preferences",
		"user = {:user} && deleted_at = ''",
		dbx.Params{"user": user.Id},
	)
	if err == nil && existing != nil {
		return existing, nil
	}
	collection, err := h.App.FindCollectionByNameOrId("user_notification_preferences")
	if err != nil {
		return nil, err
	}
	record := core.NewRecord(collection)
	record.Set("uuid", serializers.NewUUID())
	record.Set("user", user.Id)
	record.Set("property_change", true)
	record.Set("state_change", true)
	record.Set("comment", true)
	record.Set("mention", true)
	record.Set("issue_completed", true)
	if err := h.App.Save(record); err != nil {
		return nil, err
	}
	return record, nil
}

// issueUUIDsForType resolves the issue UUIDs backing one notification `type`
// branch. `zeroOut` mirrors MarkAllReadNotificationViewSet's behaviour for a
// guest filtering by "created": the whole queryset becomes empty.
func (h *Handler) issueUUIDsForType(scope *middleware.Scope, user *core.Record, kind string) ([]string, bool) {
	switch kind {
	case "subscribed", "watching":
		return h.subscribedIssueUUIDs(scope, user), false
	case "assigned":
		return h.assignedIssueUUIDs(scope, user), false
	case "created":
		if scope.WorkspaceRole < middleware.RoleMember {
			return nil, true
		}
		return h.createdIssueUUIDs(scope, user), false
	}
	return nil, false
}

// subscribedIssueUUIDs returns issues the caller watches but neither created nor
// is assigned to.
func (h *Handler) subscribedIssueUUIDs(scope *middleware.Scope, user *core.Record) []string {
	rows, err := h.App.FindRecordsByFilter(
		"issue_subscribers",
		"workspace = {:workspace} && subscriber = {:user} && deleted_at = ''",
		"", 0, 0,
		dbx.Params{"workspace": scope.Workspace.Id, "user": user.Id},
	)
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		issue, _ := h.App.FindRecordById("issues", row.GetString("issue"))
		if issue == nil || issue.GetString("created_by") == user.Id {
			continue
		}
		if h.isAssigned(issue.Id, user.Id) {
			continue
		}
		out = append(out, serializers.UUID(issue))
	}
	return out
}

// assignedIssueUUIDs returns issues assigned to the caller.
func (h *Handler) assignedIssueUUIDs(scope *middleware.Scope, user *core.Record) []string {
	rows, err := h.App.FindRecordsByFilter(
		"issue_assignees",
		"workspace = {:workspace} && assignee = {:user} && deleted_at = ''",
		"", 0, 0,
		dbx.Params{"workspace": scope.Workspace.Id, "user": user.Id},
	)
	if err != nil {
		return nil
	}
	return h.issueUUIDsFromRows(rows)
}

// createdIssueUUIDs returns issues the caller created.
func (h *Handler) createdIssueUUIDs(scope *middleware.Scope, user *core.Record) []string {
	rows, err := h.App.FindRecordsByFilter(
		"issues",
		"workspace = {:workspace} && created_by = {:user} && deleted_at = ''",
		"", 0, 0,
		dbx.Params{"workspace": scope.Workspace.Id, "user": user.Id},
	)
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		out = append(out, serializers.UUID(row))
	}
	return out
}

// issueUUIDsFromRows maps join-table rows to their issue's Plane UUID.
func (h *Handler) issueUUIDsFromRows(rows []*core.Record) []string {
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		issue, _ := h.App.FindRecordById("issues", row.GetString("issue"))
		if issue == nil {
			continue
		}
		out = append(out, serializers.UUID(issue))
	}
	return out
}

// isAssigned reports whether the issue is assigned to the user.
func (h *Handler) isAssigned(issueID, userID string) bool {
	row, err := h.App.FindFirstRecordByFilter(
		"issue_assignees",
		"issue = {:issue} && assignee = {:user} && deleted_at = ''",
		dbx.Params{"issue": issueID, "user": userID},
	)
	return err == nil && row != nil
}

// isListType reports whether a comma-separated `type` value contributes a branch.
func isListType(kind string) bool {
	switch kind {
	case "subscribed", "assigned", "created":
		return true
	}
	return false
}

// notificationOrderBy maps Plane's `order_by` allowlist onto a PocketBase sort.
func notificationOrderBy(param string) string {
	switch param {
	case "created_at":
		return "created_at"
	case "updated_at":
		return "updated_at"
	case "-updated_at":
		return "-updated_at"
	default:
		return "-created_at"
	}
}

// orUUIDExpr builds `(col = {:t0} || col = {:t1} …)` and registers the params.
func orUUIDExpr(column string, ids []string, params dbx.Params) string {
	parts := make([]string, 0, len(ids))
	for i, id := range ids {
		key := fmt.Sprintf("t%d", i)
		parts = append(parts, column+" = {:"+key+"}")
		params[key] = id
	}
	return "(" + strings.Join(parts, " || ") + ")"
}

// dedupe preserves order while removing repeated UUIDs.
func dedupe(ids []string) []string {
	seen := make(map[string]struct{}, len(ids))
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

func defaultString(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

// truthy mirrors Django's truthiness for JSON booleans and their string forms.
func truthy(v any) bool {
	switch value := v.(type) {
	case bool:
		return value
	case string:
		return value != "" && value != "false" && value != "0"
	default:
		return false
	}
}

func asString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}
