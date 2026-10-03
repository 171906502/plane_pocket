package serializers

import (
	"strings"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
)

// Notification mirrors Django's NotificationSerializer
// (plane/app/serializers/notification.py). That serializer declares
// `fields = "__all__"` plus four computed keys, so every Notification model
// column is projected under its DRF name: `workspace`, `project`,
// `triggered_by` and `receiver` are FKs and serialize as UUIDs, not nested
// objects, while `triggered_by_details` is the one nested UserLite object.
//
// The PocketBase-only `uuid` column is deliberately omitted — Django has no
// such column and the frontends read `id`.
func Notification(app core.App, r *core.Record) map[string]any {
	if r == nil {
		return nil
	}

	var triggeredByDetails any
	if id := r.GetString("triggered_by"); id != "" {
		if user, err := app.FindRecordById("users", id); err == nil && user != nil {
			triggeredByDetails = UserLite(app, user)
		}
	}

	isIntake := isIntakeIssue(app, r.GetString("entity_identifier"))
	isMentioned := strings.Contains(r.GetString("sender"), "mentioned")

	return map[string]any{
		"id":                        UUID(r),
		"workspace":                 RelationUUID(app, r, "workspace"),
		"project":                   RelationUUID(app, r, "project"),
		"data":                      JSONValue(r, "data"),
		"entity_identifier":         NullableText(r, "entity_identifier"),
		"entity_name":               Text(r, "entity_name"),
		"title":                     Text(r, "title"),
		"message":                   JSONValue(r, "message"),
		"message_html":              Text(r, "message_html"),
		"message_stripped":          NullableText(r, "message_stripped"),
		"sender":                    Text(r, "sender"),
		"triggered_by":              RelationUUID(app, r, "triggered_by"),
		"triggered_by_details":      triggeredByDetails,
		"receiver":                  RelationUUID(app, r, "receiver"),
		"read_at":                   NullableDateTime(r, "read_at"),
		"snoozed_till":              NullableDateTime(r, "snoozed_till"),
		"archived_at":               NullableDateTime(r, "archived_at"),
		"created_at":                DateTime(r, "created_at"),
		"updated_at":                DateTime(r, "updated_at"),
		"created_by":                RelationUUID(app, r, "created_by"),
		"updated_by":                RelationUUID(app, r, "updated_by"),
		"deleted_at":                NullableDateTime(r, "deleted_at"),
		"is_inbox_issue":            isIntake,
		"is_intake_issue":           isIntake,
		"is_mentioned_notification": isMentioned,
	}
}

// UserNotificationPreference mirrors UserNotificationPreferenceSerializer
// (`fields = "__all__"`). The five booleans default to true in Django; the
// PocketBase columns carry no default, so the row is seeded at read time by the
// handler when absent.
func UserNotificationPreference(app core.App, r *core.Record) map[string]any {
	if r == nil {
		return nil
	}
	return map[string]any{
		"id":              UUID(r),
		"user":            RelationUUID(app, r, "user"),
		"workspace":       RelationUUID(app, r, "workspace"),
		"project":         RelationUUID(app, r, "project"),
		"property_change": r.GetBool("property_change"),
		"state_change":    r.GetBool("state_change"),
		"comment":         r.GetBool("comment"),
		"mention":         r.GetBool("mention"),
		"issue_completed": r.GetBool("issue_completed"),
		"created_at":      DateTime(r, "created_at"),
		"updated_at":      DateTime(r, "updated_at"),
		"created_by":      RelationUUID(app, r, "created_by"),
		"updated_by":      RelationUUID(app, r, "updated_by"),
		"deleted_at":      NullableDateTime(r, "deleted_at"),
	}
}

// isIntakeIssue reports whether `entityIdentifier` names an issue that has an
// open intake row, mirroring NotificationViewSet's `is_inbox_issue` /
// `is_intake_issue` annotations (`issue_intake__status__in=[0, 2, -2]`).
func isIntakeIssue(app core.App, entityIdentifier string) bool {
	if entityIdentifier == "" {
		return false
	}
	issue, _ := FindByUUID(app, "issues", entityIdentifier)
	if issue == nil {
		return false
	}
	row, err := app.FindFirstRecordByFilter(
		"intake_issues",
		"issue = {:issue} && (status = 0 || status = 2 || status = -2)",
		dbx.Params{"issue": issue.Id},
	)
	return err == nil && row != nil
}
