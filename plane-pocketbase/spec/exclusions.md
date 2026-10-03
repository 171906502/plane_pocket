# Plane Feature Exclusions — Definitive Removal List

Source of truth: Plane **v1.4.2** at `apps/api/plane/` (backend) and `apps/web/`,
`apps/admin/`, `apps/space/`, `packages/` (frontend). **Every path below was verified to
exist.** Nothing in the Plane repo has been modified; this is a plan.

Five feature areas are dropped. For each: backend files/dirs to delete, API endpoints to
drop, frontend routes/components, and env vars / feature flags to clean up. §6 lists every
**kept** call site that touches an excluded module and its replacement. §7 flags ambiguous
items rather than deciding them.

Legend: **[DEL]** = delete the file/dir · **[EDIT]** = modify a kept file · **[DROP]** =
remove an endpoint · **[VERIFY]** = confirm before deleting.

---

## 1. AI

### 1.1 Backend — files and directories

| Path | Action | Notes |
|---|---|---|
| `apps/api/plane/app/views/external/base.py` | **[EDIT]** | Contains the AI code **and** `UnsplashEndpoint` (kept). Delete: `LLMProvider`, `OpenAIProvider`, `AnthropicProvider`, `GeminiProvider`, `SUPPORTED_PROVIDERS`, `get_llm_config`, `get_llm_response`, `GPTIntegrationEndpoint` (line 148), `WorkspaceGPTIntegrationEndpoint` (line 184). Keep `UnsplashEndpoint` (line 215). **See §7-A.** |
| `apps/api/plane/app/urls/external.py` | **[EDIT]** | **[DROP]** the two `ai-assistant` routes; keep `unsplash/`. |
| `apps/api/plane/app/views/__init__.py:183-185` | **[EDIT]** | Remove `GPTIntegrationEndpoint` and `WorkspaceGPTIntegrationEndpoint` from the import block; keep `UnsplashEndpoint` (line 184). |
| `apps/api/plane/utils/instance_config_variables/core.py:216-242` | **[EDIT]** | Delete `llm_config_variables` (lines 216-242) and its entry in `core_config_variables` (line 261). |
| `apps/api/plane/license/api/views/instance.py` | **[EDIT]** | Remove `LLM_API_KEY` from the config-fetch tuple (line 63) and its spec entry (line 112); remove `data["has_llm_configured"] = bool(LLM_API_KEY)` (line 139). |
| `apps/api/plane/requirements/base.txt` | **[EDIT]** | Remove the `openai` package (imported only by `external/base.py`). Verified present in this file. |
| `.env.example:31-34` | **[EDIT]** | Remove the `# GPT settings` block (`OPENAI_API_BASE`, `OPENAI_API_KEY`, `GPT_ENGINE`). |

### 1.2 API endpoints to drop

| Method | Path | Source |
|---|---|---|
| POST | `/api/workspaces/<slug>/projects/<project_id>/ai-assistant/` | `app/urls/external.py:15` (`GPTIntegrationEndpoint`) |
| POST | `/api/workspaces/<slug>/ai-assistant/` | `app/urls/external.py:20` (`WorkspaceGPTIntegrationEndpoint`) |

> Both routes are registered with `name="importer"` — a **mislabel**, not an importer route.
> Do not treat it as evidence of an importer endpoint.

> The web frontend also calls `POST /api/workspaces/<slug>/rephrase-grammar/`
> (`apps/web/services/ai.service.ts:43`, `packages/services/src/ai/ai.service.ts:68`).
> **No backend route for `rephrase-grammar` exists in v1.4.2** — it is a dead frontend call.
> Dropping the frontend call site is sufficient; there is nothing to remove server-side.

### 1.3 Frontend — routes, components, services

