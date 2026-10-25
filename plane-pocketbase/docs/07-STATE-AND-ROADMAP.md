# 07 · 现状与路线图

> 这份文档回答三个问题：**已经做完什么 / 还没做什么 / 下一步该做什么。**
>
> 数字会随迭代变化。**每次批量补完接口后请更新本文件**，
> 并重跑 `python tests/browser/audit-endpoints.py` 与
> `python tests/browser/audit-methods.py` 获取最新基线。

---

## 1. 当前基线（截至 2026-10-15）

| 指标 | 数值 | 怎么来的 |
|---|---|---|
| 仓库受控文件 | 6773 | `git ls-files \| wc -l` |
| 后端 Go 代码 | 133 个文件 / 36933 行 | `internal/` + `pb_migrations/` |
| **已注册路由** | **339** 个模式 | `tests/browser/audit-endpoints.py` |
| Plane collection 数 | 79 个 | `pb_migrations/` |
| 数据库迁移 | 13 个 | `pb_migrations/*.go` |
| 前端调用形状 | 150 个 | `tests/browser/audit-endpoints.py` |
| 静态审计未匹配 | **26** 种路径形状：**15** 项方案内排除 + **10** 项不可达的 `epics` 分支 + **1** 项三元模板误报；真实缺口 **0** | `tests/browser/audit-endpoints.py`（2026-10-15 实测 339 注册 / 150 形状 / 26 UNSERVED）；逐条归类见 `docs/10-REMAINING-WORK.md` §T-4 |
| 已修缺陷 | 25 项已索引；全流程测试报告叙述其中 21 项 | 本文件 §4 / `tests/WORKFLOW_TEST_REPORT.md` |
| HTTP 方法级覆盖 | 已盘点并**清零**（2026-10-15 复测）：注册 **571** 个 (方法, 模式) 对；前端 368 个 (方法, 路径形状) 调用（served **293**）；METHOD-MISSING **0**；剩余 **49** UNSERVED 均为方案内排除（webhooks/integrations/importers/AI/assets）、`epics` 幽灵分支或三元模板误报 | `tests/browser/audit-methods.py`；分类见 §3.3 注 |
| 前端代码改动 | **0 行** | 核心承诺 |

---

## 2. 已完成

### 2.1 基础设施

| 项 | 状态 |
|---|---|
| Git 仓库（三棵树合一） | ✅ `main` @ `1c763a5`（当前基线） |
| 根 `.gitignore` / `.gitattributes` | ✅ 含 `-text` 保护与 `go:embed` 保护 |
| PocketBase 当库集成 | ✅ `go.mod` `replace` 本地依赖 |
| 79 个 collection 的定义 | ✅ 13 个迁移，分 wave 组织 |
| UUID 双主键策略 | ✅ 已落地（`uuid` 列 + 序列化层映射） |
| 会话认证（cookie + PocketBase JWT） | ✅ `session-id` / `admin-session-id` |
| CSRF（double-submit） | ✅ 含 `PLANE_PB_CSRF=off` 调试开关 |
| CORS（含 credentialed 支持） | ✅ 手动注入 `Allow-Credentials` |
| 权限模型（两级角色轴） | ✅ `middleware/scope.go` |
| 路由锚点护栏（AST 测试） | ✅ `internal/router/patterns_test.go` |
| 游标分页 / 分组分页 | ✅ `serializers/pagination.go` |
| 静态缺口审计工具 | ✅ `tests/browser/audit-endpoints.py` |
| 方法级缺口审计工具 | ✅ `tests/browser/audit-methods.py`（2026-10-10 首次全域盘点） |
| 浏览器验收链路（6 脚本） | ✅ `tests/browser/run*.mjs` |
| 栈管理脚本（起 / 停 / 重启 / 状态 / 重置） | ✅ `tests/browser/stack.sh`（转发到 `start-stack.sh` / `stop-stack.sh` / `reset-data.sh`） |
| Node 运行时解析（不写死版本化路径） | ✅ `tests/browser/node-env.sh`，见 `06-PITFALLS.md` **C-13** |
| 测试报告与证据存档 | ✅ 30 张截图 + 网络日志 |

### 2.2 功能域

