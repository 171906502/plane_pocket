# 01 · 架构与设计决策

> 读完本文你应该能回答：一个浏览器请求进来到返回 JSON，中间经过了什么；
> 数据是怎么存的；哪些设计是**刻意**这样做的、为什么。

---

## 1. 项目定位

Plane 是一个项目管理系统（类似 Linear/Jira）。它的官方架构是三层：

```
Plane 前端 (React, apps/web + apps/space + apps/admin)
        ↓ HTTP REST
Plane 后端 (Django + DRF + PostgreSQL + Celery + Redis)
```

本项目的目标是把**中间那层**换掉：

```
Plane 前端 (apps/web + apps/space + apps/admin)   ← 一行不改
        ↓ HTTP REST（契约完全一致）
plane-pocketbase (Go 单二进制 + PocketBase + SQLite)   ← 我们写的
```

**为什么值得做**：官方 Plane 后端需要 Postgres + Redis + Celery + 若干 Python 依赖，
部署和运维成本高。换成一个 Go 二进制 + 一个 SQLite 文件后，部署退化成"拷贝一个文件"。

**核心承诺（也是唯一验收标准）**：前端源码零改动。所有适配都发生在我们这一层。

---

## 2. 技术栈

| 层 | 技术 | 说明 |
|---|---|---|
| 语言 | Go **1.27+** | 硬性要求，由 `pocketbase-master/go.mod` 决定 |
| 框架 | PocketBase（**当库用**，不是当二进制用） | 提供 SQLite、认证、管理界面、迁移、HTTP 栈 |
| 数据库 | SQLite（PocketBase 内置，`modernc.org/sqlite` 纯 Go 实现，无 CGO） | |
| 路由 | Go 标准库 `net/http.ServeMux` | PocketBase 的路由器是它的一层薄封装，**不是 echo/gin** |
| 前端 | Plane v1.4.2 原样 | 只读依赖，不参与构建 |

---

## 3. 请求生命周期

这是理解整个代码库最重要的一张图。一个 `GET /api/workspaces/acme/issues/` 进来后：

```
1. PocketBase 内建中间件
   ├─ CORS（优先级 -1041）
   └─ loadAuthToken（从 Authorization 头解析 token → e.Auth）
        │
2. 我们注册的全局中间件（router.go，按注册顺序）
   ├─ middleware.Auth(app)      从 session-id / admin-session-id cookie 解析用户
   │                            只在 e.Auth 仍为空时才查 cookie（不覆盖 token 认证）
   ├─ middleware.CSRFProtect()  非安全方法做 double-submit 校验
   └─ Access-Control-Allow-Credentials 头注入（必须在 CORS 中间件之前跑）
        │
3. ServeMux 路由匹配
   └─ 匹配到 /api/workspaces/{slug}/issues/{$}
        │
4. 组中间件 middleware.ResolveWorkspace(app)   ← workspace 组的 BindFunc
   ├─ 从路径参数取 slug
   ├─ 查 workspaces 表（slug 匹配 且 deleted_at = ''）
   ├─ 查 workspace_members（workspace + member + is_active = true）拿角色
   └─ 角色为 0（无活跃成员身份）→ 直接 404（不是 403！）
        │
5. （项目级路由还会再跑）middleware.ResolveProject(app)
   ├─ 从 {project_id} 取 Plane UUID → FindByUUID 换成 PocketBase record
   ├─ 校验项目属于当前 workspace 且未删除
   └─ 无项目角色时，若 workspace 角色 ≥ Admin **且已是项目成员** → 提升为 Admin，否则 404
        │
6. （按需）middleware.RequireWorkspaceRole / RequireProjectRole
        │
7. handler（internal/handlers/<domain>/）
   ├─ 用 e.Request.URL.Query() 读参数
   ├─ 用 app.FindRecordsByFilter(...) 查数据
   └─ 调 serializers.XXX(record) 构造 Plane 期望的 JSON
        │
8. serializers.Paginate(...) → 包成 Plane 的游标分页信封
        │
9. e.JSON(200, envelope)
```