| Path | Action |
|---|---|
| `packages/services/src/ai/` (entire dir: `ai.service.ts`, `index.ts`) | **[DEL]** |
| `packages/services/src/index.ts:7` (`export * from "./ai";`) | **[EDIT]** remove |
| `apps/web/services/ai.service.ts` | **[DEL]** |
| `apps/web/components/core/modals/gpt-assistant-popover.tsx` | **[DEL]** |
| `apps/web/components/pages/editor/ai/` (entire dir: `menu.tsx`, `ask-pi-menu.tsx`, `index.ts`) | **[DEL]** |
| `apps/admin/app/(all)/(dashboard)/ai/` (entire dir: `page.tsx`, `form.tsx`) | **[DEL]** |
| `apps/admin/app/routes.ts:22` (`route("ai", "./(all)/(dashboard)/ai/page.tsx")`) | **[EDIT]** remove |
| `apps/admin/hooks/use-sidebar-menu/core.ts:44` (`href: "/ai/"`) | **[EDIT]** remove the AI menu entry |

**Kept files that merely *wire* AI** — these are core and must survive; remove only the AI
imports/usages (see §6.4):

- `apps/web/components/pages/editor/editor-body.tsx` (`:16`, `:47`, `:187-189`, `:304`)
- `apps/web/components/issues/issue-modal/components/description-editor.tsx`
- `apps/web/components/issues/issue-modal/form.tsx:106` (`gptAssistantModal` state)
- `apps/web/components/inbox/modals/create-modal/issue-description.tsx:40` (a `// TODO: have to implement GPT Assistance` comment only)

### 1.4 Env vars / flags to clean up

`LLM_API_KEY`, `LLM_PROVIDER`, `LLM_MODEL`, `GPT_ENGINE`, `OPENAI_API_KEY`,
`OPENAI_API_BASE`, and the derived instance-config flag `has_llm_configured`.

---

## 2. External integrations (GitHub, Slack)

### 2.1 Backend — files and directories

| Path | Action | Notes |
|---|---|---|
| `apps/api/plane/db/models/integration/` (entire dir: `__init__.py`, `base.py`, `github.py`, `slack.py`) | **[DEL]** | `Integration`, `WorkspaceIntegration`, `GithubRepository`, `GithubRepositorySync`, `GithubIssueSync`, `GithubCommentSync`, `SlackProjectSync` |
| `apps/api/plane/db/models/__init__.py:22-29` | **[EDIT]** | Remove the `.integration` import block |
| `apps/api/plane/db/models/social_connection.py` | **[VERIFY]** | `SocialLoginConnection` (medium choices Google/Github/GitLab/Jira). Already orphaned — only imported by `db/models/__init__.py`. OAuth/Jira-related. See §7-B. |
| `apps/api/plane/settings/common.py:358` | **[EDIT]** | Remove `GITHUB_ACCESS_TOKEN` |
| `apps/api/plane/license/api/views/instance.py` | **[EDIT]** | Remove `SLACK_CLIENT_ID` (line 60, spec line 106), `data["slack_client_id"]`; remove `GITHUB_APP_NAME` (line 56, spec line 96) and `data["github_app_name"]`. **Note:** `IS_GITHUB_ENABLED` etc. belong to OAuth (§3) — do not double-remove. |

### 2.2 API endpoints to drop

**None.** In v1.4.2 there are **no GitHub/Slack integration views or URLs** — verified by
grepping `app/urls/`, `api/urls/`, `space/urls/`, and `web/urls.py` (the `plane/web/` app is
only `robots.txt` + `health_check`). The integration **models are orphaned**: no route, view,
or serializer references them outside `db/models/`. Deletion is model-only.

### 2.3 Frontend — routes, components, services

