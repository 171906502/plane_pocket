# 07 · 现状与路线图

> 这份文档回答三个问题：**已经做完什么 / 还没做什么 / 下一步该做什么。**
>
> 数字会随迭代变化。**每次批量补完接口后请更新本文件**，
> 并重跑 `python tests/browser/audit-endpoints.py` 获取最新基线。

---

## 1. 当前基线（截至 2026-10-04）

| 指标 | 数值 | 怎么来的 |
|---|---|---|
| 仓库受控文件 | 6735 | `git ls-files \| wc -l` |
| 后端 Go 代码 | 112 个文件 / 28660 行 | `internal/` + `pb_migrations/` |
| **已注册路由** | **258** 个模式 | `audit-endpoints.py` |
| 路由（按方法） | GET 206 · POST 97 · DELETE 70 · PATCH 62 · PUT 10 | 静态计数 |
| Plane collection 数 | 79 个 | `pb_migrations/` |
| 数据库迁移 | 12 个（按 wave 组织） | `pb_migrations/17590000*.go` |
| 前端调用形状 | 150 个 | `audit-endpoints.py` |
| **未覆盖接口** | **73** 个 | `audit-endpoints.py` |
| 已修后端缺陷 | 21 个 | `tests/WORKFLOW_TEST_REPORT.md` |
| 前端代码改动 | **0 行** | 核心承诺 |

### 路由分布（按域）

| 域 | 路由数 | 文件 |
|---|---|---|
| publicapi（`/api/v1/` 那套） | 127 | `router/publicapi.go` |
| admin（实例管理） | 49 | `router/admin.go` |
| view（保存视图） | 36 | `router/view.go` |
| asset（附件/上传） | 31 | `router/asset.go` |
| module | 30 | `router/module.go` |
| intake / cycle | 27 / 27 | `router/intake.go` / `router/cycle.go` |
| project | 19 | `router/project.go` |
| search / page | 18 / 18 | `router/search.go` / `router/page.go` |
| workspace | 17 | `router/workspace.go` |
| users | 16 | `router/users.go` |
| notification | 12 | `router/notification.go` |
| auth | 9 | `router/auth.go` |
| issue | 6 | `router/issue.go` |
| meta（health/timezones/unsplash） | 3 | `router/router.go` |

---

## 2. 已完成

### 2.1 基础设施

| 项 | 状态 |
|---|---|
| Git 仓库（三棵树合一） | ✅ `main` @ `ae4b931` |
| 根 `.gitignore` / `.gitattributes` | ✅ 含 `-text` 保护与 `go:embed` 保护 |
| PocketBase 当库集成 | ✅ `go.mod` `replace` 本地依赖 |
| 79 个 collection 的定义 | ✅ 12 个迁移，分 wave 组织 |
| UUID 双主键策略 | ✅ 已落地（`uuid` 列 + 序列化层映射） |
| 会话认证（cookie + PocketBase JWT） | ✅ `session-id` / `admin-session-id` |
| CSRF（double-submit） | ✅ 含 `PLANE_PB_CSRF=off` 调试开关 |
| CORS（含 credentialed 支持） | ✅ 手动注入 `Allow-Credentials` |
| 权限模型（两级角色轴） | ✅ `middleware/scope.go` |
| 路由锚点护栏（AST 测试） | ✅ `internal/router/patterns_test.go` |
| 游标分页 / 分组分页 | ✅ `serializers/pagination.go` |
| 静态缺口审计工具 | ✅ `tests/browser/audit-endpoints.py` |
| 浏览器验收链路（6 脚本） | ✅ `tests/browser/run*.mjs` |
| 测试报告与证据存档 | ✅ 30 张截图 + 网络日志 |

### 2.2 功能域