| 域 | 状态 | 备注 |
|---|---|---|
| 实例初始化 / admin 控制台 | ✅ | `/god-mode/` |
| 认证（邮箱密码 / 注册 / 登出） | ✅ | @ Magic Link 验证码走独立日志文件（`PLANE_MAGIC_LOG`），不发邮件，见 N-5 |
| 工作区（创建/成员/偏好） | ✅ | |
| 项目（创建/成员/标识） | ✅ | |
| 工作项（增删改查/评论/附件/关系） | ✅ 主流程 | 边角操作见 §3.3 |
| 页面（Pages 数据面 + 描述） | ✅ | CRUD / 描述二进制 / 层级 均已实现；**富文本协作编辑暂不可用**——`apps/live` 属 N-10，2026-10-16 决定**暂不实现**（见 `10-REMAINING-WORK.md` §3.1、`01` D5′） |
| 发布到 Space | ✅ | |
| Space 端（公开只读视图） | ✅ | |
| Cycle / Module | ✅ 主流程 | 端点全覆盖（2026-10-11 复核）；多值过滤已修；估算点降级为 0（见 §3.3） |
| View（保存视图/收藏/最近） | ✅ 主流程 | 看板经通用 issues 端点 + `filters` 表达式取数；`rich_filters` 正确保存 |
| Intake（收件箱） | ✅ 主流程 | 过滤条多值/桥表/日期窗口与 `issue__*` 排序已补齐（2026-10-11） |
| 搜索 / Analytics | ✅ | |
| 通知 | ✅ | |
| 附件 / 上传 | ✅ | |
| Deploy Board | ✅ | |
| Public API（`/api/v1/` + `X-Api-Key`） | ✅ | |
| AI / GitHub / Slack / OAuth / 导入导出 / 出站 Webhook | ❌ **按方案剔除** | 见 `spec/exclusions.md` |

---

## 3. 静态审计与实际剩余缺口（截至 2026-10-15）

> 最新静态审计（2026-10-15 实测）：**339** 个已注册路由模式、150 种前端调用形状、**26** 种路径形状未匹配。
> 26 项逐条归类为 **15 项方案内排除 + 10 项不可达的 `epics` 分支 + 1 项三元模板误报**，
> **不是 26 个真实待办**，真实缺口为 **0**。上游 Django/spec 没有 `epics` API，不要为这些形状新增路由。
> 逐条明细（含每条的引用依据）见 `docs/10-REMAINING-WORK.md` §T-4。
>
> 路径级审计不核对 HTTP 方法，也不能代表整模块的功能完整度。仍需跟进的功能缺口见 §3.2。

### 3.1 类别 A · 方案内明确剔除（当前审计中 15 项，**不要做**）

这些是**故意不实现**的，因为项目决定剔除对应功能（见 `spec/exclusions.md`）。
2026-10-15 实测 `audit-endpoints.py` 的 26 项未匹配形状中，下列 15 项属此类：

| 路径形状 | 被剔除的功能 | 依据 |
|---|---|---|
| `/api/integrations/` | GitHub/Slack 集成 | `spec/exclusions.md` §2 |
| `/api/workspaces/{}/workspace-integrations/` | GitHub/Slack 集成 | `spec/exclusions.md` §2 |
| `/api/workspaces/{}/workspace-integrations/{}/github-repositories/` | GitHub | `spec/exclusions.md` §2 |
| `/api/workspaces/{}/workspace-integrations/{}/provider/` | 集成 | `spec/exclusions.md` §2 |
| `/api/workspaces/{}/projects/{}/workspace-integrations/{}/github-repository-sync/` | GitHub | `spec/exclusions.md` §2 |
| `/api/workspaces/{}/projects/{}/workspace-integrations/{}/project-slack-sync/` | Slack | `spec/exclusions.md` §2 |
| `/api/workspaces/{}/webhooks/` | 出站 Webhook | `spec/exclusions.md` §5.2 |
| `/api/workspaces/{}/webhooks/{}/regenerate/` | 出站 Webhook | `spec/exclusions.md` §5.2 |
| `/api/workspaces/{}/importers/` | 导入 | `spec/exclusions.md` §4.1 |
| `/api/workspaces/{}/importers/jira/` | 导入（Jira） | `spec/exclusions.md` §4.1 / §7-C |
| `/api/workspaces/{}/projects/importers/jira/` | 导入（Jira） | `spec/exclusions.md` §4.1 |
| `/api/workspaces/{}/export-issues/` | 导出 | `spec/exclusions.md` §4.3 |
| `/api/workspaces/{}/ai-assistant/` | AI | `spec/exclusions.md` §1.2 |
| `/api/workspaces/{}/rephrase-grammar/` | AI（上游亦无此路由，死调用） | `spec/exclusions.md` §1.2 |
| `/api/assets/v2/workspaces/{}/{}/bulk/` | assets-v2（上游同样无此 URL） | `docs/08-IMPLEMENTATION-PLAN.md` §11.2 |

**处理方式**：保持 404。**不要**为了消除数字而实现它们。
如果将来要加回来，先更新 `spec/exclusions.md`。

### 3.1b `{serviceType}` 模板形状（当前审计中 11 项：10 幽灵 + 1 误报，**不要做**）

其余 11 项未匹配形状都源自前端模板变量 `${this.serviceType}`（取值域 `issues` / `epics`，
见 `apps/web/services/issue/*.ts`）。其 `issues` 分支后端**均已注册并实测**；唯一不可达的是
`epics` 分支——上游 Django/spec 没有 `epics` API，**不实现**（见
`docs/08-IMPLEMENTATION-PLAN.md` §11.1）。最后一行是纯静态解析误报。

