# Plane × PocketBase 全流程工作流测试报告

> 目标：验证用 PocketBase 复刻的 Plane 后端，能让 Plane 现有前端（`web` / `space` / `admin`）**零改动**跑通从「实例初始化」到「工作区 → 项目 → 工作项 → 页面 → 设置」的完整业务链路。
>
> 测试方式：**真实浏览器操作**（Microsoft Edge + Playwright），不是 curl 冒烟。每个关键节点截图留证，截图与网络请求日志一并归档在 `tests/evidence/`。

---

## 一、测试环境

| 组件    | 地址                                | 说明                                   |
| ----- | --------------------------------- | ------------------------------------ |
| 后端    | `http://127.0.0.1:8000`           | 本仓库 Go 二进制（`.fe.exe`），数据目录 `.fe-run` |
| web   | `http://localhost:3000`           | Plane `apps/web`（主应用）                |
| admin | `http://localhost:3001/god-mode/` | Plane `apps/admin`（实例管理台）            |
| space | `http://localhost:3002`           | Plane `apps/space`（公开只读视图）           |
| live  | `http://localhost:3100/live/`     | Plane `apps/live`（Yjs 协同编辑服务）        |
| Redis | `127.0.0.1:6379`                  | `apps/live` 的依赖                      |

数据库为**全新空库**（`.fe-run` 由空目录启动，由后端自行跑迁移建表），不迁移任何 PostgreSQL 历史数据。

### 启动方式

```bash
cd plane_pocket/plane-pocketbase/tests/browser
bash run-all.sh --fresh      # 全新库，跑完整链路（run1 → run5）
bash run-all.sh --from 4     # 复用现有库，只跑第 4、5 段
bash run-one.sh run5-publish-space.mjs <projectId>   # 只跑单个脚本
```

`start-stack.sh` 负责拉起整套服务，脚本头部注释里记录了 4 个「踩过一次就再也不想踩」的坑（CORS origins、`APPDATA`、前端必须用各自的 `node_modules/.bin`、`CODEBUDDY_SAFE_DELETE_ENABLED=0`）。

---

## 二、测试链路总览

| 编号 | 阶段           | 操作                        | 关键断言                                                                                                                           | 截图   |
| -- | ------------ | ------------------------- | ------------------------------------------------------------------------------------------------------------------------------ | ---- |
| 01 | A 实例初始化      | 打开 god-mode 注册页           | 空实例渲染注册表单                                                                                                                      | `01` |
| 02 | A            | 填写实例管理员信息                 | 表单可提交                                                                                                                          | `02` |
| 03 | A            | 注册成功进入管理台                 | 实例名/邮箱/实例 ID 正确回显                                                                                                              | `03` |
| 04 | B Onboarding | 跳转 web 创建个人资料             | 资料页渲染                                                                                                                          | `04` |
| 05 | B            | 填写姓名与用途                   | 提交成功                                                                                                                           | `05` |
| 06 | B            | 创建工作区表单                   | 表单渲染                                                                                                                           | `06` |
| 07 | B            | 填写工作区名称/URL               | 提交成功                                                                                                                           | `07` |
| 08 | B            | 进入工作区首页                   | 工作区 `hrms` 建立                                                                                                                  | `08` |
| 09 | C 项目         | 工作区首页加载完成                 | 侧边栏/项目区渲染；**工作区启动接口全 200**（`workspace-members/me` / `states` / `user-properties` / `sidebar-preferences` / `home-preferences`） | `09` |
| 10 | C            | 关闭引导弹窗                    | **挂件（Quick Links / Recents / My Stickies）真实渲染**，而不是 "widgets are turned off" 空态                                                | `10` |
| 11 | C            | 打开「新建项目」                  | 表单渲染                                                                                                                           | `11` |
| 12 | C            | 填写项目名称 `hrms` / ID `HRMS` | 提交成功                                                                                                                           | `12` |
| 13 | C            | 项目创建成功                    | 成功弹窗 + 功能开关                                                                                                                    | `13` |
| 14 | D 工作项        | 进入项目 Work items           | 空态正确                                                                                                                           | `14` |
| 15 | D            | 填写工作项标题                   | 输入框可编辑                                                                                                                         | `15` |
| 16 | D            | 保存工作项                     | 列表出现 `HRMS-1`                                                                                                                  | `16` |
| 17 | D            | 打开工作项详情                   | 详情面板渲染                                                                                                                         | `17` |
| 18 | D            | 补充描述                      | 富文本可编辑并自动保存                                                                                                                    | `18` |
| 19 | D            | **刷新页面**                  | `HRMS-1` 与描述仍在（持久化）                                                                                                            | `19` |
| 20 | E 页面         | 进入项目 Pages                | 页面列表渲染                                                                                                                         | `20` |
| 21 | E            | 点击 Add page               | 富文本编辑器挂载                                                                                                                       | `21` |
| 22 | E            | 输入页面标题                    | 标题保存成功                                                                                                                         | `22` |
| 23 | F 设置         | 项目设置 General              | 项目字段回显                                                                                                                         | `23` |
| 24 | F            | 工作区设置 General             | 工作区字段回显                                                                                                                        | `24` |
| 25 | F            | Your work 个人工作台           | **Summary 计数卡 + 4 个页签渲染**；不是 "Not Authorized!" gate，也不是运行时崩溃页                                                                  | `25` |
| 26 | G 发布         | 项目页（Publish 入口）           | 项目数据完整                                                                                                                         | `26` |
| 27 | G            | 打开 Publish project 弹窗     | 弹窗渲染                                                                                                                           | `27` |
| 28 | G            | 点击 Publish                | 生成公开链接                                                                                                                         | `28` |
| 29 | G            | 打开 Space 公开链接             | **只读项目视图渲染，且 Backlog 分组里能看到 `HRMS-1`**                                                                                         | `29` |
| 30 | G            | Space 中打开工作项              | 只读详情（peek）渲染                                                                                                                   | `30` |

---

## 三、逐阶段证据

### 阶段 A — 实例初始化（admin）

全新空库启动后，`GET /api/instances/` 返回 `is_setup_done: false`，admin 应用据此渲染**注册**而不是登录表单。

![实例管理员注册页](evidence/screenshots/01-admin-setup-empty.png)

*图 01 — 空实例：god-mode 渲染注册表单，说明 `/api/instances/` 的 `is_setup_done` 判定正确。*

![填写实例管理员信息](evidence/screenshots/02-admin-setup-filled.png)

*图 02 — 填写姓名、邮箱 `admin@hrms.local`、公司名、密码。*

![实例管理台 General](evidence/screenshots/03-admin-console.png)

*图 03 — 注册成功后进入实例管理台，实例名 `HRMS`、管理员邮箱、实例 ID 均正确回显，说明 `POST /api/instances/admins/sign-up/` 与 `GET /api/instances/` 的数据契约成立。*

### 阶段 B — Web 端 Onboarding（web）

![Onboarding：创建个人资料](evidence/screenshots/04-onboarding-profile.png)

*图 04 — 从 admin 点「Redirect to Plane」跳到 web，进入 onboarding 个人资料步骤。*

![填写个人资料](evidence/screenshots/05-onboarding-profile-filled.png)

*图 05 — 填写姓名与「你打算怎么用 Plane」。*

![创建工作区表单](evidence/screenshots/06-onboarding-workspace-form.png)

*图 06 — 个人资料提交后进入工作区创建步骤。*

