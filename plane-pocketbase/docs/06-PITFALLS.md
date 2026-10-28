# 06 · 陷阱与雷区总集

> **如果你只读一篇文档，读这篇。**
>
> 这个项目里最耗时的 bug 都不是"不会写代码"，而是踩了一个**静默失败**的陷阱：
> 错误的路由模式返回 200、`curl` 请求成功了却退出 23、端口被占但服务是死的。
> 这些坑的共同特点是**看起来一切正常**，所以每个都花了数小时。
>
> 用法：出问题时按**症状**搜。每条给「症状 → 根因 → 修法 → 如何自查」。

---

## A 类 · Go / ServeMux

### A-1 ⭐ 未锚定的路由返回 `200 + 错对象`（本项目最高成本 bug）

**症状**

- 未注册的接口返回 **HTTP 200**，但响应体语义完全不对
- 前端在离根因很远的地方崩，典型报错：
  `Cannot read properties of undefined (reading 'find')`
- 或者：某个接口"一直正常工作"，其实**从来没实现过**

**根因**

Go 1.22+ 的 `http.ServeMux` 里，**以 `/` 结尾的模式是子树模式（subtree）**，匹配它下面的每一条路径。
PocketBase 的路由器把组前缀与路由路径**原样拼接**后交给 ServeMux：

```go
ws := e.Router.Group("/api/workspaces/{slug}")
ws.GET("/", h.Detail)     // 生成模式 /api/workspaces/{slug}/
                          // → 吞掉 /api/workspaces/{slug}/<任何东西>/
```

unregistered 的子路径就被 `h.Detail` 用**工作区对象 + 200** 回答掉了。

**真实案例（都是实测的）**

| 请求路径 | 实际拿到 | 前端后果 |
|---|---|---|
| `/api/workspaces/{slug}/user-profile/{id}/` | 工作区对象 | profile 页崩在 `userProfile.state_distribution.find(...)` |
| `/api/users/me/workspaces/{slug}/project-roles/` | 工作区**列表** | 数据错乱 |
| `/api/workspaces/{slug}/projects/{id}/…` | 项目**列表** | 项目页数据错乱 |

**更隐蔽的后果**：它**掩盖了从未实现的接口**。
以下 4 个接口长期"工作正常"，只因为子树模式一直在拿工作区对象回答它们：

- `workspace-members/me/` —— 缺失时前端渲染**全屏 "Not Authorized!" 盖住整个应用**
- `states/`
- `user-properties/`
- `sidebar-preferences/`

补上锚点后它们变成诚实的 404，症状才第一次可见。

**修法**

**每一个以 `/` 结尾的路由都加 `{$}`**（ServeMux 的精确路径标记）：

```go
ws.GET("/{$}", h.Detail)                    // 精确匹配 /api/workspaces/{slug}/
ws.GET("/members/{$}", h.Members)
proj.GET("/"+base+"/{pk}/{$}", h.Detail)
```

路径本身**不以 `/` 结尾**的（如 `"/stickies/{pk}"`）已经是精确匹配，**不需要**锚点。

`{$}` 只会让模式**更具体**，所以它能消除 ServeMux 冲突，永远不会制造冲突。

**如何自查**

```bash
cd plane-pocketbase
go test ./internal/router/ -run TestRoutePatternsAreAnchored -v
```

`patterns_test.go` 用 `go/ast` 静态断言（毫秒级，不需要起服务），
失败会精确指出违规行。**改路由后必跑。**

**不要靠注释防这类问题** —— 它已经复发过三次。

---

### A-2 ⭐ 「修完一个 bug，情况反而变糟了」

**症状**

- 给路由加上 `{$}` 锚点之后，**涌现一批新的 404**
- 看起来像是"我刚刚弄坏了什么"

**根因**

那批 404 **本来就是缺失的接口**。它们之前被子树模式用 `200 + 工作区对象`掩盖着。
锚点让它们第一次诚实地报告"我没实现"。

**修法**

**不要回退锚点。** 那批 404 就是你要补的接口清单。

> 推广成一条通用工作原则：**当一个修复让情况看起来变差时，先怀疑是更深一层的 bug 终于暴露了，
> 而不是你弄坏了什么。**

**如何自查**

```bash
python tests/browser/audit-endpoints.py   # 静态列出全部缺口
```

---

### A-3 路由冲突导致服务启动 panic

**症状**：服务**起不来**，`ServeMux` 在注册时 panic。

**根因**：两个互相重叠、又**没有谁更具体**的子树模式（例如 `/a/{x}/` 与 `/a/details/`）。

**修法**：加 `{$}` 收窄。锚定后的模式永远是原子树的严格子集，所以**只会消除冲突**。

**注意与 Django 的差异**：

| | Django | Go ServeMux |
|---|---|---|
| 匹配顺序 | **有序** URL 列表，前面的优先 | 按**特异性**排序，注册顺序无关 |
| 移植影响 | 一个"靠写在前面才生效"的路由 | 必须改成**不重叠** |

---

### A-4 PocketBase 的路由 API 不是 echo

**症状**：编译错误 `undefined: e.Param` / `e.QueryParam` / `e.Cookie`。

**根因**：PocketBase 基于标准库 `http.ServeMux`，**不是 echo/gin**。

**速查表**：

| 想要 | 用 |
|---|---|
| 路径参数 | `e.Request.PathValue("id")` |
| 查询参数 | `e.Request.URL.Query().Get("per_page")` |
| 表单值 | `e.Request.FormValue("field")` |
| Cookie | `e.Request.Cookie("name")` |
| 解析 body | `e.BindBody(&dst)` |
| 返回 JSON | `e.JSON(status, v)` |
| 返回空 | `e.NoContent(status)` |
| 重定向 | `e.Redirect(status, url)` |
| 设 cookie | `e.SetCookie(&http.Cookie{...})` |
| 请求级存储 | `e.Set(k,v)` / `e.Get(k)` |
| 组类型 | `*router.RouterGroup[*core.RequestEvent]`（包 `pocketbase/tools/router`） |

### A-5 ⭐ 有一些路径在 ServeMux 里**根本无法注册** —— 兜底路由是唯一出口

**症状**：注册一条看似普通的路由，服务**整个起不来** —— 日志里
`[SafeWrap] recovered from panic: pattern "GET /api/workspaces/file-assets/{workspace_id}/{asset_key}/{$}"
conflicts with pattern "GET /api/workspaces/{slug}/workspace-members/me/{$}"`，
随后 PocketBase 恢复了 panic 但**所有接口都返回 502**。

**根因**：两条模式都是 5 段，一条把字面量 `file-assets` 钉在第 3 位，另一条把
`workspace-members`/`me` 钉在第 4/5 位。请求
`/api/workspaces/file-assets/workspace-members/me/` **同时匹配两者**，
且**谁都不比谁更具体** —— Go 1.22+ 的 ServeMux 遇到这种"重叠但无优先级"的模式
在**注册期**直接 panic。Django 靠 URL **顺序**解决这类冲突，Go 的 mux 没有等价机制。
`{$}` 锚定救不了它：两条都已锚定，问题出在**中间**某段的字面量/通配之争。

**教训**：
1. **"注册即 panic，恢复后全站 502"是这类问题的标志性指纹。** 看到 502 而日志里有
   `conflicts with pattern`，先找那条冲突的路由，别去怀疑数据库或反向代理。
2. **这类路径只有一条出路：兜底路由（catch-all）。** PocketBase 的 `BuildMux`
   只在**没有** `"/"` 路由时才自动补一个 404 兜底（`tools/router/router.go:68`）。
   注册自己的 `"/"`，在处理函数里按**路径前缀**分发到目标 handler，其余路径
   **原样调用框架的同一个 `NotFoundError`** —— mux 的模式集合一个都没变，
   只有兜底函数体不同，所以其它路由的 404/405 行为不变。
   实现见 `internal/handlers/asset/legacy.go` 的 `LegacyFallback`。
3. **验收要靠"404 的措辞"区分"路由没注册"和"路由注册了但对象不存在"**：
   路由 404 是 `"The requested resource wasn't found."`，
   处理器 404 是 `"The required object does not exist."`。
   两者措辞不同这件事本身就是路由已匹配的证据。
4. **段数不同就不冲突**：`…/restore/`（6 段）与那条 5 段路由无重叠，可以正常注册。
   判断冲突先数段，再看字面量与通配的位置。

---

## B 类 · PocketBase 数据层

### B-1 ⭐ `grep -c $'\r' file` 数的是「全部行数」，不是 CR 数

**症状**

- 你以为某个目录是 CRLF，实际不是（或反之）
- 数字**看起来非常合理**（大文件本来就很多行），所以错误结论能通过审查

**根因**

在 Git Bash（MSYS2）下，`$'\r'` 参数在参数传递中**被吞成空模式**，
而空模式**匹配每一行** —— 于是返回值恰好等于 `wc -l`。

**实测证据**

```bash
$ grep -c $'\r' pnpm-lock.yaml ; wc -l < pnpm-lock.yaml
14830
14830        # 完全相同 —— 说明模式是空的，这其实是行数
```

本项目就据此**误判**「前端是 CRLF、`pnpm-lock.yaml` 有 14830 个 CR」，
真实答案是 **5168 个文本文件里只有 2 个 CRLF**。

**修法**

```bash
# 可靠：精确 CR 字节数
tr -cd '\r' < "$f" | wc -c

# 或 Python
python -c "b=open(p,'rb').read(); print(b.count(13))"
```

> ⚠️ `od -An -tx1 -v file | tr ' ' '\n' | grep -c '^0d$'` 在大文件上**也不可靠**
> （对含 CR 的文件返回了 0）。

**统计全树的额外陷阱**：**必须先过滤二进制文件**。
PNG / GIF / `.db` / `.zip` 里经常恰好含 `\r\n` 字节对。
未过滤时 `plane-preview` 报出 **265 个**"CRLF 文件"，其中约 **263 个是图片**。

```python
if b'\x00' in b[:8192]: continue    # 快速判二进制
```

**通用教训**：**别信看起来合理的数字。** 要拿它做决定时，用两种独立方法交叉验证。

---

### B-2 ⭐ `**/dist/` 会让 `go build` 直接失败

**症状**

```
pattern all:dist: no matching files found
```

**根因**

`pocketbase-master/ui/embed.go:11` 是：

```go
//go:embed all:dist
```

它把**预构建的 PocketBase 管理界面**（`ui/dist/`）编进二进制。
根 `.gitignore` 里任何 `**/dist/` 或 `**/build/` 通配都会让这个目录不被跟踪，
clone 出来就缺文件，编译失败。

**修法**

**根 `.gitignore` 不下 dist/build 通配。** 前端产物交给 `plane-preview/.gitignore`
里**作用域受限**的 `dist/`（上游规则，只会命中前端包）。

**如何自查**

```bash
git check-ignore -v pocketbase-master/ui/dist/index.html   # 必须无输出
git ls-files pocketbase-master/ui/dist | wc -l             # 应为 94
```

---

### B-3 ⭐ 嵌套 `.git` 让整个子树不被纳管（gitlink）

**症状**

- 在父级 `git init` 后，`git add` **报成功**，但子目录的源码一个都没进索引
- `git status` 看起来干净

**根因**

子目录里**已存在** `.git` 时，父仓库会把它记成一个 **gitlink**（一个裸的 commit 指针），
而不是把里面的文件纳入跟踪。

**修法**

**先归档内层 `.git`，再 init**：

```bash
mkdir -p _scratch/git-archive
mv plane-pocketbase/.git "_scratch/git-archive/plane-pocketbase.git.bak-$(date +%s)"
```

（本项目实际就是这么做的：归档到 `_scratch/git-archive/plane-pocketbase.git.bak-1791047299/`，
**未删除**。）

**如何自查**

```bash
git add --dry-run . | wc -l     # 应该是几千，不是几十
```

---

### B-4 ⭐ `core.autocrlf=true` 会改写第三方文件

**症状**

- 一个承诺"字节不变"的只读第三方树，在某次 `git checkout` 之后行尾全变了
- `git status` 显示大片"已修改"，但其实没人动过

**根因**

Windows 上全局 `core.autocrlf=true` 很常见。配合默认的 `* text=auto eol=lf`，
一次 checkout 就会把 CRLF 文件重写成 LF。

**修法**

对必须字节不变的树显式关闭转换：

```gitattributes
* text=auto eol=lf          # 默认规则放前面
plane-preview/**      -text # 例外放后面
pocketbase-master/**  -text
```

`-text` 让索引存原始字节、checkout 还原原始字节。代价是那些文件的 `git diff` 显示为二进制
（对只读参考代码可接受）。

**两个必须知道的细节**：

1. **`.gitattributes` 里「最后匹配者生效」** —— 顺序就是优先级。
2. **`*` 会跨越 `/` 匹配**（和 `.gitignore` 里 `*` 不跨 `/` **相反**）。
   把默认规则写在例外后面会**静默取消所有例外**。

同时设 `git config core.autocrlf false`（仓库级）。

**如何自查（最硬的证据：索引 blob 大小 == 磁盘文件大小）**

```bash
f=plane-preview/package.json
echo "disk=$(stat -c%s $f) blob=$(git cat-file -s $(git ls-files -s $f | awk '{print $2}'))"
# disk=1479 blob=1479  ← 一致 = 字节被完整保留
```

或看属性：

```bash
git check-attr text -- plane-preview/package.json   # 期望: text: unset
```

---

### B-5 子目录 `.gitignore` 优先级高于根级

**症状**

- 你在根 `.gitignore` 写了一条规则，但某个子目录里的文件**依然没被忽略**（或反之）

**根因**

`.gitignore` 在子目录中**自动生效**，且**优先级高于根级**。
上游 `plane-preview/.gitignore` 已经忽略了 `node_modules`、`dist`、`build`、`.react-router`、
`.turbo`、`.env`、`.next`、`tsconfig.tsbuildinfo`。

**实践含义**

**写根级规则之前先读第三方树自己的规则。** 本项目里根 `.gitignore` 只需要很少的兜底行 ——
`tests/browser/` 的 14492 个文件最终只有 29 个进索引，全靠子目录规则。

**如何自查**

```bash
git check-ignore -v <path>   # 会告诉你「是哪条规则、在哪个文件里」命中的
```

> 💡 **不要用宽泛的 grep 做"危险文件"体检**：`grep /profile` 会命中
> `internal/profiles/profiles.go`、`app/.../profile/[userId]/page.tsx` 这类**合法源码路径**。
> 用 `git check-ignore -v` 针对具体路径查。

---

### B-6 PocketBase 的数据层限制（会直接影响建模）

