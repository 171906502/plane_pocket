# Plane Authorization Model — Spec for the PocketBase/Go Backend

Source of truth: Plane **v1.4.2** at `apps/api/plane/`. Every path below was verified to
exist in the tree. Line numbers are from that revision and are provided so implementers can
re-read the exact guard being replicated.

This document describes **authorization only** (who may do what). Authentication
(session cookie, `X-Api-Key`) is covered where it intersects with authorization.

---

## 1. Role model

Plane has **two independent role axes** and one **feature flag** that changes guest
behaviour. Roles are integer-coded and stored on membership rows.

### 1.1 Workspace roles

Defined at `apps/api/plane/db/models/workspace.py:19`:

```python
ROLE_CHOICES = ((20, "Admin"), (15, "Member"), (5, "Guest"))
```

Stored on `WorkspaceMember.role` (`workspace.py:205`, `default=5`) together with
`WorkspaceMember.is_active` (`workspace.py:210`, `default=True`).

- **Workspace Admin (20)** — owns the workspace. Only role that can update workspace
  settings partially (`PUT/PATCH` allow 20 *and* 15), delete the workspace, change member
  roles, remove members. Has a **cross-project bypass**: a workspace admin who is *also an
  active member of a project* passes project-role checks regardless of their project role.
- **Workspace Member (15)** — a normal workspace user. Can create projects, invite members,
  read all workspace-level metadata. Must be added to a project to act on its contents.
- **Workspace Guest (5)** — the workspace-level default. Read-mostly; see §5.1.

### 1.2 Project roles

Defined at `apps/api/plane/db/models/project.py:21` and `:24`:

```python
ROLE_CHOICES = ((20, "Admin"), (15, "Member"), (5, "Guest"))

class ROLE(Enum):
    ADMIN = 20
    MEMBER = 15
    GUEST = 5
```

Stored on `ProjectMember.role` (`project.py:219`, `default=5`) with
`ProjectMember.is_active` (inherited from `ProjectBaseModel`).

- **Project Admin (20)** — manage project settings, members, and destructive operations.
- **Project Member (15)** — day-to-day contributor: create/update work items, cycles,
  modules, states, intake triage, pages.
- **Project Guest (5)** — restricted, often to "things I created". See §5.1.

### 1.3 How the axes compose

There is **no role inheritance**. A project role is not derived from the workspace role.
Effective authority on a project-scoped action is:

```
allowed(action) =
      (is_active WorkspaceMember(workspace, role=20))            # ws-admin bypass
   OR (project-role-check passes for the action)                 # normal path
```

The ws-admin bypass is implemented in **two** places and they differ slightly:

1. `apps/api/plane/app/permissions/base.py:64-78` (`allow_permission`, `level="PROJECT"`):
   a workspace admin passes **only if they are also an active `ProjectMember`** of the
   target project.
2. `apps/api/plane/utils/permissions/base.py:53-67`: same structure, **but the creator
   short-circuit at the top has no workspace-membership check** (see §7).

The bypass is pinned by contract test
`tests/contract/app/test_project_member_is_active_authz.py:147`
(`test_workspace_admin_with_low_project_role_can_deactivate`): a workspace admin holding
only project role 5 may still deactivate a project admin.

### 1.4 `is_active` is load-bearing

Every authorization query filters `is_active=True`. Setting `ProjectMember.is_active=False`
(or `WorkspaceMember.is_active=False`) immediately revokes all of that role's authority.
Contract test `test_project_member_is_active_authz.py` exists because `is_active` was once
writable through a member `PATCH` without a role check (GHSA-hpgm-9r34-c4x5).

**Rule for the Go layer:** the membership lookup must be `WHERE is_active = true` in every
code path, including the ws-admin bypass. A soft-deleted / inactive membership row must
never satisfy a role check.

---

## 2. Workspace / project resolution flow

### 2.1 URL shapes

Plane's app API nests everything under a workspace slug and (usually) a project id:

```
/api/workspaces/<slug>/...                                    # workspace-scoped
/api/workspaces/<slug>/projects/<project_id>/...              # project-scoped
/api/workspaces/<slug>/projects/<project_id>/issues/<pk>/...  # project + object
```

