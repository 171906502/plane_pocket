# 08 · 未覆盖接口的实现计划与任务编排

> 目标：把 73 个未覆盖接口里**该做的 59 个**做完（类别 A 的 15 个明确不做，见 §7）。
> 本文是**编排契约**：分批、文件所有权、验收标准、agent 工作规则。
>
> 基线核对时间：2026-10-09。`python tests/browser/audit-endpoints.py`
> → 258 注册 / 150 调用形状 / 73 未覆盖。

---

## 1. 一个必须先说清楚的事实

**`audit-endpoints.py` 有假阴性，73 是下限不是上限。**

`user-issues/{user_id}/` 确实缺失，但**不在**那 73 个里 —— 因为它的 URL 是模板字符串
动态拼的，静态扫描只能看见字面量。

> **规则**：补接口前，必须去 `plane-preview/apps/web/services/` 里搜一遍**真实调用点**，
> 不能只信 audit 的数字。**注意 `services/` 有两个位置**：
> `apps/web/services/`（页面级）与 `apps/web/core/services/`（core 级）——
> `user-issues` 在前者，我第一次只搜后者扑空了。

---

## 2. 文件所有权矩阵（**这是并行不冲突的关键**）

`internal/router/router.go` 的 `registrars` 表是**共享文件**，任何需要"新增一个域"的
改动都由**编排者预先完成**，agent 不得修改它。

| 角色 | 独占文件 | 禁止触碰 |
|---|---|---|
| **Agent A**（工作项域） | `internal/router/issue.go`<br>`internal/handlers/issues/**` | 其它 `router/*.go`、`router.go`、`pb_migrations/**` |
| **Agent B**（页面/项目/视图域） | `internal/router/page.go`、`project.go`、`view.go`<br>`internal/handlers/page/**`、`projects/**`、`view/**` | 同上 |
| **Agent C**（工作区/用户域） | `internal/router/workspace.go`、`users.go`<br>`internal/handlers/workspaces/**`、`users/**` | 同上 |
| **编排者**（我） | `router.go`、`patterns_test.go`、`pb_migrations/**`（**全部**，含新增）、`spec/**`、`docs/**` | — |

> **迁移创建权收归编排者（2026-10-10 修订）**：原计划把新增迁移的权利给 Agent C，
> 现已**收回**。理由见 §5 的更正说明 —— Go 把 `pb_migrations` 当一个编译单元，
> 并发写迁移会互相打断构建。**任何 agent 都不得新增或修改 `pb_migrations/`**；
> 缺表就写进交付报告，由编排者补。

**为什么按域切而不是按类别切**：类别 C 的 25 个散落在 issue / page / project / workspace
四个域里。若按类别分给 3 个 agent，它们会同时改同一个 `router/*.go` —— 必然冲突。
按域切之后，**三个 agent 的文件集合两两不相交**。

### 2.1 路由归属的边界规则（避免两个 agent 抢同一条路径）

`/projects/{project_id}/…` 下的路由由**谁注册**，取决于它属于哪个域：

| 路径 | 归属 | 理由 |
|---|---|---|
| `/projects/{pid}/issues/…`、`bulk-*`、`deleted-issues/`、`archived-issues/`、`issue-dates/`、`issue-display-properties/`、`epics-user-properties/` | **Agent A**（`issue.go`） | 工作项语义；`issue.go` 已经建好了 `proj` 组 |
| `/projects/{pid}/pages/…`、`favorite-pages/`、`archived-pages/`、`states/{}/mark-default/`、`project-identifiers`、`views/{}/issues/`、`archive/` | **Agent B**（`page.go`/`project.go`/`view.go`） | 页面/项目/视图语义 |

---

## 3. 分批（3 波 × 3 agent）

**总计 59 个端点。** 每波三个 agent 并行，编排者验收通过后才开下一波 ——
避免上一波的错误被下一波继承。

### Wave 1 — P0：可见入口（21 个）

