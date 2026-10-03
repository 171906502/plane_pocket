package serializers

import (
	"github.com/pocketbase/pocketbase/core"
)

// Comment mirrors IssueCommentSerializer: the wire shape of a comment as the
// app and space frontends consume it.
func Comment(app core.App, r *core.Record) map[string]any {
	actor := map[string]any{
		"id":           nil,
		"first_name":   "",
		"last_name":    "",
		"avatar":       "",
		"avatar_url":   nil,
		"display_name": "",
	}
	if u, _ := FindByUUIDOrID(app, "users", r.GetString("actor")); u != nil {
		actor["id"] = UUID(u)
		actor["first_name"] = u.GetString("first_name")
		actor["last_name"] = u.GetString("last_name")
		actor["avatar"] = u.GetString("avatar")
		actor["display_name"] = u.GetString("display_name")
	}
	return map[string]any{
		"id":               UUID(r),
		"comment_html":     r.GetString("comment_html"),
		"comment_stripped": r.GetString("comment_stripped"),
		"comment_json":     jsonOrNull(r, "comment_json"),
		"attachments":      jsonOrNull(r, "attachments"),
		"access":           r.GetString("access"),
		"edited_at":        NullableDateTime(r, "edited_at"),
		"actor":            RelationUUID(app, r, "actor"),
		"actor_detail":     actor,
		"issue":            RelationUUID(app, r, "issue"),
		"project":          RelationUUID(app, r, "project"),
		"workspace":        RelationUUID(app, r, "workspace"),
		"created_at":       NullableDateTime(r, "created_at"),
		"updated_at":       NullableDateTime(r, "updated_at"),
	}
}

// Reaction mirrors IssueReactionSerializer / CommentReactionSerializer.
func Reaction(app core.App, r *core.Record) map[string]any {
	return map[string]any{
		"id":         UUID(r),
		"reaction":   r.GetString("reaction"),
		"actor":      RelationUUID(app, r, "actor"),
		"issue":      RelationUUID(app, r, "issue"),
		"comment":    RelationUUID(app, r, "comment"),
		"project":    RelationUUID(app, r, "project"),
		"workspace":  RelationUUID(app, r, "workspace"),
		"created_at": NullableDateTime(r, "created_at"),
	}
}

// Vote mirrors IssueVoteSerializer.
func Vote(app core.App, r *core.Record) map[string]any {
	actor := map[string]any{
		"id":         nil,
		"first_name": "",
		"last_name":  "",
		"avatar":     "",
		"avatar_url": nil,
	}
	if u, _ := FindByUUIDOrID(app, "users", r.GetString("actor")); u != nil {
		actor["id"] = UUID(u)
		actor["first_name"] = u.GetString("first_name")
		actor["last_name"] = u.GetString("last_name")
		actor["avatar"] = u.GetString("avatar")
	}
	return map[string]any{
		"id":            UUID(r),
		"vote":          r.GetInt("vote"),
		"actor":         RelationUUID(app, r, "actor"),
		"actor_details": actor,
		"issue":         RelationUUID(app, r, "issue"),
		"project":       RelationUUID(app, r, "project"),
		"workspace":     RelationUUID(app, r, "workspace"),
		"created_at":    NullableDateTime(r, "created_at"),
	}
}
