package serializers

import (
	"github.com/pocketbase/pocketbase/core"
)

// Instance mirrors license/api/serializers/instance.py InstanceSerializer
// (fields = "__all__", so every model column except the read-only ones).
func Instance(r *core.Record) map[string]any {
	return map[string]any{
		"id":                            UUID(r),
		"instance_name":                 r.GetString("instance_name"),
		"whitelist_emails":              r.GetString("whitelist_emails"),
		"instance_id":                   r.GetString("instance_id"),
		"current_version":               r.GetString("current_version"),
		"latest_version":                nullableString(r.GetString("latest_version")),
		"edition":                       r.GetString("edition"),
		"domain":                        r.GetString("domain"),
		"last_checked_at":               NullableDateTime(r, "last_checked_at"),
		"namespace":                     nullableString(r.GetString("namespace")),
		"is_telemetry_enabled":          r.GetBool("is_telemetry_enabled"),
		"is_support_required":           r.GetBool("is_support_required"),
		"is_setup_done":                 r.GetBool("is_setup_done"),
		"is_signup_screen_visited":      r.GetBool("is_signup_screen_visited"),
		"is_verified":                   r.GetBool("is_verified"),
		"is_test":                       r.GetBool("is_test"),
		"is_current_version_deprecated": r.GetBool("is_current_version_deprecated"),
		"created_at":                    NullableDateTime(r, "created_at"),
		"updated_at":                    NullableDateTime(r, "updated_at"),
	}
}

// InstanceAdmin mirrors InstanceAdminSerializer: all columns plus `user_detail`
// (UserAdminLiteSerializer over the `user` relation).
func InstanceAdmin(app core.App, r *core.Record) map[string]any {
	out := map[string]any{
		"id":          UUID(r),
		"instance":    RelationUUID(app, r, "instance"),
		"user":        RelationUUID(app, r, "user"),
		"role":        r.GetInt("role"),
		"is_verified": r.GetBool("is_verified"),
		"created_at":  NullableDateTime(r, "created_at"),
		"updated_at":  NullableDateTime(r, "updated_at"),
	}
	if u, _ := FindByUUIDOrID(app, "users", r.GetString("user")); u != nil {
		out["user_detail"] = map[string]any{
			"id":         UUID(u),
			"email":      u.GetString("email"),
			"first_name": u.GetString("first_name"),
			"last_name":  u.GetString("last_name"),
		}
	} else {
		out["user_detail"] = nil
	}
	return out
}

// InstanceAdminMe mirrors InstanceAdminMeSerializer over the requesting User.
func InstanceAdminMe(r *core.Record) map[string]any {
	return map[string]any{
		"id":                  UUID(r),
		"avatar":              r.GetString("avatar"),
		"avatar_url":          nil,
		"cover_image":         r.GetString("cover_image"),
		"date_joined":         NullableDateTime(r, "date_joined"),
		"display_name":        r.GetString("display_name"),
		"email":               r.GetString("email"),
		"first_name":          r.GetString("first_name"),
		"last_name":           r.GetString("last_name"),
		"is_active":           r.GetBool("is_active"),
		"is_bot":              r.GetBool("is_bot"),
		"is_email_verified":   r.GetBool("is_email_verified"),
		"user_timezone":       r.GetString("user_timezone"),
		"username":            r.GetString("username"),
		"is_password_autoset": r.GetBool("is_password_autoset"),
	}
}

// Configuration mirrors InstanceConfigurationSerializer.
func Configuration(r *core.Record) map[string]any {
	return map[string]any{
		"id":           UUID(r),
		"key":          r.GetString("key"),
		"value":        r.GetString("value"),
		"category":     r.GetString("category"),
		"is_encrypted": r.GetBool("is_encrypted"),
	}
}

