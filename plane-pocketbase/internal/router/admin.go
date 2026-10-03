package router

import (
	"github.com/pocketbase/pocketbase/core"

	"github.com/plane-pocketbase/plane-pocketbase/internal/handlers/instance"
	"github.com/plane-pocketbase/plane-pocketbase/internal/handlers/space"
)

// RegisterAdmin wires the instance-admin (`/api/instances/…`) and public
// space (`/api/public/…`) domains. The open API v1 (`/api/v1/…`) group lives
// in publicapi.go.
// Contract: spec/api-spec.yaml, groups `instance` and `space`.
func RegisterAdmin(e *core.ServeEvent, app core.App) error {
	inst := instance.New(app)

	// Seed the singleton instance row (stands in for Django's
	// `configure_instance` boot command) so the admin console has something
	// to bootstrap against.
	if _, err := inst.SeedInstance(); err != nil {
		return err
	}

	// ---- Instance domain ----
	e.Router.GET("/api/instances/{$}", inst.Info)
	e.Router.PATCH("/api/instances/{$}", inst.Update)
	e.Router.POST("/api/instances/admins/sign-up/{$}", inst.SignUp)
	e.Router.POST("/api/instances/admins/sign-in/{$}", inst.SignIn)
	e.Router.POST("/api/instances/admins/sign-out/{$}", inst.SignOut)
	e.Router.POST("/api/instances/admins/sign-up-screen-visited/{$}", inst.SignUpScreenVisited)
	e.Router.GET("/api/instances/admins/session/{$}", inst.Session)
	e.Router.GET("/api/instances/admins/me/{$}", inst.Me)
	e.Router.GET("/api/instances/admins/{$}", inst.AdminList)
	e.Router.POST("/api/instances/admins/{$}", inst.AdminCreate)
	e.Router.DELETE("/api/instances/admins/{pk}/{$}", inst.AdminDelete)
	e.Router.GET("/api/instances/configurations/{$}", inst.Configurations)
	e.Router.PATCH("/api/instances/configurations/{$}", inst.ConfigurationsUpdate)
	e.Router.DELETE("/api/instances/configurations/disable-email-feature/{$}", inst.DisableEmailFeature)
	e.Router.POST("/api/instances/email-credentials-check/{$}", inst.EmailCredentialsCheck)
	e.Router.GET("/api/instances/workspace-slug-check/{$}", inst.WorkspaceSlugCheck)
	e.Router.GET("/api/instances/workspaces/{$}", inst.WorkspaceList)

	// ---- Space (public deploy-board) domain ----
	s := space.New(app)

	// Anchor discovery for a published project.
	e.Router.GET("/api/public/workspaces/{slug}/projects/{project_id}/anchor/{$}", s.AnchorForProject)

	// Project boards listing by workspace (WorkspaceProjectDeployBoardEndpoint
	// in the Django space module; the anchor variant in the spec).
	e.Router.GET("/api/public/workspaces/{slug}/project-boards/{$}", s.AnchorForProject)

	// Everything below is addressed by the board's anchor.
	anchor := e.Router.Group("/api/public/anchor/{anchor}")
	anchor.GET("/meta/{$}", s.Meta)
	anchor.GET("/settings/{$}", s.Settings)
	anchor.GET("/members/{$}", s.Members)
	anchor.GET("/states/{$}", s.States)
	anchor.GET("/labels/{$}", s.Labels)
	anchor.GET("/cycles/{$}", s.Cycles)
	anchor.GET("/modules/{$}", s.Modules)
	anchor.GET("/issues/{$}", s.Issues)
	anchor.GET("/issues/{issue_id}/{$}", s.IssueDetail)
	anchor.GET("/issues/{issue_id}/comments/{$}", s.Comments)
	anchor.POST("/issues/{issue_id}/comments/{$}", s.Comments)
	anchor.GET("/issues/{issue_id}/comments/{pk}/{$}", s.CommentDetail)
	anchor.PATCH("/issues/{issue_id}/comments/{pk}/{$}", s.CommentDetail)
	anchor.DELETE("/issues/{issue_id}/comments/{pk}/{$}", s.CommentDetail)
	anchor.GET("/issues/{issue_id}/reactions/{$}", s.IssueReactions)
	anchor.POST("/issues/{issue_id}/reactions/{$}", s.IssueReactions)
	anchor.DELETE("/issues/{issue_id}/reactions/{reaction_code}/{$}", s.IssueReactionDelete)
	anchor.GET("/comments/{comment_id}/reactions/{$}", s.CommentReactions)
	anchor.POST("/comments/{comment_id}/reactions/{$}", s.CommentReactions)
	anchor.DELETE("/comments/{comment_id}/reactions/{reaction_code}/{$}", s.CommentReactionDelete)
	anchor.GET("/issues/{issue_id}/votes/{$}", s.Votes)
	anchor.POST("/issues/{issue_id}/votes/{$}", s.Votes)
	anchor.DELETE("/issues/{issue_id}/votes/{$}", s.Votes)

	// Public intake (inbox submission).
	anchor.GET("/intakes/{intake_id}/intake-issues/{$}", s.IntakeIssues)
	anchor.POST("/intakes/{intake_id}/intake-issues/{$}", s.IntakeIssues)
	anchor.GET("/intakes/{intake_id}/inbox-issues/{$}", s.IntakeIssues)
	anchor.POST("/intakes/{intake_id}/inbox-issues/{$}", s.IntakeIssues)
	anchor.GET("/intakes/{intake_id}/intake-issues/{pk}/{$}", s.IntakeIssueDetail)
	anchor.PATCH("/intakes/{intake_id}/intake-issues/{pk}/{$}", s.IntakeIssueDetail)
	anchor.DELETE("/intakes/{intake_id}/intake-issues/{pk}/{$}", s.IntakeIssueDetail)

	return nil
}