| Agent | 端点 | 个数 |
|---|---|---|
| **A** | `user-issues/{user_id}/`（决定 1）· `bulk-archive-issues/` · `bulk-delete-issues/` · `bulk-operation-issues/` · `bulk-subscribe-issues/` · `deleted-issues/` · `archived-issues/` · `issues/{pk}/meta/` | 8 |
| **B** | `favorite-pages/`（list）· `archived-pages/` · `pages/{pk}/move/` · `pages/{pk}/versions/{vpk}/restore/` · `states/{spk}/mark-default/` · `projects/{pid}/archive/` · `project-identifiers` · `views/{vpk}/issues/` | 8 |
| **C** | `my-issues/` · `labels/`（CRUD）· `workspaces/{slug}/issues/` · `issues-detail/` · `quick-links/`（CRUD + 新迁移） | 5 |

### Wave 2 — 工作项详情配套 + 整模块（约 20 个）

| Agent | 端点 |
|---|---|
| **A** | `issues/{pk}/history/` · `issues/{pk}/description-versions/` · `issues/{pk}/comments/` · `issues/{pk}/subscribe/` · `issues/{pk}/reactions/` · `issues/{pk}/issue-relation/` · `issues/{pk}/remove-relation/` · `comments/{ck}/reactions/` · `issue-dates/` · `issue-display-properties/` · `epics-user-properties/` |
| **B** | `cycles/{ck}/cycle-progress/` · `active-cycles/` · `estimates/` · `estimates/{ek}/estimate-points/` · `intake-work-items/{ik}/description-versions/` · `dashboard/` |
| **C** | `users/me/accounts/` · `users/me/email/` · `users/me/email/generate-code/` · `users/me/instance-admin/` · `users/last-visited-workspace/` · `invitations/` · `invitations/{ik}/join/` · `members/leave/` · `projects/{pid}/members/leave/` · `configs/` · `release-notes/` |

### Wave 3 — 剩余（约 18 个）

| Agent | 端点 |
|---|---|
| **A** | `projects/{pid}/{serviceType}/list/` 的 `epics` 变体 · `{serviceType}-detail/` 的 `epics` 变体 · `issues/{pk}/archive/` 的 epics 变体 |
| **B** | `pages/{pk}/archive/` 补充 · 视图相关的剩余项 |
| **C** | `assets/v2/…/attachments/` · `assets/v2/workspaces/{}/bulk/` · `file-assets/{}/{}/restore/` |

> **`{serviceType}` 是什么**：前端 `issue.service.ts` 里 `this.serviceType` ∈
> `{ "issues", "epics" }`，同一个方法为两个服务类型拼出不同路径。
> 所以 `/projects/{pid}/{serviceType}/` 展开后是 **`/issues/` 和 `/epics/` 两条**。
> `issues/` 已实现；**`epics/` 变体是 Wave 3 的内容**。

---

## 4. 已解析的 URL 形状（agent 直接用，**不要猜**）

以下形状是从前端 `services/` 里实测提取的，`{}` 是占位符：

| 端点 | 真实形状 | 注意 |
|---|---|---|
| user-issues | `GET /api/workspaces/{slug}/user-issues/{user_id}/` | `apps/web/services/user.service.ts:217` |
| deleted-issues | `GET /projects/{pid}/deleted-issues/` | `issue.service.ts:90` |
| archived-issues | `GET /projects/{pid}/archived-issues/` | `issue_archive.service.ts` |
| issue meta | `GET /projects/{pid}/{serviceType}/{issueId}/meta/` | |
| bulk-operation | `POST /projects/{pid}/bulk-operation-issues/` | `issue.service.ts:340` |
| bulk-delete | `DELETE /projects/{pid}/bulk-delete-issues/` | `issue.service.ts:354`，**body 带 `issue_ids`** |
| issue-dates | `GET /projects/{pid}/issue-dates/` | |
| issue-display-properties | `GET/PATCH /projects/{pid}/issue-display-properties/` | |
| epics-user-properties | `GET /projects/{pid}/epics-user-properties/` | |
| issues-detail | `GET /api/workspaces/{slug}/issues-detail/` | **工作区级**，不是项目级 |
| workspaces issues | `GET /api/workspaces/{slug}/issues/` | **工作区级** |
| quick-links | `GET/POST /api/workspaces/{slug}/quick-links/`<br>`PATCH/DELETE /api/workspaces/{slug}/quick-links/{linkId}/` | |
| **project-identifiers** | `/api/workspaces/{slug}/project-identifiers` | ⚠️ **没有尾部斜杠！** |
| views issues | `GET /projects/{pid}/views/{viewId}/issues/` | |
| estimates | `GET/POST /api/workspaces/{slug}/estimates/`<br>`GET/PATCH/DELETE /projects/{pid}/estimates/{estimateId}/`<br>`…/estimates/{estimateId}/estimate-points/{epId}/` | |
| dashboard | `/api/workspaces/{slug}/dashboard/{dashboardId}/` | |
| cycle-progress | `/projects/{pid}/cycles/{cycleId}/cycle-progress/` | |

