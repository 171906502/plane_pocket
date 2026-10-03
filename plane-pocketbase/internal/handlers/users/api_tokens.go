package users

import (
	"net/http"
	"strings"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"

	"github.com/plane-pocketbase/plane-pocketbase/internal/middleware"
	"github.com/plane-pocketbase/plane-pocketbase/internal/serializers"
)

// tokenHex mirrors plane/db/models/api.py generate_key: "plane_api_" + uuid4 hex.
func tokenHex() string {
	return "plane_api_" + strings.ReplaceAll(serializers.NewUUID(), "-", "")
}

// tokenOut is the full APITokenSerializer — includes `token`, which Django
// only exposes at creation time.
func tokenOut(r *core.Record, withToken bool) map[string]any {
	out := map[string]any{
		"id":          serializers.UUID(r),
		"label":       r.GetString("label"),
		"description": r.GetString("description"),
		"is_active":   r.GetBool("is_active"),
		"expired_at":  serializers.NullableDateTime(r, "expired_at"),
		"last_used":   serializers.NullableDateTime(r, "last_used"),
		"created_at":  serializers.NullableDateTime(r, "created_at"),
	}
	if withToken {
		out["token"] = r.GetString("token")
	}
	return out
}

// ApiTokens serves GET/POST /api/users/api-tokens/ (ApiTokenEndpoint).
// POST returns the plaintext token exactly once, as in Django.
func (h *Handler) ApiTokens(e *core.RequestEvent) error {
	user := middleware.CurrentUser(e)
	if user == nil {
		return serializers.Unauthorized(e, "Authentication credentials were not provided.")
	}
	switch e.Request.Method {
	case http.MethodGet:
		recs, err := h.App.FindRecordsByFilter("api_tokens",
			"user = {:u} && is_service = false", "-created_at", 0, 0,
			dbx.Params{"u": user.Id})
		if err != nil {
			return err
		}
		out := make([]map[string]any, 0, len(recs))
		for _, r := range recs {
			out = append(out, tokenOut(r, false))
		}
		return e.JSON(http.StatusOK, out)
	case http.MethodPost:
		body := map[string]any{}
		if err := e.BindBody(&body); err != nil {
			return serializers.BadRequest(e, "Invalid request body.")
		}
		label, _ := body["label"].(string)
		if strings.TrimSpace(label) == "" {
			label = serializers.NewUUID()
		}
		coll, err := h.App.FindCollectionByNameOrId("api_tokens")
		if err != nil {
			return err
		}
		rec := core.NewRecord(coll)
		rec.Set("uuid", serializers.NewUUID())
		rec.Set("user", user.Id)
		rec.Set("label", label)
		rec.Set("description", asStringUser(body["description"]))
		rec.Set("token", tokenHex())
		rec.Set("is_active", true)
		rec.Set("user_type", 0)
		if err := h.App.Save(rec); err != nil {
			return serializers.Validation(e, "detail", err.Error())
		}
		return e.JSON(http.StatusCreated, tokenOut(rec, true))
	}
	return serializers.BadRequest(e, "Unsupported method")
}

// ApiTokenDetail serves GET/PATCH/DELETE /api/users/api-tokens/{pk}/.
func (h *Handler) ApiTokenDetail(e *core.RequestEvent) error {
	user := middleware.CurrentUser(e)
	if user == nil {
		return serializers.Unauthorized(e, "Authentication credentials were not provided.")
	}
	rec, _ := serializers.FindByUUIDOrID(h.App, "api_tokens", e.Request.PathValue("pk"))
	if rec == nil || rec.GetString("user") != user.Id {
		return serializers.NotFound(e, "")
	}
	switch e.Request.Method {
	case http.MethodGet:
		return e.JSON(http.StatusOK, tokenOut(rec, false))
	case http.MethodPatch:
		body := map[string]any{}
		if err := e.BindBody(&body); err != nil {
			return serializers.BadRequest(e, "Invalid request body.")
		}
		if v, ok := body["label"].(string); ok {
			rec.Set("label", v)
		}
		if v, ok := body["description"].(string); ok {
			rec.Set("description", v)
		}
		if v, ok := body["is_active"].(bool); ok {
			rec.Set("is_active", v)
		}
		if err := h.App.Save(rec); err != nil {
			return serializers.Validation(e, "detail", err.Error())
		}
		return e.JSON(http.StatusOK, tokenOut(rec, false))
	case http.MethodDelete:
		if err := h.App.Delete(rec); err != nil {
			return serializers.Validation(e, "detail", err.Error())
		}
		return e.NoContent(http.StatusNoContent)
	}
	return serializers.BadRequest(e, "Unsupported method")
}

func asStringUser(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}
