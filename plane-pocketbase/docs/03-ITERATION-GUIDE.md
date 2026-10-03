# 03 · 迭代指南

> 目标：让你（或另一个 agent）在**不破坏契约**的前提下加功能、改 schema、升依赖。
> 这个项目最容易出的问题不是"写不出来"，而是"写了但形状不对"——前端不报错，只是默默地渲染错东西。

---

## 0. 三条不可违反的纪律

| # | 纪律 | 违反的后果 |
|---|---|---|
| **D-1** | **绝不修改 `plane-preview/`** | 违反"前端零改动"承诺，从此要跟上游赛跑 |
| **D-2** | **所有路由必须 `{$}` 锚定** | 未注册的子路径会返回 `200 + 错对象`，静默污染前端状态 |
| **D-3** | **响应形状以 `spec/` 为准，不凭记忆** | 形状错了前端不报错，只是渲染出貌似正常的东西 |

---

## 1. 标准流程：新增一个接口

假设要补 `/api/workspaces/{slug}/user-issues/{user_id}/`（真实待办，见 `07-STATE-AND-ROADMAP.md`）。

### 步骤 1 — 先读契约（不要跳）

```bash
cd plane_pocket/plane-pocketbase

# 1a. 这个端点在 Django 里长什么样
grep -n "user-issues" plane-preview/../plane-preview/apps/api/plane/app/urls/workspace.py
# → path("user-issues/<uuid:user_id>/", WorkspaceUserProfileIssuesEndpoint.as_view(), ...)

# 1b. 视图类做了什么（过滤条件、分页、权限）
# 读 plane-preview/apps/api/plane/app/views/workspace/user.py

# 1c. API spec 里的记录（请求/响应形状、是否被前端调用）
grep -n -A20 "user-issues" spec/api-spec.yaml
```

**必须搞清楚的五件事**：

1. **权限**：需要什么角色？（查 `spec/permissions.md`）
2. **响应形状**：是数组？是分页信封？是分组信封？（看视图基类和 serializer）
3. **过滤语义**：有哪些查询参数会影响结果？（`order_by` / `sub_issue` / `assignees` …）
4. **失败码**：子资源缺失时是 404 还是 400？（**相邻接口的形状经常不对称，不要猜**）
5. **前端怎么调**：`plane-preview/packages/services/src/**` 搜路径

> ⚠️ **不要根据隔壁接口的形状外推**。本项目实测到的真实不对称：
>
> | 接口 | GET 返回 | PATCH 返回 |
> |---|---|---|
> | `sidebar-preferences/` | **map**（以 key 为键） | 收 **list** 的 `{key,...}` |
> | `home-preferences/` | **list**，元素含 `config` | **object**，**不含** `config` |
> | `user-properties/` | object（**GET 有副作用**：会 `get_or_create`） | object |
>
> 而且 `home-preferences/{key}/` 找不到 key 时返回 **400** `{"detail": "Preference not found"}`，**不是 404**。

### 步骤 2 — 写 serializer（契约层，先写这个）

在 `internal/serializers/<domain>.go` 里加一个函数，把 PocketBase record 转成 Plane 期望的对象。

```go
// internal/serializers/issues.go
func Issue(r *core.Record) map[string]any {
    return map[string]any{
        "id":           r.GetString("uuid"),      // ← 注意：输出的是 uuid 不是 id
        "name":         r.GetString("name"),
        "state":        r.GetString("uuid"),      // 关系字段也要映射
        "target_date":  NullableText(r, "target_date"),
        // ...
    }
}
```

**要点**：

- `"id"` 输出的是 **`uuid` 列**的值（Plane UUID），不是 PocketBase 的 `id`。见 `01-ARCHITECTURE.md` §4。
- 可空字段用 `NullableText`（`""` → `null`）；有 Django `default=` 的用 `TextOr`。
- 关系字段输出的是**目标记录的 Plane UUID**，不是 PocketBase id。

### 步骤 3 — 写 handler

在 `internal/handlers/<domain>/` 里加函数。