---

## 5. 数据库：**几乎所有 collection 都已存在**

已在 `pb_migrations/` 里定义（79 个），**agent 不需要新建**：

```
issue_relations · issue_comments · issue_reactions · comment_reactions
issue_activities · issue_versions · issue_description_versions · issue_subscribers
issue_labels · labels · estimates · estimate_points · project_identifiers
file_assets · issue_attachments · page_versions · page_logs · pages
notifications · email_notification_logs · user_favorites · user_recent_visits
workspace_member_invites · project_member_invites · workspace_members · accounts
```

**唯一缺失**：`quick_links` → 只允许 **Agent C** 新增一个迁移文件
`pb_migrations/1759000013_init_quick_links.go`。

> **⚠️ 更正（2026-10-10，Wave 2 准备阶段实测）**：上面这段是**错的**。
> 权威清单来自 `grep -rho 'NewBaseCollection("...")' pb_migrations/`（共 **79** 个），
> 对照后缺的不止 `quick_links`：
> - **`quick_links`** —— Wave 1 已确认可复用 `workspace_user_links`，**不需要迁移**。
> - **`dashboards` / `dashboard_widgets`** —— **真的不存在**，Wave 2 需要。
> - **`intake_issue_versions`** —— 不存在，但有个**通用**的 `description_versions`，
>   `intake-work-items/{ik}/description-versions/` 大概率复用它（agent 必须读 Django 确认）。
> - `release-notes` 对应的表是 **`changelogs`**（已存在）。
>
> **新政策**：迁移创建**一律由编排者做**，agent 一个都不许加。原因是 Go 把整个
> `pb_migrations` 包当**一个编译单元**，两个 agent 同时写迁移会互相打断构建。
> `1759000013_init_dashboards.go` 已由编排者写好并实测通过。

> 这是本次编排最大的风险削减点：**没有 agent 需要动 `pb_migrations/`**，
> 也就没有"迁移编号撞车"和"共享包编译被打断"的问题。

---

## 6. 验收标准（分级，编排者执行）

**agent 自己的最低要求**（不满足即视为未完成）：
1. `go build -o .fe.exe .` **exit 0**
2. `go test ./internal/router/ -run TestRoutePatternsAreAnchored` **通过**
   （所有新路由必须 `{$}` 锚定 —— 未锚定的子树模式会返回 `200 + 错对象`，见 06 A-1）
3. 新路由的 handler 已接到真实数据（不是 `return nil` 占位）

**编排者的验收**（我逐项做，不通过就打回）：
| 级别 | 检查 | 方法 |
|---|---|---|
| L1 | 编译 + 路由护栏 | `go build` / `go test` |
| L2 | **接口级**：注册了、返回 200/201、**响应形状**对 | 起栈 + `curl`，逐条断言 |
| L3 | **内容级**：返回的 key 存在、计数非零 | 见下方"断言具体内容" |
| L4 | **浏览器级**：真实页面渲染正确 | 起栈 + 截图 |
| L5 | 无回归 | `audit-endpoints.py` 未覆盖数必须**下降**，且不出现新的 404 |

> **L3 是最容易被跳过、也最重要的一级。** 前端会把失败渲染成"看起来正常"的状态
> （`home-preferences` 404 → 首页显示"It's Quiet Without Widgets, Turn Them On"，
> 截图毫无异常但挂件全空）。**"页面没崩"不是验收标准。**
> 每条接口必须断言**具体内容**：key 存在 / 数组非空 / 计数 > 0。

> **L5 的注意点**：修掉一个 bug 后"变差"往往是更深一层的 bug 暴露了。
> 补 `{$}` 锚点后曾涌现一批 404，那是**漏写清单**，不是回归。

---

## 7. 明确不做（类别 A，15 个）

`rephrase-grammar/`、`ai-assistant/`、`workspace-integrations/*`、`webhooks/*`、
`importers/*`、`export-issues/`、`user-activity/{}/export/`、`integrations/`。

