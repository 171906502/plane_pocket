package serializers

import (
	"github.com/pocketbase/pocketbase/core"
)

// AnalyticView renders an `analytic_views` row in Plane's AnalyticViewSerializer
// shape (`fields = "__all__"`): the workspace relation is projected back to its
// Plane UUID and the audit columns keep DRF's names.
func AnalyticView(app core.App, r *core.Record) map[string]any {
	return map[string]any{
		"id":          UUID(r),
		"created_at":  DateTime(r, "created_at"),
		"updated_at":  DateTime(r, "updated_at"),
		"created_by":  RelationUUID(app, r, "created_by"),
		"updated_by":  RelationUUID(app, r, "updated_by"),
		"name":        Text(r, "name"),
		"description": NullableText(r, "description"),
		"query":       JSONValue(r, "query"),
		"query_dict":  JSONValue(r, "query_dict"),
		"workspace":   RelationUUID(app, r, "workspace"),
	}
}
