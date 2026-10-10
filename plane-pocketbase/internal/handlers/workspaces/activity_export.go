package workspaces

import (
	"bytes"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"

	"github.com/plane-pocketbase/plane-pocketbase/internal/middleware"
	"github.com/plane-pocketbase/plane-pocketbase/internal/serializers"
)

// UserActivityExport serves POST
// /api/workspaces/{slug}/user-activity/{user_id}/export/
// (ExportWorkspaceUserActivityEndpoint, plane/app/views/workspace/base.py:339).
//
// It is a CSV *download*, not JSON: the frontend's profile page "Download"
// button (components/profile/activity/download-button.tsx) posts the day it is
// showing and saves the response as a Blob, so the body must be CSV text and the
// headers must carry the filename.
//
// Faithful to Django in five details that are easy to get wrong:
//
//  1. A missing/blank/malformed `date` is a 400 with exactly
//     {"error": "Date is required"} — it does not fall back to "today".
//  2. The day filter is `created_at__date == date`, i.e. a half-open range on the
//     stored timestamp, not a prefix match.
//  3. Four `field` values (comment / vote / reaction / draft) are excluded — the
//     same four the JSON activity feed hides.
//  4. Rows are scoped to projects the *requesting* user is an active member of,
//     so the export can never leak another user's projects.
//  5. Output is csv.QUOTE_ALL plus `sanitize_csv_row`, i.e. every cell quoted and
//     leading formula characters escaped. Go's encoding/csv does neither (it
//     quotes minimally and has no injection guard), so the writer is explicit.
func (h *Handler) UserActivityExport(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Workspace == nil {
		return serializers.NotFound(e, "")
	}
	user := middleware.CurrentUser(e)
	if user == nil {
		return serializers.Unauthorized(e, "Authentication credentials were not provided.")
	}
	target := h.targetMember(e)
	if target == nil {
		return serializers.NotFound(e, "")
	}

	body := map[string]any{}
	if err := e.BindBody(&body); err != nil {
		return serializers.BadRequest(e, "Invalid request body.")
	}
	date := strings.TrimSpace(asString(body["date"]))
	day, err := time.Parse("2006-01-02", date)
	if date == "" || err != nil {
		// Django returns this exact body when `date` is falsy; a malformed date
		// would instead raise inside the ORM, which is not worth reproducing.
		return e.JSON(http.StatusBadRequest, map[string]any{"error": "Date is required"})
	}

	records, err := h.exportActivities(scope, user, target, day)
	if err != nil {
		return err
	}

	rows := make([][]string, 0, len(records)+1)
	rows = append(rows, []string{
		"Actor name", "Issue ID", "Project", "Created at",
		"Updated at", "Action", "Field", "Old value", "New value",
	})
	for _, activity := range records {
		rows = append(rows, h.activityCSVRow(activity))
	}

	var buf bytes.Buffer
	for _, row := range rows {
		for i, cell := range row {
			if i > 0 {
				buf.WriteByte(',')
			}
			buf.WriteString(csvQuote(sanitizeCSVValue(cell)))
		}
		// Python's csv module terminates rows with \r\n.
		buf.WriteString("\r\n")
	}

	e.Response.Header().Set("Content-Type", "text/csv")
	e.Response.Header().Set("Content-Disposition", `attachment; filename="workspace-user-activity.csv"`)
	return e.Blob(http.StatusOK, "text/csv", buf.Bytes())
}

// exportActivities mirrors ExportWorkspaceUserActivityEndpoint's queryset: the
// target user's activity in this workspace on `day`, restricted to projects the
// requester is an active member of, capped at 10 000 rows.
func (h *Handler) exportActivities(
	scope *middleware.Scope, requesting, target *core.Record, day time.Time,
) ([]*core.Record, error) {
	projectIDs := h.activityProjectIDs(scope, requesting, nil)
	if len(projectIDs) == 0 {
		return nil, nil
	}

	// PocketBase stores datetimes as "2006-01-02 15:04:05.000Z", so a half-open
	// string range is both correct and index-friendly.
	const layout = "2006-01-02 15:04:05.000Z"
	params := dbx.Params{
		"workspace": scope.Workspace.Id,
		"actor":     target.Id,
		"start":     day.UTC().Format(layout),
		"end":       day.UTC().AddDate(0, 0, 1).Format(layout),
	}
	clauses := make([]string, 0, len(projectIDs))
	for i, id := range projectIDs {
		key := "proj" + strconv.Itoa(i)
		clauses = append(clauses, "project = {:"+key+"}")
		params[key] = id
	}

	filter := `workspace = {:workspace} && actor = {:actor} && deleted_at = ''
		&& field != 'comment' && field != 'vote' && field != 'reaction' && field != 'draft'
		&& created_at >= {:start} && created_at < {:end}
		&& (` + strings.Join(clauses, " || ") + `)`

	return h.App.FindRecordsByFilter("issue_activities", filter, "created_at", 10000, 0, params)
}

// activityCSVRow renders one activity exactly as Django's row tuple does.
func (h *Handler) activityCSVRow(activity *core.Record) []string {
	actor := ""
	if a, _ := h.App.FindRecordById("users", activity.GetString("actor")); a != nil {
		actor = a.GetString("display_name")
	}

	// Django: f"{project.identifier} - {issue.sequence_id if issue else ''}"
	projectRef, projectName := "", ""
	if project, _ := h.App.FindRecordById("projects", activity.GetString("project")); project != nil {
		projectRef = project.GetString("identifier")
		projectName = project.GetString("name")
	}
	issueRef := ""
	if issue, _ := h.App.FindRecordById("issues", activity.GetString("issue")); issue != nil {
		issueRef = strconv.Itoa(issue.GetInt("sequence_id"))
	}

	return []string{
		actor,
		projectRef + " - " + issueRef,
		projectName,
		djangoDateTime(activity, "created_at"),
		djangoDateTime(activity, "updated_at"),
		activity.GetString("verb"),
		activity.GetString("field"),
		activity.GetString("old_value"),
		activity.GetString("new_value"),
	}
}

// djangoDateTime formats a stored timestamp the way Python's str(datetime)
// renders it in the CSV ("2026-10-10 03:28:25.482028+00:00"), rather than the
// RFC3339 form the JSON serializers use.
func djangoDateTime(r *core.Record, field string) string {
	t := r.GetDateTime(field).Time()
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format("2006-01-02 15:04:05.000000+00:00")
}

// csvQuote mirrors csv.QUOTE_ALL: always wrap in quotes, doubling any inner
// quote.
func csvQuote(v string) string {
	return `"` + strings.ReplaceAll(v, `"`, `""`) + `"`
}

// sanitizeCSVValue mirrors plane/utils/csv_utils.py sanitize_csv_value: a cell
// beginning with a formula trigger is prefixed with an apostrophe so spreadsheet
// applications treat it as text rather than evaluating it
// (https://owasp.org/www-community/attacks/CSV_Injection).
func sanitizeCSVValue(v string) string {
	if v == "" {
		return v
	}
	switch v[0] {
	case '=', '+', '-', '@', '\t', '\r', '\n':
		return "'" + v
	}
	return v
}