**保持 404 才是正确的。** 不要为了消除 audit 的数字去实现它们。
要加回来先改 `spec/exclusions.md`。

---

## 8. 决定 3 的结论：反向代理能否解决 `SameSite` 问题？

**能，但有且只有一个前提：让前端与 API 落在同一个"站点"（同一注册域 eTLD+1）下。**

`SameSite` 是**浏览器**根据它看到的 URL 判断的，**与网络拓扑无关** —— 所以：

| 拓扑 | 反代能否解决 | 代码改动 |
|---|---|---|
| ✅ **同域路径分流**：`example.com/` → web，`example.com/api/` → 后端 | **能**。浏览器视为同站 → `Lax` 直接可用 | **零改动**（只改前端 `VITE_API_BASE_URL` 为同域路径） |
| ❌ 跨注册域：`app.foo.com` + `api.bar.com` | **不能**。反代只是转发，浏览器看到的仍是两个站 | 必须改 `SameSite=None; Secure` |

**选同域路径分流的连带好处**：CORS 也不再需要（同源），`--origins` 那套配置可以退居二线。

**前置条件**：
- 必须 **HTTPS**（`SameSite=Lax` 的 cookie 若带 `Secure` 必须走 TLS）
- 反代**必须转发 `X-Forwarded-Proto`**，否则 `isSecureRequest()` 判断错
- `VITE_*` 是**构建期**烧进产物的，改完必须重新 `pnpm build`

**结论**：N-2 在"同域路径分流"下**不需要改代码**。
可选加固（低成本保险）：把 `SameSite` 做成环境变量可配（`PLANE_COOKIE_SAMESITE`），
默认 `Lax`，跨站部署时设成 `None`。约 30 行，列入 Wave 3 可选。

---

## 9. Agent 工作规则（每个 agent 必须遵守）

1. **只写自己名下的文件**（§2 矩阵）。需要改共享文件时，**停下来报告**，不要自己改。
2. **契约先行**：先读 Django 源码（`plane-preview/apps/api/plane/app/views/…`）与
   serializer，确认**响应形状**，再写 Go。**不要凭记忆猜形状** ——
   相邻接口的形状经常不对称（`sidebar-preferences` GET 是 map / PATCH 收 list）。
3. **所有路由 `{$}` 锚定**。路径不以 `/` 结尾的除外。
4. **必须实测**：`go build` exit 0 之外，要真的起服务 `curl` 确认行为。
   **"编译通过"不等于"行为正确"。**
5. **不要用 `curl -o /dev/null`**（Git Bash 下静默退出 23）。用 `-o NUL`。
6. **不要删除文件**，备份用 `mv X X.bak-$(date +%s)`。
7. **绝对不要用 `.fe-run` 起服务做冒烟测试。** 那是**共享数据库**。
   agent 若需要跑起来验证，必须先把它**复制**一份到自己名下：
   `cp -r .fe-run .fe-run.agentX`，然后 `--dir=.fe-run.agentX`。
   端口也必须错开（A=8011 / B=8012 / C=8013），二进制也必须错开
   （`.fe.agentX.exe`，绝不能用 `.fe.exe` —— 三个 agent 会同时写同一个文件）。
8. 完成后报告：改了哪些文件、加了哪些路由、**每条接口的实测输出**、未决问题。

> **⚠️ 已发生的真实事故（Wave 1）**：三个 agent 并行时，其中一个按"用 `--dir=.fe-run`
> 起 8011"的指示做了冒烟测试，**把共享库里的既有工作流测试数据覆盖成了它自己的
> 测试账号**（`admin@hrms.local` → `agentb@example.com`，工作区 `hrms` → `agentbws`）。
> 数据本身是可再生的（`run-all.sh --fresh` 重建），但这暴露了一个真实的编排缺陷：
> **给并行 agent 的指示里出现了"可以写共享资源"的口子，就一定会被用上。**
> 后续波的指示必须显式给出**独立的目录与端口**，而不是"你随便挑一个"。

---

## 10. Wave 2 简报（2026-10-10 编排者实测后写定）

**基线**：`audit-endpoints.py` → 281 注册 / 150 调用形状 / **UNSERVED 53**。
（53 是**下限**，`user-issues` 那种模板字符串拼的 URL 扫不出来。）

