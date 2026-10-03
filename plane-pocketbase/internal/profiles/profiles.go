// Package profiles owns the Django `Profile` row that Plane keeps one-to-one
// with every user. Onboarding state (onboarding_step, is_onboarded,
// is_tour_completed, last_workspace_id, theme) lives here — not on the user —
// so /api/users/me/profile/ must read and write this collection.
package profiles

import (
	"math/rand"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"

	"github.com/plane-pocketbase/plane-pocketbase/internal/serializers"
)

// Ensure returns the user's Profile, creating it with Django's model defaults
// on first access.
//
// Django creates the row from a post_save signal on User; the compatibility
// layer does it lazily instead, so that users created by any path (web sign-up,
// the admin console, or seeding) always end up with a profile.
func Ensure(app core.App, user *core.Record) (*core.Record, error) {
	if user == nil {
		return nil, nil
	}
	if existing, err := findByUser(app, user.Id); err == nil && existing != nil {
		return existing, nil
	}

	collection, err := app.FindCollectionByNameOrId("profiles")
	if err != nil {
		return nil, err
	}

	profile := core.NewRecord(collection)
	profile.Set("uuid", serializers.NewUUID())
	profile.Set("user", user.Id)
	profile.Set("theme", map[string]any{})
	profile.Set("is_app_rail_docked", true)
	profile.Set("is_tour_completed", false)
	profile.Set("onboarding_step", serializers.DefaultOnboardingSteps())
	profile.Set("is_onboarded", false)
	profile.Set("billing_address_country", "INDIA")
	profile.Set("notification_view_mode", "full")
	profile.Set("language", "en")
	profile.Set("start_of_the_week", 0)
	profile.Set("goals", map[string]any{})
	profile.Set("background_color", randomColor())
	profile.Set("product_tour", serializers.DefaultProductTour())
	profile.Set("mobile_onboarding_step", serializers.DefaultMobileOnboardingSteps())

	if err := app.Save(profile); err != nil {
		// Two concurrent first-requests can race past the lookup; the unique
		// index on `user` rejects the loser, which should then read the winner.
		if again, ferr := findByUser(app, user.Id); ferr == nil && again != nil {
			return again, nil
		}
		return nil, err
	}
	return profile, nil
}

func findByUser(app core.App, userID string) (*core.Record, error) {
	record, err := app.FindFirstRecordByFilter("profiles", "user = {:user}", dbx.Params{"user": userID})
	if err != nil {
		// PocketBase reports a miss as sql.ErrNoRows; treat that as "absent".
		return nil, nil
	}
	return record, nil
}

// randomColor mirrors Django's `get_random_color` used as the Profile
// background_color default.
func randomColor() string {
	const palette = "0123456789abcdef"
	out := make([]byte, 0, 7)
	out = append(out, '#')
	for range 6 {
		out = append(out, palette[rand.Intn(len(palette))])
	}
	return string(out)
}