```go
func (h *Handler) UserIssues(e *core.RequestEvent) error {
    scope := middleware.ScopeOf(e)
    if scope == nil { return serializers.NotFound(e, "") }

    userID := e.Request.PathValue("user_id")

    // 1. 路径参数是 Plane UUID → 查 PocketBase record
    target, err := serializers.FindByUUID(h.app, "users", userID)
    if err != nil || target == nil { return serializers.NotFound(e, "") }

    // 2. 读查询参数（Plane 的参数名，不是我们发明的）
    perPage, cursor := serializers.ListParams(e, 100)
    q := e.Request.URL.Query()

    // 3. 构造 PocketBase 过滤器
    filter := "workspace = {:workspace} && deleted_at = ''"
    params := dbx.Params{"workspace": scope.Workspace.Id}

    // 4. 分页
    env, err := serializers.Paginate(h.app, serializers.ListQuery{
        Collection: "issues",
        Filter:     filter,
        Params:     params,
        Sort:       orderBy(q.Get("order_by")),   // 默认 "-created_at"
        PerPage:    perPage,
        Cursor:     cursor,
    }, serializers.Issue)
    if err != nil { return serializers.BadRequest(e, err.Error()) }

    return e.JSON(http.StatusOK, env)
}
```

**要点**：

- 参数名（`per_page` / `cursor` / `order_by` / …）必须**逐字对齐 Plane**，不能自创。
- 默认排序从 Django 视图里抄（常见 `-created_at`）。
- 软删除条件 `deleted_at = ''` 几乎每个查询都要带。

### 步骤 4 — 注册路由（带 `{$}`）

在 `internal/router/<domain>.go`：

```go
ws.GET("/user-issues/{user_id}/{$}", h.UserIssues)
//                      ^^^^ 必须！否则会吞掉所有子路径
```

### 步骤 5 — 跑护栏 + 编译

```bash
go test ./internal/router/ -run TestRoutePatternsAreAnchored   # 护栏
go build -o .fe.exe .                                          # 编译
```

护栏失败会精确告诉你哪一行漏了 `{$}`。

### 步骤 6 — 实测（**编译通过不算完成**）

```bash
cd tests/browser
bash run-one.sh verify-profile.mjs        # 或你新写的验证脚本
```

然后在 `tests/logs/pb-backend.log` 里确认：

```
INFO GET /api/workspaces/hrms/user-issues/<uuid>/     ← 路由匹配上了
```

如果是 `ERROR` + `The requested resource wasn't found.`，说明路由没注册上（十有八九是锚点或路径拼写）。

### 步骤 7 — 断言具体内容，不是"页面没崩"

**这是本项目最贵的教训**。前端会把失败渲染成看起来完全正常的状态：

- `home-preferences/` 404 时，首页显示一句很友好的 **"It's Quiet Without Widgets, Turn Them On"** ——
  一张毫无异常的截图，挂件却一个都没出来。
- `workspace-members/me/` 缺失时，前端渲染全屏 "Not Authorized!" 盖住整个应用 ——
  看起来像权限问题，实际是接口没实现。

所以验证必须是：

```js
// ✅ 断言具体内容
check("user-issues returns a cursor envelope", res.status === 200 && "results" in res.json);
check("user-issues excludes triage-state issues", res.json.results.every(
    (i) => i.state_group !== "triage"
));
check("user-issues honours sub_issue=false", res.json.results.every((i) => i.parent === null));

// ❌ 毫无价值的验证
check("page loaded", screenshot !== null);
```

### 步骤 8 — 更新文档

- `docs/07-STATE-AND-ROADMAP.md` 勾掉这一项
- 重跑 `python tests/browser/audit-endpoints.py` 看未覆盖数下降
- 如果有新的坑，追加到 `docs/06-PITFALLS.md`

---

## 2. 标准流程：修改 / 新增 collection

### 步骤 1 — 先读 spec

```bash
grep -n "### MODEL: <模型名>" spec/schema-spec.md
```

`spec/schema-spec.md` 是 Django 模型 → PocketBase collection/field 的**权威映射**。

### 步骤 2 — 读 PocketBase 的真实字段 API（**不要猜**）

```bash
# 确认字段类型有哪些选项
cat ../pocketbase-master/core/field_text.go
cat ../pocketbase-master/core/field_relation.go
```

**实测过的坑**：`TextField` 用的是 `Max` 和 `Pattern`，**不是** `MaxLength`。

### 步骤 3 — 新增迁移文件（不要改已有的）

```
pb_migrations/1759000013_<描述>.go
```

文件名格式：`<时间戳>_<描述>.go`，时间戳递增。

```go
package pb_migrations

import (
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

func init() {
	m.Register(func(app core.App) error {      // up
		c := core.NewBaseCollection("my_thing")
		c.Fields.Add(&core.TextField{Name: "name", Required: true, Max: 255})
		// uuid 字段必须落在位序 1
		c.Fields.AddAt(1, &core.TextField{Name: "uuid", Required: true})
		c.AddIndex("idx_my_thing_uuid", true, "uuid", "")
		return app.Save(c)
	}, func(app core.App) error {              // down
		c, err := app.FindCollectionByNameOrId("my_thing")
		if err != nil { return err }
		return app.Delete(c)
	})
}
```

