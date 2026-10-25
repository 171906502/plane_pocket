package serializers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
)

// MaxPerPage mirrors Plane's BasePaginator.max_limit. Requests above this are
// clamped rather than rejected, matching Django's behaviour.
const MaxPerPage = 1000

// Envelope is Plane's cursor-pagination response body. Field order and names
// are load-bearing: Plane's frontends read `next_page_results` to decide
// whether to keep scrolling, and `count`/`total_results` are distinct values
// (items on this page vs. items overall). See utils/paginator.py BasePaginator.
type Envelope struct {
	GroupedBy       any    `json:"grouped_by"`
	SubGroupedBy    any    `json:"sub_grouped_by"`
	TotalCount      int    `json:"total_count"`
	NextCursor      string `json:"next_cursor"`
	PrevCursor      string `json:"prev_cursor"`
	NextPageResults bool   `json:"next_page_results"`
	PrevPageResults bool   `json:"prev_page_results"`
	Count           int    `json:"count"`
	TotalPages      int    `json:"total_pages"`
	TotalResults    int    `json:"total_results"`
	ExtraStats      any    `json:"extra_stats"`
	Results         any    `json:"results"`
}

// Cursor is Plane's "value:offset:is_prev" pagination cursor, where `value` is
// the page size and `offset` is the page number (not a row offset).
type Cursor struct {
	Value  float64
	Offset int
	IsPrev bool
}

// ParseCursor decodes a Plane cursor. An empty string yields the first page.
func ParseCursor(raw string, perPage int) (Cursor, error) {
	if raw == "" {
		return Cursor{Value: float64(perPage), Offset: 0, IsPrev: false}, nil
	}
	bits := strings.Split(raw, ":")
	if len(bits) != 3 {
		return Cursor{}, fmt.Errorf("invalid cursor format")
	}
	value, err := strconv.ParseFloat(bits[0], 64)
	if err != nil {
		return Cursor{}, fmt.Errorf("invalid cursor value")
	}
	offset, err := strconv.Atoi(bits[1])
	if err != nil {
		return Cursor{}, fmt.Errorf("invalid cursor offset")
	}
	isPrev, err := strconv.Atoi(bits[2])
	if err != nil {
		return Cursor{}, fmt.Errorf("invalid cursor direction")
	}
	return Cursor{Value: value, Offset: offset, IsPrev: isPrev != 0}, nil
}

// String re-encodes the cursor. Plane emits the page size as an integer when it
// is whole, so 1000.0 renders as "1000" — keep that to stay byte-compatible.
func (c Cursor) String() string {
	value := strconv.FormatFloat(c.Value, 'f', -1, 64)
	isPrev := 0
	if c.IsPrev {
		isPrev = 1
	}
	return fmt.Sprintf("%s:%d:%d", value, c.Offset, isPrev)
}

// ListQuery describes a paginated collection read.
type ListQuery struct {
	Collection string
	// Filter is a PocketBase filter expression; bind user input via Params.
	Filter string
	Params dbx.Params
	Sort   string
	// PerPage defaults to MaxPerPage when zero.
	PerPage int
	// Cursor is the raw `cursor` query parameter; empty means the first page.
	Cursor string
}