**关键点**：

- **认证不拒绝请求**。`middleware.Auth` 永远返回 `e.Next()`，是否要求登录由路由自己决定
  （Plane 有公开路由：登录页、公开看板、共享 Space）。
- **作用域解析失败返回 404 而不是 403**。这是 Plane 的刻意设计：不泄漏私有 workspace 的存在性。
- **`ResolveProject` 的"workspace Admin 仍需是项目成员"这一条很重要** —— 工作区 Admin
  不会自动获得项目角色。这是 Plane 的真实语义，很多 bug 源于假设了它会。

---

## 4. 数据模型：UUID 双主键策略（最需要理解的一处设计）

### 问题

- Plane 用 **36 位 UUID**（`550e8400-e29b-41d4-a716-446655440000`）作为所有实体的主键，
  前端代码里到处硬编码这个假设。
- PocketBase 原生主键是 **15 位随机字符串**（`a1b2c3d4e5f6g7h`），且 PocketBase 内部
  （`_externalAuths`、`_authOrigins`、关联字段）强依赖它。

### 方案

**两者并存，各司其职**：

```
PocketBase record
├── id      (15 位, PocketBase 原生主键)  ← PocketBase 内部关系用它
└── uuid    (36 位文本, required + 唯一索引, 字段位序 1)  ← Plane 前端看到的 ID
```

序列化层做双向映射：

| 方向 | 做法 |
|---|---|
| 出（响应） | `serializers` 输出 `record.GetString("uuid")`，前端拿到 Plane UUID |
| 入（路径参数） | `serializers.FindByUUID(app, collection, uuid)` 用 `uuid = {:uuid}` 查 |
| 入（宽容） | `FindByUUIDOrID` 先试 uuid，再试 PocketBase id |

**特例**（不遵循 `uuid` 列名）：

| 集合 | 用的列 |
|---|---|
| `project_identifiers` | `legacy_pk` |
| `sessions` | `session_key` |

> 权威说明见 `spec/schema-spec.md` §2。**不要自创第三种映射方式**。

### 为什么不用"直接把 PocketBase id 当 UUID"或反之

已评估并否决的替代方案记录在 `spec/schema-spec.md` §2 里。简言之：改 PocketBase 内部主键格式
会破坏它的内建集合（`_externalAuths` 等硬编码了 `collectionId` 语义），而改前端则违反核心承诺。

---

## 5. 目录职责（每个目录"只干一件事"）

```
plane-pocketbase/
├── main.go                入口。只做三件事：装配 app、注册迁移命令、注册路由
│
├── internal/router/       ★ 路由注册。每个 Plane 域一个文件
│   ├── router.go             总装配 + 全局中间件 + meta 路由
│   ├── workspace.go          /api/workspaces/*
│   ├── project.go            /api/workspaces/{slug}/projects/*
│   ├── issue.go              工作项
│   ├── cycle.go / module.go / page.go / intake.go / notification.go
│   ├── view.go / asset.go / search.go / admin.go / publicapi.go / users.go / auth.go
│   └── patterns_test.go      ★ AST 静态护栏：断言所有路由都被 {$} 锚定
│
├── internal/handlers/<domain>/   业务逻辑。目录名与 router 文件一一对应
│
├── internal/serializers/   响应形状。这是"Plane JSON 契约"的唯一定义处
│   ├── pagination.go         ★ 游标分页信封（Envelope / GroupedEnvelope）
│   ├── plane.go              UUID 生成、时间、可空字段的工具函数
│   ├── records.go            FindByUUID / FindByUUIDOrID
│   ├── errors.go             统一的错误响应形状
│   └── <domain>.go           各域的对象 → JSON
│
├── internal/middleware/    请求上下文
│   ├── auth.go              会话 cookie、CSRF、登录/登出辅助
│   ├── scope.go             ★ Scope 解析（workspace/project/角色）、角色守卫
│   └── apikey.go            X-Api-Key（api/v1 那套）
│
├── pb_migrations/          PocketBase collection 定义（12 个迁移，按 wave 组织）
│
└── spec/                   契约冻结文档（权威，改代码前先读）
```