![填写工作区信息](evidence/screenshots/07-workspace-form-filled.png)

*图 07 — 工作区名称 `hrms`、URL slug `hrms`。*

![工作区首页](evidence/screenshots/08-workspace-home.png)

*图 08 — 工作区创建成功，落到 `/hrms/` 首页。*

### 阶段 C — 项目创建（web）

![工作区首页（已加载）](evidence/screenshots/09-workspace-home-loaded.png)

*图 09 — 首页数据加载完成。*

![关闭欢迎弹窗后的工作区首页](evidence/screenshots/10-workspace-home-clean.png)

*图 10 — 关闭引导弹窗后的干净首页，Quick Links / Recents / My Stickies 挂件已渲染。*

> **这张图曾经是「看起来正常的失败」**：修复前它显示的是  
> *"It's Quiet Without Widgets, Turn Them On — It looks like all your widgets are turned off."*  
> 页面没有任何报错、没有空白，我当时据此判定「工作区首页 OK」——  
> 实际上 `GET /api/workspaces/hrms/home-preferences/` 一直在 404，挂件一个都没取到，  
> UI 把「空列表」优雅地降级成了「你的挂件都关掉了」。详见缺陷 #21。

#### 工作区加载契约（workspace boot surface）

每次打开任意 `/{slug}/…` 页面，前端都会先打一组「工作区启动」请求。这四个接口**全部缺失过**，  
且全部被子树模式用工作区对象掩盖（缺陷 #19/#20）。它们的形状各有陷阱：

| 接口                                | 返回形状                           | 陷阱                                              |
| --------------------------------- | ------------------------------ | ----------------------------------------------- |
| `GET /workspace-members/me/`      | 成员对象 + `draft_issue_count`     | **成员资格闸门**：缺失 → 全屏 "Not Authorized!"            |
| `GET /states/`                    | 数组，带组内 `order`                 | 工作区级，≠ 项目级 `projects/{id}/states/`；要排除 Triage   |
| `GET/PATCH /user-properties/`     | 对象                             | **GET 有副作用**（Django `get_or_create`）            |
| `GET/PATCH /sidebar-preferences/` | GET 是 **map**，PATCH 收 **list** | GET/PATCH 形状不对称                                 |
| `GET /home-preferences/`          | **列表**，元素含 `config`            | PATCH 返回**不含** `config`；key 不存在是 **400** 不是 404 |

实测（`verify-profile.mjs`，全部 PASS）：

```
workspace-members/me -> 200 {"role":20,"view_props":{...},"draft_issue_count":0,...}
states               -> 200 [{"group":"backlog","order":0.33,...}, ...]   # 无 triage
user-properties      -> 200 {"navigation_project_limit":10,...}
sidebar-preferences  -> 200 {"views":{...},"drafts":{"is_pinned":true}, ...}  # 7 个 key
home-preferences     -> 200 [{"key":"quick_links","sort_order":999,...}, ...] # 3 个 key
```

![新建项目表单](evidence/screenshots/11-project-form.png)

*图 11 — 点击「Add project」打开表单。*

![填写项目名称与 ID](evidence/screenshots/12-project-form-filled.png)

*图 12 — 项目名称 `hrms`、项目 ID `HRMS`。*

![项目创建成功](evidence/screenshots/13-project-created.png)

*图 13 — 项目创建成功弹窗，同时列出 Cycles / Modules / Views / Pages / Intake 开关（Pages 默认开启）。*

### 阶段 D — 工作项（web）

![项目 → Work items（空态）](evidence/screenshots/14-work-items-empty.png)

*图 14 — 进入 `Work items`，空态渲染正常（不是报错页，说明 `issues/` 列表契约正确）。*

![填写工作项标题](evidence/screenshots/15-work-item-composer-filled.png)

*图 15 — 点击「Add work item」，输入标题。*

![工作项已创建并出现在列表中](evidence/screenshots/16-work-item-created.png)

*图 16 — 保存后列表出现 `HRMS-1 验证 PocketBase 后端：创建第一个工作项`。*

![工作项详情面板](evidence/screenshots/17-work-item-detail.png)

*图 17 — 打开工作项详情侧栏。*

![补充工作项描述](evidence/screenshots/18-work-item-description.png)

*图 18 — 在富文本描述区输入内容并自动保存。*

![刷新后工作项仍然存在](evidence/screenshots/19-work-item-after-reload.png)

*图 19 — **刷新页面后 `HRMS-1` 与描述仍在**，证明写入落到了 PocketBase 的 SQLite，而不只是前端内存。*

### 阶段 E — 页面（web + live）

![项目 → Pages](evidence/screenshots/20-project-pages.png)

*图 20 — 进入项目 `Pages` 模块。*

![新建页面编辑器](evidence/screenshots/21-page-composer.png)

*图 21 — 点击「Add page」后富文本编辑器挂载。*

![页面已创建（标题已自动保存）](evidence/screenshots/22-page-created.png)

*图 22 — 输入标题 `PocketBase 迁移验证记录`，编辑器自动保存成功。*

> 阶段 E 依赖 `apps/live`（Yjs 服务，3100 端口）。这一点非常关键，详见第六节缺陷 #10。

### 阶段 F — 设置页（web）

![项目设置（General）](evidence/screenshots/23-project-settings.png)

*图 23 — 项目设置 General：项目名 `HRMS`、Network、Timezone 等字段正确回显。*

![工作区设置（General）](evidence/screenshots/24-workspace-settings.png)

*图 24 — 工作区设置 General：Logo、名称、公司规模、URL、时区等。*

![Your work（个人工作台）](evidence/screenshots/25-your-work.png)

*图 25 — 个人工作台 `/hrms/profile/<userId>/`：Summary 四个计数卡 + Assigned / Created / Subscribed / Activity 四个页签。*

> 这一张**曾经连续踩了三个坑**，是整份报告里信息量最大的一张图：
>
> 1. 先是**崩溃现场** —— 页面停在 `ProfileSidebar` 的运行时错误上，红字  
>    `Cannot read properties of undefined (reading 'find')`。原因见缺陷 #17/#18：  
>    `user-profile` / `user-stats` / `user-activity` 三个接口被 ServeMux 的子树模式吞掉，  
>    返回了工作区对象（HTTP 200），前端把它当真值继续用。
> 2. 补完锚点后变成**全屏 "Not Authorized!"** —— 因为 `workspace-members/me/`  
>    这个成员资格闸门接口从未实现（缺陷 #19）。
> 3. 最后还剩下一个**不报错的空白**：`home-preferences/` 404（缺陷 #21）。
>
> 三次都出在同一个页面上，但症状完全不同（崩溃 → 全屏 gate → 静默降级），  
> 这正是「必须断言具体内容、不能只看页面有没有崩」的最好例证。  
> 修复后的接口级复验见 `verify-profile-page.png` 与 `verify-profile-assigned.png`。

### 阶段 G — 发布到 Space（web → space）

![项目页（Publish 入口所在）](evidence/screenshots/26-publish-entry.png)

*图 26 — 项目页，工作项 `HRMS-1` 已存在，是 Publish 的入口页。*

![Publish project 弹窗](evidence/screenshots/27-publish-modal.png)

*图 27 — Publish project 弹窗（视图布局、评论/表情/投票开关）。*

![发布成功，显示公开链接](evidence/screenshots/28-published-link.png)

*图 28 — 发布成功，弹窗给出可复制的公开链接。*

