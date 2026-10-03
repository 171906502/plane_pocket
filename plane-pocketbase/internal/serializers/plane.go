package serializers

import (
	"crypto/rand"
	"encoding/hex"
	"time"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/types"
)

// NewUUID returns a random RFC 4122 version-4 UUID. Every collection created
// from spec/schema-spec.md carries a required, unique `uuid` text column that
// holds Plane's UUID, so every insert through the compatibility layer must
// generate one.
func NewUUID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return ""
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10
	h := hex.EncodeToString(b[:])
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}

// Now returns the current time in UTC, used for Plane's manually-managed
// timestamp columns (`last_active`, `last_login_time`, `completed_at`).
func Now() time.Time {
	return time.Now().UTC()
}

// Text renders a required/non-null text column. PocketBase stores "" for an
// absent value; Plane's non-nullable CharFields/TextFields serialize that as "".
func Text(r *core.Record, field string) string {
	return r.GetString(field)
}

// NullableText renders a nullable text column. PocketBase has no NULL for text
// (spec §7.1), so "" is Plane's NULL and must be projected back to null — the
// frontends distinguish null from "" for fields such as `emoji` or `logo`.
func NullableText(r *core.Record, field string) any {
	if v := r.GetString(field); v != "" {
		return v
	}
	return nil
}

// TextOr renders a non-null text column that Django declares with a `default=`.
// Such a column is never null in Postgres, so an empty value here means the row
// was created outside the compatibility layer or predates the default — emit the
// default instead of null.
//
// This matters: Plane's frontends feed these values straight into platform APIs
// (e.g. `Intl.DateTimeFormat({ timeZone: user_timezone })` in
// apps/web/components/home/user-greetings.tsx), which throw on null.
func TextOr(r *core.Record, field, fallback string) string {
	if v := r.GetString(field); v != "" {
		return v
	}
	return fallback
}

// DateOnly renders a PocketBase date column as DRF's DATE_FORMAT ("%Y-%m-%d").
// Plane declares `start_date`, `target_date`, `completed_at` and
// `Issue.archived_at` as DateField (not DateTimeField), so they carry no time
// component on the wire.
func DateOnly(r *core.Record, field string) any {
	dt := r.GetDateTime(field)
	if dt.IsZero() {
		return nil
	}
	return dt.Time().UTC().Format(DRFDateOnlyFormat)
}

// Number renders an integer column, defaulting to 0 for an unset value.
func Number(r *core.Record, field string) int {
	return r.GetInt(field)
}

// NullableNumber renders an integer column, or nil when unset.
func NullableNumber(r *core.Record, field string) any {
	if r.Get(field) == nil {
		return nil
	}
	return r.GetInt(field)
}

// JSONValue returns a raw JSON column value (nil when unset).
func JSONValue(r *core.Record, field string) any {
	return r.Get(field)
}

// JSONOrDefault returns a JSON column's value, or `def` when the column is
// unset.
//
// A plain `r.Get(field) == nil` check is NOT enough: PocketBase hands back a
// non-nil `types.JSONRaw` wrapping a nil slice for an unset JSON column, so the
// interface never compares equal to nil and the field marshals to `null`
// instead of the Django `default=`. Plane's frontends read several of these
// values as objects (e.g. `ProjectUserProperty.filters` feeds
// `display_filters` straight into the issue layout), so a `null` where Django
// would send `{...}` breaks rendering.
func JSONOrDefault(r *core.Record, field string, def any) any {
	raw := r.Get(field)
	switch v := raw.(type) {
	case nil:
		return def
	case types.JSONRaw:
		if len(v) == 0 || string(v) == "null" {
			return def
		}
		return v
	case []byte:
		if len(v) == 0 || string(v) == "null" {
			return def
		}
		return v
	}
	return raw
}
