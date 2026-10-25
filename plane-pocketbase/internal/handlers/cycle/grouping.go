package cycle

import (
	"net/http"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"

	"github.com/plane-pocketbase/plane-pocketbase/internal/middleware"
	"github.com/plane-pocketbase/plane-pocketbase/internal/serializers"
)

// priorityGroupOrder is `issue_group_values(field="priority")` upstream
// (apps/api/plane/utils/grouper.py): the fixed list, in this order.
var priorityGroupOrder = []string{"low", "medium", "high", "urgent", "none"}

// stateGroupOrder is `issue_group_values(field="state__group")`.
var stateGroupOrder = []string{"backlog", "unstarted", "started", "completed", "cancelled"}

// issueGroupKeys returns the ordered group buckets for a `group_by`/`sub_group_by`
// value, mirroring upstream `issue_group_values()`:
//
//   - state_id: the project's states (triage excluded — StateManager drops
//     group="triage"), ordered by State.Meta ordering (`sequence`);
//   - priority / state__group: the fixed canonical lists above;
//   - anything else (labels__id, assignees__id, target_date, …): not grouped
//     by this port — ok=false and the caller falls back to the flat envelope.
//
// `param` names the bind placeholder in the returned filter ("group" for the
// outer grouping, "subgroup" for the nested one).
func issueGroupKeys(app core.App, scope *middleware.Scope, field, param string) (keys []serializers.GroupKey, filter string, ok bool) {
	p := "{:" + param + "}"
	switch field {
	case "state_id":
		states, err := app.FindRecordsByFilter(
			"states",
			"project = {:project} && workspace = {:ws} && group != 'triage' && is_triage = false",
			"sequence", 0, 0,
			dbx.Params{"project": scope.Project.Id, "ws": scope.Workspace.Id},
		)
		if err != nil {
			return nil, "", false
		}
		keys = make([]serializers.GroupKey, 0, len(states))
		for _, s := range states {
			// Bucket keys are Plane UUIDs (what the frontend keys its grouped
			// maps by); the `state` relation stores the PocketBase record id.
			keys = append(keys, serializers.GroupKey{ID: serializers.UUID(s), Match: s.Id})
		}
		return keys, "state = " + p, true
	case "priority":
		// The DB may carry an empty priority (PocketBase select fields store
		// '' until first written), and the serializer renders that as "none",
		// so the "none" bucket must also catch ''. `{:param} = 'none'` is
		// safe: PocketBase substitutes placeholder params with quoted
		// literals before parsing (tools/search/filter.go BuildExpr).
		for _, v := range priorityGroupOrder {
			keys = append(keys, serializers.GroupKey{ID: v, Match: v})
		}
		return keys, "(priority = " + p + " || (" + p + " = 'none' && priority = ''))", true
	case "state__group":
		for _, g := range stateGroupOrder {
			keys = append(keys, serializers.GroupKey{ID: g, Match: g})
		}
		return keys, "state.group = " + p, true
	default:
		return nil, "", false
	}
}

// groupedCycleIssues serves the grouped form of GET …/cycle-issues/
// (CycleIssueViewSet.list with group_by/sub_group_by). It returns handled=false
// when the requested grouping is not supported by this port, and the caller
// degrades to the flat envelope instead of erroring.
//
// The envelope is the shared GroupedEnvelope (upstream GroupedOffsetPaginator /
// SubGroupedOffsetPaginator): `results` is an object keyed by group id with
// `{results, total_results}` buckets, every declared group present (empty ones
// too) in display order. With `sub_group_by`, each group's results nests an
// object keyed by subgroup id — cells with zero rows omitted, matching
// upstream __get_field_dict()'s seeding from the subgroup totals.
func (h *Handler) groupedCycleIssues(scope *middleware.Scope, groupBy, subGroupBy, filter string, params dbx.Params, sort string, perPage int, cursor string) (*serializers.GroupedEnvelope, bool, error) {
	groupKeys, groupFilter, ok := issueGroupKeys(h.App, scope, groupBy, "group")
	if !ok {
		return nil, false, nil
	}

	q := serializers.GroupQuery{
		Collection:  "issues",
		Filter:      filter,
		Params:      params,
		Sort:        sort,
		PerPage:     perPage,
		Cursor:      cursor,
		GroupedBy:   groupBy,
		GroupFilter: groupFilter,
		GroupKeys:   groupKeys,
	}

	if subGroupBy != "" {
		subKeys, subFilter, subOK := issueGroupKeys(h.App, scope, subGroupBy, "subgroup")
		// Unsupported sub-grouping also degrades to the flat envelope, per
		// the L-6 contract (never 500, never a half-grouped shape).
		if !subOK {
			return nil, false, nil
		}
		q.SubGroupedBy = subGroupBy
		q.SubGroupKeys = subKeys
		q.SubGroupFilter = subFilter
	}

	env, err := serializers.PaginateGrouped(h.App, q, func(r *core.Record) any {
		return serializers.Issue(h.App, r)
	})
	if err != nil {
		return nil, true, err
	}
	return env, true, nil
}

// errGroupSubGroupEqual is upstream's response body when `group_by` equals
// `sub_group_by` (CycleIssueViewSet.list returns 400 before paginating).
func groupSubGroupEqual(e *core.RequestEvent) error {
	return e.JSON(http.StatusBadRequest, map[string]any{
		"error": "Group by and sub group by cannot have same parameters",
	})
}
