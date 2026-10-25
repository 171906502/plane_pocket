package cycle

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"
)

// groupingFixture extends the shared fixture with explicit state sequences
// (the fixture's states are seeded in map order, all with sequence 0) and
// issues whose priorities/states pin each grouping cell.
type groupingFixture struct {
	*fixture
}

func newGroupingFixture(t *testing.T) *groupingFixture {
	t.Helper()
	f := &groupingFixture{fixture: newFixture(t)}
	// State.Meta ordering upstream is `sequence`; give each state a distinct
	// one so the bucket order on the wire is deterministic.
	sequences := map[string]float64{"Todo": 100, "Blocked": 200, "Done": 300}
	for name, seq := range sequences {
		f.states[name].Set("sequence", seq)
		if err := f.app.Save(f.states[name]); err != nil {
			t.Fatalf("save state %s: %v", name, err)
		}
	}
	return f
}

// objectKeyOrder decodes a JSON object's keys in wire order. encoding/json
// sorts map keys when decoding, so order assertions must scan tokens instead.
func objectKeyOrder(t *testing.T, raw []byte) []string {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(raw))
	tok, err := dec.Token()
	if err != nil {
		t.Fatalf("token: %v", err)
	}
	if delim, ok := tok.(json.Delim); !ok || delim != '{' {
		t.Fatalf("objectKeyOrder: not an object: %s", raw)
	}
	keys := []string{}
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			t.Fatalf("key token: %v", err)
		}
		keys = append(keys, keyTok.(string))
		var skip json.RawMessage
		if err := dec.Decode(&skip); err != nil {
			t.Fatalf("skip value: %v", err)
		}
	}
	return keys
}

// rawAt digs the JSON value at keys (objects all the way down) as RawMessage.
func rawAt(t *testing.T, raw []byte, keys ...string) []byte {
	t.Helper()
	cur := raw
	for _, key := range keys {
		out := map[string]json.RawMessage{}
		if err := json.Unmarshal(cur, &out); err != nil {
			t.Fatalf("decode %v: %v (%s)", keys, err, cur)
		}
		next, ok := out[key]
		if !ok {
			t.Fatalf("missing key %v in %s", keys, raw)
		}
		cur = next
	}
	return cur
}

// rowIDs extracts the Plane UUID of each serialized issue row in an array
// (the rows are full Issue serializer objects, not bare ids).
func rowIDs(t *testing.T, raw []byte) []string {
	t.Helper()
	var rows []map[string]any
	if err := json.Unmarshal(raw, &rows); err != nil {
		t.Fatalf("rows: %v (%s)", err, raw)
	}
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		id, _ := row["id"].(string)
		ids = append(ids, id)
	}
	return ids
}

// containsUUID reports whether ids contains want.
func containsUUID(ids []string, want string) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}