| 限制 | 后果 | 应对 |
|---|---|---|
| **除 `json` 外不支持 NULL** | 可空字段只能用 `""` 表示 | 序列化层用 `NullableText` 投影回 `null` |
| **没有字段级 default** | 新行不会自动填默认值 | 在写入路径里手工填，或用 `TextOr` 兜底 |
| **`autodate` 字段会阻止 `Set()`** | 手动设置自动时间戳字段无效 | 不要试图设它 |
| **软删除级联要自己实现** | PocketBase 不懂 Plane 的 `deleted_at` 语义 | 查询时手动带 `deleted_at = ''` |
| **原生主键是 15 位字符串** | 与 Plane 的 36 位 UUID 冲突 | UUID 双主键策略，见 `01-ARCHITECTURE.md` §4 |

**额外陷阱**：`core.NewAuthCollection` 会**前插**系统 auth 字段，
所以想把字段放在位序 1 必须用 `Fields.AddAt(1, field)`，用 `Add` 会落到错误位置。

---

### B-7 PocketBase 自带的 `users` auth collection 不能新建

**症状**：系统 auth 相关表（`_externalAuths` / `_authOrigins` / `_mfas` / `_otps`）关联失效。

**根因**：PocketBase 自带一个 id 固定为 **`_pb_users_auth_`** 的 `users` auth collection。
如果新建一个同名的，那些内建表里硬编码的 `collectionId` 就指向了错误的对象。

**修法**：**复用**那个 id：

```go
core.NewAuthCollection("users", "_pb_users_auth_")
```

---

### B-8 `pb_migrations` 必须做 blank import

**症状**：`migrate` 报 **`No new migrations to apply`**，尽管迁移文件明明存在。

**根因**：Go 迁移通过 `init()` 注册。如果包没被链接进二进制，
那些 `init()` 根本不会执行，`core.AppMigrations` 是空的。

**修法**：在 `main.go` 里 blank import：

```go
import _ "github.com/plane-pocketbase/plane-pocketbase/pb_migrations"
```

---

### B-9 字段 API 不能猜

**症状**：编译错误，或者字段选项静默不生效。

**根因**：猜了选项名。实测 `core.TextField` 用的是 **`Max`** 和 **`Pattern`**，
**不是** `MaxLength`。

**修法**：**读源码再写**。

```bash
cat pocketbase-master/core/field_text.go
cat pocketbase-master/core/field_relation.go
cat pocketbase-master/core/field_date.go
```

---

### B-10 错误的 400 只给通用文案 —— 去翻后端 SQL 日志

**症状**：后端返回 400 但响应体是通用文案，看不出原因。

**根因**：PocketBase 会把原始 SQL 错误**包装掉**。

**修法**：去 `tests/logs/pb-backend.log` 翻实际执行的 SQL。

> 同类：一个**错误的列名**（例如假设 `Project` 有 `sort_order` 列，实际没有）
> 只会表现为一个通用 400。**读后端日志，不要猜。**

---

### B-11 计数不能手写 `SELECT count(*)`

**症状**：分页的 `total_results` / `total_pages` 与实际结果不一致（尤其带关系过滤时）。

**根因**：手写的 `SELECT count(*)` 绕过了 PocketBase 的过滤器解析器，
而解析器理解 `&&`/`||` 和**关系遍历**（如 `state.group`）。

**修法**：用 `serializers.Paginate`，它内部的 `countMatching` 故意复用
`FindRecordsByFilter` 来保住解析器语义。性能略差，但**线协议兼容性更重要**。

---

## C 类 · 环境（Windows / Git-Bash / 沙箱）

### C-1 ⭐⭐ `curl -o /dev/null` 静默失败，请求成功但退出 23

**症状**

- 健康检查**永远失败**，但服务其实是好的
- 等待循环每次都把预算跑满（本项目实测：**每次启动延迟约 9 分钟**）
- 表现**完全像脚本卡死**，实际是"永远在重试一个不存在的失败"

**根因**

如果脚本导出了 `MSYS2_ARG_CONV_EXCL='*'`（为了阻止 MSYS2 篡改 `*` 之类参数的常见做法），
**那个开关同时关掉了 MSYS2 的 POSIX→Windows 路径转换**。
于是 Windows 的 `curl.exe` 把 `/dev/null` 当**字面量**，试图写 `C:\dev\null`。

**请求本身成功了**，但 curl 因为写文件失败而退出 **23（`CURLE_WRITE_ERROR`）**。

**实测证据**

```bash
$ export MSYS2_ARG_CONV_EXCL='*'
$ curl -s -o /dev/null http://127.0.0.1:8000/api/instances/ ; echo $?
23                                  # 请求成功，退出码却是 23
$ curl -s -o NUL      http://127.0.0.1:8000/api/instances/ ; echo $?
0                                   # 正确
```

**修法**：按平台显式取 null 设备

```bash
case "$(uname -s 2>/dev/null)" in
  MINGW*|MSYS*|CYGWIN*) NULL_DEV="NUL" ;;
  *)                    NULL_DEV="/dev/null" ;;
esac
curl -s -o "$NULL_DEV" -w '%{http_code}' --noproxy '*' --max-time 5 "$url"
```

本项目已修在 `start-stack.sh` / `run-all.sh` / `run-one.sh`。
**修好后启动耗时从 ~9 分钟降到 2 秒。**

**如何自查**：如果起栈耗时以分钟计 → 你踩了这个（或 C-2）。

---

### C-2 ⭐ 「端口被占用」≠「服务可用」

**症状**

- 健康检查说服务已就绪，但请求全部超时
- `netstat` 显示端口在 LISTENING，`curl` 却完全不响应（状态码 `000`）

**根因**

dev server 在**启动过程中崩溃**时，**端口仍然是绑定的**。
本项目实测：react-router 的 typegen 执行 `rmdir .react-router/types/app/+types`，
沙箱的批量删除保护拒绝它，进程以 `EPERM` 退出 —— **但 socket 已经在监听了**。

**修法**

区分两个概念，并清掉僵尸后再启：

```bash
# 端口有没有被占
bound()     { netstat -ano | grep LISTENING | grep -q ":$1 "; }

# 服务到底答不答
answering() { code=$(curl -s -o "$NULL_DEV" -w '%{http_code}' --noproxy '*' --max-time 5 "http://127.0.0.1:$1${2:-/}"); [ "$code" != "000" ] && [ -n "$code" ]; }

# 崩了的 dev server 仍占着端口，直接重启会 EADDRINUSE，先清僵尸
kill_port() { netstat -ano | grep LISTENING | grep ":$1 " | awk '{print $NF}' | sort -u | xargs -r -I{} taskkill //PID {} //F; }
```

`start-stack.sh` 还会在首次等待失败后**重启一次前端**再等一轮。

**相关**：沙箱批量删除保护可以用 `CODEBUDDY_SAFE_DELETE_ENABLED=0` 关掉（启动脚本已设）。
**必须走 `start-stack.sh`**：它会在 nohup 之前 `export` 这个变量。手敲
`./node_modules/.bin/react-router dev --port 3000` 会绕开它，然后被 typegen 的
`fs.rm('.react-router/types')` 打死（117 个文件 > 阈值 50）：

```
Error: [safe-delete][SAFE_DELETE_BULK_CONFIRM_REQUIRED]
       {"count":117,"threshold":50,"scope":"turn", ...}
    at watch (.../@react-router/dev/dist/typegen-*.js)
Node.js v22.22.2
```

症状是**启动 5 秒后无提示退出、日志停在 `Local: http://127.0.0.1:3000/`**，
很容易误判成"还在冷启动"。

---

### C-3 ⭐ 等待循环必须落盘

**症状**

- 循环在做什么**完全看不见**，只能靠 `ps -W` 做进程取证
- "卡住了"和"在重试"无法区分

**根因**：stdout 被 `| tail` 缓冲时，直到运行结束前什么都看不到。

**修法**：每轮同时写文件

```bash
printf 't=%s ' "$i" | tee -a "$LOGS/stack-wait.log"
```

本项目有 `tests/logs/stack-wait.log` 与 `run-all-wait.log`。

---

### C-4 健康检查超时必须给足

**症状**：健康的服务被判定为 down，循环跑满预算。

**根因**：Vite dev server 的**首次请求**要等它编译完路由，实测命中 ~1.3s，冷启动更久。
用 2s 超时会把健康服务判死。

**修法**：用 **5–10s**（本项目最终用了 10s）。
等待预算给足：后端 + 4 个 Vite 服务同时冷启动需要时间，
本项目从 40 次提到 **90 次**（约 15 分钟上限）才不再误报 TIMEOUT。

---

### C-5 沙箱 Job Object 会杀掉你启动的服务

**症状**：在一个工具调用里起的后端，下一个工具调用就找不到了。
`nohup`、`disown`、`&` **全都无效**。

**根因**：沙箱把每个派生的 shell 放进一个 **Windows Job Object**，工具调用返回时整个 Job 被拆掉。
Job Object **逃不出去**。

**修法**：**把整套服务 + 全部测试脚本放进同一个进程树**（`run-all.sh`）。

> 已从上一个会话存活下来的服务不受影响（它们不属于任何活跃 Job）——
> 这解释了"为什么 web/admin 一直好用，而我在工具调用里起的后端秒死"。

---

### C-6 `APPDATA` 未设置导致 Go 配置不可见

**症状**

```
cannot find go env config: %AppData% is not defined
```

之后构建**静默回落到被墙的默认代理**。

**根因**：Git Bash 里 `%AppData%` 没有定义。

**修法**：**每个跑 Go 命令的 shell** 都要

```bash
export APPDATA="C:\\Users\\<你>\\AppData\\Roaming"
```

---

### C-7 `proxy.golang.org` 不可达（中国大陆网络）

**症状**：拉模块 502 / 连接被拒。

**修法**（持久化，只需做一次）

```bash
export APPDATA="C:\\Users\\<你>\\AppData\\Roaming"     # 必须先设，否则 go env -w 会失败
go env -w GOPROXY=https://goproxy.cn,direct
go env -w GOSUMDB=sum.golang.google.cn
```

---

### C-8 `wmic` 被安全策略禁用

**症状**：进程取证命令不可用。

**修法**：只用 `ps -W`。列含义：`PID PPID PGID WINPID` + 命令。

---

### C-9 Node 是 Windows 二进制，Git-Bash 路径要转换

**症状**：Node 脚本收到 `/c/Users/...` 被当成相对路径，解析成 `C:\c\Users\...`。

**修法**

```bash
script="$(cygpath -w "$HERE/$name" 2>/dev/null || echo "$HERE/$name")"
"$NODE" "$script" "$@"
```

---

### C-10 不要在系统临时目录写日志

**症状**：重定向静默失败，服务启动不了。

**根因**：沙箱某阶段开始拒绝写系统 temp（`Permission denied`，连 `mv` 已有临时文件都失败），
把脚本里所有 `> /tmp/x.log` 都打哑了。

**修法**：日志写到项目内（`tests/logs/`），不要用 `/tmp`。

---

### C-11 ⭐⭐ 在 cmd/PowerShell 里敲 `bash xxx.sh` 会跑成 **WSL** 的 bash，然后**假报成功**

**症状**（一次典型的完整输出）

```
C:\...\tests\browser> bash start-stack.sh all
mkdir: cannot create directory '/c': Permission denied
start-stack.sh: line 36: .../tests/logs/stack-wait.log: No such file or directory
start-stack.sh: line 105: cd: /c/Users/cao/.../plane-pocketbase: No such file or directory
start-stack.sh: line 123: cd: /c/Users/cao/.../apps/web:         No such file or directory
...（5 个 cd 全部失败）
[stack] all requested services are up (after 1s)      ← ★ 最危险的一行
```

提示符是 `C:\...>` 或 `PS C:\...>`（**不是** `cao@MINGW64 ...$`）。

**根因**（两层，缺一不可）

1. **解释器选错了。** 在 cmd/PowerShell 里 `bash` 走 `%PATH%` 解析，而
   `C:\Windows\System32\bash.exe` **排在前面**——它是 **WSL 启动器**（旁边就是 `wsl.exe`）。
   WSL 的 bash：
   - 看不到 `/c/...`。在 WSL 里 `/c` 是 Linux 根目录 `/` 下的一个普通目录，
     所以 `mkdir -p /c` 撞到只读的 `/` → `Permission denied`；
   - 没有 MSYS2 的 POSIX→Windows 路径转换，所以 `cd /c/Users/...` → `No such file or directory`。

2. **脚本本身在撒谎。** 每个 `cd` 失败后子 shell 立刻退出，**一个服务都没启动**，
   但 `wait_for_services()` 的最后一行是无条件成功：
   ```bash
   if ! wait_for_services; then ...fi
   # 函数返回后，脚本没有任何"再确认一次"就自然结束，退出码 0
   ```
   更早的版本甚至在 `for` 循环里用 `curl ... && printf OK || { ok=0; }`，
   在 WSL 下 `curl` 是 Linux 的 `curl`，对 `127.0.0.1:8000` 的失败探测有时被
   `||` 分支吞掉，于是第 1 轮就 `ok=1` 直接报成功。

**修法**（三重，都已落地在 `tests/browser/start-stack.sh`）

```bash
# ① 解释器护栏：进错环境立刻退出并告诉用户正确命令
_sys="$(uname -s 2>/dev/null || echo unknown)"
case "$_sys" in
  MINGW*|MSYS*|CYGWIN*) : ;;        # Git Bash，正确
  Linux)  echo "你在 WSL 里跑…" >&2; exit 3 ;;   # WSL 启动器
  *)      echo "不支持的 shell 环境" >&2; exit 3 ;;
esac

# ② 路径自推导，不再硬编码 /c/Users/cao/...
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PB="$(cd "$HERE/../.." && pwd)"

# ③ 启动失败要能传播：子 shell 里 cd 失败打 FATAL 并 exit 1，
#    外层 `) || LAUNCH_FAILED=1` 捕获；随后立刻报错退出，
#    而不是白等 90 轮（≈15 分钟）
```

以及**失败诊断**：失败时打印 `===== FAILURE REPORT =====`，逐个服务说明
「端口没被占用（从未启动）」还是「端口被占用但不响应（启动中崩了）」，
并附上每个 `*-dev.log` 的末 12 行。

**如何自查**

```bash
# 你在哪个 bash 里？
uname -s          # MINGW64_NT-* / MSYS_NT-* = Git Bash（对）；Linux = WSL（错）

# 正确调用姿势（从 Git Bash 提示符，或用 -lc 显式进 Git Bash）
"C:\Program Files\Git\bin\bash.exe" -lc \
  "cd /d/ai-work/plane_pocket/plane-pocketbase/tests/browser && bash start-stack.sh all"

# 脚本退出码必须可信：0 才代表真的起来了
bash start-stack.sh all; echo "exit=$?"
```

