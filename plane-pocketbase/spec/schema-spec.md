# Plane → PocketBase Schema Specification

```
spec:        plane-pocketbase-schema
source:      Plane v1.4.2 Django/PostgreSQL backend
             apps/api/plane/db/models/*.py
             apps/api/plane/db/models/integration/*.py
             apps/api/plane/license/models/instance.py
target:      PocketBase (SQLite) — collections + fields system (v0.23+)
reference:   C:\Users\cao\Desktop\asl-work\plane_pocket\pocketbase-master\core\field_*.go
method:      (1) scripted bulk extraction of every `class X(...)` under db/models and
             license/models; (2) full read of every model file; (3) full read of every
             PocketBase core/field_*.go before asserting any field type or option;
             (4) grep audit of every excluded model against the kept feature code.
purpose:     Authoritative mapping so implementers can write PocketBase migrations and
             the API compatibility layer directly from this document.
```

## 0. How to read this document

* **§1** — Django → PocketBase type mapping, with the non-obvious cases justified.
* **§2** — The UUID primary-key strategy (normative) + the rejected alternative.
* **§3** — One greppable section per model. Search for `### MODEL:` to jump between models.
* **§4** — `EXCLUDED` models and the evidence for each.
* **§5** — M2M / join-table inventory with unique constraints.
* **§6** — Dependency-ordered creation plan.
* **§7** — Sharp edges: Django constructs with no clean PocketBase equivalent.

Conventions used throughout:

| Token | Meaning |
|---|---|
| `A-block` | The 7 standard `BaseModel`/`AuditModel` columns (§3.0). Present on every model unless stated otherwise. |
| `P-block` | `A-block` **plus** `project`, `workspace` (from `ProjectBaseModel`). |
| `W-block` | `A-block` **plus** `workspace`, `project` (from `WorkspaceBaseModel`). |
| `EXCLUDED` | Collection is not created by the migration (§4). |
| `REVIEW` | Kept, but flagged for a decision — see §7. |
| `<n>` in *Django default* | The value the Go layer must apply, because PocketBase fields have no `default` option (§1.3). |

**Field-name safety.** PocketBase rejects field names that are not `^\w+$`, that contain
`_via_`, or that collide with `null`, `true`, `false`, `_rowid_`, `collectionId`,
`collectionName`, `expand` (`core/field.go:234` `DefaultFieldNameValidationRule`).
Every field name used by Plane passes. The only reserved name Plane uses is `id`, which is
the PocketBase primary key itself (§2).

**Collection-name safety.** `core/collection_validate.go:19` requires `^\w+$` and forbids
collisions with internal tables. Every Plane `db_table` is lowercase snake_case and passes.

---

## 1. Type mapping table

### 1.1 PocketBase field types actually available

Verified by reading `core/field_*.go`. The complete set is:

| Type | Go source | SQLite column emitted | Notes |
|---|---|---|---|
| `text` | `field_text.go` | `TEXT DEFAULT '' NOT NULL` (PK: `TEXT PRIMARY KEY DEFAULT ('r'||lower(hex(randomblob(7)))) NOT NULL`) | `Min`, `Max` (default 5000 if 0), `Pattern`, `AutogeneratePattern`, `Required`, `PrimaryKey` |
| `number` | `field_number.go` | `NUMERIC DEFAULT 0 NOT NULL` | `Min`, `Max`, `OnlyInt`, `Required` |
| `bool` | `field_bool.go` | `BOOLEAN DEFAULT FALSE NOT NULL` | `Required` means "must be true" |
| `email` | `field_email.go` | `TEXT DEFAULT '' NOT NULL` | `OnlyDomains`/`ExceptDomains` |
| `url` | `field_url.go` | `TEXT DEFAULT '' NOT NULL` | `OnlyDomains`/`ExceptDomains`; format-validated |
| `date` | `field_date.go` | `TEXT DEFAULT '' NOT NULL` | `Min`, `Max`; stores full `types.DateTime` |
| `autodate` | `field_autodate.go` | `TEXT DEFAULT '' NOT NULL` | `OnCreate`, `OnUpdate`; requires at least one; value not settable via `record.Set()` |
| `select` | `field_select.go` | single: `TEXT DEFAULT '' NOT NULL`; multi: `JSON DEFAULT '[]' NOT NULL` | `Values []string` (required), `MaxSelect` |
| `json` | `field_json.go` | `JSON DEFAULT NULL` | `MaxSize` (default 1 MB). **The only nullable column type.** |
| `file` | `field_file.go` | single: `TEXT DEFAULT '' NOT NULL`; multi: `JSON DEFAULT '[]' NOT NULL` | `MaxSize` (default 5 MB), `MaxSelect`, `MimeTypes`, `Thumbs`, `Protected` |
| `relation` | `field_relation.go` | single: `TEXT DEFAULT '' NOT NULL`; multi: `JSON DEFAULT '[]' NOT NULL` | `CollectionId` (required), `CascadeDelete`, `MinSelect`, `MaxSelect`, `Required` |
| `editor` | `field_editor.go` | `TEXT DEFAULT '' NOT NULL` | HTML text; `MaxSize` (default 5 MB), `ConvertURLs` (UI hint only — **no server-side sanitisation**) |
| `geoPoint` | `field_geo_point.go` | `JSON DEFAULT NULL` | unused by Plane |
| `password` | `field_password.go` | `TEXT DEFAULT '' NOT NULL` | auth collections only; unused as a mapping target |

**Two facts that drive most of this document:**

1. `core/collection_model.go:957` `initIdField()` creates the `id` field as
   `TextField{System:true, PrimaryKey:true, Required:true, Min:15, Max:15,
   Pattern:"^[a-z0-9]+$", AutogeneratePattern:"[a-z0-9]{15}"}` — **but when a field named
   `id` is already present in the submitted definition it is kept as-is** (`field.Pattern`
   is only defaulted when empty; `Min`/`Max` are not touched). This is why §2 has both a
   chosen approach and a documented alternative.
2. No PocketBase field type except `json` can hold SQL `NULL`
   (`field_json.go:116` → `JSON DEFAULT NULL`). Every other type is `NOT NULL` with a zero
   default. See §1.3 and §7.

### 1.2 Django → PocketBase mapping

| Django construct | PocketBase | Options to set | Justification / caveat |
|---|---|---|---|
| `UUIDField(primary_key=True)` | `id` (system text PK) **+** `uuid` (`text`) | `uuid`: `required`, unique index, `Max=36`, `Pattern=^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$` | See §2 |
| `AutoField`/`BigAutoField(primary_key=True)` | `id` (native PB 15-char) **+** `legacy_pk` (`number`, `OnlyInt`) | `legacy_pk`: unique index | Only `ProjectIdentifier` (§3.3) |
| `CharField(primary_key=True)` | `id` (text PK, custom `Pattern`/`Max`) **or** a separate unique `text` field | see §2.4 | Only `Session.session_key` (§3.1) |
| `CharField(max_length=n)` | `text` | `Max=n` | Direct |
| `CharField(max_length=n, choices=[...])` | `select` | `Values=[...]`, `MaxSelect=1` | Direct: single-value enumeration |
| `SlugField(max_length=n)` | `text` | `Max=n`, `Pattern=^[-a-zA-Z0-9_]+$` | Direct |
| `TextField()` | `text` | **`Max` must be raised** (PB default cap is 5000) | See §7 |
| `TextField(choices=[...])` | `select` | `Values=[...]` | Direct |
| `TextField()` storing HTML (`*_html`) | `editor` | `MaxSize` (default 5 MB) | `editor` is `TEXT` + a UI hint; **no** server-side sanitisation (`field_editor.go:126`), so stored HTML is byte-preserved |
| `IntegerField` | `number` | `OnlyInt=true`, `Min`/`Max` from validators | Direct |
| `PositiveSmallIntegerField(choices=[(int, str)])` | `number` | `OnlyInt=true`, `Min=0` | **Not** `select` — see below |
| `PositiveIntegerField` / `PositiveBigIntegerField` | `number` | `OnlyInt=true`, `Min=0` | Direct |
| `BigIntegerField` | `number` | `OnlyInt=true` | GitHub sync ids (excluded) |
| `FloatField` | `number` | — | Direct |
| `DecimalField` | `number` | — | Not used by Plane |
| `BooleanField` | `bool` | — | Direct |
| `DateTimeField(auto_now_add=True)` | `autodate` | `OnCreate=true` | See §1.3 (import path) |
| `DateTimeField(auto_now=True)` | `autodate` | `OnUpdate=true` | See §1.3 (import path) |
| `DateTimeField()` | `date` | `Required=false` when `null=True` | Stores full datetime |
| `DateField()` | `date` | `Required=false` when `null=True` | **Date-only in Django, datetime in PB.** Store midnight UTC and truncate on read (§7) |
| `JSONField()` | `json` | `MaxSize` raised above 1 MB where Plane stores rich content | Direct; also the only NULL-capable type |
| `ArrayField(URLField())` / `ArrayField(UUIDField())` | `json` | `MaxSize` | PB has no array type; `json` is the direct equivalent. Length caps (`size=10`) must move to the Go layer |
| `BinaryField()` | **no equivalent** | `file` (single, `Protected=true`) | See §1.3 and §7 |
| `FileField()` | `file` | `MaxSize`, `Protected` | `upload_to` is replaced by PB's `/{collectionId}/{recordId}/{filename}` layout |
| `URLField()` | `url` | — | PB validates URL format; Plane's `TextField`-based URLs stay `text` |
| `EmailField()` | `email` | — | Only `User.email` is an email |
| `GenericIPAddressField()` | `text` | `Pattern` for IPv4/IPv6 | PB has no IP type; `geoPoint` is not applicable |
| `ForeignKey(null=False)` | `relation` | `MaxSelect=1`, `Required=true` | Direct |
| `ForeignKey(null=True)` | `relation` | `MaxSelect=1`, `Required=false` | Direct |
| `ForeignKey(on_delete=CASCADE)` | `relation` | `CascadeDelete=true` | Verified semantics: `record_model.go:1625` deletes referencing records when the target is hard-deleted — matches Django CASCADE |
| `ForeignKey(on_delete=SET_NULL)` | `relation` | `CascadeDelete=false`, `Required=false` | Verified semantics: `record_model.go:1633` unsets the id — matches Django SET_NULL exactly |
| `ForeignKey(on_delete=DO_NOTHING)` | `relation` | `CascadeDelete=false`, `Required=false` + **Go hook** | PB will unset the reference, Django keeps it. Must be re-implemented (§7) |
| `ForeignKey(on_delete=PROTECT)` | — | — | **Not used anywhere in Plane** |
| `OneToOneField()` | `relation` | `MaxSelect=1`, plus a **unique index** | `IssueComment.description`, `ProjectIdentifier.project`, `GithubRepositorySync.repository`, `Profile.user` |
| `ManyToManyField(through=...)` | explicit join collection | — | Plane uses `through=` for **every** M2M, so each is a real model (§5) |
| `models.TextChoices` / `IntegerChoices` | `select` (text) / `number` (int) | `Values=[...]` | See the integer-choices note below |

### 1.3 Non-obvious mappings, justified

**(a) `PositiveSmallIntegerField(choices=)` / `IntegerField(choices=)` → `number`, not `select`.**
PocketBase `select` stores **strings only** (`SelectField.Values []string`). Plane's
integer enumerations are returned by the API as JSON numbers and are compared/sorted
numerically in the Go layer:

| Field | Values |
|---|---|
| `WorkspaceMember.role`, `ProjectMember.role`, `ProjectMemberInvite.role`, `WorkspaceMemberInvite.role`, `InstanceAdmin.role` | `20` Admin / `15` Member / `5` Guest |
| `Project.network` | `0` Secret / `2` Public |
| `Page.access`, `IssueView.access` | `0` Public / `1` Private |
| `IssueVote.vote` | `-1` Down / `1` Up |
| `IntakeIssue.status` | `-2` Pending / `-1` Rejected / `0` Snoozed / `1` Accepted / `2` Duplicate |
| `Integration.network` (excluded) | `1` Private / `2` Public |

Storing these as `select` would force `"20"` and break numeric comparison, sorting and the
API contract. Use `number` with `OnlyInt=true` and enforce the allowed set in the Go layer
(PB has no "enum of numbers" primitive). Document the allowed values in the field `Help`.

**(b) `DateTimeField(auto_now_add=True)` / `auto_now=True` → `autodate`.**
`AutodateField` (`field_autodate.go`) is purpose-built for this. Caveat for the data
import: `AutodateField.FindSetter` returns `noopSetter`, so `record.Set()` is ignored; the
importer must use `record.SetRaw(...)` (the interceptor explicitly allows a manually set
value: *"ignore if a date different from the old one was manually set with SetRaw"*,
`field_autodate.go:174`). `created_at`/`updated_at` must be preserved from PostgreSQL.

**(c) `BinaryField` → `file` (single, `Protected=true`).**
PocketBase has no binary/BLOB field. The four Plane binary columns
(`Issue.description_binary`, `DraftIssue.description_binary`, `Page.description_binary`,
`PageVersion.description_binary`, `IssueDescriptionVersion.description_binary`,
`Description.description_binary`, `DescriptionVersion.description_binary`) hold the Yjs
collaborative-editor document state. Two options:

* **Chosen:** `file`, single select, `Protected=true`, `MaxSize` raised to the document
  limit. PB stores the bytes on disk and the field value is the generated filename, which
  is exactly PB's storage model. Consequence: the API layer must stream the blob and the
  serializer must expose it as the original `description_binary` payload.
* Rejected: base64 in a `text` field — inflates storage 33 %, and PB `text` has a `Max`
  cap that must be raised for every row.

Because PB `file` fields cannot be populated by a plain JSON import, the data migration
must write these blobs through the file API, not through record JSON. Flagged in §7.

**(d) `JSONField` → `json`, and it is the only NULL-capable type.**
`field_json.go:116` emits `JSON DEFAULT NULL`; every other type emits `NOT NULL`. Where
Django distinguishes `NULL` from the zero value, use `json` to preserve the distinction
(see (e)).

**(e) Django `null=True` on a non-`json` column has no exact PB equivalent.**
PB cannot store SQL `NULL` in `text`, `number`, `bool`, `date`, `select`, `relation` or
`file`. Two strategies, applied per field in §3:

* *Default strategy — zero-value-as-NULL:* store the PB zero value (`''`, `0`, `false`,
  zero date, empty relation) and have the API layer translate zero → `null` on read and
  `null` → zero on write. This is correct whenever the zero value is not itself a legal
  domain value.
* *Escalation — use `json`:* required where the zero value **is** legal, so the two cases
  would collide. Confirmed collisions in Plane:
  * `Issue.point` — `IntegerField(null=True)` with `MinValueValidator(0)`; `0` is a legal
    estimate point **and** the "unset" marker. → `json`.
  * `IssueActivity.epoch` — `FloatField(null=True)`; `0.0` is a legal epoch. → `json`.
  * `EstimatePoint.key` — `IntegerField(default=0, MinValueValidator(0))`, not nullable,
    so `number` is fine.
  * `Page.moved_to_page` / `Page.moved_to_project` — `UUIDField(null=True, blank=True)`;
    the empty string is not a legal UUID, so `text` with a UUID pattern is safe.

**(f) `FileAsset.asset` and `IssueAttachment.asset` → `file`.**
`FileAsset.asset` is `FileField(upload_to=get_upload_path, max_length=800)` and
`IssueAttachment.asset` is `FileField(upload_to=get_upload_path)`. PB `file` stores only
the filename and manages the directory itself. The Plane URL builders
(`FileAsset.asset_url`) are computed properties and must move into the API layer.

**(g) `ArrayField(models.UUIDField())` → `json`.**
`IssueVersion.assignees`, `IssueVersion.labels`, `IssueVersion.modules` and
`ExporterHistory.project`. These are denormalised UUID snapshots, **not** relations — keep
them as `json` so they hold Plane UUIDs verbatim (see §2.5).

**(h) `models.TextChoices` → `select`, but only when the Django field actually passes
`choices=`.**
Several Plane enums are declared but never wired to a field and therefore impose **no**
database constraint; those stay `text`:
`IssueRelation.relation_type` (`max_length=20`, default `blocked_by` — the
`IssueRelationChoices` enum is *not* passed as `choices=`),
`Webhook.version`, `User.bot_type`, `WorkspaceMember.company_role`,
`WorkspaceUserLink.title`, `DeployBoard.entity_name`, `PageLog.entity_name`,
`PageLog.entity_type`, `FileAsset.entity_type`, `UserRecentVisit.entity_name`.
Conversely `State.group`, `Module.status`, `Issue.priority`, `IssueComment.access`,
`Estimate.type`, `IntakeIssue.status`, `Project.network`, `Page.access`,
`IssueView.access`, `UserNotificationPreference.*` (bools), `Profile.notification_view_mode`,
`Profile.start_of_the_week`, `WorkspaceUserProperties.navigation_control_preference`,
`Workspace.timezone`/`Project.timezone`/`Cycle.timezone`/`User.user_timezone` (choices from
`pytz.common_timezones`) **do** pass `choices=` and map to `select`.

> For the four `timezone` fields the `Values` list would be ~600 IANA names.
> **Recommendation:** map timezone to `text` with `Max=255` instead of `select`, and
> validate against `timezone.available` in the Go layer. A 600-entry `select` is legal but
> makes the PB admin UI unusable. Flagged as `REVIEW` in §3.

**(i) Django field defaults have no PocketBase counterpart.**
No PB field type exposes a `default` option — the zero value is the default. Every Django
`default=` recorded in §3 (e.g. `Issue.priority="none"`, `sort_order=65535`,
`access=0/1`, `Project.network=2`, `Workspace.timezone="UTC"`,
`WorkspaceMember.role=5`, `get_default_props()` JSON blobs) must be applied by the Go
service layer before insert. The `get_default_*()` JSON factories
(`get_default_props`, `get_default_filters`, `get_default_display_filters`,
`get_default_display_properties`, `get_default_views`, `get_default_preferences`,
`get_default_onboarding`, `get_default_product_tour`, `get_issue_props`) must be ported
verbatim — the exact key sets are part of the API contract.

