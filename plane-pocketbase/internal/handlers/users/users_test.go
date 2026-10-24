package users

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"

	"github.com/plane-pocketbase/plane-pocketbase/internal/serializers"

	_ "github.com/plane-pocketbase/plane-pocketbase/pb_migrations"
)

// TestDeleteMe proves DELETE /api/users/me/ deactivates the caller: 204 with
// the persisted row flipped to is_active=false (Django's account deactivation,
// not a hard delete).
func TestDeleteMe(t *testing.T) {
	app, err := tests.NewTestApp(t.TempDir())
	if err != nil {
		t.Fatalf("new test app: %v", err)
	}
	t.Cleanup(app.Cleanup)

	coll, err := app.FindCollectionByNameOrId("users")
	if err != nil {
		t.Fatalf("users collection: %v", err)
	}
	user := core.NewRecord(coll)
	user.Set("uuid", serializers.NewUUID())
	user.Set("username", "me")
	user.Set("email", "me@example.com")
	user.Set("password", "1234567890")
	user.Set("display_name", "Me")
	user.Set("last_active", "2024-01-01 00:00:00.000Z")
	user.Set("is_active", true)
	if err := app.Save(user); err != nil {
		t.Fatalf("seed user: %v", err)
	}

	h := New(app)
	req := httptest.NewRequest(http.MethodDelete, "/", nil)
	rec := httptest.NewRecorder()
	e := &core.RequestEvent{App: app}
	e.Request = req
	e.Response = rec
	e.Auth = user

	if err := h.DeleteMe(e); err != nil {
		t.Fatalf("delete me: %v", err)
	}
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete me status %d", rec.Code)
	}

	fresh, _ := app.FindRecordById("users", user.Id)
	if fresh == nil {
		t.Fatalf("user row must survive deactivation")
	}
	if fresh.GetBool("is_active") {
		t.Fatalf("user must be deactivated, is_active is still true")
	}
}