The v1 API (`/api/v1/`) uses the same nesting. The public Spaces API is separate:
`/api/public/anchor/<anchor>/...` (see §5.3).

### 2.2 How `slug` + `project_id` become an authorized context

There is **no dedicated resolution middleware** for workspaces/projects in Plane. Resolution
is done lazily by the permission classes and the views, off the URL kwargs:

- `apps/api/plane/app/views/base.py:125-136` — `BaseViewSet.workspace_slug` returns
  `self.kwargs["slug"]`; `project_id` returns `self.kwargs["project_id"]`, **except** when
  the resolved URL name is `"project"`, in which case it falls back to `self.kwargs["pk"]`.
  (`apps/api/plane/api/views/base.py:258-269` has the identical logic for the v1 API.)
- The permission classes (`app/permissions/workspace.py`, `project.py`) then run a single
  `WorkspaceMember.objects.filter(workspace__slug=slug, member=request.user, is_active=True)`
  or `ProjectMember.objects.filter(workspace__slug=slug, project_id=project_id, ...)` query.
  **The workspace/project row is never separately fetched and never checked for existence
  in isolation** — a non-matching slug simply yields an empty queryset.

Consequences for API compatibility:

- A URL naming a **workspace the user is not an active member of** ⇒ the membership query is
  empty ⇒ **403** `{"error": "You don't have the required permissions."}` — *not* 404.
- A URL naming a **project_id that does not exist in that workspace**, or a project the user
  is not an active member of ⇒ empty `ProjectMember` query ⇒ **403** (same shape).
- A **detail endpoint that additionally re-fetches the object scoped to
  `workspace__slug=slug, project_id=project_id`** will instead raise
  `ObjectDoesNotExist` and return **404**. Example: `IssueViewSet.destroy`
  (`app/views/issue/base.py:716-718`) does
  `Issue.objects.get(workspace__slug=slug, project_id=project_id, pk=pk)`.
- **Unauthenticated** requests fail `IsAuthenticated` before any role check ⇒ **401**.

### 2.3 The 404-vs-403 decision table (critical for compatibility)

| Situation | Status | Body shape | Where |
|---|---|---|---|
| No session / bad API key | 401 | DRF default | `permission_classes = [IsAuthenticated]` |
| Authenticated, wrong workspace (not a member) | 403 | `{"error": "You don't have the required permissions."}` | `allow_permission`, `app/permissions/base.py:81` |
| Authenticated, member of workspace, **not** member of the project | 403 | same | `ProjectEntityPermission` etc. |
| Authenticated, member of project, role too low | 403 | same | `allow_permission` |
| Object id not in the URL's project/workspace (detail fetch scoped to URL) | 404 | `{"error": "The required object does not exist."}` | `app/views/base.py:92-96` |
| Cross-project **page** access | 403 | DRF `PermissionDenied` | `ProjectPagePermission` (`app/permissions/page.py:48-55`) |
| Public board (AllowAny) referencing a foreign object | 404 (issue/comment) / 400 (intake) | see §5.3 | `space/views/*` |
| Convert another user's draft | 404 | `ObjectDoesNotExist` | `test_draft_to_issue_owner_scope_app.py` |

> Note the body differs between the two API trees. The **app** API returns
> `"The required object does not exist."` (`app/views/base.py:94`); the **v1** API returns
> `"The requested resource does not exist."` (`api/views/base.py:95`). Replicate both.

### 2.4 Caching

`BaseViewSet`/`BaseAPIView` set `use_read_replica` per view
(`app/views/base.py:61`, `api/views/base.py:57`) — a **read/write DB routing** concern, not a
permission one. It must **not** be allowed to serve a stale membership row from a replica in
a way that grants or denies incorrectly; resolve membership from the primary (or accept the
documented replica lag, as Plane does).

---

## 3. Permission matrix

Legend: **A** = allow, **D** = deny (403), **C** = conditional (see note). "own" means the
row was created by the caller (`created_by == request.user`).

### 3.1 Workspace-scoped capabilities

Columns are the caller's **workspace role**. All rows require an active `WorkspaceMember`.