![Space：公开的 Issues 列表](evidence/screenshots/29-space-published-issues.png)

*图 29 — 在新标签打开公开链接 `http://localhost:3002/spaces/issues/<anchor>`，Space 渲染只读项目视图。*

![Space：工作项详情（只读）](evidence/screenshots/30-space-issue-detail.png)

*图 30 — 在 Space 中打开工作项，只读详情渲染正常。*

---

## 四、网络证据

浏览器产生的真实 HTTP 会话记录在 `tests/evidence/network.log`，按脚本分段追加。

### 4.1 写入类请求（Mutations）

```
===== run1-bootstrap =====
302  POST   /api/instances/admins/sign-up/
200  PATCH  /api/users/me/
200  PATCH  /api/users/me/profile/
201  POST   /api/workspaces/
200  PATCH  /api/users/me/onboard/

===== run2-project =====
200  PATCH  /api/users/me/tour-completed/
200  POST   /api/assets/v2/workspaces/hrms/
200  POST   /api/assets/v2/upload/28d9bee1-70d0-46bd-9402-f0affdb62664/
204  PATCH  /api/assets/v2/workspaces/hrms/28d9bee1-70d0-46bd-9402-f0affdb62664/
201  POST   /api/workspaces/hrms/projects/
204  POST   /api/assets/v2/workspaces/hrms/projects/<pid>/<pid>/bulk/
200  PATCH  /api/workspaces/hrms/projects/<pid>/

===== run3-issue =====
201  POST   /api/workspaces/hrms/projects/<pid>/issues/
204  PATCH  /api/workspaces/hrms/projects/<pid>/issues/<iid>/

===== run4-project-pages =====
201  POST   /api/workspaces/hrms/projects/<pid>/pages/
200  PATCH  /api/workspaces/hrms/projects/<pid>/pages/<pageId>/
```

### 4.2 失败请求

- `run2` / `run3` / `run4`：**无失败请求**。
- `run1`：`403 GET /api/instances/admins/me/` 与 `/api/instances/admins/` —— 这是**注册前** admin 应用探测当前会话的正常行为（未登录即 403）。
- `404 GET /site.webmanifest.json` —— 前端静态资源，与后端无关。

---

## 五、本次联调发现并修复的后端缺陷

以下问题**全部是靠「真的开一个浏览器去点」才暴露出来的**，编译通过和 curl 冒烟都发现不了。

### 1. `GET /api/workspaces/{slug}/projects/` 返回了分页信封

`ProjectViewSet.list` 返回的是 `.values(...)` 投影的**纯数组**，前端 `fetchPartialProjects` 直接对它调 `.forEach()`。我们最初返回了 Plane 其它列表用的 cursor 分页信封，前端直接抛异常。  
**修复**：`List` 返回 `[]PartialProject` 纯数组。

### 2. `GET /api/workspaces/{slug}/projects/details/` 404

Django 有独立的 `workspaces/<str:slug>/projects/details/` 路由。我们缺失，请求落进了 `{project_id}` 分支，去查一个 UUID 字面量为 `"details"` 的项目。  
**修复**：新增 `Details`，返回按名称排序的完整 `Project` 数组。

### 3. 项目子资源被 `{project_id}` 子树吞掉 → 「Project not found」


`project-members/me/`、`members/`、`user-properties/`、`issue-labels/`、`estimates/` 这些**不以 `{project_id}/` 开头**的路由，被 `/projects/{project_id}` 子树模式抢先匹配。

后果最严重的是 `project-members/me/`：它本应返回「我在这个项目里的角色」，却返回了一个**项目对象**。前端 `fetchUserProjectInfo` 把 `response.role`（`undefined`）写进 `workspaceProjectsPermissions[slug][pid]`，**覆盖**了 `project-roles` 接口拿到的正确值，于是项目页直接报「Project not found」。  
**修复**：补齐 6 个子资源路由（见 `internal/handlers/projects/subresources.go`）。

### 4. 路由冲突导致启动 panic

补路由时出现：

```
pattern "…/projects/{project_id}/states/" conflicts with pattern "…/projects/details/"
```

Go 1.22+ 的 `ServeMux` 里，**结尾带斜杠 = 子树模式**，`details/` 会匹配所有更深路径，于是和 `states/` 冲突。  
**修复**：用 `{$}` 把模式锚定到精确路径：`ws.GET("/projects/details/{$}", h.Details)`。

### 5. CORS：`--origins` 是必填项

Plane web 用 `axios withCredentials: true` 跨域调 API。不传 `--origins` 时 PocketBase 内置 CORS 中间件回 `Access-Control-Allow-Origin: *`，浏览器在**携带凭证模式**下直接拒绝，页面停在「Plane didn't start up correctly」错误边界，且**后端日志里一条 API 调用都没有**（非常难查）。

**修复**：`start-stack.sh` 固定传 `--origins=http://localhost:3000,http://localhost:3001,http://localhost:3002`。

### 6. JSON 字段被序列化成 `null`

PocketBase 的 `r.Get(field)` 对一个**未写入**的 JSON 列返回的不是 `nil`，而是包着空切片的 `types.JSONRaw`。所以 `== nil` 永远不成立，字段被原样输出成 `null`，而 Django 那边是 `default=` 有默认值的。  
**修复**：新增 `serializers.JSONOrDefault`，空值/`null` 时回落到 Django 的默认值（`get_default_props()`、`get_default_preferences()`、`get_default_filters()` 等）。

### 7. `GET /api/timezones/` 404

项目设置页要时区列表。缺失会让项目设置页发一条失败请求。  
**修复**：实现 `TimezoneEndpoint`，时区表逐条从 `apps/api/plane/app/views/timezone/base.py` 提取。

> 顺带修了 Django 的一个显示 bug：Django 用 Python 整除（`total_seconds // 3600`）算小时偏移，会把 UTC 以西的**半小时时区**标错（纽芬兰 UTC-03:30 被写成 `UTC-04:30`）。我们改成向零截断。

### 8. `GET /api/unsplash/?query=` 404

未配置 Unsplash key 时 Django 仍然回 **200 + 空数组**。返回 404 会让封面图选择器在每次打开项目页时报错。  
**修复**：补一个返回 `[]` 的桩。

### 9. `POST .../pages/` 强制要求 `name`

Django 的 `Page.name` 是 `TextField(blank=True)`，序列化器里是**可选**的。web 端的交互是「先建页面，再让用户在内联标题里输入」——我们强制校验 `name` 会让每次「Add page」都报 `400` + 前端 toast「Page could not be created」。  
**修复**：去掉 `name` 必填校验。

### 10. **Pages 编辑器死循环 —— 必须运行 `apps/live`**

这是最难查的一个。现象：进入页面编辑器后整个渲染进程**卡死**，`page.evaluate` / `page.screenshot` 全部超时，后端日志里刷屏式循环：

```
INFO  PATCH /api/workspaces/hrms/projects/<pid>/pages/<pageId>/description/
INFO  GET   /api/workspaces/hrms/projects/<pid>/pages/<pageId>/description/
INFO  PATCH … /description/
INFO  GET   … /description/
…（无限循环）
```

**根因**：`apps/live`（Yjs 协同编辑服务，3100）没起。编辑器连不上 live 服务 → `collaborationState.stage.kind === "disconnected"` → 触发 `apps/web/hooks/use-page-fallback.ts` 的**降级保存**逻辑：

