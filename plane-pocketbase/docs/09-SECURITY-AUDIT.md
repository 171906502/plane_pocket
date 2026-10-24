# 09 · 安全审计（对照 `spec/permissions.md` 逐端点核对守卫）

> **范围**：`plane-pocketbase/` 全部 525 条路由注册，逐一核对「谁可以调这个端点」是否与契约
> （`spec/permissions.md` 的 52 行能力矩阵 + §5 特例）一致。
> **日期**：2026-10-15（三轮）。**结论**：
>
> | 轮次 | 方法 | 结果 |
> |---|---|---|
> | 第一轮 | 契约（permissions.md / api-spec）+ 代码 | 确认并修复 **1 处越权缺口**：`issues.PlainList` 缺 Guest 过滤（GHSA-32c7-84jc-4w67 绕过变体）；162 条自动标记全部归类误报 |
> | 第二轮 | **对照 Django 源码**（`plane-preview/apps/api/plane/`）逐行比对**路由级**守卫 | 4 条"契约未固定"全部结清（3 条无偏离 + 1 条死代码清理）；**新发现并修复 3 处真实偏离**：view 权限阶梯（含 1 处 Guest 403 形状）、`RequireProjectRole` 缺 ws-admin 旁路（影响 18 条路由）、deploy board 守卫过严 |
> | 第三轮 | 同一方法用在**处理器内部**的内联判定上 | **新发现并修复 6 处偏离**：`issues` 关联三端点完全无守卫、`page` lock/unlock 无守卫、`page` description PATCH 无守卫、`page.findPage` 缺私有页可见性（读取型泄漏）、`page.Archive` 过严、45 处内联判定缺 ws-admin 旁路。详见 §5.8 |
>
> 三轮合计：**10 处真实偏离**（1 处读取型泄漏 + 4 处缺守卫 + 1 处读取型泄漏 + 1 处过严 + 3 处过严/形状）。
> 这不是渗透测试，也不是依赖漏洞扫描。它回答一个具体问题：**后端有没有把某个端点开得比
> Plane v1.4.2 更宽（或更窄）**——前者是数据泄露，后者是功能缺陷，两类都需要证据。

---

## 1. 为什么值得做，以及为什么路径级审计查不出来

`docs/05-TESTING.md` §9 的两套静态审计（`audit-endpoints.py` / `audit-methods.py`）只核对
**路径**与 **HTTP 方法**是否存在。它们对「端点存在且方法正确、但守卫缺失」完全无感——
这正是本项目此前真实的故障形态：

- `GET …/issues/list/?issues=<id,id>`（fetch-by-id 变体）存在、方法正确、返回 200，
  但它**绕过了 Guest 的创建者过滤**。契约里明写：

  > `spec/permissions.md` §5.1：该过滤必须在**每一条** issue 列举路径上生效，
  > 包括 `?issues=<id,id>` 这个「按 id 取」变体，因为该端点此前绕过它（GHSA-32c7-84jc-4w67）。

- 前端视图看板的过滤此前被静默忽略（`?filters=` JSON 表达式树），同样是"有端点、有方法、
  有 200，但语义不对"。

所以本次审计的**判据不是状态码，而是守卫落点**：读代码，确认每个变更类端点的守卫在哪一行。

---

## 2. 方法

三层核对，逐端点追到"谁放行"：

| 层 | 位置 | 查什么 |
|---|---|---|
| **L1 路由层** | `internal/router/*.go` | 是否有 `BindFunc(middleware.RequireProjectRole(...))` / `RequireWorkspaceRole(...)`；组级中间件（`workspaceGroup`、`APIKey`） |
| **L2 处理器层** | `internal/handlers/<domain>/*.go` | 是否解析 `middleware.ScopeOf(e)`；`scope == nil → 404`（非成员泄漏防护）；是否调用 `middleware.IsCreator` / `CanWrite` / 专用 `adminGuard` |
| **L3 数据层** | 查询构造处 | 是否按 `workspace`/`project`/`receiver`/`user` 收窄（对象级作用域），而不只是"能通过路由" |

