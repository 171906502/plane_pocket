package serializers

import (
	"github.com/pocketbase/pocketbase/core"
)

// DefaultOnboardingSteps mirrors `get_default_onboarding()` in
// apps/api/plane/db/models/user.py.
func DefaultOnboardingSteps() map[string]any {
	return map[string]any{
		"profile_complete": false,
		"workspace_create": false,
		"workspace_invite": false,
		"workspace_join":   false,
	}
}

// DefaultMobileOnboardingSteps mirrors `get_mobile_default_onboarding()`.
func DefaultMobileOnboardingSteps() map[string]any {
	return map[string]any{
		"profile_complete": false,
		"workspace_create": false,
		"workspace_join":   false,
	}
}

// DefaultProductTour mirrors `get_default_product_tour()`.
func DefaultProductTour() map[string]any {
	return map[string]any{
		"work_items": false,
		"cycles":     false,
		"modules":    false,
		"intake":     false,
		"pages":      false,
	}
}

// jsonOr returns the record's JSON field value, or fallback when the field is
// unset. PocketBase stores an unset JSON field as nil, whereas Django's model
// defaults are non-null dicts, so the serializer must supply them.
func jsonOr(r *core.Record, field string, fallback map[string]any) any {
	return JSONOrDefault(r, field, fallback)
}

// Profile mirrors Django's ProfileSerializer, which is declared with
// `fields = "__all__"` and `read_only_fields = ["user"]`, so the body is the
// whole Profile row with the FK exposed as the user's UUID.
//
// `id` is the Profile's own UUID — not the user's. Plane's web app only uses it
// as an identity sentinel for theme syncing (lib/wrappers/store-wrapper.tsx),
// so keeping Django's exact shape is both correct and safe.
func Profile(app core.App, r *core.Record) map[string]any {
	if r == nil {
		return nil
	}
	return map[string]any{
		"id":                           UUID(r),
		"user":                         RelationUUID(app, r, "user"),
		"theme":                        jsonOr(r, "theme", map[string]any{}),
		"is_app_rail_docked":           r.GetBool("is_app_rail_docked"),
		"is_tour_completed":            r.GetBool("is_tour_completed"),
		"onboarding_step":              jsonOr(r, "onboarding_step", DefaultOnboardingSteps()),
		"use_case":                     NullableText(r, "use_case"),
		"role":                         NullableText(r, "role"),
		"is_onboarded":                 r.GetBool("is_onboarded"),
		"last_workspace_id":            nullableUUIDText(r, "last_workspace_id"),
		"billing_address_country":      Text(r, "billing_address_country"),
		"billing_address":              r.Get("billing_address"),
		"has_billing_address":          r.GetBool("has_billing_address"),
		"company_name":                 Text(r, "company_name"),
		"notification_view_mode":       Text(r, "notification_view_mode"),
		"is_smooth_cursor_enabled":     r.GetBool("is_smooth_cursor_enabled"),
		"is_mobile_onboarded":          r.GetBool("is_mobile_onboarded"),
		"mobile_onboarding_step":       jsonOr(r, "mobile_onboarding_step", DefaultMobileOnboardingSteps()),
		"mobile_timezone_auto_set":     r.GetBool("mobile_timezone_auto_set"),
		"language":                     Text(r, "language"),
		"start_of_the_week":            r.GetInt("start_of_the_week"),
		"goals":                        jsonOr(r, "goals", map[string]any{}),
		"background_color":             Text(r, "background_color"),
		"is_navigation_tour_completed": r.GetBool("is_navigation_tour_completed"),
		"has_marketing_email_consent":  r.GetBool("has_marketing_email_consent"),
		"is_subscribed_to_changelog":   r.GetBool("is_subscribed_to_changelog"),
		"product_tour":                 jsonOr(r, "product_tour", DefaultProductTour()),
		"created_at":                   DateTime(r, "created_at"),
		"updated_at":                   DateTime(r, "updated_at"),
	}
}

// nullableUUIDText renders a nullable UUID text column the way DRF renders a
// nullable UUIDField: the string, or null when unset.
func nullableUUIDText(r *core.Record, field string) any {
	if v := r.GetString(field); v != "" {
		return v
	}
	return nil
}