// DefaultViewProps mirrors the inline fallback in Django's
// `ProjectDeployBoardViewSet.create` (apps/api/plane/app/views/project/base.py:553):
//
//	views = request.data.get("views", {"list": True, "kanban": True,
//	                                  "calendar": True, "gantt": True,
//	                                  "spreadsheet": True})
//
// It is used twice: as the value persisted when a project is published, and as
// the read-side fallback so a board row whose `view_props` was never written
// still advertises a layout. The Space app derives its whole layout state from
// this key — `apps/space/app/issues/[anchor]/layout.tsx` only calls
// `updateLayoutOptions` `if (response.view_props)`, and
// `components/issues/navbar/controls.tsx` builds `viewsAcceptable` solely from
// it. With `view_props` missing/empty, `viewsAcceptable` stays empty, no board
// is selected, `initIssueFilters` never runs, and the published issue list
// renders as an empty shell.
var DefaultViewProps = map[string]any{
	"list":        true,
	"kanban":      true,
	"calendar":    true,
	"gantt":       true,
	"spreadsheet": true,
}

// DeployBoard mirrors DeployBoardSerializer (app/serializers/project.py:247):
// all columns plus nested project_details / workspace_detail.
func DeployBoard(app core.App, r *core.Record) map[string]any {
	out := map[string]any{
		"id":                   UUID(r),
		"anchor":               r.GetString("anchor"),
		"entity_name":          r.GetString("entity_name"),
		"is_comments_enabled":  r.GetBool("is_comments_enabled"),
		"is_reactions_enabled": r.GetBool("is_reactions_enabled"),
		"is_votes_enabled":     r.GetBool("is_votes_enabled"),
		"view_props":           JSONOrDefault(r, "view_props", DefaultViewProps),
		"is_activity_enabled":  r.GetBool("is_activity_enabled"),
		"is_disabled":          r.GetBool("is_disabled"),
		"intake":               RelationUUID(app, r, "intake"),
		"created_at":           NullableDateTime(r, "created_at"),
		"updated_at":           NullableDateTime(r, "updated_at"),
	}
	if p, _ := FindByUUIDOrID(app, "projects", r.GetString("project")); p != nil {
		out["project_details"] = ProjectLite(p)
	} else {
		out["project_details"] = nil
	}
	if w, _ := FindByUUIDOrID(app, "workspaces", r.GetString("workspace")); w != nil {
		out["workspace_detail"] = WorkspaceLite(w)
	} else {
		out["workspace_detail"] = nil
	}
	return out
}

// ProjectLite mirrors space/serializer/project.py ProjectLiteSerializer.
//
// `logo_props` is a deliberate addition on top of Django's field list. Django's
// space serializer only emits `icon_prop`, but the Space frontend types
// `TProjectDetails = IProjectLite & Pick<IProject, "cover_image" | "logo_props" |
// "description">`, and `components/common/project-logo.tsx` dereferences
// `logo.in_use` unconditionally. With only `icon_prop` present, `logo` is
// undefined and the whole Space app dies on the error boundary
// ("Cannot read properties of undefined (reading 'in_use')") — i.e. Plane v1.4.2
// cannot render its own published pages. We emit both keys; `icon_prop` stays
// for Django parity, `logo_props` is what the frontend actually reads.
func ProjectLite(r *core.Record) map[string]any {
	return map[string]any{
		"id":          UUID(r),
		"identifier":  r.GetString("identifier"),
		"name":        r.GetString("name"),
		"cover_image": nil,
		"icon_prop":   jsonOrNull(r, "icon_prop"),
		"logo_props":  JSONOrDefault(r, "logo_props", map[string]any{"in_use": "icon"}),
		"emoji":       nullableString(r.GetString("emoji")),
		"description": nullableString(r.GetString("description")),
	}
}

// WorkspaceLite mirrors WorkspaceLiteSerializer (id, slug, name, logo...).
func WorkspaceLite(r *core.Record) map[string]any {
	return map[string]any{
		"id":       UUID(r),
		"slug":     r.GetString("slug"),
		"name":     r.GetString("name"),
		"logo_url": nil,
	}
}

func jsonOrNull(r *core.Record, field string) any {
	raw := r.Get(field)
	if raw == nil {
		return nil
	}
	return raw
}