### 10.1 已解析的 URL 形状（**直接用，不要猜**）

下面每一条都是我从 `plane-preview/apps/web/services/` 里**逐行读出来**的，
不是从 audit 的输出猜的。`{slug}` 等工作区/项目参数照旧。

**Agent A（issue 域）**

| 端点 | 真实形状 | 出处 |
|---|---|---|
| history | `GET /api/workspaces/{slug}/projects/{pid}/{serviceType}/{issueId}/history/` | `issue.service.ts:140`、`issue_activity.service.ts:32`、`issue_comment.service.ts:36` —— **三处都是 GET**，未见 POST，写前先去 Django 确认 |
| description-versions | `GET …/{serviceType}/{workItemId}/description-versions/`<br>`GET …/description-versions/{versionId}/` | `work_item_version.service.ts:29,44` |
| comments | `GET, POST …/{serviceType}/{issueId}/comments/`<br>`PATCH, DELETE …/comments/{commentId}/` | `issue_comment.service.ts:55,72,88` |
| subscribe | `GET, POST, DELETE …/{serviceType}/{issueId}/subscribe/` | `issue.service.ts:385,394,403` |
| reactions | `GET, POST …/{serviceType}/{issueId}/reactions/`<br>`DELETE …/reactions/{reaction}/` | `issue_reaction.service.ts:29,39,48` |
| issue-relation | `GET, POST …/issues/{issueId}/issue-relation/`<br>`DELETE …/issue-relation/{relationId}/` | `issue_relation.service.ts:18,31`、`issue.service.ts:185,196` |
| remove-relation | `POST …/issues/{issueId}/remove-relation/` | `issue_relation.service.ts:44` |
| comment reactions | `GET, POST …/comments/{commentId}/reactions/`<br>`DELETE …/reactions/{reaction}/` | `issue_reaction.service.ts:62,74,88` |
| issue-dates | `GET …/issue-dates/` | `issue.service.ts` |
| issue-display-properties | `GET, PATCH …/issue-display-properties/` | `issue.service.ts` |
| epics-user-properties | `GET …/epics-user-properties/` | `issue_filter.service.ts` |

> ⚠️ **`{serviceType}` 展开**：`this.serviceType ∈ {"issues","epics"}`。
> 本波**只做 `issues` 变体**；`epics` 变体是 Wave 3。但注册时**两条都要想清楚**，
> 因为 `{serviceType}` 是路径参数还是两条字面路由，会影响 `{$}` 锚定写法。
> 建议**显式写两条**（`/issues/...` 与 `/epics/...`），Wave 3 只需把 epics 的
> 路由接到同一批 handler —— 比用 `{serviceType}` 通配更可控，也不会误吞其它子路径。
> **注意 `issue_relation.service.ts` 用的是字面量 `issues`**，不是 `serviceType`。

> ⚠️ **`issue-dates` / `issue-display-properties` / `epics-user-properties` 三条
> 在 Wave 1 的 commit message 里被**误报为已完成**（已更正）。它们**没做**，本波要做。

**Agent B（cycle / estimate / intake 域）**

| 端点 | 真实形状 | 出处 |
|---|---|---|
| cycle-progress | `GET /api/workspaces/{slug}/projects/{pid}/cycles/{cycleId}/cycle-progress/` | `cycle.service.ts:57` |
| active-cycles | `GET /api/workspaces/{slug}/active-cycles/` | `cycle.service.ts:69` |
| estimates (list/create) | `GET /api/workspaces/{slug}/estimates/`（**工作区级**）<br>`GET, POST /api/workspaces/{slug}/projects/{pid}/estimates/` | `estimate.service.ts:23,32,60` |
| estimate detail | `GET, PATCH, DELETE …/projects/{pid}/estimates/{estimateId}/` | `estimate.service.ts:46,69` |
| estimate-points | `GET, POST …/estimates/{estimateId}/estimate-points/`<br>`GET, PATCH, DELETE …/estimate-points/{pointId}/` | `estimate.service.ts:83,101` |
| intake description-versions | `GET …/intake-work-items/{ik}/description-versions/`<br>`GET …/description-versions/{versionId}/` | `intake-work_item_version.service.ts:25,40` |
| dashboard | 见 §10.3 | `dashboard.service.ts:19,35,45,53` |

