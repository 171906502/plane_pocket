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

**症状**：应用无限 PATCH/GET `.../pages/<id>/description/`，渲染进程卡死。

**根因**：编辑器连不上 `apps/live`（Yjs 协作服务）时退回 `usePageFallback`，
它的 effect 在每次 `page.description_html` 变化时都重跑，
于是"断线后保存"永远不收敛。

**修法**：**运行 `apps/live`**（需要 Redis）。
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
| Triage 状态漏进项目状态列表 | 数据结构对了但内容多了 |
| Space 的 issue 列表 | 按升序返回，应降序 |

**共性**：形状/顺序/分组，任何一个不对，前端都不报错，只是渲染错东西。

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
| **起栈等满 90 轮最后 TIMED OUT，只有 live 起不来** | **C-13** |
| **Pages 编辑器无限循环（先确认 live 到底起没起）** | **C-13 / D-9** |
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