**纪律**：加一个新域 = 新建 `router/<domain>.go` + `handlers/<domain>/` + `serializers/<domain>.go`。
不要去改别的域的文件（`patterns_test.go` 例外，它是全局护栏）。

---

## 6. 路由注册的约定（最容易出错的地方）

### 6.1 一切都要 `{$}` 锚定

```go
ws := e.Router.Group("/api/workspaces/{slug}")

ws.GET("/{$}", h.Detail)                    // ✅ 精确匹配 /api/workspaces/{slug}/
ws.GET("/members/{$}", h.Members)           // ✅ 精确匹配 .../members/
ws.GET("/", h.Detail)                       // ❌ 子树模式！吞掉所有子路径

// 路径本身不以 / 结尾的已经是精确匹配，不需要锚点
ws.GET("/stickies/{pk}", h.Sticky)          // ✅ 已经是精确匹配
```

**为什么这是铁律**：未锚定的模式会把未注册的子路径用 `200 + 一个语义无关的对象` 回答掉。
前端拿到一个形状不对但状态码是 200 的响应，会把它当合法数据一路带到渲染树深处，
然后在离根因很远的地方炸掉（典型症状：`Cannot read properties of undefined (reading 'find')`）。

更糟的是它会**掩盖从未实现的接口** —— 本项目里 4 个接口（`workspace-members/me/`、`states/`、
`user-properties/`、`sidebar-preferences/`）长期"工作正常"，只因为子树模式一直在拿工作区对象回答它们。

### 6.2 护栏是测试，不是注释

`internal/router/patterns_test.go` 用 `go/ast` 解析整个 router 包，
断言每一个以 `/` 结尾的路由路径字面量也以 `{$}` 结尾。

```bash
# 改动路由后必须跑
cd plane-pocketbase && go test ./internal/router/ -run TestRoutePatternsAreAnchored
```

它不需要起服务、毫秒级完成，并且报错会精确指出违规的那一行。
**这个测试失败 = 有人引入了静默 200，绝不允许合并。**

### 6.3 路由顺序不重要，但冲突会 panic

`ServeMux` 按**特异性**排序，不按注册顺序（这点和 Django 的有序 URL 列表不同）。
但两个互相重叠、又没有谁更具体的子树模式会在**注册时 panic**（服务起不来）。
`{$}` 只会让模式更具体，所以加锚点能**消除**冲突，永远不会**制造**冲突。

从 Django 移植路由时特别注意：一个"Django 里靠写在前面才生效"的路由，
在 ServeMux 里必须改成不重叠的。

### 6.4 错误用法对照

| 症状 | 原因 |
|---|---|
| `undefined: e.Param` / `e.QueryParam` / `e.Cookie` | PocketBase 用的不是 echo。见下表 |
| 服务启动时 panic 说模式冲突 | 两个重叠的子树模式，加 `{$}` |
| 未注册的路径返回 200 + 错误对象 | 忘了 `{$}` |

**PocketBase 路由 API 速查**（因为不是 echo，这是最常写错的地方）：

| 想要 | 用 |
|---|---|
| 路径参数 | `e.Request.PathValue("id")` |
| 查询参数 | `e.Request.URL.Query().Get("per_page")` |
| 表单值 | `e.Request.FormValue("field")` |
| Cookie | `e.Request.Cookie("name")` |
| 解析 JSON body | `e.BindBody(&dst)` |
| 返回 JSON | `e.JSON(status, v)` |
| 返回空 | `e.NoContent(status)` |
| 设 cookie | `e.SetCookie(&http.Cookie{...})` |
| 请求级存储 | `e.Set(key, val)` / `e.Get(key)` |
| 组类型 | `*router.RouterGroup[*core.RequestEvent]`（包 `pocketbase/tools/router`） |