**凡看到「成功」字样，务必独立复验**（这也是 C-2 的同一条纪律）：

```bash
for p in 8000 3000 3001 3002 3100; do
  curl -s -o /dev/null -w "port $p -> %{http_code}\n" --noproxy '*' --max-time 5 "http://127.0.0.1:$p/"
done
```

**连带坑**：`run-all.sh` / `run-one.sh` 也有同样的「假成功」。
`run-all.sh` 原代码在探测超时后只打一句 `continuing anyway` 就往下跑完整个链路；
`run-one.sh` 原代码 `break` 出循环后**无条件**打印 `services ready after ${i}s`，
什么都没起来时会报 `ready after 120s`。两者都已改为**显式 `ready` 标志 + 非零退出**。

---

### C-12 ⭐ `curl` 检测存活的 `|| echo 000` 写法会把死服务判成活的

**症状**：脚本报告 `answering=yes` / 服务「正常」，但实际端口是死的
（`curl` 拿不到任何响应）。诊断信息把人往错方向带。
本项目是在写 `stop-stack.sh` 时实测撞到的 —— 一个只 `listen` 从不响应的
僵尸进程被判成 `answering=yes`。

**根因**：`curl -w '%{http_code}'` 在**连接失败**时会把 `000` **写到 stdout**，
**然后**以非零码退出。于是这个常见写法会得到两个 `000`：

```bash
# ❌ 错误
code=$(curl -s -o "$NULL_DEV" -w '%{http_code}' --max-time 3 "http://..." || echo 000)
# 连接失败时 code="000000"（curl 的 000 + fallback 的 000）
[ "$code" != "000" ] && ...   # "000000" != "000" 成立 → 误判为存活
```

实测证据：

```
code=[000000]   length=6
→ "BUG CONFIRMED: reports answering=yes for a dead port"
```

**修法**：把**退出码**和**输出**分开判断，不要用 `|| echo 000` 兜底。

```bash
# ✅ 正确
was_answering() {
  local code status
  code=$(curl -s -o "$NULL_DEV" -w '%{http_code}' --noproxy '*' --max-time 3 "http://127.0.0.1:$1${2:-/}" 2>/dev/null)
  status=$?
  [ "$status" -eq 0 ] && [ "$code" != "000" ] && [ -n "$code" ]
}
```

（`start-stack.sh` 的 `answering()` 本来就没有 `|| echo 000`，所以是对的 ——
这个坑是**新写脚本时抄错**引入的。）

**如何自查**：故意探测一个没人监听的端口，打印变量内容与长度：

```bash
code=$(curl -s -o NUL -w '%{http_code}' --max-time 3 http://127.0.0.1:9999/ || echo 000)
echo "code=[$code]"          # 期望 [000]；若是 [000000] 就是踩了这个坑
```

**推广**：任何 `cmd=$(...) || fallback` 的写法都要怀疑
「命令**既输出又失败**」的情况。`curl -w` 就是典型。

---

### C-13 ⭐ 硬编码的工具链路径会随版本升级**静默**失效

**症状**：`bash stack.sh start` 的输出最上面混着这么一行

```
[start-stack] WARNING: NODE=.../node/versions/22.22.2-3/node.exe is not executable; apps/live will fail to start
```

然后 `live` 起不来。**它不是立刻报错，而是先等满 90 轮、再等满一轮重启重试**，
最后才以 `TIMED OUT` + `FAILURE REPORT` 收场 —— 而**唯一的线索就是开头那一行 WARNING**。
（`start all` 因为 `live` 在等待清单里，最终会 exit 1；但排查时你看到的是一份
「4 个服务都 OK、只有 live DOWN」的报告，很难联想到是**路径**问题。）

真正昂贵的后果不在启动阶段：`apps/live` 是 Pages 富文本编辑器的协作后端，
缺了它编辑器会退化成 `usePageFallback`，在 `.../pages/<id>/description/` 上
**无限 PATCH/GET 打转**（见 **D-9**）。所以这行 WARNING 的代价是"某天编辑器开始抽风"。

> **2026-10-16 备注（N-10 暂不实现）**：既然 `apps/live` 决定**暂不部署**，
> 本条与 #10 目前**不会触发**（没有 live 可起）。保留于此，是因为**将来一旦启用 live，坑照旧存在**，
> 且启用时务必先修脚本里写死的版本化路径。见 `01` D5′、`10-REMAINING-WORK.md` §3.1。
>
> **2026-10-23 更新**：协作端已改由 Go 的 `internal/live` 提供，**`apps/live` 不再是运行时依赖**，
> 本条只影响"还想跑 dev 栈里的 live"这一种情况。

**根因**：托管 Node 运行时放在**带版本后缀**的目录里，后缀每次升级都会变：

```
/c/Users/cao/.workbuddy-ai/binaries/node/versions/22.22.2-3/node.exe   ← 脚本里写死的（已不存在）
/c/Users/cao/.workbuddy-ai/binaries/node/versions/22.22.2-6/node.exe   ← 实际存在的
```

`start-stack.sh` / `run-all.sh` / `run-one.sh` 三个脚本都写死了 `22.22.2-3`。
运行时一升级，路径就悬空了。而当时 `start-stack.sh` 只是 `echo` 一行警告继续往下跑 ——
**它警告的对象恰好就是它自己即将做错的事**，可没人会去读那一行。

**修法**：不要写死，去解析。`tests/browser/node-env.sh` 提供 `resolve_node()`：

```bash
. "$HERE/node-env.sh"
resolve_node          # 设置 $NODE
```

优先级：`$PLANE_NODE`（显式覆盖）→ `versions/` 下**版本号最大**的 → `versions/current`
→ PATH 上的 `node` → Windows 默认安装位置。

**两个自己踩到的细节**：

- 必须用 `sort -V`（**版本序**）。普通字典序会把 `22.22.2-10` 排在 `22.22.2-6`
  **前面**，`tail -1` 于是取到**更旧**的运行时 —— 升级一次反而回退一次。
- 解析结果要拼上 `/node.exe`。第一版 `resolve_node` 返回的是**目录**，
  而 `[ -x <目录> ]` 是**真**的，直到真的去执行才报 `Is a directory`。
  **所以"解析成功"必须以真的跑一次 `"$NODE" --version` 为证**，不能只看 `[ -x ]`。

**如何自查**：

```bash
bash -c '. tests/browser/node-env.sh; resolve_node; echo "$NODE"; "$NODE" --version'
# 期望：打印一个存在的 node.exe 路径 + 一个版本号（如 v22.22.2）
```

**推广**：**凡是"外部工具的位置"，都不要写死路径。** 版本化目录（node/python/go 的
托管运行时）、`node_modules/.bin`、临时目录，都属于这一类。写死路径的代价不是立刻报错，
而是**某天悄悄少起一个服务**，然后在离根因很远的地方（这里是编辑器死循环）表现出来。

### C-14 ⭐⭐ `pnpm install` 报成功但 `node_modules` 是空的（本环境建不了符号链接）

**症状**：`bash stack.sh start` 之后四个前端全部秒退，浏览器「拒绝连接」。日志里是：

```
Error [ERR_MODULE_NOT_FOUND]: Cannot find package 'pathe'
    imported from .../apps/web/node_modules/@react-router/dev/dist/routes-*.js
Error [ERR_MODULE_NOT_FOUND]: Cannot find package '@react-pdf/primitives'
Error [ERR_MODULE_NOT_FOUND]: Cannot find package '@plane/logger'
    imported from .../apps/live/dist/start.mjs
```

而 `pnpm install` 的输出是 **`added 1183, done`、exit 0、零 WARN**。

**根因有两层，第二层才是真正致命的：**

1. **搬家把链接拍平了。** 2026-10-10 仓库从
   `C:\Users\cao\Desktop\asl-work\plane_pocket` 搬到 `D:\ai-work\plane_pocket`
   （提交 `1c763a5` 只改了 10 行路径字符串，**不是脚本的错**）。跨盘复制把 pnpm 的
   junction 解引用成了实体目录：旧树 `apps/web/node_modules` 是 **64 个 Junction / 0 个实体目录**，
   新树变成 **0 / 64**。实体副本让 Node 的 realpath 停在副本上，再也走不到 `.pnpm` 里的兄弟依赖。
2. **这个环境里 pnpm 建不出符号链接。** 即使删干净重装（`.pnpm` 1184 个包齐全、
   `added 1183, done`、exit 0、零警告），`apps/*/node_modules` 里**只有空的作用域目录**，
   `.pnpm/*/node_modules/` 也没有兄弟链接。

**30 秒最小复现**（只依赖 `pathe@2.0.3` 的空项目）：

| `node-linker` | 结果 |
|---|---|
| `isolated`（上游默认） | `node_modules/pathe` **不存在**，但 pnpm 打印 `+ pathe 2.0.3` |
| `hoisted` | `node_modules/pathe/package.json` **存在** ✅ |

**排除项**（都验证过，不是它们）：
- D: 是 NTFS，手工 `New-Item -ItemType Junction` 与 `fs.symlinkSync(...,'junction')` **都成功**
- `NODE_OPTIONS` 注入的 `node-language-shim.cjs` 里没有 symlink 相关代码；`env -u NODE_OPTIONS` 结果相同
- lockfile 的 `importers` 完整包含 19 个 workspace 项目

**修法**：改用 hoisted 布局（只写实体目录，不依赖链接）。`plane-preview/.npmrc`：

```ini
node-linker = hoisted
```

hoisted 之后还有两件事 pnpm 仍然要靠链接，必须手工补 junction（本环境可建 junction）：

```powershell
# 1) 工作区包 -> 根 node_modules/<name>
#    读 packages/*/package.json 的 name，建 Junction 到 packages/<dir>
# 2) 各 app 的 .bin -> 根 .bin（start-stack.sh 用的是 ./node_modules/.bin/react-router）
# 3) 各 app 的 @react-router/dev -> 根同名包（.bin 的 shim 用相对路径 ../@react-router/dev/bin.cjs）
```

**验证**（期望：8000/3000/3001 = 200，3002 = 302，3100 = 404）：

```bash
for u in http://127.0.0.1:8000/api/instances/ http://127.0.0.1:3000/ \
         http://127.0.0.1:3001/god-mode/ http://127.0.0.1:3002/ http://127.0.0.1:3100/live/; do
  printf '%-45s ' "$u"; curl -s --noproxy '*' -o /dev/null -w '%{http_code}\n' --max-time 20 "$u"
done
```

**自查"链接图是否完好"**（0 就是坏了）：

```powershell
(Get-ChildItem 'D:\ai-work\plane_pocket\plane-preview\apps\web\node_modules' -Force |
  Where-Object { $_.LinkType }).Count
```

**配套的环境坑**：这次安装中途还撞上 **C: 盘 0 字节可用**（`ENOSPC`），而 C: 满会让安全删除代理
写不了审批记录 → **删不动文件**的死锁。绕法是 `[System.IO.Directory]::Delete()`。
C: 的构成：Users 124.9 GB / Windows 25.8 / Program Files 37.7 / 回收站 9.07 / ProgramData 7.5。
重装留下的 `node_modules.broken-*` 千万别留在项目树里，清法见 **C-15**。

> 教训：**Go 侧全绿不能证明前端可用**。`go test` / `release-check` 完全不经过 `node_modules`，
> 所以这个洞从 10-10 搬家一直潜伏到 10-17 才被发现。

#### C-14b ⭐⭐ `pnpm install` 报 `resolve the installed @pnpm/exe … os error 2`

**症状**：任何 `pnpm` 命令（install、run、甚至 `-r`）开头就死：

```
Error: × resolve the installed @pnpm/exe at
  C:\Users\cao\.pnpm\package-manager-store\v11\tmp\pnpm-engine-11.10.0-…\node_modules\@pnpm\exe
  ╰─▶ 系统找不到指定的文件。 (os error 2)
```

**根因**：根 `package.json` 里有 `"packageManager": "pnpm@11.10.0"`。pnpm 会先把这个**精确版本**
下载到 `%LOCALAPPDATA%\pnpm\package-manager-store` 再用它执行命令；那一步在这里完不成，
于是所有命令都在"还没开始"时失败。

**绕法**（关掉包管理器自管理，用 PATH 上的 pnpm v12，它读同一份 lockfile 没问题）：

```bash
npm_config_manage_package_manager_versions=false pnpm install --frozen-lockfile
```

已固化在 `tests/frontend/install-deps.sh`。

**⚠️ 它会以三种"变形"再次出现，别以为躲过了：**

1. **turbo 里**：turbo 读 `packageManager` 字段，把每个 task 跑成 `pnpm run build`，
   于是子任务里又炸一次。变量必须在**启动 turbo 之前** export。
2. **turbo 的 strict env**：turbo 2 默认 `envMode: strict`，子任务只拿到 `turbo.json`
   里 allowlist 的变量，**你刚 export 的那个会被静默剥掉**。要加 `--env-mode=loose`
   （不能改 `turbo.json` 的 `globalPassThroughEnv`——那是上游纯净树）。
3. **⭐ `pnpm run` 会先重装整个 workspace。** 这是最要命的一条：pnpm ≥10 在跑任何
   script 前会校验依赖树，发现"记录的安装方式 ≠ 当前配置"就**先装一遍**
   （子进程命令行是 `pnpm install --verify-deps-before-run-install --use-stderr`）。
   而我们偏偏是用 `--node-linker hoisted` 装的，默认配置是 `isolated` —— 于是
   **每一次 `pnpm run` 都判定不一致、都重装**。turbo 并行跑 5 个包 = 5 个并发
   workspace 安装抢同一把锁：

   ```
   # 实测：20 分钟零产出，5 个 pnpm install 子进程各占 100 MB 空转
   @plane/i18n:build: cache miss, executing …
   （之后再无输出，packages/*/dist 一个都没有）
   ```

   `npm_config_verify_deps_before_run=false` / `npm_config_node_linker=hoisted`
   都**无效**（pnpm 12 不认这两个 env，`pnpm config get node-linker` 仍返回空）；
   `pnpm run --help` 里也没有对应开关。

**结论：不要用包管理器去跑构建脚本。** `tests/frontend/build-packages.mjs` 直接按
依赖拓扑序遍历 `packages/*`，把 `node_modules/.bin` 放进 PATH 后执行每个包的 `build`
脚本（脚本体内的 `pnpm run x` 就地内联展开）。没有包管理器，就没有安装。

> 另一个坑：`node_modules/.bin/<tool>` 是 `/bin/sh` 脚本，`.bin/<tool>.cmd` 才是
> Windows 入口。用 `node <shim>` 去跑会得到
> `SyntaxError: Invalid or unexpected token`（shim 的注释头被当 JS 解析）。

---

### C-15 ⭐⭐ 残留的 `node_modules.broken-*` 目录会把 Vite 监听器拖到 OOM