// TestIssuesListGroupedByStateAndPriority pins the grouped envelope for
// `?group_by=state_id&sub_group_by=priority`: nesting, bucket keys in upstream
// display order, per-cell totals, and empty cells omitted. The shape is the
// frontend's TIssueResponseResults (packages/types/src/issues/issue.ts):
// results = { <state uuid>: { results: { <priority>: { results: [...],
// total_results } }, total_results } }.
func TestIssuesListGroupedByStateAndPriority(t *testing.T) {
	f := newGroupingFixture(t)
	h := New(f.app)

	urgentTodo := f.seedIssue(t, "Urgent", "urgent", "Todo")
	highBlocked := f.seedIssue(t, "High", "high", "Blocked")
	lowDone := f.seedIssue(t, "Low", "low", "Done")
	// No priority at all: the DB stores '' and the bucket must be "none"
	// (the serializer renders '' as "none").
	noneTodo := f.seedIssue(t, "NoPriority", "", "Todo")
	_ = highBlocked

	e, rec := f.event(t, http.MethodGet, "/?group_by=state_id&sub_group_by=priority",
		map[string]string{"cycle_id": f.cycle.GetString("uuid")})
	if err := h.IssuesList(e); err != nil {
		t.Fatalf("grouped list: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.Bytes()

	if got := rawAt(t, body, "grouped_by"); string(got) != `"state_id"` {
		t.Fatalf("grouped_by = %s, want \"state_id\"", got)
	}
	if got := rawAt(t, body, "sub_grouped_by"); string(got) != `"priority"` {
		t.Fatalf("sub_grouped_by = %s, want \"priority\"", got)
	}

	// Groups: every project state appears, ordered by `sequence`
	// (Todo=100 < Blocked=200 < Done=300).
	wantStates := []string{
		f.states["Todo"].GetString("uuid"),
		f.states["Blocked"].GetString("uuid"),
		f.states["Done"].GetString("uuid"),
	}
	gotStates := objectKeyOrder(t, rawAt(t, body, "results"))
	if len(gotStates) != len(wantStates) {
		t.Fatalf("results keys = %v, want %v", gotStates, wantStates)
	}
	for i, want := range wantStates {
		if gotStates[i] != want {
			t.Fatalf("results keys[%d] = %s, want %s", i, gotStates[i], want)
		}
	}

	// Sub-groups: only priorities with rows appear, in upstream's canonical
	// order ["low", "medium", "high", "urgent", "none"] (grouper.py).
	// Todo holds urgent + none (empty '' bucket), so "low"… are omitted.
	todoSubs := objectKeyOrder(t, rawAt(t, body, "results", wantStates[0], "results"))
	if len(todoSubs) != 2 || todoSubs[0] != "urgent" || todoSubs[1] != "none" {
		t.Fatalf("Todo subgroups = %v, want [urgent none]", todoSubs)
	}

	// Cell contents and totals.
	todoUrgent := rawAt(t, body, "results", wantStates[0], "results", "urgent")
	if got := string(rawAt(t, todoUrgent, "total_results")); got != "1" {
		t.Fatalf("Todo/urgent total_results = %s, want 1", got)
	}
	urgentIDs := rowIDs(t, rawAt(t, todoUrgent, "results"))
	if len(urgentIDs) != 1 || urgentIDs[0] != urgentTodo.GetString("uuid") {
		t.Fatalf("Todo/urgent = %v, want [%s]", urgentIDs, urgentTodo.GetString("uuid"))
	}
	todoNone := rawAt(t, body, "results", wantStates[0], "results", "none")
	noneIDs := rowIDs(t, rawAt(t, todoNone, "results"))
	if len(noneIDs) != 1 || noneIDs[0] != noneTodo.GetString("uuid") {
		t.Fatalf("Todo/none = %v, want [%s] (empty priority must land in none)", noneIDs, noneTodo.GetString("uuid"))
	}

	// Group-level total counts every row of the group across subgroups.
	if got := string(rawAt(t, body, "results", wantStates[0], "total_results")); got != "2" {
		t.Fatalf("Todo total_results = %s, want 2", got)
	}
	// An empty subgroup cell does not exist; Done has only "low".
	doneSubs := objectKeyOrder(t, rawAt(t, body, "results", wantStates[2], "results"))
	if len(doneSubs) != 1 || doneSubs[0] != "low" {
		t.Fatalf("Done subgroups = %v, want [low]", doneSubs)
	}
	doneIDs := rowIDs(t, rawAt(t, body, "results", wantStates[2], "results", "low", "results"))
	if len(doneIDs) != 1 || doneIDs[0] != lowDone.GetString("uuid") {
		t.Fatalf("Done/low = %v, want [%s]", doneIDs, lowDone.GetString("uuid"))
	}
}

// TestIssuesListGroupedByStateOnly pins the single-level grouped envelope:
// sub_grouped_by is null, every declared state group is present (empty ones
// with `results: []`), and each bucket is `{results, total_results}`.
func TestIssuesListGroupedByStateOnly(t *testing.T) {
	f := newGroupingFixture(t)
	h := New(f.app)

	high := f.seedIssue(t, "High", "high", "Blocked")

	e, rec := f.event(t, http.MethodGet, "/?group_by=state_id",
		map[string]string{"cycle_id": f.cycle.GetString("uuid")})
	if err := h.IssuesList(e); err != nil {
		t.Fatalf("grouped list: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.Bytes()

	if got := rawAt(t, body, "grouped_by"); string(got) != `"state_id"` {
		t.Fatalf("grouped_by = %s, want \"state_id\"", got)
	}
	if got := rawAt(t, body, "sub_grouped_by"); string(got) != "null" {
		t.Fatalf("sub_grouped_by = %s, want null", got)
	}

	todoUUID := f.states["Todo"].GetString("uuid")
	// Empty group still present with an empty array (GroupedOffsetPaginator
	// __get_field_dict pre-seeds every key).
	todoBucket := rawAt(t, body, "results", todoUUID)
	if got := string(rawAt(t, todoBucket, "total_results")); got != "0" {
		t.Fatalf("Todo total_results = %s, want 0", got)
	}
	if got := rawAt(t, todoBucket, "results"); string(got) != "[]" {
		t.Fatalf("Todo results = %s, want []", got)
	}
	blockedIDs := rowIDs(t, rawAt(t, body, "results", f.states["Blocked"].GetString("uuid"), "results"))
	if len(blockedIDs) != 1 || blockedIDs[0] != high.GetString("uuid") {
		t.Fatalf("Blocked = %v, want [%s]", blockedIDs, high.GetString("uuid"))
	}
}

// TestIssuesListGroupedByStateGroup pins `group_by=state__group`: bucket keys
// are the five state groups in upstream's fixed order and issues bucket by
// their state's group.
func TestIssuesListGroupedByStateGroup(t *testing.T) {
	f := newGroupingFixture(t)
	h := New(f.app)

	high := f.seedIssue(t, "High", "high", "Blocked") // group=started
	f.seedIssue(t, "Low", "low", "Done")              // group=completed

	e, rec := f.event(t, http.MethodGet, "/?group_by=state__group",
		map[string]string{"cycle_id": f.cycle.GetString("uuid")})
	if err := h.IssuesList(e); err != nil {
		t.Fatalf("grouped list: %v", err)
	}
	body := rec.Body.Bytes()

	want := []string{"backlog", "unstarted", "started", "completed", "cancelled"}
	got := objectKeyOrder(t, rawAt(t, body, "results"))
	if len(got) != len(want) {
		t.Fatalf("state__group keys = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("state__group keys[%d] = %s, want %s", i, got[i], want[i])
		}
	}
	var startedIDs []string = rowIDs(t, rawAt(t, body, "results", "started", "results"))
	if len(startedIDs) != 1 || startedIDs[0] != high.GetString("uuid") {
		t.Fatalf("started = %v, want [%s]", startedIDs, high.GetString("uuid"))
	}
}

// TestIssuesListFlatWithoutGroupBy pins the no-params contract: the flat
// envelope (results is an ARRAY, grouped_by null) — byte-shape unchanged from
// before L-6.
func TestIssuesListFlatWithoutGroupBy(t *testing.T) {
	f := newGroupingFixture(t)
	h := New(f.app)

	urgent := f.seedIssue(t, "Urgent", "urgent", "Todo")
	high := f.seedIssue(t, "High", "high", "Blocked")

	for _, target := range []string{
		"/",
		"/?group_by=&sub_group_by=",
	} {
		e, rec := f.event(t, http.MethodGet, target, map[string]string{"cycle_id": f.cycle.GetString("uuid")})
		if err := h.IssuesList(e); err != nil {
			t.Fatalf("%s: %v", target, err)
		}
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status %d", target, rec.Code)
		}
		body := rec.Body.Bytes()
		if got := rawAt(t, body, "grouped_by"); string(got) != "null" {
			t.Fatalf("%s: grouped_by = %s, want null", target, got)
		}
		var list []map[string]any
		if err := json.Unmarshal(rawAt(t, body, "results"), &list); err != nil {
			t.Fatalf("%s: results not an array: %s", target, body)
		}
		if len(list) != 2 {
			t.Fatalf("%s: results = %v, want 2 issues", target, body)
		}
		// Both issues present (order is -created_at and the seeds tie).
		ids := rowIDs(t, rawAt(t, body, "results"))
		if !containsUUID(ids, urgent.GetString("uuid")) || !containsUUID(ids, high.GetString("uuid")) {
			t.Fatalf("%s: results = %v, want both seeds", target, ids)
		}
	}
}

// TestIssuesListUnknownGroupByFallsBackFlat: an unsupported grouping key must
// degrade to the flat envelope (200), never 500 or a half-grouped shape.
func TestIssuesListUnknownGroupByFallsBackFlat(t *testing.T) {
	f := newGroupingFixture(t)
	h := New(f.app)

	f.seedIssue(t, "Urgent", "urgent", "Todo")

	for _, target := range []string{
		"/?group_by=assignees__id",
		"/?group_by=state_id&sub_group_by=labels__id",
		"/?group_by=state_id&sub_group_by=bogus",
	} {
		e, rec := f.event(t, http.MethodGet, target, map[string]string{"cycle_id": f.cycle.GetString("uuid")})
		if err := h.IssuesList(e); err != nil {
			t.Fatalf("%s: %v", target, err)
		}
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status %d body %s", target, rec.Code, rec.Body.String())
		}
		body := rec.Body.Bytes()
		if got := rawAt(t, body, "grouped_by"); string(got) != "null" {
			t.Fatalf("%s: grouped_by = %s, want null (flat fallback)", target, got)
		}
		var list []map[string]any
		if err := json.Unmarshal(rawAt(t, body, "results"), &list); err != nil {
			t.Fatalf("%s: results not an array: %s", target, body)
		}
		if len(list) != 1 {
			t.Fatalf("%s: results = %s, want the flat list", target, body)
		}
	}
}

// TestIssuesListEqualGroupAndSubGroup: upstream returns 400 when group_by ==
// sub_group_by (CycleIssueViewSet.list, before paginating).
func TestIssuesListEqualGroupAndSubGroup(t *testing.T) {
	f := newGroupingFixture(t)
	h := New(f.app)

	e, rec := f.event(t, http.MethodGet, "/?group_by=priority&sub_group_by=priority",
		map[string]string{"cycle_id": f.cycle.GetString("uuid")})
	if err := h.IssuesList(e); err != nil {
		t.Fatalf("equal group/sub: %v", err)
	}
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400 (body %s)", rec.Code, rec.Body.String())
	}
	if got := rawAt(t, rec.Body.Bytes(), "error"); got == nil {
		t.Fatalf("body missing error: %s", rec.Body.String())
	}
}

// TestIssuesListPriorityGroupOrder: plain group_by=priority emits the buckets
// in upstream's canonical order ["low","medium","high","urgent","none"],
// including empty ones.
func TestIssuesListPriorityGroupOrder(t *testing.T) {
	f := newGroupingFixture(t)
	h := New(f.app)

	f.seedIssue(t, "Urgent", "urgent", "Todo")
	f.seedIssue(t, "Low", "low", "Done")

	e, rec := f.event(t, http.MethodGet, "/?group_by=priority",
		map[string]string{"cycle_id": f.cycle.GetString("uuid")})
	if err := h.IssuesList(e); err != nil {
		t.Fatalf("priority grouped: %v", err)
	}
	body := rec.Body.Bytes()

	if got := rawAt(t, body, "grouped_by"); string(got) != `"priority"` {
		t.Fatalf("grouped_by = %s, want \"priority\"", got)
	}
	want := []string{"low", "medium", "high", "urgent", "none"}
	got := objectKeyOrder(t, rawAt(t, body, "results"))
	if len(got) != len(want) {
		t.Fatalf("priority keys = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("priority keys[%d] = %s, want %s", i, got[i], want[i])
		}
	}
	var urgentIDs []string = rowIDs(t, rawAt(t, body, "results", "urgent", "results"))
	if len(urgentIDs) != 1 {
		t.Fatalf("urgent = %v, want 1 issue", urgentIDs)
	}
}
