// Package serializers contains the primitives that let the PocketBase backend
// speak Plane's wire format: DRF-compatible error bodies, DRF datetime
// formatting, the UUID<->record-id mapping, and the cursor pagination envelope.
//
// Every handler must go through these helpers rather than emitting ad-hoc JSON,
// because Plane's frontends parse these exact shapes.
package serializers

import (
	"net/http"

	"github.com/pocketbase/pocketbase/core"
)

// ErrorBody is the JSON body returned for a failed request. DRF produces two
// shapes: field-keyed validation maps, and a plain {"detail": ...} object.
type ErrorBody map[string]any

// FieldErrors builds a field-keyed validation error, matching DRF's serializer
// error output, e.g. {"name": ["This field is required."]}.
func FieldErrors(fields map[string][]string) ErrorBody {
	body := ErrorBody{}
	for k, v := range fields {
		body[k] = v
	}
	return body
}

// FieldError is the single-field convenience form of FieldErrors.
func FieldError(field, message string) ErrorBody {
	return ErrorBody{field: []string{message}}
}

// Detail builds a {"detail": message} body. Plane's frontends read `.detail`
// for non-field errors, so this is the right shape for auth and 4xx responses.
func Detail(message string) ErrorBody {
	return ErrorBody{"detail": message}
}

// WriteError sends a DRF-shaped error response with the given HTTP status.
func WriteError(e *core.RequestEvent, status int, body ErrorBody) error {
	return e.JSON(status, body)
}

// BadRequest reports a 400 with a detail message.
func BadRequest(e *core.RequestEvent, message string) error {
	return WriteError(e, http.StatusBadRequest, Detail(message))
}

// Unauthorized reports a 401. Plane's client treats this as "session expired"
// and redirects to the login screen, so the 401 status matters more than the body.
func Unauthorized(e *core.RequestEvent, message string) error {
	return WriteError(e, http.StatusUnauthorized, Detail(message))
}

// Forbidden reports a 403.
func Forbidden(e *core.RequestEvent, message string) error {
	return WriteError(e, http.StatusForbidden, Detail(message))
}

// NotFound reports a 404. Plane deliberately returns 404 (not 403) when a user
// lacks access to a workspace or project, so that the existence of the resource
// is not leaked. Preserve that behaviour here.
func NotFound(e *core.RequestEvent, message string) error {
	if message == "" {
		message = "Not found."
	}
	return WriteError(e, http.StatusNotFound, Detail(message))
}

// Validation reports a 400 with a field-keyed body.
func Validation(e *core.RequestEvent, field, message string) error {
	return WriteError(e, http.StatusBadRequest, FieldError(field, message))
}