---

## 7. 契约层：序列化器

### 7.1 分页信封

Plane 的 `BasePaginator` 返回的响应体，字段名和**顺序**都是承重的：

```json
{
  "grouped_by": null,
  "sub_grouped_by": null,
  "total_count": 42,
  "next_cursor": "100:1:0",
  "prev_cursor": "100:-1:1",
  "next_page_results": false,
  "prev_page_results": false,
  "count": 42,
  "total_pages": 1,
  "total_results": 42,
  "extra_stats": null,
  "results": [ ... ]
}
```

易错点：

- **`count` ≠ `total_results`**。`count` 是**本页**条数，`total_results` 是**总数**。
- **游标格式是 `"value:offset:is_prev"`**，其中 `offset` 是**页号**（不是行偏移），
  `value` 是每页大小。`1000.0` 要渲染成 `"1000"`（整数形式），保持字节兼容。
- **`per_page` 上限 1000**（`MaxPerPage`），超出是**钳制**而不是报错（对齐 Django 行为）。
- **探测下一页的方式**是查 `per_page + 1` 条然后截断，对齐 Django 的 `stop = offset + limit + 1`。

### 7.2 分组信封（`GroupedEnvelope`）

当请求带 `group_by_field_name` 时，`results` 从数组变成**对象**：

```json
"results": {
  "<group-uuid>": { "results": [...], "total_results": 12 },
  "<group-uuid2>": { "results": [], "total_results": 0 }
}
```

三个反直觉的点：

1. **空分组也必须出现**。Django 的 `GroupedOffsetPaginator.__get_field_dict()` 会先把整个分组
   列表预置好，再往里填行。
2. **`total_pages` 是 `ceil(最大分组大小 / limit)`**，不是 `ceil(总数 / limit)` —— 分组分页是**每个分组独立翻页**。
3. **分组键 vs 匹配值不是一回事**。前端看到的键是 **Plane UUID**，而底层列存的是
   **PocketBase 15 位 id**。`GroupKey{ID, Match}` 就是为了这个差异存在的。

### 7.3 可空字段的处理

PocketBase **除 `json` 外不支持 NULL**，而 Django/Postgres 到处是 `null=True`。
所以序列化器里有几个专门函数：

| 函数 | 用途 |
|---|---|
| `serializers.Text(r, f)` | 非空文本列，`""` 就是 `""` |
| `serializers.NullableText(r, f)` | 可空文本列，`""` 要投影回 `null`（前端会区分 `null` 和 `""`，例如 `emoji`、`logo`） |
| `serializers.TextOr(r, f, fallback)` | Django 里 `default=` 的列永不 null，空值要回退到默认值 |

`TextOr` 存在的原因很实际：Plane 前端会把这些值**直接喂给平台 API**，例如
`Intl.DateTimeFormat({ timeZone: user_timezone })`，传 `null` 会抛异常。

---

## 8. 认证与安全

### 8.1 会话载体

| 项 | 值 | 说明 |
|---|---|---|
| 会话 cookie 名 | `session-id` | 必须与 Plane 的 `SESSION_COOKIE_NAME` 完全一致 |
| 管理端 cookie 名 | `admin-session-id` | 实例管理台（`/god-mode/`） |
| CSRF cookie 名 | `csrftoken` | |
| CSRF 请求头 | `X-CSRFTOKEN` | XHR 用 |
| CSRF 表单字段 | `csrfmiddlewaretoken` | 原生表单 POST（登录/注册/登出）用 |
| Cookie 有效期 | 604800s（7 天） | 对齐 Plane 的 `SESSION_COOKIE_AGE` |