| Path | Action |
|---|---|
| `apps/web/components/integration/github/select-repository.tsx` | **[DEL]** |
| `apps/web/components/integration/slack/select-channel.tsx` | **[DEL]** |
| `apps/web/components/integration/single-integration-card.tsx` | **[DEL]** |
| `apps/web/components/project/integration-card.tsx` | **[DEL]** |
| `apps/web/components/ui/loader/settings/integration.tsx` | **[DEL]** |
| `apps/web/hooks/use-integration-popup.tsx` | **[DEL]** |
| `apps/web/services/integrations/` (entire dir: `github.service.ts`, `index.ts`, `integration.service.ts`, `jira.service.ts`) | **[DEL]** — see §7-C re: `jira.service.ts` |
| `apps/web/app/(all)/[workspaceSlug]/(settings)/settings/(workspace)/integrations/page.tsx` | **[DEL]** (workspace settings route) |
| `apps/web/app/assets/empty-state/project-settings/integrations-*.webp` | **[DEL]** (4 files) |
| `apps/web/app/assets/empty-state/workspace-settings/integrations-*.webp` | **[DEL]** (4 files) |

### 2.4 Env vars / flags

`GITHUB_ACCESS_TOKEN`, `SLACK_CLIENT_ID`, `GITHUB_APP_NAME` (and the Slack/GitHub instance
config entries in `license/api/views/instance.py`).

---

## 3. OAuth login

### 3.1 Backend — files and directories

| Path | Action | Notes |
|---|---|---|
| `apps/api/plane/authentication/provider/oauth/` (entire dir: `__init__.py`, `gitea.py`, `github.py`, `gitlab.py`, `google.py`) | **[DEL]** | |
| `apps/api/plane/authentication/views/app/github.py` | **[DEL]** | |
| `apps/api/plane/authentication/views/app/gitlab.py` | **[DEL]** | |
| `apps/api/plane/authentication/views/app/gitea.py` | **[DEL]** | |
| `apps/api/plane/authentication/views/app/google.py` | **[DEL]** | |
| `apps/api/plane/authentication/views/space/github.py` | **[DEL]** | |
| `apps/api/plane/authentication/views/space/gitlab.py` | **[DEL]** | |
| `apps/api/plane/authentication/views/space/gitea.py` | **[DEL]** | |
| `apps/api/plane/authentication/views/space/google.py` | **[DEL]** | |
| `apps/api/plane/authentication/adapter/oauth.py` | **[VERIFY]** | OAuth-specific adapter. Confirm no kept provider imports it. See §7-D. |
| `apps/api/plane/authentication/views/__init__.py` | **[EDIT]** | Remove the OAuth imports for app + space (GitHub/GitLab/Gitea/Google) |
| `apps/api/plane/authentication/urls.py` | **[EDIT]** | **[DROP]** all OAuth routes (below) |
| `apps/api/plane/utils/instance_config_variables/core.py:38-163` | **[EDIT]** | Delete `google_config_variables`, `github_config_variables`, `gitlab_config_variables`, `gitea_config_variables`; remove them from `core_config_variables` (lines 257-260) |
| `apps/api/plane/license/api/views/instance.py` | **[EDIT]** | Remove `IS_GOOGLE_ENABLED`, `IS_GITHUB_ENABLED`, `IS_GITLAB_ENABLED`, `IS_GITEA_ENABLED` and the `data["is_*_enabled"]` fields |

### 3.2 API endpoints to drop (from `authentication/urls.py`)

Under the `/auth/` prefix:

| Path | Name |
|---|---|
| `google/` | `google-initiate` |
| `google/callback/` | `google-callback` |
| `spaces/google/` | `space-google-initiate` |
| `spaces/google/callback/` | `space-google-callback` |
| `github/` | `github-initiate` |
| `github/callback/` | `github-callback` |
| `spaces/github/` | `space-github-initiate` |
| `spaces/github/callback/` | `space-github-callback` |
| `gitlab/` | `gitlab-initiate` |
| `gitlab/callback/` | `gitlab-callback` |
| `spaces/gitlab/` | `space-gitlab-initiate` |
| `spaces/gitlab/callback/` | `space-gitlab-callback` |
| `gitea/` | `gitea-initiate` |
| `gitea/callback/` | `gitea-callback` |
| `spaces/gitea/` | `space-gitea-initiate` |
| `spaces/gitea/callback/` | `space-gitea-callback` |