**迁移的三个硬性要求**：

| 要求 | 原因 |
|---|---|
| 每个 collection 必须有 `uuid` 列（required + 唯一索引 + **位序 1**） | UUID 双主键策略，见 `01-ARCHITECTURE.md` §4 |
| 时间戳必须递增，**不要修改已发布的迁移** | 已应用的迁移再改不会生效（PocketBase 记录了已应用版本） |
| `pb_migrations` 包必须有 blank import | 否则 `init()` 注册的迁移进不了 `core.AppMigrations`，`migrate` 报 `No new migrations to apply` |

> **`uuid` 为什么必须用 `AddAt(1, ...)`**：`core.NewAuthCollection` 会**前插系统字段**，
> 用 `Add` 追加会落到错误位置。

### 步骤 4 — 验证迁移真的生效

```bash
go build -o .fe.exe .
./.fe.exe migrate up --dir=./.tmp-verify
# exit 0

# 直接读 SQLite 确认 collection 和字段真的存在
python -c "import sqlite3; c=sqlite3.connect('.tmp-verify/data.db'); \
print([r[0] for r in c.execute(\"select name from _collections\")])"
```

**编译通过 ≠ 迁移正确**。必须落盘验证。

---

## 3. 标准流程：升级 Plane 前端版本

前端是只读依赖，升级 = 换一棵树。

1. **备份旧树**（不删除）
   ```bash
   cd plane_pocket
   mv plane-preview "plane-preview.bak-$(date +%s)"
   ```

2. 取新版本源码放到 `plane-preview/`

3. **重新生成 `.env`**（新版本可能改了变量名）
   ```bash
   cd plane-preview
   for a in web space admin live; do
     diff apps/$a/.env.example apps/$a/.env 2>/dev/null || cp apps/$a/.env.example apps/$a/.env
   done
   ```

4. **重新装依赖**（`packageManager` 版本可能变了 —— 读 `package.json` 里的声明）

5. **重建 packages 的 dist**
   ```bash
   pnpm install && pnpm build
   ```

6. **跑全套验收**（关键步骤）
   ```bash
   cd plane-pocketbase/tests/browser
   bash run-all.sh --fresh        # 从空库全链路重跑
   python audit-endpoints.py      # 新的接口缺口清单
   ```

7. 对照 `audit-endpoints.py` 的输出评估新版前端新增了哪些调用

**预期**：升级会导致一批新 404，因为新版本可能调用了新接口。这些是**真实缺口清单**，不是回归。

---

## 4. 标准流程：升级 PocketBase

1. 备份 `pocketbase-master/`（同上，`mv` 不删）
2. 放新版本源码
3. **先读 `go.mod` 的 `go 1.x` 要求** —— 可能要求更高的 Go 版本
4. ```bash
   cd plane-pocketbase
   go mod tidy          # 依赖变化
   go build -o .fe.exe .
   ```
5. **重跑迁移自检** —— PocketBase 升级可能改变字段 API：
   ```bash
   ./.fe.exe migrate up --dir=./.tmp-verify
   ```
6. 跑全套验收

**注意**：`go.mod` 里是 `replace github.com/pocketbase/pocketbase => ../pocketbase-master`，
所以升级就是换那个目录的内容，不需要改 `go.mod` 的版本号。

---

## 5. 验证阶梯（从便宜到贵，逐步升级）

**不要跳过前面的层级直接跑最贵的那个。**

| 层级 | 手段 | 耗时 | 能发现什么 |
|---|---|---|---|
| 1 | `go build` | 秒 | 语法/类型错误 |
| 2 | `go test ./internal/router/` | 毫秒 | 路由锚点漏洞 |
| 3 | `.fe.exe migrate up --dir=.tmp-verify` + 读 SQLite | 秒 | collection/字段定义错误 |
| 4 | `curl` 打单个接口 | 秒 | 路由是否注册、响应形状 |
| 5 | 看 `pb-backend.log` | 秒 | 区分路由层 404 和处理器层 404 |
| 6 | `python audit-endpoints.py` | 秒 | 全局接口缺口（静态，不用起服务） |
| 7 | `bash run-one.sh <script>.mjs` | 分钟 | 单页真实浏览器交互 |
| 8 | `bash run-all.sh --fresh` | 十分钟级 | 全链路回归 + 30 张截图 |

