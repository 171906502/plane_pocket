// Package filters evaluates Plane's "rich filter" expression — the JSON blob
// the web client sends as the `filters` query parameter on issue-list
// endpoints (`?filters={"and":[{"state_id__exact":"<uuid>"},…]}`).
//
// Contract: the web client builds the expression from
// TWorkItemFilterExpression (packages/types/src/view-props.ts): either a flat
// condition map `{ "<property>__<operator>": value }` or a single AND group
// `{"and": [conditionMap, …]}`. The only logical operator is `and`; the only
// value operators are `exact`, `in` (multi-value, comma-joined on the wire)
// and `range` (dates, "from,to").
//
// Unsupported properties or values are skipped rather than erroring — the
// Django filter layer degrades the same way for unknown keys.
package filters

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
)

// condition is one decoded rich-filter condition.
type condition struct {
	property string
	operator string
	values   []string
	isNone   bool
}

// Apply decodes rawJSON (the `filters` query parameter) and ANDs every
// supported condition onto the PocketBase filter expression. projectID scopes
// bridge-table lookups and state-group resolution; it may be "" for
// workspace-level lists that pass project_id conditions instead.
func Apply(app core.App, filter string, params dbx.Params, projectID, rawJSON string) string {
	if strings.TrimSpace(rawJSON) == "" {
		return filter
	}
	conds, ok := decode(rawJSON)
	if !ok || len(conds) == 0 {
		return filter
	}
	for _, cond := range conds {
		if clause := predicate(app, projectID, cond, params); clause != "" {
			filter += " && " + clause
		}
	}
	return filter
}

// decode extracts the flat list of conditions from the expression JSON.
func decode(rawJSON string) ([]condition, bool) {
	var expr map[string]any
	if err := json.Unmarshal([]byte(rawJSON), &expr); err != nil {
		return nil, false
	}
	maps := []map[string]any{}
	if group, ok := expr["and"].([]any); ok {
		for _, item := range group {
			if m, ok := item.(map[string]any); ok {
				maps = append(maps, m)
			}
		}
	} else {
		maps = append(maps, expr)
	}
	conds := make([]condition, 0)
	for _, m := range maps {
		for key, raw := range m {
			if !strings.Contains(key, "__") {
				continue
			}
			parts := strings.SplitN(key, "__", 2)
			cond := condition{property: parts[0], operator: parts[1]}
			switch v := raw.(type) {
			case string:
				for _, item := range strings.Split(v, ",") {
					item = strings.TrimSpace(item)
					if item != "" {
						cond.values = append(cond.values, item)
					}
				}
			case float64:
				cond.values = append(cond.values, strconv.FormatFloat(v, 'f', -1, 64))
			case bool:
				cond.values = append(cond.values, strconv.FormatBool(v))
			case []any:
				for _, item := range v {
					if s, ok := item.(string); ok && s != "" {
						cond.values = append(cond.values, s)
					}
				}
			default:
				continue
			}
			for _, v := range cond.values {
				if v == "none" || v == "None" {
					cond.isNone = true
					break
				}
			}
			conds = append(conds, cond)
		}
	}
	return conds, true
}

// predicate builds the PocketBase predicate for one condition, or "" when the
// property/operator is not supported.
func predicate(app core.App, projectID string, cond condition, params dbx.Params) string {
	switch cond.property {
	case "priority":
		if cond.isNone {
			return ""
		}
		return orIn("priority", cond.values, params)
	case "state_id":
		if cond.isNone {
			return ""
		}
		ids := resolveIDs(app, "states", cond.values)
		return orIn("state", ids, params)
	case "state_group":
		return orIn("state", stateIDsByGroup(app, projectID, cond.values), params)
	case "created_by_id":
		if cond.isNone {
			return ""
		}
		ids := resolveIDs(app, "users", cond.values)
		return orIn("created_by", ids, params)
	case "project_id":
		if cond.isNone {
			return ""
		}
		return orIn("project", cond.values, params)
	case "assignee_id":
		return bridgePredicate(app, projectID, "issue_assignees", "assignee", "users", cond, params)
	case "label_id":
		return bridgePredicate(app, projectID, "issue_labels", "label", "labels", cond, params)
	case "cycle_id":
		return bridgePredicate(app, projectID, "cycle_issues", "cycle", "cycles", cond, params)
	case "module_id":
		return bridgePredicate(app, projectID, "module_issues", "module", "modules", cond, params)
	case "subscriber_id":
		return bridgePredicate(app, projectID, "issue_subscribers", "subscriber", "users", cond, params)
	case "start_date", "target_date", "created_at", "updated_at":
		column := cond.property
		switch cond.operator {
		case "exact":
			return dayWindow(column, cond.values, params)
		case "range":
			if len(cond.values) != 2 {
				return ""
			}
			return rangeWindow(column, cond.values[0], cond.values[1], params)
		}
	}
	return ""
}