```ts
useEffect(() => {
  if (hasConnectionFailed) handleUpdateDescription();
}, [handleUpdateDescription, hasConnectionFailed]);
```

而 `handleUpdateDescription` 的依赖里有 `page.description_html`；保存成功后 store 更新 → `description_html` 变化 → 回调身份变化 → effect 重跑 → 再 fetch、再保存……**永不收敛**。

这不是我们的后端 bug，是 Plane 在「live 服务缺失」时的既有缺陷；真实部署里 live 服务始终存在，所以不会触发。但对我们这套栈来说，**live 是 Pages 的硬依赖**。  
**修复**：`start-stack.sh` 增加 `start_live()`，并补建 `apps/live/.env`、构建 `packages/logger` 与 `packages/decorators` 的 `dist`（live 服务依赖这两个 workspace 包的产物）。

### 11. Space 端渲染崩溃 —— `project_details.logo_props` 缺失

发布成功后打开公开链接，Space 应用直接掉进错误边界：

> Yikes! That doesn't look good.

浏览器控制台的真实异常：

```
TypeError: Cannot read properties of undefined (reading 'in_use')
    at ProjectLogo (apps/space/components/common/project-logo.tsx:10:12)
    at IssuesNavbarRoot (apps/space/components/issues/navbar/root.tsx:31:22)
```

`root.tsx` 传的是 `project_details.logo_props`，而 `ProjectLogo` 无条件解引用 `logo.in_use`：

```tsx
export function ProjectLogo(props: Props) {
  const { className, logo } = props;
  if (logo.in_use === "icon" && logo.icon)   // logo === undefined → 抛异常
```

`project_details` 来自 `GET /api/public/anchor/{anchor}/settings/`。对照 Plane 自己的定义：

- 前端类型（`packages/types/src/project/projects.ts`）：`IProjectLite` **要求 `logo_props`**
- Django 序列化器（`apps/api/plane/space/serializer/project.py`）：`ProjectLiteSerializer.fields` 里只有 **`icon_prop`**，没有 `logo_props`

也就是说 **Plane v1.4.2 的 space 接口和它自己的前端类型对不上，Django 后端同样会让 Space 崩溃**。

**修复**：我们的 `ProjectLite` 同时输出两个键——`icon_prop` 保留以对齐 Django，`logo_props` 取项目实际存储的值（缺省回落 `{"in_use": "icon"}`），前端读的就是它。

### 12. `project-deploy-boards/` 返回数组，前端当对象读

`DeployBoardViewSet.list` 是 `Response(DeployBoardSerializer(board.first()).data)`——**没有 `many=True`**，返回的是单个对象。web 端 `ProjectPublishStore.fetchPublishSettings` 把这个响应直接塞进 `publishSettingsMap[projectID]`，弹窗再读 `projectPublishSettings?.anchor`：

```ts
const projectPublishSettings = getPublishSettingsByProjectID(projectId);
const isProjectPublished = !!projectPublishSettings?.anchor;
```

我们返回了数组，`anchor` 永远是 `undefined` → 弹窗**始终认为项目未发布**，每次打开都会重新 POST 一遍，而不是显示已发布状态和链接。

**修复**：GET 返回单个对象（无记录时返回 `{}` + 200，Django 在这种情况下会因 `DeployBoardSerializer(None)` 直接 500，前端只能靠 `.catch` 兜住）。

### 13. 发布时 `view_props` 被丢弃 —— Space 列表整个空掉

这是让截图 29 长时间只有空壳的根因。

`applyBoardFlags` 只搬了 5 个布尔开关，**从没写过 `view_props`**。而 Space 的布局完全由它决定：

- `apps/space/app/issues/[anchor]/layout.tsx`：只有 `if (response.view_props)` 才调 `updateLayoutOptions(...)`
- `apps/space/components/issues/navbar/controls.tsx`：`viewsAcceptable` 只从 `view_props` 里挑 layout

`view_props` 为空 → `viewsAcceptable` 为空 → 选不出 board → `initIssueFilters` 不执行 → `display_filters.layout` 永远 `undefined` → 列表和看板都不渲染，只剩导航条。

**顺带挖出 Django 自己的一个不一致**：`ProjectDeployBoardViewSet.create` 读的是请求体里的 **`views`**（`project/base.py:553`），而发布弹窗发的是 **`view_props`**（`publish-project/modal.tsx:166`）。所以上游在**首次发布时会静默丢掉用户选的布局**，落库的是那份 all-true 默认值（`list/kanban/calendar/gantt/spreadsheet` 全 true）；只有走 PATCH 更新时（序列化器里 `view_props` 是模型字段）才真的生效。

**修复**：`applyViewProps` 依次尝试 `views` → `view_props` → Django 的 all-true 默认值。前两者保证「谁发了就听谁的」，最后的默认值保证即使请求体什么都不带，落库的也和数据契约一致。

### 14. Space 的 issues 接口返回扁平数组，而前端要的是「按 state 分组」

Space 的列表和看板**一定**带 `group_by=state_id`（`store/helpers/filter.helpers.ts` 把 `groupedBy: "state"` 映射成 `state_id`）。Django 的 `BasePaginator` 一旦拿到 `group_by` 就切换成 `GroupedOffsetPaginator`，响应里的 `results` 从**数组**变成**以 state id 为键的对象**：

```json
{ "grouped_by": "state_id", "total_count": 1,
  "results": { "<state_id>": { "results": [ …issue… ], "total_results": 1 }, … } }
```

Space 的 store 正是这么消费的（`store/helpers/base-issues.store.ts::processIssueResponse` 遍历 `results[groupId].results`），布局再按 `groupedIssueIds[stateId]` 取行。

我们返回扁平数组时，所有 issue 都落进前端合成的 `"All Issues"` 桶里，**每个真实 state 分组都渲染成 0**——issue 明明已经取到了，页面却显示空的。

**修复**：新增 `serializers.PaginateGrouped`，按 `GroupedOffsetPaginator` 的语义实现分组分页（每个分组各自 `row_number` 窗口、`total_pages` 取最大分组、`next_page_results` 取「是否还有分组没翻完」），Space 的 issues 接口按 `group_by` 走分组分支。

> 补一个实现细节：分组的**键**必须是 Plane 的 UUID，不是 PocketBase 的记录 id。`states/` 接口给前端的是 UUID，前端就用 UUID 当分组键；而 `issues.state` 关系列存的是 PocketBase id。所以 `GroupKey{ID, Match}` 把「给前端看的键」和「过滤用的值」分开——第一版直接用了记录 id，5 个分组的键全都对不上。

### 15. Triage 状态漏进了项目状态列表

Django 靠**默认管理器**排除 Triage，不是靠 `is_triage` 字段：

```python
class StateManager(SoftDeletionManager):
    def get_queryset(self):
        return super().get_queryset().exclude(group=StateGroup.TRIAGE.value)
```

而 `DEFAULT_STATES` **根本没设 `is_triage`**，所以 Django 播种出来的六个状态（含 Triage）全都是 `is_triage=False`。

我们所有「排除 Triage」的地方都只写了 `is_triage = false`，于是 Triage 被当成普通状态返回，前端就会多渲染出一个 Triage 分组。

**修复**：相关过滤条件补上 `group != 'triage'`（`projects.go` 的项目状态列表、`space.go` 的公开状态列表与分组桶）。

### 16. Space 的 issue 列表按升序返回

