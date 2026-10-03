package serializers

import (
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
)

// UUIDField is the column that carries Plane's UUID on every collection.
// PocketBase's own `id` is a 15-char lowercase identifier that cannot hold a
// UUID, so Plane's UUID lives alongside it and is swapped in at the edge.
const UUIDField = "uuid"

// UUID returns the Plane UUID for a record. Every collection created from
// spec/schema-spec.md carries this field, so a missing value means the record
// was created outside the compatibility layer — surface the PocketBase id so
// the failure is at least traceable rather than empty.
func UUID(r *core.Record) string {
	if r == nil {
		return ""
	}
	if u := r.GetString(UUIDField); u != "" {
		return u
	}
	return r.Id
}

// UUIDs maps a record slice to their Plane UUIDs, preserving order.
func UUIDs(records []*core.Record) []string {
	out := make([]string, 0, len(records))
	for _, r := range records {
		out = append(out, UUID(r))
	}
	return out
}

// FindByUUID resolves a Plane UUID to its PocketBase record.
// Returns nil (no error) when the collection has no such record, so callers can
// return Plane's 404 shape without distinguishing "missing" from "not found".
func FindByUUID(app core.App, collection string, uuid string) (*core.Record, error) {
	if uuid == "" {
		return nil, nil
	}
	record, err := app.FindFirstRecordByFilter(
		collection,
		UUIDField+" = {:uuid}",
		dbx.Params{"uuid": uuid},
	)
	if err != nil {
		// PocketBase reports a miss as sql.ErrNoRows; treat that as "absent".
		return nil, nil
	}
	return record, nil
}

// FindByUUIDOrID accepts either a Plane UUID or a raw PocketBase id. Handy for
// route parameters that may arrive from either the UI or internal tooling.
func FindByUUIDOrID(app core.App, collection string, idOrUUID string) (*core.Record, error) {
	if idOrUUID == "" {
		return nil, nil
	}
	if r, err := FindByUUID(app, collection, idOrUUID); err != nil || r != nil {
		return r, err
	}
	r, err := app.FindRecordById(collection, idOrUUID)
	if err != nil {
		return nil, nil
	}
	return r, nil
}

// RelationUUID resolves a single-relation field to the related record's Plane
// UUID. Returns nil when the relation is unset, matching Plane's nullable FKs.
func RelationUUID(app core.App, r *core.Record, field string) any {
	id := r.GetString(field)
	if id == "" {
		return nil
	}
	related, err := app.FindRecordById(relationCollection(app, r, field), id)
	if err != nil || related == nil {
		return nil
	}
	return UUID(related)
}

// RelationUUIDs resolves a multi-relation field to the related records' Plane
// UUIDs. Always returns a non-nil slice: Plane's frontends iterate these
// directly and a null would break rendering.
func RelationUUIDs(app core.App, r *core.Record, field string) []string {
	ids := r.GetStringSlice(field)
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		related, err := app.FindRecordById(relationCollection(app, r, field), id)
		if err != nil || related == nil {
			continue
		}
		out = append(out, UUID(related))
	}
	return out
}

// relationCollection resolves the target collection name of a relation field so
// RelationUUID/RelationUUIDs can look the record up. Falls back to the field
// name, which matches the convention used throughout the schema spec.
func relationCollection(app core.App, r *core.Record, field string) string {
	collection, err := app.FindCollectionByNameOrId(r.Collection().Name)
	if err != nil || collection == nil {
		return field
	}
	for _, f := range collection.Fields {
		rel, ok := f.(*core.RelationField)
		if !ok || rel.GetName() != field {
			continue
		}
		if rel.CollectionId == "" {
			break
		}
		if target, err := app.FindCollectionByNameOrId(rel.CollectionId); err == nil && target != nil {
			return target.Name
		}
	}
	return field
}