| 域 | 状态 | 备注 |
|---|---|---|
| 实例初始化 / admin 控制台 | ✅ | `/god-mode/` |
| 认证（邮箱密码 / 注册 / 登出） | ✅ | @ Magic Link 需发信能力，见未就绪 N-5 |
| 工作区（创建/成员/偏好） | ✅ | |
| 项目（创建/成员/标识） | ✅ | |
| 工作项（增删改查/评论/附件/关系） | ✅ 主流程 | 边角操作见 §3.3 |
| 页面（Pages + 富文本） | ✅ | **依赖 `apps/live` + Redis** |
| 发布到 Space | ✅ | |
| Space 端（公开只读视图） | ✅ | |
| Cycle / Module | ⚠️ 部分 | 基础接口在，边角操作缺口多 |
| View（保存视图/收藏/最近） | ⚠️ 部分 | |
| Intake（收件箱） | ⚠️ 部分 | |
| 搜索 / Analytics | ✅ | |
| 通知 | ✅ | |
| 附件 / 上传 | ✅ | |
| Deploy Board | ✅ | |
| Public API（`/api/v1/` + `X-Api-Key`） | ✅ | |
| AI / GitHub / Slack / OAuth / 导入导出 / 出站 Webhook | ❌ **按方案剔除** | 见 `spec/exclusions.md` |

---

## 3. 未完成：73 个未覆盖接口

> 生成方式：`python plane-pocketbase/tests/browser/audit-endpoints.py`（纯静态，不需要起服务）

按**性质**分三类。**类别决定了优先级**，不要一律当 bug 处理。

### 3.1 类别 A · 方案内明确剔除（约 20 个，**不要做**）

这些是**故意不实现**的，因为项目决定剔除对应功能（见 `spec/exclusions.md`）。

| 路径 | 被剔除的功能 |
|---|---|
| `/api/workspaces/{}/rephrase-grammar/` | AI |
| `/api/workspaces/{}/workspace-integrations/` | GitHub/Slack 集成 |
| `/api/workspaces/{}/workspace-integrations/{}/github-repositories/` | GitHub |
| `/api/workspaces/{}/workspace-integrations/{}/provider/` | 集成 |
| `/api/workspaces/{}/webhooks/` | 出站 Webhook |
| `/api/workspaces/{}/webhooks/{}/regenerate/` | 出站 Webhook |
| `/api/workspaces/{}/projects/importers/jira/` | 导入 |
| `/api/workspaces/{}/projects/{}/export-issues/` | 导出 |
| `/api/workspaces/{}/user-activity/{}/export/` | 导出 |
| （以及同族的其他条目） | |

**处理方式**：保持 404。**不要**为了消除数字而实现它们。
如果将来要加回来，先更新 `spec/exclusions.md`。

### 3.2 类别 B · 整模块未做（约 30 个）

这些是**真实的缺口**，但属于"整个功能没做"，不是"主流程有 bug"。

| 路径族 | 说明 |
|---|---|
| `/api/workspaces/{}/projects/{}/{}/{}/history/` | 工作项历史 |
| `/api/workspaces/{}/projects/{}/{}/{}/description-versions/` | 描述版本历史 |
| `/api/workspaces/{}/projects/{}/{}/{}/issue-relation/` | 工作项关系（阻塞/被阻塞/重复…） |
| `/api/workspaces/{}/projects/{}/{}/{}/reactions/` | 表情回应 |
| `/api/workspaces/{}/projects/{}/{}/{}/subscribe/` | 订阅工作项 |
| `/api/workspaces/{}/projects/{}/{}/{}/comments/` | 评论（部分） |
| `/api/workspaces/{}/quick-links/` | 工作区快捷链接 |
| Cycle 进度 / Estimate points / Intake work items 等 | 见 §3 说明 |

**处理方式**：按模块整体规划，不要零散补。参考 `internal/router/cycle.go`（69 行）作为最小完整模板。

### 3.3 类别 C · ⭐ 已实现模块的边角操作（**最高优先级**）

**这一类最该先补**，因为它们是**列表页上的可见入口** —— 用户点了就会 404。