// Paginate runs the query and builds Plane's pagination envelope.
//
// `serialize` turns one PocketBase record into the JSON object Plane expects.
// It must be provided — there is no default record shape in Plane's API.
func Paginate(app core.App, q ListQuery, serialize func(*core.Record) any) (*Envelope, error) {
	perPage := q.PerPage
	if perPage <= 0 {
		perPage = MaxPerPage
	}
	if perPage > MaxPerPage {
		perPage = MaxPerPage
	}

	cursor, err := ParseCursor(q.Cursor, perPage)
	if err != nil {
		return nil, err
	}

	page := cursor.Offset
	offset := page * perPage
	if offset < 0 {
		return nil, fmt.Errorf("pagination offset cannot be negative")
	}

	// Fetch one extra row to detect whether another page exists, mirroring
	// Django's `stop = offset + limit + 1` probe.
	records, err := app.FindRecordsByFilter(q.Collection, q.Filter, q.Sort, perPage+1, offset, q.Params)
	if err != nil {
		return nil, err
	}
	hasNext := len(records) > perPage
	if hasNext {
		records = records[:perPage]
	}

	total, err := countMatching(app, q.Collection, q.Filter, q.Params)
	if err != nil {
		return nil, err
	}

	results := make([]any, 0, len(records))
	for _, r := range records {
		results = append(results, serialize(r))
	}

	return &Envelope{
		GroupedBy:       nil,
		SubGroupedBy:    nil,
		TotalCount:      total,
		NextCursor:      Cursor{Value: float64(perPage), Offset: page + 1}.String(),
		PrevCursor:      Cursor{Value: float64(perPage), Offset: page - 1, IsPrev: true}.String(),
		NextPageResults: hasNext,
		PrevPageResults: page > 0,
		Count:           len(results),
		TotalPages:      int(math.Ceil(float64(total) / float64(perPage))),
		TotalResults:    total,
		ExtraStats:      nil,
		Results:         results,
	}, nil
}

// GroupBucket is one bucket of a grouped pagination response: the page of rows
// belonging to that group, plus the group's total row count across all pages.
//
// `Results` carries the bucket payload: a `[]any` of serialized rows in plain
// grouped mode, or an ordered map of nested sub-buckets when the request also
// carries `sub_group_by` (upstream SubGroupedOffsetPaginator nests one envelope
// per subgroup — same `{results, total_results}` keys, see
// `packages/types/src/issues/issue.ts` TIssueResponseResults).
type GroupBucket struct {
	Results      any `json:"results"`
	TotalResults int `json:"total_results"`
}

// GroupBuckets is the ordered object Plane's grouped paginator emits under
// `results`. It exists because encoding/json sorts plain maps alphabetically,
// which would scramble the display order upstream builds into
// `issue_group_values()` (states by `sequence`, priorities by its fixed list).
type GroupBuckets struct {
	keys    []string
	buckets map[string]*GroupBucket
}

// NewGroupBuckets builds an empty ordered bucket map.
func NewGroupBuckets(capacity int) *GroupBuckets {
	return &GroupBuckets{buckets: make(map[string]*GroupBucket, capacity)}
}

// Set inserts (or replaces) a bucket, keeping the first insertion position.
func (g *GroupBuckets) Set(key string, bucket *GroupBucket) {
	if _, exists := g.buckets[key]; !exists {
		g.keys = append(g.keys, key)
	}
	g.buckets[key] = bucket
}

// Get returns the bucket stored under key.
func (g *GroupBuckets) Get(key string) (*GroupBucket, bool) {
	bucket, ok := g.buckets[key]
	return bucket, ok
}

