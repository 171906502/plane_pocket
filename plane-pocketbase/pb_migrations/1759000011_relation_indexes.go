// Wave W12: phase 3 — every remaining partial UNIQUE index that references a
// relation column (spec §6.2 phase 3, §6.5).
//
// These could not be created with their collections because they reference
// relation columns, several of which were only appended in the W11 back-fill.
// Index names are the Django constraint names preserved verbatim (spec §6.5);
// the three hard uniques (`project_deploy_boards`, `page_logs`) carry no
// `WHERE deleted_at = ''` clause.
//
// The wave-1 phase-3 indexes (`projects`, `workspace_members`, `project_members`,
// `states`, `labels`, `issue_assignees`) were already created by
// 1759000004_deferred_relations_and_indexes.go and are NOT repeated here.
//
// `issue_labels`, `draft_issue_labels` and `page_labels` intentionally get no
// unique index (spec §5.1 J2/J5/J6).
package pb_migrations

import (
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

// deferredRelationIndexes maps a collection to the phase-3 index DDL it needs.
var deferredRelationIndexes = map[string][]string{
	"workspace_member_invites": {
		"CREATE UNIQUE INDEX `workspace_member_invite_unique_email_workspace_when_deleted_at_null` ON `workspace_member_invites` (`email`, `workspace`) WHERE deleted_at = ''",
	},
	"teams": {
		"CREATE UNIQUE INDEX `team_unique_name_workspace_when_deleted_at_null` ON `teams` (`name`, `workspace`) WHERE deleted_at = ''",
	},
	"workspace_themes": {
		"CREATE UNIQUE INDEX `workspace_theme_unique_workspace_name_when_deleted_at_null` ON `workspace_themes` (`workspace`, `name`) WHERE deleted_at = ''",
	},
	"workspace_user_properties": {
		"CREATE UNIQUE INDEX `workspace_user_properties_unique_workspace_user_when_deleted_at_null` ON `workspace_user_properties` (`workspace`, `user`) WHERE deleted_at = ''",
	},
	"workspace_home_preferences": {
		"CREATE UNIQUE INDEX `workspace_user_home_preferences_unique_workspace_user_key_when_deleted_at_null` ON `workspace_home_preferences` (`workspace`, `user`, `key`) WHERE deleted_at = ''",
	},
	"workspace_user_preferences": {
		"CREATE UNIQUE INDEX `workspace_user_preferences_unique_workspace_user_key_when_deleted_at_null` ON `workspace_user_preferences` (`workspace`, `user`, `key`) WHERE deleted_at = ''",
	},
	"project_public_members": {
		"CREATE UNIQUE INDEX `project_public_member_unique_project_member_when_deleted_at_null` ON `project_public_members` (`project`, `member`) WHERE deleted_at = ''",
	},
	"project_user_properties": {
		"CREATE UNIQUE INDEX `project_user_property_unique_user_project_when_deleted_at_null` ON `project_user_properties` (`user`, `project`) WHERE deleted_at = ''",
	},
	"project_identifiers": {
		"CREATE UNIQUE INDEX `unique_name_workspace_when_deleted_at_null` ON `project_identifiers` (`name`, `workspace`) WHERE deleted_at = ''",
		"CREATE UNIQUE INDEX `idx_project_identifiers_project` ON `project_identifiers` (`project`)",
	},
	"project_issue_types": {
		"CREATE UNIQUE INDEX `project_issue_type_unique_project_issue_type_when_deleted_at_null` ON `project_issue_types` (`project`, `issue_type`) WHERE deleted_at = ''",
	},
	"project_pages": {
		"CREATE UNIQUE INDEX `project_page_unique_project_page_when_deleted_at_null` ON `project_pages` (`project`, `page`) WHERE deleted_at = ''",
	},
	"project_deploy_boards": {
		"CREATE UNIQUE INDEX `project_deploy_boards_project_anchor` ON `project_deploy_boards` (`project`, `anchor`)",
	},
	"estimates": {
		"CREATE UNIQUE INDEX `estimate_unique_name_project_when_deleted_at_null` ON `estimates` (`name`, `project`) WHERE deleted_at = ''",
	},
	"modules": {
		"CREATE UNIQUE INDEX `module_unique_name_project_when_deleted_at_null` ON `modules` (`name`, `project`) WHERE deleted_at = ''",
	},
	"intakes": {
		"CREATE UNIQUE INDEX `intake_unique_name_project_when_deleted_at_null` ON `intakes` (`name`, `project`) WHERE deleted_at = ''",
	},
	"issue_mentions": {
		"CREATE UNIQUE INDEX `issue_mention_unique_issue_mention_when_deleted_at_null` ON `issue_mentions` (`issue`, `mention`) WHERE deleted_at = ''",
	},
	"issue_subscribers": {
		"CREATE UNIQUE INDEX `issue_subscriber_unique_issue_subscriber_when_deleted_at_null` ON `issue_subscribers` (`issue`, `subscriber`) WHERE deleted_at = ''",
	},
	"issue_votes": {
		"CREATE UNIQUE INDEX `issue_vote_unique_issue_actor_when_deleted_at_null` ON `issue_votes` (`issue`, `actor`) WHERE deleted_at = ''",
	},
	"issue_reactions": {
		"CREATE UNIQUE INDEX `issue_reaction_unique_issue_actor_reaction_when_deleted_at_null` ON `issue_reactions` (`issue`, `actor`, `reaction`) WHERE deleted_at = ''",
	},
	"comment_reactions": {
		"CREATE UNIQUE INDEX `comment_reaction_unique_comment_actor_reaction_when_deleted_at_null` ON `comment_reactions` (`comment`, `actor`, `reaction`) WHERE deleted_at = ''",
	},
	"issue_relations": {
		"CREATE UNIQUE INDEX `issue_relation_unique_issue_related_issue_when_deleted_at_null` ON `issue_relations` (`issue`, `related_issue`) WHERE deleted_at = ''",
	},
	"issue_comments": {
		// Django's unique OneToOne exempts NULL; PocketBase stores '' instead,
		// so the index must be partial or a second comment would collide.
		"CREATE UNIQUE INDEX `idx_issue_comments_description` ON `issue_comments` (`description`) WHERE description != ''",
	},
	"cycle_issues": {
		"CREATE UNIQUE INDEX `cycle_issue_when_deleted_at_null` ON `cycle_issues` (`cycle`, `issue`) WHERE deleted_at = ''",
	},
	"cycle_user_properties": {
		"CREATE UNIQUE INDEX `cycle_user_properties_unique_cycle_user_when_deleted_at_null` ON `cycle_user_properties` (`cycle`, `user`) WHERE deleted_at = ''",
	},
	"module_members": {
		"CREATE UNIQUE INDEX `module_member_unique_module_member_when_deleted_at_null` ON `module_members` (`module`, `member`) WHERE deleted_at = ''",
	},
	"module_issues": {
		"CREATE UNIQUE INDEX `module_issue_unique_issue_module_when_deleted_at_null` ON `module_issues` (`issue`, `module`) WHERE deleted_at = ''",
	},
	"module_user_properties": {
		"CREATE UNIQUE INDEX `module_user_properties_unique_module_user_when_deleted_at_null` ON `module_user_properties` (`module`, `user`) WHERE deleted_at = ''",
	},
	"draft_issue_assignees": {
		"CREATE UNIQUE INDEX `draft_issue_assignee_unique_issue_assignee_when_deleted_at_null` ON `draft_issue_assignees` (`draft_issue`, `assignee`) WHERE deleted_at = ''",
	},
	"draft_issue_modules": {
		"CREATE UNIQUE INDEX `module_draft_issue_unique_issue_module_when_deleted_at_null` ON `draft_issue_modules` (`draft_issue`, `module`) WHERE deleted_at = ''",
	},
	"draft_issue_cycles": {
		"CREATE UNIQUE INDEX `draft_issue_cycle_when_deleted_at_null` ON `draft_issue_cycles` (`draft_issue`, `cycle`) WHERE deleted_at = ''",
	},
	"user_favorites": {
		"CREATE UNIQUE INDEX `user_favorite_unique_entity_type_entity_identifier_user_when_deleted_at_null` ON `user_favorites` (`entity_type`, `entity_identifier`, `user`) WHERE deleted_at = ''",
	},
	"deploy_boards": {
		"CREATE UNIQUE INDEX `deploy_board_unique_entity_name_entity_identifier_when_deleted_at_null` ON `deploy_boards` (`entity_name`, `entity_identifier`) WHERE deleted_at = ''",
	},
	"page_logs": {
		"CREATE UNIQUE INDEX `page_logs_page_transaction` ON `page_logs` (`page`, `transaction`)",
	},
}

func init() {
	m.Register(func(app core.App) error {
		for name, ddls := range deferredRelationIndexes {
			if err := patchCollection(app, name, func(c *core.Collection) {
				c.Indexes = append(c.Indexes, ddls...)
			}); err != nil {
				return err
			}
		}
		return nil
	}, func(app core.App) error {
		for name, ddls := range deferredRelationIndexes {
			names := make([]string, 0, len(ddls))
			for _, ddl := range ddls {
				names = append(names, indexName(ddl))
			}
			if err := patchCollection(app, name, func(c *core.Collection) {
				for _, idxName := range names {
					c.RemoveIndex(idxName)
				}
			}); err != nil {
				return err
			}
		}
		return nil
	}, "1759000011_relation_indexes.go")
}