**cookie 里装的是 PocketBase auth token**，不是自建 session 表。这样直接复用 PocketBase 的
JWT 签发/过期/密钥失效机制，不用手写会话存储。

`HttpOnly = true`（Plane 的前端从不读这个 cookie，靠浏览器自动携带）；
`SameSite = Lax`（保住顶层表单 POST 登录，同时挡掉跨站 XHR）。

### 8.2 CORS（一个必须理解的顺序问题）

Plane 前端是**跨域**调用 API 的（web `:3000` → API `:8000`），且 axios 配了 `withCredentials: true`。
所以每个响应（**包括 preflight**）都必须带 `Access-Control-Allow-Credentials: true`。

**为什么我们手动注入这个头**：PocketBase 内建的 CORS 中间件（优先级 `-1041`）
从不设置这个头，而且在 preflight 时直接返回 204、**不会调用后续任何中间件**。
所以必须在它**之前**跑（优先级 `-1042`）。

允许的 origin 由 PocketBase 内建中间件从 `serve --origins=...` 回显，
**只支持精确匹配** —— 浏览器在带凭证模式下会拒绝 `*` 通配 origin。

**`--origins` 是必填项**。不传的话 CORS 会回 `Access-Control-Allow-Origin: *`，
浏览器拒绝，前端直接落到 "Plane didn't start up correctly" 错误边界，**且日志里一个 API 调用都没有**。

### 8.3 CSRF

标准的 double-submit 校验：cookie 里的 `csrftoken` 必须等于请求头/表单字段里的值。

| 行为 | 说明 |
|---|---|
| 安全方法免检 | `GET` / `HEAD` / `OPTIONS` / `TRACE` |
| 无 cookie 时放行 | 首次登录还没有 cookie，没东西可比；此时仍受 CORS origin 检查 |
| 调试开关 | `PLANE_PB_CSRF=off` 环境变量可整体关闭 |

### 8.4 权限模型（两级独立角色轴）

```
WorkspaceMember.role   ∈ {Admin 20, Member 15, Guest 5}
ProjectMember.role     ∈ {Admin 20, Member 15, Guest 5}
```

**两条轴完全独立**。工作区 Admin **不自动**拥有任何项目角色。
`ResolveProject` 里的逻辑是：

```go
if scope.ProjectRole == 0 {
    if scope.WorkspaceRole >= RoleAdmin && scope.ProjectMember != nil {
        scope.ProjectRole = RoleAdmin
    } else {
        return 404   // 注意：404，不是 403
    }
}
```

那个 `&& scope.ProjectMember != nil` 是关键 —— 工作区 Admin 必须**同时**是项目成员。

**Guest 的例外**：Plane 在若干端点上**先判断"是不是创建者"，再判断角色**，
所以 Guest 能编辑/删除自己创建的评论、Cycle、Module。
镜像这些端点时必须先调 `middleware.IsCreator(e, record)` 再调 `RequireProjectRole`。

> 完整逐端点守卫表见 `spec/permissions.md`。

---

## 9. 数据访问

PocketBase 的 API（都在 `core.App` 上）：

```go
app.FindRecordById(collection, id)
app.FindFirstRecordByFilter("posts", "slug = {:slug}", dbx.Params{"slug": s})  // 未命中返回 sql.ErrNoRows
app.FindRecordsByFilter("posts", filter, sort, limit, offset, params...)
app.CountRecords(collection, dbx.NewExp(filter, params))
app.Save(record); app.Delete(record)
record.GetString/GetInt/GetBool/GetDateTime/GetStringSlice(field)
```

过滤语法：`field = {:param} && other = true`；空值比较用 `field = ''`。

### 为什么 `countMatching` 用 `FindRecordsByFilter` 而不是 `SELECT count(*)`