自动化辅助：`_scratch/guard-inventory.py` 把 525 条注册映射到处理器函数，扫描函数体 + 一层
辅助函数里的守卫标记（`PROJ` / `WS` / `CREATOR` / `GUEST` / `OWNFILTER` / `AUTH`），把
「变更类方法但没有任何角色守卫标记」的条目打上 `REVIEW`。输出：`_scratch/guard-inventory.txt`
（525 行，162 条 `REVIEW`）。

> `REVIEW` 只是**人工复核的入口**，不是结论。L1 的守卫（`BindFunc`）在处理器函数体之外，
> 扫描器看不到——这是误报的主要来源，也是 §4 存在的原因。

---

## 3. 已确认并修复的缺口

### 3.1 `issues.PlainList` 缺失 Guest 创建者过滤（GHSA-32c7-84jc-4w67 绕过变体）

**端点**：`GET /api/workspaces/{slug}/projects/{project_id}/issues/list/?issues=<uuid>,<uuid>`
**文件**：`internal/handlers/issues/issues.go` → `PlainList`

**缺口**：分页集合端点 `listIssues` 有 Guest 过滤，`PlainList` 没有。任何活跃项目成员
（含 Guest）只要拿到 issue UUID，就能读出**其他用户创建**的 issue 内容——契约 §5.1 明确要求
该过滤覆盖此路径。

**修复**（`issues.go`）：

```go
guestRestricted := scope.ProjectRole == middleware.RoleGuest &&
    !scope.Project.GetBool("guest_view_all_features") && user != nil
...
if guestRestricted && issue.GetString("created_by") != user.Id {
    continue
}
```

**回归测试**：`internal/handlers/issues/guestlist_test.go`（3 个用例，内容断言）

| 用例 | 断言 |
|---|---|
| `TestPlainListGuestScope` | 同时请求「自己的 + 他人的」两个 UUID → 只返回 1 条且 `name == "mine"`；单独请求他人 UUID → 空数组 |
| `TestPlainListGuestViewAllFeatures` | `guest_view_all_features = true` 时同一请求返回 2 条 |
| `TestPlainListMemberSeesAll` | 对照：Member 角色不受影响，返回 2 条 |

**已做反向验证**（证明测试非空转）：临时把守卫条件改成 `false && …` 后重跑
`TestPlainListGuestScope` → **FAIL**，返回体里出现 `"created_by":"…00c2"`（他人）的记录；
恢复守卫后通过。这是"测试确实能捕获回归"的证据，而不是"测试恰好是绿的"。

---

## 4. 逐域核对结果