// MarshalJSON emits the buckets in insertion (display) order instead of the
// alphabetical order encoding/json would impose on a map.
func (g *GroupBuckets) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, key := range g.keys {
		if i > 0 {
			buf.WriteByte(',')
		}
		keyJSON, err := json.Marshal(key)
		if err != nil {
			return nil, err
		}
		buf.Write(keyJSON)
		buf.WriteByte(':')
		bucketJSON, err := json.Marshal(g.buckets[key])
		if err != nil {
			return nil, err
		}
		buf.Write(bucketJSON)
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

// GroupedEnvelope is Plane's grouped cursor-pagination body. BasePaginator
// switches to it whenever `group_by_field_name` is set: `results` stops being an
// array and becomes an object keyed by group id, and *every* group in the
// group-by field list appears — including empty ones — because
// `GroupedOffsetPaginator.__get_field_dict()` pre-seeds the whole list before
// `__query_grouper()` appends rows.
type GroupedEnvelope struct {
	GroupedBy       any           `json:"grouped_by"`
	SubGroupedBy    any           `json:"sub_grouped_by"`
	TotalCount      int           `json:"total_count"`
	NextCursor      string        `json:"next_cursor"`
	PrevCursor      string        `json:"prev_cursor"`
	NextPageResults bool          `json:"next_page_results"`
	PrevPageResults bool          `json:"prev_page_results"`
	Count           int           `json:"count"`
	TotalPages      int           `json:"total_pages"`
	TotalResults    int           `json:"total_results"`
	ExtraStats      any           `json:"extra_stats"`
	Results         *GroupBuckets `json:"results"`
}

// GroupKey is one bucket of a grouped response.
//
// `ID` is the key the frontend sees and `Match` is the value the backing column
// actually stores. They are usually different: Plane's frontends key their
// grouping maps by Plane's UUID, while PocketBase stores relations as its own
// 15-char record ids (the UUID lives in a side column).
type GroupKey struct {
	ID    string
	Match string
}

// GroupQuery describes a grouped, cursor-paginated collection read.
type GroupQuery struct {
	Collection string
	// Filter is the PocketBase filter shared by every group.
	Filter string
	Params dbx.Params
	// Sort is a PocketBase sort expression ("-created_at", "sequence_id").
	Sort string
	// PerPage defaults to MaxPerPage when zero.
	PerPage int
	// Cursor is the raw `cursor` query parameter; empty means the first page.
	Cursor string
	// GroupedBy is echoed into the envelope's `grouped_by` field.
	GroupedBy any
	// GroupFilter is ANDed onto Filter for a single group and must reference
	// {:group} (e.g. "state = {:group}").
	GroupFilter string
	// GroupKeys is the ordered list of buckets to seed. Every entry gets a
	// bucket, empty or not.
	GroupKeys []GroupKey
	// SubGroupedBy is echoed into the envelope's `sub_grouped_by` field.
	SubGroupedBy any
	// SubGroupKeys switches PaginateGrouped into nested sub-group mode
	// (upstream SubGroupedOffsetPaginator). Every group's `results` becomes an
	// ordered map of subgroup buckets. Unlike GroupKeys, only subgroups with
	// at least one matching row appear — upstream seeds them from
	// __get_subgroup_total_queryset(), which only knows existing cells.
	SubGroupKeys []GroupKey
	// SubGroupFilter is ANDed onto Filter + GroupFilter for a single
	// (group, subgroup) cell and must reference {:subgroup}.
	SubGroupFilter string
}

// PaginateGrouped mirrors BasePaginator + GroupedOffsetPaginator.
//
// Django partitions each group with a row_number() window and keeps rows
// (page*perPage, page*perPage+perPage], so pagination is per group, not across
// the whole result set. We do the equivalent with one query per group: at the
// scale a published board sees this is cheap, and it avoids hand-writing window
// SQL against PocketBase's filter resolver.
func PaginateGrouped(app core.App, q GroupQuery, serialize func(*core.Record) any) (*GroupedEnvelope, error) {
	perPage := q.PerPage
	if perPage <= 0 {
		perPage = MaxPerPage
	}
	if perPage > MaxPerPage {
		perPage = MaxPerPage
	}

	cursor, err := ParseCursor(q.Cursor, perPage)
	if err != nil {
		return nil, err
	}
	page := cursor.Offset
	if page < 0 {
		return nil, fmt.Errorf("pagination offset cannot be negative")
	}
	offset := page * perPage

	total, err := countMatching(app, q.Collection, q.Filter, q.Params)
	if err != nil {
		return nil, err
	}

	groupFilter := q.GroupFilter
	if groupFilter == "" {
		groupFilter = "group = {:group}"
	}

	env := &GroupedEnvelope{
		GroupedBy:       q.GroupedBy,
		SubGroupedBy:    q.SubGroupedBy,
		TotalCount:      total,
		NextCursor:      Cursor{Value: float64(perPage), Offset: page + 1}.String(),
		PrevCursor:      Cursor{Value: float64(perPage), Offset: page - 1, IsPrev: true}.String(),
		PrevPageResults: page > 0,
		TotalResults:    total,
		ExtraStats:      nil,
		Results:         NewGroupBuckets(len(q.GroupKeys)),
	}

	// `total_pages` in Django is ceil(largest group size / limit), not
	// ceil(total / limit) — the grouped paginator pages each group separately
	// (in sub-group mode each (group, subgroup) cell separately).
	largestGroup := 0

	if len(q.SubGroupKeys) == 0 {
		for _, key := range q.GroupKeys {
			params := make(dbx.Params, len(q.Params)+1)
			for k, v := range q.Params {
				params[k] = v
			}
			params["group"] = key.Match

			filter := q.Filter + " && " + groupFilter

			count, err := countMatching(app, q.Collection, filter, params)
			if err != nil {
				return nil, err
			}
			if count > largestGroup {
				largestGroup = count
			}

			records, err := app.FindRecordsByFilter(q.Collection, filter, q.Sort, perPage, offset, params)
			if err != nil {
				return nil, err
			}

			bucket := &GroupBucket{Results: make([]any, 0, len(records)), TotalResults: count}
			for _, r := range records {
				bucket.Results = append(bucket.Results.([]any), serialize(r))
			}
			env.Count += len(records)
			env.Results.Set(key.ID, bucket)
		}
	} else {
		// Nested mode: upstream partitions rows by (group, subgroup) and
		// seeds each group's results dict only from subgroup keys that
		// actually occur in the filtered queryset, so a group with no rows
		// emits `"results": {}` and cells with zero rows are omitted.
		for _, key := range q.GroupKeys {
			params := make(dbx.Params, len(q.Params)+1)
			for k, v := range q.Params {
				params[k] = v
			}
			params["group"] = key.Match

			filter := q.Filter + " && " + q.GroupFilter

			groupCount, err := countMatching(app, q.Collection, filter, params)
			if err != nil {
				return nil, err
			}

			subs := NewGroupBuckets(len(q.SubGroupKeys))
			for _, subKey := range q.SubGroupKeys {
				cellParams := make(dbx.Params, len(params)+1)
				for k, v := range params {
					cellParams[k] = v
				}
				cellParams["subgroup"] = subKey.Match

				cellFilter := filter + " && " + q.SubGroupFilter

				cellCount, err := countMatching(app, q.Collection, cellFilter, cellParams)
				if err != nil {
					return nil, err
				}
				if cellCount > largestGroup {
					largestGroup = cellCount
				}
				if cellCount == 0 {
					continue
				}

				records, err := app.FindRecordsByFilter(q.Collection, cellFilter, q.Sort, perPage, offset, cellParams)
				if err != nil {
					return nil, err
				}

				bucket := &GroupBucket{Results: make([]any, 0, len(records)), TotalResults: cellCount}
				for _, r := range records {
					bucket.Results = append(bucket.Results.([]any), serialize(r))
				}
				env.Count += len(records)
				subs.Set(subKey.ID, bucket)
			}
			env.Results.Set(key.ID, &GroupBucket{Results: subs, TotalResults: groupCount})
		}
	}

	// `queryset.filter(row_number__gte=stop).exists()`: another page exists iff
	// some group still has rows past this page's window.
	env.NextPageResults = largestGroup > (page+1)*perPage
	if env.Count > 0 {
		env.TotalPages = int(math.Ceil(float64(largestGroup) / float64(perPage)))
	}

	return env, nil
}

// countMatching counts rows matching the same filter the page query used, so
// `total_results` and `total_pages` stay consistent with `results`.
//
// The count must run through PocketBase's filter resolver (which understands
// `&&`/`||` and relation traversal such as `state.group`) rather than raw SQL,
// so it reuses FindRecordsByFilter with no limit instead of a hand-built
// `SELECT count(*)`. A dedicated COUNT query would be cheaper; this keeps the
// resolver semantics exact, which matters more for wire compatibility.
func countMatching(app core.App, collection string, filter string, params dbx.Params) (int, error) {
	records, err := app.FindRecordsByFilter(collection, filter, "", 0, 0, params)
	if err != nil {
		return 0, err
	}
	return len(records), nil
}

// ListParams reads `per_page` and `cursor` off the request, matching Plane's
// parameter names.
func ListParams(e *core.RequestEvent, defaultPerPage int) (perPage int, cursor string) {
	query := e.Request.URL.Query()
	perPage = defaultPerPage
	if raw := query.Get("per_page"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			perPage = parsed
		}
	}
	if perPage > MaxPerPage {
		perPage = MaxPerPage
	}
	return perPage, query.Get("cursor")
}