**Agent C（user / workspace 域）**

| 端点 | 真实形状 | 出处 |
|---|---|---|
| accounts | `GET /api/users/me/accounts/` | `user.service.ts:80` |
| instance-admin | `GET /api/users/me/instance-admin/` | `user.service.ts:88` |
| email | `PATCH /api/users/me/email/` | `user.service.ts:286` |
| email code | `POST /api/users/me/email/generate-code/` | `user.service.ts:278` |
| last-visited-workspace | `GET /api/users/last-visited-workspace/` | `workspace.service.ts:105` |
| my workspace invitations | `GET, POST /api/users/me/workspaces/invitations/` | `workspace.service.ts:97,113` |
| my project invitations | `POST /api/users/me/workspaces/{slug}/projects/invitations/` | `user.service.ts:246` |
| workspace invitations | `GET, POST /api/workspaces/{slug}/invitations/` | `workspace.service.ts:79,165` |
| invitation join | `GET, POST /api/workspaces/{slug}/invitations/{ik}/join/` | `workspace.service.ts:87,173` |
| invitation detail | `PATCH, DELETE /api/workspaces/{slug}/invitations/{ik}/` | `workspace.service.ts:185,193` |
| workspace member leave | `POST /api/workspaces/{slug}/members/leave/` | `user.service.ts:238` |
| project member leave | `POST /api/workspaces/{slug}/projects/{pid}/members/leave/` | `user.service.ts:254` |
| configs | `GET /api/configs/` | `app_config.service.ts:21` |
| release-notes | `GET /api/release-notes/` | `workspace.service.ts:225` |

> ⚠️ **`members/leave/` 是工作区级**（`/api/workspaces/{slug}/members/leave/`），
> **不是** `/api/members/leave/`。同一文件里项目级那条是
> `/api/workspaces/{slug}/projects/{pid}/members/leave/`。两条都要。
> 另外 `user.service.ts:238` 与 `:254` 都在 `user.service.ts` 里，但路径分属
> workspace.go 与 workspace.go（都归你），`users.go` 只管 `/api/users/...`。

> ⚠️ **`invitations/{ik}/join/` 的 POST 带 `{ headers: {} }`** —— 即**不带认证头**。
> 这是"被邀请人尚未登录也能接受邀请"的流程。请读 Django 的
> `WorkspaceInvitationsViewSet` 确认它对未登录请求的返回，**不要**直接套
> `ResolveWorkspace` 的成员校验（否则未登录必然 404，邀请链接就是死的）。

> ⚠️ **`configs` 与 `release-notes` 在 Django 里没有 view、没有 url**
> （实测：`grep -rn 'configs|release-notes' apps/api/plane/app/urls/*.py` 无命中）。
> 形状只能从**前端 TS 类型 + 它镜像的 Django serializer** 推。请在报告里
> **写清推导过程**，并明确标注 confidence。
> `release-notes` 的后端表大概率是已存在的 **`changelogs`**。

### 10.2 本波不做（留给 Wave 3）

`{serviceType}` 的 **`epics` 变体**、`pages/{pk}/archive/` 补充、
`assets/v2/…/attachments/`、`file-assets/{}/{}/restore/`。

### 10.3 dashboard 域：本波**做**，且契约已由编排者补齐

`dashboard` 是**可见入口**（`apps/web/components/home/root.tsx` 渲染
`DashboardWidgets`），且 `dashboard.store.ts` 的 `fetchHomeDashboardWidgets`
**失败直接 throw、没有静态兜底** → 404 会让首页挂件区**静默空白**
（与 `home-preferences` 同一类静默失败）。

编排者已补齐三件事，agent **不需要**再碰：

1. **表**：`pb_migrations/1759000013_init_dashboards.go` 已创建
   `dashboards` + `dashboard_widgets`，并实测 `Applied` 成功。
2. **spec**：`spec/api-spec.yaml` 已补 4 条（`dashboard.home.list` /
   `dashboard.home.widget-stats` / `dashboard.widget-stats` /
   `dashboard.widget.update`），`confidence: low`、`view_class: null`。
3. **路由骨架**：`internal/router/dashboard.go` 的 `RegisterDashboard` 已注册进
   `registrars` 表（占位返回 nil），**Agent B 只需填函数体**。