| 域 | 守卫落点（读到的代码） | 结论 |
|---|---|---|
| **auth / users** | `internal/handlers/users/*`：`/api/users/me/*` 全部以 `CurrentUser(e)` 为根；api-tokens 自作用域（`user == token.user`，非本人 404） | ✅ 契约 §5.4「token 自作用域、404 而非 403」一致 |
| **workspaces** | `router/workspace.go:32` `DELETE /{$}` → `RequireWorkspaceRole(RoleAdmin)`；`invitations/*` 4 条 → `RequireWorkspaceRole(RoleMember)`；`members/leave` 无角色要求（契约 `workspace.leave` = A/A/A） | ✅ 矩阵 13 行逐条对上 |
| **projects** | `router/project.go:47/48/53-57` → `RequireProjectRole(RoleAdmin)`；`:62/66` archive/unarchive → `RequireProjectRole(RoleMember)`（契约明写 **Member 允许**）；`:70` mark-default → `RoleAdmin` | ✅ 与矩阵 `project.update/delete/archive` 一致；ws-admin 旁路由 `ResolveProject` 承担 |
| **issues（列举）** | `listIssues` / `ListDetail` / `PlainList` 三条路径现均有 Guest 过滤；`?filters=` 走 `internal/filters`；`?priority=`/`?state_id=` 多值语义 | ✅ §5.1 全覆盖（本审计修复点） |
| **issues（写入）** | `Create` 内联 `RoleMember` 判定；`Update`/`Delete` 先 `IsCreator` 后角色（矩阵 `creator=True` 例外）；`BulkDelete`/`Restore` → Admin | ✅ 与 `issue.create/update/delete/bulk_delete/restore` 一致 |
| **cycle / module** | `router/cycle.go` / `module.go` 的 manage 类 → `RoleMember`；`Delete` 走 `IsCreator` 例外（`.delete.own` 行） | ✅ 与 `cycle.manage`/`cycle.delete.own`、`module.manage`/`module.delete.own` 一致 |
| **view** | `view.go:265` `ProjectMember == nil → Forbidden`；`:291` `WorkspaceMember == nil → Forbidden` | ⚠️ 见 §5.1（矩阵无 `view.*` 行，属未固定项） |
| **intake** | `IssuesList` 走 `scope` + 过滤；`IssuesCreate`/`DraftsCreate` 以项目成员为界；`IssueDelete` 走 `IsCreator` | ✅ 与 `intake.read/manage/delete_issue` 一致 |
| **asset** | `scopedWorkspaceAsset` / `scopedProjectAsset` / `scopedAttachment` 均收窄到 `workspace` + `project` + `deleted_at`；`UserAsset*` 收窄到 `user == 调用者`；上传路径 `createAsset` 内联 `WorkspaceRole >= RoleAdmin` | ✅ 契约唯一固定规则（`test_workspace_file_asset_project_scope_app.py`：资产按 project 作用域）一致；`canWriteAsset` 为死代码，见 §5.3 |
| **notification** | `notification.go:397 find()` 收窄 `workspace` + `receiver == user.Id` + `deleted_at`，否则 404 | ✅ 自作用域正确 |
| **publicapi（/api/v1）** | `router/publicapi.go:19` 整组 `BindFunc(middleware.APIKey(app))`，按 `api_tokens` 鉴权；契约 §8.4 说明 token 视为**用户级**而非工作区级 | ✅ 未鉴权不可达 |
| **instance（/api/instances）** | `instance.go:146/204/246/304` 全部先 `h.adminGuard(e) == nil → Forbidden` | ✅ 实例管理员会话为界 |
| **space（/api/public）** | `router/admin.go:47-86` 全部 `AllowAny`；契约 §5.3 明写这些是**公开部署看板**端点（`AllowAny`），仅以 anchor 作用域约束对象 | ✅ 公开是有意设计，非缺口 |

---

## 5. 未固定项观察清单 —— 第二轮：已用 Django 源码结清

> **2026-10-15 补记**：Django 源码就在 `plane-preview/apps/api/plane/`，所以第一轮
> "契约未固定"的四条**全部核对完毕**。结论：**3 条确认无偏离，1 条为死代码清理**；
> 同时**新发现 2 处真实偏离**（都在 view 域）与 **1 处守卫层偏离**（ws-admin 旁路缺失），
> 见 §5.5–§5.7。

| # | 端点 | Django 权威来源 | 结论 |
|---|---|---|---|
| 1 | asset v2 `PATCH`/`DELETE`（workspace / project / user） | `app/views/asset/v2.py:417/445/461/647/663/674` → `@allow_permission([ADMIN, MEMBER, GUEST], level="WORKSPACE")` | ✅ **无偏离**：上游就是"任意工作区成员（含 Guest）"，与 Go 的 `scoped*` 成员身份判定一致 |
| 2 | workspace 级 `labels` | `app/views/workspace/label.py:17-18` → `permission_classes = [WorkspaceViewerPermission]` | ✅ **无偏离**：viewer = 任意成员（含 Guest） |
| 3 | `quick-links` | `app/views/workspace/quick_link.py:23-61` → 全部 `[ADMIN, MEMBER, GUEST]` level=WORKSPACE | ✅ **无偏离** |
| 4 | `issue-relation` 增删 | `app/views/issue/relation.py:40` → `permission_classes = [ProjectEntityPermission]` | ✅ **无偏离**：活跃项目成员即可（Safe/非 Safe 均以成员身份为界） |
| — | **`asset.canWriteAsset`** | 上游规则是 `[ADMIN, MEMBER, GUEST]`（见第 1 行），**不是**"创建者或 Member" | ❌ **死代码且规则错误 → 已删除**。此前它从未被调用；若将来有人"接线"，会把行为改得比上游更严 |