`serializers/pagination.go` 里的 `countMatching` 故意用 `FindRecordsByFilter(..., limit=0, offset=0)`
再取 `len()`，而不是手写 `SELECT count(*)`。原因：**计数必须走 PocketBase 的过滤器解析器**，
它理解 `&&`/`||` 和关系遍历（如 `state.group`）。手写 SQL 的 COUNT 更省，但会丢失解析器语义，
而**线协议兼容性比性能重要**。

### PocketBase 数据层的限制（会直接影响你的建模）

| 限制 | 后果 |
|---|---|
| 除 `json` 外**不支持 NULL** | 可空字段靠 `""` 表示，序列化层负责投影回 `null` |
| **没有字段级 default** | 默认值只能在写入路径里手工填 |
| `autodate` 字段**会阻止 `Set()`** | 不能手动设置自动时间戳字段 |
| **软删除级联要自己实现** | PocketBase 不知道 Plane 的 `deleted_at` 语义 |
| 原生主键是 15 位字符串 | 见 §4 的 UUID 双主键策略 |

---

## 10. 设计决策记录（ADR）

| # | 决策 | 理由 | 否决的替代方案 |
|---|---|---|---|
| D1 | 兼容层复刻 Plane API，前端零改动 | 改前端 = 维护永不合并的 fork，且要跟上游赛跑 | 给前端加 adapter 层、fork 前端 |
| D2 | 三端全要（web + space + admin） | space 是"发布"流程的终点；admin 是实例初始化入口 | 只做 web |
| D3 | **全新空库，不迁 Postgres 数据** | 迁移期数据一致性成本远高于价值；目标是可部署的新实例 | 写数据迁移脚本 |
| D4 | 去掉 OAuth，只留邮箱密码 + Magic Link | 减少外部依赖与配置面 | 保留全部登录方式 |
| D5 | 保留 `apps/live`（Yjs 协作编辑） | **不是可选项**：编辑器连不上 live 会退回 `usePageFallback`，其 effect 每次 description 变化都重跑，导致 PATCH/GET 无限循环卡死渲染 | 砍掉 rich-text 协作 |
| D6 | PocketBase 原生 id 与 Plane UUID 并存 | 见 §4 | 改 PocketBase 主键 / 改前端 |
| D7 | 所有路由 `{$}` 锚定 + AST 护栏 | 子树模式的静默 200 是本项目最高成本的 bug 类 | 靠注释和记性 |
| D8 | PocketBase 当**库**用而非当二进制用 | 需要自定义 HTTP 表面，PocketBase 的默认 API 形状与 Plane 不同 | 用 PocketBase 二进制 + 反向代理改写 |
| D9 | 剔除 AI / GitHub / Slack / OAuth / 导入导出 / 出站 Webhook；保留 Deploy Board / Analytics / 通知 / 附件 / 搜索 | 降低依赖面，剔除的都是需要外部凭据或出站网络的 | 全功能移植 |

> D5 特别值得一提：这不是"顺手保留"，而是**发现编辑器有死循环后才知道它必须有**。
> 详见 `tests/WORKFLOW_TEST_REPORT.md` 缺陷 #10。

---

## 11. 横向一致性

几个跨域必须保持一致的约定：

| 约定 | 说明 |
|---|---|
| 软删除 | Plane 用 `deleted_at = ''` 表示未删除。**所有查询都要带上这个条件** |
| 时间格式 | ISO 8601 UTC。工具函数在 `serializers/time.go` 与 `serializers/plane.go` |
| ID 生成 | `serializers.NewUUID()` 生成 RFC 4122 v4 UUID，每次插入都要填 `uuid` 列 |
| 错误形状 | 统一走 `serializers/errors.go`（`NotFound` / `Forbidden` / `BadRequest`） |
| 404 vs 403 | **越权访问一律 404**，不泄漏资源存在性 |

---

**下一篇**：本地搭建请见 [`02-GETTING-STARTED.md`](02-GETTING-STARTED.md)；
要动手改代码见 [`03-ITERATION-GUIDE.md`](03-ITERATION-GUIDE.md)。