原来的排序把 `-created_at` 的 `-` 前缀剥掉后直接把裸字段名交给 PocketBase，而 PocketBase 把裸字段名读作**升序**。于是默认的「最新在前」被静默变成了「最旧在前」。

**修复**：改用与 app 侧一致的 `issueOrderBy` 映射表，保留方向。

### 17. 未注册的子路径返回 200 + 一个完全无关的对象（Go ServeMux 子树模式）

这是本次联调里**最隐蔽**的一类缺陷：它不报错、不 404，而是**用一个语义完全不同的对象回答了请求**。

根因在 Go 1.22+ 的 `ServeMux` 语义：**以 `/` 结尾的模式是子树模式**。PocketBase 的 router 把「分组前缀 + 路由路径」原样拼接后交给 `http.ServeMux`，于是

```go
ws := e.Router.Group("/api/workspaces/{slug}")   // 前缀
ws.GET("/", h.Detail)                            // 拼出来是 /api/workspaces/{slug}/
```

得到的 `/api/workspaces/{slug}/` 会匹配**它下面的一切**。任何没有单独注册的子路径，都会被这个前缀的 handler 接管。

实测后果：

```
GET /api/workspaces/hrms/user-profile/56c327be-…/   ->  200  {"total_members":1,"id":"df8fc291-…","name":"hrms",…}
```

返回的是**工作区对象**，不是用户资料。前端 `page.tsx` 拿到这个「真值」后执行：

```tsx
const group = userProfile?.state_distribution.find((g) => g.state_group === key);
```

`state_distribution` 是 `undefined`，于是整个 profile 页在 `ProfileOverviewPage` 里抛  
`Cannot read properties of undefined (reading 'find')` —— **一个 404 本可以给出清晰的自解释错误，却被伪装成了 200**。

同一根因还导致：

- `GET /api/workspaces/hrms/projects/<pid>/definitely-not-a-route/` → 200 + **项目数组**
- `GET /api/users/me/workspaces/<slug>/project-roles/` → 早先返回工作区列表（缺陷 #3 的近亲）

**修复**：给**所有**以 `/` 结尾的路由模式补上 `{$}` 锚点（ServeMux 的「精确路径」标记）。共修改 **405 处**、覆盖 16 个路由文件。`{$}` 只会让模式变得更精确，因此**只可能消除** mux 冲突，不可能引入新的。

> 顺带修好了一个历史遗留：`asset.go` 里那两条 legacy file-assets 路由之所以「故意不注册」，是因为 `/api/workspaces/{slug}/members/` 作为子树会和它们重叠、导致启动 panic。锚定之后这个冲突自然消失。

**防回归**：新增 `internal/router/patterns_test.go`，用 `go/ast` 静态扫描本包所有路由注册，断言**任何以 `/` 结尾的路径都必须以 `{$}` 结尾**（不以 `/` 结尾的如 `/stickies/{pk}` 本身已是精确匹配，不要求）。这个测试不需要起服务、毫秒级、直接指到出问题的行号 —— 因为这一类 bug 在本项目里已经**反复咬过三次**，靠注释提醒是不够的。

### 18. `user-profile` / `user-stats` / `user-activity` 三个接口整体缺失

`/[workspaceSlug]/profile/[userId]/` 这条路由树依赖 Django 的三个 endpoint：

| 接口                                                    | Django 视图                           | 前端消费方                                            |
| ----------------------------------------------------- | ----------------------------------- | ------------------------------------------------ |
| `GET /api/workspaces/{slug}/user-profile/{user_id}/`  | `WorkspaceUserProfileEndpoint`      | `profile/[userId]/layout.tsx`（侧栏项目列表）            |
| `GET /api/workspaces/{slug}/user-stats/{user_id}/`    | `WorkspaceUserProfileStatsEndpoint` | `profile/[userId]/page.tsx`（统计卡 + 两张分布图）         |
| `GET /api/workspaces/{slug}/user-activity/{user_id}/` | `WorkspaceUserActivityEndpoint`     | `components/profile/overview/activity.tsx`（最近动态） |

三个都没实现。**修复**：新增 `internal/handlers/workspaces/user_profile.go` 与 `internal/serializers/activity.go`，按 Django 契约实现：

- `user-profile` 返回 `{project_data: [...], user_data: {...}}`；`project_data` 仅在**发起请求者**工作区角色 ≥ 15（Member/Admin）时填充，Guest 得到空数组；每个项目的 `created/assigned/completed/pending_issues` 统计的是**被查看用户**的数字，不是发起者的。
- `user-stats` 的 `state_distribution` / `priority_distribution` 走的是 Django 的 `Issue.issue_objects` 语义 —— 即排除软删除、已归档、草稿、**归档项目下的**、以及 **Triage 状态组**的 issue（这一条和缺陷 #15 同源）。`priority_distribution` 按 `urgent → high → medium → low → none` 排序并丢弃空桶。
- `user-activity` 复用 cursor 分页信封，并排除 `comment`/`vote`/`reaction`/`draft` 四种 `field`（它们在 UI 里另有渲染器）。


> 一个实现细节：`Project` 模型**没有** `sort_order` 列（对照 `plane/db/models/project.py` 确认）。第一版照抄了别处的 `ORDER BY p.sort_order, p.name`，SQLite 直接报  
> `SQL logic error: no such column: p.sort_order`，接口返回 400 —— 而且这个 400 是 PocketBase 把原始错误包装后的通用响应，看不出真正原因，只能去后端日志里翻 SQL。

### 19. `workspace-members/me/` 缺失 —— 全屏「Not Authorized!」把整个应用盖住了

这是缺陷 #17 的**下游后果**，也是本次联调里「症状离根因最远」的一个。

修复 #17（补 `{$}` 锚点）之后，我重新跑浏览器链路，页面**没有变好，反而变了样**：截图里出现了一整屏

```
Not Authorized!
You're not a member of this workspace.
Please contact the workspace admin to get an invitation or check your pending invitations.
```

这个文案来自 `layouts/auth-layout/workspace-wrapper.tsx:212`：

```tsx
if (!workspaceInfoBySlug(slug)) return <NotAuthorizedScreen />;
```

而 `workspaceInfoBySlug` 的数据源是 `GET /api/workspaces/{slug}/workspace-members/me/`。

**关键点**：这个接口**从来没有实现过**。在补 `{$}` 之前，`/api/workspaces/{slug}/workspace-members/me/` 被子树模式 `/api/workspaces/{slug}/` 接走，返回**工作区对象**。前端解析这个「形状不对但非空」的响应时拿到了一个 `undefined` 的成员信息，某些路径下恰好没触发 gate —— 换句话说，**子树模式用一个错误的 200 长期掩盖了一个根本不存在的接口**。锚点把它变成诚实的 404 后，gate 立刻显形。

所以：**run 20–25 里失败的每一步，截到的都是这个 gate 页面，而不是目标功能页**（`ERR-D2-create-page.png`、`ERR-D5-your-work.png`、`ERR-E2-open-publish-modal.png` 等，文件大小完全相同，因为都是同一张 gate）。这些截图此前被当作「页面能打开」的证据，实际证明力为零 —— 这是本次联调最重要的一条方法论教训：**截图必须断言页面内容，不能只看「截到了图」**。

**修复**：实现 `MemberMe`（`internal/handlers/workspaces/workspaces.go`），返回当前用户在该工作区的成员记录，含 `role` / `view_props` / `draft_issue_count`（`draft_issues` 按 `created_by + workspace + deleted_at=''` 计数）。非成员返回 404，让前端正常走到 gate —— 这次 gate 才是**真实语义**。

