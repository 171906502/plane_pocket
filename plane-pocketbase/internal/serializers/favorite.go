package serializers

import (
	"github.com/pocketbase/pocketbase/core"
)

// UserFavorite mirrors Django's UserFavoriteSerializer
// (plane/app/serializers/favorite.py). The Meta.fields list is explicit, so the
// wire shape is a fixed set of keys — notably `workspace_id`/`project_id`
// (DRF renders the FK attributes as read-only scalars) rather than the bare
// `workspace`/`project` names used elsewhere.
//
// `entity_data` is the nested lite object for the favourited entity, resolved
// through the same entity map as Django: cycle / module / view / page / project
// produce an object, while issue and folder produce null.
func UserFavorite(app core.App, r *core.Record) map[string]any {
	if r == nil {
		return nil
	}
	return map[string]any{
		"id":                UUID(r),
		"entity_type":       Text(r, "entity_type"),
		"entity_identifier": NullableText(r, "entity_identifier"),
		"entity_data":       favoriteEntityData(app, r.GetString("entity_type"), r.GetString("entity_identifier")),
		"name":              NullableText(r, "name"),
		"is_folder":         r.GetBool("is_folder"),
		"sequence":          favoriteSequence(r),
		"parent":            RelationUUID(app, r, "parent"),
		"workspace_id":      RelationUUID(app, r, "workspace"),
		"project_id":        RelationUUID(app, r, "project"),
	}
}

// favoriteEntityData resolves the nested lite payload for a favourite.
func favoriteEntityData(app core.App, entityType, entityIdentifier string) any {
	if entityIdentifier == "" {
		return nil
	}
	switch entityType {
	case "cycle":
		record, _ := FindByUUID(app, "cycles", entityIdentifier)
		return entityLite(app, record)
	case "module":
		record, _ := FindByUUID(app, "modules", entityIdentifier)
		return entityLite(app, record)
	case "view":
		record, _ := FindByUUID(app, "issue_views", entityIdentifier)
		return entityLite(app, record)
	case "page":
		record, _ := FindByUUID(app, "pages", entityIdentifier)
		if record == nil {
			return nil
		}
		return map[string]any{
			"id":         UUID(record),
			"name":       Text(record, "name"),
			"logo_props": jsonObject(record, "logo_props"),
			"project_id": nullableString(firstPageProject(app, record.Id)),
		}
	case "project":
		record, _ := FindByUUID(app, "projects", entityIdentifier)
		if record == nil {
			return nil
		}
		return map[string]any{
			"id":         UUID(record),
			"name":       Text(record, "name"),
			"logo_props": jsonObject(record, "logo_props"),
		}
	}
	// "issue" and "folder" have no lite serializer in Django.
	return nil
}

// entityLite renders the shared `{id, name, logo_props, project_id}` lite shape
// used by the cycle/module/view favourite serializers.
func entityLite(app core.App, record *core.Record) any {
	if record == nil {
		return nil
	}
	return map[string]any{
		"id":         UUID(record),
		"name":       Text(record, "name"),
		"logo_props": jsonObject(record, "logo_props"),
		"project_id": RelationUUID(app, record, "project"),
	}
}

// favoriteSequence renders the FloatField `sequence`, defaulting to 65535.
func favoriteSequence(r *core.Record) any {
	if r.Get("sequence") == nil {
		return float64(65535)
	}
	return r.GetFloat("sequence")
}