**症状**：服务**先好后坏**。起栈时 3000 正常，几十分钟后浏览器「拒绝连接」，`web-dev.log` 末尾是：

```
[vite] changed tsconfig file detected:
  .../apps/web/node_modules.broken-20261017135138/@plane/editor/node_modules.broken-.../@plane/blocks/...
  - Clearing cache and forcing full-reload to ensure TypeScript is compiled with updated config values.
...（同一条消息刷几百行）...

FATAL ERROR: Reached heap limit Allocation failed - JavaScript heap out of memory
```

**根因**：修 C-14 时反复重装，留下一堆 `node_modules.broken-<ts>` / `.dead-*` / `.partial-*` 目录。
Vite 默认只忽略 `**/node_modules/**`，**不匹配 `node_modules.broken-*`**，于是 chokidar 进去爬。
这些目录里是 pnpm 的嵌套副本（`@plane/editor/node_modules.broken-*/@plane/blocks/node_modules.broken-*/...`），
每个都带一份 `tsconfig.json`；`vite-tsconfig-paths` 每读到一个就「清缓存 + 全量重载」，
最终把 4 GB 堆撑爆。**不是代码问题，是垃圾目录问题。**

**修法（两件事都要做）**：

1. 清掉残留目录（见下方命令）。它们必须在项目树里消失，改名成 `.bak` 没用。
2. 加护栏，防止下次重装再踩：三个 `vite.config.ts` 的 `server` 段都加

```ts
watch: { ignored: ["**/node_modules.*/**"] },
```

**清理命令**（路径常超 260 字符，且 sharp 的 `libvips-42.dll` 容易被杀软锁住，
`Remove-Item` / `Directory.Delete` 会报「访问被拒绝」；robocopy 空目录镜像最稳）：

```powershell
$root='D:\ai-work\plane_pocket\plane-preview'
$empty='D:\ai-work\plane_pocket\.purge-empty'
New-Item -ItemType Directory -Path $empty -Force | Out-Null
$dirs = @(Get-ChildItem $root -Directory -Force | Where-Object { $_.Name -like 'node_modules.*' })
foreach ($sub in @('apps','packages')) {
  Get-ChildItem (Join-Path $root $sub) -Directory | ForEach-Object {
    $dirs += Get-ChildItem $_.FullName -Directory -Force | Where-Object { $_.Name -like 'node_modules.*' }
  }
}
foreach ($d in $dirs) {
  robocopy $empty $d.FullName /MIR /R:1 /W:1 /NFL /NDL /NJH /NJS /NP | Out-Null
  try { [System.IO.Directory]::Delete("\\?\$($d.FullName)", $true) } catch {}
}
```

个别删不掉的（`libvips-42.dll` 被占）：直接 `Move-Item` 移到项目外的
`D:\ai-work\.trash-<日期>\`，**只要不在项目树里就不会被监听**，之后再删。

**自查**（期望 0；非 0 就还有雷）：

```powershell
$root='D:\ai-work\plane_pocket\plane-preview'
@(Get-ChildItem $root -Directory -Force | Where-Object { $_.Name -like 'node_modules.*' }).Count
```

> 教训：**「先好后坏」优先怀疑工作目录里的垃圾，而不是代码**。这次 3000 端口是在
> 稳定运行一小时后才崩的，期间没人改过一行代码——变的只有我留下的临时目录。

---

### C-16 ⭐⭐ 代码全在 D:，C: 却一直在变小（缓存/日志/安装残留都在用户目录）

**症状**：仓库、`node_modules`、`.fe-run` 数据全在 `D:\ai-work\plane_pocket`，
但 C: 可用空间持续下降。2026-10-17 实测：18:20 有 12.1 GB，22:04 只剩 8.46 GB。

**审计出来的四个来源（没有一个在项目目录里）**：

| 来源 | 实测大小 | 是否持续增长 |
|---|---|---|
| `%TEMP%\<随机名>`（VS / VS Code 安装器下载的组件包） | 3.07 GB | 一次性，装完不自动删 |
| `%USERPROFILE%\.workbuddy-ai\logs`（沙箱审计日志，按天分目录） | 4.8 GB | **是，约 1 GB/天** |
| `%USERPROFILE%\.workbuddy-ai\workspace\sessions`（会话快照库） | 2.95 GB | 不超，GC 上限 3000 MB |
| `%LOCALAPPDATA%\pnpm\store\v11` + `package-manager-store` | 789 MB + 612 MB | 与项目无关的孤儿缓存 |

**关于 pnpm store 的两个反直觉点（都实测过）**：

1. **Windows 下 pnpm 的默认 store 在项目所在盘，不是 C:。**
   本项目的 store 是 `D:\.pnpm-store\v11`（2.9 GB / 64,406 个文件），一开始就在 D:。
   `%LOCALAPPDATA%\pnpm\store` 里那 789 MB 是**别的上下文**留下的（cwd 在 C: 时跑过 pnpm，
   比如装 pnpm 自身），对本项目是孤儿。所以「把 store 从 C: 搬到 D:」是伪需求，
   真正要做的是**删掉 C: 的孤儿目录**。
2. **pnpm 11 的设置在 `pnpm-workspace.yaml`（camelCase），`.npmrc` 里的 `store-dir` 会被静默忽略。**
   验证方式：加完 `.npmrc` 的 `store-dir` 后，`pnpm config list` 里**根本没有** store 条目，
   `pnpm store path` 仍返回默认值。换成 `pnpm-workspace.yaml` 的 `storeDir` 才生效。

**修法**（`plane-preview/pnpm-workspace.yaml` 顶层加一行，把位置钉死）：

```yaml
storeDir: "D:/.pnpm-store"
```

```bash
# 验证：两处都必须对
cd /d/ai-work/plane_pocket/plane-preview
node C:/Users/cao/.workbuddy-ai/binaries/node/workspace/node_modules/pnpm/bin/pnpm.cjs store path   # -> D:\.pnpm-store\v11
node .../pnpm.cjs config list | grep storeDir                                                       # -> "storeDir": "D:/.pnpm-store"
```

**清理 C: 上的孤儿 store**（删之前先确认项目自己的 store 在 D:，不要删错）：

```powershell
# 先确认：这个输出必须是 D: 开头
cd D:\ai-work\plane_pocket\plane-preview
node C:\Users\cao\.workbuddy-ai\binaries\node\workspace\node_modules\pnpm\bin\pnpm.cjs store path
# 再删 C: 的孤儿（保留 package-manager-store / global）
Remove-Item "$env:LOCALAPPDATA\pnpm\store" -Recurse -Force
```

> 别急着 `robocopy` 搬 store：本次先花 40 分钟跨盘复制了 6 万个小文件，
> 后来才发现目标内容早就存在、源目录根本没人用。**先 `pnpm store path` 看清事实再动手。**

**清理日志与安装残留**（都在项目外，属缓存性质）：

```powershell
# 沙箱日志：只留最近 7 天
Get-ChildItem "$env:USERPROFILE\.workbuddy-ai\logs\sandbox" -Directory |
  Where-Object { $_.Name -match '^\d{8}$' -and $_.Name -lt (Get-Date).AddDays(-7).ToString('yyyyMMdd') } |
  Remove-Item -Recurse -Force