| Capability | WS Admin (20) | WS Member (15) | WS Guest (5) | Source |
|---|---|---|---|---|
| `workspace.create` | A | A | A | any authenticated user; `WorkSpaceBasePermission` POST → `True` (`app/permissions/workspace.py:25-26`) |
| `workspace.read` | A | A | A | `WorkspaceViewerPermission` (`workspace.py:93-100`) |
| `workspace.update` (PUT/PATCH) | A | A | D | `WorkSpaceBasePermission` (`workspace.py:33-39`) |
| `workspace.delete` | A | D | D | `workspace.py:42-48` (role == 20) |
| `workspace.member.list` | A | A | A | `WorkSpaceMemberViewSet.list` (`app/views/workspace/member.py:45-55`); guests get a reduced serializer |
| `workspace.member.invite` | A | A | D | `WorkspaceInvitationsViewset`, `permission_classes=[WorkSpaceAdminPermission]` (`app/views/workspace/invite.py:36-52`) |
| `workspace.member.update_role` | A | D | D | `member.py:76-96`; also cannot change own role (400) |
| `workspace.member.remove` | A | D | D | `member.py:98-150`; cannot remove self (400), cannot remove a higher role (400) |
| `workspace.leave` | A | A | A | `member.py:160-204`; sole-admin guards return 400 |
| `workspace.api_token.manage` | A | A | A | self-scoped; `ApiTokenEndpoint` (see §5.4) |
| `workspace.export_issues` | A | A | D | `ExportIssuesEndpoint`, `@allow_permission([ROLE.ADMIN, ROLE.MEMBER], level="WORKSPACE")` — **excluded area**, listed for completeness |
| `workspace.webhook.manage` | A | D | D | `WebhookEndpoint`, `@allow_permission([ROLE.ADMIN], level="WORKSPACE")` — **excluded area** |
| `workspace.ai_assistant` | A | A | D | `WorkspaceGPTIntegrationEndpoint`, `@allow_permission([ROLE.ADMIN, ROLE.MEMBER], level="WORKSPACE")` — **excluded area** |

### 3.2 Project-scoped capabilities

Columns are the caller's **project role**. Every row additionally requires an active
`WorkspaceMember` (the `allow_permission` decorator resolves `slug` for the workspace and
`project_id` for the project). **WS-admin bypass:** a workspace admin (20) who is an active
`ProjectMember` passes every row below regardless of project role.