| 路径形状 | 前端来源（`{serviceType}` 所在行） | 说明 |
|---|---|---|
| `/api/assets/v2/workspaces/{}/projects/{}/{}/{}/attachments/` | `issue_attachment.service.ts:52` | `issues/…/attachments/` 已注册（`router/asset.go:51-65`）；`epics` 幽灵 |
| `/api/workspaces/{}/projects/{}/{}-detail/` | `issue.service.ts:48` | `issues-detail/` 已注册（`router/issue.go:33`）；`epics-detail/` 幽灵 |
| `/api/workspaces/{}/projects/{}/{}/list/` | `issue.service.ts:130` | `issues/list/` 已注册（`router/issue.go:26`）；`epics/list/` 幽灵 |
| `/api/workspaces/{}/projects/{}/{}/{}/archive/` | `issue_archive.service.ts:43` | `issues/{pk}/archive/` 已注册；`epics` 幽灵 |
| `/api/workspaces/{}/projects/{}/{}/{}/comments/` | `issue_comment.service.ts:55` | `issues/{}/comments/` 已注册；`epics` 幽灵 |
| `/api/workspaces/{}/projects/{}/{}/{}/description-versions/` | `work_item_version.service.ts:29` | `issues/{}/description-versions/` 已注册；`epics` 幽灵 |
| `/api/workspaces/{}/projects/{}/{}/{}/history/` | `issue.service.ts:140` / `issue_activity.service.ts:32` | `issues/{}/history/` 已注册；`epics` 幽灵 |
| `/api/workspaces/{}/projects/{}/{}/{}/issue-relation/` | `issue.service.ts:185` | `issues/{}/issue-relation/` 已注册；`epics` 幽灵 |
| `/api/workspaces/{}/projects/{}/{}/{}/reactions/` | `issue_reaction.service.ts:39` | `issues/{}/reactions/` 已注册；`epics` 幽灵 |
| `/api/workspaces/{}/projects/{}/{}/{}/subscribe/` | `issue.service.ts:385` | `issues/{}/subscribe/` 已注册；`epics` 幽灵 |
| `/api/workspaces/{}/projects/{}/{}/{}/${this.serviceType === EIssueServiceType.EPICS …` | `issue.service.ts:261` | **三元模板误报**：`${cond ? "issues" : "sub-issues"}` 无法静态解析，`sub-issues/` 已注册（`router/issue.go:84`） |

**处理方式**：保持现状。**不要**为 `epics` 新增别名路由，也不要为了消数字动这些形状。

### 3.2 类别 B · 部分实现模块的剩余缺口

工作项详情配套接口、quick-links 以及 Wave 3 的附件、用户活动导出和 file-assets 路由已在前几波完成；不要再把它们列为待办。

**2026-10-11 复核结论**：Cycle、View、Intake 三个域的路径/方法面已全覆盖且行为缺口已补齐（见 §5 P1 划线行），本节从「部分实现」降级为「主流程完成」。遗留的**有意降级**（不是待办）：cycle/module 估算点聚合恒 0、cycle analytics `completion_chart` 为空——两者依赖 `issues.estimate_point` 与 issue 状态历史，本切片未建模。

| 域 | 当前状态 | 备注 |
|---|---|---|
| Cycle / Estimate | ✅ 主流程完成 | 端点全覆盖；多值过滤已修；估算点降级见上 |
| View | ✅ 主流程完成 | 看板取数走通用 issues 端点 + `?filters=<表达式树>`（`internal/filters` 求值）；`getViewIssues` 已注册但非主流程 |
| Intake | ✅ 主流程完成 | 过滤条（status/priority/state/assignees/labels/created_by/日期窗口）与 `issue__*` 排序已补齐 |

**处理方式**：先从前端 service 明确端点、方法和响应形状，再按模块整体实现和验收；不要以静态审计数字下降作为唯一完成标准。

### 3.3 类别 C · ⭐ 已实现模块的边角操作（**已全部补齐**）

这一类曾是最高优先级，因为它们是列表页上的可见入口；目前已完成并验收，不再是待办。

> **2026-10-10 更新**：下表中的接口已注册并实测通过（Wave 1 + Wave 2）。
> Wave 3 另完成了 issue 附件 V2、profile 用户活动 CSV 导出以及 legacy file-assets/restore 路由，L3 验收细节见 `docs/08-IMPLEMENTATION-PLAN.md` §11。
> 下表由「待办清单」改为「验收记录」，并修正了原表的**两处不准确**：
> ① 原表漏列了约 11 项同类入口；② 原表把 `favorite-pages/` 记作"整体缺失"，
> 实际 `POST`/`DELETE` 早已存在，缺的只有 `GET`。