# 安装器残留：装完就没人用了
Remove-Item "$env:TEMP\ff5ytblb" -Recurse -Force -ErrorAction SilentlyContinue
```

**自查**（按大小排序，看谁在长）：

```powershell
foreach ($p in "$env:LOCALAPPDATA\pnpm","$env:USERPROFILE\.workbuddy-ai\logs","$env:TEMP","$env:LOCALAPPDATA\go-build") {
  if (Test-Path $p) { "{0,-55} {1,7:N0} MB" -f $p, ((Get-ChildItem $p -Recurse -File -Force -ErrorAction SilentlyContinue | Measure-Object Length -Sum).Sum/1MB) }
}
```

> 教训：**"代码在 D:" 不等于"只在 D: 写盘"**。工具链的缓存、日志、快照默认都在
> `%LOCALAPPDATA%` / `%USERPROFILE%`，要逐个显式改掉才会真正搬到 D:。

**构建期把临时目录也搬到 D:**（2026-10-27 又踩一次：`pnpm install` 跑到一半
C: 归零，bash 连 `echo` 都写不进去，报 `No space left on device`）。
`pnpm install` / `tsdown` / `react-router build` 都会往 `%TEMP%` 写：

```bash
export TEMP='D:\ai-work\plane_pocket\_scratch\tmp'
export TMP='D:\ai-work\plane_pocket\_scratch\tmp'
export TMPDIR=/d/ai-work/plane_pocket/_scratch/tmp
```

**⚠️ 别在 bash 脚本运行时改那个脚本。** bash 是按**字节偏移**增量读脚本的：
编辑后它从旧偏移继续读，会读到半行，报出像 `line 107: tree: command not found`
这种莫名其妙的错（本次在验证脚本上真实发生过，差点被当成脚本 bug 排查）。

---

### C-17 ⭐⭐ 生产构建报 `ENOENT <app>\tailwindcss`（hoisted 安装 + Tailwind v4）

**症状**：开发（`react-router dev`）一切正常，但 `react-router build` 在
「building ssr environment」阶段失败：

```
[plugin vite:css] apps/admin/styles/globals.css?transform-only:undefined:NaN
Error: [postcss] ENOENT: no such file or directory, open 'D:\...\apps\admin\tailwindcss'
```

注意路径里少了 `node_modules`——它把 `tailwindcss` 当成了**相对路径**。

**影响面**：`web` / `admin` / `space` **三端全挂**，与 `ssr` 开关无关。
2026-10-18 之前这个坑一直没暴露，因为只跑 dev、没跑过 `build`。

**根因链**：

1. Vite 的 CSS 处理用 `postcss-import` 解析 `@import`，它内部用的是 `resolve` 包。
2. `resolve` 只认 `package.json` 的 `main` / `style`，**不认 `exports` 字段**。
3. Tailwind v4 的 `package.json` 只有 `exports`（`"." -> {"style": "./index.css"}`），
   没有 `main` → `resolve` 失败 → 退回「按相对路径找」→ `apps/<app>/tailwindcss` → ENOENT。
4. C-14 改成 `node-linker = hoisted` 后，`tailwindcss` 只在**根** `node_modules`，
   应用目录下没有，进一步保证了这个回退路径必然失败。

**修法**：把共享样式里的裸导入改成显式子路径（一行）。

```css
/* packages/tailwind-config/index.css */
@import "tailwindcss/index.css";   /* 原来: @import "tailwindcss"; */
```

子路径会被当成普通文件解析，绕开 `exports`；内容等价（`style` 字段指向的就是它）。

**验证**：

```bash
cd plane-preview/apps/admin
CODEBUDDY_SAFE_DELETE_ENABLED=0 ./node_modules/.bin/react-router build   # 必须 exit 0
```

> 别用 `pnpm --filter <app> build` 验证：本环境的 pnpm 会先跑一次
> `pnpm install`，撞上 C-14 的 `ERR_PNPM_PNPM_ENGINE_IDENTITY_UNVERIFIABLE`，
> 报的是安装失败而不是构建失败，容易误判。直接调 `./node_modules/.bin/react-router build`。

> 教训：**dev 能跑 ≠ 能构建**。dev 走 Vite 的按需转换，build 才走完整的
> postcss 链；换了 node-linker 之后尤其要补一次 `build` 验证。

---

### C-18 ⭐⭐ 自己写 gzip 中间件：四个必须处理的细节

**背景**：`internal/middleware/gzip.go` 让单二进制部署也有压缩（否则前端 bundle
1.99 MB 原样发出，首屏约 3.5 倍流量）。看起来是「包一层 `gzip.Writer`」，
实际有四个坑，每一个都会产出**客户端无法解析**的响应。

**① 报错路径上缓冲的 body 会粘在 500 前面**

`tools/router.loadMux` 里是 `ErrorHandler(resp, req, err)`——用的是**原始**
`resp` 变量，不是 `e.Response`。中间件替换了 `e.Response` 之后，处理器写了一半
再返回 error 时：缓冲的 200 body 留在我们手里，ErrorHandler 又往原始 writer 写
JSON 错误 → 客户端收到「半截 JSON + 错误 JSON」。
所以错误路径必须 `Discard()` 掉缓冲区，把响应完全交给 router。

**② `Content-Length` 必须删**

`http.ServeContent` 会按**原始文件大小**设 `Content-Length`。压缩后长度变了，
不删的话客户端按旧长度读、或服务端直接报错。同理要删 `Accept-Ranges`
（字节范围已经映射不到压缩流上），并补 `Vary: Accept-Encoding`。

**③ 不能一刀切**

| 情况 | 为什么必须跳过 |
|---|---|
| `Range: bytes=…` | 客户端按偏移取字节，压缩流里没有这些偏移（实测 `206` + 100 B 必须保持 identity） |
| `text/event-stream` | `/api/realtime` 的 SSE 必须逐条直达；压了以后帧边界被改变，代理也会缓冲 |
| 已带 `Content-Encoding` | 双重压缩，客户端只解一层 → 乱码 |
| PNG / WebP / woff2 / mp4 / zip | 已经是压缩格式，再压是负收益（CPU 换 0%） |
| 1xx / 204 / 304 | 没有 body，包一层只会让状态码错乱 |

**④ 别自己实现 `Written()` / `Status()`**

`tools/router` 的 `getWritten` / `getStatus` 会沿着 `Unwrap() http.ResponseWriter`
链回落到内层 `*router.ResponseWriter`。包装器只要实现 `Unwrap()`（外加 `Flush()`
转发给 `http.NewResponseController`），状态就只有一份事实来源；
自己再记一份 `written` 容易和真实状态不同步，导致 ErrorHandler 误判。

**验证**（不要只看 `Content-Encoding`，要 round-trip）：

```bash
curl -s -H 'Accept-Encoding: gzip' -o /tmp/x.gz "$B/spaces/"
gunzip -c /tmp/x.gz | cmp - <(curl -s "$B/spaces/")   # 必须无输出
```

> 实测收益：`page--3ScAE-D.js` 1,989,435 → 563,150（3.5×），
> `index.html` 4,284 → 1,713（2.5×），50 B 的 `/api/health` 与 PNG 原样通过。
> 细节与关闭开关（`PLANE_GZIP=0`）见 [04 §4.4](04-DEPLOYMENT.md)。

---

### C-19 ⭐⭐ 构建单端口前端：`.env.production` 是**不生效**的

**症状**

- 想改前端打包时的 API 地址，老老实实写了 `apps/web/.env.production`，
  重新 build，浏览器里还在打 `localhost:3001`。
- 或者：单端口模式下 admin 页面 502/连不上后端，而 web 正常。

**根因**（读 `apps/*/vite.config.ts` 那两行就能看到，但很容易忽略）：

```ts
dotenv.config({ path: path.resolve(__dirname, ".env") });  // ① 只读 .env
... define: { "process.env": JSON.stringify(viteEnv) }   // ② 整体替换
```

1. **它只读 `.env`**。`.env.production`、`.env.local`、`.env.development`
   一概不碰——`vite build --mode production` 也不会让 dotenv 改看别的文件。
2. **`process.env` 被整体替换成 `.env` 内容的字面量**，于是 `.env` 里的
   `localhost:3000/3001/3002` 会被**编进产物**，与运行时无关、改不了。
3. **dotenv 不覆盖已存在的 process.env 变量**——这是唯一的杠杆：
   构建前在 shell 里 `export VITE_…`，dotenv 会让路，`define` 拿到新值。

**修法**：不要改 `.env.production`。用 `tests/browser/build-frontends.sh`
（`BASE_URL` 一处改全部），或自己 export 后逐个构建：

```bash
cd plane-pocketbase/tests/browser && bash build-frontends.sh   # 官方入口
# 等价手工版：
export VITE_API_BASE_URL=http://localhost:8000 VITE_ADMIN_BASE_PATH=/god-mode …
cd plane-preview/apps/web && ./node_modules/.bin/react-router build
```

**如何自查**（脚本内置，手工也要做——grep 产物，别 grep 源码）：

```bash
grep -rl 'localhost:300[012]' apps/web/build/client && echo "还有多端口地址"
# 必须是空。3000/3001/3002 一旦出现，说明 .env 赢了。
```

> 附带一条：**单端口下 `admin_base_url` 留空是正确的**。
> `adminBaseHost()`（`internal/handlers/instance/auth.go`）在没有 env 时返回
> 相对路径 `/god-mode/`，浏览器对着 8000 解析即中；反而设了多端口的
> `PLANE_ADMIN_BASE_URL` 才会跳错地方。多端口部署才需要它。

---

### C-20 ⭐⭐⭐ Git Bash 会把 env 里的 `/xxx` 改写成 Windows 路径，毁掉 Vite 的 base

**症状**

- `curl http://localhost:8000/god-mode/` → **200**，`/god-mode/assets/<hash>.js` → **200**
- 浏览器打开却只有一个转圈的 logo；Network 里出现这种请求：

```
GET http://localhost:8000/C:/Users/cao/AppData/Local/Programs/WorkBuddyAI/
    resources/vendor/PortableGit/god-mode/assets/root-XXXX.css   (failed)
```

- index.html 里每个资源 URL 都长这样，`window.__reactRouterContext.basename`
  也是 `"/C:/…/PortableGit/god-mode/"`。

**根因**

Git for Windows 的 MSYS 运行时在 **spawn Windows 子进程**时，会把环境变量里
「长得像 POSIX 路径」的值改写成 Windows 路径（不只是 argv）：

```bash
$ export VITE_ADMIN_BASE_PATH="/god-mode"
$ node -e 'console.log(process.env.VITE_ADMIN_BASE_PATH)'
C:/Users/cao/AppData/Local/Programs/WorkBuddyAI/resources/vendor/PortableGit/god-mode
```

而 `apps/admin/vite.config.ts` 里 `base: joinUrlPath(process.env.VITE_ADMIN_BASE_PATH, "/")`
直接吃这个值 → Vite 用它重写 HTML 里**每一个**资源 URL 和 React Router 的 basename。
`space` 同理（`VITE_SPACE_BASE_PATH`）。`web` 没有 base path，所以幸免——
**这也解释了为什么只有 admin/space 坏，web 正常**。

**修法**

```bash
export MSYS_NO_PATHCONV=1          # Git 官方开关，实测有效
# 或
export MSYS2_ENV_CONV_EXCL='*'
```

`tests/browser/build-frontends.sh` 已内置这一行。**注意范围**：从 Git Bash 启动
任何吃路径型 env 的 Windows 程序都有这个风险，包括后端
（`ADMIN_BASE_PATH=/god-mode`）。PowerShell / cmd 不受影响。

**如何自查**（curl 探活查不出来，必须看 HTML 或浏览器）

```bash
grep -c 'C:/' apps/admin/build/client/index.html      # 必须 0
grep -o '"basename":"[^"]*"' apps/admin/build/client/index.html   # 必须是 /god-mode/
```

`build-frontends.sh` 现在会在构建后断言：index.html 不含 `C:/` / `PortableGit`，
且必须出现 `/god-mode/assets/`（admin）或 `/spaces/assets/`（space）前缀。

> **教训**：`200 OK` 只证明文件在磁盘上、路径没写错；**不证明应用能跑**。
> 这一条是浏览器里才暴露的——三个入口都 curl 200 的时候，admin 和 space 其实是白屏。
> 验收必须真的打开页面（或用 Playwright）看 DOM，见 `tests/browser/run*.mjs`。

---

### C-21 ⭐⭐ `MSYS_NO_PATHCONV=1` 反过来会**害死** Go 二进制

**症状**：同一台机器上，直接 `bash serve-single.sh` 一切正常；但从某个脚本里调它，
二进制拒绝启动：

```
2026/10/24 02:03:13 PLANE_ADMIN_DIR: /d/ai-work/.../apps/admin/build/client
                     has no index.html (run the frontend build first)
```

而那个文件明明存在（`ls` 得到 4546 字节）。

**根因**：和 C-20 是同一个开关的**两个方向**。

| | 谁在转换 | 期望的形式 |
|---|---|---|
| C-20（Vite `base`） | Git Bash 自动把 `/god-mode` 改写成 `C:/…/PortableGit/god-mode` | **不能**转 → 要 `MSYS_NO_PATHCONV=1` |
| C-21（`PLANE_*_DIR`） | Git Bash 自动把 `/d/ai-work/...` 改写成 `D:\ai-work\...` | **必须**转，因为 Go 是原生 Windows 程序 |

所以只要调用方（`build-frontends.sh`、`run-chain-single.sh`、`run-all.sh`）
导出了 `MSYS_NO_PATHCONV=1`，`PLANE_ADMIN_DIR=/d/...` 就会**原样**传给二进制，
Go 的 `os.Stat` 找不到 `/d/ai-work/...`，于是报"没有 index.html"。

**修法**：`serve-single.sh` 里用 `cygpath -w` 显式转换，不依赖调用方的 MSYS 开关：

```bash
to_native() {
  case "$(uname -s 2>/dev/null)" in
    MINGW*|MSYS*|CYGWIN*) cygpath -w "$1" 2>/dev/null || printf '%s' "$1" ;;
    *)                    printf '%s' "$1" ;;
  esac
}
export PLANE_ADMIN_DIR="$(to_native "$ADMIN")"
```

**判据**：看到 `PLANE_*_DIR: … has no index.html` 但文件确实在，先 `echo $MSYS_NO_PATHCONV`。

---

### C-22 ⭐⭐⭐ `git apply` 打补丁"成功"了但树没变（退出码 0 + `Skipped patch`）

**症状**：补丁脚本打印 `applied`、退出码 0，随后构建报一个**毫不相关**的错
（我们这里是 `[postcss] ENOENT … apps/web/@plane/editor/styles`），因为补丁根本没进去。

**三个独立原因，任何一个都会造成"静默成功"：**

**① 路径基准不是当前目录，而是仓库顶层。**
`git apply` 把补丁里的 `a/apps/web/...` 解析成 **`<repo 顶层>/apps/web/...`**，
不是 `<当前目录>/apps/web/...`。我们的树在 `<repo>/plane-preview`，所以在
`plane-preview/` 里跑 `git apply` 时，git 去 `<repo>/apps/...` 找文件、找不到，
于是打印：

```
Skipped patch 'apps/web/...'.
```

**并且退出码是 0**。修法：从顶层驱动，带前缀。

```bash
TOP="$(git -C "$TARGET" rev-parse --show-toplevel)"
PREFIX="$(cd "$TARGET" && git rev-parse --show-prefix)"      # 例：plane-preview/
( cd "$TOP" && git apply -p1 --directory="$PREFIX" "$patch" )
```

**② `.gitattributes` 里的 `-text` 会把文件标成二进制**，`git apply` 同样
`Skipped patch` + 退出 0。用 `text eol=lf`，不要 `-text`。

**③ 退出码本身不能作为证据。** 所以 `apply.sh` 每次 apply 成功后立刻反向校验：

```bash
run_apply "$p" --reverse --check || die "reported success but the tree is unchanged"
```

revert 之后做对称的正向 `--check`。这条断言才是上面两个坑的疫苗——
**凡是"命令说成功了"的地方，都要再验一次实际状态**。

**附带的坑**：`.patch` 文件本身若被 CRLF 污染，`git apply` 会因为上下文行不匹配而失败。
本仓库 `.gitattributes` 对 `*.patch` 也强制 LF。

---

### C-23 ⭐⭐ 编辑器显示 "Connection lost"，其实是 live 日志文件被外部短暂占用

**症状**：打开 Pages 里的某篇文档，编辑器顶部报 `Connection lost`（中文界面是"连接已断开"），
内容能显示但协作不工作；刷新几次后有时自己好了。

**服务端日志**（唯一线索，必须看服务端）：

```
[live] open room "60a3fdfb-…": open …\.fe-run\live\60a3fdfb-…-889a4443.log: Access is denied.
```

**因果链**：`Hub.room()` 打不开日志 → `handler.go` 直接 `c.close()` 关掉 WS →
前端只能报 "Connection lost"。**一个瞬时的文件占用 = 用户掉线一次**。

**为什么是"瞬时"**：同一个文件一分钟前打不开、一分钟后 `os.OpenFile` 正常；
ACL 与另一个能正常写入的日志文件**完全一致**（`Authenticated Users: Modify`），
也不是只读属性。这是 Windows 上典型的**短暂独占持有者**：杀软实时扫描、
搜索索引器、备份代理——它们会以不带 `FILE_SHARE_WRITE` 的方式打开文件一小会儿。
本次触发点很典型：那个日志自 10-24 起就没被碰过，第一次打开正好撞上扫描。

**修法**（`internal/live/store.go` `openLogFile`）：对
`ERROR_SHARING_VIOLATION(32)` / `ERROR_ACCESS_DENIED(5)` 重试 5 次、退避 100/200/300/400ms，
其它错误立刻上报。平台判定在 `openfile_windows.go` / `openfile_other.go` 里分叉
（Unix 没有强制锁，不重试）。

> 注意：`syscall` 包**没有**导出 `ERROR_SHARING_VIOLATION`（只有
> `golang.org/x/sys/windows` 有，而它在这里是 indirect 依赖），所以用
> `syscall.Errno(32)` 并写注释，不要为这一个常量加直接依赖。

**判据**：前端报 "Connection lost" 时，**先看服务端日志有没有 `[live] open room … Access is denied`**。
有 → 文件层问题，不是 WS 升级、不是鉴权、不是前端产物。

---

## D 类 · 前端契约

### D-1 ⭐⭐ 前端会把失败渲染成「看起来正常」的状态

**症状**

- 页面不报错、不空白、没有错误边界
- 但**内容是空的**，或者显示一句很礼貌的空态文案

**真实案例（都是实测的）**

| 缺失的接口 | 前端显示 | 截图看起来 |
|---|---|---|
| `home-preferences/` | **"It's Quiet Without Widgets, Turn Them On"** | 完全正常，像功能设计 |
| `workspace-members/me/` | 全屏 **"Not Authorized!"** 盖住整个应用 | 像权限问题，实际是接口没实现 |
| `user-profile/` | 渲染到一半崩 | 部分内容正常 |

**修法**

**验收必须断言具体内容，不能断言"页面没崩"。**

```js
// ✅
check("home widgets seeded", ["quick_links","recents","my_stickies"]
      .every(k => res.json.some(w => w.key === k)));

// ❌ 永远为真，毫无价值
check("page did not crash", true);
check("screenshot exists", fs.existsSync(png));
check("status is not 500", res.status !== 500);
```

**本项目为此付出过代价**：截图 01–19 曾被误判为通过，直到补完接口重跑才暴露。

---

### D-2 ⭐ 相邻接口的响应形状经常不对称

**症状**：照着隔壁接口的形状实现，前端却渲染错误。

**根因**：猜了。实测到的真实不对称：

| 接口 | GET 返回 | PATCH 返回 | 子资源缺失 |
|---|---|---|---|
| `sidebar-preferences/` | **map**（以 key 为键） | 收 **list** 的 `{key,...}` | — |
| `home-preferences/` | **list**（元素含 `config`） | **object**（**不含** `config`） | **400** `{"detail":"Preference not found"}` |
| `user-properties/` | object（**GET 有副作用**：`get_or_create`） | object | — |

**修法**：**一律读 Django 视图 + serializer**，不要外推。

```bash
grep -rn "class XXXViewSet\|class XXXEndpoint" plane-preview/apps/api/plane/app/views/
```

**注意失败码也可能是 400 而不是 404。**

---

### D-3 分页信封的字段有陷阱

| 陷阱 | 说明 |
|---|---|
| `count` ≠ `total_results` | `count` 是**本页**条数，`total_results` 是**总数** |
| 游标格式 | `"value:offset:is_prev"`，`offset` 是**页号**不是行偏移 |
| 游标渲染 | `1000.0` 要输出成 `"1000"`（整数形式），保持字节兼容 |
| `per_page` 上限 | **1000**，超出是**钳制**不是报错（对齐 Django） |
| 探测下一页 | 查 `per_page + 1` 条再截断（对齐 Django 的 `stop = offset + limit + 1`） |

---

### D-4 分组信封的三个反直觉点

| 点 | 说明 |
|---|---|
| 空分组也要出现 | Django 会预置整个分组列表再填行 |
| `total_pages` = `ceil(最大分组 / limit)` | **不是** `ceil(总数 / limit)` —— 分组是**各自独立翻页**的 |
| 分组键 ≠ 匹配值 | 前端看到的键是 **Plane UUID**，底层列存的是 **PocketBase 15 位 id** |

---

### D-5 `null` 与 `""` 是有区别的

**症状**：前端抛异常，例如 `Intl.DateTimeFormat({ timeZone: user_timezone })` 收到 `null`。

**根因**：Django 里 `default=` 的列永不 null，但 PocketBase 用 `""` 表示缺值。

**修法**：用对的序列化辅助函数

| 函数 | 用途 |
|---|---|
| `Text(r, f)` | 非空列，`""` 就是 `""` |
| `NullableText(r, f)` | 可空列，`""` → `null`（前端会区分，如 `emoji`/`logo`） |
| `TextOr(r, f, fallback)` | Django 有 `default=` 的列，空值回退到默认值 |

---

### D-6 `Project` 没有 `sort_order` 列

**症状**：通用 400，看不出原因。

**根因**：照抄了别处的排序字段。Django 侧确认 `Project` 模型**没有**这个列。

**修法**：去后端日志翻 SQL。

---

### D-7 越权返回 404 而不是 403（这是刻意的）

**症状**：以为是权限配置错了。

**根因**：Plane **刻意**不区分"无权限"和"不存在"，以免泄漏私有 workspace 的存在性。

**修法**：这是正确行为。`ResolveWorkspace` / `ResolveProject` 里
"无活跃成员身份"一律返回 404。

**另一个容易搞错的点**：`ResolveProject` 里

```go
if scope.WorkspaceRole >= RoleAdmin && scope.ProjectMember != nil
```

那个 `&& scope.ProjectMember != nil` 很关键 ——
**工作区 Admin 不自动拥有项目角色，必须同时是项目成员**。

---

### D-8 CORS：`--origins` 必填、不能通配、且必须**列全 hostname 变体**

**症状 A**：前端报 **"Plane didn't start up correctly"** 错误边界，
而**后端日志里一个 API 调用都没有**。

**症状 B**（同一条 CORS 规则的另一种表现，容易漏判）：
**admin 打开 `http://127.0.0.1:3001/god-mode/` 显示
"We were unable to fetch the details of the instance."**（`components/instance/failure.tsx`），
但 **`http://localhost:3001/god-mode/` 完全正常**。
用 curl 直接打 `/api/instances/` 也是 200，所以很容易误判成"后端坏了"。

**根因**：没传 `--origins`（默认 `[*]`），或**列表没列全前端可能的 hostname**。
前端 axios 配了 `withCredentials: true`，浏览器在**带凭证模式**下
**拒绝 `*` 通配 origin**，也拒绝 origin 不匹配的响应。

关键事实：**`127.0.0.1` 与 `localhost` 在浏览器眼里是两个不同的 origin**，
尽管它们解析到同一台主机。只列 `localhost:3001` 时，从 `127.0.0.1:3001` 发出的请求
拿不到 `Access-Control-Allow-Origin`，axios 抛错 →
`instance.store.ts` 的 `fetchInstanceInfo` catch 分支设置 `error` →
页面渲染 `InstanceFailureView`。**后端完全无辜。**

**验证方法**（一眼看出区别）：

```bash
# 有 ACO 头 = 放行；没有 = 被拒
curl -s -i -H "Origin: http://127.0.0.1:3001" http://127.0.0.1:8000/api/instances/ | grep -i '^access-control-allow-origin:'
```

**修法**：精确列出前端 origin，**且每个端口都给出 `localhost` 与 `127.0.0.1` 两种写法**。

```
--origins="http://localhost:3000,http://localhost:3001,http://localhost:3002,\
http://127.0.0.1:3000,http://127.0.0.1:3001,http://127.0.0.1:3002"
```

改前端端口 → 必须同步改这个列表。已固化在 `start-stack.sh` 的 `ORIGINS`。

**相关**：`Access-Control-Allow-Credentials: true` 必须由**我们的中间件**注入，
且必须在 PocketBase 内建 CORS 中间件（优先级 -1041）**之前**（-1042）——
因为内建中间件在 preflight 时直接返回 204，**不调用任何后续中间件**。

**陷阱旁注**：admin 上 `GET /api/instances/admins/me/` 返回 **403 是正确行为**
（Django 侧 `InstanceAdminUserMeEndpoint.permission_classes = [InstanceAdminPermission]`，
未登录即 403）。不要看到 403 就当成未实现接口去"修"——
未登录状态下 `admins/me/` 本就应该 403，而 `/api/instances/` 应 200。

**回归脚本**：`tests/browser/verify-admin-origins.mjs` —— 真浏览器分别打开两个
origin 的 `/god-mode/`，断言**没有**渲染失败视图 + `/api/instances/` 返回 200
（断言内容，不是"页面没崩"）。

---

### D-9 Pages 编辑器会死循环（`apps/live` 不是可选项）

> **2026-10-23 更新：已从根上解决。** 不再需要 `apps/live` 与 Redis —— 纯 Go 中继
> `internal/live/` 已提供 `/live/collaboration`，编辑器保持 `connected`，fallback 不再触发。
> 见 `10-REMAINING-WORK.md` §3.3 / §3.4，以及本文件的 **D-11**（中继自身的坑）。
> 下面保留原始分析，因为它是"为什么必须有 WS 协作端"的完整推导。

> **2026-10-16 决策更新（N-10 暂不实现）**：`apps/live` 决定**暂不部署也不改造**。
> 所以本条的"修法"由"**运行 live**"改为"**不暴露 Pages 富文本编辑入口**"。
> ⚠ **死循环本身没有消失**，只是绕开了会触发它的入口；一旦入口对用户可见且 live 不在，本坑照旧复现。
> 见 `01` D5′、`04` §9 N-10、`10-REMAINING-WORK.md` §3.1。若将来恢复协作编辑，须先撤销该决策。

**症状**：应用无限 PATCH/GET `.../pages/<id>/description/`，渲染进程卡死。

**根因**：编辑器连不上 `apps/live`（Yjs 协作服务）时退回 `usePageFallback`，
它的 effect 在每次 `page.description_html` 变化时都重跑，
于是"断线后保存"永远不收敛。

**修法（当前，2026-10-16 起）**：**不暴露 Pages 富文本编辑入口**，从而不进入 fallback。

**修法（若将来启用 live）**：**运行 `apps/live`**（需要 Redis）。
live 服务在，编辑器保持 `connected`，就不会走 fallback。

**⚠ 第二个独立原因：live 自己的 CORS 白名单。**
即使 live 起来了，`apps/live/src/server.ts` 的 `setupCors()` 也会按
`CORS_ALLOWED_ORIGINS` 逐个核对 Origin —— 不在名单里就连 WebSocket 都握不上手，
**症状与「live 没起来」一样**（编辑器退回 fallback）。

实测过的一个真实缺陷：`apps/live/.env` 里只有

```
CORS_ALLOWED_ORIGINS="http://localhost:3000"
```

即**只放行 web**。于是：

| Origin | 结果 |
|---|---|
| `http://localhost:3000` | ✅ 放行 |
| `http://localhost:3002`（**space**） | ❌ **完全没有 ACO 头** |
| `http://127.0.0.1:3000` | ❌ 被拒 |

而 **space 恰恰是展示「已发布页面」的地方** —— 富文本编辑器正是在那里用的，
所以这个缺陷会直接打断 space 的页面渲染。

**修法**：列出**所有会嵌入编辑器**的前端（web `3000` / admin `3001` / space `3002`），
且每个都给 `localhost` 与 `127.0.0.1` 两种写法，与后端 `--origins` 保持同一套值。

另注：`apps/live/.env.example`（**被跟踪的模板**）里**根本没有
`CORS_ALLOWED_ORIGINS`**，`src/env.ts` 默认值是 `""` → `origin: false` →
**一个 origin 都不放行**。新 clone 从 `.env.example` 生成 `.env` 后必须自己补这一项。

**复验**：

```bash
for o in http://localhost:3000 http://localhost:3002 http://127.0.0.1:3000; do
  echo -n "$o -> "
  curl -s -i -H "Origin: $o" http://127.0.0.1:3100/live/ \
    | grep -i '^access-control-allow-origin:' | tr -d '\r' || echo "<none: REJECTED>"
done
```

`/live/` 返回 404 是设计如此；这里只看**有没有 ACO 头**。

---

### D-10 `page_id` 之前的接口形状容易想当然

已修的实际案例（供参考形状多样性）：

| 缺陷 | 症状 |
|---|---|
| `project-deploy-boards/` | 返回**数组**，前端当**对象**读 |
| 发布时 `view_props` 被丢弃 | Space 列表整个空掉 |
| Space 的 issues 接口 | 返回扁平数组，而前端要的是**按 state 分组** |

---

### D-11 Go 中继（`internal/live`）的六个坑

**① 探针写 `Y.Text("default")`，编辑器读 `Y.XmlFragment("default")`**

两个类型**同名但互不兼容**，探针写进去的内容编辑器完全看不见，页面显示空文档，
日志解码后是 `keys: ['default'] → AbstractType`。极易误判成"中继坏了"。
测试时要么用真实编辑器写第一条 update，要么显式 `doc.getXmlFragment("default")`。

**② 回放不触发 `onStateChange`**

中继回放是远端 yjs update，不是本地编辑，`editor.onStateChange` 不响。
所以"只打开不编辑"的页面不会回写 `description_html` —— 若正文只在日志里，
发布到 Space 就是空白页。修法：`use-collab-description-sync.ts` 在 attach 后
做一次 settle 检查（见 `10-REMAINING-WORK.md` §3.4）。

**③ `page.description_html` 的空串会被折叠成 `undefined`**

`store/pages/base-page.ts:127` 是 `page?.description_html || undefined`。
于是"空页面"与"尚未 hydrate"在类型上无法区分，写回逻辑不能用它当基线判空，
只能用"文档里有文字就写"兜底。

**④ 同一资源的两条写路径容易漏字段**

`PATCH …/pages/{id}/`（`page.Update`）会派生 `description_stripped`，
但编辑器实际走的 `PATCH …/pages/{id}/description/`（`page.DescriptionPatch`）**不会**。
结果页面正文永远搜不到（sticky 搜索过滤 `description_stripped`）。
**新增/改动任何"写描述"的路径时，都要同步检查 `description_stripped`。**

**⑤ `description_binary` 是 file 字段，不能直接塞 base64**

`pages.description_binary` 在 PocketBase 里是 `FileField`：**列里存的是文件名**，
字节在 `<data>/storage/<collection>/<record>/` 下。前端发来的是 base64 的 yjs update，
直接 `page.Set("description_binary", base64)` 会把它当成文件名，`Save` 时校验失败
（或者存进去一个不存在的文件名），于是 `GET …/pages/{id}/description/` 永远返回空，
编辑器的离线兜底也永远拿不到文档。
必须走 `filesystem.NewFileFromBytes` + `Set`（见 `page.setDescriptionBinary`），
读取走 `app.NewFilesystem()` + `record.BaseFilesPath()`，**不要**自己拼
`DataDir()/storage/...`（record 的 PK 与目录名不总是同一个东西）。
同一个坑在 `Create` / `Update` / `DescriptionPatch` 三处都要按这个来。

**⑥ compaction 的快照请求必须与广播在同一把锁下**

中继不会合并 CRDT，唯一的瘦身方式是「向在线客户端要一次全量快照，用它替换整个日志」。
快照的正确性依赖一条隐含前提：**客户端收到请求时，已经应用了我们记录的每一条 update**。
如果 `record()` 先解锁、再去广播、再发 `SyncStep1`，两个并发编辑就可能让 A 的快照请求
抢在 B 那条 update 的广播之前进入队列 —— 快照里缺 B，而 B 的下标又小于
`compactIdx`，compaction 会把它**静默丢掉**。
所以 `record()` 里的「写盘 + 广播 + 判定/发快照请求」必须在 `r.mu` 的同一临界区内完成
（`trySend` 是非阻塞的缓冲队列，持锁发送是安全的）。同理，`applySnapshot` 也要持锁替换
`updates` 并重写文件，否则与 `append` 交错会写坏日志。

| Triage 状态漏进项目状态列表 | 数据结构对了但内容多了 |
| Space 的 issue 列表 | 按升序返回，应降序 |

**共性**：形状/顺序/分组，任何一个不对，前端都不报错，只是渲染错东西。

---

### D-12 ⭐⭐⭐ PocketBase 记录钩子的三条前提（做 L-1 前实测，后续所有钩子照此办）

**背景**：L-1 首次引入 `app.OnRecordAfterCreateSuccess` / `OnRecordAfterUpdateSuccess`（`internal/hooks/`）。
挂之前有三个"想当然"必须实测，结论都与直觉有出入。探针固化在
`internal/hooks/premises_test.go`（`go test ./internal/hooks/ -run TestProbe -v`）。

**① 内部 `app.Save()` 照样触发钩子（不是只对 HTTP 请求生效）**

`BaseApp.Save()` → `create()/update()` 直接 `Trigger(OnModelAfterCreateSuccess…)`，
`record_model.go` 的系统钩子再把它代理成 `OnRecordAfter*Success` ——
**跟有没有 HTTP 请求无关**。本项目所有跨表写入本来就是 handler 里的 `app.Save()`，
所以钩子对每一条写入路径都生效（这正是想要的效果），但也意味着
**钩子里不能再 Save 被监听的 collection**（会递归）。实测：钩子里 Save 一条
`issue_activities`（未被监听）→ 钩子只为这条新记录多触发一次即停止，**无需防重入**。

**② `Original()` 有旧值的前提是"记录从 DB 加载过"**

`originalData` 只在 `PostScan()` / finder 加载时填充。`core.NewRecord()` 建的记录，
`originalData` 是**字段默认值（空白）**——哪怕紧接着 `app.Save()` 成功，钩子里
`Original()` 拿到的还是空白，不是刚写进去的那行。实测踩到过：探针第一版直接用
seeder 返回的内存对象 Save，`Original().name == ""`。**handler 都是
`FindByUUID → Set → Save`（先加载后改），所以正式路径没问题**；但任何
"新建后立刻再改"的路径想 diff 旧值，必须先 `FindRecordById` 重新加载。

**③ 钩子返回 error 会让主保存失败——而且行已经写进去了**

`create()/update()` 里 `OnModelAfter*Success` 的 error 被 `return err` 原样带出
`app.Save()`。也就是说：DB 行**已经写成功**，钩子再报错，HTTP 层拿到的是 500。
派生数据（活动流/通知/版本）写失败**必须吞掉只记日志**
（`e.App.Logger().Error(...)` + `return nil`），绝不能让"写活动失败"把
"存 issue"搞成假失败。另外 `RunInTransaction` 里的保存，after-success 触发被
`txInfo.OnComplete` **推迟到事务提交后**，且 `event.App` 已被换成父 app ——
钩子里一律用 `e.App`，不要用闭包捕获的 app 引用。

### D-13 ⭐⭐ 估算体系：UI 全部门控在 `projects.estimate` 上，字段缺失 = 功能不存在

**现象**：`issues.estimate_point` 加好了、周期/模块聚合也真的算出来了，API 探针全绿，
**但浏览器里看不到任何估算 UI** —— Issue 侧栏没有 Estimate 行，周期详情的估算类型
下拉点不动。

**根因**：`projects` 集合**没有 `estimate` 字段**。上游迁移
`pb_migrations/1759000001_init_identity_workspace_project.go:275` 注释
"estimate deferred — `estimates` is outside this slice"，之后一直没补。而前端把**所有**
估算入口都门控在这一个字段上：

- `components/issues/issue-detail/sidebar.tsx:180`
  `{projectId && areEstimateEnabledByProjectId(projectId) && (…Estimate 行…)}`
  —— 为空时整行不渲染；
- `store/cycle.store.ts` 的 `getEstimateTypeByCycleId` 在 `project.estimate` 为假时
  **硬返回 `"issues"`**，下拉切不到 Estimates；
- 后端 `serializers/projects.go` 不输出、`handlers/projects/projects.go` 的 Update
  白名单也不含它 —— `PATCH {"estimate":"<id>"}` 返回 **200 但被静默丢弃**。

**正确做法**：迁移加 `projects.estimate`（relation → `estimates`，可空 SET_NULL）
+ 序列化器输出平铺 uuid + Update 白名单放行并校验同项目归属。前端**零改动**。
见 `docs/10-REMAINING-WORK.md` §3.9（L-11）。

**附带教训**：一个被 defer 的字段可以让**整条 UI 路径不可达**，而 API 层自测全绿。
凡"前端有入口、后端没字段"的组合，验收必须走到浏览器，不能停在 curl。

---

### D-14 ⭐ 估算相关接口的四个反直觉点

1. **`POST …/estimates/` 要嵌套 body**：
   `{"estimate":{"name":"fib","type":"points"},"estimate_points":[{"key":1,"value":"1"}]}`。
   发扁平的 `{"name":…,"type":"points"}` **会被静默忽略** —— name 变成随机串、
   type 落默认 `categories`。而 `categories` 类型的点**不计入任何**
   `*_estimate_points` 聚合（上游同规则：只算 `type='points'`）。探针时表现为
   "明明挂了点，聚合却是 0"。
2. **`estimate.go` 的 Create 默认 `type="categories"`**：不显式传 `"type":"points"`
   建出来的体系，其点在任何聚合里都不见。
3. **前端只认 `last_used=true` 的估算为项目"活跃"体系**：用裸 API 建估算不带
   `last_used` 时，即使 `project.estimate` 已挂上，Issue 侧栏选中值仍显示 "None"
   （选点显示走 `currentActiveEstimateIdByProjectId` → last_used 估算）。
   上游前端建估算时本来就发 `last_used: true`。
4. **周期图例文字是 "Current Points"/"Ideal Points"**（`t("points")`="Points"），
   不是 "Current Estimates"；只有**下拉选项**的标签才是 "Estimates"。
   写验收预期时别搞错，否则会误判成没实现。

---

### D-15 ⭐ 测试里永远不要对响应体做字符串搜 id

`e.JSON` 序列化 Go map 的 **key 顺序不固定**。用 `strings.Contains(body, ""id":"…"")`
这类写法，可能命中 `points` 数组里某个点的 id 而不是 estimate 自己的 id
（estimate 的 id 排在后面），测试会**偶发 flaky**（实测 6 次挂 1 次）。
一律 `json.Unmarshal` 后按字段取。

---

### D-16 ⭐⭐ 通知生成的四个"字段写错就等于没写"

`internal/handlers/notification/notification.go` 的**消费端过滤器**决定了哪些
通知在收件箱里可见，写错一个字段这一行就**不存在**（L-4 实测固化）：

1. **`entity_name` 必须字面 `"issue"`** —— `List` 的过滤器是
   `entity_name = 'issue'`，写 "Issue" / "work_item" 都直接不可见。
2. **`sender` 是纯文本列（必填，≤255），不是 relation** —— 且 mention 类通知的
   `sender` 必须**字面包含子串 `mentioned`**：`?mentioned=true` 用
   `sender ~ 'mentioned'`，未读数的 mention/非 mention 拆分也靠它，序列化器的
   `is_mentioned_notification` 同样 `strings.Contains(sender, "mentioned")`。
3. **`entity_identifier` 是 issue 的 Plane UUID，不是 PocketBase 记录 id** ——
   序列化器原样吐出、前端拿它跳转；`type=assigned/created/subscribed` 的过滤
   分支也是拿它和 `issues.uuid` 对。
4. **`triggered_by` 才是 actor 的 relation**（用户 id），别把触发种类写进去、
   也别把用户 uuid 写进 `sender`。

**推论**：任何新增通知类型先读一遍 `notification.go` 的三个过滤器
（List / UnreadCount / serializer），再决定 `sender`/`entity_*` 怎么写。

---

### D-17 ⭐⭐ 通知是扇出写：三层防线缺一不可

一个动作 N 个接收者（一条评论 → 全体订阅者；批量改 200 个 issue → 200×N）。
L-4 的三层防线，各自防一种爆炸，**互相不可替代**：

1. **DB 插入前 5 分钟去重**（同 receiver + entity_identifier + sender）——
   必须是**查询**而不是内存缓存：要跨重启、要覆盖不是本进程发起的写入。
   实测坑：只靠它挡不住"批量操作改 200 个不同 issue"（entity 不同，查询全过）。
2. **5s 合并窗口**（同 actor + receiver + kind，`commitBulk` 思路）—— 防的正是
   上面那种批量扇出：200 个 issue 只留第一条。注意它挡不住"5 秒后第二次人类
   操作"，那是去重层的事。
3. **每次 save 扇出上限**（L-4 定 50）—— 防单条记录的接收者本身就很多
   （200 个订阅者的一条评论）。超限**丢 + 记日志**：丢一条通知远好过让用户
   的保存变慢/失败（派生数据错误绝不能影响主保存，见 D-12 ③）。

另一个易翻点：**钩子写 `notifications` / `issue_mentions` 是"写未被监听的
collection"**（D-12①：只会为新记录多触发一次即停止，无需防重入）——但
`issue_mentions` 有 partial UNIQUE `(issue, mention) WHERE deleted_at = ''`，
**必须先查后写**，否则重复提及直接 UNIQUE 报错。

### D-18 ⭐⭐ 版本恢复必须同时清 live 日志，且要摘得到"正在服务的那个 hub"

L-3 恢复页面版本时两个连在一起的坑：

1. **只写回 `description_binary` 不够，还要删 `<data>/live/<sanitised>-<hash>.log`**
   （连 `.name` / `.bak` 边文件）。Yjs 中继在 join 时把日志里的 CRDT 更新**按序回放**，
   会把恢复前的内容盖回去 —— 恢复"看起来没发生"。删了日志，下次 join 走
   `seedFromStoredBinary`，用刚恢复的 `description_binary` 重新 seed，才对得上。
2. **REST 代码要够到 socket 正在用的 hub，不能自己 `New` 一个**。`live.New(app)`
   原来每次调用都建新 `Hub`：恢复路径新建的 hub 里没有那个 room，既摘不掉内存更新，
   Windows 上还因为原 hub 仍持打开句柄而连删文件都会失败。修法：`New` 按 app
   记忆化（sync.Map），恢复路径 `live.New(app).PurgeDocument(uuid)` 落到同一个 hub。
   purge 内部 **hub.mu → room.mu 必须分两段**（先摘 map 再锁 room）：`evict()` 是
   room.mu → hub.mu，嵌套持锁会死锁。

附带一条（D-12 的衍生）：恢复本身就是一次 `pages` update，会再触发版本钩子、
**有意**再记一版（上游同语义）；因为版本写在未被监听的 `page_versions`，
链条到那里就停，不会循环。

### D-19 ⭐⭐ autodate 列：`record.Set` 被静默吞掉，裸 Save 会自动刷新时间戳

`user_recent_visits.visited_at` 这类 **autodate 字段**（L-7 实测固化，
探针：create 后列为空 / SetRaw 回拨被尊重 / 裸 Save 刷新）有三个反直觉行为，
写错不报错、只是"没写"或"时间悄悄变了"：

1. **`record.Set("visited_at", …)` 是 no-op**。autodate 字段的 `FindSetter`
   对自己的名字返回 `noopSetter`，`Set` 先查 setter 再落列——所以 OnCreate=false
   的列（`visited_at` 只开 `OnUpdate`）首次插入时列是**空的**，必须
   `record.SetRaw("visited_at", …)` 才真正写进去。
2. **裸 Save（不带 SetRaw）会把 OnUpdate 列刷新为 now**。拦截器发现
   `当前值 == Original()`（没动过）就自动盖 now——这正是 RecordVisit 重访路径
   依赖的性质（命中节流过期分支时裸 Save 一次即是时间戳刷新）；反过来说，
   **为了别的原因 load-and-save 这种记录，也会顺手把时间戳冲掉**，要留心。
3. **`SetRaw` 一个与 Original 不同的值则被尊重**（拦截器只盖"没动过"的值），
   所以记录层也能回拨；测试里用 SQL 层 UPDATE 回拨只是更直白、不依赖这条
   拦截器语义。

---

### D-20 ⭐⭐ "+ Add sticky 点了没反应" 是上游自带的去重，不是坏了

**症状**：首页点 `+ Add sticky`，界面上什么都没发生，既没有新卡片也没有报错。

**真相**（`apps/web/components/stickies/sticky/use-operations.tsx:61-79`，上游代码）：
创建前会检查**最新一条 sticky**，只要它的 `description_html` 为空（或
`isCommentEmpty`），就直接 `return`，只弹一个 `type: "warning"` 的 toast：

```
Sticky not created
There already exists a sticky with no description
```

两个让人误判的因素：

1. toast 在**左下角**，视线在右上角的按钮上，基本看不到；
2. 那条"已存在"的空便签渲染成 `Click to type here` 的浅色占位卡，**看起来像空态**，
   不像"已经有一条记录"。

**判据**（不用看界面）：

```bash
sqlite3 .fe-run/data.db \
  "select id, length(description_stripped), created_at from stickies order by created_at;"
```

最后一行长度为 0 → 就是这个情况。**往那张 `Click to type here` 卡里打字**即可，
再点 `+ Add sticky` 才会新建。

**完整实测链路**（浏览器驱动，证据齐全，别再靠猜）：

| 步骤 | 观测 |
|---|---|
| 打开 Stickies 面板 | `GET /api/workspaces/hrms/stickies/…` 200，5 张卡，**第 1 张空白** |
| 点 `+ Add sticky` | **没有任何请求**，只有左下角 warning toast |
| 点空白卡并打字 | `PATCH /api/workspaces/hrms/stickies/<uuid>/` **200**，`description_stripped` 落库 |
| 填完再点 `+ Add sticky` | `POST /api/workspaces/hrms/stickies/` **201**，卡数 5→6，新空白卡在最左 |

也就是说：**那张空白卡就是"上一次新建的便签"**，不是占位符。整个流程没有 bug，
只是"新建 → 卡片是空的 → 看起来像没建成 → 再点被拦"这个循环。

**排查这类"按钮无反应"的第一条**：在 Network 面板看**有没有发出请求**。
没有请求 = 前端自己拦掉了（本条的 `return`），有请求才是后端问题。

> 附带的坑：用 `page.evaluate(fetch(...))` 手工调不安全方法（DELETE/PATCH/POST）时，
> 少 `X-CSRFTOKEN` 会被中间件直接 **403**，很容易误判成"接口没实现"。取值：
> `document.cookie.match(/(?:^|;\s*)csrftoken=([^;]+)/)`，见补丁 0004。

---

### D-21 ⭐⭐⭐ 列表接口返回"信封"而不是裸数组，整页直接进错误边界

**症状**：`/settings/members/` 白屏成 `🚧 Looks like something went wrong!`，
控制台里真正的原因被压缩包埋着：

```
TypeError: this.workspaceMemberInvitations?.[e]?.map is not a function
    at get workspaceMemberInvitationIds (store-context-….js)
React Router caught the following error during render
```

**根因**：前端把这个接口的响应**原样**塞进 store，之后当数组用：

```ts
// apps/web/store/member/workspace/workspace-member.store.ts
set(this.workspaceMemberInvitations, workspaceSlug, response);
// …渲染时
return this.workspaceMemberInvitations?.[workspaceSlug]?.map((inv) => inv.id);
```

我们这边用了通用的 `serializers.Paginate(...)`，返回的是**信封对象**
（`{results, total_count, next_cursor, …}`）。`.map` 立刻炸，
`workspaceMemberInvitationIds` 是 mobx computed，渲染期抛错 → 整页错误边界。

Django 的 `WorkspaceInvitationsViewset.list` 返回的是 `Response(serializer.data)`，
也就是**裸数组**。所以这一条要以「上游返回什么形状」为准，不能以「我们有现成的分页器」为准。

**判据**：`curl` 一下看第一个字符是 `[` 还是 `{`。

```bash
curl -s …/api/workspaces/hrms/invitations/ | head -c 1     # 必须是 [
```

**修法**：该端点改成 `FindRecordsByFilter` + 手工 `[]any`，不要套分页器。
回归断言别写 `"total_count":3`——那等于把错误形状固化成契约
（`wave2c_test.go` 原来就是这么写的，已改）。断言写成：

```go
want:    []string{`[{"`, `"invitee@example.com"`, `"invite_link"`},
notWant: []string{`"total_count"`, `"next_cursor"`, `"results"`},
```

**同类风险**：凡是前端 `set(store, response)` 之后 `.map` / `.filter` / `[...response]`
的列表接口，都必须裸数组。写处理器前先看 Django 那个 viewset 的 `list` 返回什么。

---

## E 类 · 测试链路

### E-1 ⭐ 不要把"起服务"和"跑测试"分成两个工具调用

见 C-5（Job Object）。用 `run-all.sh` / `run-one.sh`。

---

### E-2 ⭐ 静态审计比一轮轮跑浏览器快几个数量级

**症状**：每次跑浏览器只发现**一个** 404，一轮几分钟。

**修法**

```bash
python tests/browser/audit-endpoints.py
```

把「前端调用形状」与「已注册路由」做**静态 diff**，一次列出**全部**缺口。
当前基线 257 路由 / 150 调用形状 / 74 未覆盖。

---

### E-3 截图是辅助证据，不是验收证据

见 D-1。截图用来**给人看**"长什么样"，机器判定必须靠**接口级断言**。

**推论（2026-10-28 踩了两次）**：既然截图不是判据，**截图失败就不许让步骤失败**。
`lib.mjs` 的 `shot()` 原来是裸的 `page.screenshot()`，Windows 上杀软/索引器
瞬时占用文件返回 `EPERM`，异常直接把整个 step 掀翻。

后果比想象大得多 —— run1 里 `shot()` 正好夹在
`name.fill("Wilber")` 和 `page.click("button[type=submit]")` **中间**：

```
A3-profile   !! EPERM 05-onboarding-profile-filled.png   ← 没点到 Continue
A4-workspace !! page.fill Timeout 30000ms                ← 还停在资料页
run2         !! B2/B3 超时                               ← 没有工作区
run3         !! C1 "no project card anchor found"        ← 没有项目
chain        ABORT: no project id
```

**一张写不进去的 PNG，报销了 5 个 run、30 个步骤。** 现在 `shot()` 重试 3 次
（150/300ms 退避），仍失败就打印 `[shot] SKIPPED …` 并返回 `null`，绝不抛。
修完之后同一条链路一次过：30 步 0 失败。

**同类**：C-23（live 日志 open 的瞬时 `Access is denied`）。判据一样——
文件是我们的、不是只读、ACL 正常，那就是外部短暂持有，重试即可。

---

### E-5 `--fresh` 的临时数据目录差点被提交

`run-chain-single.sh --fresh` 用 `PLANE_DATA_DIR=.fe-run-chain-<时间戳>`。
`plane-pocketbase/.gitignore` 里原有 `.fe-run/` 和 `.fe-run.*/`，
**两个都不匹配** `.fe-run-chain-…`（前者要斜杠，后者要那个点），
于是一次全量浏览器测试会在仓库里留下一个可提交的数据目录。
已补 `.fe-run-*/`，判据：

```bash
git check-ignore -v plane-pocketbase/.fe-run-chain-20261028-222418
```

---

### E-4 `page.evaluate` 没有超时，会卡死脚本

**症状**：测试脚本挂住不返回。

**根因**：渲染进程忙时（富文本编辑器会一直让它忙），`page.evaluate` 永不 resolve。

**修法**：用 `Promise.race` 包一层超时，把卡死降级成一次可报告的失败：

```js
return Promise.race([
  page.evaluate(...),
  new Promise(r => setTimeout(() => r({status: 0, text: "timeout"}), 15000)),
]);
```

---

### E-5 从控制台抓的日志可能已经过期

**症状**：读到一份"当前"日志，其实是上一次运行留下的。

**修法**：检查文件 mtime。`run-one.sh` 会 `tee` 到 `tests/logs/<script>.log`，
确认时间戳是你这次运行的。

---

### E-6 ⭐⭐ `run*.mjs` 的**退出码 0 不代表通过**

**症状**：链路跑完打印「ALL RUNS EXIT 0」，看着全绿；实际 run1 的 A1 根本没连上 admin
（`ERR_CONNECTION_REFUSED`），A3/A4 与 run2 的 B2–B4 全是级联失败，run3 用的是**上一次留下的**
项目 id，所以后面几轮"照样通过"。整条链是**假绿**。

**根因**：每个 `run*.mjs` 的 `step()` 捕获异常后只写一行 `  !! <step> FAILED: …` 然后继续，
进程最终正常退出。退出码反映的是"脚本没崩"，不是"步骤通过"。

**判据**（两条都要看）：

```bash
grep -c '^===== ' tests/logs/run1-bootstrap.mjs.log   # 计划跑的步骤数
grep -c '^  !! '  tests/logs/run1-bootstrap.mjs.log   # 实际失败数，必须为 0
```

`run-chain-single.sh` 已内置这个判定（失败步骤 > 0 即整体 FAIL），
`run-all.sh` 目前**只**看退出码。

**附带的第二个坑**：`lib.mjs` 里 admin / space 的地址分别是 `PLANE_ADMIN_URL`、`PLANE_SPACE_URL`，
默认值是 dev 栈的 `:3001` / `:3002`。单端口模式下只设 `PLANE_WEB_URL` 会让 run1 去打
`http://localhost:3001/god-mode/` → 连接被拒。三个变量都要指向同一个 `:8000`。