// orIn binds `(col = {:p} || col = {:p} …)` with an auto-numbered prefix.
func orIn(column string, values []string, params dbx.Params) string {
	parts := make([]string, 0, len(values))
	for i, v := range values {
		key := "rf" + strconv.Itoa(len(params)) + "x" + strconv.Itoa(i)
		parts = append(parts, column+" = {:"+key+"}")
		params[key] = v
	}
	if len(parts) == 0 {
		return ""
	}
	if len(parts) == 1 {
		return parts[0]
	}
	return "(" + strings.Join(parts, " || ") + ")"
}

// resolveIDs maps Plane UUIDs to PocketBase record ids, dropping misses.
func resolveIDs(app core.App, collection string, uuids []string) []string {
	ids := make([]string, 0, len(uuids))
	for _, uuid := range uuids {
		if record, _ := app.FindFirstRecordByFilter(collection, "uuid = {:uuid}", dbx.Params{"uuid": uuid}); record != nil {
			ids = append(ids, record.Id)
		}
	}
	return ids
}

// ResolveIDs maps Plane UUIDs to PocketBase record ids, dropping misses.
// Exported for the intake handler, which filters its bridge collection by the
// related issue's attributes.
func ResolveIDs(app core.App, collection string, uuids []string) []string {
	return resolveIDs(app, collection, uuids)
}

// InPredicate binds `(col = {:p0} || col = {:p1} …)` over plain values.
// Exported for the intake handler (same wire semantics as orIn).
func InPredicate(column string, values []string, params dbx.Params) string {
	return orIn(column, values, params)
}

// BridgeIssueIDs resolves the PocketBase issue ids that have a live row in
// `bridge` pointing at one of the given Plane UUIDs (resolved through
// `collection`). Exported for the intake handler.
func BridgeIssueIDs(app core.App, bridge, column, collection string, uuids []string) []string {
	ids := resolveIDs(app, collection, uuids)
	if len(ids) == 0 {
		return nil
	}
	legs := make([]string, 0, len(ids))
	sqlParams := dbx.Params{}
	for i, id := range ids {
		key := "b" + strconv.Itoa(i)
		legs = append(legs, "b."+column+" = {:"+key+"}")
		sqlParams[key] = id
	}
	query := "SELECT DISTINCT b.issue AS id FROM " + bridge + " b WHERE b.deleted_at = '' AND (" + strings.Join(legs, " OR ") + ")"
	return queryIDs(app, query, sqlParams)
}

// stateIDsByGroup resolves state-group names to the project's state ids.
func stateIDsByGroup(app core.App, projectID string, groups []string) []string {
	if projectID == "" || len(groups) == 0 {
		return nil
	}
	records, err := app.FindRecordsByFilter("states", "project = {:project} && deleted_at = ''", "", 0, 0, dbx.Params{"project": projectID})
	if err != nil {
		return nil
	}
	want := map[string]bool{}
	for _, g := range groups {
		want[g] = true
	}
	ids := make([]string, 0, len(records))
	for _, r := range records {
		if want[r.GetString("group")] {
			ids = append(ids, r.Id)
		}
	}
	return ids
}