| 路径 | 用户在哪里看到 | 状态 |
|---|---|---|
| `bulk-archive-issues/` | 工作项列表 → 批量操作菜单 | ✅ |
| `bulk-delete-issues/` | 同上 | ✅ |
| `bulk-operation-issues/` | 同上 | ✅ |
| `bulk-subscribe-issues/` | 同上 | ✅ |
| `bulk-create-labels/` | 同上（批量打标签） | ✅ |
| `issues/{}/meta/` | 工作项详情（`project_identifier` / `sequence_id`） | ✅ |
| `issues/{}/issue-relation/` | 工作项详情 → 关联 | ✅ |
| `issues/{}/remove-relation/` | 同上 | ✅ |
| `issues/{}/history/` | 工作项详情 → 活动 | ✅ |
| `issues/{}/description-versions/` | 工作项详情 → 版本历史 | ✅ |
| `issues/{}/comments/` + `comments/{}/reactions/` | 工作项详情 → 评论 | ✅ |
| `issues/{}/subscribe/` | 工作项详情 → 订阅 | ✅ |
| `issues/{}/reactions/` | 工作项详情 → 表情 | ✅ |
| `issue-dates/` · `issue-display-properties/` · `epics-user-properties/` | 工作项筛选器 / 显示属性 | ✅ |
| `projects/{}/issue-labels/` **CRUD** | 项目设置 → 标签 | ✅（原表漏列；见下方注） |
| `projects/{}/archive/` **DELETE** | 项目设置 → 恢复项目 | ✅（原表漏列） |
| `pages/{}/move/` | 页面 → 移动到项目 | ✅ |
| `states/{}/mark-default/` | 项目设置 → 状态 | ✅ |
| `deleted-issues/` | 工作项列表 → 已删除筛选 | ✅ |
| `archived-issues/` | 工作项列表 → 已归档筛选 | ✅ |
| `favorite-pages/` | 侧边栏 → 收藏页面 | ✅（**修正**：原表记作整体缺失，实际只缺 `GET`） |
| `my-issues/` | 个人工作项视图 | ✅ |
| `user-issues/{user_id}/` | profile 页 `/assigned` 标签 | ✅ |
| `quick-links/` | 工作区快捷链接 | ✅ |
| `project-members/me/` · `members/` · `user-properties/` | 项目页每次加载 | ✅ |
| `workspace-members/me/` · `sidebar-preferences/` · `home-preferences/` | 应用外壳 / 首页 | ✅ |

> ⚠️ **一个 audit 脚本看不见的缺陷类别（方法级缺口）**：
> `audit-endpoints.py` diff 的是**路径**。`projects/{}/issue-labels/` 这条路径
> **早已注册**（只有 `GET`），所以脚本永远报"已服务" —— 而前端要的
> `POST`/`PATCH`/`DELETE` 全部 404。
> **同类**：`projects/{}/archive/` 只有 `POST`，缺 `DELETE`（"恢复项目"点了没反应）。
> 两处均在 Wave 2 收尾时补齐并实测（见 §4 缺陷 23/24）。
> **结论：路径级审计不能替代逐方法核对；有疑点一律回去读前端 service 文件。**
>
> ✅ **2026-10-10 已完成全域方法级普查**（`audit-methods.py`）：从 `apps/web` 的
> TS service 中提取 **368** 个 (HTTP 方法, 路径形状) 调用（含 `${this.serviceType}`
> 三元分支展开），与 `internal/router/*.go` 的注册逐对 diff。发现并**当场补齐
> 全部真实缺口**（均已配内容断言测试实测）：
>
> | 补齐的端点 | 契约来源 |
> |---|---|
> | `issues/{issue_id}/sub-issues/` GET/POST | `sub_issue.py` |
> | `issues/{issue_id}/issue-links/` GET/POST + `{pk}/` GET/PATCH/PUT/DELETE | `link.py` |
> | `issues/{pk}/archive/` GET/POST/DELETE | `archive.py` |
> | `projects/{}/issues-detail/` GET（列表项携带 `issue_relation`） | `base.py` + `serializers/issue.py` |
> | `projects/{}/members/` POST + `members/{pk}/` GET/PATCH/DELETE | `ProjectMemberViewSet`（ADMIN；自移除走 leave） |
> | `projects/{}/states/` POST + `states/{pk}/` GET/PATCH/PUT/DELETE | `StateViewSet`（`state.manage` → ADMIN） |
> | `workspaces/{}/members/{pk}/` GET/PATCH/DELETE | `WorkSpaceMemberViewSet`（ADMIN；不能改/移除自己与更高角色） |
> | `users/me/` DELETE（账号停用 `is_active=false`） | `UserEndpoint.delete` |
> | `sidebar-preferences/{key}/` PATCH（单个偏好项） | `WorkspaceUserPreferenceViewSet` |
> | `projects/{}/v2/issues/` GET + `work-items/{identifier}-{sequence}/` GET | `IssuePaginatedViewSet` / `IssueDetailIdentifierEndpoint` |
>
> 最终结果：**METHOD-MISSING 0**；剩余 49 UNSERVED 全部为方案内排除
> （webhooks、integrations、importers、AI、v2/assets）、`epics` 幽灵分支
> （**不实现**，见 `08-IMPLEMENTATION-PLAN.md` §11）或三元模板误报
> （如 `issues/{}/links/` —— issues 实际走 `issue-links/`）。

