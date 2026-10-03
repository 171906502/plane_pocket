package serializers

import (
	"fmt"

	"github.com/pocketbase/pocketbase/core"
)

// FileAsset entity types. Plane's FileAsset.EntityTypeContext (spec §3.4) — the
// enum is *not* wired to a Django `choices=` so it carries no DB constraint, but
// the API layer keys `asset_url` and the upload guards off these exact strings.
const (
	EntityIssueAttachment    = "ISSUE_ATTACHMENT"
	EntityIssueDescription   = "ISSUE_DESCRIPTION"
	EntityCommentDescription = "COMMENT_DESCRIPTION"
	EntityPageDescription    = "PAGE_DESCRIPTION"
	EntityUserCover          = "USER_COVER"
	EntityUserAvatar         = "USER_AVATAR"
	EntityWorkspaceLogo      = "WORKSPACE_LOGO"
	EntityProjectCover       = "PROJECT_COVER"
	EntityDraftIssueAttach   = "DRAFT_ISSUE_ATTACHMENT"
	EntityDraftIssueDesc     = "DRAFT_ISSUE_DESCRIPTION"
)

// ValidEntityTypes is Plane's EntityTypeContext.values.
var ValidEntityTypes = []string{
	EntityIssueAttachment,
	EntityIssueDescription,
	EntityCommentDescription,
	EntityPageDescription,
	EntityUserCover,
	EntityUserAvatar,
	EntityWorkspaceLogo,
	EntityProjectCover,
	EntityDraftIssueAttach,
	EntityDraftIssueDesc,
}

// ValidEntityType reports whether value is one of Plane's entity types.
func ValidEntityType(value string) bool {
	for _, v := range ValidEntityTypes {
		if v == value {
			return true
		}
	}
	return false
}

// Asset renders a `file_assets` row in Plane's FileAssetSerializer shape
// (`fields = "__all__"` plus the computed `asset_url` property). Relation fields
// are projected back to their Plane UUIDs; `asset` is the stored filename (the
// PocketBase-internal shape the frontends never read — they use `asset_url`).
func Asset(app core.App, r *core.Record) map[string]any {
	return map[string]any{
		"id":                UUID(r),
		"created_at":        DateTime(r, "created_at"),
		"updated_at":        DateTime(r, "updated_at"),
		"created_by":        RelationUUID(app, r, "created_by"),
		"updated_by":        RelationUUID(app, r, "updated_by"),
		"attributes":        JSONValue(r, "attributes"),
		"asset":             Text(r, "asset"),
		"user":              RelationUUID(app, r, "user"),
		"workspace":         RelationUUID(app, r, "workspace"),
		"draft_issue":       RelationUUID(app, r, "draft_issue"),
		"project":           RelationUUID(app, r, "project"),
		"issue":             RelationUUID(app, r, "issue"),
		"comment":           RelationUUID(app, r, "comment"),
		"page":              RelationUUID(app, r, "page"),
		"entity_type":       NullableText(r, "entity_type"),
		"entity_identifier": NullableText(r, "entity_identifier"),
		"is_deleted":        r.GetBool("is_deleted"),
		"is_archived":       r.GetBool("is_archived"),
		"external_id":       NullableText(r, "external_id"),
		"external_source":   NullableText(r, "external_source"),
		"size":              r.GetFloat("size"),
		"is_uploaded":       r.GetBool("is_uploaded"),
		"storage_metadata":  JSONValue(r, "storage_metadata"),
		"asset_url":         AssetURL(app, r),
	}
}

// AssetURL reproduces Django's `FileAsset.asset_url` computed property verbatim:
// four distinct shapes keyed on `entity_type`. These are Plane URLs the
// frontends fetch directly; asset.go registers handlers for each shape that
// stream the stored bytes (see the upload/serving design note in
// internal/handlers/asset/asset.go).
func AssetURL(app core.App, r *core.Record) any {
	id := UUID(r)
	switch r.GetString("entity_type") {
	case EntityWorkspaceLogo, EntityUserAvatar, EntityUserCover, EntityProjectCover:
		return "/api/assets/v2/static/" + id + "/"
	case EntityIssueAttachment:
		return fmt.Sprintf(
			"/api/assets/v2/workspaces/%s/projects/%s/issues/%s/attachments/%s/",
			workspaceSlug(app, r), relationUUIDString(app, r, "project"),
			relationUUIDString(app, r, "issue"), id,
		)
	case EntityIssueDescription, EntityCommentDescription, EntityPageDescription, EntityDraftIssueDesc:
		return fmt.Sprintf(
			"/api/assets/v2/workspaces/%s/projects/%s/%s/",
			workspaceSlug(app, r), relationUUIDString(app, r, "project"), id,
		)
	}
	return nil
}

// AssetFieldName is the single-file PocketBase column holding the bytes.
const AssetFieldName = "asset"

// workspaceSlug resolves the `workspace` relation to its slug ("" when unset).
func workspaceSlug(app core.App, r *core.Record) string {
	ws, err := app.FindRecordById("workspaces", r.GetString("workspace"))
	if err != nil || ws == nil {
		return ""
	}
	return ws.GetString("slug")
}

// relationUUIDString is RelationUUID narrowed to a string ("" when unset), for
// the URL builders above.
func relationUUIDString(app core.App, r *core.Record, field string) string {
	if v, ok := RelationUUID(app, r, field).(string); ok {
		return v
	}
	return ""
}