**响应形状**（`packages/types/src/dashboard.ts`）：

```
GET  /api/workspaces/{slug}/dashboard/?dashboard_type=home
  -> { dashboard: TDeprecatedDashboard, widgets: TWidget[] }
GET  /api/workspaces/{slug}/dashboard/{dashboard_id}/?widget_key=…
GET  /api/dashboard/{dashboard_id}/?widget_key=…          <- 注意：不挂 workspace
  -> TWidgetStatsResponse（联合类型，按 widget_key 变形，见 dashboard.ts:159）
PATCH /api/dashboard/{dashboard_id}/widgets/{widget_id}/  body: {is_visible?, filters?}
  -> 单个 TWidget
```

`widget_key` 取值：`assigned_issues` · `created_issues` · `issues_by_state_groups` ·
`issues_by_priority` · `recent_activity` · `recent_projects` · `recent_collaborators`
（外加一个 `overview_stats` 形状 `TOverviewStatsWidgetResponse`）。

**注意**：`/api/dashboard/{id}/` 与 `/api/dashboard/{id}/widgets/{wid}/` **不在
workspace 前缀下**，但**仍然必须做认证 + 归属校验**（只能看自己的 dashboard），
否则就是越权读。这两条请单独放在 `RegisterDashboard` 里，不要塞进 `workspaceGroup`。

### 10.4 Agent 隔离（**硬性**，Wave 1 事故的直接产物）

| Agent | 数据库目录 | 端口 | 二进制 | 独占文件 |
|---|---|---|---|---|
| A | `.fe-run.agentA` | 8011 | `.fe.agentA.exe` | `internal/router/issue.go`、`internal/handlers/issues/**` |
| B | `.fe-run.agentB` | 8012 | `.fe.agentB.exe` | `internal/router/{page,project,view,cycle,intake,dashboard,estimate}.go`、`internal/handlers/{page,projects,view,cycle,intake,dashboard,estimate}/**` |
| C | `.fe-run.agentC` | 8013 | `.fe.agentC.exe` | `internal/router/{workspace,users,config}.go`、`internal/handlers/{workspaces,users,config}/**` |

三个目录**已由编排者预先复制好**（含 `admin@hrms.local` / `hrms` / `HRMS` 测试数据）。

> **三个 agent 都不得**：改 `router.go`、`patterns_test.go`、`spec/**`、`docs/**`、
> `pb_migrations/**`，也不得用 `.fe-run`（共享库）或 `.fe.exe`（共享二进制）。

### 10.5 一条实测提醒：本波所有目标表都是空的

实测 `data.db`：`cycles` / `issue_comments` / `issue_reactions` / `issue_activities` /
`estimates` / `estimate_points` / `workspace_member_invites` / `changelogs` …
**全部 0 行**。

所以 **L3（内容级）验收必须自己造数据**：先用 API 或 fixture 创建一条评论/一个 estimate，
再断言返回的数组非空、`count > 0`。
**只断言"返回 200"是不够的** —— 空数组也是 200，而这正是本项目反复踩的坑。

---

## 11. Wave 3 结论（2026-10-10 编排者完成）

Wave 3 的实际工作量比原计划小得多 —— **原计划的主体（`epics` 变体）根本不是缺口**。
结论先行，逐项证据如下。

### 11.1 ⭐ `epics` 变体：不是缺口，是幽灵

原计划把 "`{serviceType}` 的 `epics` 变体" 列为 Wave 3 主体。**实测推翻：**

- Django 的 URL 配置里**没有任何 `epics` 路径**（`grep -rn "epics" apps/api/plane/ --include=*.py`
  全库只有 2 处无关命中），也没有 `EpicViewSet`。
- 我们的 spec（由 Django 生成）**没有一条 epics 端点** —— 与上游一致。
- 前端**没有 epics 页面**：`app/(all)/[workspaceSlug]/(projects)/projects/(detail)/[projectId]/`
  下只有 `archives / cycles / intake / issues / modules / pages / views`。
- `is_epic` 在整个 web 前端**只被读、从不被写**（33 处引用全部是
  `issue?.is_epic ? EPICS : ISSUES` 这类分支）。我们决定**全新空库**，
  所以 `is_epic` 恒为 false，`/epics/...` **不可达**。