### 3.4 已完成的高优先级项

| 待办 | 说明 |
|---|---|
| ~~**`GET /api/workspaces/{slug}/user-issues/{user_id}/`**~~ | ✅ **已完成**。profile 个人页 `/assigned` 标签页的数据来源，实测 `total_count:1` 且**标题真实渲染在页面上**（`verify-profile.mjs` 断言通过） |


> **契约要点**（已在会话中推导，实现时请再核对 Django 源码）：
> - 权限：`IsAuthenticated` + `WorkspaceViewerPermission`
> - 过滤：`issue_objects` 语义 —— 排除软删除、已归档、草稿、归档项目下的工作项、以及 Triage 状态组；
>   限定为"该用户是 assignee **或** creator **或** subscriber"，并与**请求者**为活跃成员的项目取交集
> - 参数：`order_by`（默认 `-created_at`）、`sub_issue`（`false` ⇒ `parent__isnull=True`）、`assignees`、`cursor`、`per_page`
> - 无 `group_by` 时返回**扁平游标信封**
> - 参考实现：`plane-preview/apps/api/plane/app/views/workspace/user.py` 的
>   `WorkspaceUserProfileIssuesEndpoint`，过滤逻辑在 `plane-preview/apps/api/plane/utils/issue_filters.py`

---

## 4. 25 个已修缺陷索引

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
| 10 | **Pages 编辑器死循环 —— 必须运行 `apps/live`** | 见 D-9；**N-10 暂不实现（2026-10-16）后，改为不暴露富文本编辑入口**，见 D-9 顶部备注 |
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
| 22 | 脚本写死 Node 版本化路径 → `apps/live` 静默起不来 | 见 **C-13**；后果是 Pages 编辑器无限循环（#10）。**N-10 暂不实现后本条不触发**，启用 live 时再修 |
| 23 | **方法级缺口**：`projects/{}/issue-labels/` 只有 `GET` | 标签 CRUD 全 404；**路径级审计看不见**，见 §3.3 注 |
| 24 | **方法级缺口**：`projects/{}/archive/` 只有 `POST` | "恢复项目"404；Django 的 `DELETE` 应答 204 |
| 25 | `issue.cycle_id` 硬编码为 `nil` | `cycle_issues` 已存在后成了陈旧占位 → 周期进度恒为 0 |

**从这 25 个缺陷提炼出的十条结论**（值得所有后来者记住）：

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
6. **"路径已注册"不等于"接口已实现"。** `audit-endpoints.py` 按**路径**做 diff，
   于是"路径在、方法缺"的缺口（#23 `issue-labels/`、#24 `archive/`）**永远报已服务**。
   这类缺口只能靠**逐方法核对 + 读前端 service 文件**发现 —— 静态审计的盲区必须有另一套手段补上
   （现已落成 `audit-methods.py`，见 §3.3 注）。
7. **"端点已实现"也不等于"行为正确"。** Cycle/View/Intake 复核发现的两类行为缺口
   在任何路径/方法审计里都不可见：① **查询参数语义**（`priority=urgent,high` 多值、
   `?filters=<JSON 表达式树>`、`order_by=-issue__created_at` 的 `__`→`.` 映射）——
   必须追到前端 store 里参数的**构造处**（`computedFilteredParams`、
   `inboxIssueQueryParams`），而不是只看 service 方法的 URL；② **消费方式**
   （前端视图看板实际经通用端点取数，`getViewIssues` 非主流程）。回归测试要断言
   过滤**结果集内容**，不是断言 200。
8. **"行为正确"里最隐蔽的一类是"开得比上游更宽"**（安全审计，2026-10-15）。
   `issues/list/?issues=<id,id>` 路径在、方法对、200 正常，但**缺 Guest 创建者过滤**，
   任何成员拿到 UUID 就能读他人 issue。路径/方法审计对此完全无感——
   守卫核对必须读三层代码（路由 `BindFunc` → 处理器 `ScopeOf` → 数据层作用域），
   并把结果固化成**内容断言**（去掉守卫即 FAIL）。详见 `09-SECURITY-AUDIT.md`。
9. **"更严"也是缺陷，而且只有对着上游源码才能发现**（同次审计第二轮）。
   `RequireProjectRole` 缺了 `allow_permission` 的 ws-admin 旁路，于是
   "工作区管理员 + 项目角色 Member"在 18 条路由上被 403——上游允许。
   根因是 `ResolveProject` 里一个**不可达分支**（要求 `ProjectRole == 0` 同时要求
   `ProjectMember != nil`）让注释里的承诺从未生效。**读自己的代码看不出问题，
   必须把判定顺序（谁先谁后、比哪个字段）与上游装饰器逐行对齐。**
   顺带教训：**看起来更安全的死代码是下一次重构的诱饵**（`asset.canWriteAsset`）。
