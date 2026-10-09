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
| **Agent C**（工作区/用户域） | `internal/router/workspace.go`、`users.go`<br>`internal/handlers/workspaces/**`、`users/**`<br>**仅 Agent C** 可新增 `pb_migrations/1759000013_*.go` | 同上 |
| **编排者**（我） | `router.go`、`patterns_test.go`、`pb_migrations/` 的既有文件、`spec/**`、`docs/**` | — |

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

> 这是本次编排最大的风险削减点：**没有 agent 需要动 `pb_migrations/` 的既有文件**，
> 也就没有"迁移编号撞车"的问题。

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