| 路径 | 用户在哪里看到 |
|---|---|
| `bulk-archive-issues/` | 工作项列表 → 批量操作菜单 |
| `bulk-delete-issues/` | 同上 |
| `bulk-operation-issues/` | 同上 |
| `bulk-subscribe-issues/` | 同上 |
| `issues/{}/meta/` | 工作项详情 |
| `issues/{}/issue-relation/` | 工作项详情 → 关联 |
| `pages/{}/move/` | 页面 → 移动到项目 |
| `states/{}/mark-default/` | 项目设置 → 状态 |
| `deleted-issues/` | 工作项列表 → 已删除筛选 |
| `archived-issues/` | 工作项列表 → 已归档筛选 |
| `favorite-pages/` | 侧边栏 → 收藏页面 |
| `my-issues/` | 个人工作项视图 |

> **核心结论**：**主流程"跑通"不等于"点哪都不 404"。**
> 上面这些是列表页右键菜单和筛选器上的可见入口，属于下一步最该补的。

### 3.4 单个高优先级待办

| 待办 | 说明 |
|---|---|
| **`GET /api/workspaces/{slug}/user-issues/{user_id}/`** | profile 个人页 `/assigned` 标签页的列表数据来源。**页面结构已正常渲染**（Summary / Overview / Workload / Work items by Priority / Work items by state / Recent activity 都在），assigned 标签能切过去但**列表为空**。补上它就能真正显示工作项 |

> **契约要点**（已在会话中推导，实现时请再核对 Django 源码）：
> - 权限：`IsAuthenticated` + `WorkspaceViewerPermission`
> - 过滤：`issue_objects` 语义 —— 排除软删除、已归档、草稿、归档项目下的工作项、以及 Triage 状态组；
>   限定为"该用户是 assignee **或** creator **或** subscriber"，并与**请求者**为活跃成员的项目取交集
> - 参数：`order_by`（默认 `-created_at`）、`sub_issue`（`false` ⇒ `parent__isnull=True`）、`assignees`、`cursor`、`per_page`
> - 无 `group_by` 时返回**扁平游标信封**
> - 参考实现：`plane-preview/apps/api/plane/app/views/workspace/user.py` 的
>   `WorkspaceUserProfileIssuesEndpoint`，过滤逻辑在 `plane-preview/apps/api/plane/utils/issue_filters.py`

---

## 4. 21 个已修缺陷索引

> 完整叙事（症状 / 根因 / 修法 / 证据）见 `tests/WORKFLOW_TEST_REPORT.md`。
> 这里只做索引，方便快速定位。

| # | 缺陷 | 一句话 |
|---|---|---|
| 1 | `projects/` 返回了分页信封 | 前端期望裸数组 |
| 2 | `projects/details/` 404 | 未实现 |
| 3 | 项目子资源被 `{project_id}` 子树吞掉 | → "Project not found" |
| 4 | 路由冲突导致启动 panic | ServeMux 重叠子树 |
| 5 | CORS 的 `--origins` 是必填项 | 见 `06-PITFALLS.md` D-8 |
| 6 | JSON 字段被序列化成 `null` | 见 D-5 |
| 7 | `GET /api/timezones/` 404 | 未实现 |
| 8 | `GET /api/unsplash/?query=` 404 | 未实现 |
| 9 | `POST .../pages/` 强制要求 `name` | 参数校验过严 |
| 10 | **Pages 编辑器死循环 —— 必须运行 `apps/live`** | 见 D-9 |
| 11 | Space 渲染崩溃 —— `project_details.logo_props` 缺失 | 字段缺失 |
| 12 | `project-deploy-boards/` 返回数组，前端当对象读 | 形状错 |
| 13 | 发布时 `view_props` 被丢弃 —— Space 列表整个空掉 | 字段丢失 |
| 14 | Space issues 返回扁平数组，前端要"按 state 分组" | 形状错 |
| 15 | Triage 状态漏进项目状态列表 | 过滤语义错 |
| 16 | Space 的 issue 列表按升序返回 | 排序错 |
| 17 | ⭐ **未注册子路径返回 200 + 无关对象**（ServeMux 子树） | **最核心的 bug 类**，见 A-1 |
| 18 | `user-profile` / `user-stats` / `user-activity` 整体缺失 | 被 #17 掩盖 |
| 19 | `workspace-members/me/` 缺失 → 全屏 "Not Authorized!" | 被 #17 掩盖 |
| 20 | `states` / `user-properties` / `sidebar-preferences` 缺失 | 被 #17 掩盖 |
| 21 | `home-preferences/` 缺失 → 首页挂件全部消失 | 被 #17 掩盖 + 前端渲染成"正常" |