> **教训**：`canWriteAsset` 这类"看起来更安全"的死代码，比没有代码更危险——
> 它是下一次重构的诱饵。审计里遇到死代码要判断两件事：是否被调用、规则是否与上游一致。

---

## 5.5 第二轮新发现 A：view 域权限阶梯（3 处，已修）

拿 Django 逐行比对后发现 Go 的 view 守卫"看起来对、细节全错"。权威来源
`app/views/view/base.py`：

| 端点 | Django 守卫 | 修复前的 Go | 修复后 |
|---|---|---|---|
| `PATCH /views/{pk}/`（项目级） | `@allow_permission(allowed_roles=[], creator=True, model=IssueView)`（`:349`）——**空角色表**，故只有创建者与 ws-admin 通过；随后 body 再判 `owned_by != user → 400`（`:358-362`） | 非所有者一律 400 | 非所有者：**普通成员 403**（`{"error":"You don't have the required permissions."}`）／**ws-admin 400**（`"Only the owner of the view can update the view"`） |
| `DELETE /views/{pk}/`（项目级） | `[ROLE.ADMIN], creator=True`（`:371`）+ body 用**原始** ProjectMember 角色复查（`:374-383`） | 非 admin 非所有者一律 400 | 同上阶梯：普通成员 403 / ws-admin 400 / 项目 admin 204 |
| `GET /views/{pk}/`（Guest 非所有者） | `IssueViewViewSet.retrieve` 显式 `403 {"error":"You are not allowed to view this issue"}`（`:323-337`） | 与"不存在"合并成 404 | Guest 非所有者 → **403 + 上游原文**；其余不可见仍 404 |
| `POST project-views/` `POST workspace-views/` | `{"error": "Forbidden"}` 403（`project/base.py:481`） | `{"detail": "Forbidden"}` | `{"error": "Forbidden"}` |

同时确认两个**容易做反**的细节：

- Django 的 `partial_update`/`destroy` 用 `IssueView.objects.get(...)`（**裸查询，无可见性过滤**），
  只有 `list`/`retrieve` 走 `Q(owned_by=user) | Q(access=1)`。所以"成员删他人私有视图"
  是 **403**，不是 404 —— Go 此前用带可见性过滤的解析器，返回了 404。
- 新增 `serializers.ErrorObject`：上游 app-API 的自有拒绝用 `{"error": …}`，
  DRF 通用权限类用 `{"detail": …}`。两种形状并存，必须按端点的上游来源选。

回归：`internal/handlers/view/view_test.go`（3 个用例覆盖完整阶梯 + 反向验证）。

---

## 5.6 第二轮新发现 B：`RequireProjectRole` 缺 ws-admin 旁路（已修）

**这是本轮最实质的发现**，因为它影响 18 条路由。

`plane.app.permissions.allow_permission` 的 PROJECT 分支（`app/permissions/base.py:65-81`）是：

```
ProjectMember(role__in=allowed)  OR  (ProjectMember 活跃 AND WorkspaceMember.role == 20)
```

即**工作区管理员只要同时是活跃项目成员，就通过所有项目级行**，与他的项目角色无关。
`app/permissions/project.py:38-46` 的 `ProjectBasePermission` 同义。

修复前的 Go：

- `ResolveProject`（`middleware/scope.go`）里的"提升"分支要求 `ProjectRole == 0`
  **且** `ProjectMember != nil` —— 而活跃项目成员的 role 必非 0，所以**该分支不可达**；
  注释里写的"workspace Admin 会被授予项目角色"从未生效。
- `RequireProjectRole` 只比较 `scope.ProjectRole`，于是
  **工作区管理员 + 项目角色 Member(15) 在 states / labels / members / invitations /
  bulk-delete / restore / mark-default / project update·delete 等 15 条路由上被 403**，
  而上游允许。这是"比上游更严"的偏离（不是泄漏，但功能上是缺陷：ws-admin 在项目里
  点不动状态、删不掉页面）。

修复：

- `RequireProjectRole` 加上游旁路：`ProjectRole >= min` **或**（`ProjectMember != nil` 且
  `WorkspaceRole >= RoleAdmin`）。