| Capability | PJ Admin (20) | PJ Member (15) | PJ Guest (5) | Source |
|---|---|---|---|---|
| `project.create` | A | A | D | workspace-scoped; `ProjectViewSet.create`, `@allow_permission([ADMIN, MEMBER], level="WORKSPACE")` (`app/views/project/base.py:257`) |
| `project.read` | A | A | A | `@allow_permission([ADMIN, MEMBER, GUEST], level="WORKSPACE")` (`project/base.py:225`) — guests see projects they are a member of |
| `project.update` | A | D | D | inline check: project admin **or** workspace admin (`project/base.py:314-336`) |
| `project.delete` | A | D | D | inline check: project admin **or** workspace admin (`project/base.py:382-399`) |
| `project.archive` | A | D | D | `@allow_permission([ADMIN, MEMBER])` (`project/base.py:428`) — note: **Member allowed** here, unlike update/delete |
| `project.member.invite` | A | D | D | `ProjectInvitationsViewset` (see `app/urls/project.py:53`) |
| `project.member.update` | A | D | D | `ProjectMemberViewSet.partial_update` — role-comparison guard; pinned by `test_project_member_is_active_authz.py` |
| `project.member.remove` | A | D | D | same guard as update |
| `issue.read` | A | A | C | `@allow_permission([ADMIN, MEMBER, GUEST])` (`issue/base.py:265`); guest restricted to own issues unless `guest_view_all_features` (§5.1) |
| `issue.create` | A | A | D | `@allow_permission([ADMIN, MEMBER])` (`issue/base.py:404`) |
| `issue.update` | A | C (own) | C (own) | `@allow_permission([ADMIN, MEMBER], creator=True, model=Issue)` (`issue/base.py:627`); `creator=True` lets a **guest who created it** update it |
| `issue.update.own` | A | A | A | the `creator=True` branch: `created_by == request.user` short-circuits the role list |
| `issue.delete` | A | C (own) | C (own) | `@allow_permission([ADMIN], creator=True, model=Issue)` (`issue/base.py:716`); only admin or the creator |
| `issue.bulk_delete` | A | D | D | `BulkDeleteIssuesEndpoint`, `@allow_permission([ADMIN])` (`issue/base.py:774`) |
| `issue.restore` | A | D | D | `@allow_permission([ADMIN])` (`issue/base.py:766` region) |
| `issue_comment.create` | A | A | A | `@allow_permission([ADMIN, MEMBER, GUEST])` (`issue/comment.py:63`) |
| `issue_comment.update` | A | C (own) | C (own) | `@allow_permission([ADMIN], creator=True, model=IssueComment)` (`issue/comment.py:109`) |
| `issue_comment.delete` | A | C (own) | C (own) | `@allow_permission([ADMIN], creator=True, model=IssueComment)` (`issue/comment.py:144`) |
| `cycle.read` | A | A | A | `@allow_permission([ADMIN, MEMBER, GUEST])` (`cycle/base.py:183`) |
| `cycle.manage` (create/update/delete) | A | A | D | `@allow_permission([ADMIN, MEMBER])` (`cycle/base.py:270,335,410`) |
| `cycle.delete.own` | A | C | C | `@allow_permission([ADMIN], creator=True, model=Cycle)` (`cycle/base.py:477`) |
| `cycle.transfer_issues` | A | A | D | `@allow_permission([ADMIN, MEMBER])` (`cycle/base.py:594`) |
| `module.read` | A | A | A | `@allow_permission([ADMIN, MEMBER, GUEST])` (`module/base.py:353`) |
| `module.manage` | A | A | D | `@allow_permission([ADMIN, MEMBER])` (`module/base.py:294,395,651`) |
| `module.delete.own` | A | C | C | `@allow_permission([ADMIN], creator=True, model=Module)` (`module/base.py:723`) |
| `intake.read` | A | A | A | `@allow_permission([ADMIN, MEMBER, GUEST])` (`intake/base.py:177,228`) |
| `intake.manage` (create/update intake) | A | A | D | `@allow_permission([ADMIN, MEMBER])` (`intake/base.py:73,78,82`) |
| `intake.delete_issue` | A | C (own) | C (own) | `@allow_permission([ADMIN], creator=True, model=Issue)` (`intake/base.py:552`) |
| `state.read` | A | A | A | `@allow_permission([ADMIN, MEMBER, GUEST])` (`state/base.py:61,77`) |
| `state.manage` (create/update/delete) | A | D | D | `@allow_permission([ADMIN])` (`state/base.py:46,105,113`) |
| `page.read` | A | A | C | `ProjectPagePermission` (`app/permissions/page.py`); public pages readable by all active project roles, private pages owner-only (§5.5) |
| `page.create` | A | A | D | `_check_project_action_access` POST → `[ADMIN, MEMBER]` (`page.py:104-107`) |
| `page.update` | A | A | D | PUT/PATCH → `[ADMIN, MEMBER]` (`page.py:116-119`) |
| `page.delete` | A | D | D | DELETE → `[ADMIN]` (`page.py:122-125`) |
| `page.version.read` | A | A | C | `PageVersionEndpoint` scoped to `project_id`; pinned by `test_page_version_project_scope_app.py` |
| `deploy_board.read` | A | A | A | `DeployBoardViewSet`, `permission_classes=[ProjectMemberPermission]` (`project/base.py:535-540`); non-members 403 |
| `deploy_board.manage` | A | D | D | project admin (`ProjectMemberPermission` non-safe path) |
| `label.manage` | A | A | D | `@allow_permission([ADMIN, MEMBER])` (label views) |
| `estimate.manage` | A | A | D | estimate views, `[ADMIN, MEMBER]` |
| `sticky.manage` | A | A | C | sticky views; guests may own stickies |

Capability count: **13 workspace-scoped + 39 project-scoped = 52** rows.

