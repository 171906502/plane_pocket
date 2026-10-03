// Package meta implements Plane's small non-domain endpoints that the
// frontends call for static reference data.
package meta

import (
	"fmt"
	"net/http"
	"sort"
	"time"

	// Windows ships no IANA timezone database, so `time.LoadLocation` fails for
	// every id on a stock install. Importing this package embeds the database
	// in the binary.
	_ "time/tzdata"

	"github.com/pocketbase/pocketbase/core"
)

// Handler holds the app dependency.
type Handler struct {
	App core.App
}

// New returns a meta handler bound to app.
func New(app core.App) *Handler {
	return &Handler{App: app}
}

// Timezones serves GET /api/timezones/ (TimezoneEndpoint).
//
// Django's implementation walks the same friendly-name -> IANA table and
// reports the *current* offset (so DST is reflected), sorted by offset and then
// by label.
//
// One deliberate deviation: Django derives the hour component with Python's
// floor division (`total_seconds // 3600`), which mislabels half-hour zones
// west of UTC — Newfoundland (UTC-03:30) is reported as `UTC-04:30`. We use
// truncation toward zero, which yields the correct `UTC-03:30`. Nothing in the
// frontend depends on the buggy value; it only renders the label.
func (h *Handler) Timezones(e *core.RequestEvent) error {
	type entry struct {
		offset int
		label  string
		item   map[string]any
	}

	now := time.Now()
	entries := make([]entry, 0, len(timezoneLocations))
	for _, pair := range timezoneLocations {
		label, identifier := pair[0], pair[1]
		location, err := time.LoadLocation(identifier)
		if err != nil {
			continue
		}
		_, seconds := now.In(location).Zone()

		hours := seconds / 3600
		minutes := abs(seconds%3600) / 60
		sign := "+"
		if hours < 0 || seconds < 0 {
			sign = "-"
		}
		offset := fmt.Sprintf("%s%02d:%02d", sign, abs(hours), minutes)

		entries = append(entries, entry{
			offset: seconds,
			label:  label,
			item: map[string]any{
				"utc_offset": "UTC" + offset,
				"gmt_offset": "GMT" + offset,
				"value":      identifier,
				"label":      label,
			},
		})
	}

	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].offset != entries[j].offset {
			return entries[i].offset < entries[j].offset
		}
		return entries[i].label < entries[j].label
	})

	timezones := make([]any, 0, len(entries))
	for _, item := range entries {
		timezones = append(timezones, item.item)
	}
	return e.JSON(http.StatusOK, map[string]any{"timezones": timezones})
}

// Unsplash serves GET /api/unsplash/ (UnsplashEndpoint).
//
// The Unsplash integration itself is deliberately out of scope (spec
// `meta.unsplash.list`, excluded: "external integration"), but Django still
// answers this route when no access key is configured — with an empty array and
// 200. Returning 404 instead makes the cover-image picker log a failed request
// on every project page, so we reproduce the unconfigured response without
// proxying anything upstream.
func (h *Handler) Unsplash(e *core.RequestEvent) error {
	return e.JSON(http.StatusOK, []any{})
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