- 新增 `RequireProjectMemberRole`（**故意无旁路**），因为 deploy board 用的是
  `ProjectMemberPermission`（`app/permissions/project.py:49-70`）：非安全方法为
  `ProjectMember.role__in=[ADMIN, MEMBER]` 的**裸查询**，没有 ws-admin 逃生口。
  据此把 `project-deploy-boards` 的 POST/PATCH/DELETE 从 `RoleAdmin` 放宽为
  `RequireProjectMemberRole(RoleMember)`（上游允许项目 Member 管理发布板）。

回归：`internal/middleware/scope_test.go`（3 组表驱动用例，含反向验证：去掉旁路即 FAIL）。

---

## 5.7 记在账上但**未改**的一处统一偏离

所有角色守卫的 403 文案，上游 `allow_permission` 是
`{"error": "You don't have the required permissions."}`，本后端统一用 DRF 通用文案
`{"detail": "You do not have permission to perform this action."}`（约 40 处）。
两者都表达"角色不足"，前端只按 403 处理，故本轮**不动**，只在
`middleware/scope.go` 把文案收敛为一个常量 `deniedMessage`，将来要改是一行的事。

---

## 5.8 第三轮（内联守卫逐行比对）——6 处偏离，已修

第二轮只看了**路由级**守卫（`BindFunc`）。第三轮把同一方法用在**处理器内部**的角色判定上：
cycle / module / intake / issues / notifications / page 六个域的内联 `scope.ProjectRole` 比较，
逐条对照 `plane-preview/apps/api/plane/app/views/**` 的装饰器与 `app/permissions/*.py` 的权限类。

### 5.8.1 缺失守卫（4 处，Go 比上游更宽）

| # | 位置 | 修复前的实际效果 | 上游判据 |
|---|---|---|---|
| 1 | `issues.CreateRelations` / `RemoveRelation` / `DeleteRelation` | **完全没有角色判定**，任何 Guest 都能建/删工作项关联 | `IssueRelationViewSet.permission_classes = [ProjectEntityPermission]`（`issue/relation.py:40`），非安全方法 = `ProjectMember.role__in=[ADMIN, MEMBER]` 裸查询，**无旁路** |
| 2 | `page.setLocked`（`POST/DELETE …/lock/`） | 无角色判定，Guest 可锁定/解锁项目内任意页面 | `PageViewSet.permission_classes = [ProjectPagePermission]`（`page/base.py:79`）：POST → ADMIN/MEMBER，DELETE → ADMIN，所有者为早退分支 |
| 3 | `page.DescriptionPatch`（`PATCH …/description/`） | 无角色判定，Guest 可改写任意页面的协作文档 | `PagesDescriptionViewSet`（`page/base.py:514`）同样用 `ProjectPagePermission`，PATCH → ADMIN/MEMBER |
| 4 | `page.findPage`（被 Detail / Description / versions / lock / archive 共用） | 缺 `Q(owned_by=caller) \| Q(access=0)` 可见性规则，**任何项目成员/管理员都能按 id 读到他人私有页面**（列表接口 `listPages` 是对的，只有按 id 取数的路径漏了） | `PageViewSet.get_queryset`（`page/base.py:99`）的 `.filter(Q(owned_by=self.request.user) \| Q(access=0))`；`retrieve` 用它取数，取不到即 404 |

第 4 条是这一轮唯一的**读取型泄漏**：页面列表已经正确隐藏私有页，但 `GET …/pages/{page_id}/`
把内容完整返回。修复后 `findPage` 直接返回 nil，非所有者得到与"页面不存在"一致的响应。

### 5.8.2 比上游更严（1 处）

`page.Archive`（`POST …/pages/{page_id}/archive/`）对非所有者要求 **ADMIN**。
`ProjectPagePermission` 的 POST 分支是 `ADMIN/MEMBER`，所以普通项目成员归档页面被误拒 400。
已放宽为 MEMBER（`Unarchive` 是 DELETE，仍是 ADMIN，**未动**）。

### 5.8.3 系统性偏离：内联判定缺 ws-admin 旁路（45 处）