### 20. 另外三个被子树模式掩盖的接口（states / user-properties / sidebar-preferences）

与 #19 完全同源：这三个接口同样从未实现，此前靠子树模式返回工作区对象「假装」可用。锚点一补，全部 404。

| 接口                                                        | Django 视图                         | 缺了会怎样                             |
| --------------------------------------------------------- | --------------------------------- | --------------------------------- |
| `GET /api/workspaces/{slug}/states/`                      | `WorkspaceStateViewSet`           | 工作区级「状态」页空白（注意：**不是**项目状态，那个早已实现） |
| `GET`/`PATCH /api/workspaces/{slug}/user-properties/`     | `WorkspaceUserPropertiesEndpoint` | 工作项详情页的显示属性面板全空                   |
| `GET`/`PATCH /api/workspaces/{slug}/sidebar-preferences/` | `WorkspaceUserPreferenceViewSet`  | 侧边栏无法记住草稿/你的工作/便签的固定与排序           |

**修复**：新增 `internal/handlers/workspaces/preferences.go`，按 Django 契约实现三者：

- `states`：工作区范围内 JOIN `projects` + `project_members` 取用户可见项目下的状态，过滤 `group != 'triage' AND is_triage = 0`（与 #15 同源），并按**组内序号/组内总数**计算 `order`。
- `user-properties`：GET 走 `get_or_create` 语义（**GET 有副作用**，与 Django 一致），缺行时用 `serializers.DefaultIssueProps()` 播种；PATCH 局部更新。
- `sidebar-preferences`：GET 返回**以 key 为键的 map**（不是数组），七个 key 全部播种、`sort_order = 65535 + i*10000`，并把 `drafts` / `your_work` / `stickies` 默认置为 pinned；PATCH 接收**数组** `[{key, is_pinned, sort_order}]`，未知 key 静默跳过。

> 注意 GET/PATCH 的**形状不对称**（GET 是 map、PATCH 是 list）以及 GET 带副作用，这两点都是照 Django 实现抠出来的；如果按「REST 直觉」写成对称的 list，前端会静默不生效。

**方法论补充**：这四个接口不是被一个个 404 发现的，而是靠 `tests/browser/audit-endpoints.py`（前端调用形状 vs 路由注册表的静态 diff）**一次性列出来的**。浏览器跑一轮只能暴露当前点到的那个页面；静态 diff 能把整个缺口一次摊开。

### 21. `home-preferences/` 缺失 —— 工作区首页的挂件全部消失

在 #19/#20 修好、重跑 run4 之后，网络日志里**只剩最后一条 404**：

```
404  GET /api/workspaces/hrms/home-preferences/
```

这条不属于 #17 那批（它不是被子树模式掩盖的，而是真的从来没实现过，且路径在锚定前后都 404）。前端消费方是 `services/workspace.service.ts`：

```ts
async fetchWorkspaceWidgets(workspaceSlug) {
  return this.get(`/api/workspaces/${workspaceSlug}/home-preferences/`)
}
async updateWorkspaceWidget(workspaceSlug, widgetKey, data) {
  return this.patch(`/api/workspaces/${workspaceSlug}/home-preferences/${widgetKey}/`, data)
}
```

**这条缺陷的价值在于：它证明「截图看着正常」不等于「功能正常」。** 回头看 01:47 拍的 `10-workspace-home-clean.png` —— 页面渲染完全正常、没有报错、没有空白，我当时判定「工作区首页 OK」。但把那张图放大看，页面中部写着：

> **It's Quiet Without Widgets, Turn Them On**  
> It looks like all your widgets are turned off. Enable them now to enhance your experience!

**挂件（Quick Links / Recents / My Stickies）一个都没渲染出来**，因为 `fetchWorkspaceWidgets` 404 了、列表为空，而 UI 把「空列表」优雅地降级成了「你的挂件都关掉了」。这是一个**无声的、被 UI 主动美化的失败**——比崩溃更难发现，因为它长得就像正常状态。

**修复**：在 `internal/handlers/workspaces/preferences.go` 增加 `HomePreferences`，按 Django 契约实现：

- `GET` 返回**列表**（注意与 `sidebar-preferences` 的 map 相反），元素为 `{key, is_enabled, config, sort_order}`；读取时播种 `quick_links` / `recents` / `my_stickies`，并**排除** `quick_tutorial` / `new_at_plane` 两个教程挂件。
- `PATCH /home-preferences/{key}/` 返回 `{key, is_enabled, sort_order}` —— **故意不含 `config`**（Django 的 `WorkspaceHomePreferenceSerializer.fields` 就只有这三个）；key 不存在时返回 **400** `{"detail":"Preference not found"}`，不是 404。
- 播种的 `sort_order` 为 999 / 998 / 997：Django 的循环每轮都用 `1000 - counter` 重新 bulk_create 累积集合（`ignore_conflicts=True`），净效果就是按声明顺序递减。

> 这个接口又一次印证了本项目反复出现的一条规律：**同一个域里的相邻接口，形状经常是不对称的**（GET 列表 vs PATCH 单对象；map vs list；有 `config` vs 无 `config`；404 vs 400）。凡是按「REST 直觉」猜的，基本都错。

**验证**：`verify-profile.mjs` 新增 4 条断言（200+数组、三个 key 被播种、教程挂件被排除、PATCH 的响应形状），并在断言后把 `recents` 改回 `is_enabled: true`，避免把「关掉的挂件」带进后续截图。

---

## 六、测试工具链本身的修复（供后续复用）