**Keep** (not OAuth): `sign-in/`, `sign-up/`, `sign-out/`, `magic-*`, `email-check/`,
`forgot-password/`, `reset-password/`, `change-password/`, `set-password/`,
`get-csrf-token/`, and their `spaces/*` equivalents.

### 3.3 Frontend — routes, components

| Path | Action |
|---|---|
| `apps/web/hooks/oauth/` (entire dir: `core.tsx`, `extended.tsx`, `index.ts`) | **[DEL]** |
| `apps/space/hooks/oauth/` (entire dir: `core.tsx`, `extended.tsx`, `index.ts`) | **[DEL]** |
| `apps/web/google.d.ts` | **[DEL]** |
| `apps/web/app/assets/logos/{gitea-logo.svg,github-black.png,github-dark.svg,github-square.png,github-white.png,gitlab-logo.svg,google-logo.svg}` | **[DEL]** |

**Kept files that consume the OAuth hooks** — remove only the OAuth wiring (§6.5):

- `apps/web/components/account/auth-forms/auth-root.tsx:24,56` (`useOAuthConfig`)
- `apps/space/components/account/auth-forms/auth-root.tsx:18,88` (`useOAuthConfig`)
- `apps/web/helpers/authentication.helper.tsx` (`EAuthenticationErrorCodes` entries for
  Google/GitHub/GitLab at lines 76-81 and 266-278) and `apps/space/helpers/authentication.helper.tsx`

### 3.4 Env vars / flags

`IS_GOOGLE_ENABLED`, `IS_GITHUB_ENABLED`, `IS_GITLAB_ENABLED`, `IS_GITEA_ENABLED`,
`GITHUB_APP_NAME`, `GOOGLE_CLIENT_ID`/`GOOGLE_CLIENT_SECRET`,
`GITHUB_CLIENT_ID`/`GITHUB_CLIENT_SECRET`, `GITLAB_*`, `GITEA_*` (as present in the
google/github/gitlab/gitea config blocks), and the instance-config flags `is_google_enabled`,
`is_github_enabled`, `is_gitlab_enabled`, `is_gitea_enabled`, `github_app_name`.

---

## 4. Import / Export

### 4.1 Import (`Importer`)

| Path | Action | Notes |
|---|---|---|
| `apps/api/plane/db/models/importer.py` | **[DEL]** | `Importer(ProjectBaseModel)`; has an FK to `APIToken` (`related_name="importer"`) |
| `apps/api/plane/db/models/__init__.py:20` | **[EDIT]** | Remove `from .importer import Importer` |
| `apps/api/plane/app/serializers/importer.py` | **[DEL]** | `ImporterSerializer` |
| `apps/api/plane/app/serializers/__init__.py:98` | **[EDIT]** | Remove `from .importer import ImporterSerializer` |

**Endpoints: none.** Confirmed: `ImporterSerializer` exists and is exported, but there is
**no importer view and no importer route** anywhere in `app/urls/`, `api/urls/`,
`space/urls/`, or the root `urls.py`. The only `name="importer"` in the codebase is the
**AI** route in `app/urls/external.py` (a mislabel, §1.2). So removing the `Importer` model
has **zero route-level impact**; the only breakage risk is the two `__init__.py` imports.

### 4.2 Export (`ExporterHistory`)