**层级 4 的正确做法**（注意 `-o NUL`）：

```bash
# 拿一个有效 session cookie 后
curl -s -b "$COOKIE" -o NUL -w '%{http_code}\n' \
  'http://127.0.0.1:8000/api/workspaces/hrms/states/'
```

---

## 6. 什么时候该重跑全套

| 改动 | 最小验证 |
|---|---|
| 加一个 handler | `go build` + `run-one.sh` + 断言脚本 |
| 加/改路由 | `go test ./internal/router/` + `run-one.sh` |
| 改 serializer 形状 | `run-one.sh` + **具体内容断言** + 受影响页面的截图 |
| 加 collection | 迁移自检 + `run-all.sh --fresh` |
| 改认证/CSRF/CORS | `run-all.sh --fresh`（认证问题会污染所有后续步骤） |
| 升级前端 / PocketBase | `run-all.sh --fresh` + `audit-endpoints.py` |

---

## 7. 加一个新区块（完整清单）

新增一个 Plane 域（比如完整做完 Cycle 模块）时，改动面是：

```
□ spec/api-spec.yaml                     ← 对照检查，不修改（只读参考）
□ internal/router/<domain>.go            ← 新建，注册所有路由（全部 {$} 锚定）
□ internal/handlers/<domain>/*.go        ← 新建，业务逻辑
□ internal/serializers/<domain>.go       ← 新建（或已有则追加），响应形状
□ internal/router/router.go              ← 在 registrars 表里加一行
□ pb_migrations/<ts>_*.go                ← 若需要新 collection
□ docs/07-STATE-AND-ROADMAP.md           ← 勾掉已完成项
```

`router.go` 里的注册表长这样，加一行即可：

```go
registrars := []struct {
    name string
    fn   func(*core.ServeEvent, core.App) error
}{
    {"cycle", RegisterCycle},
    {"module", RegisterModule},
    // ...
    {"yourdomain", RegisterYourDomain},   // ← 加这里
}
```

参考 **`internal/router/cycle.go`（69 行）** 作为最小完整模板 —— 它是所有域文件里最简洁的一个。

---

## 8. 明确不要做的事

| ❌ 不要 | 为什么 | 应该 |
|---|---|---|
| 改 `plane-preview/` 里的任何文件 | 破坏核心承诺 | 改我们的兼容层 |
| 写不带 `{$}` 的路由 | 静默 200 污染前端 | 加 `{$}`，跑护栏 |
| 修改已发布的迁移文件 | 不会被重新应用 | 新建迁移 |
| 凭记忆写响应形状 | 形状错了前端不报错 | 读 `spec/` + Django serializer |
| 根据隔壁接口外推形状 | 相邻接口经常不对称 | 逐个查视图 |
| 手写 `SELECT count(*)` 做分页计数 | 丢失 PocketBase 过滤器语义 | 用 `serializers.Paginate` |
| 用 `rm` 删文件来重置 | 违反项目约定、丢证据 | `mv X X.bak-$(date +%s)` + `mkdir` |
| 只用 `go build` 成功就宣布完成 | 编译≠行为正确 | 跑起来 + 断言内容 |
| 用截图"没报错"当验收 | 前端会渲染貌似正常的失败态 | 断言具体 key/文本/计数 |
| 直接在 `plane_pocket/` 里 `git init` | 会把三棵树搞乱 | 仓库已存在，直接用 |

---

## 9. 给其他 agent 的交接要点

如果你是接手的 agent，开工前请：

1. **读 `docs/06-PITFALLS.md`** —— 这里的坑每一个都花了数小时，别再踩一遍
2. **读 `docs/07-STATE-AND-ROADMAP.md`** —— 知道哪些是已知缺口、哪些是故意不做的
3. **先跑 `python tests/browser/audit-endpoints.py`** —— 建立"当前缺口"的真实认知（不用起服务）
4. **改路由后必跑 `go test ./internal/router/`**
5. **改完必跑 `run-one.sh` 或 `run-all.sh`** —— 并断言内容
6. **遵守文件操作纪律**：`mv` 备份，不 `rm`
7. **不确定响应形状时，去读 Django 源码**（`plane-preview/apps/api/plane/app/views/**`），不要猜

---

**下一篇**：部署上线见 [`04-DEPLOYMENT.md`](04-DEPLOYMENT.md)；
测试与证据规范见 [`05-TESTING.md`](05-TESTING.md)。