> The `ROLE` enum used by decorators lives at `apps/api/plane/app/permissions/base.py:13`.
> A second, used copy lives at `apps/api/plane/utils/permissions/base.py:13` (imported by
> `api/serializers/member.py:12`, `api/views/invite.py:20`, `api/views/member.py:23`).

---

## 4. The `allow_permission` algorithm (reference implementation)

This is the single most-replicated primitive. Faithful port from
`app/permissions/base.py:19-88`:

```
def allow_permission(allowed_roles, level="PROJECT", creator=False, model=None):
    # 1. creator short-circuit (only when creator=True and model given)
    if creator and model:
        if not WorkspaceMember(member=user, workspace.slug=slug, is_active=True).exists():
            return 403                                   # app/ variant only
        if model.objects.filter(id=pk, created_by=user).exists():
            return ALLOW

    roles = [r.value for r in allowed_roles]

    # 2. workspace level
    if level == "WORKSPACE":
        if WorkspaceMember(member=user, workspace.slug=slug, role in roles, is_active=True).exists():
            return ALLOW

    # 3. project level
    else:
        if ProjectMember(member=user, workspace.slug=slug, project_id=pid, role in roles, is_active=True).exists():
            return ALLOW
        elif (ProjectMember(member=user, workspace.slug=slug, project_id=pid, is_active=True).exists()
              and WorkspaceMember(member=user, workspace.slug=slug, role=20, is_active=True).exists()):
            return ALLOW                                     # ws-admin bypass

    return 403 {"error": "You don't have the required permissions."}
```

Key semantics to preserve:

1. **`creator=True` is checked first and grants access regardless of role list.** It is the
   "own resource" escape hatch used by issue/cycle/module/intake/comment delete & update.