| Path | Action |
|---|---|
| `apps/api/plane/db/models/exporter.py` | **[DEL]** (`ExporterHistory`) |
| `apps/api/plane/db/models/__init__.py:19` | **[EDIT]** remove the import |
| `apps/api/plane/app/serializers/exporter.py` | **[DEL]** (`ExporterHistorySerializer`) |
| `apps/api/plane/app/serializers/__init__.py:127` | **[EDIT]** remove the import |
| `apps/api/plane/app/views/exporter/` (entire dir: `base.py` → `ExportIssuesEndpoint`) | **[DEL]** |
| `apps/api/plane/app/views/__init__.py:226` | **[EDIT]** remove `from .exporter.base import ExportIssuesEndpoint` |
| `apps/api/plane/app/urls/exporter.py` | **[DEL]** |
| `apps/api/plane/app/urls/__init__.py:24,46` | **[EDIT]** remove the `exporter_urls` import and its spread |
| `apps/api/plane/bgtasks/export_task.py` | **[DEL]** (`issue_export_task`) |
| `apps/api/plane/bgtasks/exporter_expired_task.py` | **[DEL]** |

### 4.3 API endpoints to drop

| Method | Path | Source |
|---|---|---|
| POST, GET | `/api/workspaces/<slug>/export-issues/` | `app/urls/exporter.py:12` (`ExportIssuesEndpoint`, name `export-issues`) |

> **Not in scope (flag §7-E):** `POST /api/workspaces/<slug>/user-activity/<uuid:user_id>/export/`
> (`ExportWorkspaceUserActivityEndpoint`, `app/views/workspace/base.py:339`) and
> `POST /api/workspaces/<slug>/export-analytics/` (`ExportAnalyticsEndpoint`,
> `app/urls/analytic.py:46`). These are exports but are **not** `ExporterHistory`-based.

### 4.4 Frontend

The import/export UI lives under workspace/project settings. **Flag §7-F:** locate the
importer/exporter UI by grepping `apps/web` for `exporter`, `import-export`, `export-issues`,
`ImporterService`, `ExporterService` before deleting — it was not enumerated in the brief's
path list and should be confirmed rather than assumed. There is no AI-style service class
dedicated to export in `packages/services`.

### 4.5 Env vars

None specific. (`ExporterHistory` uses a generated token, not an env var.)

---

## 5. Outbound webhooks

### 5.1 Backend — files and directories

| Path | Action |
|---|---|
| `apps/api/plane/db/models/webhook.py` | **[DEL]** (`Webhook`, `WebhookLog`, `ProjectWebhook`) |
| `apps/api/plane/db/models/__init__.py:67` | **[EDIT]** remove `from .webhook import Webhook, WebhookLog` |
| `apps/api/plane/app/serializers/webhook.py` | **[DEL]** (`WebhookSerializer`, `WebhookLogSerializer`) |
| `apps/api/plane/app/serializers/__init__.py:129` | **[EDIT]** remove the import |
| `apps/api/plane/app/views/webhook/` (entire dir: `base.py`) | **[DEL]** (`WebhookEndpoint`, `WebhookSecretRegenerateEndpoint`, `WebhookLogsEndpoint`) |
| `apps/api/plane/app/views/__init__.py:230-232` | **[EDIT]** remove the three exports |
| `apps/api/plane/app/urls/webhook.py` | **[DEL]** |
| `apps/api/plane/app/urls/__init__.py:23,47` | **[EDIT]** remove the `webhook_urls` import and spread |
| `apps/api/plane/bgtasks/webhook_task.py` | **[DEL]** (`model_activity`, `webhook_activity`, `webhook_send_task`, `save_webhook_log`, `send_webhook_deactivation_email`, `get_model_data`) |
| `apps/api/plane/bgtasks/cleanup_task.py` | **[EDIT]** remove `WebhookLog` import (line 25), `get_webhook_logs_queryset` (line ~149), and `delete_webhook_logs` (line ~203) |
| `apps/api/plane/settings/common.py:59-88,445` | **[EDIT]** remove `WEBHOOK_ALLOWED_IPS`, `WEBHOOK_ALLOWED_HOSTS`, `WEBHOOK_DISALLOWED_DOMAINS`, `WEBHOOK_LOG_RETENTION_DAYS` |
| `apps/api/plane/utils/url_security.py` | **KEEP** — `pinned_fetch` is still used by `bgtasks/work_item_link_task.py` (lines 21, 96, 194) and `authentication/adapter/base.py` (line 14). Only the webhook import is removed. |

