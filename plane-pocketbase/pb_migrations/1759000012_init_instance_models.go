// Instance / licensing / changelog models (spec §3.17).
//
// These four models live in Plane's `license` app (a different Django app but the
// same database). The spec flags them `RECOMMENDED EXCLUDE` and they are NOT part
// of the §4 exclusion list, so they are created here. They only depend on `users`.
//
// `instance_admins` carries a hard UNIQUE `(instance, user)` (Django
// `unique_together`, no `deleted_at`); its two columns both exist at creation
// time, so the index is created inline.
package pb_migrations

import (
	"fmt"

	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

// ---------------------------------------------------------------------------
// instances — Django `Instance` (spec §3.17). A-block.
// ---------------------------------------------------------------------------

func buildInstances(usersID string) *core.Collection {
	c := core.NewBaseCollection("instances")
	addAuditFields(c, usersID, true)

	c.Fields.Add(&core.TextField{Name: "instance_name", Required: true, Max: 255})
	c.Fields.Add(&core.TextField{Name: "whitelist_emails", Max: maxTextBlob})
	c.Fields.Add(&core.TextField{Name: "instance_id", Required: true, Max: 255})
	c.Fields.Add(&core.TextField{Name: "current_version", Required: true, Max: 255})
	c.Fields.Add(&core.TextField{Name: "latest_version", Max: 255})
	c.Fields.Add(&core.TextField{Name: "edition", Max: 255})
	c.Fields.Add(&core.TextField{Name: "domain", Max: maxTextBlob})
	c.Fields.Add(&core.DateField{Name: "last_checked_at", Required: true})
	c.Fields.Add(&core.TextField{Name: "namespace", Max: 255})
	c.Fields.Add(&core.BoolField{Name: "is_telemetry_enabled"})
	c.Fields.Add(&core.BoolField{Name: "is_support_required"})
	c.Fields.Add(&core.BoolField{Name: "is_setup_done"})
	c.Fields.Add(&core.BoolField{Name: "is_signup_screen_visited"})
	c.Fields.Add(&core.BoolField{Name: "is_verified"})
	c.Fields.Add(&core.BoolField{Name: "is_test"})
	c.Fields.Add(&core.BoolField{Name: "is_current_version_deprecated"})

	addIdentityIndexes(c, true)
	c.Indexes = append(c.Indexes, "CREATE UNIQUE INDEX `idx_instances_instance_id` ON `instances` (`instance_id`)")

	return c
}

// ---------------------------------------------------------------------------
// instance_admins — Django `InstanceAdmin` (spec §3.17). A-block.
// ---------------------------------------------------------------------------

func buildInstanceAdmins(usersID, instancesID string) *core.Collection {
	c := core.NewBaseCollection("instance_admins")
	addAuditFields(c, usersID, true)

	c.Fields.Add(relation("user", usersID, false, false)) // SET_NULL
	c.Fields.Add(relation("instance", instancesID, true, true))
	c.Fields.Add(&core.NumberField{Name: "role", OnlyInt: true})
	c.Fields.Add(&core.BoolField{Name: "is_verified"})

	addIdentityIndexes(c, true)
	// Hard UNIQUE (instance, user) — Django unique_together, no deleted_at.
	c.Indexes = append(c.Indexes,
		"CREATE UNIQUE INDEX `instance_admin_unique_instance_user` ON `instance_admins` (`instance`, `user`)",
	)

	return c
}

// ---------------------------------------------------------------------------
// instance_configurations — Django `InstanceConfiguration` (spec §3.17). A-block.
// ---------------------------------------------------------------------------

func buildInstanceConfigurations(usersID string) *core.Collection {
	c := core.NewBaseCollection("instance_configurations")
	addAuditFields(c, usersID, true)

	c.Fields.Add(&core.TextField{Name: "key", Required: true, Max: 100})
	c.Fields.Add(&core.TextField{Name: "value", Max: maxTextBlob})
	c.Fields.Add(&core.TextField{Name: "category", Required: true, Max: maxTextBlob})
	c.Fields.Add(&core.BoolField{Name: "is_encrypted"})

	addIdentityIndexes(c, true)
	c.Indexes = append(c.Indexes, "CREATE UNIQUE INDEX `idx_instance_configurations_key` ON `instance_configurations` (`key`)")

	return c
}

// ---------------------------------------------------------------------------
// changelogs — Django `ChangeLog` (spec §3.17). A-block.
// ---------------------------------------------------------------------------

func buildChangelogs(usersID string) *core.Collection {
	c := core.NewBaseCollection("changelogs")
	addAuditFields(c, usersID, true)

	c.Fields.Add(&core.TextField{Name: "title", Required: true, Max: 255})
	c.Fields.Add(&core.TextField{Name: "description", Max: maxTextBlob})
	c.Fields.Add(&core.TextField{Name: "version", Required: true, Max: 255})
	c.Fields.Add(&core.JSONField{Name: "tags"})
	c.Fields.Add(&core.DateField{Name: "release_date"})
	c.Fields.Add(&core.BoolField{Name: "is_release_candidate"})

	addIdentityIndexes(c, true)

	return c
}

func init() {
	m.Register(func(app core.App) error {
		usersID, err := collectionID(app, "users")
		if err != nil {
			return err
		}

		if err := app.Save(buildInstances(usersID)); err != nil {
			return fmt.Errorf("create instances: %w", err)
		}
		instancesID, err := collectionID(app, "instances")
		if err != nil {
			return err
		}
		if err := app.Save(buildInstanceAdmins(usersID, instancesID)); err != nil {
			return fmt.Errorf("create instance_admins: %w", err)
		}
		if err := app.Save(buildInstanceConfigurations(usersID)); err != nil {
			return fmt.Errorf("create instance_configurations: %w", err)
		}
		if err := app.Save(buildChangelogs(usersID)); err != nil {
			return fmt.Errorf("create changelogs: %w", err)
		}

		return nil
	}, func(app core.App) error {
		for _, name := range []string{"changelogs", "instance_configurations", "instance_admins", "instances"} {
			c, err := app.FindCollectionByNameOrId(name)
			if err != nil {
				return fmt.Errorf("down: find %q: %w", name, err)
			}
			if err := app.Delete(c); err != nil {
				return fmt.Errorf("down: delete %q: %w", name, err)
			}
		}
		return nil
	}, "1759000012_init_instance_models.go")
}