// bridgePredicate filters issues through a bridge table:
//
//	exact/in  → issue has a non-deleted bridge row pointing at one of the values
//	none      → issue has no non-deleted bridge row at all (project-scoped)
//
// PocketBase filter expressions cannot express subqueries, so the matching
// issue ids are resolved with SQL first and turned into a disjunction over
// `id`. `bridge`/`column`/`collection` are internal literals, never user input.
func bridgePredicate(app core.App, projectID, bridge, column, collection string, cond condition, params dbx.Params) string {
	if cond.isNone {
		if projectID == "" {
			return ""
		}
		ids := noneBridgeIssueIDs(app, bridge, projectID)
		return orIn("id", ids, params)
	}
	ids := resolveIDs(app, collection, cond.values)
	if len(ids) == 0 {
		return ""
	}
	legs := make([]string, 0, len(ids))
	sqlParams := dbx.Params{}
	for i, id := range ids {
		key := "b" + strconv.Itoa(i)
		legs = append(legs, "b."+column+" = {:"+key+"}")
		sqlParams[key] = id
	}
	query := "SELECT DISTINCT b.issue AS id FROM " + bridge + " b WHERE b.deleted_at = '' AND (" + strings.Join(legs, " OR ") + ")"
	return orIn("id", queryIDs(app, query, sqlParams), params)
}

// noneBridgeIssueIDs lists the project's issues without any live bridge row.
func noneBridgeIssueIDs(app core.App, bridge, projectID string) []string {
	return queryIDs(app,
		"SELECT i.id FROM issues i WHERE i.project = {:project} AND i.deleted_at = '' "+
			"AND NOT EXISTS (SELECT 1 FROM "+bridge+" b WHERE b.issue = i.id AND b.deleted_at = '')",
		dbx.Params{"project": projectID})
}

// queryIDs runs a single-column SQL query aliased `id` and returns the values.
func queryIDs(app core.App, query string, params dbx.Params) []string {
	var rows []struct {
		ID string `db:"id"`
	}
	if err := app.DB().NewQuery(query).Bind(params).All(&rows); err != nil {
		return nil
	}
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		if row.ID != "" {
			ids = append(ids, row.ID)
		}
	}
	return ids
}

// dayWindow matches one calendar day: `col >= day 00:00 && col < day+1`.
func dayWindow(column string, values []string, params dbx.Params) string {
	if len(values) != 1 {
		return ""
	}
	day, ok := parseDay(values[0])
	if !ok {
		return ""
	}
	lo, hi := windowKeys(params)
	params[lo] = day.Format("2006-01-02 15:04:05.000Z")
	params[hi] = day.AddDate(0, 0, 1).Format("2006-01-02 15:04:05.000Z")
	return "(" + column + " >= {:" + lo + "} && " + column + " < {:" + hi + "})"
}

// rangeWindow matches an inclusive date interval: `[from 00:00, to+1)`.
func rangeWindow(column, from, to string, params dbx.Params) string {
	fromDay, ok := parseDay(from)
	if !ok {
		return ""
	}
	// The `to` side may itself be a full ISO timestamp; fall back to day
	// granularity when it is not parseable beyond the date prefix.
	toDay, ok := parseDay(to)
	if !ok {
		return ""
	}
	lo, hi := windowKeys(params)
	params[lo] = fromDay.Format("2006-01-02 15:04:05.000Z")
	params[hi] = toDay.AddDate(0, 0, 1).Format("2006-01-02 15:04:05.000Z")
	return "(" + column + " >= {:" + lo + "} && " + column + " < {:" + hi + "})"
}

// parseDay keeps the leading YYYY-MM-DD of an ISO-ish value.
func parseDay(value string) (time.Time, bool) {
	if len(value) < 10 {
		return time.Time{}, false
	}
	day, err := time.Parse("2006-01-02", value[:10])
	if err != nil {
		return time.Time{}, false
	}
	return day, true
}

// windowKeys allocates two distinct param names.
func windowKeys(params dbx.Params) (string, string) {
	base := "rfw" + strconv.Itoa(len(params))
	return base + "lo", base + "hi"
}