10. **同一个上游规则在本仓库有三处实现，修一处不等于修了全部**（同次审计第三轮）。
    `allow_permission` 的 PROJECT 分支同时体现在：路由级 `RequireProjectRole`、
    **处理器内联**的 `scope.ProjectRole` 比较、以及数据层的访客可见性过滤。
    第二轮修好了路由级，第三轮才发现 45 处内联判定仍是旧语义，于是同一个上游规则
    在同一个后端里表现不一致。另外三处**缺守卫**（`issues` 关联三端点、
    `page` lock/unlock、`page` description PATCH）和一处**读取型泄漏**
    （`page.findPage` 缺 `Q(owned_by=caller) | Q(access=0)`，任何成员可按 id 读他人私有页面）
    也只有把内联判定逐行对照上游才查得出。
    **教训：审计的粒度必须与实现的分布对齐 —— 规则在哪里重复实现，就要在哪里分别核对。**

---

## 5. 建议的下一步（按优先级）

> **2026-10-24 更新**：本节已完成项全部划掉；**未完成项已迁移到
> [`11-TASKLIST.md`](11-TASKLIST.md)**（L-1 … L-10 任务卡，含独占文件与可证伪验收标准）。
> 迁移的 5 个真实功能缺口（均为"接口在、数据永远空"）：
> `issue_activities`（活动流/燃尽图/仪表盘）、`page_versions` + `issue_description_versions`
> （版本历史）、`notifications`（收件箱）、`issues.estimate_point`（估算聚合恒 0）、
> `user_recent_visits`（最近访问）。另有一处运维缺口：备份脚本未覆盖 `<data>/live/`。
> 本节以下内容仅作**历史记录**保留。