### 5.2 API endpoints to drop (from `app/urls/webhook.py`)

| Method(s) | Path | View |
|---|---|---|
| GET, POST | `/api/workspaces/<slug>/webhooks/` | `WebhookEndpoint` |
| GET, PATCH, DELETE | `/api/workspaces/<slug>/webhooks/<uuid:pk>/` | `WebhookEndpoint` |
| POST | `/api/workspaces/<slug>/webhooks/<uuid:pk>/regenerate/` | `WebhookSecretRegenerateEndpoint` |
| GET | `/api/workspaces/<slug>/webhook-logs/<uuid:webhook_id>/` | `WebhookLogsEndpoint` |

### 5.3 Frontend — routes, components, stores, services

| Path | Action |
|---|---|
| `apps/web/components/web-hooks/` (entire dir: `create-webhook-modal.tsx`, `delete-webhook-modal.tsx`, `webhooks-list-item.tsx`, `webhooks-list.tsx`) | **[DEL]** |
| `apps/web/hooks/store/use-webhook.ts` | **[DEL]** |
| `apps/web/services/webhook.service.ts` | **[DEL]** |
| `apps/web/store/workspace/webhook.store.ts` | **[DEL]** |
| `apps/web/app/(all)/[workspaceSlug]/(settings)/settings/(workspace)/webhooks/` (entire dir: `page.tsx`, `header.tsx`, `[webhookId]/page.tsx`, `[webhookId]/header.tsx`) | **[DEL]** |
| `apps/web/app/assets/empty-state/workspace-settings/webhooks-*.webp` | **[DEL]** (4 files) |

### 5.4 Env vars / flags

`WEBHOOK_ALLOWED_IPS`, `WEBHOOK_ALLOWED_HOSTS`, `WEBHOOK_DISALLOWED_DOMAINS`,
`WEBHOOK_LOG_RETENTION_DAYS`.

---

## 6. Must keep working after removal

This section enumerates every **kept** call site that references an excluded module, and the
replacement.

### 6.1 Outbound webhooks — 24 call sites across 9 files (not ~10)

The prior estimate of "~10" was **low**. Verified by grepping
`model_activity\.delay|webhook_activity\.delay` across `app/`, `api/`, `space/`
(excluding `bgtasks/webhook_task.py` itself): **24 call sites, 9 import lines, 9 files.**

| File | Import line | `model_activity.delay` call sites | `webhook_activity.delay` call sites |
|---|---|---|---|
| `app/views/cycle/base.py` | 60 | 318, 397 | — |
| `app/views/issue/base.py` | 46 | 473, 698 | — |
| `app/views/issue/comment.py` | 25 | 97, 132 | — |
| `app/views/module/base.py` | 65 | 339, 708 | — |
| `app/views/project/base.py` | 27 | 300, 369 | 400 |
| `api/views/cycle.py` | 53 | 339, 595 | — |
| `api/views/issue.py` | 92 | 516, 676, 736, 823, 1508, 1647 | — |
| `api/views/module.py` | 44 | 231, 489 | — |
| `api/views/project.py` | 41 | 291, 585 | 633 |

**All 24 are fire-and-forget** — the return value of `.delay(...)` is never read, awaited, or
stored. They exist solely to dispatch webhook notifications.

**Replacement:** remove the import line and delete the `.delay(...)` invocation in each of
the 9 files. Two call sites need care:

1. `api/views/project.py:290-301` — `model_activity.delay(...)` is wrapped in a nested
   function `_dispatch_model_activity` registered via
   `transaction.on_commit(_dispatch_model_activity, robust=True)`. Remove the nested function
   **and** its `on_commit` registration (or point the `on_commit` at a no-op). Do **not**
   leave a dangling `on_commit` callback that imports the deleted task.
