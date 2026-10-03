// Wave W11: phase 2 — append the deferred relation fields now that every target
// exists (spec §6.2 phase 2, §6.4).
//
// The fields still outstanding after the wave-1 back-fill file (1759000004,
// which already added D4/D10/D11) are:
//   D1  users.avatar_asset        -> file_assets
//   D2  users.cover_image_asset   -> file_assets
//   D3  workspaces.logo_asset     -> file_assets
//   D5  file_assets.draft_issue   -> draft_issues
//   D6  file_assets.project       -> projects
//   D7  file_assets.issue         -> issues
//   D8  file_assets.comment       -> issue_comments
//   D9  file_assets.page          -> pages
//   D12 issue_comments.parent     -> issue_comments (self)
//   D13 pages.parent              -> pages (self)
//   D14 user_favorites.parent     -> user_favorites (self)
//
// All of D1–D3/D5–D9 are SET_NULL (non-cascading, nullable) per §1.3(k); the
// three self-relations are CASCADE, nullable.
package pb_migrations

import (
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

func init() {
	m.Register(func(app core.App) error {
		fileAssetsID, err := collectionID(app, "file_assets")
		if err != nil {
			return err
		}
		projectsID, err := collectionID(app, "projects")
		if err != nil {
			return err
		}
		issuesID, err := collectionID(app, "issues")
		if err != nil {
			return err
		}
		commentsID, err := collectionID(app, "issue_comments")
		if err != nil {
			return err
		}
		pagesID, err := collectionID(app, "pages")
		if err != nil {
			return err
		}
		draftIssuesID, err := collectionID(app, "draft_issues")
		if err != nil {
			return err
		}

		// D1/D2/D3 — the users/workspaces -> file_assets cycle.
		if err := patchCollection(app, "users", func(c *core.Collection) {
			c.Fields.Add(relation("avatar_asset", fileAssetsID, false, false))      // SET_NULL
			c.Fields.Add(relation("cover_image_asset", fileAssetsID, false, false)) // SET_NULL
		}); err != nil {
			return err
		}
		if err := patchCollection(app, "workspaces", func(c *core.Collection) {
			c.Fields.Add(relation("logo_asset", fileAssetsID, false, false)) // SET_NULL
		}); err != nil {
			return err
		}

		// D5–D9 — file_assets -> the entities it attaches to.
		if err := patchCollection(app, "file_assets", func(c *core.Collection) {
			c.Fields.Add(relation("draft_issue", draftIssuesID, false, true)) // CASCADE
			c.Fields.Add(relation("project", projectsID, false, true))        // CASCADE
			c.Fields.Add(relation("issue", issuesID, false, true))            // CASCADE
			c.Fields.Add(relation("comment", commentsID, false, true))        // CASCADE
			c.Fields.Add(relation("page", pagesID, false, true))              // CASCADE
		}); err != nil {
			return err
		}

		// D12/D13/D14 — self-relations.
		if err := patchCollection(app, "issue_comments", func(c *core.Collection) {
			c.Fields.Add(relation("parent", c.Id, false, true)) // self, CASCADE
		}); err != nil {
			return err
		}
		if err := patchCollection(app, "pages", func(c *core.Collection) {
			c.Fields.Add(relation("parent", c.Id, false, true)) // self, CASCADE
		}); err != nil {
			return err
		}
		if err := patchCollection(app, "user_favorites", func(c *core.Collection) {
			c.Fields.Add(relation("parent", c.Id, false, true)) // self, CASCADE
		}); err != nil {
			return err
		}

		return nil
	}, func(app core.App) error {
		// Reverse order of the up migration.
		if err := patchCollection(app, "user_favorites", func(c *core.Collection) {
			c.Fields.RemoveByName("parent")
		}); err != nil {
			return err
		}
		if err := patchCollection(app, "pages", func(c *core.Collection) {
			c.Fields.RemoveByName("parent")
		}); err != nil {
			return err
		}
		if err := patchCollection(app, "issue_comments", func(c *core.Collection) {
			c.Fields.RemoveByName("parent")
		}); err != nil {
			return err
		}
		if err := patchCollection(app, "file_assets", func(c *core.Collection) {
			for _, name := range []string{"draft_issue", "project", "issue", "comment", "page"} {
				c.Fields.RemoveByName(name)
			}
		}); err != nil {
			return err
		}
		if err := patchCollection(app, "workspaces", func(c *core.Collection) {
			c.Fields.RemoveByName("logo_asset")
		}); err != nil {
			return err
		}
		if err := patchCollection(app, "users", func(c *core.Collection) {
			for _, name := range []string{"avatar_asset", "cover_image_asset"} {
				c.Fields.RemoveByName(name)
			}
		}); err != nil {
			return err
		}

		return nil
	}, "1759000010_deferred_relations.go")
}