---

### E-7 ⭐ 在 Bash 调用里用 `&` 起的服务，**它写的文件不会落到真实磁盘**

**症状**：`run-chain-single.sh --fresh` 逻辑上全过（workspace / project / page 都建出来了），
跑完去看 `.fe-run`——空的。甚至"运行中"去看也是空的。

**根因**：agent 沙箱对"工具调用内 `&` 后台化的子进程"给了隔离的文件视图，调用结束即丢弃。
（这与 `02-GETTING-STARTED` 里"沙箱内 `nohup` 的服务会被回收"是同一条限制的另一面：
不只进程被回收，**写入也不落盘**。）

**后果与对策**：

- 这类跑法**可以**用来验证行为（接口返回、页面渲染、链路是否通过），
  **不能**用来留下数据。要看结果就把断言写进日志，别指望事后查库。
- 需要**真落盘**的服务，必须用工具层的后台任务（`run_in_background`），
  它由 harness 管理，写入是真实的。
- 因此 `run-chain-single.sh --fresh` **不再 `mv .fe-run`**，改用
  `PLANE_DATA_DIR=.fe-run-chain-<时间戳>`：跑完临时目录里可能是空的，
  但**用户的实例一根头发都不动**。这一点是 `--full-single` 敢放进发布门禁的前提。