2. `app/views/project/base.py:400` and `api/views/project.py:633` — direct
   `webhook_activity.delay(event="project", verb="deleted", ...)` inside the project
   `destroy`. Remove the call; the surrounding `project.delete()` and response must remain.

Also in this area:

- `bgtasks/cleanup_task.py` imports `WebhookLog` (line 25) → remove the import, the
  `get_webhook_logs_queryset` helper, and the `delete_webhook_logs` task.
- `ProjectWebhook` is referenced **nowhere** outside its own model definition (verified) → no
  call-site changes; deleting the model is safe.

### 6.2 Import (`Importer`)

- `ImporterSerializer` is exported by `app/serializers/__init__.py:98` but **has no route and
  no view** → the only kept references are the model import (`db/models/__init__.py:20`) and
  the serializer import. Removing both `__init__.py` lines is the complete replacement.
- **Confirmed:** `Importer` has a serializer but **no route**.

### 6.3 Export (`ExporterHistory`)

- Kept references: `app/serializers/__init__.py:127`, `app/views/__init__.py:226`,
  `app/urls/__init__.py:24,46`. All four are edited (removed) as part of §4.2. No other kept
  code references `ExporterHistory` or `ExportIssuesEndpoint`.

### 6.4 AI wiring inside kept editor/modal components

Remove only the AI import + usage; keep the component:

| Kept file | Remove |
|---|---|
| `apps/web/components/pages/editor/editor-body.tsx` | `import { EditorAIMenu } from "./ai/menu"` (line 47), the `getAIMenu` callback (lines 187-189), and the `menu: getAIMenu` wiring (line 304). `TAIMenuProps` import (line 16) is from `@plane/editor` types — keep if the editor still types `menu`. |
| `apps/web/components/issues/issue-modal/components/description-editor.tsx` | `GptAssistantPopover` import (line 22), `gptAssistantModal` prop, and the popover render (line 268). |
| `apps/web/components/issues/issue-modal/form.tsx` | `gptAssistantModal` state (line 106), its setter (line 248), and the prop passed at line 402. |
| `apps/web/components/inbox/modals/create-modal/issue-description.tsx` | The `// TODO: have to implement GPT Assistance` comment (line 40) — cosmetic. |

### 6.5 OAuth wiring inside kept auth components

Remove only the OAuth hook usage; keep the auth forms:

| Kept file | Remove |
|---|---|
| `apps/web/components/account/auth-forms/auth-root.tsx` | `import { useOAuthConfig } from "@/hooks/oauth"` (line 24) and `const { isOAuthEnabled, oAuthOptions } = useOAuthConfig(...)` (line 56) plus the OAuth button rendering. |
| `apps/space/components/account/auth-forms/auth-root.tsx` | Same (lines 18, 88). |
| `apps/web/helpers/authentication.helper.tsx` | The Google/GitHub/GitLab error codes (lines 76-81) and their message map entries (lines 266-278). |
| `apps/space/helpers/authentication.helper.tsx` | Equivalent entries. |

### 6.6 Do NOT remove (adjacent, kept)

- **`issue_activity`** (`bgtasks/issue_activities_task.py`) — a **separate** task from
  `webhook_activity`, used by 21 import sites for issue activity/notifications. It is **kept**.
  The `_no_activity` fixtures in several contract tests patch `issue_activity`, not webhooks —
  do not confuse the two.
- **`utils/url_security.py`** (`pinned_fetch`, `pinned_fetch_following_redirects`) — kept;
  still used by `work_item_link_task.py` and `authentication/adapter/base.py`.
- **`UnsplashEndpoint`** — kept pending §7-A.
- **Analytics / user-activity exports** — kept pending §7-E.

---

