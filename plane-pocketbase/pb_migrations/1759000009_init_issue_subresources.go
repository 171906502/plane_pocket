// Wave W10: the issue sub-resources, cycle/module/intake/draft joins, pages,
// descriptions history and views/favourites/recents.
//
// Spec §6.3 W10 — 36 collections. (`issue_assignees` and `issue_labels` already
// exist from wave 1.) Every non-deferred relation target already exists, so all
// of these are created here. The three self-relations
// (`issue_comments.parent` D12, `pages.parent` D13, `user_favorites.parent` D14)
// are deferred to the W11 back-fill file, as is `file_assets.*` (D5–D9).
//
// Partial UNIQUE indexes that reference relation columns are appended in W12
// (§6.5); scalar-only indexes are created inline.
package pb_migrations

import (
	"fmt"

	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

// ---------------------------------------------------------------------------
// Issue sub-resources
// ---------------------------------------------------------------------------

func buildIssueSequences(usersID, projectsID, workspacesID, issuesID string) *core.Collection {
	c := core.NewBaseCollection("issue_sequences")
	addAuditFields(c, usersID, true)

	c.Fields.Add(relation("issue", issuesID, false, false)) // SET_NULL: keep the sequence after issue deletion
	c.Fields.Add(&core.NumberField{Name: "sequence", OnlyInt: true, Min: f64(1)})
	c.Fields.Add(&core.BoolField{Name: "deleted"})

	c.Fields.Add(relation("project", projectsID, true, true))
	c.Fields.Add(relation("workspace", workspacesID, true, true))

	addIdentityIndexes(c, true)
	c.Indexes = append(c.Indexes, "CREATE INDEX `idx_issue_sequences_sequence` ON `issue_sequences` (`sequence`)")

	return c
}

func buildIssueBlockers(usersID, projectsID, workspacesID, issuesID string) *core.Collection {
	c := core.NewBaseCollection("issue_blockers")
	addAuditFields(c, usersID, true)

	c.Fields.Add(relation("block", issuesID, true, true))
	c.Fields.Add(relation("blocked_by", issuesID, true, true))

	c.Fields.Add(relation("project", projectsID, true, true))
	c.Fields.Add(relation("workspace", workspacesID, true, true))

	// No unique constraint (spec §3.6 REVIEW).
	addIdentityIndexes(c, true)

	return c
}

func buildIssueRelations(usersID, projectsID, workspacesID, issuesID string) *core.Collection {
	c := core.NewBaseCollection("issue_relations")
	addAuditFields(c, usersID, true)

	c.Fields.Add(relation("issue", issuesID, true, true))
	c.Fields.Add(relation("related_issue", issuesID, true, true))
	// relation_type stays `text` — the enum is not wired to choices= (spec §1.3(h)).
	c.Fields.Add(&core.TextField{Name: "relation_type", Required: true, Max: 20})

	c.Fields.Add(relation("project", projectsID, true, true))
	c.Fields.Add(relation("workspace", workspacesID, true, true))

	// Partial UNIQUE (issue, related_issue) is a phase-3 index (§6.5).
	addIdentityIndexes(c, true)

	return c
}

func buildIssueMentions(usersID, projectsID, workspacesID, issuesID string) *core.Collection {
	c := core.NewBaseCollection("issue_mentions")
	addAuditFields(c, usersID, true)

	c.Fields.Add(relation("issue", issuesID, true, true))
	c.Fields.Add(relation("mention", usersID, true, true))

	c.Fields.Add(relation("project", projectsID, true, true))
	c.Fields.Add(relation("workspace", workspacesID, true, true))

	// Partial UNIQUE (issue, mention) is a phase-3 index (§6.5).
	addIdentityIndexes(c, true)

	return c
}

func buildIssueLinks(usersID, projectsID, workspacesID, issuesID string) *core.Collection {
	c := core.NewBaseCollection("issue_links")
	addAuditFields(c, usersID, true)

	c.Fields.Add(&core.TextField{Name: "title", Max: 255})
	// Django TextField, NOT URLField — stays `text` (spec §3.6).
	c.Fields.Add(&core.TextField{Name: "url", Max: maxTextBlob})
	c.Fields.Add(relation("issue", issuesID, true, true))
	c.Fields.Add(&core.JSONField{Name: "metadata"})

	c.Fields.Add(relation("project", projectsID, true, true))
	c.Fields.Add(relation("workspace", workspacesID, true, true))

	addIdentityIndexes(c, true)

	return c
}

func buildIssueAttachments(usersID, projectsID, workspacesID, issuesID string) *core.Collection {
	c := core.NewBaseCollection("issue_attachments")
	addAuditFields(c, usersID, true)

	c.Fields.Add(&core.JSONField{Name: "attributes"})
	c.Fields.Add(&core.FileField{Name: "asset", MaxSelect: 1, Required: true, Protected: true, MaxSize: maxEditor})
	c.Fields.Add(relation("issue", issuesID, true, true))
	c.Fields.Add(&core.TextField{Name: "external_source", Max: 255})
	c.Fields.Add(&core.TextField{Name: "external_id", Max: 255})

	c.Fields.Add(relation("project", projectsID, true, true))
	c.Fields.Add(relation("workspace", workspacesID, true, true))

	addIdentityIndexes(c, true)

	return c
}

func buildIssueSubscribers(usersID, projectsID, workspacesID, issuesID string) *core.Collection {
	c := core.NewBaseCollection("issue_subscribers")
	addAuditFields(c, usersID, true)

	c.Fields.Add(relation("issue", issuesID, true, true))
	c.Fields.Add(relation("subscriber", usersID, true, true))

	c.Fields.Add(relation("project", projectsID, true, true))
	c.Fields.Add(relation("workspace", workspacesID, true, true))

	// Partial UNIQUE (issue, subscriber) is a phase-3 index (§6.5).
	addIdentityIndexes(c, true)

	return c
}

func buildIssueReactions(usersID, projectsID, workspacesID, issuesID string) *core.Collection {
	c := core.NewBaseCollection("issue_reactions")
	addAuditFields(c, usersID, true)

	c.Fields.Add(relation("actor", usersID, true, true))
	c.Fields.Add(relation("issue", issuesID, true, true))
	c.Fields.Add(&core.TextField{Name: "reaction", Required: true, Max: maxTextBlob})

	c.Fields.Add(relation("project", projectsID, true, true))
	c.Fields.Add(relation("workspace", workspacesID, true, true))

	// Partial UNIQUE (issue, actor, reaction) is a phase-3 index (§6.5).
	addIdentityIndexes(c, true)

	return c
}

func buildIssueVotes(usersID, projectsID, workspacesID, issuesID string) *core.Collection {
	c := core.NewBaseCollection("issue_votes")
	addAuditFields(c, usersID, true)

	c.Fields.Add(relation("issue", issuesID, true, true))
	c.Fields.Add(relation("actor", usersID, true, true))
	c.Fields.Add(&core.NumberField{
		Name:    "vote",
		OnlyInt: true,
		Min:     f64(-1),
		Max:     f64(1),
		Help:    "-1=Down, 1=Up (spec §1.3(a)).",
	})

	c.Fields.Add(relation("project", projectsID, true, true))
	c.Fields.Add(relation("workspace", workspacesID, true, true))

	// Partial UNIQUE (issue, actor) is a phase-3 index (§6.5).
	addIdentityIndexes(c, true)

	return c
}

func buildIssueComments(usersID, projectsID, workspacesID, issuesID, descriptionsID string) *core.Collection {
	c := core.NewBaseCollection("issue_comments")
	addAuditFields(c, usersID, true)

	c.Fields.Add(&core.TextField{Name: "comment_stripped", Max: maxStripped})
	c.Fields.Add(&core.JSONField{Name: "comment_json"})
	c.Fields.Add(&core.EditorField{Name: "comment_html", MaxSize: maxEditor})
	c.Fields.Add(relation("description", descriptionsID, false, true)) // CASCADE + unique index
	c.Fields.Add(&core.JSONField{Name: "attachments", MaxSize: 1 << 20})
	c.Fields.Add(relation("issue", issuesID, true, true))
	c.Fields.Add(relation("actor", usersID, false, true)) // CASCADE, nullable
	c.Fields.Add(&core.SelectField{Name: "access", Values: []string{"INTERNAL", "EXTERNAL"}, MaxSelect: 1})
	c.Fields.Add(&core.TextField{Name: "external_source", Max: 255})
	c.Fields.Add(&core.TextField{Name: "external_id", Max: 255})
	c.Fields.Add(&core.DateField{Name: "edited_at"})
	// `parent` self-relation deferred (§6.4 D12).

	c.Fields.Add(relation("project", projectsID, true, true))
	c.Fields.Add(relation("workspace", workspacesID, true, true))

	// `idx_issue_comments_description` (unique on `description`) is a phase-3 index (§6.5).
	addIdentityIndexes(c, true)

	return c
}

func buildCommentReactions(usersID, projectsID, workspacesID, commentsID string) *core.Collection {
	c := core.NewBaseCollection("comment_reactions")
	addAuditFields(c, usersID, true)

	c.Fields.Add(relation("actor", usersID, true, true))
	c.Fields.Add(relation("comment", commentsID, true, true))
	c.Fields.Add(&core.TextField{Name: "reaction", Required: true, Max: maxTextBlob})

	c.Fields.Add(relation("project", projectsID, true, true))
	c.Fields.Add(relation("workspace", workspacesID, true, true))

	// Partial UNIQUE (comment, actor, reaction) is a phase-3 index (§6.5).
	addIdentityIndexes(c, true)

	return c
}

func buildIssueActivities(usersID, projectsID, workspacesID, issuesID, commentsID string) *core.Collection {
	c := core.NewBaseCollection("issue_activities")
	addAuditFields(c, usersID, true)

	// DO_NOTHING -> non-cascading nullable relation (spec §1.3(k), §7.5).
	c.Fields.Add(relation("issue", issuesID, false, false))
	c.Fields.Add(&core.TextField{Name: "verb", Required: true, Max: 255})
	c.Fields.Add(&core.TextField{Name: "field", Max: 255})
	c.Fields.Add(&core.TextField{Name: "old_value", Max: maxTextBlob})
	c.Fields.Add(&core.TextField{Name: "new_value", Max: maxTextBlob})
	c.Fields.Add(&core.TextField{Name: "comment", Max: maxTextBlob})
	c.Fields.Add(&core.JSONField{Name: "attachments", MaxSize: 1 << 20})
	c.Fields.Add(relation("issue_comment", commentsID, false, false)) // DO_NOTHING
	c.Fields.Add(relation("actor", usersID, false, false))            // SET_NULL
	c.Fields.Add(uuidRef("old_identifier"))
	c.Fields.Add(uuidRef("new_identifier"))
	// epoch is json: 0.0 is a legal epoch AND the unset marker (spec §1.3(e)).
	c.Fields.Add(&core.JSONField{Name: "epoch", MaxSize: 64})

	c.Fields.Add(relation("project", projectsID, true, true))
	c.Fields.Add(relation("workspace", workspacesID, true, true))

	addIdentityIndexes(c, true)
	c.Indexes = append(c.Indexes,
		"CREATE INDEX `idx_issue_activities_issue` ON `issue_activities` (`issue`)",
		"CREATE INDEX `idx_issue_activities_comment` ON `issue_activities` (`issue_comment`)",
	)

	return c
}

func buildIssueVersions(usersID, projectsID, workspacesID, issuesID, activitiesID string) *core.Collection {
	c := core.NewBaseCollection("issue_versions")
	addAuditFields(c, usersID, true)

	// UUID snapshots, not relations (spec §2.5).
	c.Fields.Add(uuidRef("parent"))
	c.Fields.Add(uuidRef("state"))
	c.Fields.Add(uuidRef("estimate_point"))
	c.Fields.Add(&core.TextField{Name: "name", Required: true, Max: 255})
	c.Fields.Add(&core.SelectField{
		Name:      "priority",
		Values:    []string{"urgent", "high", "medium", "low", "none"},
		MaxSelect: 1,
	})
	c.Fields.Add(&core.DateField{Name: "start_date"})
	c.Fields.Add(&core.DateField{Name: "target_date"})
	c.Fields.Add(&core.JSONField{Name: "assignees", MaxSize: 1 << 20})
	c.Fields.Add(&core.NumberField{Name: "sequence_id", OnlyInt: true})
	c.Fields.Add(&core.JSONField{Name: "labels", MaxSize: 1 << 20})
	c.Fields.Add(&core.NumberField{Name: "sort_order"})
	c.Fields.Add(&core.DateField{Name: "completed_at"})
	c.Fields.Add(&core.DateField{Name: "archived_at"})
	c.Fields.Add(&core.BoolField{Name: "is_draft"})
	c.Fields.Add(&core.TextField{Name: "external_source", Max: 255})
	c.Fields.Add(&core.TextField{Name: "external_id", Max: 255})
	c.Fields.Add(uuidRef("type"))
	c.Fields.Add(uuidRef("cycle"))
	c.Fields.Add(&core.JSONField{Name: "modules", MaxSize: 1 << 20})
	c.Fields.Add(&core.JSONField{Name: "properties"})
	c.Fields.Add(&core.JSONField{Name: "meta"})
	c.Fields.Add(&core.DateField{Name: "last_saved_at", Required: true})
	c.Fields.Add(relation("issue", issuesID, true, true))
	c.Fields.Add(relation("activity", activitiesID, false, false)) // SET_NULL
	c.Fields.Add(relation("owned_by", usersID, true, true))

	c.Fields.Add(relation("project", projectsID, true, true))
	c.Fields.Add(relation("workspace", workspacesID, true, true))

	addIdentityIndexes(c, true)

	return c
}

func buildIssueDescriptionVersions(usersID, projectsID, workspacesID, issuesID string) *core.Collection {
	c := core.NewBaseCollection("issue_description_versions")
	addAuditFields(c, usersID, true)

	c.Fields.Add(relation("issue", issuesID, true, true))
	c.Fields.Add(&core.FileField{Name: "description_binary", MaxSelect: 1, Protected: true})
	c.Fields.Add(&core.EditorField{Name: "description_html", MaxSize: maxEditor})
	c.Fields.Add(&core.TextField{Name: "description_stripped", Max: maxStripped})
	c.Fields.Add(&core.JSONField{Name: "description_json"})
	c.Fields.Add(&core.DateField{Name: "last_saved_at", Required: true})
	c.Fields.Add(relation("owned_by", usersID, true, true))

	c.Fields.Add(relation("project", projectsID, true, true))
	c.Fields.Add(relation("workspace", workspacesID, true, true))

	addIdentityIndexes(c, true)

	return c
}

// ---------------------------------------------------------------------------
// Cycle / module / intake joins
// ---------------------------------------------------------------------------

func buildCycleIssues(usersID, projectsID, workspacesID, issuesID, cyclesID string) *core.Collection {
	c := core.NewBaseCollection("cycle_issues")
	addAuditFields(c, usersID, true)

	c.Fields.Add(relation("issue", issuesID, true, true))
	c.Fields.Add(relation("cycle", cyclesID, true, true))

	c.Fields.Add(relation("project", projectsID, true, true))
	c.Fields.Add(relation("workspace", workspacesID, true, true))

	// Partial UNIQUE (cycle, issue) is a phase-3 index (§6.5).
	addIdentityIndexes(c, true)

	return c
}

func buildCycleUserProperties(usersID, projectsID, workspacesID, cyclesID string) *core.Collection {
	c := core.NewBaseCollection("cycle_user_properties")
	addAuditFields(c, usersID, true)

	c.Fields.Add(relation("cycle", cyclesID, true, true))
	c.Fields.Add(relation("user", usersID, true, true))
	c.Fields.Add(&core.JSONField{Name: "filters"})
	c.Fields.Add(&core.JSONField{Name: "display_filters"})
	c.Fields.Add(&core.JSONField{Name: "display_properties"})
	c.Fields.Add(&core.JSONField{Name: "rich_filters"})

	c.Fields.Add(relation("project", projectsID, true, true))
	c.Fields.Add(relation("workspace", workspacesID, true, true))

	// Partial UNIQUE (cycle, user) is a phase-3 index (§6.5).
	addIdentityIndexes(c, true)

	return c
}

func buildModuleMembers(usersID, projectsID, workspacesID, modulesID string) *core.Collection {
	c := core.NewBaseCollection("module_members")
	addAuditFields(c, usersID, true)

	c.Fields.Add(relation("module", modulesID, true, true))
	c.Fields.Add(relation("member", usersID, true, true))

	c.Fields.Add(relation("project", projectsID, true, true))
	c.Fields.Add(relation("workspace", workspacesID, true, true))

	// Partial UNIQUE (module, member) is a phase-3 index (§6.5).
	addIdentityIndexes(c, true)

	return c
}

func buildModuleIssues(usersID, projectsID, workspacesID, modulesID, issuesID string) *core.Collection {
	c := core.NewBaseCollection("module_issues")
	addAuditFields(c, usersID, true)

	c.Fields.Add(relation("module", modulesID, true, true))
	c.Fields.Add(relation("issue", issuesID, true, true))

	c.Fields.Add(relation("project", projectsID, true, true))
	c.Fields.Add(relation("workspace", workspacesID, true, true))

	// Partial UNIQUE (issue, module) is a phase-3 index (§6.5).
	addIdentityIndexes(c, true)

	return c
}

func buildModuleLinks(usersID, projectsID, workspacesID, modulesID string) *core.Collection {
	c := core.NewBaseCollection("module_links")
	addAuditFields(c, usersID, true)

	c.Fields.Add(&core.TextField{Name: "title", Max: 255})
	// Django URLField here (contrast IssueLink.url) — PB `url` validates the format.
	c.Fields.Add(&core.URLField{Name: "url"})
	c.Fields.Add(relation("module", modulesID, true, true))
	c.Fields.Add(&core.JSONField{Name: "metadata"})

	c.Fields.Add(relation("project", projectsID, true, true))
	c.Fields.Add(relation("workspace", workspacesID, true, true))

	addIdentityIndexes(c, true)

	return c
}

func buildModuleUserProperties(usersID, projectsID, workspacesID, modulesID string) *core.Collection {
	c := core.NewBaseCollection("module_user_properties")
	addAuditFields(c, usersID, true)

	c.Fields.Add(relation("module", modulesID, true, true))
	c.Fields.Add(relation("user", usersID, true, true))
	c.Fields.Add(&core.JSONField{Name: "filters"})
	c.Fields.Add(&core.JSONField{Name: "display_filters"})
	c.Fields.Add(&core.JSONField{Name: "display_properties"})
	c.Fields.Add(&core.JSONField{Name: "rich_filters"})

	c.Fields.Add(relation("project", projectsID, true, true))
	c.Fields.Add(relation("workspace", workspacesID, true, true))

	// Partial UNIQUE (module, user) is a phase-3 index (§6.5).
	addIdentityIndexes(c, true)

	return c
}

func buildIntakeIssues(usersID, projectsID, workspacesID, intakesID, issuesID string) *core.Collection {
	c := core.NewBaseCollection("intake_issues")
	addAuditFields(c, usersID, true)

	c.Fields.Add(relation("intake", intakesID, true, true))
	c.Fields.Add(relation("issue", issuesID, true, true))
	c.Fields.Add(&core.NumberField{
		Name:    "status",
		OnlyInt: true,
		Min:     f64(-2),
		Max:     f64(2),
		Help:    "-2=Pending, -1=Rejected, 0=Snoozed, 1=Accepted, 2=Duplicate (spec §1.3(a)).",
	})
	c.Fields.Add(&core.DateField{Name: "snoozed_till"})
	c.Fields.Add(relation("duplicate_to", issuesID, false, false)) // SET_NULL, second relation to issues
	c.Fields.Add(&core.TextField{Name: "source", Max: 255})
	c.Fields.Add(&core.TextField{Name: "source_email", Max: maxTextBlob})
	c.Fields.Add(&core.TextField{Name: "external_source", Max: 255})
	c.Fields.Add(&core.TextField{Name: "external_id", Max: 255})
	c.Fields.Add(&core.JSONField{Name: "extra"})

	c.Fields.Add(relation("project", projectsID, true, true))
	c.Fields.Add(relation("workspace", workspacesID, true, true))

	// No unique constraint (spec §3.9 REVIEW).
	addIdentityIndexes(c, true)

	return c
}

// ---------------------------------------------------------------------------
// Draft issues
// ---------------------------------------------------------------------------

func buildDraftIssues(usersID, projectsID, workspacesID, issuesID, statesID, estimatePointsID, issueTypesID string) *core.Collection {
	c := core.NewBaseCollection("draft_issues")
	addAuditFields(c, usersID, true)

	c.Fields.Add(relation("parent", issuesID, false, true)) // CASCADE
	c.Fields.Add(relation("state", statesID, false, true))  // CASCADE
	c.Fields.Add(relation("estimate_point", estimatePointsID, false, false))
	c.Fields.Add(&core.TextField{Name: "name", Max: 255})
	c.Fields.Add(&core.JSONField{Name: "description_json"})
	c.Fields.Add(&core.EditorField{Name: "description_html", MaxSize: maxEditor})
	c.Fields.Add(&core.TextField{Name: "description_stripped", Max: maxStripped})
	c.Fields.Add(&core.FileField{Name: "description_binary", MaxSelect: 1, Protected: true})
	c.Fields.Add(&core.SelectField{
		Name:      "priority",
		Values:    []string{"urgent", "high", "medium", "low", "none"},
		MaxSelect: 1,
	})
	c.Fields.Add(&core.DateField{Name: "start_date"})
	c.Fields.Add(&core.DateField{Name: "target_date"})
	c.Fields.Add(&core.NumberField{Name: "sort_order"})
	c.Fields.Add(&core.DateField{Name: "completed_at"})
	c.Fields.Add(&core.TextField{Name: "external_source", Max: 255})
	c.Fields.Add(&core.TextField{Name: "external_id", Max: 255})
	c.Fields.Add(relation("type", issueTypesID, false, false)) // SET_NULL

	c.Fields.Add(relation("workspace", workspacesID, true, true))
	c.Fields.Add(relation("project", projectsID, false, true))

	// No unique constraint (spec §3.10).
	addIdentityIndexes(c, true)

	return c
}

func buildDraftIssueAssignees(usersID, projectsID, workspacesID, draftIssuesID string) *core.Collection {
	c := core.NewBaseCollection("draft_issue_assignees")
	addAuditFields(c, usersID, true)

	c.Fields.Add(relation("draft_issue", draftIssuesID, true, true))
	c.Fields.Add(relation("assignee", usersID, true, true))

	c.Fields.Add(relation("workspace", workspacesID, true, true))
	c.Fields.Add(relation("project", projectsID, false, true))

	// Partial UNIQUE (draft_issue, assignee) is a phase-3 index (§6.5).
	addIdentityIndexes(c, true)

	return c
}

func buildDraftIssueLabels(usersID, projectsID, workspacesID, draftIssuesID, labelsID string) *core.Collection {
	c := core.NewBaseCollection("draft_issue_labels")
	addAuditFields(c, usersID, true)

	c.Fields.Add(relation("draft_issue", draftIssuesID, true, true))
	c.Fields.Add(relation("label", labelsID, true, true))

	c.Fields.Add(relation("workspace", workspacesID, true, true))
	c.Fields.Add(relation("project", projectsID, false, true))

	// No unique constraint (spec §5.1 J5).
	addIdentityIndexes(c, true)

	return c
}

func buildDraftIssueModules(usersID, projectsID, workspacesID, draftIssuesID, modulesID string) *core.Collection {
	c := core.NewBaseCollection("draft_issue_modules")
	addAuditFields(c, usersID, true)

	c.Fields.Add(relation("module", modulesID, true, true))
	c.Fields.Add(relation("draft_issue", draftIssuesID, true, true))

	c.Fields.Add(relation("workspace", workspacesID, true, true))
	c.Fields.Add(relation("project", projectsID, false, true))

	// Partial UNIQUE (draft_issue, module) is a phase-3 index (§6.5).
	addIdentityIndexes(c, true)

	return c
}

func buildDraftIssueCycles(usersID, projectsID, workspacesID, draftIssuesID, cyclesID string) *core.Collection {
	c := core.NewBaseCollection("draft_issue_cycles")
	addAuditFields(c, usersID, true)

	c.Fields.Add(relation("draft_issue", draftIssuesID, true, true))
	c.Fields.Add(relation("cycle", cyclesID, true, true))

	c.Fields.Add(relation("workspace", workspacesID, true, true))
	c.Fields.Add(relation("project", projectsID, false, true))

	// Partial UNIQUE (draft_issue, cycle) is a phase-3 index (§6.5).
	addIdentityIndexes(c, true)

	return c
}

// ---------------------------------------------------------------------------
// Views, favourites, recents, stickies
// ---------------------------------------------------------------------------

func buildIssueViews(usersID, projectsID, workspacesID string) *core.Collection {
	c := core.NewBaseCollection("issue_views")
	addAuditFields(c, usersID, true)

	c.Fields.Add(&core.TextField{Name: "name", Required: true, Max: 255})
	c.Fields.Add(&core.TextField{Name: "description", Max: maxTextBlob})
	c.Fields.Add(&core.JSONField{Name: "query", MaxSize: maxJSONRich})
	c.Fields.Add(&core.JSONField{Name: "filters"})
	c.Fields.Add(&core.JSONField{Name: "display_filters"})
	c.Fields.Add(&core.JSONField{Name: "display_properties"})
	c.Fields.Add(&core.JSONField{Name: "rich_filters"})
	c.Fields.Add(&core.NumberField{
		Name:    "access",
		OnlyInt: true,
		Min:     f64(0),
		Max:     f64(1),
		Help:    "0=Public, 1=Private (spec §1.3(a); note this is inverted vs Page.access).",
	})
	c.Fields.Add(&core.NumberField{Name: "sort_order"})
	c.Fields.Add(&core.JSONField{Name: "logo_props"})
	c.Fields.Add(relation("owned_by", usersID, true, true))
	c.Fields.Add(&core.BoolField{Name: "is_locked"})
	c.Fields.Add(&core.DateField{Name: "archived_at"})

	c.Fields.Add(relation("workspace", workspacesID, true, true))
	c.Fields.Add(relation("project", projectsID, false, true))

	// No unique constraint (spec §3.13).
	addIdentityIndexes(c, true)

	return c
}

func buildUserFavorites(usersID, projectsID, workspacesID string) *core.Collection {
	c := core.NewBaseCollection("user_favorites")
	addAuditFields(c, usersID, true)

	c.Fields.Add(relation("user", usersID, true, true))
	c.Fields.Add(&core.TextField{Name: "entity_type", Required: true, Max: 100})
	c.Fields.Add(uuidRef("entity_identifier"))
	c.Fields.Add(&core.TextField{Name: "name", Max: 255})
	c.Fields.Add(&core.BoolField{Name: "is_folder"})
	c.Fields.Add(&core.NumberField{Name: "sequence"})
	// `parent` self-relation deferred (§6.4 D14).

	c.Fields.Add(relation("workspace", workspacesID, true, true))
	c.Fields.Add(relation("project", projectsID, false, true))

	addIdentityIndexes(c, true)
	c.Indexes = append(c.Indexes,
		"CREATE INDEX `fav_entity_type_idx` ON `user_favorites` (`entity_type`)",
		"CREATE INDEX `fav_entity_identifier_idx` ON `user_favorites` (`entity_identifier`)",
		"CREATE INDEX `fav_entity_idx` ON `user_favorites` (`entity_type`, `entity_identifier`)",
	)
	// Partial UNIQUE (entity_type, entity_identifier, user) is a phase-3 index (§6.5).

	return c
}

func buildUserRecentVisits(usersID, projectsID, workspacesID string) *core.Collection {
	c := core.NewBaseCollection("user_recent_visits")
	addAuditFields(c, usersID, true)

	c.Fields.Add(uuidRef("entity_identifier"))
	c.Fields.Add(&core.TextField{Name: "entity_name", Required: true, Max: 30})
	c.Fields.Add(relation("user", usersID, true, true))
	c.Fields.Add(&core.AutodateField{Name: "visited_at", OnUpdate: true})

	c.Fields.Add(relation("workspace", workspacesID, true, true))
	c.Fields.Add(relation("project", projectsID, false, true))

	// No unique constraint (spec §3.13 REVIEW).
	addIdentityIndexes(c, true)

	return c
}

// ---------------------------------------------------------------------------
// Pages
// ---------------------------------------------------------------------------

func buildPages(usersID, workspacesID string) *core.Collection {
	c := core.NewBaseCollection("pages")
	addAuditFields(c, usersID, true)

	c.Fields.Add(relation("workspace", workspacesID, true, true))
	c.Fields.Add(&core.TextField{Name: "name", Max: maxTextBlob})
	c.Fields.Add(&core.JSONField{Name: "description_json"})
	c.Fields.Add(&core.FileField{Name: "description_binary", MaxSelect: 1, Protected: true})
	c.Fields.Add(&core.EditorField{Name: "description_html", MaxSize: maxEditor})
	c.Fields.Add(&core.TextField{Name: "description_stripped", Max: maxStripped})
	c.Fields.Add(relation("owned_by", usersID, true, true))
	c.Fields.Add(&core.NumberField{
		Name:    "access",
		OnlyInt: true,
		Min:     f64(0),
		Max:     f64(1),
		Help:    "0=Public, 1=Private (spec §1.3(a)).",
	})
	c.Fields.Add(&core.TextField{Name: "color", Max: 255})
	// `parent` self-relation deferred (§6.4 D13).
	c.Fields.Add(&core.DateField{Name: "archived_at"})
	c.Fields.Add(&core.BoolField{Name: "is_locked"})
	c.Fields.Add(&core.JSONField{Name: "view_props"})
	c.Fields.Add(&core.JSONField{Name: "logo_props"})
	c.Fields.Add(&core.BoolField{Name: "is_global"})
	c.Fields.Add(uuidRef("moved_to_page"))
	c.Fields.Add(uuidRef("moved_to_project"))
	c.Fields.Add(&core.NumberField{Name: "sort_order"})
	c.Fields.Add(&core.TextField{Name: "external_id", Max: 255})
	c.Fields.Add(&core.TextField{Name: "external_source", Max: 255})

	// No unique constraint (spec §3.11).
	addIdentityIndexes(c, true)

	return c
}

func buildPageLogs(usersID, workspacesID, pagesID string) *core.Collection {
	c := core.NewBaseCollection("page_logs")
	addAuditFields(c, usersID, true)

	c.Fields.Add(uuidRef("transaction"))
	c.Fields.Add(relation("page", pagesID, true, true))
	c.Fields.Add(uuidRef("entity_identifier"))
	c.Fields.Add(&core.TextField{Name: "entity_name", Required: true, Max: 30})
	c.Fields.Add(&core.TextField{Name: "entity_type", Max: 30})
	c.Fields.Add(relation("workspace", workspacesID, true, true))

	addIdentityIndexes(c, true)
	c.Indexes = append(c.Indexes,
		"CREATE INDEX `pagelog_entity_type_idx` ON `page_logs` (`entity_type`)",
		"CREATE INDEX `pagelog_entity_id_idx` ON `page_logs` (`entity_identifier`)",
		"CREATE INDEX `pagelog_entity_name_idx` ON `page_logs` (`entity_name`)",
		"CREATE INDEX `pagelog_type_id_idx` ON `page_logs` (`entity_type`, `entity_identifier`)",
		"CREATE INDEX `pagelog_name_id_idx` ON `page_logs` (`entity_name`, `entity_identifier`)",
	)
	// `page_logs_page_transaction` (hard unique) is a phase-3 index (§6.5).

	return c
}

func buildPageLabels(usersID, workspacesID, pagesID, labelsID string) *core.Collection {
	c := core.NewBaseCollection("page_labels")
	addAuditFields(c, usersID, true)

	c.Fields.Add(relation("label", labelsID, true, true))
	c.Fields.Add(relation("page", pagesID, true, true))
	c.Fields.Add(relation("workspace", workspacesID, true, true))

	// No unique constraint (spec §5.1 J6).
	addIdentityIndexes(c, true)

	return c
}

func buildProjectPages(usersID, projectsID, workspacesID, pagesID string) *core.Collection {
	c := core.NewBaseCollection("project_pages")
	addAuditFields(c, usersID, true)

	c.Fields.Add(relation("project", projectsID, true, true))
	c.Fields.Add(relation("page", pagesID, true, true))
	c.Fields.Add(relation("workspace", workspacesID, true, true))

	// Partial UNIQUE (project, page) is a phase-3 index (§6.5).
	addIdentityIndexes(c, true)

	return c
}

func buildPageVersions(usersID, workspacesID, pagesID string) *core.Collection {
	c := core.NewBaseCollection("page_versions")
	addAuditFields(c, usersID, true)

	c.Fields.Add(relation("workspace", workspacesID, true, true))
	c.Fields.Add(relation("page", pagesID, true, true))
	c.Fields.Add(&core.DateField{Name: "last_saved_at", Required: true})
	c.Fields.Add(relation("owned_by", usersID, true, true))
	c.Fields.Add(&core.FileField{Name: "description_binary", MaxSelect: 1, Protected: true})
	c.Fields.Add(&core.EditorField{Name: "description_html", MaxSize: maxEditor})
	c.Fields.Add(&core.TextField{Name: "description_stripped", Max: maxStripped})
	c.Fields.Add(&core.JSONField{Name: "description_json"})
	c.Fields.Add(&core.JSONField{Name: "sub_pages_data"})

	addIdentityIndexes(c, true)

	return c
}

// ---------------------------------------------------------------------------
// Description versions + deprecated project deploy boards
// ---------------------------------------------------------------------------

func buildDescriptionVersions(usersID, projectsID, workspacesID, descriptionsID string) *core.Collection {
	c := core.NewBaseCollection("description_versions")
	addAuditFields(c, usersID, true)

	c.Fields.Add(relation("description", descriptionsID, true, true))
	c.Fields.Add(&core.JSONField{Name: "description_json"})
	c.Fields.Add(&core.EditorField{Name: "description_html", MaxSize: maxEditor})
	c.Fields.Add(&core.FileField{Name: "description_binary", MaxSelect: 1, Protected: true})
	c.Fields.Add(&core.TextField{Name: "description_stripped", Max: maxStripped})

	c.Fields.Add(relation("workspace", workspacesID, true, true))
	c.Fields.Add(relation("project", projectsID, false, true))

	addIdentityIndexes(c, true)

	return c
}

func buildProjectDeployBoards(usersID, projectsID, workspacesID, intakesID string) *core.Collection {
	c := core.NewBaseCollection("project_deploy_boards")
	addAuditFields(c, usersID, true)

	c.Fields.Add(&core.TextField{Name: "anchor", Required: true, Max: 255})
	c.Fields.Add(&core.BoolField{Name: "comments"})
	c.Fields.Add(&core.BoolField{Name: "reactions"})
	c.Fields.Add(relation("intake", intakesID, false, false)) // SET_NULL
	c.Fields.Add(&core.BoolField{Name: "votes"})
	c.Fields.Add(&core.JSONField{Name: "views"})

	c.Fields.Add(relation("project", projectsID, true, true))
	c.Fields.Add(relation("workspace", workspacesID, true, true))

	addIdentityIndexes(c, true)
	c.Indexes = append(c.Indexes,
		"CREATE UNIQUE INDEX `idx_project_deploy_boards_anchor` ON `project_deploy_boards` (`anchor`)",
	)
	// `project_deploy_boards_project_anchor` (hard unique) is a phase-3 index (§6.5).

	return c
}

func init() {
	m.Register(func(app core.App) error {
		usersID, err := collectionID(app, "users")
		if err != nil {
			return err
		}
		projectsID, err := collectionID(app, "projects")
		if err != nil {
			return err
		}
		workspacesID, err := collectionID(app, "workspaces")
		if err != nil {
			return err
		}
		issuesID, err := collectionID(app, "issues")
		if err != nil {
			return err
		}
		labelsID, err := collectionID(app, "labels")
		if err != nil {
			return err
		}
		statesID, err := collectionID(app, "states")
		if err != nil {
			return err
		}
		cyclesID, err := collectionID(app, "cycles")
		if err != nil {
			return err
		}
		modulesID, err := collectionID(app, "modules")
		if err != nil {
			return err
		}
		intakesID, err := collectionID(app, "intakes")
		if err != nil {
			return err
		}
		descriptionsID, err := collectionID(app, "descriptions")
		if err != nil {
			return err
		}
		estimatePointsID, err := collectionID(app, "estimate_points")
		if err != nil {
			return err
		}
		issueTypesID, err := collectionID(app, "issue_types")
		if err != nil {
			return err
		}

		// --- issue sub-resources (order: comments before their dependants) ---
		if err := app.Save(buildIssueSequences(usersID, projectsID, workspacesID, issuesID)); err != nil {
			return fmt.Errorf("create issue_sequences: %w", err)
		}
		if err := app.Save(buildIssueBlockers(usersID, projectsID, workspacesID, issuesID)); err != nil {
			return fmt.Errorf("create issue_blockers: %w", err)
		}
		if err := app.Save(buildIssueRelations(usersID, projectsID, workspacesID, issuesID)); err != nil {
			return fmt.Errorf("create issue_relations: %w", err)
		}
		if err := app.Save(buildIssueMentions(usersID, projectsID, workspacesID, issuesID)); err != nil {
			return fmt.Errorf("create issue_mentions: %w", err)
		}
		if err := app.Save(buildIssueLinks(usersID, projectsID, workspacesID, issuesID)); err != nil {
			return fmt.Errorf("create issue_links: %w", err)
		}
		if err := app.Save(buildIssueAttachments(usersID, projectsID, workspacesID, issuesID)); err != nil {
			return fmt.Errorf("create issue_attachments: %w", err)
		}
		if err := app.Save(buildIssueSubscribers(usersID, projectsID, workspacesID, issuesID)); err != nil {
			return fmt.Errorf("create issue_subscribers: %w", err)
		}
		if err := app.Save(buildIssueReactions(usersID, projectsID, workspacesID, issuesID)); err != nil {
			return fmt.Errorf("create issue_reactions: %w", err)
		}
		if err := app.Save(buildIssueVotes(usersID, projectsID, workspacesID, issuesID)); err != nil {
			return fmt.Errorf("create issue_votes: %w", err)
		}
		if err := app.Save(buildIssueComments(usersID, projectsID, workspacesID, issuesID, descriptionsID)); err != nil {
			return fmt.Errorf("create issue_comments: %w", err)
		}
		commentsID, err := collectionID(app, "issue_comments")
		if err != nil {
			return err
		}
		if err := app.Save(buildCommentReactions(usersID, projectsID, workspacesID, commentsID)); err != nil {
			return fmt.Errorf("create comment_reactions: %w", err)
		}
		if err := app.Save(buildIssueActivities(usersID, projectsID, workspacesID, issuesID, commentsID)); err != nil {
			return fmt.Errorf("create issue_activities: %w", err)
		}
		activitiesID, err := collectionID(app, "issue_activities")
		if err != nil {
			return err
		}
		if err := app.Save(buildIssueVersions(usersID, projectsID, workspacesID, issuesID, activitiesID)); err != nil {
			return fmt.Errorf("create issue_versions: %w", err)
		}
		if err := app.Save(buildIssueDescriptionVersions(usersID, projectsID, workspacesID, issuesID)); err != nil {
			return fmt.Errorf("create issue_description_versions: %w", err)
		}

		// --- cycle / module / intake joins ---
		if err := app.Save(buildCycleIssues(usersID, projectsID, workspacesID, issuesID, cyclesID)); err != nil {
			return fmt.Errorf("create cycle_issues: %w", err)
		}
		if err := app.Save(buildCycleUserProperties(usersID, projectsID, workspacesID, cyclesID)); err != nil {
			return fmt.Errorf("create cycle_user_properties: %w", err)
		}
		if err := app.Save(buildModuleMembers(usersID, projectsID, workspacesID, modulesID)); err != nil {
			return fmt.Errorf("create module_members: %w", err)
		}
		if err := app.Save(buildModuleIssues(usersID, projectsID, workspacesID, modulesID, issuesID)); err != nil {
			return fmt.Errorf("create module_issues: %w", err)
		}
		if err := app.Save(buildModuleLinks(usersID, projectsID, workspacesID, modulesID)); err != nil {
			return fmt.Errorf("create module_links: %w", err)
		}
		if err := app.Save(buildModuleUserProperties(usersID, projectsID, workspacesID, modulesID)); err != nil {
			return fmt.Errorf("create module_user_properties: %w", err)
		}
		if err := app.Save(buildIntakeIssues(usersID, projectsID, workspacesID, intakesID, issuesID)); err != nil {
			return fmt.Errorf("create intake_issues: %w", err)
		}

		// --- draft issues ---
		if err := app.Save(buildDraftIssues(usersID, projectsID, workspacesID, issuesID, statesID, estimatePointsID, issueTypesID)); err != nil {
			return fmt.Errorf("create draft_issues: %w", err)
		}
		draftIssuesID, err := collectionID(app, "draft_issues")
		if err != nil {
			return err
		}
		if err := app.Save(buildDraftIssueAssignees(usersID, projectsID, workspacesID, draftIssuesID)); err != nil {
			return fmt.Errorf("create draft_issue_assignees: %w", err)
		}
		if err := app.Save(buildDraftIssueLabels(usersID, projectsID, workspacesID, draftIssuesID, labelsID)); err != nil {
			return fmt.Errorf("create draft_issue_labels: %w", err)
		}
		if err := app.Save(buildDraftIssueModules(usersID, projectsID, workspacesID, draftIssuesID, modulesID)); err != nil {
			return fmt.Errorf("create draft_issue_modules: %w", err)
		}
		if err := app.Save(buildDraftIssueCycles(usersID, projectsID, workspacesID, draftIssuesID, cyclesID)); err != nil {
			return fmt.Errorf("create draft_issue_cycles: %w", err)
		}

		// --- views / favourites / recents ---
		if err := app.Save(buildIssueViews(usersID, projectsID, workspacesID)); err != nil {
			return fmt.Errorf("create issue_views: %w", err)
		}
		if err := app.Save(buildUserFavorites(usersID, projectsID, workspacesID)); err != nil {
			return fmt.Errorf("create user_favorites: %w", err)
		}
		if err := app.Save(buildUserRecentVisits(usersID, projectsID, workspacesID)); err != nil {
			return fmt.Errorf("create user_recent_visits: %w", err)
		}

		// --- pages ---
		if err := app.Save(buildPages(usersID, workspacesID)); err != nil {
			return fmt.Errorf("create pages: %w", err)
		}
		pagesID, err := collectionID(app, "pages")
		if err != nil {
			return err
		}
		if err := app.Save(buildPageLogs(usersID, workspacesID, pagesID)); err != nil {
			return fmt.Errorf("create page_logs: %w", err)
		}
		if err := app.Save(buildPageLabels(usersID, workspacesID, pagesID, labelsID)); err != nil {
			return fmt.Errorf("create page_labels: %w", err)
		}
		if err := app.Save(buildProjectPages(usersID, projectsID, workspacesID, pagesID)); err != nil {
			return fmt.Errorf("create project_pages: %w", err)
		}
		if err := app.Save(buildPageVersions(usersID, workspacesID, pagesID)); err != nil {
			return fmt.Errorf("create page_versions: %w", err)
		}

		// --- description history + deprecated project deploy boards ---
		if err := app.Save(buildDescriptionVersions(usersID, projectsID, workspacesID, descriptionsID)); err != nil {
			return fmt.Errorf("create description_versions: %w", err)
		}
		if err := app.Save(buildProjectDeployBoards(usersID, projectsID, workspacesID, intakesID)); err != nil {
			return fmt.Errorf("create project_deploy_boards: %w", err)
		}

		return nil
	}, func(app core.App) error {
		for _, name := range []string{
			"project_deploy_boards", "description_versions", "page_versions",
			"project_pages", "page_labels", "page_logs", "pages",
			"user_recent_visits", "user_favorites", "issue_views",
			"draft_issue_cycles", "draft_issue_modules", "draft_issue_labels",
			"draft_issue_assignees", "draft_issues",
			"intake_issues", "module_user_properties", "module_links",
			"module_issues", "module_members", "cycle_user_properties", "cycle_issues",
			"issue_description_versions", "issue_versions", "issue_activities",
			"comment_reactions", "issue_comments", "issue_votes", "issue_reactions",
			"issue_subscribers", "issue_attachments", "issue_links", "issue_mentions",
			"issue_relations", "issue_blockers", "issue_sequences",
		} {
			c, err := app.FindCollectionByNameOrId(name)
			if err != nil {
				return fmt.Errorf("down: find %q: %w", name, err)
			}
			if err := app.Delete(c); err != nil {
				return fmt.Errorf("down: delete %q: %w", name, err)
			}
		}
		return nil
	}, "1759000009_init_issue_subresources.go")
}