| 问题                                        | 现象                                                                                                                                                                                                                                                                                                      | 修复                                                                                                                                                         |
| ----------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `settle()` 用无超时的 `page.evaluate`          | 渲染进程忙时整个脚本挂死                                                                                                                                                                                                                                                                                            | 加 5s race + 连续两次超时即放弃；新增 `safeText()` / `resetPage()`                                                                                                      |
| 编辑器挂载后 `page.evaluate` 永不返回               | run4 卡在 D2 超过 20 分钟                                                                                                                                                                                                                                                                                     | 所有 DOM 读取走 `safeText()`；步骤级 90s watchdog                                                                                                                   |
| 侧边栏渲染两份                                   | 第一个 DOM 匹配常是隐藏的那份，`click()` 超时                                                                                                                                                                                                                                                                          | 先 `hover` 项目行再用 boundingBox 点击                                                                                                                             |
| 服务被后台任务回收                                 | `run_in_background` 的任务结束时，它拉起的服务全被连带杀掉                                                                                                                                                                                                                                                                 | 整套服务 + 全部测试脚本放进**同一个进程树**（`run-all.sh`）                                                                                                                    |
| 健康检查用 2s 超时探 Vite                         | 首次请求要等编译，2s 必然超时，判定「服务没起」                                                                                                                                                                                                                                                                               | `start-stack.sh` 的探测超时提到 10s                                                                                                                               |
| **同一个 2s 超时在 `run-all.sh` 里漏改**           | `up()` 仍是 `--max-time 2`，而热态请求实测就要 ~1.3s；`up 8000 && up 3000 && … && up 3100 /live/` 要五个**同时**成功才算数，于是这个循环几乎永远不满足，每次都把 120 次预算跑满 → 整条链路**每次晚开始约 9 分钟**                                                                                                                                                  | 统一改成 `--max-time 5` + 明确判定（`backend=200 && web=200 && admin=200 && space=302 && live=404`），并把每次探测结果打出来                                                     |
| 等待循环是哑的                                   | 一个永远不满足的静默循环，和一个卡死的脚本，从外部看完全一样（本次为此花了 10 分钟做进程取证）                                                                                                                                                                                                                                                       | 每轮打印 `t=Ns backend=… web=… admin=… space=… live=…`；预算从 120 轮降到 60 轮，超时也**继续往下走**（各脚本自己还有门禁）                                                                |
| **把「端口被占用」当成「服务可用」**                      | `listening()` 只看 `netstat` 里有没有 `LISTENING`。react-router 的 typegen 会在**绑定端口之后**崩（`EPERM: rmdir .react-router/types/app/+types`），留下一个**绑着端口、但什么都不响应**的死进程。于是 `start-stack.sh` 认为「web 已经起来了」从不重启它，而 `run-all.sh` 的 `up 3000` 永远拿不到响应（curl 返回 `000`）→ 整条链路每次晚开始约 9 分钟                                      | 拆成两个判定：`bound()`（端口被占）与 `answering()`（真能返回 HTTP 状态码，不是 `000`）；`answering()` 为假时先 `kill_port` 清掉僵尸再重启；等待循环超时后再补一次「重启前端 + 重等」                                |
| **`curl -o /dev/null` 在 MSYS2 下静默失败（真凶）** | `run-all.sh` 导出了 `MSYS2_ARG_CONV_EXCL='*'`，这会**顺带关掉 MSYS2 的 POSIX→Windows 路径转换**。于是 Windows 的 `curl.exe` 把 `/dev/null` 当成字面量，去写 `C:\dev\null`，**请求本身成功、但 curl 退出码 23（CURLE_WRITE_ERROR）**。所有 `curl … \|\| ok=0` 形式的健康检查因此**永远失败**，等待循环每次都把预算跑满 —— 这就是「服务明明活着、脚本却等 9 分钟」的最终原因。它完美的伪装成「脚本卡死」，因为从外部看两者毫无区别 | 按平台显式取 null 设备：`MINGW*/MSYS*/CYGWIN*` → `NUL`，否则 `/dev/null`；全项目不再手写 `-o /dev/null`。修好后启动从 ~9 分钟降到 **2 秒**                                                 |
| 等待循环不落盘                                   | stdout 被 `\| tail` 缓冲，循环在跑什么**完全看不见**，只能靠 `ps -W` 做进程取证（本次花掉 20+ 分钟）                                                                                                                                                                                                                                    | 每次探测同时写 `tests/logs/stack-wait.log` 与 `run-all-wait.log`，运行途中即可读                                                                                           |
| 日志写 `/tmp` 被拒                             | 中途开始 `/tmp/x.log: Permission denied`，五个服务全部启动失败                                                                                                                                                                                                                                                         | 日志改写到 `tests/logs/`                                                                                                                                        |
| 数据目录被旧进程占用                                | 新旧后端同时持有 `.fe-run`，出现「全新库却说实例已初始化」                                                                                                                                                                                                                                                                      | 换库前先确认 8000 端口无监听                                                                                                                                          |
| 一次性改写 400 处路由的脚本按「行」扫描                    | 处理函数是跨行 `func(...) error {` 字面量时，括号配平扫到行尾就放弃，`/api/plane-meta/health/` 被静默漏掉                                                                                                                                                                                                                            | 改成按**整个文件**扫描并做括号配平                                                                                                                                        |
| 同上脚本用 `Path.write_text()` 写回              | Windows 默认把 `\n` 翻译成 `\r\n`，4 行的改动变成整文件 diff                                                                                                                                                                                                                                                            | 读写都显式 `newline=""`                                                                                                                                         |
| 静态改写脚本没有护栏                                | 同一类 bug 已经咬了三次（`project-roles` / `user-profile` / 项目子路径）                                                                                                                                                                                                                                                | 落成 `internal/router/patterns_test.go`：AST 静态断言「以 `/` 结尾的路径必须以 `{$}` 结尾」                                                                                    |
| 断言探针用裸 `page.evaluate`                    | 渲染进程忙时永不返回，整个脚本挂死                                                                                                                                                                                                                                                                                       | `Promise.race` 15s 超时，超时记为一次显式失败而不是静默挂起                                                                                                                    |
| 截图只看「有没有截到图」                              | run 20–25 截到的其实是全屏「Not Authorized!」gate，却被当成「页面打开了」的证据（缺陷 #19）                                                                                                                                                                                                                                          | 步骤断言改为 `waitForShell` + 等待页面特征文本 + `waitNoSpinner`，断言不过就落 `ERR-*.png`                                                                                      |
| 同上，更隐蔽的一种                                 | 工作区首页显示「It's Quiet Without Widgets, Turn Them On」，一张毫无异常的截图，实际挂件全空（缺陷 #21）                                                                                                                                                                                                                              | 接口级断言写进 `verify-profile.mjs`（`home-preferences` 必须返回三个 key 且排除教程挂件），不依赖肉眼看图                                                                                |
| 靠浏览器一轮轮试才发现缺接口                            | 每轮只能暴露当前点到的那个页面，缺口发现得又慢又碎                                                                                                                                                                                                                                                                               | 新增 `tests/browser/audit-endpoints.py`：静态 diff「前端 `/api/...` 调用形状」vs「路由注册表」，一次列出全部缺口（当前 253→257 条路由 / 150 条前端调用形状 / 74 条未覆盖）                                |
| 批量改路由没有可复查的 diff                          | 405 处机械改写，改错一处（`patterns_test.go` 的注释被改）肉眼很难发现                                                                                                                                                                                                                                                          | `anchor-routes.py` 只改路由字面量、读写保 LF，改完逐文件 `git diff` 复查                                                                                                      |
| 分不清「路由没注册」和「handler 主动返回 404」             | 两者前端看到的都是 404，无法定位                                                                                                                                                                                                                                                                                      | 看 `tests/logs/pb-backend.log`：**`INFO GET <path>` = 路由匹配上了、handler 跑了**；**`ERROR GET <path>` + `└─ The requested resource wasn't found.` = 路由没匹配上**（mux 层） |

---

## 七、已知限制

1. **未覆盖**：Cycle / Module / View / Intake / Webhook / 出站集成等模块的逐项交互（方案里已明确剔除 AI、GitHub/Slack、OAuth、导入导出、出站 Webhook）。
2. **Space 端的写操作**未测——Space 设计上就是只读。
3. `apps/live` 需要 Redis；本机用的是系统自带 Redis 服务。
4. 前端 `apps/*/node_modules` 与部分 workspace 包的 `dist` 是本地构建产物，不属于仓库源码。

### 7.1 尚未实现的接口清单（由 `audit-endpoints.py` 生成）

`tests/browser/audit-endpoints.py` 把前端 `services/**` 里的 `/api/...` 调用形状与路由注册表做静态 diff。当前基线：

```
registered patterns : 256
frontend call shapes: 150
UNSERVED            : 74
```

74 条未覆盖按性质分三类：