2. `creator=True` requires `kwargs["pk"]` (or the model's pk in kwargs) to be present.
3. The ws-admin bypass requires an **active `ProjectMember` row** — a workspace admin who is
   not in the project does *not* get in via this branch (they get in via `WorkspaceEntityPermission`
   paths only where a workspace-level check is used).
4. Denial is always **403**, never 404, at this layer.

---

## 5. Special cases

### 5.1 Guests and `guest_view_all_features`

`Project.guest_view_all_features` (`db/models/project.py:100`, `default=False`) widens what a
project **guest** can read.

- **Default (`False`):** a project guest may only read work items they authored. This is
  enforced in the **queryset**, not the permission class. `IssueViewSet.list` scopes to
  `created_by=request.user` for such guests; `IssueListEndpoint.get` was fixed to do the same
  (`tests/contract/app/test_issue_list_guest_scope_app.py`, GHSA-32c7-84jc-4w67).
- **`True`:** the guest sees all issues in the project.

The Go layer must apply this filter on **every** issue-listing path, including the
`?issues=<id,id>` "fetch by id" variant, because that endpoint previously bypassed it.

Guests are also the default role: both `WorkspaceMember.role` and `ProjectMember.role`
default to `5` (`workspace.py:205`, `project.py:219`).

### 5.2 Secret vs public projects (`ProjectNetwork`)

`db/models/project.py:30-36`:

```python
class ProjectNetwork(Enum):
    SECRET = 0
    PUBLIC = 2
    @classmethod
    def choices(cls): return [(0, "Secret"), (2, "Public")]
```

`Project.network` defaults to **`2` (Public)** (`project.py:74`).

**Critical:** in v1.4.2 `ProjectNetwork` is **not consulted by any permission class**. Every
authorization path is based on `ProjectMember`/`WorkspaceMember` rows. "Public" here is a UI
concept (board/visibility), not an authorization grant. The **actual** anonymous read path is
`DeployBoard` (§5.3), which is a separate opt-in row.

**Do not** grant unauthenticated or non-member access based on `network == PUBLIC`. Doing so
would diverge from Plane. Flagged in §8.

### 5.3 `DeployBoard` public (anonymous) access

The only genuinely anonymous surface is the public Spaces API
(`/api/public/...`, `apps/api/plane/space/`), gated by a `DeployBoard` row whose `anchor`
appears in the URL.

- `space/views/project.py:19` `ProjectDeployBoardPublicSettingsEndpoint` — `AllowAny`.
- `space/views/project.py:28` `WorkspaceProjectDeployBoardEndpoint` — `AllowAny`.
- `space/views/project.py:54` `WorkspaceProjectAnchorEndpoint` — `AllowAny`.
- `space/views/project.py:65` `ProjectMembersEndpoint` — `AllowAny`.

Because these are `AllowAny`, object-level scoping replaces role checks, and the **status
codes are the object-binding contract** (pinned by
`tests/contract/app/test_spaces_board_object_scope_app.py`):

| Attempt | Expected |
|---|---|
| Comment / react / vote on an issue outside the board's project | **404** |
| React to a comment outside the board's project | **404** |
| Comment on an issue in a **different workspace** | **404** |
| Create intake issue against a foreign `intake_id` | **400** |
| Vote when `DeployBoard.is_votes_enabled == False` | **400** |
| Reaction on an `INTERNAL` (non-public) comment via public endpoints | not listed / **404** on delete |

Every caller-supplied object id (`issue_id`, `comment_id`, `intake_id`, reaction code) must be
bound to **the board's `project_id` AND `workspace_id`** before read or write. Comment lists
must be filtered to the board's project and to `access="EXTERNAL"`.

The `DeployBoardViewSet` *admin* side (`app/views/project/base.py:535`) is **not** anonymous:
it uses `ProjectMemberPermission`, and a workspace member who is not a project member gets
**403** (`test_deploy_board_project_scope_app.py`).

### 5.4 API-token access (`APIToken`)

Model: `db/models/api.py:23-39`.

```python
class APIToken(BaseModel):
    label, description, is_active(default True), token(unique),
    user_type(0 Human / 1 Bot), workspace(FK, null=True),
    expired_at(null), is_service(default False), allowed_rate_limit("60/min")
```

- Authentication: `X-Api-Key` header, validated by `APIKeyAuthentication`
  (`app/middleware/api_authentication.py` and the duplicate
  `api/middleware/api_authentication.py`). The token resolves to its **owning user**, and all
  authorization then runs **as that user** — there is no separate token role.
- Validity: `expired_at > now() OR expired_at IS NULL`, `is_active=True`, `user__is_active=True`.
  Invalid → `AuthenticationFailed` ("Given API token is not valid") → **401**.
- **Workspace scope:** `APIToken.workspace` is an FK but is **not enforced** by the auth class;
  the token is effectively workspace-scoped only in the sense that the UI creates it for a
  workspace. Authorization still uses the owning user's `WorkspaceMember`/`ProjectMember` rows.
  A token therefore has **read/write** access to whatever its user can reach.
- The v1 API (`/api/v1/`, `api/views/base.py`) uses `authentication_classes=[APIKeyAuthentication]`
  and rate-limits via `ApiKeyRateThrottle` (`api/views/base.py:69-70`), returning
  `X-RateLimit-Remaining` / `X-RateLimit-Reset` headers (`api/views/base.py:123-136`).
- **Service tokens** (`is_service=True`) are hidden from the user token endpoints and are
  read-only via those endpoints (404 on GET/PATCH/DELETE) — see `test_api_token.py`.
- Token management is **self-scoped**: another user's token id returns **404**, not 403
  (`test_api_token.py::test_get_other_users_api_token`).

### 5.5 Soft-deleted entities

Plane uses soft delete (`deleted_at`) via `BaseModel`/`ProjectBaseModel`. Authorization
implications:

- The permission classes do **not** filter `deleted_at`; the **querysets** do. Default
  managers exclude soft-deleted rows; `all_objects` includes them.
- The **page → project link** (`ProjectPage`) is the sharpest case: `ProjectPagePermission`
  requires an **active** link —
  `project_pages__deleted_at__isnull=True` on the *same* relation
  (`app/permissions/page.py:48-53`). A page whose `ProjectPage` link was soft-deleted is
  denied even to a member of the project (GHSA-g49r / GHSA-ghcr,
  `test_page_version_project_scope_app.py::test_revoked_project_link_denied`).
- `PageVersionEndpoint` must resolve the page/version scoped to the **URL's project_id**, not
  just the workspace + page_id (the original bug: 200 leak → now **403**).
- Cleanup of expired rows happens in `bgtasks/cleanup_task.py` (e.g. `delete_webhook_logs`),
  out of scope for authorization.

### 5.6 "Own resource" rules

The `creator=True` branch is the entire own-resource model. Concretely:

| Question | Answer | Evidence |
|---|---|---|
| Can a Member edit someone else's issue? | **Yes** (Member has role 15 in the list). | `issue/base.py:627` |
| Can a Member edit someone else's **comment**? | **No.** Only a project admin or the comment's creator. | `issue/comment.py:109` |
| Can a Guest delete their own issue? | **Yes** (`creator=True`). | `issue/base.py:716` |
| Can a Guest delete someone else's issue? | **No.** | `issue/base.py:716` |
| Can a Guest read someone else's issue? | **Only if** `guest_view_all_features=True`, else no. | `issue_list_guest_scope_app.py` |
| Can a Member deactivate another project member? | **No.** Role-comparison guard; 403. | `test_project_member_is_active_authz.py` |
| Can a workspace admin deactivate a project admin (holding only project guest)? | **Yes** (ws-admin bypass). | same file, `test_workspace_admin_with_low_project_role_can_deactivate` |
| Can any member convert another user's **draft** to an issue? | **No.** 404; drafts are private to their creator. | `test_draft_to_issue_owner_scope_app.py` |

**Draft-to-issue** (`WorkspaceDraftIssueViewSet.create_draft_to_issue`,
`app/views/workspace/draft.py`) deserves emphasis: the decorator admits ADMIN/MEMBER but the
lookup must additionally be scoped `created_by=request.user`, returning **404** (not 403) for
another user's draft, and must not reassign the draft's `FileAsset` rows or delete the draft.

---

## 6. Implementation guidance for the Go / PocketBase layer

### 6.1 Where the logic should live

**One middleware that resolves + caches the authorized context, plus per-handler checks.**

1. **`authz.Context` middleware** (runs after authentication, before handlers):
   - Parse `slug` and `project_id` (with the `url_name == "project" → pk` fallback from
     `app/views/base.py:130-136`).
   - Resolve and attach: `WorkspaceMember{role, is_active}` for the caller, and — if
     `project_id` is present — `ProjectMember{role, is_active}`.
   - Compute two booleans once: `isWorkspaceAdmin` (role==20, active) and
     `isProjectMember` (active row exists).
   - **Cache per request only.** Do not cache across requests in a way that survives a
     membership change; Plane re-queries on every request. If you add a short TTL cache,
     invalidate it on membership mutation.
   - Attach nothing user-visible; a missing membership is **not** an error here (Plane defers
     the 403/404 decision to the handler).

2. **Per-handler authorization** mirroring the decorator:
   - Implement a `RequireRole(level, roles, creatorModel)` guard used at the top of each
     handler, reproducing §4 exactly (creator short-circuit first, then role list, then
     ws-admin bypass, else 403).
   - Implement `ResolveProjectScopedObject(...)` for detail endpoints that must 404 when the
     object is not in the URL's project/workspace.

3. **Query-level scoping** (do not rely on the guard alone):
   - Guest issue lists: `WHERE created_by = caller` unless `guest_view_all_features`.
   - `PageVersion`: `WHERE workspace = :ws AND project_id = :pid`.
   - Public boards: bind every caller-supplied id to `board.project_id AND board.workspace_id`.

### 6.2 Error codes and shapes to match

| Case | Status | Body |
|---|---|---|
| Unauthenticated | 401 | DRF default (no `{"error":...}` required) |
| Permission denied (role/scope) | 403 | `{"error": "You don't have the required permissions."}` |
| Object not in scope (app API) | 404 | `{"error": "The required object does not exist."}` |
| Object not in scope (v1 API) | 404 | `{"error": "The requested resource does not exist."}` |
| Invalid JSON body (v1, non-container) | 400 | `{"error": "Request body must be a JSON object or array."}` (`api/views/base.py:35`) |
| Generic server error | 500 | `{"error": "Something went wrong please try again later"}` |

Do **not** invent `{"detail": ...}` shapes; Plane's clients read the `error` key.

### 6.3 Cross-cutting notes

- Reproduce the **`is_active` filter everywhere**, including the ws-admin bypass.
- Reproduce the **creator-first ordering**; a naive `role in allowed_roles` check will wrongly
  deny a guest deleting their own issue/comment/cycle/module.
- The `creator=True` branch needs the object pk in the URL kwargs. Endpoints that pass the pk
  in the body instead will not get the own-resource allowance — match Plane.
- Keep the **`utils.permissions` vs `app.permissions` divergence** in mind (§7); the v1
  member/invite views use the `utils` copy.

---

## 7. Known divergences in the source (port both, do not "fix")

Two near-duplicate permission packages exist and are **both imported**:

- `apps/api/plane/app/permissions/*` — used by `app/views/*` (the bulk of the API).
- `apps/api/plane/utils/permissions/*` — imported by `api/serializers/member.py:12`,
  `api/views/invite.py:20`, `api/views/member.py:23`.

They differ in one place: the **creator short-circuit** in
`utils/permissions/base.py:24-27` omits the workspace-membership check that
`app/permissions/base.py:26-34` performs. Practically, the `utils` variant lets a creator
through without first confirming workspace membership. Because the v1 API depends on the
`utils` copy, replicate the per-package behaviour per endpoint rather than unifying.

Also note `utils/permissions/workspace.py` lacks the `WorkspaceMemberPermission` class that
`app/permissions/workspace.py:113-137` defines (used by `FileAssetEndpoint` to resolve by
`slug` *or* `workspace_id`).

---

## 8. Ambiguities flagged (not decided here)

1. **`ProjectNetwork.PUBLIC` grants nothing in code.** It is tempting to treat "public" as an
   authorization grant, but v1.4.2 does not. Confirm with product whether the new backend
   should preserve that (recommended: yes, preserve).
2. **`DeployBoard` is partly a permission surface and partly an excluded integration.**
   The board *admin* endpoints stay; the public board API is core. But the board's
   `intake` linkage and comment/reaction endpoints overlap with the integration areas being
   removed. See `exclusions.md` cross-check.
3. **`unsplash`** is a third-party integration that is **not** on the exclusion list. It
   shares `app/urls/external.py` with the AI endpoints being deleted. Flagged in
   `exclusions.md`.
4. **`APIToken.workspace` is never enforced.** Treat tokens as user-scoped, not
   workspace-scoped, until product confirms otherwise.
5. **`read_replica` membership reads** could in principle serve a stale role. Plane accepts
   this; confirm the target's consistency requirements.

---

## 9. Contract-test traceability

| Test file | Rule it pins |
|---|---|
| `test_issue_list_guest_scope_app.py` | Guest issue list filtered to `created_by` unless `guest_view_all_features` |
| `test_project_member_is_active_authz.py` | `is_active` not writable without role check; ws-admin bypass preserved |
| `test_page_version_project_scope_app.py` | Page/version scoped to URL `project_id`; soft-deleted `ProjectPage` link denied |
| `test_deploy_board_project_scope_app.py` | `DeployBoardViewSet` SAFE methods scoped to `project_id`; non-member 403 |
| `test_draft_to_issue_owner_scope_app.py` | Draft conversion scoped to `created_by`; 404 otherwise |
| `test_spaces_board_object_scope_app.py` | Public board ids bound to project+workspace; 404/400 codes |
| `test_workspace_cycles_modules_project_scope_app.py` | Workspace cycles/modules lists filtered to caller's projects |
| `test_webhook_secret_key_scope_app.py` | `secret_key` never leaked on read (webhook area — excluded) |
| `test_api_token.py` | Token CRUD self-scoped; service tokens hidden; 404 not 403 |
| `test_workspace_file_asset_project_scope_app.py` | File assets scoped to project |
| `test_sub_issue_cross_project_scope_app.py` | Sub-issues cannot cross projects |
| `test_module_issue_destroy_app.py` | Module-issue link destroy scoping |
| `test_project_join_cross_project_activation.py` | Joining a project does not activate other projects |
| `test_project_members_roster_scope.py` | Project member roster scoping |
| `test_issue_history_app.py` | Issue history object scoping |