**(j) Composite and partial unique constraints.**
`Collection.Indexes []string` accepts raw SQLite index DDL, including `UNIQUE` and partial
`WHERE` clauses (`core/collection_model.go:372`, `:637`, `:1052`). Plane's
`UniqueConstraint(fields=[...], condition=Q(deleted_at__isnull=True))` therefore maps
exactly to
`CREATE UNIQUE INDEX "<name>" ON "<table>" (<cols>) WHERE deleted_at = ''`
(because a PB `date` field's "null" is the empty string). The legacy
`unique_together = [..., "deleted_at"]` entries are superseded by these partial indexes
and are **not** recreated.

**(k) `on_delete` mapping summary.**
`CASCADE → CascadeDelete=true`; `SET_NULL → CascadeDelete=false, Required=false`
(PB unsets automatically); `DO_NOTHING → CascadeDelete=false, Required=false` + an explicit
Go hook to restore the keep-the-reference behaviour. Plane uses `DO_NOTHING` twice:
`IssueActivity.issue` and `IssueActivity.issue_comment` — deliberately, so that activity
history survives issue deletion.

---

## 2. UUID primary-key strategy

### 2.1 Problem statement

Plane's `BaseModel` (`apps/api/plane/db/models/base.py:18`) declares
`id = UUIDField(default=uuid.uuid4, unique=True, editable=False, db_index=True, primary_key=True)`.
Every real model except `ProjectIdentifier` and `Session` inherits a 36-character RFC-4122
UUID primary key, and every foreign key, every M2M join row and many denormalised
`entity_identifier` columns carry those UUIDs.

PocketBase's **default** primary key is created by `initIdField()`
(`core/collection_model.go:957`) as a `TextField` with `Min=15, Max=15,
Pattern="^[a-z0-9]+$"`, i.e. a 15-character lowercase-alphanumeric token. A UUID (36 chars,
containing `-`) cannot be stored there.

### 2.2 Chosen approach (normative)

**Keep PocketBase's native `id` untouched, store the Plane UUID in a dedicated unique
`uuid` text field, and translate `uuid ↔ id` in the API compatibility layer.**

* Every collection gets a field `uuid`:
  `{ "name": "uuid", "type": "text", "required": true, "max": 36,
     "pattern": "^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$" }`
* `uuid` carries a **unique index** (see §2.3). This is the authoritative Plane identity.
* PocketBase's `id` remains its own 15-char token. It is an internal join key only.
* Relation fields store PocketBase `id` values (that is the only thing a `relation` field
  can store — `RelationField.CollectionId` resolves against the target collection's `id`).
* The API layer performs the translation in both directions (§2.4).

### 2.3 How it appears in the collection definition

Example — `workspaces` (abridged to the identity columns):

```json
{
  "name": "workspaces",
  "type": "base",
  "fields": [
    { "name": "id",   "type": "text", "system": true, "primaryKey": true, "required": true,
      "min": 15, "max": 15, "pattern": "^[a-z0-9]+$", "autogeneratePattern": "[a-z0-9]{15}" },
    { "name": "uuid", "type": "text", "required": true, "max": 36,
      "pattern": "^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$",
      "help": "Plane UUID primary key (Django workspaces.id)" },
    { "name": "name", "type": "text", "required": true, "max": 80 },
    { "name": "slug", "type": "text", "required": true, "max": 48, "pattern": "^[-a-zA-Z0-9_]+$" },
    { "name": "owner", "type": "relation", "required": true, "collectionId": "<users>",
      "maxSelect": 1, "cascadeDelete": true }
  ],
  "indexes": [
    "CREATE UNIQUE INDEX `idx_workspaces_uuid` ON `workspaces` (`uuid`)",
    "CREATE UNIQUE INDEX `idx_workspaces_slug` ON `workspaces` (`slug`)"
  ]
}
```

Rule: **`uuid` is field index 1 in every collection, immediately after `id`**, so that
codegen and the serializer can rely on a stable position.

### 2.4 How it appears in the serializer

The API contract must be byte-identical to Plane's, i.e. `"id": "<uuid>"`. The Go
serializer is a pure rename plus relation resolution:

```go
// record -> API JSON
func toAPI(rec *core.Record) map[string]any {
    out := map[string]any{}
    // 1. Plane UUID becomes the API "id"
    out["id"] = rec.GetString("uuid")
    // 2. every other scalar is copied by name
    for _, f := range rec.Collection().Fields {
        name := f.GetName()
        if name == "id" || name == "uuid" { continue }
        out[name] = rec.Get(name)
    }
    // 3. relation fields are expanded: PB id -> Plane UUID
    for _, f := range rec.Collection().Fields {
        rel, ok := f.(*core.RelationField)
        if !ok { continue }
        ids := rec.GetStringSlice(rel.Name)          // PB ids
        uuids := resolveUUIDs(rec.Collection().App, rel, ids) // uuid lookup, cached
        if rel.MaxSelect <= 1 {
            if len(uuids) == 0 { out[rel.Name] = nil } else { out[rel.Name] = uuids[0] }
        } else {
            out[rel.Name] = uuids
        }
    }
    return out
}

// API JSON -> record
func fromAPI(rec *core.Record, body map[string]any) error {
    for name, v := range body {
        if name == "id" { // Plane UUID supplied by the client
            rec.Set("uuid", v)
            continue
        }
        f := rec.Collection().Fields.GetByName(name)
        if rel, ok := f.(*core.RelationField); ok {
            rec.Set(rel.Name, resolvePBIds(rec.Collection().App, rel, v)) // UUID -> PB id
            continue
        }
        rec.Set(name, v)
    }
    return nil
}
```

Consequences the implementer must honour:

* Every write path needs a **UUID → PB id** resolver and every read path a **PB id → UUID**
  resolver, both batched and cached (one query per collection per request, not one per row).
* Every collection must therefore expose `uuid` on its list/view API so the frontend and
  the resolver can address records by UUID.
* Join tables are addressed by their own UUID, and their two relation columns are resolved
  independently.

### 2.5 Polymorphic `entity_identifier` columns

Plane stores several *polymorphic* references as a plain `UUIDField` plus an
`entity_name`/`entity_type` discriminator, **not** as a foreign key:
`Notification.entity_identifier`, `PageLog.entity_identifier`, `UserFavorite.entity_identifier`,
`UserRecentVisit.entity_identifier`, `DeployBoard.entity_identifier`,
`FileAsset.entity_identifier` (a `CharField` here), `WebhookLog.webhook` (a bare `UUIDField`),
and the `IssueVersion.*` UUID snapshots.

These map to `text` with the UUID pattern and hold **Plane UUIDs** (not PB ids). They are
resolved by a lookup on the target collection's `uuid` field. This is deliberate and
required — the discriminator can point at any of several collections, which a `relation`
field cannot express. Note the asymmetry to keep straight:

> **Real `relation` fields store PocketBase `id` values. Polymorphic
> `entity_identifier`-style columns store Plane UUIDs.** They are resolved through
> different columns. Never mix the two.

### 2.6 Alternative considered and rejected

A custom primary key *is* technically expressible: `initIdField()` preserves a
pre-supplied `Pattern` and `Max` on an existing `id` field
(`core/collection_model.go:980`), and `TextField.ValidateValue` explicitly anticipates
*"the id could have been inserted manually by migration from another system"*
(`core/field_text.go:194`). `-` is not in `forbiddenPKCharacters` (`field_text.go:34`), so a
UUID would pass. Rejected because:

1. PocketBase uses the record id as a filesystem path segment (`record.BaseFilesPath()`,
   `field_file.go:533`). Overriding the default id weakens a documented invariant.
2. The default id is what PocketBase's own tooling (admin UI, auth, backups, `tokenKey`
   flows) assumes.
3. The brief mandates the separate-field approach.

Recorded here so implementers understand **why** the extra field and the translation layer
exist, and so the decision is not silently revisited.

### 2.7 Special cases

| Model | PK in Django | Mapping |
|---|---|---|
| `ProjectIdentifier` | `BigAutoField` (implicit, `apps/api/plane/db/models/project.py:264` inherits `AuditModel` which defines no `id`) | No UUID exists. PB native `id` **plus** `legacy_pk` (`number`, `OnlyInt`, unique index) holding the original integer so the API can keep returning the integer `id`. Do **not** add a `uuid` field. |
| `Session` | `session_key = CharField(max_length=128, primary_key=True)` | No UUID exists. PB native `id` **plus** `session_key` (`text`, `Max=128`, unique index). The API addresses sessions by `session_key`. Do **not** add a `uuid` field. |
| `User` | UUID, but the collection is an **auth** collection | `uuid` field as usual. The PB auth system fields (`password`, `tokenKey`, `email`, `emailVisibility`, `verified`) are additional system fields, not replacements for `uuid`. |
| `Workspace` | UUID, `slug` is a separate unique business key | `uuid` field as usual; `slug` keeps its own unique index. Note `Workspace.delete()` appends `__<epoch>` to the slug on soft delete, so `slug` must remain mutable. |

---

## 3. Per-model collection specification

### §3.0 Standard blocks

Every model inherits one of three abstract bases. Instead of repeating the same seven
columns in 96 tables, each model section states its base and this section defines the
columns exactly. **This is not an abbreviation of the field list — it is the field list,
factored.**

#### A-block — `BaseModel(AuditModel)` → `TimeAuditModel` + `UserAuditModel` + `SoftDeleteModel`

| # | PB field | PB type | PB options | Django source | null/blank | default | unique |
|---|---|---|---|---|---|---|---|
| 0 | `id` | `text` | `system`, `primaryKey`, `required`, `min=15`, `max=15`, `pattern=^[a-z0-9]+$`, `autogeneratePattern=[a-z0-9]{15}` | `id UUIDField(pk)` | — | autogenerated | PK |
| 1 | `uuid` | `text` | `required`, `max=36`, UUID pattern | `id UUIDField(pk)` | — | — | **unique index** |
| 2 | `created_at` | `autodate` | `onCreate=true` | `created_at DateTimeField(auto_now_add)` | — | now on insert | — |
| 3 | `updated_at` | `autodate` | `onUpdate=true` | `updated_at DateTimeField(auto_now)` | — | now on update | — |
| 4 | `created_by` | `relation`→`users` | `maxSelect=1`, `cascadeDelete=false`, `required=false` | `created_by FK(User, SET_NULL, null=True)` | null | — | — |
| 5 | `updated_by` | `relation`→`users` | `maxSelect=1`, `cascadeDelete=false`, `required=false` | `updated_by FK(User, SET_NULL, null=True)` | null | — | — |
| 6 | `deleted_at` | `date` | `required=false` | `deleted_at DateTimeField(null=True, blank=True)` | null | — | — |

Soft delete: **present**. `deleted_at = ''` means "active". The default Django manager
(`SoftDeletionManager`, `db/mixins.py:56`) filters `deleted_at__isnull=True`; the Go layer
must reproduce that filter on every list query, and reproduce the cascade performed by
`soft_delete_related_objects` (`plane.bgtasks.deletion_task`) in a hook (§7).

Index note: every collection also gets
`CREATE INDEX idx_<table>_deleted_at ON <table> (deleted_at)` because every Plane list
query filters on it.

#### P-block — `ProjectBaseModel(BaseModel)`

`A-block` **plus**, appended after `deleted_at`:

| # | PB field | PB type | PB options | Django source | null/blank | default | unique |
|---|---|---|---|---|---|---|---|
| 7 | `project` | `relation`→`projects` | `maxSelect=1`, `required=true`, `cascadeDelete=true` | `project FK(Project, CASCADE)` | not null | — | — |
| 8 | `workspace` | `relation`→`workspaces` | `maxSelect=1`, `required=true`, `cascadeDelete=true` | `workspace FK(Workspace, CASCADE)` | not null | — | — |

`ProjectBaseModel.save()` (`project.py:187`) force-sets `workspace = project.workspace`.
The Go layer must enforce the same invariant: on insert/update, if `project` is set,
overwrite `workspace` from the project.

#### W-block — `WorkspaceBaseModel(BaseModel)`

`A-block` **plus**, appended after `deleted_at`:

| # | PB field | PB type | PB options | Django source | null/blank | default | unique |
|---|---|---|---|---|---|---|---|
| 7 | `workspace` | `relation`→`workspaces` | `maxSelect=1`, `required=true`, `cascadeDelete=true` | `workspace FK(Workspace, CASCADE)` | not null | — | — |
| 8 | `project` | `relation`→`projects` | `maxSelect=1`, `required=false`, `cascadeDelete=true` | `project FK(Project, CASCADE, null=True)` | null | — | — |

`WorkspaceBaseModel.save()` (`workspace.py:192`) sets `workspace = project.workspace`
when a project is present. Reproduce in the Go layer. A `NULL` `project` means the record
is workspace-scoped (e.g. a workspace-level `Label` or `IssueView`); this is why the
`Label` uniqueness rules are project-conditional (§3.5).

---

### §3.1 Identity, authentication, tokens

#### MODEL: `users` — Django `User`
- **Source**: `db/models/user.py` (`User(AbstractBaseUser, PermissionsMixin)`)
- **Base**: none (Django framework base) · **Soft delete**: **no** (`deleted_at` absent)
- **Collection type**: **`auth`** — this is the only auth collection in the schema.
- **Fields**:

| field | PB type | PB options | Django source | null/blank | default | unique |
|---|---|---|---|---|---|---|
| `uuid` | `text` | required, max 36, UUID pattern | `id UUIDField(pk)` | — | — | **unique** |
| `username` | `text` | required, max 128 | `username CharField(128)` | — | — | **unique** |
| `mobile_number` | `text` | max 255 | `mobile_number CharField(255)` | null, blank | — | — |
| `email` | `email` | **system auth field** | `email CharField(255, unique, null=True)` | null | — | **unique** (PB system index, `WHERE email != ''`) |
| `display_name` | `text` | max 255 | `display_name CharField(255)` | — | `<n>` `""`; save() falls back to the email local-part | — |
| `first_name` | `text` | max 255 | `first_name CharField(255)` | blank | `""` | — |
| `last_name` | `text` | max 255 | `last_name CharField(255)` | blank | `""` | — |
| `avatar` | `text` | max 1 000 000 | `avatar TextField(blank)` | blank | `""` | — |
| `avatar_asset` | `relation`→`file_assets` | `maxSelect=1`, `cascadeDelete=false`, `required=false` | `avatar_asset FK(FileAsset, SET_NULL, null=True)` | null | — | — |
| `cover_image` | `url` | max 800 (PB `url` has no length option; enforce in Go) | `cover_image URLField(max_length=800)` | null, blank | — | — |
| `cover_image_asset` | `relation`→`file_assets` | `maxSelect=1`, `cascadeDelete=false`, `required=false` | `cover_image_asset FK(FileAsset, SET_NULL, null=True)` | null | — | — |
| `date_joined` | `autodate` | `onCreate=true` | `date_joined DateTimeField(auto_now_add)` | — | now | — |
| `created_at` | `autodate` | `onCreate=true` | `created_at DateTimeField(auto_now_add)` | — | now | — |
| `updated_at` | `autodate` | `onUpdate=true` | `updated_at DateTimeField(auto_now)` | — | now | — |
| `last_location` | `text` | max 255 | `last_location CharField(255)` | blank | `""` | — |
| `created_location` | `text` | max 255 | `created_location CharField(255)` | blank | `""` | — |
| `is_superuser` | `bool` | — | `is_superuser BooleanField` | — | `false` | — |
| `is_managed` | `bool` | — | `is_managed BooleanField` | — | `false` | — |
| `is_password_expired` | `bool` | — | `is_password_expired BooleanField` | — | `false` | — |
| `is_active` | `bool` | — | `is_active BooleanField` | — | `true` | — |
| `is_staff` | `bool` | — | `is_staff BooleanField` | — | `false` | — |
| `is_email_verified` | `bool` | — | `is_email_verified BooleanField` | — | `false` | — |
| `is_password_autoset` | `bool` | — | `is_password_autoset BooleanField` | — | `false` | — |
| `is_password_reset_required` | `bool` | — | `is_password_reset_required BooleanField` | — | `false` | — |
| `token` | `text` | max 64 | `token CharField(64)` | blank | `""`; regenerated when `token_updated_at` is set | — |
| `last_active` | `date` | required | `last_active DateTimeField(default=now, null=True)` | null | `<n>` now | — |
| `last_login_time` | `date` | required=false | `last_login_time DateTimeField(null=True)` | null | — | — |
| `last_logout_time` | `date` | required=false | `last_logout_time DateTimeField(null=True)` | null | — | — |
| `last_login_ip` | `text` | max 255 | `last_login_ip CharField(255)` | blank | `""` | — |
| `last_logout_ip` | `text` | max 255 | `last_logout_ip CharField(255)` | blank | `""` | — |
| `last_login_medium` | `text` | max 20 | `last_login_medium CharField(20)` | — | `<n>` `"email"` | — |
| `last_login_uagent` | `text` | max 1 000 000 | `last_login_uagent TextField(blank)` | blank | `""` | — |
| `token_updated_at` | `date` | required=false | `token_updated_at DateTimeField(null=True)` | null | — | — |
| `is_bot` | `bool` | — | `is_bot BooleanField` | — | `false` | — |
| `bot_type` | `text` | max 30 | `bot_type CharField(30)` | null, blank | — | — |
| `user_timezone` | `text` | max 255 (**`REVIEW`**: Django has `choices=pytz.common_timezones`; see §1.3(h) — use `text`, not a 600-value `select`) | `user_timezone CharField(255, choices, default="UTC")` | — | `<n>` `"UTC"` | — |
| `is_email_valid` | `bool` | — | `is_email_valid BooleanField` | — | `false` | — |
| `masked_at` | `date` | required=false | `masked_at DateTimeField(null=True)` | null | — | — |

- **PB system auth fields** (added automatically by `initDefaultFields()`,
  `collection_model.go:945`): `password` (hidden, system, min 8), `tokenKey` (hidden,
  system, unique index), `email` (system — mapped above), `emailVisibility` (system bool),
  `verified` (system bool). **Recommendation:** bind `verified` to
  `is_email_verified` semantics but keep `is_email_verified` as its own field so the API
  response shape is unchanged, and leave `emailVisibility = false`.
- **Dropped Django framework columns**: `last_login` (`AbstractBaseUser`) is superseded by
  PB's auth `lastLogin` bookkeeping — Plane's own `last_login_time` is authoritative and is
  kept. `PermissionsMixin.groups` and `.user_permissions` are Django auth M2M tables
  (`auth_user_groups`, `auth_user_user_permissions`) and are **not migrated** (§4.1).
  `password` keeps Django's hasher format in PostgreSQL; PB uses bcrypt/argon2 — see §7.
- **Notes**: `User.save()` lowercases/trims `email`, auto-derives `display_name`, and sets
  `is_staff = true` when `is_superuser`. Port all three to the Go layer. Bot users
  (`is_bot=true, bot_type=WORKSPACE_SEED`, created by `bgtasks/workspace_seed_task.py:526`)
  always receive a synthetic email, so the PB `email` field is always populated.

#### MODEL: `profiles` — Django `Profile`
- **Source**: `db/models/user.py` (`Profile(TimeAuditModel)`)
- **Base**: **neither A/P/W** — `TimeAuditModel` only. Fields: `id`(UUID PK), `created_at`, `updated_at`. **No** `created_by`, `updated_by`, `deleted_at`.
- **Fields**:

| field | PB type | PB options | Django source | null/blank | default | unique |
|---|---|---|---|---|---|---|
| `uuid` | `text` | required, max 36, UUID pattern | `id UUIDField(pk)` | — | — | **unique** |
| `created_at` | `autodate` | `onCreate=true` | `created_at` | — | now | — |
| `updated_at` | `autodate` | `onUpdate=true` | `updated_at` | — | now | — |
| `user` | `relation`→`users` | `maxSelect=1`, `required=true`, `cascadeDelete=true` | `user OneToOneField(User, CASCADE)` | not null | — | **unique index** |
| `theme` | `json` | `maxSize` 1 MB | `theme JSONField(default=dict)` | — | `<n>` `{}` | — |
| `is_app_rail_docked` | `bool` | — | `is_app_rail_docked` | — | `true` | — |
| `is_tour_completed` | `bool` | — | `is_tour_completed` | — | `false` | — |
| `onboarding_step` | `json` | — | `onboarding_step JSONField(default=get_default_onboarding)` | — | `<n>` `{"profile_complete":false,"workspace_create":false,"workspace_invite":false,"workspace_join":false}` | — |
| `use_case` | `text` | max 1 000 000 | `use_case TextField(blank, null)` | null, blank | — | — |
| `role` | `text` | max 300 | `role CharField(300)` (job role) | null, blank | — | — |
| `is_onboarded` | `bool` | — | `is_onboarded` | — | `false` | — |
| `last_workspace_id` | `text` | max 36, UUID pattern | `last_workspace_id UUIDField(null=True)` | null | — | — |
| `billing_address_country` | `text` | max 255 | `billing_address_country` | — | `<n>` `"INDIA"` | — |
| `billing_address` | `json` | — | `billing_address JSONField(null=True)` | null | — | — |
| `has_billing_address` | `bool` | — | `has_billing_address` | — | `false` | — |
| `company_name` | `text` | max 255 | `company_name` | blank | `""` | — |
| `notification_view_mode` | `select` | `values=["full","compact"]`, `maxSelect=1` | `notification_view_mode CharField(255, choices)` | — | `<n>` `"full"` | — |
| `is_smooth_cursor_enabled` | `bool` | — | `is_smooth_cursor_enabled` | — | `false` | — |
| `is_mobile_onboarded` | `bool` | — | `is_mobile_onboarded` | — | `false` | — |
| `mobile_onboarding_step` | `json` | — | `mobile_onboarding_step` | — | `<n>` `{"profile_complete":false,"workspace_create":false,"workspace_join":false}` | — |
| `mobile_timezone_auto_set` | `bool` | — | `mobile_timezone_auto_set` | — | `false` | — |
| `language` | `text` | max 255 | `language` | — | `<n>` `"en"` | — |
| `start_of_the_week` | `number` | `onlyInt=true`, `min=0`, `max=6` | `start_of_the_week PositiveSmallIntegerField(choices, default=0)` | — | `<n>` `0` (Sunday) | — |
| `goals` | `json` | — | `goals JSONField(default=dict)` | — | `<n>` `{}` | — |
| `background_color` | `text` | max 255 | `background_color` | — | `<n>` random hex from `get_random_color()` | — |
| `is_navigation_tour_completed` | `bool` | — | `is_navigation_tour_completed` | — | `false` | — |
| `has_marketing_email_consent` | `bool` | — | `has_marketing_email_consent` | — | `false` | — |
| `is_subscribed_to_changelog` | `bool` | — | `is_subscribed_to_changelog` | — | `false` | — |
| `product_tour` | `json` | — | `product_tour` | — | `<n>` `{"work_items":false,"cycles":false,"modules":false,"intake":false,"pages":false}` | — |

- **Indexes**: `CREATE UNIQUE INDEX idx_profiles_user ON profiles (user)`.

#### MODEL: `accounts` — Django `Account`
- **Source**: `db/models/user.py` (`Account(TimeAuditModel)`)
- **Base**: `TimeAuditModel` only — `id`(UUID), `created_at`, `updated_at`; no audit-user / soft-delete columns.
- **Kept?** Yes — `REVIEW`. Referenced by `plane/app/views/user/base.py:399` (`AccountEndpoint`, list/get/delete the caller's connected accounts) and `plane/authentication/adapter/oauth.py:104,120`. The api-spec lists `workspace.accounts.list` as frontend-critical, so the collection is retained; only the OAuth *provider handshake* endpoints are excluded. If OAuth login is later dropped, this collection can be dropped with it.
- **Fields**:

| field | PB type | PB options | Django source | null/blank | default | unique |
|---|---|---|---|---|---|---|
| `uuid` | `text` | required, UUID pattern | `id UUIDField(pk)` | — | — | **unique** |
| `created_at` / `updated_at` | `autodate` | `onCreate` / `onUpdate` | — | — | now | — |
| `user` | `relation`→`users` | `maxSelect=1`, `required=true`, `cascadeDelete=true` | `user FK(User, CASCADE)` | not null | — | — |
| `provider_account_id` | `text` | max 255 | `provider_account_id CharField(255)` | not null | — | part of unique index |
| `provider` | `select` | `values=["google","github","gitlab"]`, `maxSelect=1` | `provider CharField(choices)` | not null | — | part of unique index |
| `access_token` | `text` | max 1 000 000 | `access_token TextField()` | not null | — | — |
| `access_token_expired_at` | `date` | required=false | `access_token_expired_at DateTimeField(null=True)` | null | — | — |
| `refresh_token` | `text` | max 1 000 000 | `refresh_token TextField(null=True, blank=True)` | null, blank | — | — |
| `refresh_token_expired_at` | `date` | required=false | `refresh_token_expired_at DateTimeField(null=True)` | null | — | — |
| `last_connected_at` | `date` | required | `last_connected_at DateTimeField(default=now)` | — | `<n>` now | — |
| `id_token` | `text` | max 1 000 000 | `id_token TextField(blank)` | blank | `""` | — |
| `metadata` | `json` | — | `metadata JSONField(default=dict)` | — | `<n>` `{}` | — |

- **Indexes**: `CREATE UNIQUE INDEX idx_accounts_provider_account ON accounts (provider, provider_account_id)` (Django `unique_together = ["provider","provider_account_id"]`).

#### MODEL: `sessions` — Django `Session`
- **Source**: `db/models/session.py` (`Session(AbstractBaseSession)`)
- **Base**: none. **No UUID, no audit columns, no `deleted_at`.**
- **Fields** (Django `AbstractBaseSession` provides `session_key`, `session_data`, `expire_date`; the model overrides `session_key` and `user_id` and adds `device_info`):

| field | PB type | PB options | Django source | null/blank | default | unique |
|---|---|---|---|---|---|---|
| `session_key` | `text` | `required`, `max=128`, `pattern=^[a-z0-9]+$` | `session_key CharField(128, primary_key=True)` | — | generated by `SessionStore._get_new_session_key` (128 chars of `[a-z0-9]`) | **unique index** |
| `session_data` | `text` | `max` 1 000 000 | `session_data TextField()` | not null | — | — |
| `expire_date` | `date` | `required=true` | `expire_date DateTimeField(db_index=True)` | not null | — | — |
| `device_info` | `json` | — | `device_info JSONField(null=True, blank=True, default=None)` | null | — | — |
| `user_id` | `text` | `max=50` | `user_id CharField(null=True, max_length=50, db_index=True)` | null | — | — |

- **No `uuid` field** — the Django model has no UUID. The API addresses sessions by
  `session_key`; the Go serializer maps `session_key → id` for this collection only.
- **Indexes**: `CREATE UNIQUE INDEX idx_sessions_session_key ON sessions (session_key)` and
  `CREATE INDEX idx_sessions_user_id ON sessions (user_id)`.
- **`REVIEW`**: this is Django's server-side session store. PocketBase has its own
  auth-token model, so the collection is likely superseded. It is **kept** here because
  `plane/app/views/user/base.py:316` calls
  `Session.objects.filter(user_id=request.user.id).delete()` on sign-out, i.e. a kept
  feature reads it. If sign-out is reimplemented as token invalidation, this collection can
  be dropped. **Decision required** — do not silently omit it.

#### MODEL: `api_tokens` — Django `APIToken`
- **Source**: `db/models/api.py` (`APIToken(BaseModel)`)
- **Base**: `A-block`
- **Fields**:

| field | PB type | PB options | Django source | null/blank | default | unique |
|---|---|---|---|---|---|---|
| `label` | `text` | max 255 | `label CharField(255)` | — | `<n>` `uuid4().hex` | — |
| `description` | `text` | max 1 000 000 | `description TextField(blank)` | blank | `""` | — |
| `is_active` | `bool` | — | `is_active` | — | `true` | — |
| `last_used` | `date` | required=false | `last_used DateTimeField(null=True)` | null | — | — |
| `token` | `text` | required, max 255 | `token CharField(255, unique, db_index)` | not null | `<n>` `"plane_api_" + uuid4().hex` | **unique index** |
| `user` | `relation`→`users` | `maxSelect=1`, `required=true`, `cascadeDelete=true` | `user FK(User, CASCADE)` | not null | — | — |
| `user_type` | `number` | `onlyInt=true`, `min=0`, `max=1` | `user_type PositiveSmallIntegerField(choices 0/1)` | — | `<n>` `0` (Human) | — |
| `workspace` | `relation`→`workspaces` | `maxSelect=1`, `required=false`, `cascadeDelete=true` | `workspace FK(Workspace, CASCADE, null=True)` | null | — | — |
| `expired_at` | `date` | required=false | `expired_at DateTimeField(blank, null=True)` | null, blank | — | — |
| `is_service` | `bool` | — | `is_service` | — | `false` | — |
| `allowed_rate_limit` | `text` | max 255 | `allowed_rate_limit CharField(255)` | — | `<n>` `"60/min"` | — |

- **Indexes**: unique on `token`.

#### MODEL: `api_activity_logs` — Django `APIActivityLog`
- **Source**: `db/models/api.py` (`APIActivityLog(BaseModel)`)
- **Base**: `A-block`
- **Fields**:

| field | PB type | PB options | Django source | null/blank | default | unique |
|---|---|---|---|---|---|---|
| `token_identifier` | `text` | max 255 | `token_identifier CharField(255)` | not null | — | — |
| `path` | `text` | max 255 | `path CharField(255)` | not null | — | — |
| `method` | `text` | max 10 | `method CharField(10)` | not null | — | — |
| `query_params` | `text` | max 1 000 000 | `query_params TextField(null=True, blank=True)` | null, blank | — | — |
| `headers` | `text` | max 1 000 000 | `headers TextField(null=True, blank=True)` | null, blank | — | — |
| `body` | `text` | max 1 000 000 | `body TextField(null=True, blank=True)` | null, blank | — | — |
| `response_code` | `number` | `onlyInt=true`, `min=0` | `response_code PositiveIntegerField()` | not null | — | — |
| `response_body` | `text` | max 1 000 000 | `response_body TextField(null=True, blank=True)` | null, blank | — | — |
| `ip_address` | `text` | max 45, IPv4/IPv6 pattern | `ip_address GenericIPAddressField(null=True, blank=True)` | null, blank | — | — |
| `user_agent` | `text` | max 512 | `user_agent CharField(512, null=True, blank=True)` | null, blank | — | — |

---

### §3.2 Workspace

#### MODEL: `workspaces` — Django `Workspace`
- **Source**: `db/models/workspace.py` (`Workspace(BaseModel)`)
- **Base**: `A-block`
- **Fields**:

| field | PB type | PB options | Django source | null/blank | default | unique |
|---|---|---|---|---|---|---|
| `name` | `text` | required, max 80 | `name CharField(80)` | not null | — | — |
| `logo` | `text` | max 1 000 000 | `logo TextField(blank, null)` | null, blank | — | — |
| `logo_asset` | `relation`→`file_assets` | `maxSelect=1`, `required=false`, `cascadeDelete=false` | `logo_asset FK(FileAsset, SET_NULL, null=True)` | null | — | — |
| `owner` | `relation`→`users` | `maxSelect=1`, `required=true`, `cascadeDelete=true` | `owner FK(User, CASCADE)` | not null | — | — |
| `slug` | `text` | required, max 48, slug pattern | `slug SlugField(48, unique, db_index, validators=[slug_validator])` | not null | — | **unique index** |
| `organization_size` | `text` | max 20 | `organization_size CharField(20)` | null, blank | — | — |
| `timezone` | `text` | max 255 (**`REVIEW`**: Django `choices=pytz.common_timezones`; use `text`, see §1.3(h)) | `timezone CharField(255, choices, default="UTC")` | — | `<n>` `"UTC"` | — |
| `background_color` | `text` | max 255 | `background_color CharField(255)` | — | `<n>` random hex | — |

- **Indexes**: unique on `slug`.
- **Notes**: `slug_validator` rejects `RESTRICTED_WORKSPACE_SLUGS`
  (`plane/utils/constants.py`) — enforce in the Go layer. `Workspace.delete()`
  (`workspace.py:156`) rewrites `slug` to `<slug>__<epoch>` after a soft delete so the
  original slug can be reused; the Go soft-delete hook must replicate this.

#### MODEL: `workspace_members` — Django `WorkspaceMember`
- **Source**: `db/models/workspace.py` (`WorkspaceMember(BaseModel)`)
- **Base**: `A-block`
- **Fields**:

| field | PB type | PB options | Django source | null/blank | default | unique |
|---|---|---|---|---|---|---|
| `workspace` | `relation`→`workspaces` | `maxSelect=1`, `required=true`, `cascadeDelete=true` | `workspace FK(Workspace, CASCADE)` | not null | — | — |
| `member` | `relation`→`users` | `maxSelect=1`, `required=true`, `cascadeDelete=true` | `member FK(User, CASCADE)` | not null | — | — |
| `role` | `number` | `onlyInt=true`, `min=0` | `role PositiveSmallIntegerField(choices 20/15/5, default=5)` | — | `<n>` `5` (Guest) | — |
| `company_role` | `text` | max 1 000 000 | `company_role TextField(null=True, blank=True)` | null, blank | — | — |
| `view_props` | `json` | — | `view_props JSONField(default=get_default_props)` | — | `<n>` `get_default_props()` | — |
| `default_props` | `json` | — | `default_props JSONField(default=get_default_props)` | — | `<n>` `get_default_props()` | — |
| `issue_props` | `json` | — | `issue_props JSONField(default=get_issue_props)` | — | `<n>` `{"subscribed":true,"assigned":true,"created":true,"all_issues":true}` | — |
| `is_active` | `bool` | — | `is_active` | — | `true` | — |
| `getting_started_checklist` | `json` | — | `default=dict` | — | `<n>` `{}` | — |
| `tips` | `json` | — | `default=dict` | — | `<n>` `{}` | — |
| `explored_features` | `json` | — | `default=dict` | — | `<n>` `{}` | — |

- **Indexes / constraints** (partial unique — see §1.3(j)):
  `CREATE UNIQUE INDEX workspace_member_unique_workspace_member_when_deleted_at_null ON workspace_members (workspace, member) WHERE deleted_at = ''`
  (`UniqueConstraint(fields=["workspace","member"], condition=Q(deleted_at__isnull=True))`).
  The legacy `unique_together = ["workspace","member","deleted_at"]` is **not** recreated.
- **Join-table role**: yes — the workspace↔user membership edge (§5).

#### MODEL: `workspace_member_invites` — Django `WorkspaceMemberInvite`
- **Source**: `db/models/workspace.py` (`WorkspaceMemberInvite(BaseModel)`)
- **Base**: `A-block`
- **Fields**:

| field | PB type | PB options | Django source | null/blank | default | unique |
|---|---|---|---|---|---|---|
| `workspace` | `relation`→`workspaces` | `required=true`, `cascadeDelete=true` | `workspace FK(Workspace, CASCADE)` | not null | — | — |
| `email` | `text` | required, max 255 | `email CharField(255)` | not null | — | part of partial unique |
| `accepted` | `bool` | — | `accepted` | — | `false` | — |
| `token` | `text` | required, max 255 | `token CharField(255)` | not null | — | — |
| `message` | `text` | max 1 000 000 | `message TextField(null=True)` | null | — | — |
| `responded_at` | `date` | required=false | `responded_at DateTimeField(null=True)` | null | — | — |
| `role` | `number` | `onlyInt=true` | `role PositiveSmallIntegerField(choices, default=5)` | — | `<n>` `5` | — |

- **Indexes**: `CREATE UNIQUE INDEX workspace_member_invite_unique_email_workspace_when_deleted_at_null ON workspace_member_invites (email, workspace) WHERE deleted_at = ''`.

#### MODEL: `teams` — Django `Team`
- **Source**: `db/models/workspace.py` (`Team(BaseModel)`)
- **Base**: `A-block`
- **Fields**:

| field | PB type | PB options | Django source | null/blank | default | unique |
|---|---|---|---|---|---|---|
| `name` | `text` | required, max 255 | `name CharField(255)` | not null | — | part of partial unique |
| `description` | `text` | max 1 000 000 | `description TextField(blank)` | blank | `""` | — |
| `workspace` | `relation`→`workspaces` | `required=true`, `cascadeDelete=true` | `workspace FK(Workspace, CASCADE)` | not null | — | — |
| `logo_props` | `json` | — | `logo_props JSONField(default=dict)` | — | `<n>` `{}` | — |

- **Indexes**: `CREATE UNIQUE INDEX team_unique_name_workspace_when_deleted_at_null ON teams (name, workspace) WHERE deleted_at = ''`.
- **Notes**: `Team` is exported by `db/models/__init__.py` but is not referenced by any
  view or serializer in v1.4.2 (the team feature is dormant). **Kept** because it is a real
  table in the source schema; flagged `REVIEW` — confirm before creating.

#### MODEL: `workspace_themes` — Django `WorkspaceTheme`
- **Source**: `db/models/workspace.py` (`WorkspaceTheme(BaseModel)`)
- **Base**: `A-block`
- **Fields**:

| field | PB type | PB options | Django source | null/blank | default | unique |
|---|---|---|---|---|---|---|
| `workspace` | `relation`→`workspaces` | `required=true`, `cascadeDelete=true` | `workspace FK(Workspace, CASCADE)` | not null | — | — |
| `name` | `text` | required, max 300 | `name CharField(300)` | not null | — | part of partial unique |
| `actor` | `relation`→`users` | `required=true`, `cascadeDelete=true` | `actor FK(User, CASCADE)` | not null | — | — |
| `colors` | `json` | — | `colors JSONField(default=dict)` | — | `<n>` `{}` | — |

- **Indexes**: `CREATE UNIQUE INDEX workspace_theme_unique_workspace_name_when_deleted_at_null ON workspace_themes (workspace, name) WHERE deleted_at = ''`.

#### MODEL: `workspace_user_properties` — Django `WorkspaceUserProperties`
- **Source**: `db/models/workspace.py` (`WorkspaceUserProperties(BaseModel)`)
- **Base**: `A-block`
- **Fields**:

| field | PB type | PB options | Django source | null/blank | default | unique |
|---|---|---|---|---|---|---|
| `workspace` | `relation`→`workspaces` | `required=true`, `cascadeDelete=true` | `workspace FK(Workspace, CASCADE)` | not null | — | — |
| `user` | `relation`→`users` | `required=true`, `cascadeDelete=true` | `user FK(User, CASCADE)` | not null | — | — |
| `filters` | `json` | — | `filters JSONField(default=get_default_filters)` | — | `<n>` `get_default_filters()` | — |
| `display_filters` | `json` | — | `display_filters JSONField(default=get_default_display_filters)` | — | `<n>` `{"display_filters":{...}}` | — |
| `display_properties` | `json` | — | `display_properties JSONField(default=get_default_display_properties)` | — | `<n>` `{"display_properties":{...}}` | — |
| `rich_filters` | `json` | — | `rich_filters JSONField(default=dict)` | — | `<n>` `{}` | — |
| `navigation_project_limit` | `number` | `onlyInt=true` | `navigation_project_limit IntegerField(default=10)` | — | `<n>` `10` | — |
| `navigation_control_preference` | `select` | `values=["ACCORDION","TABBED"]`, `maxSelect=1` | `navigation_control_preference CharField(25, choices)` | — | `<n>` `"ACCORDION"` | — |

- **Indexes**: `CREATE UNIQUE INDEX workspace_user_properties_unique_workspace_user_when_deleted_at_null ON workspace_user_properties (workspace, user) WHERE deleted_at = ''`.
- **Note**: `get_default_filters()` here is `workspace.py`'s own copy (identical key set to
  `issue.py`'s). The `display_filters`/`display_properties` factories in `workspace.py`
  return a **wrapped** object (`{"display_filters": {...}}`), unlike `issue.py`'s which
  return the bare object. Port each factory from its own module — they are not identical.

#### MODEL: `workspace_user_links` — Django `WorkspaceUserLink`
- **Source**: `db/models/workspace.py` (`WorkspaceUserLink(WorkspaceBaseModel)`)
- **Base**: `W-block`
- **Fields**:

| field | PB type | PB options | Django source | null/blank | default | unique |
|---|---|---|---|---|---|---|
| `title` | `text` | max 255 | `title CharField(255, null=True, blank=True)` | null, blank | — | — |
| `url` | `text` | max 1 000 000 (Django `TextField`, **not** `URLField`) | `url TextField()` | not null | — | — |
| `metadata` | `json` | — | `metadata JSONField(default=dict)` | — | `<n>` `{}` | — |
| `owner` | `relation`→`users` | `required=true`, `cascadeDelete=true` | `owner FK(User, CASCADE)` | not null | — | — |

- **Indexes**: none beyond the A/W-block defaults.

#### MODEL: `workspace_home_preferences` — Django `WorkspaceHomePreference`
- **Source**: `db/models/workspace.py` (`WorkspaceHomePreference(BaseModel)`)
- **Base**: `A-block`
- **Fields**:

| field | PB type | PB options | Django source | null/blank | default | unique |
|---|---|---|---|---|---|---|
| `workspace` | `relation`→`workspaces` | `required=true`, `cascadeDelete=true` | `workspace FK(Workspace, CASCADE)` | not null | — | — |
| `user` | `relation`→`users` | `required=true`, `cascadeDelete=true` | `user FK(User, CASCADE)` | not null | — | — |
| `key` | `text` | required, max 255 | `key CharField(255)` | not null | — | part of partial unique |
| `is_enabled` | `bool` | — | `is_enabled` | — | `true` | — |
| `config` | `json` | — | `config JSONField(default=dict)` | — | `<n>` `{}` | — |
| `sort_order` | `number` | — | `sort_order FloatField(default=65535)` | — | `<n>` `65535` | — |

- **Indexes**: `CREATE UNIQUE INDEX workspace_user_home_preferences_unique_workspace_user_key_when_deleted_at_null ON workspace_home_preferences (workspace, user, key) WHERE deleted_at = ''`.
- **Notes**: `HomeWidgetKeys` enumerates `quick_links`, `recents`, `my_stickies`,
  `new_at_plane`, `quick_tutorial` but is **not** passed as `choices=` → `key` stays `text`.

#### MODEL: `workspace_user_preferences` — Django `WorkspaceUserPreference`
- **Source**: `db/models/workspace.py` (`WorkspaceUserPreference(BaseModel)`)
- **Base**: `A-block`
- **Fields**:

| field | PB type | PB options | Django source | null/blank | default | unique |
|---|---|---|---|---|---|---|
| `workspace` | `relation`→`workspaces` | `required=true`, `cascadeDelete=true` | `workspace FK(Workspace, CASCADE)` | not null | — | — |
| `user` | `relation`→`users` | `required=true`, `cascadeDelete=true` | `user FK(User, CASCADE)` | not null | — | — |
| `key` | `text` | required, max 255 | `key CharField(255)` | not null | — | part of partial unique |
| `is_pinned` | `bool` | — | `is_pinned` | — | `false` | — |
| `sort_order` | `number` | — | `sort_order FloatField(default=65535)` | — | `<n>` `65535` | — |

- **Indexes**: `CREATE UNIQUE INDEX workspace_user_preferences_unique_workspace_user_key_when_deleted_at_null ON workspace_user_preferences (workspace, user, key) WHERE deleted_at = ''`.
- **Notes**: `UserPreferenceKeys` (`views`, `active_cycles`, `analytics`, `drafts`,
  `your_work`, `archives`, `stickies`) is **not** wired to `choices=` → `key` stays `text`.

---

### §3.3 Project

#### MODEL: `projects` — Django `Project`
- **Source**: `db/models/project.py` (`Project(BaseModel)`)
- **Base**: `A-block`
- **Fields**:

| field | PB type | PB options | Django source | null/blank | default | unique |
|---|---|---|---|---|---|---|
| `name` | `text` | required, max 255 | `name CharField(255)` | not null | — | part of partial unique |
| `description` | `text` | max 1 000 000 | `description TextField(blank)` | blank | `""` | — |
| `description_text` | `json` | — | `description_text JSONField(blank, null)` | null, blank | — | — |
| `description_html` | `json` | — | `description_html JSONField(blank, null)` | null, blank | — | — |
| `network` | `number` | `onlyInt=true`, `min=0`, `max=2` | `network PositiveSmallIntegerField(choices 0/2, default=2)` | — | `<n>` `2` (Public) | — |
| `workspace` | `relation`→`workspaces` | `required=true`, `cascadeDelete=true` | `workspace FK(Workspace, CASCADE)` | not null | — | — |
| `identifier` | `text` | required, max 12 | `identifier CharField(12, db_index)` | not null | — | part of partial unique; `save()` upper-cases and strips |
| `default_assignee` | `relation`→`users` | `required=false`, `cascadeDelete=true` | `default_assignee FK(User, CASCADE, null=True)` | null, blank | — | — |
| `project_lead` | `relation`→`users` | `required=false`, `cascadeDelete=true` | `project_lead FK(User, CASCADE, null=True)` | null, blank | — | — |
| `emoji` | `text` | max 255 | `emoji CharField(255, null, blank)` | null, blank | — | — |
| `icon_prop` | `json` | — | `icon_prop JSONField(null=True)` | null | — | — |
| `module_view` | `bool` | — | `module_view` | — | `false` | — |
| `cycle_view` | `bool` | — | `cycle_view` | — | `false` | — |
| `issue_views_view` | `bool` | — | `issue_views_view` | — | `false` | — |
| `page_view` | `bool` | — | `page_view` | — | `true` | — |
| `intake_view` | `bool` | — | `intake_view` | — | `false` | — |
| `is_time_tracking_enabled` | `bool` | — | — | — | `false` | — |
| `is_issue_type_enabled` | `bool` | — | — | — | `false` | — |
| `guest_view_all_features` | `bool` | — | — | — | `false` | — |
| `cover_image` | `text` | max 1 000 000 | `cover_image TextField(blank, null)` | null, blank | — | — |
| `cover_image_asset` | `relation`→`file_assets` | `required=false`, `cascadeDelete=false` | `cover_image_asset FK(FileAsset, SET_NULL, null=True)` | null, blank | — | — |
| `estimate` | `relation`→`estimates` | `required=false`, `cascadeDelete=false` | `estimate FK(Estimate, SET_NULL, null=True)` | null | — | — |
| `archive_in` | `number` | `onlyInt=true`, `min=0`, `max=12` | `archive_in IntegerField(default=0, validators 0..12)` | — | `<n>` `0` | — |
| `close_in` | `number` | `onlyInt=true`, `min=0`, `max=12` | `close_in IntegerField(default=0, validators 0..12)` | — | `<n>` `0` | — |
| `logo_props` | `json` | — | `logo_props JSONField(default=dict)` | — | `<n>` `{}` | — |
| `default_state` | `relation`→`states` | `required=false`, `cascadeDelete=false` | `default_state FK(State, SET_NULL, null=True)` | null | — | — |
| `archived_at` | `date` | required=false | `archived_at DateTimeField(null=True)` | null | — | — |
| `timezone` | `text` | max 255 (**`REVIEW`**, §1.3(h)) | `timezone CharField(255, choices, default="UTC")` | — | `<n>` inherited from the workspace on create | — |
| `external_source` | `text` | max 255 | `external_source CharField(255, null, blank)` | null, blank | — | — |
| `external_id` | `text` | max 255 | `external_id CharField(255, blank, null)` | null, blank | — | — |

- **Indexes**:
  `CREATE UNIQUE INDEX project_unique_identifier_workspace_when_deleted_at_null ON projects (identifier, workspace) WHERE deleted_at = ''`
  `CREATE UNIQUE INDEX project_unique_name_workspace_when_deleted_at_null ON projects (name, workspace) WHERE deleted_at = ''`
  plus a plain index on `identifier` (Django `db_index=True`).
- **Notes**: `Project.save()` (`project.py:167`) strips/upper-cases `identifier` and, on
  create with no explicit timezone, copies `workspace.timezone`. Port both.
  `Project.FORBIDDEN_IDENTIFIER_CHARS_PATTERN` is a serializer-level validation, not a DB
  constraint — enforce in the Go layer.

#### MODEL: `project_members` — Django `ProjectMember`
- **Source**: `db/models/project.py` (`ProjectMember(ProjectBaseModel)`)
- **Base**: `P-block`
- **Fields**:

| field | PB type | PB options | Django source | null/blank | default | unique |
|---|---|---|---|---|---|---|
| `member` | `relation`→`users` | `required=false`, `cascadeDelete=true` | `member FK(User, CASCADE, null=True, blank=True)` | null, blank | — | — |
| `comment` | `text` | max 1 000 000 | `comment TextField(blank, null)` | null, blank | — | — |
| `role` | `number` | `onlyInt=true` | `role PositiveSmallIntegerField(choices 20/15/5, default=5)` | — | `<n>` `5` | — |
| `view_props` | `json` | — | `view_props JSONField(default=get_default_props)` | — | `<n>` `get_default_props()` | — |
| `default_props` | `json` | — | `default_props JSONField(default=get_default_props)` | — | `<n>` `get_default_props()` | — |
| `preferences` | `json` | — | `preferences JSONField(default=get_default_preferences)` | — | `<n>` `{"pages":{"block_display":true},"navigation":{"default_tab":"work_items","hide_in_more_menu":[]}}` | — |
| `sort_order` | `number` | — | `sort_order FloatField(default=65535)` | — | `<n>` `65535` | — |
| `is_active` | `bool` | — | `is_active` | — | `true` | — |

- **Indexes**: `CREATE UNIQUE INDEX project_member_unique_project_member_when_deleted_at_null ON project_members (project, member) WHERE deleted_at = ''`.
- **Join-table role**: yes — the project↔user membership edge (§5).
- **Notes**: `ProjectMember.save()` (`project.py:226`) auto-creates a
  `ProjectUserProperty` on first insert with
  `sort_order = (min(existing ProjectUserProperty.sort_order for workspace+user) ?? 65535) - 10000`.
  This side-effect must be reproduced in a Go create hook.

#### MODEL: `project_member_invites` — Django `ProjectMemberInvite`
- **Source**: `db/models/project.py` (`ProjectMemberInvite(ProjectBaseModel)`)
- **Base**: `P-block`
- **Fields**:

| field | PB type | PB options | Django source | null/blank | default | unique |
|---|---|---|---|---|---|---|
| `email` | `text` | required, max 255 | `email CharField(255)` | not null | — | — |
| `accepted` | `bool` | — | `accepted` | — | `false` | — |
| `token` | `text` | required, max 255 | `token CharField(255)` | not null | — | — |
| `message` | `text` | max 1 000 000 | `message TextField(null=True)` | null | — | — |
| `responded_at` | `date` | required=false | `responded_at DateTimeField(null=True)` | null | — | — |
| `role` | `number` | `onlyInt=true` | `role PositiveSmallIntegerField(choices, default=5)` | — | `<n>` `5` | — |

- **Indexes**: none declared. **`REVIEW`**: no uniqueness on `(email, project)` is enforced
  in the current model (unlike the workspace-level invite). Do not invent one.
- **Notes**: `db_table` is `project_member_invites` in the current model (migration
  `0001_initial.py` used `project_member_invite`; the plural is authoritative).

#### MODEL: `project_identifiers` — Django `ProjectIdentifier`
- **Source**: `db/models/project.py` (`ProjectIdentifier(AuditModel)`)
- **Base**: **A-block minus `id`-as-UUID** — this model inherits `AuditModel` directly and
  defines **no `id`**, so Django assigns an implicit `BigAutoField` primary key
  (confirmed in `migrations/0001_initial.py:1267`). See §2.7.
- **Fields**:

| field | PB type | PB options | Django source | null/blank | default | unique |
|---|---|---|---|---|---|---|
| `legacy_pk` | `number` | `onlyInt=true`, unique index | implicit `BigAutoField(pk)` | not null | — | **unique** |
| `created_at` / `updated_at` | `autodate` | `onCreate` / `onUpdate` | `AuditModel` | — | now | — |
| `created_by` / `updated_by` | `relation`→`users` | `required=false`, `cascadeDelete=false` | `AuditModel` | null | — | — |
| `deleted_at` | `date` | required=false | `AuditModel` | null | — | — |
| `workspace` | `relation`→`workspaces` | `required=false`, `cascadeDelete=true` | `workspace FK(Workspace, CASCADE, null=True)` | null | — | — |
| `project` | `relation`→`projects` | `required=true`, `maxSelect=1`, `cascadeDelete=true` + **unique index** | `project OneToOneField(Project, CASCADE)` | not null | — | **unique** |
| `name` | `text` | required, max 12, db_index | `name CharField(12, db_index)` | not null | — | part of partial unique |

- **Indexes**:
  `CREATE UNIQUE INDEX unique_name_workspace_when_deleted_at_null ON project_identifiers (name, workspace) WHERE deleted_at = ''`
  `CREATE UNIQUE INDEX idx_project_identifiers_project ON project_identifiers (project)`
  `CREATE UNIQUE INDEX idx_project_identifiers_legacy_pk ON project_identifiers (legacy_pk)`
- **Notes**: **no `uuid` field.** This is the only model in the schema whose Django PK is an
  integer, and the only one where the API's `id` is an integer. `ProjectIdentifier` is
  marked `# TODO: Remove workspace relation later` in the source.

#### MODEL: `project_deploy_boards` — Django `ProjectDeployBoard`
- **Source**: `db/models/project.py` (`ProjectDeployBoard(ProjectBaseModel)`)
- **Base**: `P-block`
- **Fields**:

| field | PB type | PB options | Django source | null/blank | default | unique |
|---|---|---|---|---|---|---|
| `anchor` | `text` | required, max 255, unique index | `anchor CharField(255, default=uuid4().hex, unique, db_index)` | not null | `<n>` `uuid4().hex` | **unique** |
| `comments` | `bool` | — | `comments` | — | `false` | — |
| `reactions` | `bool` | — | `reactions` | — | `false` | — |
| `intake` | `relation`→`intakes` | `required=false`, `cascadeDelete=false` | `intake FK(Intake, SET_NULL, null=True)` | null | — | — |
| `votes` | `bool` | — | `votes` | — | `false` | — |
| `views` | `json` | — | `views JSONField(default=get_default_views)` | — | `<n>` `{"list":true,"kanban":true,"calendar":true,"gantt":true,"spreadsheet":true}` | — |

- **Indexes**: unique on `anchor`; `CREATE UNIQUE INDEX project_deploy_boards_project_anchor ON project_deploy_boards (project, anchor)` (from `unique_together = ["project","anchor"]` — note this one has **no** `deleted_at` and **no** `condition`, so it is a hard unique constraint).
- **Notes**: marked `# DEPRECATED TODO:` in the source; superseded by `DeployBoard`. Kept
  because the table exists and legacy rows are read.

#### MODEL: `project_public_members` — Django `ProjectPublicMember`
- **Source**: `db/models/project.py` (`ProjectPublicMember(ProjectBaseModel)`)
- **Base**: `P-block`
- **Fields**:

| field | PB type | PB options | Django source | null/blank | default | unique |
|---|---|---|---|---|---|---|
| `member` | `relation`→`users` | `required=true`, `cascadeDelete=true` | `member FK(User, CASCADE)` | not null | — | — |

- **Indexes**: `CREATE UNIQUE INDEX project_public_member_unique_project_member_when_deleted_at_null ON project_public_members (project, member) WHERE deleted_at = ''`.
- **Join-table role**: yes — public (unauthenticated) project↔user edge (§5).

#### MODEL: `project_user_properties` — Django `ProjectUserProperty`
- **Source**: `db/models/project.py` (`ProjectUserProperty(ProjectBaseModel)`)
- **Base**: `P-block`
- **Fields**:

| field | PB type | PB options | Django source | null/blank | default | unique |
|---|---|---|---|---|---|---|
| `user` | `relation`→`users` | `required=true`, `cascadeDelete=true` | `user FK(User, CASCADE)` | not null | — | — |
| `filters` | `json` | — | `filters JSONField(default=get_default_filters)` (imported from `issue.py`) | — | `<n>` `get_default_filters()` | — |
| `display_filters` | `json` | — | `display_filters JSONField(default=get_default_display_filters)` (`issue.py` — **bare**, not wrapped) | — | `<n>` `{...}` | — |
| `display_properties` | `json` | — | `display_properties JSONField(default=get_default_display_properties)` (`issue.py` — **bare**) | — | `<n>` `{...}` | — |
| `rich_filters` | `json` | — | `default=dict` | — | `<n>` `{}` | — |
| `preferences` | `json` | — | `preferences JSONField(default=get_default_preferences)` | — | `<n>` `{"pages":{"block_display":true},"navigation":{...}}` | — |
| `sort_order` | `number` | — | `sort_order FloatField(default=65535)` | — | `<n>` `65535` | — |

- **Indexes**: `CREATE UNIQUE INDEX project_user_property_unique_user_project_when_deleted_at_null ON project_user_properties (user, project) WHERE deleted_at = ''`.

---

### §3.4 Assets

#### MODEL: `file_assets` — Django `FileAsset`
- **Source**: `db/models/asset.py` (`FileAsset(BaseModel)`)
- **Base**: `A-block`
- **Fields**:

| field | PB type | PB options | Django source | null/blank | default | unique |
|---|---|---|---|---|---|---|
| `attributes` | `json` | — | `attributes JSONField(default=dict)` | — | `<n>` `{}` | — |
| `asset` | `file` | single, `required=true`, `maxSize` = Plane `FILE_SIZE_LIMIT` (5 MB default), `protected=true` | `asset FileField(upload_to=get_upload_path, max_length=800, validators=[file_size])` | not null | — | — |
| `user` | `relation`→`users` | `required=false`, `cascadeDelete=true` | `user FK(User, CASCADE, null=True)` | null | — | — |
| `workspace` | `relation`→`workspaces` | `required=false`, `cascadeDelete=true` | `workspace FK(Workspace, CASCADE, null=True)` | null | — | — |
| `draft_issue` | `relation`→`draft_issues` | `required=false`, `cascadeDelete=true` | `draft_issue FK(DraftIssue, CASCADE, null=True)` | null | — | — |
| `project` | `relation`→`projects` | `required=false`, `cascadeDelete=true` | `project FK(Project, CASCADE, null=True)` | null | — | — |
| `issue` | `relation`→`issues` | `required=false`, `cascadeDelete=true` | `issue FK(Issue, CASCADE, null=True)` | null | — | — |
| `comment` | `relation`→`issue_comments` | `required=false`, `cascadeDelete=true` | `comment FK(IssueComment, CASCADE, null=True)` | null | — | — |
| `page` | `relation`→`pages` | `required=false`, `cascadeDelete=true` | `page FK(Page, CASCADE, null=True)` | null | — | — |
| `entity_type` | `text` | max 255 | `entity_type CharField(255, null, blank)` — values in `EntityTypeContext` are **not** wired to `choices=` | null, blank | — | — |
| `entity_identifier` | `text` | max 255 | `entity_identifier CharField(255, null, blank)` | null, blank | — | — |
| `is_deleted` | `bool` | — | `is_deleted` | — | `false` | — |
| `is_archived` | `bool` | — | `is_archived` | — | `false` | — |
| `external_id` | `text` | max 255 | `external_id CharField(255, null, blank)` | null, blank | — | — |
| `external_source` | `text` | max 255 | `external_source CharField(255, null, blank)` | null, blank | — | — |
| `size` | `number` | — | `size FloatField(default=0)` | — | `<n>` `0` | — |
| `is_uploaded` | `bool` | — | `is_uploaded` | — | `false` | — |
| `storage_metadata` | `json` | — | `storage_metadata JSONField(default=dict, null=True, blank=True)` | null, blank | `<n>` `{}` | — |

- **Indexes**: `asset_entity_type_idx (entity_type)`, `asset_entity_identifier_idx (entity_identifier)`, `asset_entity_idx (entity_type, entity_identifier)`, `asset_asset_idx (asset)`.
- **Notes**: this is the most circular collection in the schema — it references `users`,
  `workspaces`, `projects`, `issues`, `issue_comments`, `pages` and `draft_issues`, and
  `users`/`workspaces`/`projects` reference it back. See §6.2.
  `FileAsset.asset_url` is a computed property with four distinct URL shapes keyed on
  `entity_type`; it must be reimplemented in the Go API layer (it is not data).
  `get_upload_path()` (workspace-scoped `{workspace_id}/{uuid4}-{filename}`) is replaced by
  PocketBase's own `/{collectionId}/{recordId}/{filename}` layout — the stored value
  changes shape. Flagged in §7.

---

### §3.5 Taxonomy: states, labels, estimates, issue types

#### MODEL: `states` — Django `State`
- **Source**: `db/models/state.py` (`State(ProjectBaseModel)`)
- **Base**: `P-block`
- **Fields**:

| field | PB type | PB options | Django source | null/blank | default | unique |
|---|---|---|---|---|---|---|
| `name` | `text` | required, max 255 | `name CharField(255)` | not null | — | part of partial unique |
| `description` | `text` | max 1 000 000 | `description TextField(blank)` | blank | `""` | — |
| `color` | `text` | required, max 255 | `color CharField(255)` | not null | — | — |
| `slug` | `text` | max 100, slug pattern | `slug SlugField(100, blank)` | blank | `<n>` `slugify(name)` on every save | — |
| `sequence` | `number` | — | `sequence FloatField(default=65535)` | — | `<n>` `65535`, else `max(existing)+15000` | — |
| `group` | `select` | `values=["backlog","unstarted","started","completed","cancelled","triage"]`, `maxSelect=1` | `group CharField(20, choices=StateGroup.choices, default=backlog)` | — | `<n>` `"backlog"` | — |
| `is_triage` | `bool` | — | `is_triage` | — | `false` | — |
| `default` | `bool` | — | `default` | — | `false` | — |
| `external_source` | `text` | max 255 | `external_source` | null, blank | — | — |
| `external_id` | `text` | max 255 | `external_id` | null, blank | — | — |

- **Indexes**: `CREATE UNIQUE INDEX state_unique_name_project_when_deleted_at_null ON states (name, project) WHERE deleted_at = ''`; ordering is `sequence` (set `ORDER BY sequence` semantics in the Go layer — PB has no per-collection default ordering).
- **Notes**: `State.save()` (`state.py:117`) sets `slug = slugify(name)` on **every** save
  and `sequence = max(project sequences) + 15000` on insert. Port both.
  `DEFAULT_STATES` (`state.py:24`) must be seeded per project.
  Three managers exist (`StateManager` excludes `triage`; `TriageStateManager` filters
  `triage`; `all_state_objects` unfiltered) — the Go layer must expose the same three
  query modes. `IssueManager` and `Issue.issue_objects` similarly exclude triage states,
  archived issues and drafts (§3.6).

#### MODEL: `labels` — Django `Label`
- **Source**: `db/models/label.py` (`Label(WorkspaceBaseModel)`)
- **Base**: `W-block`
- **Fields**:

| field | PB type | PB options | Django source | null/blank | default | unique |
|---|---|---|---|---|---|---|
| `parent` | `relation`→`labels` | `required=false`, `cascadeDelete=true` | `parent FK(self, CASCADE, null=True, blank=True)` | null, blank | — | — |
| `name` | `text` | required, max 255 | `name CharField(255)` | not null | — | conditional (see indexes) |
| `description` | `text` | max 1 000 000 | `description TextField(blank)` | blank | `""` | — |
| `color` | `text` | max 255 | `color CharField(255, blank)` | blank | `""` | — |
| `sort_order` | `number` | — | `sort_order FloatField(default=65535)` | — | `<n>` `65535`, else `max+10000` | — |
| `external_source` | `text` | max 255 | `external_source` | null, blank | — | — |
| `external_id` | `text` | max 255 | `external_id` | null, blank | — | — |

- **Indexes** (two **conditional** uniques — this is the trickiest constraint in the schema):
  `CREATE UNIQUE INDEX unique_name_when_project_null_and_not_deleted ON labels (name) WHERE project = '' AND deleted_at = ''`
  `CREATE UNIQUE INDEX unique_project_name_when_not_deleted ON labels (project, name) WHERE project != '' AND deleted_at = ''`
  A workspace-level label (no project) is unique by name alone; a project-level label is
  unique by `(project, name)`. `Label.save()` (`label.py:46`) sets
  `sort_order = max(project labels) + 10000`.
- **Self-relation**: `parent` → `labels`. Deferred to pass 2 (§6.2).

#### MODEL: `estimates` — Django `Estimate`
- **Source**: `db/models/estimate.py` (`Estimate(ProjectBaseModel)`)
- **Base**: `P-block`
- **Fields**:

| field | PB type | PB options | Django source | null/blank | default | unique |
|---|---|---|---|---|---|---|
| `name` | `text` | required, max 255 | `name CharField(255)` | not null | — | part of partial unique |
| `description` | `text` | max 1 000 000 | `description TextField(blank)` | blank | `""` | — |
| `type` | `select` | `values=["categories","points"]`, `maxSelect=1` | `type CharField(255, choices=EstimateType, default=categories)` | — | `<n>` `"categories"` | — |
| `last_used` | `bool` | — | `last_used` | — | `false` | — |

- **Indexes**: `CREATE UNIQUE INDEX estimate_unique_name_project_when_deleted_at_null ON estimates (name, project) WHERE deleted_at = ''`.

#### MODEL: `estimate_points` — Django `EstimatePoint`
- **Source**: `db/models/estimate.py` (`EstimatePoint(ProjectBaseModel)`)
- **Base**: `P-block`
- **Fields**:

| field | PB type | PB options | Django source | null/blank | default | unique |
|---|---|---|---|---|---|---|
| `estimate` | `relation`→`estimates` | `required=true`, `cascadeDelete=true` | `estimate FK(Estimate, CASCADE)` | not null | — | — |
| `key` | `number` | `onlyInt=true`, `min=0` | `key IntegerField(default=0, validators=[MinValueValidator(0)])` | — | `<n>` `0` | — |
| `description` | `text` | max 1 000 000 | `description TextField(blank)` | blank | `""` | — |
| `value` | `text` | required, max 255 | `value CharField(255)` | not null | — | — |

- **Indexes**: none declared (the historical `unique_together` was dropped in migration `0030`).
- **Notes**: `key=0` is legal and distinct from "unset" only because `key` is NOT NULL —
  this is why `Issue.point` (nullable) needs `json` (§1.3(e)) while `EstimatePoint.key` is
  fine as `number`.

#### MODEL: `issue_types` — Django `IssueType`
- **Source**: `db/models/issue_type.py` (`IssueType(BaseModel)`)
- **Base**: `A-block`
- **Fields**:

| field | PB type | PB options | Django source | null/blank | default | unique |
|---|---|---|---|---|---|---|
| `workspace` | `relation`→`workspaces` | `required=true`, `cascadeDelete=true` | `workspace FK(Workspace, CASCADE)` | not null | — | — |
| `name` | `text` | required, max 255 | `name CharField(255)` | not null | — | — |
| `description` | `text` | max 1 000 000 | `description TextField(blank)` | blank | `""` | — |
| `logo_props` | `json` | — | `logo_props JSONField(default=dict)` | — | `<n>` `{}` | — |
| `is_epic` | `bool` | — | `is_epic` | — | `false` | — |
| `is_default` | `bool` | — | `is_default` | — | `false` | — |
| `is_active` | `bool` | — | `is_active` | — | `true` | — |
| `level` | `number` | — | `level FloatField(default=0)` | — | `<n>` `0` | — |
| `external_source` | `text` | max 255 | `external_source` | null, blank | — | — |
| `external_id` | `text` | max 255 | `external_id` | null, blank | — | — |

- **Indexes**: none declared. Gated by `Project.is_issue_type_enabled`.

#### MODEL: `project_issue_types` — Django `ProjectIssueType`
- **Source**: `db/models/issue_type.py` (`ProjectIssueType(ProjectBaseModel)`)
- **Base**: `P-block`
- **Fields**:

| field | PB type | PB options | Django source | null/blank | default | unique |
|---|---|---|---|---|---|---|
| `issue_type` | `relation`→`issue_types` | `required=true`, `cascadeDelete=true` | `issue_type FK(IssueType, CASCADE)` | not null | — | part of partial unique |
| `level` | `number` | `onlyInt=true`, `min=0` | `level PositiveIntegerField(default=0)` | — | `<n>` `0` | — |
| `is_default` | `bool` | — | `is_default` | — | `false` | — |

- **Indexes**: `CREATE UNIQUE INDEX project_issue_type_unique_project_issue_type_when_deleted_at_null ON project_issue_types (project, issue_type) WHERE deleted_at = ''`.
- **Join-table role**: yes — project↔issue_type enablement edge (§5).

---

### §3.6 Issue core

#### MODEL: `issues` — Django `Issue`
- **Source**: `db/models/issue.py` (`Issue(ChangeTrackerMixin, ProjectBaseModel)`)
- **Base**: `P-block`
- **Fields**:

| field | PB type | PB options | Django source | null/blank | default | unique |
|---|---|---|---|---|---|---|
| `parent` | `relation`→`issues` | `required=false`, `cascadeDelete=true` | `parent FK(self, CASCADE, null=True, blank=True)` | null, blank | — | — |
| `state` | `relation`→`states` | `required=false`, `cascadeDelete=true` | `state FK(State, CASCADE, null=True, blank=True)` | null, blank | `<n>` resolved from `State(default=True)` else first non-triage state | — |
| `point` | **`json`** | `maxSize` 64 | `point IntegerField(validators 0..12, null=True, blank=True)` | null, blank | — | — |
| `estimate_point` | `relation`→`estimate_points` | `required=false`, `cascadeDelete=false` | `estimate_point FK(EstimatePoint, SET_NULL, null=True, blank=True)` | null, blank | — | — |
| `name` | `text` | required, max 255 | `name CharField(255)` | not null | — | — |
| `description_json` | `json` | `maxSize` raised (default 1 MB may be tight) | `description_json JSONField(blank, default=dict)` | blank | `<n>` `{}` | — |
| `description_html` | `editor` | `maxSize` 5 MB | `description_html TextField(blank, default="<p></p>")` | blank | `<n>` `"<p></p>"` | — |
| `description_stripped` | `text` | `max` 2 000 000 | `description_stripped TextField(blank, null)` | null, blank | `<n>` `strip_tags(description_html)` | — |
| `description_binary` | `file` | single, `protected=true`, `required=false` | `description_binary BinaryField(null=True)` | null | — | — |
| `priority` | `select` | `values=["urgent","high","medium","low","none"]`, `maxSelect=1` | `priority CharField(30, choices=PRIORITY_CHOICES, default="none")` | — | `<n>` `"none"` | — |
| `start_date` | `date` | required=false | `start_date DateField(null=True, blank=True)` | null, blank | — | — |
| `target_date` | `date` | required=false | `target_date DateField(null=True, blank=True)` | null, blank | — | — |
| `sequence_id` | `number` | `onlyInt=true`, `min=1` | `sequence_id IntegerField(default=1)` | — | `<n>` computed on insert (see notes) | — |
| `sort_order` | `number` | — | `sort_order FloatField(default=65535)` | — | `<n>` `65535`, else `max(same project+state)+10000` | — |
| `completed_at` | `date` | required=false | `completed_at DateTimeField(null=True)` | null | `<n>` `now()` when state group is `completed`, else cleared | — |
| `archived_at` | `date` | required=false | `archived_at DateField(null=True)` | null | — | — |
| `is_draft` | `bool` | — | `is_draft` | — | `false` | — |
| `external_source` | `text` | max 255 | `external_source` | null, blank | — | — |
| `external_id` | `text` | max 255 | `external_id` | null, blank | — | — |
| `type` | `relation`→`issue_types` | `required=false`, `cascadeDelete=false` | `type FK(IssueType, SET_NULL, null=True, blank=True)` | null, blank | — | — |

- **M2M declarations (not columns)** — both are `through=` models and produce **no** extra
  table: `assignees` → `issue_assignees`, `labels` → `issue_labels` (§5).
- **Indexes**: `CREATE INDEX idx_issues_project_state ON issues (project, state)` and
  `CREATE INDEX idx_issues_sequence_id ON issues (project, sequence_id)`. No unique
  constraint on `(project, sequence_id)` exists in Django — do **not** add one.
- **Notes — behaviour that must be ported, not just schema**:
  1. `Issue.save()` (`issue.py:180`) takes a **PostgreSQL advisory lock**
     (`pg_advisory_xact_lock(convert_uuid_to_integer(project.id))`) to serialise sequence
     allocation per project, then sets `sequence_id = max(IssueSequence.sequence)+1` and
     inserts an `issue_sequences` row. SQLite has no advisory locks: the Go layer must use
     a transaction with an appropriate lock (or PB's own transaction + `BEGIN IMMEDIATE`).
     This is the single most behaviour-critical port in the schema.
  2. `_ensure_default_state()` assigns the project's default non-triage state when none is
     given.
  3. `_sync_completed_at()` sets/clears `completed_at` whenever `state` changes to/from the
     `completed` group (`TRACKED_FIELDS = ["state_id"]`).
  4. `description_stripped` is recomputed from `description_html` via `strip_tags` on every
     save.
  5. `Issue.issue_objects` (`IssueManager`, `issue.py:92`) excludes
     `state.group = "triage"`, `archived_at IS NOT NULL`, `project.archived_at IS NOT NULL`
     and `is_draft = true`. The Go layer must reproduce this as the default issue queryset.
  6. `Issue.archived_at` is a **`DateField`** (date only) while `Project.archived_at` and
     `Cycle.archived_at` are `DateTimeField`. Do not unify them.
- **Self-relation**: `parent` → `issues`. Deferred to pass 2 (§6.2).

#### MODEL: `issue_sequences` — Django `IssueSequence`
- **Source**: `db/models/issue.py` (`IssueSequence(ProjectBaseModel)`)
- **Base**: `P-block`
- **Fields**:

| field | PB type | PB options | Django source | null/blank | default | unique |
|---|---|---|---|---|---|---|
| `issue` | `relation`→`issues` | `required=false`, `cascadeDelete=false` | `issue FK(Issue, SET_NULL, null=True)` | null | — | — |
| `sequence` | `number` | `onlyInt=true`, `min=1` | `sequence PositiveBigIntegerField(default=1, db_index)` | — | `<n>` `1` | — |
| `deleted` | `bool` | — | `deleted` | — | `false` | — |

- **Indexes**: `CREATE INDEX idx_issue_sequences_sequence ON issue_sequences (sequence)`.
- **Notes**: the source comment is explicit — *"This is set to null because we want to keep
  the sequence even if the issue is deleted"*. This is why `SET_NULL` and not `CASCADE`.
  This table is the monotonic issue-number allocator; losing it re-issues numbers.

#### MODEL: `issue_blockers` — Django `IssueBlocker`
- **Source**: `db/models/issue.py` (`IssueBlocker(ProjectBaseModel)`)
- **Base**: `P-block`
- **Fields**:

| field | PB type | PB options | Django source | null/blank | default | unique |
|---|---|---|---|---|---|---|
| `block` | `relation`→`issues` | `required=true`, `cascadeDelete=true` | `block FK(Issue, CASCADE, related_name=blocker_issues)` | not null | — | — |
| `blocked_by` | `relation`→`issues` | `required=true`, `cascadeDelete=true` | `blocked_by FK(Issue, CASCADE, related_name=blocked_issues)` | not null | — | — |

- **Indexes**: none declared (no unique constraint). **`REVIEW`** — duplicates are possible
  in Plane; do not add a unique index.

#### MODEL: `issue_relations` — Django `IssueRelation`
- **Source**: `db/models/issue.py` (`IssueRelation(ProjectBaseModel)`)
- **Base**: `P-block`
- **Fields**:

| field | PB type | PB options | Django source | null/blank | default | unique |
|---|---|---|---|---|---|---|
| `issue` | `relation`→`issues` | `required=true`, `cascadeDelete=true` | `issue FK(Issue, CASCADE)` | not null | — | part of partial unique |
| `related_issue` | `relation`→`issues` | `required=true`, `cascadeDelete=true` | `related_issue FK(Issue, CASCADE)` | not null | — | part of partial unique |
| `relation_type` | `text` | required, max 20 | `relation_type CharField(20, default=IssueRelationChoices.BLOCKED_BY)` — enum **not** wired to `choices=` | not null | `<n>` `"blocked_by"` | — |

- **Indexes**: `CREATE UNIQUE INDEX issue_relation_unique_issue_related_issue_when_deleted_at_null ON issue_relations (issue, related_issue) WHERE deleted_at = ''`.
- **Notes**: `relation_type` stays `text`, **not** `select` — see §1.3(h). The valid set is
  `duplicate, relates_to, blocked_by, start_before, finish_before, implemented_by` plus the
  reverse names in `IssueRelationChoices._REVERSE_MAPPING` (`blocking`, `start_after`,
  `finish_after`, `implements`); the Go layer must implement the bidirectional pair logic.

#### MODEL: `issue_mentions` — Django `IssueMention`
- **Source**: `db/models/issue.py` (`IssueMention(ProjectBaseModel)`)
- **Base**: `P-block`
- **Fields**:

| field | PB type | PB options | Django source | null/blank | default | unique |
|---|---|---|---|---|---|---|
| `issue` | `relation`→`issues` | `required=true`, `cascadeDelete=true` | `issue FK(Issue, CASCADE)` | not null | — | part of partial unique |
| `mention` | `relation`→`users` | `required=true`, `cascadeDelete=true` | `mention FK(User, CASCADE)` | not null | — | part of partial unique |

- **Indexes**: `CREATE UNIQUE INDEX issue_mention_unique_issue_mention_when_deleted_at_null ON issue_mentions (issue, mention) WHERE deleted_at = ''`.
- **Join-table role**: yes (§5).

#### MODEL: `issue_assignees` — Django `IssueAssignee`
- **Source**: `db/models/issue.py` (`IssueAssignee(ProjectBaseModel)`)
- **Base**: `P-block`
- **Fields**:

| field | PB type | PB options | Django source | null/blank | default | unique |
|---|---|---|---|---|---|---|
| `issue` | `relation`→`issues` | `required=true`, `cascadeDelete=true` | `issue FK(Issue, CASCADE)` | not null | — | part of partial unique |
| `assignee` | `relation`→`users` | `required=true`, `cascadeDelete=true` | `assignee FK(User, CASCADE)` | not null | — | part of partial unique |

- **Indexes**: `CREATE UNIQUE INDEX issue_assignee_unique_issue_assignee_when_deleted_at_null ON issue_assignees (issue, assignee) WHERE deleted_at = ''`.
- **Join-table role**: yes — the `Issue.assignees` M2M through model (§5).

#### MODEL: `issue_links` — Django `IssueLink`
- **Source**: `db/models/issue.py` (`IssueLink(ProjectBaseModel)`)
- **Base**: `P-block`
- **Fields**:

| field | PB type | PB options | Django source | null/blank | default | unique |
|---|---|---|---|---|---|---|
| `title` | `text` | max 255 | `title CharField(255, null, blank)` | null, blank | — | — |
| `url` | `text` | max 1 000 000 | `url TextField()` | not null | — | — |
| `issue` | `relation`→`issues` | `required=true`, `cascadeDelete=true` | `issue FK(Issue, CASCADE)` | not null | — | — |
| `metadata` | `json` | — | `metadata JSONField(default=dict)` | — | `<n>` `{}` | — |

- **Notes**: `url` is a `TextField`, **not** a `URLField` — it stays `text` so arbitrary
  pasted values survive (§1.2). Contrast `ModuleLink.url`, which *is* a `URLField`.

#### MODEL: `issue_attachments` — Django `IssueAttachment`
- **Source**: `db/models/issue.py` (`IssueAttachment(ProjectBaseModel)`)
- **Base**: `P-block`
- **Fields**:

| field | PB type | PB options | Django source | null/blank | default | unique |
|---|---|---|---|---|---|---|
| `attributes` | `json` | — | `attributes JSONField(default=dict)` | — | `<n>` `{}` | — |
| `asset` | `file` | single, `required=true`, `maxSize` = `FILE_SIZE_LIMIT`, `protected=true` | `asset FileField(upload_to=get_upload_path, validators=[file_size])` | not null | — | — |
| `issue` | `relation`→`issues` | `required=true`, `cascadeDelete=true` | `issue FK(Issue, CASCADE)` | not null | — | — |
| `external_source` | `text` | max 255 | `external_source` | null, blank | — | — |
| `external_id` | `text` | max 255 | `external_id` | null, blank | — | — |

#### MODEL: `issue_activities` — Django `IssueActivity`
- **Source**: `db/models/issue.py` (`IssueActivity(ProjectBaseModel)`)
- **Base**: `P-block`
- **Fields**:

| field | PB type | PB options | Django source | null/blank | default | unique |
|---|---|---|---|---|---|---|
| `issue` | `relation`→`issues` | `required=false`, **`cascadeDelete=false`** | `issue FK(Issue, **DO_NOTHING**, null=True)` | null | — | — |
| `verb` | `text` | required, max 255 | `verb CharField(255, default="created")` | not null | `<n>` `"created"` | — |
| `field` | `text` | max 255 | `field CharField(255, blank, null)` | null, blank | — | — |
| `old_value` | `text` | max 1 000 000 | `old_value TextField(blank, null)` | null, blank | — | — |
| `new_value` | `text` | max 1 000 000 | `new_value TextField(blank, null)` | null, blank | — | — |
| `comment` | `text` | max 1 000 000 | `comment TextField(blank)` | blank | `""` | — |
| `attachments` | `json` | `maxSize` 1 MB | `attachments ArrayField(URLField(), size=10, blank, default=list)` | blank | `<n>` `[]`; **max 10 items enforced in Go** | — |
| `issue_comment` | `relation`→`issue_comments` | `required=false`, **`cascadeDelete=false`** | `issue_comment FK(IssueComment, **DO_NOTHING**, null=True)` | null | — | — |
| `actor` | `relation`→`users` | `required=false`, `cascadeDelete=false` | `actor FK(User, SET_NULL, null=True)` | null | — | — |
| `old_identifier` | `text` | max 36, UUID pattern | `old_identifier UUIDField(null=True)` | null | — | — |
| `new_identifier` | `text` | max 36, UUID pattern | `new_identifier UUIDField(null=True)` | null | — | — |
| `epoch` | **`json`** | `maxSize` 64 | `epoch FloatField(null=True)` | null | — | — |

- **Indexes**: `CREATE INDEX idx_issue_activities_issue ON issue_activities (issue)`,
  `CREATE INDEX idx_issue_activities_comment ON issue_activities (issue_comment)`.
- **Notes**: the two `DO_NOTHING` foreign keys are deliberate — activity history must
  outlive the issue and the comment it describes. PocketBase has no `DO_NOTHING`: a
  non-cascading relation is *automatically unset* (`record_model.go:1633`). To preserve
  Plane's behaviour the Go layer must either (a) add a pre-delete hook that blocks the
  unset, or (b) accept the unset and rely on `old_identifier`/`new_identifier` to retain
  the identity. **Recommended: (b)** — `old_identifier`/`new_identifier` already exist
  precisely for this, and they hold Plane UUIDs. Document the behaviour change. §7.
  `epoch` is `json` because `0.0` is a legal epoch (§1.3(e)).

#### MODEL: `issue_comments` — Django `IssueComment`
- **Source**: `db/models/issue.py` (`IssueComment(ChangeTrackerMixin, ProjectBaseModel)`)
- **Base**: `P-block`
- **Fields**:

| field | PB type | PB options | Django source | null/blank | default | unique |
|---|---|---|---|---|---|---|
| `comment_stripped` | `text` | `max` 2 000 000 | `comment_stripped TextField(blank)` | blank | `<n>` `strip_tags(comment_html)` | — |
| `comment_json` | `json` | — | `comment_json JSONField(blank, default=dict)` | blank | `<n>` `{}` | — |
| `comment_html` | `editor` | `maxSize` 5 MB | `comment_html TextField(blank, default="<p></p>")` | blank | `<n>` `"<p></p>"` | — |
| `description` | `relation`→`descriptions` | `required=false`, `cascadeDelete=true` + **unique index** | `description OneToOneField(Description, CASCADE, null=True)` | null | — | **unique** |
| `attachments` | `json` | `maxSize` 1 MB | `attachments ArrayField(URLField(), size=10, blank, default=list)` | blank | `<n>` `[]`; max 10 in Go | — |
| `issue` | `relation`→`issues` | `required=true`, `cascadeDelete=true` | `issue FK(Issue, CASCADE)` | not null | — | — |
| `actor` | `relation`→`users` | `required=false`, `cascadeDelete=true` | `actor FK(User, CASCADE, null=True)` | null | — | — |
| `access` | `select` | `values=["INTERNAL","EXTERNAL"]`, `maxSelect=1` | `access CharField(100, choices, default="INTERNAL")` | — | `<n>` `"INTERNAL"` | — |
| `external_source` | `text` | max 255 | `external_source` | null, blank | — | — |
| `external_id` | `text` | max 255 | `external_id` | null, blank | — | — |
| `edited_at` | `date` | required=false | `edited_at DateTimeField(null=True, blank=True)` | null, blank | — | — |
| `parent` | `relation`→`issue_comments` | `required=false`, `cascadeDelete=true` | `parent FK(self, CASCADE, null=True, blank=True)` | null, blank | — | — |

- **Indexes**: `CREATE UNIQUE INDEX idx_issue_comments_description ON issue_comments (description)`.
- **Notes — critical side-effect**: `IssueComment.save()` (`issue.py:481`) maintains a
  paired `Description` row in the same transaction: it creates one on insert, links it via
  `description_id`, and mirrors `comment_html/stripped/json` →
  `description_html/stripped/json` on update (using `ChangeTrackerMixin._changes_on_save`).
  The Go layer must reproduce this transactionally, otherwise `descriptions` and
  `description_versions` become orphaned. `comment_stripped` is recomputed from
  `comment_html` on every save.
- **Self-relation**: `parent` → `issue_comments`. Deferred to pass 2 (§6.2).

#### MODEL: `issue_labels` — Django `IssueLabel`
- **Source**: `db/models/issue.py` (`IssueLabel(ProjectBaseModel)`)
- **Base**: `P-block`
- **Fields**:

| field | PB type | PB options | Django source | null/blank | default | unique |
|---|---|---|---|---|---|---|
| `issue` | `relation`→`issues` | `required=true`, `cascadeDelete=true` | `issue FK(Issue, CASCADE)` | not null | — | — |
| `label` | `relation`→`labels` | `required=true`, `cascadeDelete=true` | `label FK(Label, CASCADE)` | not null | — | — |

- **Indexes**: **none declared** — `IssueLabel` has *no* `unique_together` and *no*
  `UniqueConstraint` (unlike every other issue join table). Duplicate
  `(issue, label)` rows are therefore possible in Plane. **`REVIEW`**: adding a unique
  index would change behaviour; do not add one unless the API layer is changed to match.
- **Join-table role**: yes — the `Issue.labels` M2M through model (§5).

#### MODEL: `issue_subscribers` — Django `IssueSubscriber`
- **Source**: `db/models/issue.py` (`IssueSubscriber(ProjectBaseModel)`)
- **Base**: `P-block`
- **Fields**:

| field | PB type | PB options | Django source | null/blank | default | unique |
|---|---|---|---|---|---|---|
| `issue` | `relation`→`issues` | `required=true`, `cascadeDelete=true` | `issue FK(Issue, CASCADE)` | not null | — | part of partial unique |
| `subscriber` | `relation`→`users` | `required=true`, `cascadeDelete=true` | `subscriber FK(User, CASCADE)` | not null | — | part of partial unique |

- **Indexes**: `CREATE UNIQUE INDEX issue_subscriber_unique_issue_subscriber_when_deleted_at_null ON issue_subscribers (issue, subscriber) WHERE deleted_at = ''`.
- **Join-table role**: yes (§5).

#### MODEL: `issue_reactions` — Django `IssueReaction`
- **Source**: `db/models/issue.py` (`IssueReaction(ProjectBaseModel)`)
- **Base**: `P-block`
- **Fields**:

| field | PB type | PB options | Django source | null/blank | default | unique |
|---|---|---|---|---|---|---|
| `actor` | `relation`→`users` | `required=true`, `cascadeDelete=true` | `actor FK(User, CASCADE)` | not null | — | part of partial unique |
| `issue` | `relation`→`issues` | `required=true`, `cascadeDelete=true` | `issue FK(Issue, CASCADE)` | not null | — | part of partial unique |
| `reaction` | `text` | required, max 1 000 000 | `reaction TextField()` (emoji) | not null | — | part of partial unique |

- **Indexes**: `CREATE UNIQUE INDEX issue_reaction_unique_issue_actor_reaction_when_deleted_at_null ON issue_reactions (issue, actor, reaction) WHERE deleted_at = ''`.
- **Join-table role**: yes (§5). `reaction` is a `TextField` (emoji), not a `CharField`.

#### MODEL: `comment_reactions` — Django `CommentReaction`
- **Source**: `db/models/issue.py` (`CommentReaction(ProjectBaseModel)`)
- **Base**: `P-block`
- **Fields**:

| field | PB type | PB options | Django source | null/blank | default | unique |
|---|---|---|---|---|---|---|
| `actor` | `relation`→`users` | `required=true`, `cascadeDelete=true` | `actor FK(User, CASCADE)` | not null | — | part of partial unique |
| `comment` | `relation`→`issue_comments` | `required=true`, `cascadeDelete=true` | `comment FK(IssueComment, CASCADE)` | not null | — | part of partial unique |
| `reaction` | `text` | required, max 1 000 000 | `reaction TextField()` | not null | — | part of partial unique |

- **Indexes**: `CREATE UNIQUE INDEX comment_reaction_unique_comment_actor_reaction_when_deleted_at_null ON comment_reactions (comment, actor, reaction) WHERE deleted_at = ''`.
- **Join-table role**: yes (§5).

#### MODEL: `issue_votes` — Django `IssueVote`
- **Source**: `db/models/issue.py` (`IssueVote(ProjectBaseModel)`)
- **Base**: `P-block`
- **Fields**:

| field | PB type | PB options | Django source | null/blank | default | unique |
|---|---|---|---|---|---|---|
| `issue` | `relation`→`issues` | `required=true`, `cascadeDelete=true` | `issue FK(Issue, CASCADE)` | not null | — | part of partial unique |
| `actor` | `relation`→`users` | `required=true`, `cascadeDelete=true` | `actor FK(User, CASCADE)` | not null | — | part of partial unique |
| `vote` | `number` | `onlyInt=true`, `min=-1`, `max=1` | `vote IntegerField(choices -1/1, default=1)` | — | `<n>` `1` (Upvote) | — |

- **Indexes**: `CREATE UNIQUE INDEX issue_vote_unique_issue_actor_when_deleted_at_null ON issue_votes (issue, actor) WHERE deleted_at = ''`.
- **Join-table role**: yes (§5).

#### MODEL: `issue_versions` — Django `IssueVersion`
- **Source**: `db/models/issue.py` (`IssueVersion(ProjectBaseModel)`)
- **Base**: `P-block`
- **Fields**:

| field | PB type | PB options | Django source | null/blank | default | unique |
|---|---|---|---|---|---|---|
| `parent` | `text` | max 36, UUID pattern | `parent UUIDField(blank, null)` | null, blank | — | — |
| `state` | `text` | max 36, UUID pattern | `state UUIDField(blank, null)` | null, blank | — | — |
| `estimate_point` | `text` | max 36, UUID pattern | `estimate_point UUIDField(blank, null)` | null, blank | — | — |
| `name` | `text` | required, max 255 | `name CharField(255)` | not null | — | — |
| `priority` | `select` | `values=["urgent","high","medium","low","none"]` | `priority CharField(30, choices, default="none")` | — | `<n>` `"none"` | — |
| `start_date` | `date` | required=false | `start_date DateField(null, blank)` | null, blank | — | — |
| `target_date` | `date` | required=false | `target_date DateField(null, blank)` | null, blank | — | — |
| `assignees` | `json` | `maxSize` 1 MB | `assignees ArrayField(UUIDField(), blank, default=list)` | blank | `<n>` `[]` | — |
| `sequence_id` | `number` | `onlyInt=true` | `sequence_id IntegerField(default=1)` | — | `<n>` `1` | — |
| `labels` | `json` | `maxSize` 1 MB | `labels ArrayField(UUIDField(), blank, default=list)` | blank | `<n>` `[]` | — |
| `sort_order` | `number` | — | `sort_order FloatField(default=65535)` | — | `<n>` `65535` | — |
| `completed_at` | `date` | required=false | `completed_at DateTimeField(null=True)` | null | — | — |
| `archived_at` | `date` | required=false | `archived_at DateField(null=True)` | null | — | — |
| `is_draft` | `bool` | — | `is_draft` | — | `false` | — |
| `external_source` | `text` | max 255 | `external_source` | null, blank | — | — |
| `external_id` | `text` | max 255 | `external_id` | null, blank | — | — |
| `type` | `text` | max 36, UUID pattern | `type UUIDField(blank, null)` | null, blank | — | — |
| `cycle` | `text` | max 36, UUID pattern | `cycle UUIDField(null, blank)` | null, blank | — | — |
| `modules` | `json` | `maxSize` 1 MB | `modules ArrayField(UUIDField(), blank, default=list)` | blank | `<n>` `[]` | — |
| `properties` | `json` | — | `properties JSONField(default=dict)` | — | `<n>` `{}` | — |
| `meta` | `json` | — | `meta JSONField(default=dict)` | — | `<n>` `{}` | — |
| `last_saved_at` | `date` | required=true | `last_saved_at DateTimeField(default=timezone.now)` | — | `<n>` now | — |
| `issue` | `relation`→`issues` | `required=true`, `cascadeDelete=true` | `issue FK(Issue, CASCADE)` | not null | — | — |
| `activity` | `relation`→`issue_activities` | `required=false`, `cascadeDelete=false` | `activity FK(IssueActivity, SET_NULL, null=True)` | null | — | — |
| `owned_by` | `relation`→`users` | `required=true`, `cascadeDelete=true` | `owned_by FK(User, CASCADE)` | not null | — | — |

- **Notes**: every UUID-shaped field here is a **snapshot**, deliberately *not* a foreign
  key — a version row must not be updated or deleted when the referenced object changes.
  They store Plane UUIDs in `text` columns (§2.5). `IssueVersion.log_issue_version()`
  (`issue.py:736`) is the writer and must be ported as-is.

#### MODEL: `issue_description_versions` — Django `IssueDescriptionVersion`
- **Source**: `db/models/issue.py` (`IssueDescriptionVersion(ProjectBaseModel)`)
- **Base**: `P-block`
- **Fields**:

| field | PB type | PB options | Django source | null/blank | default | unique |
|---|---|---|---|---|---|---|
| `issue` | `relation`→`issues` | `required=true`, `cascadeDelete=true` | `issue FK(Issue, CASCADE)` | not null | — | — |
| `description_binary` | `file` | single, `protected=true`, `required=false` | `description_binary BinaryField(null=True)` | null | — | — |
| `description_html` | `editor` | `maxSize` 5 MB | `description_html TextField(blank, default="<p></p>")` | blank | `<n>` `"<p></p>"` | — |
| `description_stripped` | `text` | `max` 2 000 000 | `description_stripped TextField(blank, null)` | null, blank | `<n>` `strip_tags(...)` | — |
| `description_json` | `json` | — | `description_json JSONField(default=dict, blank)` | blank | `<n>` `{}` | — |
| `last_saved_at` | `date` | required=true | `last_saved_at DateTimeField(default=timezone.now)` | — | `<n>` now | — |
| `owned_by` | `relation`→`users` | `required=true`, `cascadeDelete=true` | `owned_by FK(User, CASCADE)` | not null | — | — |

- **Notes**: **`REVIEW`** — `log_issue_description_version()` is defined but this model is
  the only one of the two description-version tables that has no `save()` override
  recomputing `description_stripped`. Keep it as a plain table.

---

### §3.7 Cycle

#### MODEL: `cycles` — Django `Cycle`
- **Source**: `db/models/cycle.py` (`Cycle(ProjectBaseModel)`)
- **Base**: `P-block`
- **Fields**:

| field | PB type | PB options | Django source | null/blank | default | unique |
|---|---|---|---|---|---|---|
| `name` | `text` | required, max 255 | `name CharField(255)` | not null | — | — |
| `description` | `text` | max 1 000 000 | `description TextField(blank)` | blank | `""` | — |
| `start_date` | `date` | required=false | `start_date DateTimeField(blank, null)` | null, blank | — | — |
| `end_date` | `date` | required=false | `end_date DateTimeField(blank, null)` | null, blank | — | — |
| `owned_by` | `relation`→`users` | `required=true`, `cascadeDelete=true` | `owned_by FK(User, CASCADE)` | not null | — | — |
| `view_props` | `json` | — | `view_props JSONField(default=dict)` | — | `<n>` `{}` | — |
| `sort_order` | `number` | — | `sort_order FloatField(default=65535)` | — | `<n>` `65535`, else `min(project cycles) - 10000` | — |
| `external_source` | `text` | max 255 | `external_source` | null, blank | — | — |
| `external_id` | `text` | max 255 | `external_id` | null, blank | — | — |
| `progress_snapshot` | `json` | — | `progress_snapshot JSONField(default=dict)` | — | `<n>` `{}` | — |
| `archived_at` | `date` | required=false | `archived_at DateTimeField(null=True)` | null | — | — |
| `logo_props` | `json` | — | `logo_props JSONField(default=dict)` | — | `<n>` `{}` | — |
| `timezone` | `text` | max 255 (**`REVIEW`**, §1.3(h)) | `timezone CharField(255, choices, default="UTC")` | — | `<n>` `"UTC"` | — |
| `version` | `number` | `onlyInt=true` | `version IntegerField(default=1)` | — | `<n>` `1` | — |

- **Indexes**: none declared.
- **Notes**: `Cycle.save()` (`cycle.py:88`) sets
  `sort_order = min(existing project cycles) - 10000` on insert. Cycles have **no**
  uniqueness constraint on `(project, name)` — do not add one.

#### MODEL: `cycle_issues` — Django `CycleIssue`
- **Source**: `db/models/cycle.py` (`CycleIssue(ProjectBaseModel)`)
- **Base**: `P-block`
- **Fields**:

| field | PB type | PB options | Django source | null/blank | default | unique |
|---|---|---|---|---|---|---|
| `issue` | `relation`→`issues` | `required=true`, `cascadeDelete=true` | `issue FK(Issue, CASCADE)` | not null | — | part of partial unique |
| `cycle` | `relation`→`cycles` | `required=true`, `cascadeDelete=true` | `cycle FK(Cycle, CASCADE)` | not null | — | part of partial unique |

- **Indexes**: `CREATE UNIQUE INDEX cycle_issue_when_deleted_at_null ON cycle_issues (cycle, issue) WHERE deleted_at = ''`.
- **Join-table role**: yes (§5). Note Plane's constraint is one cycle per issue at a time
  (the index is on `cycle, issue`, and `IssueVersion.log_issue_version` takes `.first()` of
  `CycleIssue.objects.filter(issue=issue)`).

#### MODEL: `cycle_user_properties` — Django `CycleUserProperties`
- **Source**: `db/models/cycle.py` (`CycleUserProperties(ProjectBaseModel)`)
- **Base**: `P-block`
- **Fields**:

| field | PB type | PB options | Django source | null/blank | default | unique |
|---|---|---|---|---|---|---|
| `cycle` | `relation`→`cycles` | `required=true`, `cascadeDelete=true` | `cycle FK(Cycle, CASCADE)` | not null | — | — |
| `user` | `relation`→`users` | `required=true`, `cascadeDelete=true` | `user FK(User, CASCADE)` | not null | — | — |
| `filters` | `json` | — | `filters JSONField(default=get_default_filters)` | — | `<n>` `get_default_filters()` | — |
| `display_filters` | `json` | — | `display_filters JSONField(default=get_default_display_filters)` | — | `<n>` `{...}` | — |
| `display_properties` | `json` | — | `display_properties JSONField(default=get_default_display_properties)` | — | `<n>` `{...}` | — |
| `rich_filters` | `json` | — | `rich_filters JSONField(default=dict)` | — | `<n>` `{}` | — |

- **Indexes**: `CREATE UNIQUE INDEX cycle_user_properties_unique_cycle_user_when_deleted_at_null ON cycle_user_properties (cycle, user) WHERE deleted_at = ''`.

---

### §3.8 Module

#### MODEL: `modules` — Django `Module`
- **Source**: `db/models/module.py` (`Module(ProjectBaseModel)`)
- **Base**: `P-block`
- **Fields**:

| field | PB type | PB options | Django source | null/blank | default | unique |
|---|---|---|---|---|---|---|
| `name` | `text` | required, max 255 | `name CharField(255)` | not null | — | part of partial unique |
| `description` | `text` | max 1 000 000 | `description TextField(blank)` | blank | `""` | — |
| `description_text` | `json` | — | `description_text JSONField(blank, null)` | null, blank | — | — |
| `description_html` | `json` | — | `description_html JSONField(blank, null)` | null, blank | — | — |
| `start_date` | `date` | required=false | `start_date DateField(null=True)` | null | — | — |
| `target_date` | `date` | required=false | `target_date DateField(null=True)` | null | — | — |
| `status` | `select` | `values=["backlog","planned","in-progress","paused","completed","cancelled"]`, `maxSelect=1` | `status CharField(20, choices, default="planned")` | — | `<n>` `"planned"` | — |
| `lead` | `relation`→`users` | `required=false`, `cascadeDelete=false` | `lead FK(User, SET_NULL, null=True)` | null | — | — |
| `view_props` | `json` | — | `view_props JSONField(default=dict)` | — | `<n>` `{}` | — |
| `sort_order` | `number` | — | `sort_order FloatField(default=65535)` | — | `<n>` `65535`, else `min - 10000` | — |
| `external_source` | `text` | max 255 | `external_source` | null, blank | — | — |
| `external_id` | `text` | max 255 | `external_id` | null, blank | — | — |
| `archived_at` | `date` | required=false | `archived_at DateTimeField(null=True)` | null | — | — |
| `logo_props` | `json` | — | `logo_props JSONField(default=dict)` | — | `<n>` `{}` | — |

- **Indexes**: `CREATE UNIQUE INDEX module_unique_name_project_when_deleted_at_null ON modules (name, project) WHERE deleted_at = ''`.
- **M2M declaration (no column)**: `members` → `module_members` (§5).
- **Notes**: `Module.save()` (`module.py:115`) sets `sort_order = min - 10000` on insert.

#### MODEL: `module_members` — Django `ModuleMember`
- **Source**: `db/models/module.py` (`ModuleMember(ProjectBaseModel)`)
- **Base**: `P-block`
- **Fields**:

| field | PB type | PB options | Django source | null/blank | default | unique |
|---|---|---|---|---|---|---|
| `module` | `relation`→`modules` | `required=true`, `cascadeDelete=true` | `module FK(Module, CASCADE)` | not null | — | part of partial unique |
| `member` | `relation`→`users` | `required=true`, `cascadeDelete=true` | `member FK(User, CASCADE)` | not null | — | part of partial unique |

- **Indexes**: `CREATE UNIQUE INDEX module_member_unique_module_member_when_deleted_at_null ON module_members (module, member) WHERE deleted_at = ''`.
- **Join-table role**: yes — the `Module.members` M2M through model (§5).

#### MODEL: `module_issues` — Django `ModuleIssue`
- **Source**: `db/models/module.py` (`ModuleIssue(ProjectBaseModel)`)
- **Base**: `P-block`
- **Fields**:

| field | PB type | PB options | Django source | null/blank | default | unique |
|---|---|---|---|---|---|---|
| `module` | `relation`→`modules` | `required=true`, `cascadeDelete=true` | `module FK(Module, CASCADE)` | not null | — | part of partial unique |
| `issue` | `relation`→`issues` | `required=true`, `cascadeDelete=true` | `issue FK(Issue, CASCADE)` | not null | — | part of partial unique |

- **Indexes**: `CREATE UNIQUE INDEX module_issue_unique_issue_module_when_deleted_at_null ON module_issues (issue, module) WHERE deleted_at = ''`.
- **Join-table role**: yes (§5). An issue may belong to several modules (the unique key is
  the pair), unlike `CycleIssue`.

#### MODEL: `module_links` — Django `ModuleLink`
- **Source**: `db/models/module.py` (`ModuleLink(ProjectBaseModel)`)
- **Base**: `P-block`
- **Fields**:

| field | PB type | PB options | Django source | null/blank | default | unique |
|---|---|---|---|---|---|---|
| `title` | `text` | max 255 | `title CharField(255, blank, null)` | null, blank | — | — |
| `url` | `url` | — | `url URLField()` | not null | — | — |
| `module` | `relation`→`modules` | `required=true`, `cascadeDelete=true` | `module FK(Module, CASCADE)` | not null | — | — |
| `metadata` | `json` | — | `metadata JSONField(default=dict)` | — | `<n>` `{}` | — |

- **Notes**: `url` **is** a `URLField` here (contrast `IssueLink.url`, which is a
  `TextField`). PB `url` validates the format — this is correct for `ModuleLink`.

#### MODEL: `module_user_properties` — Django `ModuleUserProperties`
- **Source**: `db/models/module.py` (`ModuleUserProperties(ProjectBaseModel)`)
- **Base**: `P-block`
- **Fields**:

| field | PB type | PB options | Django source | null/blank | default | unique |
|---|---|---|---|---|---|---|
| `module` | `relation`→`modules` | `required=true`, `cascadeDelete=true` | `module FK(Module, CASCADE)` | not null | — | — |
| `user` | `relation`→`users` | `required=true`, `cascadeDelete=true` | `user FK(User, CASCADE)` | not null | — | — |
| `filters` | `json` | — | `filters JSONField(default=get_default_filters)` | — | `<n>` `get_default_filters()` | — |
| `display_filters` | `json` | — | `display_filters JSONField(default=get_default_display_filters)` | — | `<n>` `{...}` | — |
| `display_properties` | `json` | — | `display_properties JSONField(default=get_default_display_properties)` | — | `<n>` `{...}` | — |
| `rich_filters` | `json` | — | `rich_filters JSONField(default=dict)` | — | `<n>` `{}` | — |

- **Indexes**: `CREATE UNIQUE INDEX module_user_properties_unique_module_user_when_deleted_at_null ON module_user_properties (module, user) WHERE deleted_at = ''`.

---

### §3.9 Intake

#### MODEL: `intakes` — Django `Intake`
- **Source**: `db/models/intake.py` (`Intake(ProjectBaseModel)`)
- **Base**: `P-block`
- **Fields**:

| field | PB type | PB options | Django source | null/blank | default | unique |
|---|---|---|---|---|---|---|
| `name` | `text` | required, max 255 | `name CharField(255)` | not null | — | part of partial unique |
| `description` | `text` | max 1 000 000 | `description TextField(blank)` | blank | `""` | — |
| `is_default` | `bool` | — | `is_default` | — | `false` | — |
| `view_props` | `json` | — | `view_props JSONField(default=dict)` | — | `<n>` `{}` | — |
| `logo_props` | `json` | — | `logo_props JSONField(default=dict)` | — | `<n>` `{}` | — |

- **Indexes**: `CREATE UNIQUE INDEX intake_unique_name_project_when_deleted_at_null ON intakes (name, project) WHERE deleted_at = ''`.
- **Notes**: `intakes` is referenced by `deploy_boards.intake` and
  `project_deploy_boards.intake`, so it must be created before those (§6.3).

#### MODEL: `intake_issues` — Django `IntakeIssue`
- **Source**: `db/models/intake.py` (`IntakeIssue(ProjectBaseModel)`)
- **Base**: `P-block`
- **Fields**:

| field | PB type | PB options | Django source | null/blank | default | unique |
|---|---|---|---|---|---|---|
| `intake` | `relation`→`intakes` | `required=true`, `cascadeDelete=true` | `intake FK(Intake, CASCADE)` | not null | — | — |
| `issue` | `relation`→`issues` | `required=true`, `cascadeDelete=true` | `issue FK(Issue, CASCADE)` | not null | — | — |
| `status` | `number` | `onlyInt=true`, `min=-2`, `max=2` | `status IntegerField(choices -2..2, default=-2)` | — | `<n>` `-2` (Pending) | — |
| `snoozed_till` | `date` | required=false | `snoozed_till DateTimeField(null=True)` | null | — | — |
| `duplicate_to` | `relation`→`issues` | `required=false`, `cascadeDelete=false` | `duplicate_to FK(Issue, SET_NULL, null=True)` | null | — | — |
| `source` | `text` | max 255 | `source CharField(255, default="IN_APP", null, blank)` — `SourceType` enum **not** wired to `choices=` | null, blank | `<n>` `"IN_APP"` | — |
| `source_email` | `text` | max 1 000 000 | `source_email TextField(blank, null)` | null, blank | — | — |
| `external_source` | `text` | max 255 | `external_source` | null, blank | — | — |
| `external_id` | `text` | max 255 | `external_id` | null, blank | — | — |
| `extra` | `json` | — | `extra JSONField(default=dict)` | — | `<n>` `{}` | — |

- **Indexes**: none declared — no uniqueness on `(intake, issue)`. **`REVIEW`**: do not add one.
- **Notes**: `duplicate_to` is a **second** relation to `issues` on the same collection
  alongside `issue`. PocketBase allows multiple relation fields to the same collection —
  name them distinctly (`issue`, `duplicate_to`). No conflict.

---

### §3.10 Draft issues

#### MODEL: `draft_issues` — Django `DraftIssue`
- **Source**: `db/models/draft.py` (`DraftIssue(WorkspaceBaseModel)`)
- **Base**: `W-block`
- **Fields**:

| field | PB type | PB options | Django source | null/blank | default | unique |
|---|---|---|---|---|---|---|
| `parent` | `relation`→`issues` | `required=false`, `cascadeDelete=true` | `parent FK(Issue, CASCADE, null=True, blank=True)` | null, blank | — | — |
| `state` | `relation`→`states` | `required=false`, `cascadeDelete=true` | `state FK(State, CASCADE, null=True, blank=True)` | null, blank | `<n>` resolved from project default state | — |
| `estimate_point` | `relation`→`estimate_points` | `required=false`, `cascadeDelete=false` | `estimate_point FK(EstimatePoint, SET_NULL, null=True, blank=True)` | null, blank | — | — |
| `name` | `text` | max 255 | `name CharField(255, blank, null)` | null, blank | — | — |
| `description_json` | `json` | — | `description_json JSONField(blank, default=dict)` | blank | `<n>` `{}` | — |
| `description_html` | `editor` | `maxSize` 5 MB | `description_html TextField(blank, default="<p></p>")` | blank | `<n>` `"<p></p>"` | — |
| `description_stripped` | `text` | `max` 2 000 000 | `description_stripped TextField(blank, null)` | null, blank | `<n>` `strip_tags(...)` | — |
| `description_binary` | `file` | single, `protected=true`, `required=false` | `description_binary BinaryField(null=True)` | null | — | — |
| `priority` | `select` | `values=["urgent","high","medium","low","none"]` | `priority CharField(30, choices, default="none")` | — | `<n>` `"none"` | — |
| `start_date` | `date` | required=false | `start_date DateField(null, blank)` | null, blank | — | — |
| `target_date` | `date` | required=false | `target_date DateField(null, blank)` | null, blank | — | — |
| `sort_order` | `number` | — | `sort_order FloatField(default=65535)` | — | `<n>` `65535`, else `max(same project+state)+10000` | — |
| `completed_at` | `date` | required=false | `completed_at DateTimeField(null=True)` | null | `<n>` `now()` when `state.group == "completed"`, else cleared | — |
| `external_source` | `text` | max 255 | `external_source` | null, blank | — | — |
| `external_id` | `text` | max 255 | `external_id` | null, blank | — | — |
| `type` | `relation`→`issue_types` | `required=false`, `cascadeDelete=false` | `type FK(IssueType, SET_NULL, null=True, blank=True)` | null, blank | — | — |

- **M2M declarations (no columns)**: `assignees` → `draft_issue_assignees`,
  `labels` → `draft_issue_labels` (§5). Note there is **no** `modules` or `cycle` M2M
  declared on `DraftIssue` — `DraftIssueModule` / `DraftIssueCycle` are standalone models
  that the draft-promotion code populates.
- **Indexes**: none declared.
- **Notes**: `DraftIssue.save()` (`draft.py:84`) mirrors `Issue.save()` for
  `completed_at`, `description_stripped` and `sort_order`, but takes **no advisory lock**
  and allocates **no** sequence. Port the save behaviour without the locking.

#### MODEL: `draft_issue_assignees` — Django `DraftIssueAssignee`
- **Source**: `db/models/draft.py` (`DraftIssueAssignee(WorkspaceBaseModel)`)
- **Base**: `W-block`
- **Fields**:

| field | PB type | PB options | Django source | null/blank | default | unique |
|---|---|---|---|---|---|---|
| `draft_issue` | `relation`→`draft_issues` | `required=true`, `cascadeDelete=true` | `draft_issue FK(DraftIssue, CASCADE)` | not null | — | part of partial unique |
| `assignee` | `relation`→`users` | `required=true`, `cascadeDelete=true` | `assignee FK(User, CASCADE)` | not null | — | part of partial unique |

- **Indexes**: `CREATE UNIQUE INDEX draft_issue_assignee_unique_issue_assignee_when_deleted_at_null ON draft_issue_assignees (draft_issue, assignee) WHERE deleted_at = ''`.
- **Join-table role**: yes — the `DraftIssue.assignees` M2M through model (§5).

#### MODEL: `draft_issue_labels` — Django `DraftIssueLabel`
- **Source**: `db/models/draft.py` (`DraftIssueLabel(WorkspaceBaseModel)`)
- **Base**: `W-block`
- **Fields**:

| field | PB type | PB options | Django source | null/blank | default | unique |
|---|---|---|---|---|---|---|
| `draft_issue` | `relation`→`draft_issues` | `required=true`, `cascadeDelete=true` | `draft_issue FK(DraftIssue, CASCADE)` | not null | — | — |
| `label` | `relation`→`labels` | `required=true`, `cascadeDelete=true` | `label FK(Label, CASCADE)` | not null | — | — |

- **Indexes**: **none declared** (no `unique_together`, no `UniqueConstraint`) — same as
  `IssueLabel`. Do not add one.
- **Join-table role**: yes — the `DraftIssue.labels` M2M through model (§5).

#### MODEL: `draft_issue_modules` — Django `DraftIssueModule`
- **Source**: `db/models/draft.py` (`DraftIssueModule(WorkspaceBaseModel)`)
- **Base**: `W-block`
- **Fields**:

| field | PB type | PB options | Django source | null/blank | default | unique |
|---|---|---|---|---|---|---|
| `module` | `relation`→`modules` | `required=true`, `cascadeDelete=true` | `module FK(Module, CASCADE)` | not null | — | part of partial unique |
| `draft_issue` | `relation`→`draft_issues` | `required=true`, `cascadeDelete=true` | `draft_issue FK(DraftIssue, CASCADE)` | not null | — | part of partial unique |

- **Indexes**: `CREATE UNIQUE INDEX module_draft_issue_unique_issue_module_when_deleted_at_null ON draft_issue_modules (draft_issue, module) WHERE deleted_at = ''`.
- **Join-table role**: yes (§5).

#### MODEL: `draft_issue_cycles` — Django `DraftIssueCycle`
- **Source**: `db/models/draft.py` (`DraftIssueCycle(WorkspaceBaseModel)`)
- **Base**: `W-block`
- **Fields**:

| field | PB type | PB options | Django source | null/blank | default | unique |
|---|---|---|---|---|---|---|
| `draft_issue` | `relation`→`draft_issues` | `required=true`, `cascadeDelete=true` | `draft_issue FK(DraftIssue, CASCADE)` | not null | — | part of partial unique |
| `cycle` | `relation`→`cycles` | `required=true`, `cascadeDelete=true` | `cycle FK(Cycle, CASCADE)` | not null | — | part of partial unique |

- **Indexes**: `CREATE UNIQUE INDEX draft_issue_cycle_when_deleted_at_null ON draft_issue_cycles (draft_issue, cycle) WHERE deleted_at = ''`.
- **Join-table role**: yes (§5).

---

### §3.11 Pages

#### MODEL: `pages` — Django `Page`
- **Source**: `db/models/page.py` (`Page(BaseModel)`)
- **Base**: `A-block`
- **Fields**:

| field | PB type | PB options | Django source | null/blank | default | unique |
|---|---|---|---|---|---|---|
| `workspace` | `relation`→`workspaces` | `required=true`, `cascadeDelete=true` | `workspace FK(Workspace, CASCADE)` | not null | — | — |
| `name` | `text` | `max` 1 000 000 | `name TextField(blank)` | blank | `""` | — |
| `description_json` | `json` | — | `description_json JSONField(default=dict, blank)` | blank | `<n>` `{}` | — |
| `description_binary` | `file` | single, `protected=true`, `required=false` | `description_binary BinaryField(null=True)` | null | — | — |
| `description_html` | `editor` | `maxSize` 5 MB | `description_html TextField(blank, default="<p></p>")` | blank | `<n>` `"<p></p>"` | — |
| `description_stripped` | `text` | `max` 2 000 000 | `description_stripped TextField(blank, null)` | null, blank | `<n>` `strip_tags(...)` | — |
| `owned_by` | `relation`→`users` | `required=true`, `cascadeDelete=true` | `owned_by FK(User, CASCADE)` | not null | — | — |
| `access` | `number` | `onlyInt=true`, `min=0`, `max=1` | `access PositiveSmallIntegerField(choices 0/1, default=0)` | — | `<n>` `0` (Public) | — |
| `color` | `text` | max 255 | `color CharField(255, blank)` | blank | `""` | — |
| `parent` | `relation`→`pages` | `required=false`, `cascadeDelete=true` | `parent FK(self, CASCADE, null=True, blank=True)` | null, blank | — | — |
| `archived_at` | `date` | required=false | `archived_at DateField(null=True)` | null | — | — |
| `is_locked` | `bool` | — | `is_locked` | — | `false` | — |
| `view_props` | `json` | — | `view_props JSONField(default=get_view_props)` | — | `<n>` `{"full_width":false}` | — |
| `logo_props` | `json` | — | `logo_props JSONField(default=dict)` | — | `<n>` `{}` | — |
| `is_global` | `bool` | — | `is_global` | — | `false` | — |
| `moved_to_page` | `text` | max 36, UUID pattern | `moved_to_page UUIDField(null=True, blank=True)` | null, blank | — | — |
| `moved_to_project` | `text` | max 36, UUID pattern | `moved_to_project UUIDField(null=True, blank=True)` | null, blank | — | — |
| `sort_order` | `number` | — | `sort_order FloatField(default=65535)` | — | `<n>` `65535` | — |
| `external_id` | `text` | max 255 | `external_id` | null, blank | — | — |
| `external_source` | `text` | max 255 | `external_source` | null, blank | — | — |

- **M2M declarations (no columns)**: `labels` → `page_labels`, `projects` → `project_pages` (§5).
- **Indexes**: none declared. `Page.DEFAULT_SORT_ORDER = 65535`.
- **Notes**: `moved_to_page`/`moved_to_project` are plain UUIDs, not relations — they record
  where a page *came from* after a move and deliberately do not cascade (§2.5).
  `Page.save()` (`page.py:70`) recomputes `description_stripped`.
- **Self-relation**: `parent` → `pages`. Deferred to pass 2 (§6.2).

#### MODEL: `page_logs` — Django `PageLog`
- **Source**: `db/models/page.py` (`PageLog(BaseModel)`)
- **Base**: `A-block`
- **Fields**:

| field | PB type | PB options | Django source | null/blank | default | unique |
|---|---|---|---|---|---|---|
| `transaction` | `text` | required, max 36, UUID pattern | `transaction UUIDField(default=uuid.uuid4)` | not null | `<n>` `uuid4()` | part of unique |
| `page` | `relation`→`pages` | `required=true`, `cascadeDelete=true` | `page FK(Page, CASCADE)` | not null | — | part of unique |
| `entity_identifier` | `text` | max 36, UUID pattern | `entity_identifier UUIDField(null=True, blank=True)` | null, blank | — | — |
| `entity_name` | `text` | required, max 30 | `entity_name CharField(30)` — `TYPE_CHOICES` **not** wired to `choices=` | not null | — | — |
| `entity_type` | `text` | max 30 | `entity_type CharField(30, null, blank)` | null, blank | — | — |
| `workspace` | `relation`→`workspaces` | `required=true`, `cascadeDelete=true` | `workspace FK(Workspace, CASCADE)` | not null | — | — |

- **Indexes**: `CREATE UNIQUE INDEX page_logs_page_transaction ON page_logs (page, transaction)` (hard unique — `unique_together = ["page","transaction"]` has no `deleted_at`).
  Plus the five declared indexes: `pagelog_entity_type_idx (entity_type)`,
  `pagelog_entity_id_idx (entity_identifier)`, `pagelog_entity_name_idx (entity_name)`,
  `pagelog_type_id_idx (entity_type, entity_identifier)`,
  `pagelog_name_id_idx (entity_name, entity_identifier)`.
- **Notes**: `entity_name`/`entity_type` are `text`, not `select` — the `TYPE_CHOICES` enum
  (`to_do`, `issue`, `image`, `video`, `file`, `link`, `cycle`, `module`, `back_link`,
  `forward_link`, `page_mention`, `user_mention`) is never passed as `choices=`, so no
  constraint exists in Plane.

#### MODEL: `page_labels` — Django `PageLabel`
- **Source**: `db/models/page.py` (`PageLabel(BaseModel)`)
- **Base**: `A-block`
- **Fields**:

| field | PB type | PB options | Django source | null/blank | default | unique |
|---|---|---|---|---|---|---|
| `label` | `relation`→`labels` | `required=true`, `cascadeDelete=true` | `label FK(Label, CASCADE)` | not null | — | — |
| `page` | `relation`→`pages` | `required=true`, `cascadeDelete=true` | `page FK(Page, CASCADE)` | not null | — | — |
| `workspace` | `relation`→`workspaces` | `required=true`, `cascadeDelete=true` | `workspace FK(Workspace, CASCADE)` | not null | — | — |

- **Indexes**: **none declared** — no uniqueness on `(page, label)`. Do not add one.
- **Join-table role**: yes — the `Page.labels` M2M through model (§5).
- **Notes**: this is the only join table that inherits `BaseModel` rather than
  `ProjectBaseModel`/`WorkspaceBaseModel`, so it carries `workspace` explicitly.

#### MODEL: `project_pages` — Django `ProjectPage`
- **Source**: `db/models/page.py` (`ProjectPage(BaseModel)`)
- **Base**: `A-block`
- **Fields**:

| field | PB type | PB options | Django source | null/blank | default | unique |
|---|---|---|---|---|---|---|
| `project` | `relation`→`projects` | `required=true`, `cascadeDelete=true` | `project FK(Project, CASCADE)` | not null | — | part of partial unique |
| `page` | `relation`→`pages` | `required=true`, `cascadeDelete=true` | `page FK(Page, CASCADE)` | not null | — | part of partial unique |
| `workspace` | `relation`→`workspaces` | `required=true`, `cascadeDelete=true` | `workspace FK(Workspace, CASCADE)` | not null | — | — |

- **Indexes**: `CREATE UNIQUE INDEX project_page_unique_project_page_when_deleted_at_null ON project_pages (project, page) WHERE deleted_at = ''`.
- **Join-table role**: yes — the `Page.projects` M2M through model (§5).

#### MODEL: `page_versions` — Django `PageVersion`
- **Source**: `db/models/page.py` (`PageVersion(BaseModel)`)
- **Base**: `A-block`
- **Fields**:

| field | PB type | PB options | Django source | null/blank | default | unique |
|---|---|---|---|---|---|---|
| `workspace` | `relation`→`workspaces` | `required=true`, `cascadeDelete=true` | `workspace FK(Workspace, CASCADE)` | not null | — | — |
| `page` | `relation`→`pages` | `required=true`, `cascadeDelete=true` | `page FK(Page, CASCADE)` | not null | — | — |
| `last_saved_at` | `date` | required=true | `last_saved_at DateTimeField(default=timezone.now)` | — | `<n>` now | — |
| `owned_by` | `relation`→`users` | `required=true`, `cascadeDelete=true` | `owned_by FK(User, CASCADE)` | not null | — | — |
| `description_binary` | `file` | single, `protected=true`, `required=false` | `description_binary BinaryField(null=True)` | null | — | — |
| `description_html` | `editor` | `maxSize` 5 MB | `description_html TextField(blank, default="<p></p>")` | blank | `<n>` `"<p></p>"` | — |
| `description_stripped` | `text` | `max` 2 000 000 | `description_stripped TextField(blank, null)` | null, blank | `<n>` `strip_tags(...)` | — |
| `description_json` | `json` | — | `description_json JSONField(default=dict, blank)` | blank | `<n>` `{}` | — |
| `sub_pages_data` | `json` | — | `sub_pages_data JSONField(default=dict, blank)` | blank | `<n>` `{}` | — |

- **Notes**: `PageVersion.save()` (`page.py:175`) recomputes `description_stripped`.

---

### §3.12 Descriptions

#### MODEL: `descriptions` — Django `Description`
- **Source**: `db/models/description.py` (`Description(WorkspaceBaseModel)`)
- **Base**: `W-block`
- **Fields**:

| field | PB type | PB options | Django source | null/blank | default | unique |
|---|---|---|---|---|---|---|
| `description_json` | `json` | — | `description_json JSONField(default=dict, blank)` | blank | `<n>` `{}` | — |
| `description_html` | `editor` | `maxSize` 5 MB | `description_html TextField(blank, default="<p></p>")` | blank | `<n>` `"<p></p>"` | — |
| `description_binary` | `file` | single, `protected=true`, `required=false` | `description_binary BinaryField(null=True)` | null | — | — |
| `description_stripped` | `text` | `max` 2 000 000 | `description_stripped TextField(blank, null)` | null, blank | `<n>` `strip_tags(...)` | — |

- **Notes**: `Description` is a **1:1 satellite of `IssueComment`**
  (`IssueComment.description` → `descriptions`, unique). It is the extraction of comment
  rich text introduced in migration `0101`. `DescriptionVersion` is its history. Both are
  written exclusively through `IssueComment.save()` (§3.6) — the Go layer must keep
  that transaction.

#### MODEL: `description_versions` — Django `DescriptionVersion`
- **Source**: `db/models/description.py` (`DescriptionVersion(WorkspaceBaseModel)`)
- **Base**: `W-block`
- **Fields**:

| field | PB type | PB options | Django source | null/blank | default | unique |
|---|---|---|---|---|---|---|
| `description` | `relation`→`descriptions` | `required=true`, `cascadeDelete=true` | `description FK(Description, CASCADE)` | not null | — | — |
| `description_json` | `json` | — | `description_json JSONField(default=dict, blank)` | blank | `<n>` `{}` | — |
| `description_html` | `editor` | `maxSize` 5 MB | `description_html TextField(blank, default="<p></p>")` | blank | `<n>` `"<p></p>"` | — |
| `description_binary` | `file` | single, `protected=true`, `required=false` | `description_binary BinaryField(null=True)` | null | — | — |
| `description_stripped` | `text` | `max` 2 000 000 | `description_stripped TextField(blank, null)` | null, blank | `<n>` `strip_tags(...)` | — |

- **Notes**: `DescriptionVersion` duplicates the description columns (it is a snapshot
  table, not a pointer to `Description` state). Keep the duplication.

---

### §3.13 Views, analytics, favourites, stickies, recents

#### MODEL: `issue_views` — Django `IssueView`
- **Source**: `db/models/view.py` (`IssueView(WorkspaceBaseModel)`)
- **Base**: `W-block`
- **Fields**:

| field | PB type | PB options | Django source | null/blank | default | unique |
|---|---|---|---|---|---|---|
| `name` | `text` | required, max 255 | `name CharField(255)` | not null | — | — |
| `description` | `text` | max 1 000 000 | `description TextField(blank)` | blank | `""` | — |
| `query` | `json` | `maxSize` raised (filters can be large) | `query JSONField()` | not null | `<n>` recomputed from `filters` | — |
| `filters` | `json` | — | `filters JSONField(default=dict)` | — | `<n>` `{}` | — |
| `display_filters` | `json` | — | `display_filters JSONField(default=get_default_display_filters)` | — | `<n>` `{...}` | — |
| `display_properties` | `json` | — | `display_properties JSONField(default=get_default_display_properties)` | — | `<n>` `{...}` | — |
| `rich_filters` | `json` | — | `rich_filters JSONField(default=dict)` | — | `<n>` `{}` | — |
| `access` | `number` | `onlyInt=true`, `min=0`, `max=1` | `access PositiveSmallIntegerField(default=1, choices 0/1)` | — | `<n>` `1` (Public) | — |
| `sort_order` | `number` | — | `sort_order FloatField(default=65535)` | — | `<n>` `65535`, else `max+10000` | — |
| `logo_props` | `json` | — | `logo_props JSONField(default=dict)` | — | `<n>` `{}` | — |
| `owned_by` | `relation`→`users` | `required=true`, `cascadeDelete=true` | `owned_by FK(User, CASCADE)` | not null | — | — |
| `is_locked` | `bool` | — | `is_locked` | — | `false` | — |
| `archived_at` | `date` | required=false | `archived_at DateTimeField(null=True)` | null | — | — |

- **Indexes**: none declared.
- **Notes**: `IssueView.save()` (`view.py:79`) recomputes `query` from `filters` via
  `plane.utils.issue_filters.issue_filters(filters, "POST")` and sets `sort_order` —
  scoped to `(project)` when a project is set, else to `(workspace, project IS NULL)`.
  Both must be ported. **Note the `access` default is `1` (Private) here**, while
  `Page.access` defaults to `0` (Public) — the enums are inverted relative to each other.

#### MODEL: `analytic_views` — Django `AnalyticView`
- **Source**: `db/models/analytic.py` (`AnalyticView(BaseModel)`)
- **Base**: `A-block`
- **Fields**:

| field | PB type | PB options | Django source | null/blank | default | unique |
|---|---|---|---|---|---|---|
| `workspace` | `relation`→`workspaces` | `required=true`, `cascadeDelete=true` | `workspace FK(Workspace, CASCADE)` | not null | — | — |
| `name` | `text` | required, max 255 | `name CharField(255)` | not null | — | — |
| `description` | `text` | max 1 000 000 | `description TextField(blank)` | blank | `""` | — |
| `query` | `json` | `maxSize` raised | `query JSONField()` | not null | — | — |
| `query_dict` | `json` | — | `query_dict JSONField(default=dict)` | — | `<n>` `{}` | — |

#### MODEL: `user_favorites` — Django `UserFavorite`
- **Source**: `db/models/favorite.py` (`UserFavorite(WorkspaceBaseModel)`)
- **Base**: `W-block`
- **Fields**:

| field | PB type | PB options | Django source | null/blank | default | unique |
|---|---|---|---|---|---|---|
| `user` | `relation`→`users` | `required=true`, `cascadeDelete=true` | `user FK(User, CASCADE)` | not null | — | — |
| `entity_type` | `text` | required, max 100 | `entity_type CharField(100)` | not null | — | part of partial unique |
| `entity_identifier` | `text` | max 36, UUID pattern | `entity_identifier UUIDField(null=True, blank=True)` | null, blank | — | part of partial unique |
| `name` | `text` | max 255 | `name CharField(255, blank, null)` | null, blank | — | — |
| `is_folder` | `bool` | — | `is_folder` | — | `false` | — |
| `sequence` | `number` | — | `sequence FloatField(default=65535)` | — | `<n>` `65535`, else `max(workspace)+10000` | — |
| `parent` | `relation`→`user_favorites` | `required=false`, `cascadeDelete=true` | `parent FK(self, CASCADE, null=True, blank=True)` | null, blank | — | — |

- **Indexes**:
  `CREATE UNIQUE INDEX user_favorite_unique_entity_type_entity_identifier_user_when_deleted_at_null ON user_favorites (entity_type, entity_identifier, user) WHERE deleted_at = ''`
  plus `fav_entity_type_idx (entity_type)`, `fav_entity_identifier_idx (entity_identifier)`,
  `fav_entity_idx (entity_type, entity_identifier)`.
- **Notes**: `entity_identifier` is a polymorphic UUID (§2.5). `UserFavorite.save()`
  (`favorite.py:52`) sets `sequence = max(workspace favourites) + 10000`, scoping on
  `project.workspace` when a project is set.
- **Self-relation**: `parent` → `user_favorites`. Deferred to pass 2 (§6.2).

#### MODEL: `stickies` — Django `Sticky`
- **Source**: `db/models/sticky.py` (`Sticky(BaseModel)`)
- **Base**: `A-block`
- **Fields**:

| field | PB type | PB options | Django source | null/blank | default | unique |
|---|---|---|---|---|---|---|
| `name` | `text` | `max` 1 000 000 | `name TextField(null=True, blank=True)` | null, blank | — | — |
| `description` | `json` | — | `description JSONField(blank, default=dict)` | blank | `<n>` `{}` | — |
| `description_html` | `editor` | `maxSize` 5 MB | `description_html TextField(blank, default="<p></p>")` | blank | `<n>` `"<p></p>"` | — |
| `description_stripped` | `text` | `max` 2 000 000 | `description_stripped TextField(blank, null)` | null, blank | `<n>` `strip_tags(...)` | — |
| `description_binary` | `file` | single, `protected=true`, `required=false` | `description_binary BinaryField(null=True)` | null | — | — |
| `logo_props` | `json` | — | `logo_props JSONField(default=dict)` | — | `<n>` `{}` | — |
| `color` | `text` | max 255 | `color CharField(255, blank, null)` | null, blank | — | — |
| `background_color` | `text` | max 255 | `background_color CharField(255, blank, null)` | null, blank | — | — |
| `workspace` | `relation`→`workspaces` | `required=true`, `cascadeDelete=true` | `workspace FK(Workspace, CASCADE)` | not null | — | — |
| `owner` | `relation`→`users` | `required=true`, `cascadeDelete=true` | `owner FK(User, CASCADE)` | not null | — | — |
| `sort_order` | `number` | — | `sort_order FloatField(default=65535)` | — | `<n>` `65535`, else `max+10000` | — |

- **Notes**: `description` is a `JSONField` named `description` (not `description_json`).
  `Sticky.save()` (`sticky.py:38`) recomputes `description_stripped` and sets `sort_order`.

#### MODEL: `user_recent_visits` — Django `UserRecentVisit`
- **Source**: `db/models/recent_visit.py` (`UserRecentVisit(WorkspaceBaseModel)`)
- **Base**: `W-block`
- **Fields**:

| field | PB type | PB options | Django source | null/blank | default | unique |
|---|---|---|---|---|---|---|
| `entity_identifier` | `text` | max 36, UUID pattern | `entity_identifier UUIDField(null=True)` | null | — | — |
| `entity_name` | `text` | required, max 30 | `entity_name CharField(30)` — `EntityNameEnum` **not** wired to `choices=` | not null | — | — |
| `user` | `relation`→`users` | `required=true`, `cascadeDelete=true` | `user FK(User, CASCADE)` | not null | — | — |
| `visited_at` | `autodate` | `onUpdate=true` | `visited_at DateTimeField(auto_now=True)` | — | now on update | — |

- **Indexes**: none declared — **no** uniqueness on `(entity_name, entity_identifier, user)`.
  Duplicate recent-visit rows are possible. **`REVIEW`**: do not add a unique index.
- **Notes**: `visited_at` is `auto_now=True`, so it maps to an `autodate` with
  `OnUpdate=true` — the same treatment as `updated_at`. `EntityNameEnum` values
  (`VIEW`, `PAGE`, `ISSUE`, `CYCLE`, `MODULE`, `PROJECT`) are not enforced by the DB.

---

### §3.14 Notifications

#### MODEL: `notifications` — Django `Notification`
- **Source**: `db/models/notification.py` (`Notification(BaseModel)`)
- **Base**: `A-block`
- **Fields**:

| field | PB type | PB options | Django source | null/blank | default | unique |
|---|---|---|---|---|---|---|
| `workspace` | `relation`→`workspaces` | `required=true`, `cascadeDelete=true` | `workspace FK(Workspace, CASCADE)` | not null | — | — |
| `project` | `relation`→`projects` | `required=false`, `cascadeDelete=true` | `project FK(Project, CASCADE, null=True)` | null | — | — |
| `data` | `json` | — | `data JSONField(null=True)` | null | — | — |
| `entity_identifier` | `text` | max 36, UUID pattern | `entity_identifier UUIDField(null=True)` | null | — | — |
| `entity_name` | `text` | required, max 255 | `entity_name CharField(255)` | not null | — | — |
| `title` | `text` | `max` 1 000 000 | `title TextField()` | not null | — | — |
| `message` | `json` | — | `message JSONField(null=True)` | null | — | — |
| `message_html` | `editor` | `maxSize` 5 MB | `message_html TextField(blank, default="<p></p>")` | blank | `<n>` `"<p></p>"` | — |
| `message_stripped` | `text` | `max` 2 000 000 | `message_stripped TextField(blank, null)` | null, blank | — | — |
| `sender` | `text` | required, max 255 | `sender CharField(255)` (an event-kind discriminator, **not** a user) | not null | — | — |
| `triggered_by` | `relation`→`users` | `required=false`, `cascadeDelete=false` | `triggered_by FK(User, SET_NULL, null=True)` | null | — | — |
| `receiver` | `relation`→`users` | `required=true`, `cascadeDelete=true` | `receiver FK(User, CASCADE)` | not null | — | — |
| `read_at` | `date` | required=false | `read_at DateTimeField(null=True)` | null | — | — |
| `snoozed_till` | `date` | required=false | `snoozed_till DateTimeField(null=True)` | null | — | — |
| `archived_at` | `date` | required=false | `archived_at DateTimeField(null=True)` | null | — | — |

- **Indexes** (nine declared — reproduce all; they carry the notification query performance):
  `notif_entity_identifier_idx (entity_identifier)`, `notif_entity_name_idx (entity_name)`,
  `notif_read_at_idx (read_at)`, `notif_entity_idx (receiver, read_at)`,
  `notif_receiver_status_idx (receiver, workspace, read_at, created_at)`,
  `notif_receiver_entity_idx (receiver, workspace, entity_name, read_at)`,
  `notif_receiver_state_idx (receiver, workspace, snoozed_till, archived_at)`,
  `notif_receiver_sender_idx (receiver, workspace, sender)`,
  `notif_entity_lookup_idx (workspace, entity_identifier, entity_name)`.
- **Notes**: `sender` is a **`CharField`**, not a user FK — despite the name it holds a
  notification category string. `triggered_by` is the actor.

#### MODEL: `user_notification_preferences` — Django `UserNotificationPreference`
- **Source**: `db/models/notification.py` (`UserNotificationPreference(BaseModel)`)
- **Base**: `A-block`
- **Fields**:

| field | PB type | PB options | Django source | null/blank | default | unique |
|---|---|---|---|---|---|---|
| `user` | `relation`→`users` | `required=true`, `cascadeDelete=true` | `user FK(User, CASCADE)` | not null | — | — |
| `workspace` | `relation`→`workspaces` | `required=false`, `cascadeDelete=true` | `workspace FK(Workspace, CASCADE, null=True)` | null | — | — |
| `project` | `relation`→`projects` | `required=false`, `cascadeDelete=true` | `project FK(Project, CASCADE, null=True)` | null | — | — |
| `property_change` | `bool` | — | `property_change` | — | `true` | — |
| `state_change` | `bool` | — | `state_change` | — | `true` | — |
| `comment` | `bool` | — | `comment` | — | `true` | — |
| `mention` | `bool` | — | `mention` | — | `true` | — |
| `issue_completed` | `bool` | — | `issue_completed` | — | `true` | — |

- **Indexes**: none declared. **`REVIEW`**: no uniqueness on `(user, workspace, project)`.
- **Notes**: a row is auto-created for every new non-bot user by the `post_save` receiver
  `create_user_notification` (`user.py:298`). Reproduce as a Go create-hook on `users`.
  Note the JSON default `get_default_preference()` is **dead code** — it is defined but
  never referenced by any field.

#### MODEL: `email_notification_logs` — Django `EmailNotificationLog`
- **Source**: `db/models/notification.py` (`EmailNotificationLog(BaseModel)`)
- **Base**: `A-block`
- **Fields**:

| field | PB type | PB options | Django source | null/blank | default | unique |
|---|---|---|---|---|---|---|
| `receiver` | `relation`→`users` | `required=true`, `cascadeDelete=true` | `receiver FK(User, CASCADE)` | not null | — | — |
| `triggered_by` | `relation`→`users` | `required=true`, `cascadeDelete=true` | `triggered_by FK(User, CASCADE)` | not null | — | — |
| `entity_identifier` | `text` | max 36, UUID pattern | `entity_identifier UUIDField(null=True)` | null | — | — |
| `entity_name` | `text` | required, max 255 | `entity_name CharField(255)` | not null | — | — |
| `data` | `json` | — | `data JSONField(null=True)` | null | — | — |
| `processed_at` | `date` | required=false | `processed_at DateTimeField(null=True)` | null | — | — |
| `sent_at` | `date` | required=false | `sent_at DateTimeField(null=True)` | null | — | — |
| `entity` | `text` | required, max 200 | `entity CharField(200)` | not null | — | — |
| `old_value` | `text` | max 300 | `old_value CharField(300, blank, null)` | null, blank | — | — |
| `new_value` | `text` | max 300 | `new_value CharField(300, blank, null)` | null, blank | — | — |

- **Indexes**: none declared.
- **Notes**: `receiver` and `triggered_by` are **two distinct relation fields to `users`**
  on the same collection. Name them distinctly; PocketBase permits this.

---

### §3.15 Deploy boards

#### MODEL: `deploy_boards` — Django `DeployBoard`
- **Source**: `db/models/deploy_board.py` (`DeployBoard(WorkspaceBaseModel)`)
- **Base**: `W-block`
- **Fields**:

| field | PB type | PB options | Django source | null/blank | default | unique |
|---|---|---|---|---|---|---|
| `entity_identifier` | `text` | max 36, UUID pattern | `entity_identifier UUIDField(null=True)` | null | — | part of partial unique |
| `entity_name` | `text` | max 30 | `entity_name CharField(30, null, blank)` — `TYPE_CHOICES` **not** wired to `choices=` | null, blank | — | part of partial unique |
| `anchor` | `text` | required, max 255, unique index | `anchor CharField(255, default=uuid4().hex, unique, db_index)` | not null | `<n>` `uuid4().hex` | **unique** |
| `is_comments_enabled` | `bool` | — | `is_comments_enabled` | — | `false` | — |
| `is_reactions_enabled` | `bool` | — | `is_reactions_enabled` | — | `false` | — |
| `intake` | `relation`→`intakes` | `required=false`, `cascadeDelete=false` | `intake FK(Intake, SET_NULL, null=True)` | null | — | — |
| `is_votes_enabled` | `bool` | — | `is_votes_enabled` | — | `false` | — |
| `view_props` | `json` | — | `view_props JSONField(default=dict)` | — | `<n>` `{}` | — |
| `is_activity_enabled` | `bool` | — | `is_activity_enabled` | — | `true` | — |
| `is_disabled` | `bool` | — | `is_disabled` | — | `false` | — |

- **Indexes**:
  `CREATE UNIQUE INDEX deploy_board_unique_entity_name_entity_identifier_when_deleted_at_null ON deploy_boards (entity_name, entity_identifier) WHERE deleted_at = ''`
  `CREATE UNIQUE INDEX idx_deploy_boards_anchor ON deploy_boards (anchor)`
- **Notes**: `DeployBoard` supersedes the deprecated `ProjectDeployBoard`. Both are kept.
  `entity_identifier` is a polymorphic UUID (§2.5) and the `TYPE_CHOICES` enum
  (`project`, `issue`, `module`, `cycle`, `page`, `view`, `intake`) is not DB-enforced.

---

### §3.16 EXCLUDED models

Every model in this section is **`EXCLUDED`** — the migration does **not** create these
collections. Fields are still catalogued so the implementer can confirm the decision and so
that a future re-introduction has a starting point.

| # | Collection | Django class | Source | Reason (one line) |
|---|---|---|---|---|
| E1 | `integrations` | `Integration` | `integration/base.py` | External-integration registry; no endpoint in v1.4.2 (grep: 0 references outside models/migrations). |
| E2 | `workspace_integrations` | `WorkspaceIntegration` | `integration/base.py` | Per-workspace external-integration install; 0 references. |
| E3 | `github_repositories` | `GithubRepository` | `integration/github.py` | GitHub integration repository link; 0 references. |
| E4 | `github_repository_syncs` | `GithubRepositorySync` | `integration/github.py` | GitHub repository sync config; 0 references. |
| E5 | `github_issue_syncs` | `GithubIssueSync` | `integration/github.py` | GitHub issue mirror; 0 references. |
| E6 | `github_comment_syncs` | `GithubCommentSync` | `integration/github.py` | GitHub comment mirror; 0 references. |
| E7 | `slack_project_syncs` | `SlackProjectSync` | `integration/slack.py` | Slack integration install; 0 references. |
| E8 | `importers` | `Importer` | `importer.py` | Jira/GitHub import job; only a serializer exists (`app/serializers/importer.py`), no URL route. |
| E9 | `exporters` | `ExporterHistory` | `exporter.py` | Issue/analytics export job; exporter endpoints are in the api-spec `excluded` set. |
| E10 | `webhooks` | `Webhook` | `webhook.py` | Outbound webhook subscription; the api-spec excludes 4 outbound-webhook endpoints. |
| E11 | `webhook_logs` | `WebhookLog` | `webhook.py` | Outbound webhook delivery log. |
| E12 | `project_webhooks` | `ProjectWebhook` | `webhook.py` | Project↔webhook scoping join. |
| E13 | `social_login_connections` | `SocialLoginConnection` | `social_connection.py` | Legacy OAuth login connection; 0 references (superseded by `Account`). |
| E14 | `devices` | `Device` | `device.py` | Mobile push-notification device registry; 0 references. |
| E15 | `device_sessions` | `DeviceSession` | `device.py` | Device↔session join; 0 references. |

#### §3.16.1 Excluded models — detailed field catalogues

For completeness, and because E10–E12 turned out to be referenced (see §4.2), the full
field lists are recorded here. **These sections exist for traceability only — no migration
code should be generated from them.**

Notation: `name type(options)` per field; `→target` denotes a relation field.

#### MODEL: `integrations` — Django `Integration` — **EXCLUDED**
- **Source**: `db/models/integration/base.py:16` · **Base**: `AuditModel` + explicit UUID `id` → A-block
- Fields: `title text(400) required` · `provider text(400) required unique` · `network number(onlyInt, min 1, max 2, default 1)` · `description json(default {})` · `author text(400)` · `webhook_url text(max 1e6)` · `webhook_secret text(max 1e6)` · `redirect_url text(max 1e6)` · `metadata json(default {})` · `verified bool(false)` · `avatar_url text(max 1e6, null)`
- Indexes: unique on `provider`.

#### MODEL: `workspace_integrations` — Django `WorkspaceIntegration` — **EXCLUDED**
- **Source**: `db/models/integration/base.py:41` · **Base**: A-block
- Fields: `workspace →workspaces(CASCADE, required)` · `actor →users(CASCADE, required)` · `integration →integrations(CASCADE, required)` · `api_token →api_tokens(CASCADE, required)` · `metadata json(default {})` · `config json(default {})`
- Indexes: unique on `(workspace, integration)` (Django `unique_together`, no `deleted_at`).

#### MODEL: `github_repositories` — Django `GithubRepository` — **EXCLUDED**
- **Source**: `db/models/integration/github.py:14` · **Base**: P-block
- Fields: `name text(500) required` · `url url(null)` · `config json(default {})` · `repository_id number(onlyInt) required` · `owner text(500) required`

#### MODEL: `github_repository_syncs` — Django `GithubRepositorySync` — **EXCLUDED**
- **Source**: `db/models/integration/github.py:32` · **Base**: P-block
- Fields: `repository →github_repositories(CASCADE, required, unique)` · `credentials json(default {})` · `actor →users(CASCADE, required)` · `workspace_integration →workspace_integrations(CASCADE, required)` · `label →labels(SET_NULL, null)`
- Indexes: unique on `(project, repository)`.

#### MODEL: `github_issue_syncs` — Django `GithubIssueSync` — **EXCLUDED**
- **Source**: `db/models/integration/github.py:54` · **Base**: P-block
- Fields: `repo_issue_id number(onlyInt)` · `github_issue_id number(onlyInt)` · `issue_url url` · `issue →issues(CASCADE, required)` · `repository_sync →github_repository_syncs(CASCADE, required)`
- Indexes: unique on `(repository_sync, issue)`.

#### MODEL: `github_comment_syncs` — Django `GithubCommentSync` — **EXCLUDED**
- **Source**: `db/models/integration/github.py:73` · **Base**: P-block
- Fields: `repo_comment_id number(onlyInt)` · `comment →issue_comments(CASCADE, required)` · `issue_sync →github_issue_syncs(CASCADE, required)`
- Indexes: unique on `(issue_sync, comment)`.

#### MODEL: `slack_project_syncs` — Django `SlackProjectSync` — **EXCLUDED**
- **Source**: `db/models/integration/slack.py:14` · **Base**: P-block
- Fields: `access_token text(300)` · `scopes text(max 1e6)` · `bot_user_id text(50)` · `webhook_url url` · `data json(default {})` · `team_id text(30)` · `team_name text(300)` · `workspace_integration →workspace_integrations(CASCADE, required)`
- Indexes: unique on `(team_id, project)`.

#### MODEL: `importers` — Django `Importer` — **EXCLUDED**
- **Source**: `db/models/importer.py:13` · **Base**: P-block
- Fields: `service select["github","jira"]` · `status select["queued","processing","completed","failed"](default "queued")` · `initiated_by →users(CASCADE, required)` · `metadata json(default {})` · `config json(default {})` · `data json(default {})` · `token →api_tokens(CASCADE, required)` · `imported_data json(null)`

#### MODEL: `exporters` — Django `ExporterHistory` — **EXCLUDED**
- **Source**: `db/models/exporter.py:24` · **Base**: A-block
- Fields: `name text(255, null)` · `type select["issue_exports","issue_worklogs"](default "issue_exports")` · `workspace →workspaces(CASCADE, required)` · `project json` (`ArrayField(UUIDField)` — array of Plane UUIDs) · `provider select["json","csv","xlsx"]` · `status select["queued","processing","completed","failed"](default "queued")` · `reason text(max 1e6)` · `key text(max 1e6)` · `url url(null)` · `token text(255) required unique` · `initiated_by →users(CASCADE, required)` · `filters json(null)` · `rich_filters json(default {}, null)`
- Indexes: unique on `token`.

#### MODEL: `webhooks` — Django `Webhook` — **EXCLUDED**
- **Source**: `db/models/webhook.py:34` · **Base**: A-block
- Fields: `workspace →workspaces(CASCADE, required)` · `url url(max 1024, validators validate_schema + validate_domain)` · `is_active bool(true)` · `secret_key text(255, default "plane_wh_"+uuid4().hex)` · `project bool(false)` · `issue bool(false)` · `module bool(false)` · `cycle bool(false)` · `issue_comment bool(false)` · `is_internal bool(false)` · `version text(50, default "v1")`
- Indexes: `CREATE UNIQUE INDEX webhook_url_unique_url_when_deleted_at_null ON webhooks (workspace, url) WHERE deleted_at = ''`.
- **Referenced by kept code** — see §4.2(1).

#### MODEL: `webhook_logs` — Django `WebhookLog` — **EXCLUDED**
- **Source**: `db/models/webhook.py:65` · **Base**: A-block
- Fields: `workspace →workspaces(CASCADE, required)` · `webhook text(36, UUID pattern, required)` — a **bare UUID, not a relation** · `event_type text(255, null)` · `request_method text(10, null)` · `request_headers text(max 1e6, null)` · `request_body text(max 1e6, null)` · `response_status text(max 1e6, null)` · `response_headers text(max 1e6, null)` · `response_body text(max 1e6, null)` · `retry_count number(onlyInt, min 0, default 0)`
- **Referenced by kept code** — see §4.2(2).

#### MODEL: `project_webhooks` — Django `ProjectWebhook` — **EXCLUDED**
- **Source**: `db/models/webhook.py:94` · **Base**: P-block
- Fields: `webhook →webhooks(CASCADE, required)`
- Indexes: `CREATE UNIQUE INDEX project_webhook_unique_project_webhook_when_deleted_at_null ON project_webhooks (project, webhook) WHERE deleted_at = ''`.
- **Referenced by kept code** — see §4.2(1).

#### MODEL: `social_login_connections` — Django `SocialLoginConnection` — **EXCLUDED**
- **Source**: `db/models/social_connection.py:14` · **Base**: A-block
- Fields: `medium select["Google","Github","GitLab","Jira"](default null)` · `last_login_at date(default now, null)` · `last_received_at date(default now, null)` · `user →users(CASCADE, required)` · `token_data json(null)` · `extra_data json(null)`
- **Note:** `medium`'s choice labels are capitalised while the values are not — a copy-paste artefact in the source; irrelevant once excluded.

#### MODEL: `devices` — Django `Device` — **EXCLUDED**
- **Source**: `db/models/device.py:11` · **Base**: A-block
- Fields: `user →users(CASCADE, required)` · `device_id text(255, null)` · `device_type select["ANDROID","IOS","WEB","DESKTOP"]` · `push_token text(255, null)` · `is_active bool(true)`

#### MODEL: `device_sessions` — Django `DeviceSession` — **EXCLUDED**
- **Source**: `db/models/device.py:30` · **Base**: A-block
- Fields: `device →devices(CASCADE, required)` · `session →sessions(CASCADE, required)` · `is_active bool(true)` · `user_agent text(255, null)` · `ip_address text(45, null)` · `start_time autodate(onCreate)` · `end_time date(null)`
- **Note:** this is the **only** consumer of `sessions` inside the data model; `sessions`
  itself is kept because a kept view reads it (§3.1).

---

### §3.17 Additional models found (not in the brief)

These four models live in `apps/api/plane/license/models/instance.py`, in a different
Django app but the **same PostgreSQL database**. They were not in the brief's list and are
**not** in the brief's exclusion list. They are catalogued here and flagged
**`RECOMMENDED EXCLUDE`** because they model instance/licensing/telemetry/changelog state,
not workspace data, and the migration brief scopes the work to the product data model.
**This is a decision the migration owner must confirm** — see §8.

#### MODEL: `instances` — Django `Instance` — `RECOMMENDED EXCLUDE`
- **Source**: `license/models/instance.py` · **Base**: `A-block`
- `instance_name text(255) required` · `whitelist_emails text(max 1e6, null)` ·
  `instance_id text(255) required unique` · `current_version text(255) required` ·
  `latest_version text(255, null)` · `edition text(255, default "PLANE_COMMUNITY")` ·
  `domain text(max 1e6)` · `last_checked_at date required` ·
  `namespace text(255, null)` · `is_telemetry_enabled bool(true)` ·
  `is_support_required bool(true)` · `is_setup_done bool(false)` ·
  `is_signup_screen_visited bool(false)` · `is_verified bool(false)` · `is_test bool(false)` ·
  `is_current_version_deprecated bool(false)`.
- Indexes: unique on `instance_id`. Endpoints live under `/api/instances/`
  (`plane/license/urls.py`).

#### MODEL: `instance_admins` — Django `InstanceAdmin` — `RECOMMENDED EXCLUDE`
- **Base**: `A-block`
- `user →users(SET_NULL, null)` · `instance →instances(CASCADE, required)` ·
  `role number(onlyInt, default 20)` · `is_verified bool(false)`.
- Indexes: `unique_together = ["instance","user"]` → hard unique on `(instance, user)`.

#### MODEL: `instance_configurations` — Django `InstanceConfiguration` — `RECOMMENDED EXCLUDE`
- **Base**: `A-block`
- `key text(100) required unique` · `value text(max 1e6, null, default null)` ·
  `category text(max 1e6) required` · `is_encrypted bool(false)`.
- Indexes: unique on `key`.

#### MODEL: `changelogs` — Django `ChangeLog` — `RECOMMENDED EXCLUDE`
- **Base**: `A-block`
- `title text(255) required` · `description text(max 1e6)` · `version text(255) required` ·
  `tags json(default [])` · `release_date date(null)` · `is_release_candidate bool(false)`.
- Indexes: none declared. Note the table name is `changelogs`, not `change_logs`.

---

## 4. Excluded models

### 4.1 The fifteen exclusions, confirmed

All fifteen models named in the brief are `EXCLUDED`. Each was checked by grepping the
entire `apps/` tree (all six apps: `admin`, `api`, `live`, `proxy`, `space`, `web`),
excluding `db/models/`, `migrations/` and `tests/`:

| Collection | Django class | References outside models/migrations/tests | Verdict |
|---|---|---|---|
| `integrations` | `Integration` | **0** (only a comment in `utils/openapi/parameters.py:286`) | Confirmed dead |
| `workspace_integrations` | `WorkspaceIntegration` | **0** | Confirmed dead |
| `github_repositories` | `GithubRepository` | **0** | Confirmed dead |
| `github_repository_syncs` | `GithubRepositorySync` | **0** | Confirmed dead |
| `github_issue_syncs` | `GithubIssueSync` | **0** | Confirmed dead |
| `github_comment_syncs` | `GithubCommentSync` | **0** | Confirmed dead |
| `slack_project_syncs` | `SlackProjectSync` | **0** | Confirmed dead |
| `social_login_connections` | `SocialLoginConnection` | **0** (OAuth login uses `Account`) | Confirmed dead |
| `devices` | `Device` | **0** | Confirmed dead |
| `device_sessions` | `DeviceSession` | **0** | Confirmed dead |
| `importers` | `Importer` | **3** — all in `app/serializers/importer.py`; **no URL route exists** | Excluded feature, dead serializer |
| `exporters` | `ExporterHistory` | **14** — `app/views/exporter/base.py`, `app/serializers/exporter.py`, `bgtasks/export_task.py`, `bgtasks/exporter_expired_task.py`, `bgtasks/cleanup_task.py` | Excluded feature |
| `webhooks` | `Webhook` | **30** | Excluded feature — **but see §4.2** |
| `webhook_logs` | `WebhookLog` | **9** — `app/serializers/webhook.py`, `app/views/webhook/base.py`, `bgtasks/cleanup_task.py`, `bgtasks/webhook_task.py` | Excluded feature — **but see §4.2** |
| `project_webhooks` | `ProjectWebhook` | **0** | Confirmed dead |

Cross-check against the sibling artefact `spec/api-spec.yaml`, which independently marks
these endpoint groups as excluded with matching reasons: `outbound webhook: 4`,
`exporter (issue export): 1`, `exporter (analytics export): 1`,
`exporter (workspace user-activity export): 1`, `OAuth login provider: 16`,
`external integration (Unsplash image search): 1`, `AI feature (LLM assistant endpoint): 2`.
The two artefacts agree.

### 4.2 Excluded models still referenced by kept features — findings

**Three exclusions are referenced from kept code paths. The references are code, not data,
and each is removable, but they are not zero-cost.** Flagging as required by the brief:

1. **`Webhook` — referenced by the core issue / cycle / module / project flows.**
   `model_activity` and `webhook_activity` from `plane.bgtasks.webhook_task` are imported
   and called by **kept** modules:
   * `plane/app/views/issue/base.py:46`
   * `plane/app/views/issue/comment.py:25`
   * `plane/app/views/cycle/base.py:60`
   * `plane/app/views/module/base.py:65`
   * `plane/app/views/project/base.py:27`
   * `plane/api/views/issue.py:92`, `cycle.py:53`, `module.py:44`, `project.py:41`

   Inspecting `webhook_task.model_activity` (`bgtasks/webhook_task.py:479`) and
   `webhook_activity` (`:393`) shows they are **fire-and-forget**: they only
   `Webhook.objects.filter(...)` and enqueue `webhook_send_task`. **No kept feature reads
   webhook data.** Therefore the exclusion is data-safe, but the implementer **must remove
   or stub those 10 call sites** in the Go port, otherwise the ported views will call into
   a non-existent collection. Add this to the port checklist — it is the single largest
   code-touch caused by an exclusion.

2. **`WebhookLog`** — same call chain (`webhook_send_task` writes the log row) plus
   `bgtasks/cleanup_task.py:152,207` (retention sweep). Purely internal to the excluded
   feature.

3. **`Importer`** — referenced only by `app/serializers/importer.py`, which is not wired to
   any URL in `plane/app/urls/` (there is no `importer.py` there). Dead code; deleting the
   serializer is sufficient.

4. **`ExporterHistory`** — fully contained in the exporter feature (`app/views/exporter/`,
   `app/urls/exporter.py`, `bgtasks/export_task.py`). No kept feature reads it. Removing the
   exporter view/url/task files is sufficient.

**No excluded model is read by a kept feature for data.** The only kept-feature coupling is
the webhook *call sites* in (1), which are write-only hooks.

### 4.3 Excluded by implication — Django framework tables

These are not Plane models and are not in the brief's list, but they exist in the same
PostgreSQL database and must **not** be migrated:

| Table | Origin | Why excluded |
|---|---|---|
| `auth_group`, `auth_permission`, `django_content_type` | `django.contrib.auth`, `django.contrib.contenttypes` | Django framework; no Plane data |
| `auth_user_groups`, `auth_user_user_permissions` | `PermissionsMixin.groups` / `.user_permissions` on `User` | Django RBAC; Plane authorises via `WorkspaceMember.role` / `ProjectMember.role`, not Django permissions |
| `django_admin_log` | `django.contrib.admin` | Admin audit log |
| `django_migrations` | Django | Migration bookkeeping; replaced by PocketBase's own `_migrations` |
| `django_session` | — | Plane overrides this with `db.Session` → table `sessions` (§3.1) |

### 4.4 Kept but flagged — `sessions`

`sessions` is **kept** (not in the exclusion list) but is a Django-infrastructure table
that PocketBase's auth system largely supersedes. It is retained because
`plane/app/views/user/base.py:316` (sign-out) issues
`Session.objects.filter(user_id=request.user.id).delete()`, i.e. a kept feature touches it.
**Decision required from the migration owner** — either keep the collection, or drop it and
reimplement sign-out as token invalidation. Recorded as `REVIEW` in §3.1.

---

## 5. Join-table inventory

Every M2M in Plane is declared with an explicit `through=` model, so **PocketBase needs no
M2M emulation**: each join is a first-class collection with its own `uuid` and its own
`id`. There are **no implicit Django M2M tables** in this schema.

### 5.1 M2M `through` models (declared as `ManyToManyField(through=...)`)

These are the sub-resources the API exposes as nested collections.

| # | Collection | Django class | Declared on | Left relation | Right relation | Unique constraint (partial, `WHERE deleted_at = ''`) |
|---|---|---|---|---|---|---|
| J1 | `issue_assignees` | `IssueAssignee` | `Issue.assignees` | `issue`→`issues` | `assignee`→`users` | **UNIQUE `(issue, assignee)`** |
| J2 | `issue_labels` | `IssueLabel` | `Issue.labels` | `issue`→`issues` | `label`→`labels` | **none** ⚠ |
| J3 | `module_members` | `ModuleMember` | `Module.members` | `module`→`modules` | `member`→`users` | **UNIQUE `(module, member)`** |
| J4 | `draft_issue_assignees` | `DraftIssueAssignee` | `DraftIssue.assignees` | `draft_issue`→`draft_issues` | `assignee`→`users` | **UNIQUE `(draft_issue, assignee)`** |
| J5 | `draft_issue_labels` | `DraftIssueLabel` | `DraftIssue.labels` | `draft_issue`→`draft_issues` | `label`→`labels` | **none** ⚠ |
| J6 | `page_labels` | `PageLabel` | `Page.labels` | `page`→`pages` | `label`→`labels` | **none** ⚠ |
| J7 | `project_pages` | `ProjectPage` | `Page.projects` | `project`→`projects` | `page`→`pages` | **UNIQUE `(project, page)`** |

⚠ **J2, J5 and J6 have no uniqueness constraint in Plane.** Duplicate rows are physically
possible today. Do **not** add a unique index — it would change observable behaviour and
could make a data migration fail on existing duplicates. If uniqueness is desired it must
be introduced together with a dedupe step and an API-level change.

### 5.2 Explicit relation tables that are semantically joins

These are not declared via `ManyToManyField` but are join tables in every practical sense —
each row asserts a relationship between two entities, and the API exposes them as
sub-resources.

| # | Collection | Django class | Left relation | Right relation | Extra payload | Unique constraint |
|---|---|---|---|---|---|---|
| J8 | `workspace_members` | `WorkspaceMember` | `workspace`→`workspaces` | `member`→`users` | `role`, `company_role`, `view_props`, `default_props`, `issue_props`, `is_active`, 3 JSON blobs | **UNIQUE `(workspace, member)`** partial |
| J9 | `project_members` | `ProjectMember` | `project`→`projects` | `member`→`users` | `comment`, `role`, `view_props`, `default_props`, `preferences`, `sort_order`, `is_active` | **UNIQUE `(project, member)`** partial |
| J10 | `project_public_members` | `ProjectPublicMember` | `project`→`projects` | `member`→`users` | — | **UNIQUE `(project, member)`** partial |
| J11 | `module_issues` | `ModuleIssue` | `module`→`modules` | `issue`→`issues` | — | **UNIQUE `(issue, module)`** partial |
| J12 | `cycle_issues` | `CycleIssue` | `cycle`→`cycles` | `issue`→`issues` | — | **UNIQUE `(cycle, issue)`** partial |
| J13 | `draft_issue_modules` | `DraftIssueModule` | `draft_issue`→`draft_issues` | `module`→`modules` | — | **UNIQUE `(draft_issue, module)`** partial |
| J14 | `draft_issue_cycles` | `DraftIssueCycle` | `draft_issue`→`draft_issues` | `cycle`→`cycles` | — | **UNIQUE `(draft_issue, cycle)`** partial |
| J15 | `issue_mentions` | `IssueMention` | `issue`→`issues` | `mention`→`users` | — | **UNIQUE `(issue, mention)`** partial |
| J16 | `issue_subscribers` | `IssueSubscriber` | `issue`→`issues` | `subscriber`→`users` | — | **UNIQUE `(issue, subscriber)`** partial |
| J17 | `issue_votes` | `IssueVote` | `issue`→`issues` | `actor`→`users` | `vote` number(−1/1) | **UNIQUE `(issue, actor)`** partial |
| J18 | `issue_reactions` | `IssueReaction` | `issue`→`issues` | `actor`→`users` | `reaction` text | **UNIQUE `(issue, actor, reaction)`** partial |
| J19 | `comment_reactions` | `CommentReaction` | `comment`→`issue_comments` | `actor`→`users` | `reaction` text | **UNIQUE `(comment, actor, reaction)`** partial |
| J20 | `project_issue_types` | `ProjectIssueType` | `project`→`projects` | `issue_type`→`issue_types` | `level`, `is_default` | **UNIQUE `(project, issue_type)`** partial |
| J21 | `page_logs` | `PageLog` | `page`→`pages` | `transaction` (uuid) | `entity_name`, `entity_type`, `entity_identifier`, `workspace` | **UNIQUE `(page, transaction)`** — **hard**, no `deleted_at` |
| J22 | `project_deploy_boards` | `ProjectDeployBoard` | `project`→`projects` | `anchor` (text) | `comments`, `reactions`, `votes`, `views`, `intake` | **UNIQUE `(project, anchor)`** — **hard**, no `deleted_at` |
| J23 | `project_identifiers` | `ProjectIdentifier` | `project`→`projects` (1:1) | `name` | `workspace` | **UNIQUE `(name, workspace)`** partial + **UNIQUE `(project)`** |

### 5.3 Excluded join tables (for reference)

`workspace_integrations` UNIQUE `(workspace, integration)` hard ·
`project_webhooks` UNIQUE `(project, webhook)` partial ·
`github_repository_syncs` UNIQUE `(project, repository)` hard ·
`github_issue_syncs` UNIQUE `(repository_sync, issue)` hard ·
`github_comment_syncs` UNIQUE `(issue_sync, comment)` hard ·
`slack_project_syncs` UNIQUE `(team_id, project)` hard ·
`device_sessions` **no** unique. All `EXCLUDED` (§4).

### 5.4 One-to-one relations (not joins, listed for completeness)

`Profile.user` (unique) · `IssueComment.description` (unique) ·
`ProjectIdentifier.project` (unique, see J23) · `GithubRepositorySync.repository` (unique,
excluded).

---

## 6. Migration ordering

### 6.1 Why ordering is non-trivial

PocketBase validates relation targets **at collection-save time**, before the row is
written. `RelationField.ValidateSettings` → `checkCollectionId`
(`core/field_relation.go:251`) calls `app.FindCachedCollectionByNameOrId(f.CollectionId)`
and returns `validation_missing_rel_collection` if the target is absent. The validation hook
`OnCollectionValidate` fires *before* `OnCollectionCreateExecute`
(`core/collection_model.go:270,290`), so a collection cannot reference itself or a
collection that does not yet exist — **including self-referencing relations**.

Two consequences:

1. Every relation target must already exist when the field is defined.
2. Self-relations (`parent` on `issues`, `issue_comments`, `pages`, `labels`,
   `user_favorites`) and the `users`/`workspaces`/`projects` ↔ `file_assets` cycle
   **cannot** be declared inline. They must be added by a later
   `PATCH /api/collections/{id}` that appends the field.

### 6.2 Normative three-phase plan

Execute in exactly this shape. Phases 1 and 3 are ordered; phase 2's internal order does not
matter once phase 1 is complete.

| Phase | Action |
|---|---|
| **1** | Create every collection with **all scalar / `json` / `file` / `editor` fields**, the `uuid` field, single-column unique indexes on scalar fields, and every relation field whose target already exists at that point in the wave order (§6.3). Defer the fields listed in §6.4. |
| **2** | For each deferred field, `PATCH` the collection to append it as a `relation`. All targets now exist. |
| **3** | Append every index that references a relation column (§6.5). These could not be created earlier because the column did not exist. |

This plan is robust: it never depends on resolving a cycle by hand.

### 6.3 Phase 1 — dependency-ordered create waves

Each wave lists only collections whose **non-deferred** relations are already satisfiable.
Deferred fields are listed per wave and collected in §6.4.

| Wave | Collections created | Relation fields **deferred** in this wave |
|---|---|---|
| **W1** | `users` (auth) | `avatar_asset`, `cover_image_asset` |
| **W2** | `workspaces`, `sessions`, `api_activity_logs` | `workspaces.logo_asset` |
| **W3** | `file_assets`, `profiles`, `accounts`, `api_tokens` | `file_assets.draft_issue`, `.project`, `.issue`, `.comment`, `.page` |
| **W4** | *(back-fill)* append `users.avatar_asset`, `users.cover_image_asset`, `workspaces.logo_asset` | — |
| **W5** | `workspace_members`, `workspace_member_invites`, `teams`, `workspace_themes`, `workspace_user_properties`, `workspace_home_preferences`, `workspace_user_preferences`, `analytic_views`, `issue_types`, `stickies`, `email_notification_logs` | — |
| **W6** | `projects` | `estimate`, `default_state` |
| **W7** | `states`, `estimates`, `estimate_points`, `project_members`, `project_member_invites`, `project_public_members`, `project_user_properties`, `project_identifiers`, `project_issue_types`, `labels` | `labels.parent` |
| **W8** | *(back-fill)* append `projects.estimate`, `projects.default_state`, `labels.parent` | — |
| **W9** | `issues`, `cycles`, `modules`, `intakes`, `descriptions`, `notifications`, `user_notification_preferences`, `workspace_user_links`, `deploy_boards` | `issues.parent` |
| **W10** | `issue_sequences`, `issue_blockers`, `issue_relations`, `issue_mentions`, `issue_assignees`, `issue_links`, `issue_attachments`, `issue_labels`, `issue_subscribers`, `issue_reactions`, `issue_votes`, `issue_comments`, `comment_reactions`, `issue_activities`, `issue_versions`, `issue_description_versions`, `cycle_issues`, `cycle_user_properties`, `module_members`, `module_issues`, `module_links`, `module_user_properties`, `intake_issues`, `draft_issues`, `draft_issue_assignees`, `draft_issue_labels`, `draft_issue_modules`, `draft_issue_cycles`, `issue_views`, `user_favorites`, `user_recent_visits`, `pages`, `page_logs`, `page_labels`, `project_pages`, `page_versions`, `description_versions`, `project_deploy_boards` | `issue_comments.parent`, `pages.parent`, `user_favorites.parent` |
| **W11** | *(back-fill)* append `issues.parent`, `issue_comments.parent`, `pages.parent`, `user_favorites.parent`, `file_assets.draft_issue`, `file_assets.project`, `file_assets.issue`, `file_assets.comment`, `file_assets.page` | — |
| **W12** | Append all relation-bearing indexes (§6.5) | — |

Ordering constraints that justify the waves:

* `workspaces` before `file_assets` before `projects`: `file_assets.workspace` and
  `projects.cover_image_asset` are satisfiable only after their targets exist.
* `projects` before `states` / `estimates` / `project_*`: every `P-block` collection
  requires `projects` and `workspaces`.
* `estimates` before `estimate_points` before `issues` (via `issues.estimate_point`).
* `states` before `issues` (via `issues.state`) and before `projects.default_state` back-fill.
* `issue_types` before `project_issue_types` and before `issues.type`.
* `descriptions` before `issue_comments.description`; `issue_comments` before
  `issue_activities.issue_comment`, `comment_reactions.comment` and `file_assets.comment`.
* `issue_activities` before `issue_versions.activity`.
* `issues` before `cycles`-independent join tables and before `draft_issues.parent`.
* `cycles` and `modules` before `draft_issue_cycles` / `draft_issue_modules`.
* `intakes` before `intake_issues` and before `deploy_boards.intake` /
  `project_deploy_boards.intake`.
* `pages` before `page_logs`, `page_labels`, `project_pages`, `page_versions` and
  `file_assets.page`.
* `labels` before `issue_labels`, `page_labels`, `draft_issue_labels`.
* `users` before everything that has `created_by`/`updated_by`.

### 6.4 Phase 2 — the complete deferred-relation list

Exactly **fourteen** relation fields cannot be declared inline:

| # | Collection | Field | Target | Why deferred |
|---|---|---|---|---|
| D1 | `users` | `avatar_asset` | `file_assets` | cycle: `file_assets.user` → `users` |
| D2 | `users` | `cover_image_asset` | `file_assets` | cycle (same) |
| D3 | `workspaces` | `logo_asset` | `file_assets` | cycle: `file_assets.workspace` → `workspaces` |
| D4 | `projects` | `cover_image_asset` | `file_assets` | cycle: `file_assets.project` → `projects` |
| D5 | `file_assets` | `draft_issue` | `draft_issues` | target created in W10 |
| D6 | `file_assets` | `project` | `projects` | target created in W6 |
| D7 | `file_assets` | `issue` | `issues` | target created in W9 |
| D8 | `file_assets` | `comment` | `issue_comments` | target created in W10 |
| D9 | `file_assets` | `page` | `pages` | target created in W10 |
| D10 | `labels` | `parent` | `labels` | **self-relation** |
| D11 | `issues` | `parent` | `issues` | **self-relation** |
| D12 | `issue_comments` | `parent` | `issue_comments` | **self-relation** |
| D13 | `pages` | `parent` | `pages` | **self-relation** |
| D14 | `user_favorites` | `parent` | `user_favorites` | **self-relation** |

### 6.5 Phase 3 — relation-bearing indexes

Create these only after §6.4. Names are the Django constraint names, preserved verbatim so
the spec stays traceable.

| Collection | Index DDL |
|---|---|
| `workspace_members` | `CREATE UNIQUE INDEX workspace_member_unique_workspace_member_when_deleted_at_null ON workspace_members (workspace, member) WHERE deleted_at = ''` |
| `workspace_member_invites` | `CREATE UNIQUE INDEX workspace_member_invite_unique_email_workspace_when_deleted_at_null ON workspace_member_invites (email, workspace) WHERE deleted_at = ''` |
| `teams` | `CREATE UNIQUE INDEX team_unique_name_workspace_when_deleted_at_null ON teams (name, workspace) WHERE deleted_at = ''` |
| `workspace_themes` | `CREATE UNIQUE INDEX workspace_theme_unique_workspace_name_when_deleted_at_null ON workspace_themes (workspace, name) WHERE deleted_at = ''` |
| `workspace_user_properties` | `CREATE UNIQUE INDEX workspace_user_properties_unique_workspace_user_when_deleted_at_null ON workspace_user_properties (workspace, user) WHERE deleted_at = ''` |
| `workspace_home_preferences` | `CREATE UNIQUE INDEX workspace_user_home_preferences_unique_workspace_user_key_when_deleted_at_null ON workspace_home_preferences (workspace, user, key) WHERE deleted_at = ''` |
| `workspace_user_preferences` | `CREATE UNIQUE INDEX workspace_user_preferences_unique_workspace_user_key_when_deleted_at_null ON workspace_user_preferences (workspace, user, key) WHERE deleted_at = ''` |
| `projects` | `CREATE UNIQUE INDEX project_unique_identifier_workspace_when_deleted_at_null ON projects (identifier, workspace) WHERE deleted_at = ''`<br>`CREATE UNIQUE INDEX project_unique_name_workspace_when_deleted_at_null ON projects (name, workspace) WHERE deleted_at = ''` |
| `project_members` | `CREATE UNIQUE INDEX project_member_unique_project_member_when_deleted_at_null ON project_members (project, member) WHERE deleted_at = ''` |
| `project_public_members` | `CREATE UNIQUE INDEX project_public_member_unique_project_member_when_deleted_at_null ON project_public_members (project, member) WHERE deleted_at = ''` |
| `project_user_properties` | `CREATE UNIQUE INDEX project_user_property_unique_user_project_when_deleted_at_null ON project_user_properties (user, project) WHERE deleted_at = ''` |
| `project_identifiers` | `CREATE UNIQUE INDEX unique_name_workspace_when_deleted_at_null ON project_identifiers (name, workspace) WHERE deleted_at = ''`<br>`CREATE UNIQUE INDEX idx_project_identifiers_project ON project_identifiers (project)` |
| `project_issue_types` | `CREATE UNIQUE INDEX project_issue_type_unique_project_issue_type_when_deleted_at_null ON project_issue_types (project, issue_type) WHERE deleted_at = ''` |
| `project_pages` | `CREATE UNIQUE INDEX project_page_unique_project_page_when_deleted_at_null ON project_pages (project, page) WHERE deleted_at = ''` |
| `project_deploy_boards` | `CREATE UNIQUE INDEX project_deploy_boards_project_anchor ON project_deploy_boards (project, anchor)` |
| `states` | `CREATE UNIQUE INDEX state_unique_name_project_when_deleted_at_null ON states (name, project) WHERE deleted_at = ''` |
| `estimates` | `CREATE UNIQUE INDEX estimate_unique_name_project_when_deleted_at_null ON estimates (name, project) WHERE deleted_at = ''` |
| `modules` | `CREATE UNIQUE INDEX module_unique_name_project_when_deleted_at_null ON modules (name, project) WHERE deleted_at = ''` |
| `intakes` | `CREATE UNIQUE INDEX intake_unique_name_project_when_deleted_at_null ON intakes (name, project) WHERE deleted_at = ''` |
| `labels` | `CREATE UNIQUE INDEX unique_name_when_project_null_and_not_deleted ON labels (name) WHERE project = '' AND deleted_at = ''`<br>`CREATE UNIQUE INDEX unique_project_name_when_not_deleted ON labels (project, name) WHERE project != '' AND deleted_at = ''` |
| `issue_assignees` | `CREATE UNIQUE INDEX issue_assignee_unique_issue_assignee_when_deleted_at_null ON issue_assignees (issue, assignee) WHERE deleted_at = ''` |
| `issue_mentions` | `CREATE UNIQUE INDEX issue_mention_unique_issue_mention_when_deleted_at_null ON issue_mentions (issue, mention) WHERE deleted_at = ''` |
| `issue_subscribers` | `CREATE UNIQUE INDEX issue_subscriber_unique_issue_subscriber_when_deleted_at_null ON issue_subscribers (issue, subscriber) WHERE deleted_at = ''` |
| `issue_votes` | `CREATE UNIQUE INDEX issue_vote_unique_issue_actor_when_deleted_at_null ON issue_votes (issue, actor) WHERE deleted_at = ''` |
| `issue_reactions` | `CREATE UNIQUE INDEX issue_reaction_unique_issue_actor_reaction_when_deleted_at_null ON issue_reactions (issue, actor, reaction) WHERE deleted_at = ''` |
| `comment_reactions` | `CREATE UNIQUE INDEX comment_reaction_unique_comment_actor_reaction_when_deleted_at_null ON comment_reactions (comment, actor, reaction) WHERE deleted_at = ''` |
| `issue_relations` | `CREATE UNIQUE INDEX issue_relation_unique_issue_related_issue_when_deleted_at_null ON issue_relations (issue, related_issue) WHERE deleted_at = ''` |
| `issue_comments` | `CREATE UNIQUE INDEX idx_issue_comments_description ON issue_comments (description)` |
| `cycle_issues` | `CREATE UNIQUE INDEX cycle_issue_when_deleted_at_null ON cycle_issues (cycle, issue) WHERE deleted_at = ''` |
| `cycle_user_properties` | `CREATE UNIQUE INDEX cycle_user_properties_unique_cycle_user_when_deleted_at_null ON cycle_user_properties (cycle, user) WHERE deleted_at = ''` |
| `module_members` | `CREATE UNIQUE INDEX module_member_unique_module_member_when_deleted_at_null ON module_members (module, member) WHERE deleted_at = ''` |
| `module_issues` | `CREATE UNIQUE INDEX module_issue_unique_issue_module_when_deleted_at_null ON module_issues (issue, module) WHERE deleted_at = ''` |
| `module_user_properties` | `CREATE UNIQUE INDEX module_user_properties_unique_module_user_when_deleted_at_null ON module_user_properties (module, user) WHERE deleted_at = ''` |
| `draft_issue_assignees` | `CREATE UNIQUE INDEX draft_issue_assignee_unique_issue_assignee_when_deleted_at_null ON draft_issue_assignees (draft_issue, assignee) WHERE deleted_at = ''` |
| `draft_issue_modules` | `CREATE UNIQUE INDEX module_draft_issue_unique_issue_module_when_deleted_at_null ON draft_issue_modules (draft_issue, module) WHERE deleted_at = ''` |
| `draft_issue_cycles` | `CREATE UNIQUE INDEX draft_issue_cycle_when_deleted_at_null ON draft_issue_cycles (draft_issue, cycle) WHERE deleted_at = ''` |
| `user_favorites` | `CREATE UNIQUE INDEX user_favorite_unique_entity_type_entity_identifier_user_when_deleted_at_null ON user_favorites (entity_type, entity_identifier, user) WHERE deleted_at = ''` |
| `deploy_boards` | `CREATE UNIQUE INDEX deploy_board_unique_entity_name_entity_identifier_when_deleted_at_null ON deploy_boards (entity_name, entity_identifier) WHERE deleted_at = ''` |
| `page_logs` | `CREATE UNIQUE INDEX page_logs_page_transaction ON page_logs (page, transaction)` |

**Indexes safe to create in phase 1** (they reference only scalar columns):
`idx_<table>_uuid` (every collection), `idx_workspaces_slug`, `idx_users_username`,
`idx_users_email` (PB auth system index), `idx_accounts_provider_account`,
`idx_api_tokens_token`, `idx_deploy_boards_anchor`, `idx_project_deploy_boards_anchor`,
`idx_profiles_user`, `idx_project_identifiers_legacy_pk`, `idx_<table>_deleted_at`,
`idx_page_logs_*` (the five entity indexes), `idx_notifications_*` (all nine),
`idx_file_assets_*` (all four), `idx_issues_project_state`,
`idx_issues_sequence_id`, `idx_issue_sequences_sequence`,
`idx_issue_activities_issue`, `idx_issue_activities_comment`,
`idx_sessions_user_id`.

### 6.6 Verification checklist for the migration author

1. Count created collections — must equal the §8 kept count.
2. Every collection has a field named `uuid` at position 1, except `project_identifiers`
   (uses `legacy_pk`) and `sessions` (uses `session_key`).
3. Every collection has `deleted_at`, except `users`, `profiles`, `accounts` and
   `sessions`.
4. No collection has a relation to a collection in §4.
5. Every relation field in §3 has `cascadeDelete` set per §1.3(k) — in particular
   `issue_activities.issue`, `issue_activities.issue_comment`,
   `issue_sequences.issue` and `issue_versions.activity` must be `cascadeDelete=false`.
6. Every partial unique index in §6.5 exists and contains a `WHERE deleted_at = ''` clause
   except the three hard uniques (`page_logs`, `project_deploy_boards`, and the excluded
   ones).
7. `issues.description_binary` and the six other `BinaryField`s are `file` fields, and the
   data import writes them through the file API, not record JSON.

---

## 7. Sharp edges

Django constructs with **no clean PocketBase equivalent**. Each entry states the problem,
the proposed workaround, and the residual risk.

### 7.1 No `NULL` for non-`json` columns

**Problem.** `field_json.go:116` is the only `ColumnType` that emits `DEFAULT NULL`. `text`,
`number`, `bool`, `date`, `select`, `relation` and `file` are all `NOT NULL` with a zero
default. Django uses `null=True` on ~70 columns across the schema.

**Workaround.** Zero-value-as-NULL translation in the API layer (§1.3(e)), escalating to
`json` for the two genuine collisions (`Issue.point`, `IssueActivity.epoch`).

**Residual risk.** Any consumer that queries SQLite directly (analytics, admin UI, raw
reports) will see `''`/`0`/`false` where Plane would have `NULL`. Every such query must go
through the API layer's null-coercion. Document it in the Go data-access layer, not at each
call site.

### 7.2 No field-level `default`

**Problem.** No PocketBase field type has a `default` option.

**Workaround.** Apply all Django defaults in the Go service layer before insert (§1.3(i)).
The `get_default_*()` factories must be ported verbatim because their exact key sets are
part of the API contract. Watch the **two different** `get_default_filters()` /
`get_default_display_filters()` implementations: `issue.py`'s return bare objects, while
`workspace.py`'s return wrapped objects (`{"display_filters": {...}}`). Both are used.

**Residual risk.** A record inserted by any path that bypasses the service layer (admin UI,
direct API, script) gets zero values instead of Plane defaults. Mitigate with a
collection-level create hook if the admin UI must remain usable.

### 7.3 `BinaryField` → `file`, and file fields are not importable via JSON

**Problem.** Seven `BinaryField` columns hold Yjs collaborative-document state. PocketBase
has no binary type; `file` stores bytes on disk and the record value is only the filename.
Critically, **a `file` field cannot be populated by writing record JSON** — the value must
be an uploaded `*filesystem.File` (`field_file.go:250`, *"disallow new plain string
filenames submission"*).

**Workaround.** Model as `file` (§1.3(c)) and perform the data migration through the file
upload API or by writing to the PB storage directory and then referencing the generated
filename.

**Residual risk.** This is the most fragile part of the data migration: it is the only place
where the SQLite row alone is not the whole record. Plan the import to run after the schema
is in place and to be resumable.

### 7.4 `FileField.upload_to` path shape changes

**Problem.** `FileAsset.asset` and `IssueAttachment.asset` store paths of the form
`{workspace_uuid}/{uuid4}-{sanitized_filename}`. PocketBase stores
`{collectionId}/{recordId}/{filename}` and generates the random prefix itself.

**Workaround.** None needed for correctness — accept the new layout and reimplement
`FileAsset.asset_url` (four distinct URL shapes keyed on `entity_type`, `asset.py:83`) in
the Go API layer so clients see unchanged URLs.

**Residual risk.** Any externally stored URL or webhook payload containing an old asset path
will not resolve. Existing `FileAsset.asset` string values must be treated as opaque
identifiers, not paths.

### 7.5 `on_delete=DO_NOTHING` has no equivalent

**Problem.** PocketBase's only non-cascading behaviour is to **unset** the reference
(`record_model.go:1633`). Django's `DO_NOTHING` leaves it in place.

**Workaround.** Accept the unset for `IssueActivity.issue` and `IssueActivity.issue_comment`
— `old_identifier`/`new_identifier` already carry the identity for exactly this reason.
Alternatively block it with a `OnRecordDelete` hook.

**Residual risk.** After an issue is hard-deleted, its activity rows lose the `issue`
relation but keep `old_identifier`. Verify that the activity-feed serializers fall back to
`old_identifier` when `issue` is empty.

### 7.6 Soft delete and its cascade have no PocketBase equivalent

**Problem.** PocketBase deletes are hard. Plane's `SoftDeletionManager` filters
`deleted_at IS NULL`, and `SoftDeleteModel.delete()` (`db/mixins.py:72`) enqueues
`soft_delete_related_objects` to cascade the soft delete across related models. PocketBase's
`cascadeDelete` only fires on **hard** delete.

**Workaround.** Keep `deleted_at` as a `date` field and implement, in Go:
(a) the `deleted_at = ''` filter on every list query; (b) a `DELETE` handler that sets
`deleted_at` and walks the relationship graph to soft-delete children; (c) the
`Workspace.delete()` slug-rewrite special case (§3.2); (d) the
`soft_delete_related_objects` model graph — port it from
`plane/bgtasks/deletion_task.py`.

**Residual risk.** This is the largest behavioural port in the project. If (b) is
incomplete, records will "reappear" because children remain active while their parent is
soft-deleted. Consider a hard-delete fallback for the excluded collections only.

### 7.7 Django field-level `choices` vs PocketBase `select` validation strictness

**Problem.** PB `select` **rejects** any value not in `Values`
(`field_select.go:204`). Django `choices` are validated at the form/serializer layer only —
the database accepts anything. Existing PostgreSQL rows may contain values outside the
declared choices (e.g. a renamed enum member, or a `timezone` string not in the current
`pytz` list).

**Workaround.** Before creating a `select` field, run a `SELECT DISTINCT` over the source
column and confirm every value is in `Values`. Where it is not, either widen `Values` or
keep the column as `text`.

**Residual risk.** A data migration will fail hard on the first out-of-range value. This is
the most likely cause of an import abort — audit every `select` column first.

### 7.8 `ArrayField` length caps and element validation are lost

**Problem.** `IssueActivity.attachments` and `IssueComment.attachments` are
`ArrayField(URLField(), size=10)`; `ExporterHistory.project` is
`ArrayField(UUIDField())`. PB `json` enforces only a byte `MaxSize`.

**Workaround.** Validate array length and element format in the Go layer.

**Residual risk.** None for existing data (Plane already enforced it on write); only new
writes through the PB admin UI or raw API could exceed the cap.

### 7.9 `DateTimeField` vs `DateField` — PB has one date type

**Problem.** PB `date` stores a full `types.DateTime` (`field_date.go:21`). Plane's
`DateField` columns are date-only, and the API returns `"2024-05-01"`, while
`DateTimeField` columns return an ISO timestamp.

**Workaround.** Store date-only columns at midnight UTC and truncate to `YYYY-MM-DD` in the
serializer; expose full timestamps for `DateTimeField` columns. Keep a per-field list of
which is which — the mixed cases inside one collection are the trap:
`Issue.archived_at` is `DateField` while `Issue.completed_at` is `DateTimeField`;
`Project.archived_at` is `DateTimeField` while `Page.archived_at` is `DateField`.

**Residual risk.** A timezone-shifted midnight can render as the previous day. Always
normalise date-only columns to UTC midnight on write.

### 7.10 `autodate` fields cannot be written through the normal setter

**Problem.** `AutodateField.FindSetter` returns `noopSetter` (`field_autodate.go:154`), so
`record.Set("created_at", ...)` is silently ignored. Plane's data must keep its original
timestamps.

**Workaround.** The import must use `record.SetRaw(...)`; the interceptor explicitly permits
a manually set value (`field_autodate.go:174`). Alternatively create the fields as plain
`date` for the import and convert them to `autodate` afterwards.

**Residual risk.** If the import uses `Set()`, every `created_at` becomes "now" and all
ordering, activity feeds and `IssueSequence` history become meaningless.

### 7.11 Django password hashes are not PocketBase password hashes

**Problem.** `users` becomes a PB auth collection, so `password` is a system field with PB's
own hashing (bcrypt/argon2). Plane stores Django PBKDF2 hashes in
`<algorithm>$<iterations>$<salt>$<hash>` form.

**Workaround.** Either (a) force a password reset for all users on cutover, or (b) implement
a one-time migration that verifies the Django hash on first login and then rehashes into PB's
format via a custom auth hook.

**Residual risk.** Option (a) is a user-visible disruption; option (b) requires storing the
legacy hash somewhere (a `legacy_password_hash` text field) and a custom
`OnRecordAuthRequest` hook. **Decide before the migration is written** — this affects whether
`users` needs an extra field.

### 7.12 Issue sequence allocation depended on a PostgreSQL advisory lock

**Problem.** `Issue.save()` (`issue.py:189`) serialises sequence allocation per project with
`SELECT pg_advisory_xact_lock(convert_uuid_to_integer(project.id))`. SQLite has no advisory
locks, and PB transactions are optimistic.

**Workaround.** Reimplement as a single SQLite transaction that reads `MAX(sequence)` and
inserts in one atomic unit, relying on SQLite's writer lock (or PB's
`RunInTransaction`). Keep the `issue_sequences` table as the allocator of record.

**Residual risk.** Under concurrent writes SQLite serialises writers anyway, so correctness
is achievable — but the implementation must not split the read and the write across two
transactions. This is the highest-risk behaviour port in the schema.

### 7.13 `IssueComment.save()` maintains a second collection

**Problem.** Saving a comment creates or updates a `Description` row and mirrors three
fields, using `ChangeTrackerMixin._changes_on_save` to detect which changed
(`issue.py:481`). PB has no per-save change tracking.

**Workaround.** Implement in a Go create/update hook on `issue_comments`: upsert the linked
`descriptions` row inside the same transaction and copy
`comment_html/stripped/json` → `description_html/stripped/json`. Recompute
`description_stripped` from `description_html` on both sides.

**Residual risk.** If this hook is missing or non-transactional, `descriptions` and
`description_versions` diverge from `issue_comments` and page/comment rich-text restore
breaks silently.

### 7.14 No per-collection default ordering

**Problem.** Every Plane model declares `Meta.ordering` (usually `("-created_at",)`, but
`states` uses `("sequence",)`, `estimates`/`intakes` use `("name",)`,
`project_issue_types` uses `("project","issue_type")`).

**Workaround.** Apply the sort in the Go list handlers. PB has no collection-level default
`ORDER BY`.

**Residual risk.** Pagination without an explicit sort is unstable in PB and can duplicate
or skip rows across pages. Every list endpoint must set an explicit sort — this is a
correctness issue, not a nicety.

### 7.15 `entity_identifier` polymorphism is not expressible as a relation

**Problem.** Seven collections reference other entities by a bare UUID plus a discriminator
(§2.5).

**Workaround.** Store Plane UUIDs in `text` and resolve through the target's `uuid` field.

**Residual risk.** No referential integrity: a deleted entity leaves dangling
`entity_identifier` values. Plane has the same weakness, so this is behaviour-preserving —
but it means the Go layer must handle "target not found" gracefully in notifications,
recent visits, favourites and page logs.

### 7.16 `select` fields with very large `Values` lists

**Problem.** `Workspace.timezone`, `Project.timezone`, `Cycle.timezone` and
`User.user_timezone` carry `choices=pytz.common_timezones` (~600 values).

**Workaround.** Model as `text` with `Max=255` and validate against `timezone.available` in
Go (§1.3(h)). Flagged `REVIEW` in §3.

**Residual risk.** Loses the DB-level enumeration guarantee, which Plane did not have
either (Django `choices` are not DB constraints).

### 7.17 `SlugField` / `CharField` `db_index` is not a uniqueness guarantee

**Problem.** Several `db_index=True` columns (`Project.identifier`, `State.slug`,
`APIToken.token`, `DeployBoard.anchor`, `PageLog.entity_identifier`,
`Notification.entity_identifier`, `Session.user_id`) are indexed but not necessarily
unique.

**Workaround.** Create plain (non-unique) indexes for the genuinely non-unique ones and
unique indexes only where Django says `unique=True`. The distinction is recorded per field
in §3.

**Residual risk.** Adding a unique index where Plane had only `db_index` will fail on
existing duplicate rows and change behaviour. Audit before adding.

### 7.18 Django `DecimalField` and `GenericIPAddressField`

`DecimalField` is unused in Plane. `GenericIPAddressField` appears twice
(`APIActivityLog.ip_address`, `DeviceSession.ip_address` — the latter excluded); map to
`text` with an IPv4/IPv6 `Pattern`. No PB equivalent exists.

---

## 8. Coverage audit

### 8.1 Counts

| Category | Count |
|---|---|
| Model classes catalogued | **103** |
| — abstract bases (`BaseModel`, `AuditModel`, `TimeAuditModel`, `UserAuditModel`, `SoftDeleteModel`, `WorkspaceBaseModel`, `ProjectBaseModel`) | 7 |
| — concrete models | **96** |
| — of which `EXCLUDED` (brief §4 list) | 15 |
| — of which additional models not in the brief (§3.17, `RECOMMENDED EXCLUDE`) | 4 |
| **Collections to create** (96 − 15 − 4) | **77** |
| — `EXCLUDED` but still referenced by kept code (call sites only, §4.2) | 3 (`webhooks`, `webhook_logs`, `project_webhooks`) |
| Collections to create if the licence models are **kept** instead | 81 |
| Non-model Django framework tables excluded (§4.3) | 8 |

The brief's list contained 92 of the 96 concrete models. The four additions are the
`license/models/instance.py` models: `Instance`, `InstanceAdmin`, `InstanceConfiguration`,
`ChangeLog` (§3.17). Nothing in the brief's list was wrong — no model was renamed, merged or
found to be abstract.

### 8.2 Corrections made to the brief's list

1. **`ProjectNetwork` is not a model.** `db/models/__init__.py:57` exports `ProjectNetwork`,
   but it is an `Enum` (`project.py:30`) used for `Project.network`'s choices. It has no
   table and no section here.
2. **Four models added** — the `license` app models (§3.17).
3. **`Integration` is not `BaseModel`-derived.** It inherits `AuditModel` and declares its
   own UUID `id` (`integration/base.py:16`), which is why it still has a UUID PK despite not
   going through `BaseModel`.
4. **`ProjectIdentifier` has an integer PK, not a UUID** — it inherits `AuditModel` without
   redefining `id` (§2.7, §3.3). This was the one genuine structural surprise.
5. **`Profile` and `Account` have no `deleted_at` and no `created_by`/`updated_by`** — they
   inherit `TimeAuditModel` only (§3.1, §3.1).
6. **`User` has no `deleted_at`** and is an auth collection, not a plain base collection
   (§3.1).
7. **`Session` has a non-UUID primary key** and no audit columns (§3.1).
8. **`db_table` for `ProjectMemberInvite` is `project_member_invites`** (plural). Migration
   `0001_initial.py` created `project_member_invite`; the current model Meta is
   authoritative.
9. **`Team`, `IssueBlocker`, `IssueLabel`, `DraftIssueLabel`, `PageLabel`,
   `UserNotificationPreference`, `UserRecentVisit`, `IntakeIssue` and `Cycle` have no
   unique constraint at all** — do not invent one (§5.1, §7.17).
10. **`ExporterHistory`'s table is `exporters`** and `ChangeLog`'s is `changelogs` — the
    table name does not match the class name in either case.

### 8.3 Deliverable checklist against the brief

| Requirement | Where |
|---|---|
| Type mapping table with non-obvious cases justified | §1.2, §1.3 |
| UUID PK strategy with collection definition + serializer | §2.2, §2.3, §2.4, §2.5 |
| Per-model collection spec (name, abstract?, fields, relations, constraints, `deleted_at`) | §3.0–§3.17 |
| Excluded models tagged with reasons, verified against kept features | §3.16, §4.1, §4.2 |
| Join-table inventory with unique-together constraints | §5 |
| Dependency-ordered creation plan | §6 |
| No clean PocketBase equivalent — stated explicitly with workarounds | §7 (18 entries) |
| One file written, nothing else modified | this file |

### 8.4 Open decisions for the migration owner

1. **Licence/instance models** (§3.17) — exclude (recommended) or migrate?
2. **`sessions`** (§3.1, §4.4) — keep, or replace with PB token invalidation?
3. **`Account`** (§3.1) — keep (it backs a frontend-critical endpoint), or drop with OAuth?
4. **`Team`** (§3.2) — dormant in v1.4.2; create or skip?
5. **Password migration** (§7.11) — reset-all, or legacy-hash field + custom auth hook?
6. **`timezone` fields** (§1.3(h), §7.16) — `text` (recommended) or 600-value `select`?
7. **Soft-delete cascade** (§7.6) — full graph port, or hard delete for some collections?