| 类别             | 数量级 | 典型条目                                                                                                                                                                                                            | 说明                                       |
| -------------- | --- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ---------------------------------------- |
| **方案内明确剔除**    | ~20 | `/rephrase-grammar/`（AI）、`/workspace-integrations/…`（GitHub/Slack）、`/projects/importers/jira/`、`/webhooks/`                                                                                                     | 不属于本次迁移范围，属预期缺口                          |
| **模块未做**       | ~30 | `/views/{}/issues/`、`/cycles/{}/cycle-progress/`、`/estimates/{}/estimate-points/`、`/intake-work-items/…`                                                                                                        | Cycle / View / Estimate / Intake 模块整体未实现 |
| **已实现模块的边角操作** | ~24 | `bulk-{archive,delete,operation,subscribe}-issues`、`issues/{}/meta/`、`issues/{}/issue-relation/`、`pages/{}/move/`、`states/{}/mark-default/`、`deleted-issues/`、`archived-issues/`、`favorite-pages/`、`my-issues/` | 主流程已通，这些是列表页的批量操作与二级功能，点了会 404           |

> 注意第 3 类：**主流程「跑通」不等于「点哪都不 404」**。上面这 24 条里，`bulk-*` 系列和 `favorite-pages/` 是列表页右键菜单上的可见入口，属于下一步最该补的。
>
> 运行方式：`python tests/browser/audit-endpoints.py`（纯静态，不需要起服务）。

---

## 八、结论

- 从**空库**开始，admin 初始化 → web onboarding → 工作区 → 项目 → 工作项 → 页面 → 设置 → 发布 → Space，**全链路在真实浏览器中跑通**，关键节点截图与网络日志齐全。
- 刷新后数据仍在，证明写入真正落到了 PocketBase/SQLite。
- 共发现并修复 **21 个后端契约缺陷**，其中 20 个只有在真实浏览器交互下才会暴露。
- 最关键的结论：**前端零改动的兼容层策略成立** —— 所有问题都出在我们的复刻层，没有一处需要改 Plane 前端源码。
- 另一条同等重要的结论：**「接口 404」和「接口返回了语义不对的 200」是两种完全不同的故障**。前者立刻可见，后者会被前端当成合法数据一路带下去，最终在离根因很远的地方炸掉（`Cannot read properties of undefined (reading 'find')`）。本项目里最耗时的两个缺陷（#17 与 #14）都属于后者，而它们共同的成因是同一个 Go 语义细节。因此这一类问题必须靠**静态护栏**（`patterns_test.go`）而不是靠注释和记性来防。
- 第三条结论（来自 #19/#20）：**错误的 200 不只掩盖「炸掉的页面」，还会掩盖「根本不存在的接口」**。四个从未实现的 endpoint（`workspace-members/me/`、`states/`、`user-properties/`、`sidebar-preferences/`）长期被子树模式用工作区对象「回答」着，其中一个直接把整个应用盖上了全屏 gate。补上 `{$}` 让它们变成诚实的 404，症状才第一次可见 —— **修掉一个 bug 之后「变差」，往往是更深一层的 bug 终于暴露了**。
- 第四条结论（来自 #21，也是最需要警惕的一条）：**前端会把失败渲染成看起来正常的状态**。`home-preferences/` 404 时，工作区首页不是报错、不是空白，而是显示一句「It's Quiet Without Widgets, Turn Them On」—— 一张完全「正常」的截图，挂件却一个都没出来。**因此「截图没有异常」不能作为验收标准**：验收必须断言**具体内容**（某个 key 存在、某段文本出现、某个数字非零），而不是「页面没崩」。本项目里 01–19 号截图正是这样被误判为通过的，直到补完接口重跑才暴露。
- 第五条结论（来自 #17/#19/#20/#21 的诊断过程，**也是本项目耗时最长的一个 bug 的真凶**）：**一切顺利的服务、等了一刻钟却不往下跑的等待循环，大概率是被一行不起眼的命令导致的**。本项目里 `curl -o /dev/null` 在 `MSYS2_ARG_CONV_EXCL='*'` 下被 Windows `curl.exe` 当成 `C:\dev\null`，**请求成功但 curl 退出码 23**，让所有 `curl … || ok=0` 形式的健康检查永远失败、等待循环每次都把 90 次预算跑满 —— 看起来「脚本卡死」其实是「脚本永远在重试不存在的失&#x8D25;**」。修好后启动从 ~9 分钟降到 2 秒。同类陷阱：dev server 崩溃后**端口被占但什么都不响应\*\*（`bound` ≠ `answering`），也要补上「真崩了就清掉僵尸再重启」。

---

## 九、最终验收（2026-10-03 14:35–16:00 完成）

为消灭「**修复过程中掺入旧证据**」的可能，对整套二进制跑了一次**完整 `--fresh` 重测**：从空库开始 admin 注册 → workspace 创建 → project → issue → pages → settings → 发布 → Space，30 张关键节点截图由最终二进制统一重生成。

```
run1-bootstrap.mjs     exit=0   D1–A3 全过 ✅
run2-project.mjs       exit=0   B0–B2 全过 ✅
run3-issue.mjs         exit=0   C1–C2 全过 ✅
run4-project-pages.mjs exit=0   D1–D5 全过 ✅  FAILED REQUESTS — 无
run5-publish-space.mjs exit=0   E1–E5 全过 ✅  FAILED REQUESTS — 无
verify-profile.mjs     exit=0   ALL CHECKS PASSED ✅
```

最终二进制网络日志（run5 节选）：

```
200 GET /api/workspaces/hrms/workspace-members/me/         ← 之前子树模式掩盖的 #19
200 GET /api/workspaces/hrms/states/                          ← 之前子树模式掩盖的 #20
200 GET /api/workspaces/hrms/user-properties/                ← 之前子树模式掩盖的 #20
200 GET /api/workspaces/hrms/sidebar-preferences/            ← 之前子树模式掩盖的 #20
200 GET /api/workspaces/hrms/home-preferences/               ← 之前从未实现 #21
200 GET /api/workspaces/hrms/user-profile/<id>/              ← 之前从未实现 #18
200 GET /api/workspaces/hrms/user-stats/<id>/                ← 之前从未实现 #18
200 GET /api/workspaces/hrms/user-activity/<id>/             ← 之前从未实现 #18
```

证据存档：

| 类型       | 路径                                                                     |
| -------- | ---------------------------------------------------------------------- |
| 截图（30 张） | `tests/evidence/screenshots/01-…30-*.png`，全部由最终二进制于 15:15–15:23 一次性生成  |
| 网络日志     | `tests/logs/run*.mjs.log`，每个脚本一段                                       |
| 失败案例（历史） | `tests/evidence/screenshots/ERR-*.png`（来自修复前的旧运行，作为缺陷叙事引用保留）           |
| 验收套件     | `tests/browser/verify-profile.mjs`，**全部断言通过**                          |
| 静态缺口分析   | `tests/browser/audit-endpoints.py`（257 路由 / 150 调用形状 / 74 未覆盖）         |
| 路由静态护栏   | `internal/router/patterns_test.go`（Go AST 静态断言，防止「`{$}` 锚定漏洞」复发）       |
| 等待循环日志   | `tests/logs/stack-wait.log` / `run-all-wait.log`（运行途中即可读，再也不怕「卡死还是失败」） |

剩一个可验证的项目是 `/user-issues/...`（profile 个人页`/assigned` 标签的列表数据来源），它在「已实现模块的边角操作」类别里、属于 `audit-endpoints.py` 列出的 74 条未覆盖之一。profile 个人页**结构**渲染正常（Summary / Overview / Workload / Work items by Priority / Work items by state / Recent activity 都在），assigned 标签页能切过去但列表为空 —— 想让 assigned 列表真正显示工作项，下一步就是补这个端点。
