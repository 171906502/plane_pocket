package serializers

import "github.com/pocketbase/pocketbase/core"

// State mirrors Django's StateSerializer. `order` is computed by the view
// (StateViewSet.list) as the position of the state within its group, not a
// stored column, so the handler passes it in.
func State(app core.App, r *core.Record, order float64) map[string]any {
	if r == nil {
		return nil
	}
	return map[string]any{
		"id":           UUID(r),
		"project_id":   RelationUUID(app, r, "project"),
		"workspace_id": RelationUUID(app, r, "workspace"),
		"name":         Text(r, "name"),
		"color":        Text(r, "color"),
		"group":        r.GetString("group"),
		"default":      r.GetBool("default"),
		"description":  Text(r, "description"),
		"sequence":     r.GetFloat("sequence"),
		"order":        order,
	}
}

// StateLite mirrors Django's StateLiteSerializer, used when a state is embedded
// in an issue.
func StateLite(app core.App, r *core.Record) map[string]any {
	if r == nil {
		return nil
	}
	return map[string]any{
		"id":    UUID(r),
		"name":  Text(r, "name"),
		"color": Text(r, "color"),
		"group": r.GetString("group"),
	}
}

// Label mirrors Django's LabelSerializer.
func Label(app core.App, r *core.Record) map[string]any {
	if r == nil {
		return nil
	}
	return map[string]any{
		"id":           UUID(r),
		"parent":       RelationUUID(app, r, "parent"),
		"name":         Text(r, "name"),
		"color":        NullableText(r, "color"),
		"project_id":   RelationUUID(app, r, "project"),
		"workspace_id": RelationUUID(app, r, "workspace"),
		"sort_order":   r.GetFloat("sort_order"),
	}
}