第二轮给 `RequireProjectRole` 补上了 `allow_permission` 的旁路，但**处理器内部**的
`scope.ProjectRole < middleware.RoleMember` 是第三处独立实现，同样缺旁路 ——
于是同一个上游规则在三处表现不一致（路由级对、内联级错）。

- 新增 `middleware.ProjectRoleAtLeast(e, minRole)`，与 `RequireProjectRole` 共用同一判定，
  并加了一条**两者必须永远一致**的表驱动测试（`TestProjectRoleAtLeastMatchesRequireProjectRole`）。
- 45 处内联判定改为调用它（脚本按显式白名单逐行替换，非全局正则）。

**为什么不在 `ResolveProject` 里提升 `ProjectRole`**（那样一处改动就能覆盖全部）：
`scope.ProjectRole == RoleGuest` 同时被**访客可见性过滤**使用
（`issues.listIssues` / `PlainList` / `intake` / `page.listPages` / `identifier.go`），
而 Plane 的 queryset 过滤用的就是**原始**项目角色。把旁路放进解析层会让
"工作区管理员 + 项目 Guest" 绕过访客过滤、看到全部工作项 —— 那是把修 bug 变成开洞。
旁路只属于**谓词**，不属于**作用域**。

**故意不加旁路的 17 处**（上游用的不是 `allow_permission`）：

| 文件 | 判据 |
|---|---|
| `module/link.go`（3 处）、`issues/wave3.go` 的 sub-issues / issue-links（4 处） | `ProjectEntityPermission` —— 裸 `role__in=[ADMIN, MEMBER]` |
| `module/module.go` 的 Archive / Unarchive（2 处） | `ModuleArchiveUnarchiveEndpoint` 用 `ProjectEntityPermission` |
| `issues/wave1.go` 的 BulkArchive（1 处） | `BulkArchiveIssuesEndpoint` 同时挂 `ProjectEntityPermission`，取交集后无旁路 |
| `page/page.go` 的 Create / Update / Delete / Archive / Unarchive / Duplicate / Move（7 处） | `ProjectPagePermission`（权限类，非装饰器） |

`BulkOperation` 与 `Move` 是纯客户端路由（上游无对应 URL），沿用同族方法的地板，未加旁路。

### 5.8.4 记在账上但**未改**：上游用裸 `IsAuthenticated` 的方法

`BaseViewSet.permission_classes = [IsAuthenticated]`（`app/views/base.py:51`），
而 `IntakeViewSet.retrieve` / `partial_update`、`CycleIssueViewSet.retrieve` /
`update` / `partial_update` 等**没有** `@allow_permission` 装饰器 —— 上游实际上只要求登录。
Go 在这些路径上要求项目成员/角色，**比上游更严**。

按"契约优先、不得凭空放宽"的原则，这里**不动**：把权限放宽到"任何已登录用户"
等于复制一个上游 IDOR 缺口。记在此处，作为"Go 比上游更严"的已知清单。

---

## 6. 162 条 `REVIEW` 的误报分类（判据）

| 类别 | 为什么被标记 | 判据 | 例 |
|---|---|---|---|
| **A. 守卫在路由 `BindFunc`** | 扫描器只看函数体 | L1 读到 `RequireProjectRole/RequireWorkspaceRole` | `projects.Delete/Archive/MarkDefault`、`workspaces.Delete`、`invitations/*` |
| **B. 作用域解析即守卫** | `scope == nil → 404` 不算"角色标记" | 非成员根本拿不到 `scope` | 各域所有 `ScopeOf(e)` 入口 |
| **C. 自作用域对象** | 无角色语义 | 收窄到 `user`/`receiver`，越界 404 | notifications、user-assets、api-tokens、`/api/users/me/*` |
| **D. 契约本就对全员开放** | 矩阵列是 A/A/A 或 A/A/C | 对齐 `permissions.md` 行 | reactions、subscribe/unsubscribe、`issue_comment.create`（A/A/A） |
| **E. 专用守卫** | 非通用角色标记 | 读到专用函数/中间件 | `instance.adminGuard`、`publicapi` 组级 `APIKey`、asset `scoped*` |

---

## 7. 复现与回归

