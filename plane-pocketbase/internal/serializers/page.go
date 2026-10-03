package serializers

import (
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
)

// Page mirrors Django's PageSerializer (plane/app/serializers/page.py). The
// serializer's `labels` field is write-only, so it never appears on the wire;
// `label_ids` and `project_ids` are read-side aggregates computed by the view's
// queryset and are resolved here from the `page_labels` / `project_pages`
// through-tables.
//
// `is_favorite` is annotated per request (a UserFavorite lookup for the caller),
// so it is passed in rather than read off the record.
func Page(app core.App, r *core.Record, isFavorite bool) map[string]any {
	if r == nil {
		return nil
	}
	return map[string]any{
		"id":          UUID(r),
		"name":        Text(r, "name"),
		"owned_by":    RelationUUID(app, r, "owned_by"),
		"access":      r.GetInt("access"),
		"color":       Text(r, "color"),
		"parent":      RelationUUID(app, r, "parent"),
		"is_favorite": isFavorite,
		"is_locked":   r.GetBool("is_locked"),
		// Page.archived_at is a DateField in Django, so it is date-only.
		"archived_at": DateOnly(r, "archived_at"),
		"workspace":   RelationUUID(app, r, "workspace"),
		"created_at":  DateTime(r, "created_at"),
		"updated_at":  DateTime(r, "updated_at"),
		"created_by":  RelationUUID(app, r, "created_by"),
		"updated_by":  RelationUUID(app, r, "updated_by"),
		"view_props":  jsonObject(r, "view_props"),
		"logo_props":  jsonObject(r, "logo_props"),
		"label_ids":   pageLabelIDs(app, r),
		"project_ids": pageProjectIDs(app, r),
	}
}

// PageDetail mirrors Django's PageDetailSerializer: PageSerializer plus the
// collaborative-editor HTML payload. `issue_ids` is added by the retrieve view
// (from PageLog) and is not part of the serializer, so it is attached by the
// handler.
func PageDetail(app core.App, r *core.Record, isFavorite bool) map[string]any {
	data := Page(app, r, isFavorite)
	if data == nil {
		return nil
	}
	data["description_html"] = Text(r, "description_html")
	return data
}

// PageVersion mirrors Django's PageVersionSerializer (the list shape).
func PageVersion(app core.App, r *core.Record) map[string]any {
	if r == nil {
		return nil
	}
	return map[string]any{
		"id":            UUID(r),
		"workspace":     RelationUUID(app, r, "workspace"),
		"page":          RelationUUID(app, r, "page"),
		"last_saved_at": DateTime(r, "last_saved_at"),
		"owned_by":      RelationUUID(app, r, "owned_by"),
		"created_at":    DateTime(r, "created_at"),
		"updated_at":    DateTime(r, "updated_at"),
		"created_by":    RelationUUID(app, r, "created_by"),
		"updated_by":    RelationUUID(app, r, "updated_by"),
	}
}

// PageVersionDetail mirrors Django's PageVersionDetailSerializer: the version
// list shape plus the three description payload columns. `description_binary`
// is a BinaryField in Django; PocketBase stores it as a protected file, so the
// stored filename is projected (nil when no binary was ever saved).
func PageVersionDetail(app core.App, r *core.Record) map[string]any {
	data := PageVersion(app, r)
	if data == nil {
		return nil
	}
	data["description_binary"] = NullableText(r, "description_binary")
	data["description_html"] = Text(r, "description_html")
	data["description_json"] = jsonObject(r, "description_json")
	return data
}

// pageLabelIDs resolves the page's labels through `page_labels`, matching
// Django's ArrayAgg over page_labels__label_id.
func pageLabelIDs(app core.App, page *core.Record) []string {
	rows, err := app.FindRecordsByFilter(
		"page_labels",
		"page = {:page} && deleted_at = ''",
		"", 0, 0,
		dbx.Params{"page": page.Id},
	)
	out := make([]string, 0, len(rows))
	if err != nil {
		return out
	}
	seen := map[string]struct{}{}
	for _, row := range rows {
		label, _ := app.FindRecordById("labels", row.GetString("label"))
		if label == nil {
			continue
		}
		id := UUID(label)
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

// pageProjectIDs resolves the projects a page is linked to through
// `project_pages`, matching Django's ArrayAgg over projects__id.
func pageProjectIDs(app core.App, page *core.Record) []string {
	rows, err := app.FindRecordsByFilter(
		"project_pages",
		"page = {:page} && deleted_at = ''",
		"", 0, 0,
		dbx.Params{"page": page.Id},
	)
	out := make([]string, 0, len(rows))
	if err != nil {
		return out
	}
	seen := map[string]struct{}{}
	for _, row := range rows {
		project, _ := app.FindRecordById("projects", row.GetString("project"))
		if project == nil {
			continue
		}
		id := UUID(project)
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

// jsonObject renders a JSON column, defaulting to an empty object. Django
// declares `view_props`/`logo_props`/`description_json` with `default=dict`, so
// an unset value serializes as `{}` rather than null.
func jsonObject(r *core.Record, field string) any {
	if v := r.Get(field); v != nil {
		return v
	}
	return map[string]any{}
}