| 优先级 | 事项 | 为什么 | 预估改动面 |
|---|---|---|---|
| ~~**P0**~~ | ~~补 §3.3 类别 C 的边角操作（尤其 `bulk-*` 与 `favorite-pages/`）~~ | ✅ **已完成**（Wave 1 + Wave 2，见 §3.3） | — |
| ~~**P0**~~ | ~~补 `user-issues/{user_id}/`~~ | ✅ **已完成**，profile `/assigned` 已实测渲染出标题 | — |
| ~~**P0**~~ | ~~Wave 3：附件 V2、profile 用户活动导出、file-assets/restore~~ | ✅ 已完成并 L3 实测；`epics` 变体经核对不是上游 API，不实现，见 `08-IMPLEMENTATION-PLAN.md` §11 | — |
| ~~**P1**~~ | ~~按模块补 Cycle / View / Intake 的剩余缺口~~ | ✅ **已完成**（2026-10-11）：① cycle/module/issues 列表的 `priority`/`state_id` 补上 MultipleChoiceFilter 多值语义（前端 `priority=urgent,high` 此前恒为空）；② 新增 `internal/filters` 富过滤求值器，消化前端 `?filters=<JSON 表达式树>`（exact/in/range、桥表 assignees/labels/cycle/module/subscribers、state_group、日期区间）——**主看板的全部用户过滤此前都被静默忽略**；③ intake 收件箱补齐 assignees/state/created_by/labels/日期窗口过滤与 `-issue__created_at` 系排序；④ latent 修复：`orIDFilter` 参数键冲突（同查询两次使用会互相覆盖）。已知降级保留：cycle/module 估算点恒 0、analytics `completion_chart` 空（issue 历史未建模） | — |
| ~~**P1**~~ | ~~**方法级缺口普查**：逐端点核对 HTTP 方法，不只核对路径~~ | ✅ **已完成并清零**（2026-10-10）：`audit-methods.py` 盘点 368 个调用，METHOD-MISSING 0，剩余 UNSERVED 均为排除/幽灵/误报，见 §3.3 注 | — |
| ~~**P1**~~ | ~~补齐方法级普查登记的剩余待办~~（`v2/issues`、`work-items/{identifier}-{sequence}`、members/states CRUD、`DELETE /api/users/me/`、`sidebar-preferences/{key}` PATCH、`members/{pk}/`） | ✅ **已全部补齐**并配内容断言测试 | — |
| ~~**P2**~~ | ~~评估跨站部署的 Cookie `SameSite` 配置~~ | ✅ **已完成**（2026-10-11）：`PLANE_COOKIE_SAMESITE`（`lax` 默认 / `strict` / `none`），选 `none` 强制 `Secure` 并对清除 cookie 复用同属性；配 `middleware/auth_test.go` 内容断言。同站部署零改动，见 `04-DEPLOYMENT.md` §3/§5、N-2 已划掉 | — |
| ~~**P2**~~ | ~~建立发布前自动化检查脚本（把 `05-TESTING.md` §9 串起来）~~ | ✅ **已完成**（2026-10-11）：`tests/release-check.sh`——build / vet / 单元测试 / 迁移自检 / 双审计基线比对（UNSERVED 超基线即失败），`--fast` 跳全量测试、`--full` 追加浏览器链；`--fast` 已实测 6/6 全过。注意：脚本内 python 调用必须 `echo` 名字而非执行（`then python` 会进 REPL 挂死）；审计超时 `AUDIT_TIMEOUT`（默认 180s）——**勿与 `go test` 并发跑**，否则审计被挤过超时会解析出空值、看起来像基线回归（现已改为"无输出"单独报错并打印原始输出） | — |
| ~~**P2**~~ | ~~安全审计（对照 `spec/permissions.md` 逐端点核对守卫）~~ | ✅ **已完成**（2026-10-15，三轮）：525 条路由逐域核对（L1 路由 `BindFunc` → L2 处理器内联判定 → L3 数据层作用域）。第一轮确认并修复 **1 处越权缺口**（`issues.PlainList` 缺 Guest 过滤，GHSA-32c7-84jc-4w67 绕过变体）；第二轮**对照 Django 源码**逐行比对路由级守卫，结清 4 条"契约未固定"并**再修 3 处真实偏离**（view 权限阶梯 403/400、Guest 取私有 view 的 403 原文、`RequireProjectRole` 缺 ws-admin 旁路——影响 18 条路由，外加 deploy board 守卫纠正为上游的 Admin/Member）；第三轮把同一方法用在**处理器内联判定**上，**再修 6 处**（`issues` 关联三端点完全无守卫、`page` lock/unlock 无守卫、`page` description PATCH 无守卫、`page.findPage` 缺私有页可见性——任何成员可按 id 读他人私有页面、`page.Archive` 过严、45 处内联判定缺 ws-admin 旁路）。三轮合计 **10 处真实偏离**，全部配内容断言 + 反向验证，见 `09-SECURITY-AUDIT.md` | — |
| **P3** | ~~Magic Link 的发信能力~~ | ✅ **日志文件级已完成**（2026-10-15）：`PLANE_MAGIC_LOG` 指向独立文件，验证码以 `0600` 追加写入（`RFC3339 / email / code / expires_at`），未设则退回进程日志。**不发真邮件**——按约定"单独日志文件即可"，属降级而非缺口。配 `auth/magic_test.go` 4 用例（独立文件/追加/退回日志/坏路径不吞码）+ 反向验证 | — |
| ~~**P3**~~ | ~~性能/并发评估（SQLite 单写者模型）~~ | **不做**（2026-10-15 决定）：本项目的设计目标是**小团队内部使用**，性能余量足够，不投入吞吐压测。容量结论按"小团队规模"给出即可，见 `04-DEPLOYMENT.md` §10 | — |
| ~~**P2**~~ | ~~备份自动化 + 恢复演练（N-7）~~ | ✅ **已完成**（2026-10-15）：`tests/backup.sh`（在线快照 + `storage/` + `MANIFEST.txt` sha256/行数 + 保留策略）、`tests/restore.sh`（先校验、旧目录 `mv` 不删、恢复后对账）、`tests/backup-drill.sh` **19/19 通过**。含关键负对照：未 checkpoint 的 WAL 下 `cp data.db` 会丢数据、在线备份不丢 | — |
| ~~**P2**~~ | ~~升级/迁移流程演练（N-9）~~ | ✅ **已完成**（2026-10-15）：`tests/upgrade-drill.sh` **12/12 通过**。实测：重复 `migrate up` 幂等；迁移后起服正常；**`migrate down` 要交互确认且连数据一起毁掉 → 回滚只能靠恢复备份**；迁移历史行丢失时 `migrate up` 失败、备份恢复可救回。写入 `03-ITERATION-GUIDE.md` §4.1 | — |
| ~~**P1**~~ | ~~Pages 协作端：不引入 Redis 也要能用~~ | ✅ **已完成**（2026-10-23）：纯 Go 中继 `internal/live/`（Hocuspocus 帧 + yjs 更新落盘回放 + awareness），挂 `GET /live/collaboration`，与后端同进程同端口。浏览器端到端验收通过（打开/输入/重载/双标签页实时同步/重启后回放），并补掉两处缺口：`description_stripped` 未派生、加载后不回写 `description_html`。见 `10-REMAINING-WORK.md` §3.3 §3.4 | — |
| **不做** | §3.1 类别 A 的全部条目 | 已在方案中明确剔除 | — |

---

## 6. 契约文档索引（实现的权威来源）

**动手前先读这些，不要凭记忆写代码。**

| 文档 | 位置 | 内容 | 大小 |
|---|---|---|---|
| API 清单 | `spec/api-spec.yaml` | 395 个端点：方法 / 视图类 / 请求响应形状 / 是否被前端调用 / 是否剔除 | ~170 KB |
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

### 7.1 已知未使用表（死表登记 · L-10，2026-10-25 实测）—— **不要重新实现**

下表 **14 张业务表在 Go 代码里零引用**（`internal/` + `main.go`，注释除外；验证方法见本节末尾）。
它们**不是功能缺口**。表保留在 schema 里（迁移删表风险 > 收益，见 `docs/11` §5），
但没有任何端点、handler 或测试会碰它们。
如果你认为某个功能缺了，**先查下表第三列**——多数情况是本移植有意的替代或裁剪，
不是"没做到"。要动工前先更新 `spec/exclusions.md` 和本节。