```bash
cd plane-pocketbase

# 第一轮修复的定向回归（3 用例）
go test ./internal/handlers/issues/ -run TestPlainList -count=1

# 第二轮修复的定向回归
go test ./internal/middleware/ -run TestRequireProject -count=1   # ws-admin 旁路 + 无旁路变体
go test ./internal/handlers/view/ -run TestView -count=1          # 权限阶梯 + Guest 403

# 第三轮修复的定向回归（内联守卫）
go test ./internal/middleware/ -run TestProjectRoleAtLeast -count=1      # 旁路谓词 + 与路由级一致性
go test ./internal/handlers/cycle/ -run TestCreateWorkspaceAdminBypass -count=1
go test ./internal/handlers/issues/ -run TestRelationMutationsRequireMember -count=1
go test ./internal/handlers/page/ -count=1                               # 私有页可见性 / lock / description / archive

# 全量静态 + 单元 + 迁移自检（含双审计基线比对）
bash tests/release-check.sh --fast

# 重建守卫清单（人工复核入口，非判定）
python ../_scratch/guard-inventory.py > ../_scratch/guard-inventory.txt
```

`tests/release-check.sh` 的 `METHOD_MISSING_BASELINE=0` 与两条 UNSERVED 基线保证：
**路径/方法层面的回归会被自动拦住**；本次审计补上的是它拦不住的**语义层**——
所以修复都配了内容断言测试，而不是只断言 200。

---

## 8. 结论

1. **第一轮：1 处真实越权缺口**（`PlainList` Guest 过滤）已修复，并有可证伪的回归测试与反向验证。
2. **第二轮（对照 Django 源码）：3 处真实偏离已修复**——
   ① view 更新/删除的权限阶梯（普通成员 403 vs ws-admin 400，此前一律 400）；
   ② Guest 取他人私有 view 的 403 原文（此前与 404 合并）；
   ③ `RequireProjectRole` 缺 ws-admin 旁路（18 条路由对"工作区管理员 + 项目 Member"误拒），
   外加 deploy board 从 `RoleAdmin` 纠正为上游的 Admin/Member。
3. **第三轮（内联守卫逐行比对）：6 处偏离已修复**——
   ① `issues` 关联三端点**完全没有守卫**（Guest 可建/删工作项关联）；
   ② `page` lock/unlock 无守卫；③ `page` description PATCH 无守卫；
   ④ `page.findPage` 缺 `owned_by|access=0` 可见性规则，**任何成员可读他人私有页面**（唯一的读取型泄漏）；
   ⑤ `page.Archive` 对非所有者要求 ADMIN，上游是 ADMIN/MEMBER（过严）；
   ⑥ 45 处内联判定缺 ws-admin 旁路，与第二轮修好的路由级守卫不一致。
   修复的关键设计：旁路只放进谓词 `ProjectRoleAtLeast`，**不放进作用域解析**——
   否则 `ProjectRole == RoleGuest` 的访客可见性过滤会被一并绕过。
4. 矩阵 52 行逐条对上：13 条 workspace 级 + 39 条 project 级守卫落点与契约一致，
   含 `creator=True` 例外（guest 可改自己的 issue/comment/cycle/module）、
   `project.archive` 的 Member 例外、ws-admin 旁路、非成员 404 而非 403。
5. 162 条自动标记全部归类为误报或未固定项，分类判据见 §6；未固定项 4 条已在 §5 结清。
   记在账上但未改的 2 条：403 文案形状（§5.7）、上游用裸 `IsAuthenticated` 的方法（§5.8.4）。
6. 方法学结论：**路径级与方法级审计无法发现守卫问题**（`docs/07` §4 结论 7 的延续），
   而**只看 Go 代码也定不了案**——本轮最有价值的发现都要求把 Go 的判定顺序
   与上游装饰器逐行对齐（谁先谁后、用哪个字段、哪个分支不可达）。
   而且**同一个上游规则在本仓库有三处实现**（路由 `BindFunc`、处理器内联、数据层过滤），
   修了一处不等于修了全部——这正是第三轮能再挖出 6 处的原因。
   守卫核对 = 上游源码 + 判定顺序 + 内容断言。