**从这 21 个缺陷提炼出的五条结论**（值得所有后来者记住）：

1. **前端零改动的兼容层策略成立** —— 所有问题都出在复刻层，没有一处需要改前端源码。
2. **"接口 404"和"接口返回语义不对的 200"是两种完全不同的故障。** 前者立刻可见，
   后者会被前端当合法数据一路带下去，在离根因很远的地方炸掉。最耗时的两个缺陷（#17、#14）
   都属于后者，成因是同一个 Go 语义细节 → 因此必须靠**静态护栏**防，而不是靠注释和记性。
3. **错误的 200 不只掩盖"炸掉的页面"，还会掩盖"根本不存在的接口"**（#19/#20 的四个接口）。
   **修掉一个 bug 之后"变差"，往往是更深一层的 bug 终于暴露了。**
4. **前端会把失败渲染成看起来正常的状态**（#21 的 "It's Quiet Without Widgets, Turn Them On"）。
   因此**"截图没有异常"不能作为验收标准** —— 必须断言**具体内容**。
5. **一切顺利却等一刻钟不往下跑的等待循环，大概率是一行不起眼的命令导致的**（`curl -o /dev/null` 退出 23）。
   修好后启动从 ~9 分钟降到 2 秒。

---

## 5. 建议的下一步（按优先级）

| 优先级 | 事项 | 为什么 | 预估改动面 |
|---|---|---|---|
| **P0** | 补 §3.3 类别 C 的边角操作（尤其 `bulk-*` 与 `favorite-pages/`） | 列表页**可见入口**，点了就 404，用户最容易撞到 | 每项 1 个 handler + 1 条路由 |
| **P0** | 补 `user-issues/{user_id}/` | profile 页 `/assigned` 标签是空的，是已知的"半成品" | 1 个 handler + 1 条路由 + 断言 |
| **P1** | 处理部署阻断项 `N-2`（`SameSite` 跨站问题） | 不改就无法跨站部署 | 改 `middleware/auth.go` 的 cookie 设置 |
| **P1** | 补 §3.2 中 Cycle / View / Intake 的模块内缺口 | 这些模块有基础接口但功能不完整 | 按模块整体规划 |
| **P2** | 建立发布前自动化检查脚本（把 `05-TESTING.md` §9 串起来） | 减少人工遗漏 | 1 个 shell 脚本 |
| **P2** | 安全审计（对照 `spec/permissions.md` 逐端点核对守卫） | 未做过 | 系统性审查 |
| **P3** | Magic Link 的发信能力 | 当前不通 | 需要邮件服务 |
| **P3** | 性能/并发评估（SQLite 单写者模型） | 影响可支撑的规模 | 压测 |
| **不做** | §3.1 类别 A 的全部条目 | 已在方案中明确剔除 | — |

---

## 6. 契约文档索引（实现的权威来源）

**动手前先读这些，不要凭记忆写代码。**

| 文档 | 位置 | 内容 | 大小 |
|---|---|---|---|
| API 清单 | `spec/api-spec.yaml` | 391 个端点：方法 / 视图类 / 请求响应形状 / 是否被前端调用 / 是否剔除 | ~170 KB |
| Schema 映射 | `spec/schema-spec.md` | 每个 Django 模型 → PocketBase collection/field 的权威映射；§2 是 UUID 策略（规范性） | ~210 KB |
| 权限模型 | `spec/permissions.md` | 两级角色轴、逐端点守卫、Guest 的创建者例外 | ~32 KB |
| 剔除清单 | `spec/exclusions.md` | 五块被剔除功能的后端文件/端点/前端路由/环境变量逐项清单，含 §6「保留但受影响的调用点及替代方案」与 §7「待决项」 | ~26 KB |

### 快速查找技巧