**结论：实现 `/epics/` 等于发明一个上游不存在的契约。上游 404，我们 404，这是兼容。**
那些"未服务"的 `epics-detail/`、`epics/list/`、`epics/{}/comments/` 条目从待办清单中**划掉**。

### 11.2 真正的 Wave 3（已全部完成并 L3 实测）

| 项 | 结论 |
|---|---|
| **`POST …/issues/{issue_id}/attachments/`** | **handler 早已写好，但没有路由可到** —— 死代码。`ProjectSubresource` 注册为 `/{entity_id}/{action}/{$}`（projects 后 **2** 段），而附件路径是 **3** 段（`issues/{issue_id}/attachments/`），永远匹配不上。补 `att.POST` 后 9 项断言全过（含完整四步流程：元数据 → 上传字节 → PATCH → 列表出现）。 |
| **`POST …/user-activity/{user_id}/export/`** | Django 有 view（`ExportWorkspaceUserActivityEndpoint`）、前端有入口（profile 页 "Download" 按钮），但从未实现。已补：CSV 下载，7 项断言全过（表头逐列一致、按日过滤、剔除 4 类字段、**CSV 注入转义** `'=SUM(...)`、`<identifier> - <sequence_id>`）。 |
| **legacy `file-assets/{ws}/{key}/` + `restore/`** | 一直被注释为"与 members 路由冲突故禁用"。实测**真正冲突的是 `workspace-members/me/`**（不是 members），且**冲突是真实的**：注册即 `BuildMux` panic，恢复后全站 502。已用 **catch-all 分发**解决（见下）。 |
| `assets/v2/workspaces/{slug}/{entity_id}/bulk/` | **不做**。Django 无此 URL（只有 projects 下的 bulk），上游同样 404，属上游缺口；前端调用点在无 projectId 的 else 分支。 |

### 11.3 `file-assets` 的 Go mux 不可表达问题（已解决，值得记录）

`GET/DELETE /api/workspaces/file-assets/{ws}/{key}/` 与
`GET /api/workspaces/{slug}/workspace-members/me/{$}` **都不可注册**：两者都是 5 段，
一个把 `file-assets` 钉在第 3 位、一个把 `workspace-members`/`me` 钉在第 4/5 位，
`/api/workspaces/file-assets/workspace-members/me/` 同时匹配两者且**谁都不更具体**，
Go 的 ServeMux 直接 panic（Django 靠 URL 顺序解决，Go 没有等价机制）。

**解法**：PocketBase 的 `BuildMux` 只在**没有** `"/"` 路由时才自动补一个 404 兜底
（`tools/router/router.go:68`）。注册我们自己的 `"/"`，就能在 mux 之前分发这个前缀，
其余所有路径**原样委托给框架同一个 `NotFoundError`** —— mux 的模式集合一个都没变，
只有兜底处理函数体不同。`restore/` 是 6 段、无重叠，正常注册，不走兜底。

**验收**：`GET` 次数返回 200 `{"error":"Asset key does not exist","status":false}`
（未注册时是路由 404）；`DELETE`/`restore` 返回**处理器自己的** 404
（"The required object does not exist."，区别于路由 404 的
"The requested resource wasn't found."—— 这个差异本身就是路由已匹配的证据）；
未匹配路径仍是框架原样 404；`workspace-members/me/` 仍 200。

### 11.4 顺带修掉的第二个真实缺陷

L3 流程测出：**附件元数据响应缺少 `attachment` 键**。
`IssueAttachmentV2Endpoint`（attachment.py:139-145）返回
`{upload_data, asset_id, asset_url, **attachment**}`，而通用 v2 上传
（asset/v2.py）**不含** `attachment`。共享的 `createAsset` 照抄了通用形状，
而前端 `uploadIssueAttachment` 返回 `signedURLResponse.attachment` →
拿到 `undefined`。已按 `createInput.attach` 开关补上。

### 11.5 最终状态

- `audit-endpoints.py`：注册 **281 → 328**，UNSERVED **53 → 26**
  （14 条类别 A 有意剔除 + 12 条 epics 幽灵条目 = 0 条真实缺口）
- 编排方式：本波仅 2 个真实缺口 + 1 个路由层修复，**规模不足以启动 3 个 agent**，
  由编排者直接实现并以与 Wave 2 相同的 L3 标准验收。