**(a) 被别的表取代**

| 表 | 上游模型（`spec/schema-spec.md` `MODEL:` 锚点） | 本移植的替代 | 替代实现位置 |
|---|---|---|---|
| `issue_attachments` | `IssueAttachment`（1307） | 附件统一走 assets-v2 通道 | `file_assets`（`internal/handlers/asset/asset.go`） |
| `issue_blockers` | `IssueBlocker`（1236） | 阻塞关系并入统一关系表 | `issue_relations` + `relation_type`（`internal/handlers/issues/wave2.go`） |
| `issue_versions` | `IssueVersion`（1452） | 版本历史只做描述版本 | `issue_description_versions`（`internal/hooks/version.go`，L-3） |
| `descriptions` | `Description`（1909） | 上游把评论富文本拆成 1:1 卫星表；本移植直接把 `comment_html/json/stripped` 存在 `issue_comments` 自己的列上 | `issue_comments` 列（有意偏离 spec §3.6 的镜像要求） |
| `description_versions` | `DescriptionVersion`（1927） | 同上；评论不另存历史版本 | `issue_description_versions`（仅 issue 描述） |
| `project_deploy_boards` | `ProjectDeployBoard`（957，上游标 `DEPRECATED TODO`） | 上游自己已用 DeployBoard 取代 | `deploy_boards`（`internal/handlers/projects/deploy_boards.go`） |

**(b) 上游功能未纳入本切片（且前端不调用）**

| 表 | 上游模型 | 为什么没实现 |
|---|---|---|
| `teams` | `Team`（737） | 上游 v1.4.2 本来就是休眠功能——spec 注明"not referenced by any view or serializer" |
| `workspace_themes` | `WorkspaceTheme`（754） | 主题功能，前端无调用 |
| `issue_types` / `project_issue_types` | `IssueType` / `ProjectIssueType`（1129 / 1149） | 工作项类型未纳入。注意：`projects.is_issue_type_enabled` **开关字段已移植**（`internal/handlers/projects/projects.go:170`）但背后无表——开关存在 ≠ 类型可用，不要只接开关 |
| `project_member_invites` | `ProjectMemberInvite`（913） | 项目级邀请行：本移植邀请走 `workspace_member_invites`，接受走 `ProjectInvitations` 直接入项（`internal/handlers/users/invitations.go:315`），没有任何端点持久化这张表。接受路径已实现，缺的是"创建"侧，属范围裁剪 |
| `project_public_members` | `ProjectPublicMember`（975） | 公开项目成员白名单边表；Space 公开访问改走 `deploy_boards` 发布路径（`internal/handlers/space/space.go:30`） |

**(c) 既定决策不实现**

| 表 | 上游模型 | 决策依据 |
|---|---|---|
| `api_activity_logs` | `APIActivityLog`（651） | API token 审计管道未纳入本切片（无端点读写）；要加先补 `spec/exclusions.md` |
| `email_notification_logs` | `EmailNotificationLog`（2112） | **不发真邮件**——既定决策：验证码/通知写日志文件（`PLANE_MAGIC_LOG`，`docs/04` N-5；`docs/10` §P3），此表保持空 |

**怎么复核"一张表是不是死的"**（本节登记时实际执行的命令）：

```bash
cd plane-pocketbase
# 1) 从迁移导出全部 collection 名
grep -hoE 'NewBaseCollection\("[a-z_]+"\)' pb_migrations/*.go | sed 's/.*("//;s/")//' | sort -u
# 2) 对每个名字统计 Go 代码引用（含注释），为 0 即死表候选
for t in <名字列表>; do printf "%s " $t; \
  grep -rn "$t" --include='*.go' internal/ main.go | wc -l; done
# 3) 命中 > 0 的逐条打开看：是注释、还是真代码？
grep -rn "<表名>" --include='*.go' internal/ main.go | grep -vE '//|/\*'
```

**三个反例（说明为什么第 3 步不可省）**：

- `accounts` 看似死表（OAuth 未纳入的叙事），实际是**只读活表**：
  `GET /api/users/me/accounts/` 在 `internal/handlers/users/invitations.go:45` 真查它
  （路由 `internal/router/users.go:31`，测试 `config/wave2c_test.go` 有数据断言）。
  **L-10 任务卡把它列错了**——本节已剔除。
- `project_deploy_boards` 全仓 grep 命中 1 处，但那是 doc 注释
  （`internal/serializers/projects.go:13`）；排除注释后为 0，才是死表。
- `draft_issue_*` 四张：字面量离写入远（经 `createJoin` 辅助函数），
  naive 窗口扫描会误判为只读（`docs/11` §1 轴 2 的既有警告）。

**对账**：L-10 卡片写"15 张"，实测 **14 张** = 卡片所列 12 张 − `accounts`（活表）
+ `description_versions` / `project_member_invites` / `project_public_members`（卡片漏列）。

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