```bash
cd plane-pocketbase

# 某个端点在 spec 里怎么定义的
grep -n -A20 "<路径片段>" spec/api-spec.yaml

# 某个 Django 模型怎么映射的（用 ### MODEL: 作锚点）
grep -n "### MODEL: <模型名>" spec/schema-spec.md

# 某个端点的权限要求
grep -n -B3 -A15 "<端点名>" spec/permissions.md

# 某个功能是否被剔除
grep -n "<功能名>" spec/exclusions.md
```

---

## 7. 数据模型规模速览

**79 个 collection**，按域分组（来自 `pb_migrations/`）：

| 域 | collection |
|---|---|
| 身份 / 工作区 | `users`, `workspaces`, `workspace_members`, `workspace_member_invites`, `teams`, `workspace_themes`, `workspace_user_properties`, `workspace_home_preferences`, `workspace_user_preferences`, `workspace_user_links`, `profiles`, `api_tokens`, `api_activity_logs`, `sessions`(≈`accounts`) |
| 项目 | `projects`, `project_members`, `project_member_invites`, `project_public_members`, `project_user_properties`, `project_identifiers`, `project_issue_types`, `project_pages`, `project_deploy_boards`, `deploy_boards` |
| 分类 | `states`, `labels`, `issue_types`, `estimates`, `estimate_points` |
| 工作项核心 | `issues`, `issue_assignees`, `issue_labels`, `issue_subscribers`, `issue_votes`, `issue_sequences`, `issue_views` |
| 工作项子资源 | `issue_comments`, `comment_reactions`, `issue_reactions`, `issue_attachments`, `file_assets`, `issue_links`, `issue_relations`, `issue_blockers`, `issue_mentions`, `issue_activities`, `issue_versions`, `issue_description_versions`, `issue_user_properties`(→`issue_types`) |
| Cycle / Module | `cycles`, `cycle_issues`, `cycle_user_properties`, `modules`, `module_issues`, `module_links`, `module_members`, `module_user_properties` |
| Intake / Draft | `intakes`, `intake_issues`, `draft_issues`, `draft_issue_assignees`, `draft_issue_cycles`, `draft_issue_labels`, `draft_issue_modules` |
| 页面 | `pages`, `page_labels`, `page_logs`, `page_versions`, `descriptions`, `description_versions` |
| 视图 / 收藏 | `user_favorites`, `user_recent_visits`, `stickies`, `analytic_views` |
| 通知 | `notifications`, `email_notification_logs`, `user_notification_preferences`, `changelogs` |
| 实例（license app） | `instances`, `instance_admins`, `instance_configurations` |

> **注意**：`instances` / `instance_admins` / `instance_configurations` / `changelogs`
> 这 4 个来自 Plane 的 `license` app（不同的 Django app、同一个库）。
> `spec/schema-spec.md` 把它们标为 `RECOMMENDED EXCLUDE`，
> 但**实例初始化流程需要它们**，所以实际实现了。

---

## 8. 文档维护约定

改动以下内容时，**同步更新对应文档**（这是纪律，不是建议）：

| 你改了什么 | 更新哪个文档 |
|---|---|
| 新增/删除路由 | 重跑 `audit-endpoints.py`，更新本文件 §1 与 §3 |
| 修掉一个缺陷 | 在 `tests/WORKFLOW_TEST_REPORT.md` 补编号条目；本文件 §4 加索引 |
| 发现一个新的坑 | 追加到 [`06-PITFALLS.md`](06-PITFALLS.md)，并在其附录索引里加一行 |
| 改了架构/数据策略 | 更新 [`01-ARCHITECTURE.md`](01-ARCHITECTURE.md)，必要时在 §10 ADR 表加一行 |
| 改了启动/依赖/环境变量 | 更新 [`02-GETTING-STARTED.md`](02-GETTING-STARTED.md) |
| 改了部署方式/参数 | 更新 [`04-DEPLOYMENT.md`](04-DEPLOYMENT.md) |
| 改了测试链路/证据约定 | 更新 [`05-TESTING.md`](05-TESTING.md) |
| 完成了一个路线图项 | 更新本文件 §5，把它移到 §2 |

---

**回到**：[`00-INDEX.md`](00-INDEX.md)（知识库导航）
