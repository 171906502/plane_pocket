package serializers

import (
	"github.com/pocketbase/pocketbase/core"
)

// UserLite mirrors Django's UserLiteSerializer (plane/app/serializers/user.py).
// It is the nested shape embedded in members, assignees and other relations.
func UserLite(app core.App, r *core.Record) map[string]any {
	if r == nil {
		return nil
	}
	avatar := NullableText(r, "avatar")
	return map[string]any{
		"id":           UUID(r),
		"first_name":   Text(r, "first_name"),
		"last_name":    Text(r, "last_name"),
		"avatar":       avatar,
		"avatar_url":   avatar,
		"is_bot":       r.GetBool("is_bot"),
		"display_name": Text(r, "display_name"),
	}
}

// UserAdminLite mirrors Django's UserAdminLiteSerializer: UserLite plus the
// email and login medium, shown to workspace admins.
func UserAdminLite(app core.App, r *core.Record) map[string]any {
	if r == nil {
		return nil
	}
	out := UserLite(app, r)
	out["email"] = NullableText(r, "email")
	out["last_login_medium"] = TextOr(r, "last_login_medium", "email")
	return out
}

// UserMe mirrors Django's UserMeSerializer, the body of GET /api/users/me/ and
// the `user` object of GET /api/users/session/.
func UserMe(app core.App, r *core.Record) map[string]any {
	if r == nil {
		return nil
	}
	avatar := NullableText(r, "avatar")
	cover := NullableText(r, "cover_image")
	return map[string]any{
		"id":                         UUID(r),
		"avatar":                     avatar,
		"cover_image":                cover,
		"avatar_url":                 avatar,
		"cover_image_url":            cover,
		"date_joined":                DateTime(r, "date_joined"),
		"display_name":               Text(r, "display_name"),
		"email":                      NullableText(r, "email"),
		"first_name":                 Text(r, "first_name"),
		"last_name":                  Text(r, "last_name"),
		"is_active":                  r.GetBool("is_active"),
		"is_bot":                     r.GetBool("is_bot"),
		"is_email_verified":          r.GetBool("is_email_verified"),
		"user_timezone":              TextOr(r, "user_timezone", "UTC"),
		"username":                   Text(r, "username"),
		"is_password_autoset":        r.GetBool("is_password_autoset"),
		"last_login_medium":          TextOr(r, "last_login_medium", "email"),
		"last_login_time":            NullableDateTime(r, "last_login_time"),
		"is_password_reset_required": r.GetBool("is_password_reset_required"),
	}
}

// UserFull mirrors Django's UserSerializer, which serializes every User model
// field except `password`. It is the body of PATCH /api/users/me/ and of
// POST /auth/set-password/. PocketBase's own auth columns (password, tokenKey,
// emailVisibility, verified) are internal and never projected.
func UserFull(app core.App, r *core.Record) map[string]any {
	if r == nil {
		return nil
	}
	avatar := NullableText(r, "avatar")
	cover := NullableText(r, "cover_image")
	return map[string]any{
		"id":                         UUID(r),
		"username":                   Text(r, "username"),
		"mobile_number":              NullableText(r, "mobile_number"),
		"email":                      NullableText(r, "email"),
		"display_name":               Text(r, "display_name"),
		"first_name":                 Text(r, "first_name"),
		"last_name":                  Text(r, "last_name"),
		"avatar":                     avatar,
		"avatar_url":                 avatar,
		"cover_image":                cover,
		"cover_image_url":            cover,
		"date_joined":                DateTime(r, "date_joined"),
		"created_at":                 DateTime(r, "created_at"),
		"updated_at":                 DateTime(r, "updated_at"),
		"last_location":              NullableText(r, "last_location"),
		"created_location":           NullableText(r, "created_location"),
		"is_superuser":               r.GetBool("is_superuser"),
		"is_managed":                 r.GetBool("is_managed"),
		"is_password_expired":        r.GetBool("is_password_expired"),
		"is_active":                  r.GetBool("is_active"),
		"is_staff":                   r.GetBool("is_staff"),
		"is_email_verified":          r.GetBool("is_email_verified"),
		"is_password_autoset":        r.GetBool("is_password_autoset"),
		"is_password_reset_required": r.GetBool("is_password_reset_required"),
		"token":                      NullableText(r, "token"),
		"last_active":                NullableDateTime(r, "last_active"),
		"last_login_time":            NullableDateTime(r, "last_login_time"),
		"last_logout_time":           NullableDateTime(r, "last_logout_time"),
		"last_login_ip":              NullableText(r, "last_login_ip"),
		"last_logout_ip":             NullableText(r, "last_logout_ip"),
		"last_login_medium":          TextOr(r, "last_login_medium", "email"),
		"last_login_uagent":          NullableText(r, "last_login_uagent"),
		"token_updated_at":           NullableDateTime(r, "token_updated_at"),
		"is_bot":                     r.GetBool("is_bot"),
		"bot_type":                   NullableText(r, "bot_type"),
		"user_timezone":              TextOr(r, "user_timezone", "UTC"),
		"is_email_valid":             r.GetBool("is_email_valid"),
		"masked_at":                  NullableDateTime(r, "masked_at"),
	}
}
