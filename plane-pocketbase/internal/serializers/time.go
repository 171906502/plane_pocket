package serializers

import (
	"time"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/types"
)

// DRFTimeFormat is DRF's default DATETIME_FORMAT ("%Y-%m-%dT%H:%M:%S.%fZ"):
// microsecond precision, always UTC, always a trailing "Z". Plane's frontends
// feed these straight into `new Date(...)`, so the shape must not drift.
const DRFTimeFormat = "2006-01-02T15:04:05.000000Z"

// DRFDateOnlyFormat is DRF's default DATE_FORMAT ("%Y-%m-%d").
const DRFDateOnlyFormat = "2006-01-02"

// Time renders a Go time in Plane's wire format.
func Time(t time.Time) string {
	return t.UTC().Format(DRFTimeFormat)
}

// NullableTime renders a time pointer, or nil when unset. Plane models use
// nullable timestamps (e.g. `archived_at`, `deleted_at`) and the frontends
// distinguish null from a zero date, so do not substitute a zero value.
func NullableTime(t *time.Time) any {
	if t == nil || t.IsZero() {
		return nil
	}
	return Time(*t)
}

// DateTime reads a PocketBase `date` field and renders it in Plane's wire
// format. PocketBase stores these as strings, so an unparseable value yields
// nil rather than a zero date.
func DateTime(r *core.Record, field string) any {
	return fromTypesDateTime(r.GetDateTime(field))
}

// NullableDateTime is DateTime for fields that may be empty.
func NullableDateTime(r *core.Record, field string) any {
	dt := r.GetDateTime(field)
	if dt.IsZero() {
		return nil
	}
	return fromTypesDateTime(dt)
}

func fromTypesDateTime(dt types.DateTime) any {
	t := dt.Time()
	if t.IsZero() {
		return nil
	}
	return Time(t)
}