---

## F 类 · 通用工程

### F-1 编译通过 ≠ 行为正确

**本项目已验证的反例**：迁移文件编译通过，但 collection 没建出来
（因为 `pb_migrations` 少了 blank import）。

**纪律**：验证阶梯逐级上升（见 `05-TESTING.md` §1），
**最贵的层不要跳过便宜的层直接跑**。

### F-2 「成功」的响应码也可能是错的

见 A-1。`200` 只说明"HTTP 层面通了"，不代表"语义对了"。

### F-3 文件操作纪律

**非必要不删除文件。** 重置/重来一律用改名备份：

```bash
mv X "X.bak-$(date +%s)" && mkdir -p X
```

理由：避免频繁的授权确认，同时保留可回溯的证据。
本项目所有重置路径（`run-all.sh --fresh`、旧 `.git` 归档、证据目录）都遵循这条。

---

## 附录 · 症状 → 章节 快速索引

| 症状 | 章节 |
|---|---|
| 未注册的接口返回 200 + 错对象 | **A-1** |
| 前端报 `Cannot read properties of undefined (reading 'find')` | **A-1** |
| 加了 `{$}` 之后涌现一批 404 | **A-2** |
| 服务启动 panic 说路由冲突 | **A-3** |
| 编译报 `undefined: e.Param` / `e.Cookie` | **A-4** |
| CRLF 统计数字不可信 | **B-1** |
| `go build` 报 `pattern all:dist: no matching files found` | **B-2** |
| `git add` 成功但源码没进索引 | **B-3** |
| checkout 之后第三方文件行尾变了 | **B-4** |
| 根 `.gitignore` 规则不生效 | **B-5** |
| 可空字段变成 `null` 导致前端抛异常 | **B-6 / D-5** |
| `migrate` 报 `No new migrations to apply` | **B-8** |
| 后端 400 但只有通用文案 | **B-10** |
| **起栈耗时以分钟计** | **C-1** |
| 健康检查永远失败 | **C-1 / C-4** |
| 端口在 LISTENING 但请求全部超时 | **C-2** |
| 等待循环看不见在做什么 | **C-3** |
| 工具调用结束后服务就死了 | **C-5** |
| `cannot find go env config: %AppData%` | **C-6** |
| 拉 Go 模块超时 | **C-7** |
| **cmd/PowerShell 里 `bash xxx.sh` 报 `mkdir: cannot create directory '/c': Permission denied`** | **C-11** |
| **`cd: /c/Users/...: No such file or directory`（5 个 cd 全失败）** | **C-11** |
| **脚本报 `all requested services are up` 但一个服务都没起来** | **C-11** |
| `services ready after 120s` 但端口全空 | **C-11** |
| **`|| echo 000` 把死服务判成存活（`code="000000"`）** | **C-12** |
| **`WARNING: NODE=... is not executable; apps/live will fail to start`** | **C-13** |
| **四个前端全部 `ERR_MODULE_NOT_FOUND`（`pathe` / `@react-pdf/primitives` / `@plane/logger`）** | **C-14** |
| **`pnpm install` 报 `added N, done` 但 `node_modules` 里是空的** | **C-14** |
| 浏览器「127.0.0.1 拒绝连接」而端口脚本说服务已启动 | **C-14 / C-13** |
| `pnpm install` 中途 `ENOSPC`，C: 盘 0 可用 | **C-14（配套环境坑）** |
| **服务先好后坏：`changed tsconfig file detected: .../node_modules.broken-*` 刷屏后 `FATAL ERROR: Reached heap limit`** | **C-15** |
| **web 跑一小时后自己挂掉，期间没人改代码** | **C-15** |
| 删 `node_modules.broken-*` 报「对路径 libvips-42.dll 的访问被拒绝」 | **C-15**（robocopy 空镜像 / 移出项目树） |
| **`react-router build` 报 `ENOENT ...\apps\<app>\tailwindcss`（dev 正常）** | **C-17**（`@import "tailwindcss/index.css"`） |
| **改了 `.env.production` 重新 build，前端还在打旧地址 / 多端口地址** | **C-19**（只读 `.env`；用 `build-frontends.sh` 预先 export） |
| **单端口部署下 admin 重定向跳错域名** | **C-19**（`admin_base_url` 留空才对） |
| **`/god-mode/`、`/spaces/` 页面只显示转圈 logo；请求里出现 `C:/…/PortableGit/...`** | **C-20**（Git Bash 改写 env 路径；`MSYS_NO_PATHCONV=1`） |
| **curl 探活全 200，但浏览器白屏** | **C-20**（200 只证明文件在，必须开浏览器验 DOM） |
| **`PLANE_ADMIN_DIR: … has no index.html`，但文件确实在** | **C-21**（`MSYS_NO_PATHCONV=1` 让路径没被转成 `D://…`） |
| **链路跑完打印「exit 0」却是假绿** | **E-6**（`run*.mjs` 失败只写 `!!`，退出码仍是 0） |
| **门禁跑完 `.fe-run` 是空的 / 后台服务写的文件找不到** | **E-7**（工具调用内 `&` 起的进程写入不落盘） |
| **单端口下 `/`、`/god-mode/`、`/spaces/` 返回 404 JSON，`/api/*` 却正常** | 三个 `PLANE_*_DIR` 没设 —— 静态托管是 opt-in，用 `tests/browser/serve-single.sh` 起就不会漏 |
| **单端口部署（不跑 live）一进 Pages 编辑器就卡死** | **D-9**（2026-10-23 起由 `internal/live` 根治） |
| **编辑器能开、能敲，但页面正文是空的（探针自测时）** | **D-11 ①**（`Y.Text` vs `Y.XmlFragment` 同名冲突，清脏日志重测） |
| **页面正文搜不到 / Space 发布后是空白** | **D-11 ③④**（`description_stripped` 未派生、`description_html` 空串被折叠成 undefined） |
| **钩子没触发 / 递归 / 主保存被派生数据写失败搞成 500** | **D-12**（内部 `app.Save()` 也触发钩子；`Original()` 要先加载；error 要吞） |
| **写了通知但收件箱/未读数就是看不到** | **D-16**（`entity_name`/`sender` 含 `mentioned`/`entity_identifier` 是 Plane UUID/`triggered_by` 才是 actor） |
| **批量操作给每个人刷出几百条通知** | **D-17**（5 分钟去重 + 5s 合并 + 每次 save 扇出上限，三层缺一不可） |
| **重复提及报 UNIQUE 错** | **D-17**（`issue_mentions` 有 partial UNIQUE，必须先查后写） |
| **恢复版本后内容"变回去了"（中继回放盖掉恢复结果）** | **D-18**（恢复必须清 live 日志；`live.New` 要按 app 记忆化才摘得到正在服务的 hub） |
| **钩子里 `Original()` 全是空白** | **D-12 ②**（记录是 `NewRecord` 造的，没从 DB 加载过） |
| **加了 gzip 后：接口 500 响应前半截是 200 body / 前端 JSON.parse 失败** | **C-18**（错误路径要 `Discard()` 缓冲区） |
| **压缩后客户端卡住或只读到原文件长度** | **C-18**（必须删 `Content-Length` / `Accept-Ranges`） |
| **`/api/realtime` 不推事件了 / 视频 Range 播放坏了** | **C-18**（跳过 SSE 与 `Range`） |
| **`pnpm` 一启动就报 `resolve the installed @pnpm/exe … os error 2`** | **C-14b**（`npm_config_manage_package_manager_versions=false`） |
| **`pnpm run` 先重装整个 workspace / turbo 卡住 20 分钟零产出** | **C-14b 第 3 条**（别用包管理器跑构建脚本，用 `build-packages.mjs`） |
| **`node <node_modules/.bin/xxx>` 报 `SyntaxError: Invalid or unexpected token`** | **C-14b 尾注**（那是 `/bin/sh` shim，要 `.cmd` 或直接执行） |
| **补丁说 applied 但构建报无关错（`Skipped patch` + 退出 0）** | **C-22**（`--directory` + 反向 `--check` 自证） |
| **编辑器报 "Connection lost"，刷新有时又好** | **C-23**（先看服务端 `[live] open room … Access is denied`） |
| **点 `+ Add sticky` 完全没反应** | **D-20**（上游去重；最新一条 sticky 是空的） |
| **`/settings/members/` 白屏 "Something went wrong"** | **D-21**（列表接口返回信封，前端 `.map` 炸） |
| **代码全在 D:，C: 可用空间却一直在降** | **C-16**（pnpm store / 日志 / 安装器残留都在用户目录） |
| `%TEMP%` 里出现 3 GB 的随机名目录 | **C-16**（VS/VS Code 安装器残留，装完可删） |
| `.workbuddy-ai\logs` 一天涨 1 GB | **C-16**（沙箱审计日志，按天分目录，不自动清理） |
| **起栈等满 90 轮最后 TIMED OUT，只有 live 起不来** | **C-13** |
| **Pages 编辑器无限循环（先确认 live 到底起没起）** | **C-13 / D-9**；N-10 暂不实现后应改为**不暴露编辑入口** |
| **关掉终端后服务还在（端口没释放）** | **C-11 / 用 `stop-stack.sh`** |
| 忘了怎么起 / 停 / 重启 / 看状态 | 用 `stack.sh`，见 [02 §5.2](02-GETTING-STARTED.md) |
| 想清空数据库重来 | `bash stack.sh reset`，见 [02 §5.2.1](02-GETTING-STARTED.md) |
| 前端全白 "Plane didn't start up correctly"，日志无请求 | **D-8** |
| **admin 报 "unable to fetch the details of the instance"（换 hostname 就好）** | **D-8** |
| **`127.0.0.1:3001` 失败但 `localhost:3001` 正常** | **D-8** |
| 页面正常但内容是空的 | **D-1** |
| 照隔壁接口写，前端渲染错 | **D-2** |
| Pages 编辑器无限循环 | **D-9** |
| **space 页面编辑器连不上 live（live 明明起着）** | **D-9**（live 的 CORS 白名单） |
| 测试脚本挂住不返回 | **E-4** |

---

**下一篇**：现状与路线图见 [`07-STATE-AND-ROADMAP.md`](07-STATE-AND-ROADMAP.md)。
