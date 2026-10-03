package router

import (
	"github.com/pocketbase/pocketbase/core"

	"github.com/plane-pocketbase/plane-pocketbase/internal/handlers/notification"
)

// RegisterNotification wires the notification domain
// (`/api/workspaces/{slug}/users/notifications/…`) plus the user-scoped
// notification preferences endpoint.
// Contract: spec/api-spec.yaml, group `notification`; Django views in
// apps/api/plane/app/views/notification/base.py.
//
// `users/notifications/unread/` and `users/notifications/mark-all-read/` are
// literal segments, so the stdlib mux prefers them over the `{pk}` wildcard.
func RegisterNotification(e *core.ServeEvent, app core.App) error {
	h := notification.New(app)

	ws := workspaceGroup(e, app)

	ws.GET("/users/notifications/{$}", h.List)
	ws.GET("/users/notifications/unread/{$}", h.UnreadCount)
	// `{$}` anchors these literal routes to an exact match: without it the
	// `mark-all-read/` subtree overlaps `{pk}/read/` (both would match
	// `/users/notifications/mark-all-read/read/`) and the mux panics.
	ws.POST("/users/notifications/mark-all-read/{$}", h.MarkAllRead)
	ws.GET("/users/notifications/{pk}/{$}", h.Detail)
	ws.PATCH("/users/notifications/{pk}/{$}", h.Update)
	ws.DELETE("/users/notifications/{pk}/{$}", h.Delete)
	ws.POST("/users/notifications/{pk}/read/{$}", h.MarkRead)
	ws.DELETE("/users/notifications/{pk}/read/{$}", h.MarkUnread)
	ws.POST("/users/notifications/{pk}/archive/{$}", h.Archive)
	ws.DELETE("/users/notifications/{pk}/archive/{$}", h.Unarchive)

	// User-scoped: not under a workspace slug, so registered on the root router.
	e.Router.GET("/api/users/me/notification-preferences/{$}", h.PrefsGet)
	e.Router.PATCH("/api/users/me/notification-preferences/{$}", h.PrefsPatch)

	return nil
}