## 7. Cross-check: ambiguous items (flagged, not decided)

- **§7-A — `UnsplashEndpoint` shares a file with the AI endpoints.**
  `apps/api/plane/app/views/external/base.py` contains both the GPT integration endpoints
  (excluded) and `UnsplashEndpoint` (image search — a third-party integration but **not** on
  the exclusion list), and `app/urls/external.py` registers `unsplash/` alongside the two
  `ai-assistant` routes. Unsplash is not "AI", not GitHub/Slack, not OAuth, not import/export,
  not webhooks. **Decision needed:** keep Unsplash (and split it into its own module), or
  treat "external integrations" as including it and delete it too. Recommendation: keep and
  move to a dedicated `unsplash` module.

- **§7-B — `SocialLoginConnection` is already orphaned.**
  `db/models/social_connection.py` defines an OAuth/Jira social-account model that **no view,
  serializer, or route references** (only `db/models/__init__.py` imports it). It is OAuth-adjacent
  but also mentions `Jira`. **Decision needed:** delete with OAuth, or retain as a data model.

- **§7-C — `jira.service.ts` sits in the integrations directory.**
  `apps/web/services/integrations/jira.service.ts` is deleted along with the GitHub/Slack
  integration services because it lives in the same directory. Jira is a distinct integration
  that is not named in the exclusion brief. **Decision needed:** confirm Jira is also dropped.

- **§7-D — `authentication/adapter/oauth.py`.**
  OAuth-specific, but the adapter package also contains `base.py` (which references GitHub and
  is used by kept credential providers) and `credential.py`. **Decision needed:** confirm
  `oauth.py` has no kept importer before deleting; keep `base.py`/`credential.py`.

- **§7-E — two other "export" endpoints.**
  `ExportWorkspaceUserActivityEndpoint` (`app/views/workspace/base.py:339`, route
  `workspaces/<slug>/user-activity/<uuid:user_id>/export/`) and `ExportAnalyticsEndpoint`
  (`app/urls/analytic.py:46`, `export-analytics`) are exports but are **not** based on
  `ExporterHistory`. The brief scopes import/export to "`Importer` model + importer views/urls,
  `ExporterHistory` model + exporter views/urls", so these two are **out of scope** by
  default — but they are named "export". **Decision needed.**

- **§7-F — import/export frontend UI was not enumerated.**
  The brief gave exact paths for AI/integration/OAuth/webhook frontends but only "Import /
  export" generically for the frontend. Grep `apps/web` for `exporter`, `export-issues`,
  `import-export`, `ImporterService`/`ExporterService` before deleting; the settings pages are
  the likely location but were not verified here.

- **§7-G — `webhook_activity` vs `model_activity` are one task graph.**
  `model_activity` internally calls `webhook_activity` (`bgtasks/webhook_task.py:482,507`).
  Both live in the deleted file, so removing the file removes both — but the 24 kept call
  sites reference `model_activity` far more than `webhook_activity`. Ensure the cleanup
  covers **both** names (this is why the count is 24, not the ~2 direct `webhook_activity`
  calls).

- **§7-H — `settings/common.py` webhook SSRF settings — RESOLVED (safe to remove).**
  `WEBHOOK_ALLOWED_IPS` / `WEBHOOK_ALLOWED_HOSTS` / `WEBHOOK_DISALLOWED_DOMAINS` /
  `WEBHOOK_LOG_RETENTION_DAYS` are consumed **only** by files that are themselves deleted:
  `app/serializers/webhook.py` (lines 32-49), `bgtasks/webhook_task.py` (lines 320-321), and
  `bgtasks/cleanup_task.py` (line 151). `bgtasks/work_item_link_task.py` does **not** read any
  `WEBHOOK_*` setting (verified), and `utils/ip_address.py` only references them in docstrings
  and receives them as parameters — it does not read `settings.WEBHOOK_*`. Removing the four
  settings is therefore safe.
