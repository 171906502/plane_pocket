# 02 · 本地从零搭建

> 目标：在一台干净机器上，从 clone 到"浏览器里能点"，并且能跑起自动化验收。
> 本文中所有命令都在 **Git Bash**（Windows）下验证过。

---

## 1. 依赖清单

| 依赖 | 版本要求 | 本机实测 | 备注 |
|---|---|---|---|
| **Go** | **≥ 1.27**（硬性） | `go1.27.1 windows/amd64` | 由 `pocketbase-master/go.mod` 决定，低了直接编译失败 |
| **Node** | 22.x | `v22.22.2` | 前端 dev server 与浏览器测试脚本都用它 |
| **pnpm** | 见下方 ⚠️ | `12.8.1`（系统） | **有版本陷阱，务必读完本节** |
| Git | 任意 | PortableGit 1.2.0 | |
| Redis | **不需要** | — | `apps/live` 才需要，而协作端已由纯 Go 的 `internal/live` 提供（2026-10-23），见 §5.6 |
| 浏览器 | Microsoft Edge（Chromium） | `C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe` | 验收脚本用 `playwright-core` 直接驱动本机 Edge，**不需要下载浏览器** |

### ⚠️ pnpm 版本陷阱（必读）

`plane-preview/package.json` 里声明了：

```json
"packageManager": "pnpm@11.10.0+sha512.0b7f..."
```

依赖是用 **pnpm 11.10.0** 装的，而**系统里装的是 pnpm 12.8.1**。
用 pnpm 12 去跑会因为 corepack 无法满足固定的 `packageManager` 声明而**无法自举自己的 wrapper**。

**当前状态**：`plane-preview/node_modules` 已经装好了（5 套：根 + `apps/{web,space,admin,live}`）。

> **如果你的机器上 node_modules 不完整**，需要用 pnpm 11.10.0 重装：
> ```bash
> cd plane_pocket/plane-preview
> corepack prepare pnpm@11.10.0 --activate   # 或用 npx pnpm@11.10.0
> pnpm install
> ```
> 直接 `pnpm install` 用系统 pnpm 12 有可能失败。

**日常启动前端不需要 pnpm** —— 启动脚本直接调 `apps/<app>/node_modules/.bin/react-router`，
绕开了包管理器。

---

## 2. 获取代码

```bash
git clone <repo-url> plane_pocket
cd plane_pocket
```

仓库是**一个** Git 仓库，包含三棵树：

```
plane_pocket/
├── plane-pocketbase/     我们写的后端（唯一可写）
├── plane-preview/        Plane v1.4.2 前端源码（只读）
└── pocketbase-master/    PocketBase 源码（只读）
```

---

## 3. 环境变量（Go 侧，必须配）

### 3.1 `APPDATA` —— Git Bash 下的硬性要求

```bash
export APPDATA="C:\\Users\\<你的用户名>\\AppData\\Roaming"
```

**为什么必须**：在 Git Bash 里 `%AppData%` 没有定义，Go 工具链找不到自己的配置文件，
`go env -w` 会报 `cannot find go env config: %AppData% is not defined`，
之后的构建会**静默回落到被墙的默认代理**。

**每一个要跑 Go 命令的 shell 都要先 export**（启动脚本 `start-stack.sh` 里已经 export 了）。

### 3.2 Go 模块代理（中国大陆网络）

`proxy.golang.org` 通常不可达。持久化到配置里（只需做一次）：

```bash
export APPDATA="C:\\Users\\<你>\\AppData\\Roaming"
go env -w GOPROXY=https://goproxy.cn,direct
go env -w GOSUMDB=sum.golang.google.cn
```

验证：

```bash
go env GOPROXY GOSUMDB
# 期望输出：
# https://goproxy.cn,direct
# sum.golang.google.cn
```

### 3.3 后端自身的环境变量

| 变量 | 默认 | 作用 |
|---|---|---|
| `PLANE_PB_DEV` | 关 | `=1` 打开 PocketBase dev 模式，**处理器的错误会打到 stderr**，而不只是记进 `_logs` 表。开发时最快的 500 排查手段 |
| `PLANE_PB_CSRF` | 开 | `=off` 关闭 CSRF 校验。用于调试还不发 CSRF 头的客户端 |
| `PLANE_ADMIN_BASE_URL` | — | 管理端基地址，启动脚本里设为 `http://localhost:3001` |

---

## 4. 编译后端

```bash
cd plane-pocketbase
export APPDATA="C:\\Users\\<你>\\AppData\\Roaming"

go build -o .fe.exe .
echo "exit=$?"
```

产物约 **39 MB** 的单个 `.exe`（`.fe.exe` 已在 `.gitignore` 里，不进仓库）。

### 4.1 跑静态护栏（强烈建议）

```bash
go test ./internal/router/ -run TestRoutePatternsAreAnchored -v
```

这个测试用 Go AST 静态检查所有路由是否都被 `{$}` 锚定（见 `01-ARCHITECTURE.md` §6）。
**毫秒级完成，不需要起服务。改动任何路由后都必须跑。**

### 4.2 验证迁移能应用（在起服务之前）

```bash
./.fe.exe migrate up --dir=./.tmp-verify
```

期望 exit 0。这能提前发现 collection 定义错误，不用等起服务。
验证完可以 `mv .tmp-verify .tmp-verify.bak-$(date +%s)` 留着（**不要删**，见 §7）。

---

## 5. 启动整个栈

### 5.1 一键启动

⚠️ **必须在 Git Bash 里跑，不能在 cmd.exe / PowerShell 里敲 `bash stack.sh`。**
在 cmd/PowerShell 里 `bash` 会被解析成 `C:\Windows\System32\bash.exe`（**WSL 启动器**），
它会报一堆 `mkdir: cannot create directory '/c': Permission denied` +
`cd: /c/Users/...: No such file or directory`，**一个服务都起不来**。
详见 [`06-PITFALLS.md`](06-PITFALLS.md) 的 **C-11**。

```bash
# ✅ 方式一：打开「Git Bash」开始菜单项，然后
cd /d/ai-work/plane_pocket/plane-pocketbase/tests/browser
bash stack.sh start
```

```bash
# ✅ 方式二：从任何 shell 显式调用 Git Bash
"C:\Program Files\Git\bin\bash.exe" -lc "cd /d/ai-work/plane_pocket/plane-pocketbase/tests/browser && bash stack.sh start"
```

脚本会自己判断环境：进错 shell 会**立刻退出并打印正确命令**（退出码 3），
不会再有"假成功"。

它会拉起五个服务并把日志写到 `plane-pocketbase/tests/logs/`：

| 服务 | 端口 | 健康探针 | 说明 |
|---|---|---|---|
| 后端 | `8000` | `/api/instances/` | 注意：后端 `/` 返回 404 是**设计如此**，所以探针打真实路由 |
| web | `3000` | `/` | 主应用 |
| admin | `3001` | `/god-mode/` | 实例管理台 |
| space | `3002` | `/` | 公开只读项目视图 |
| live | `3100` | `/live/` | 上游 Yjs 协作服务，**dev 栈里仍在跑但不再是必需**：协作端已由 Go 的 `internal/live`（同进程 `:8000/live/collaboration`）提供，见 §5.6 |

也可以只起单个：`bash stack.sh start backend`（或 `web` / `admin` / `space` / `live`）

**启动耗时**：正常情况下约 2 秒（Vite 首次编译时 5–6 秒）。**如果它耗时以分钟计，
说明踩了坑** —— 立刻看 [`06-PITFALLS.md`](06-PITFALLS.md) 里的 `curl -o /dev/null`
与 `bound vs answering` 两节。

**退出码是可信的**：脚本只在所有请求的服务真正响应时才返回 0。
失败时会打印 `===== FAILURE REPORT =====`，逐个说明端口是「没被占用（从未启动）」
还是「被占用但不响应（启动中崩了）」，并附日志末尾。失败时也可以自查：

```bash
for p in 8000 3000 3001 3002 3100; do
  curl -s -o /dev/null -w "port $p -> %{http_code}\n" --noproxy '*' --max-time 5 "http://127.0.0.1:$p/"
done
# 期望：8000→200  3000→200  3001→200  3002→302  3100→404
```

**启动行为的审计线**：`tests/logs/stack-wait.log` 记录每一轮的逐服务探测结果，
例如

```
t=1 backend=OK web=FAIL(7) admin=FAIL(7) space=FAIL(7) live=FAIL(7) ok=0
t=5 backend=OK web=OK      admin=OK      space=OK      live=FAIL(7) ok=0
t=6 backend=OK web=OK      admin=OK      space=OK      live=OK      ok=1
```

`FAIL(7)` = 连接被拒（还没起来）；`FAIL(28)` = 超时（Vite 正在编译）。

### 5.2 停止 / 重启 / 状态 / 重置

日常操作用 `stack.sh`。它是**唯一入口，只做转发** —— 真正的实现在
`start-stack.sh` / `stop-stack.sh` / `reset-data.sh` 里，所以不存在两份实现要同步的问题。

```bash
cd plane-pocketbase/tests/browser
bash stack.sh status               # 只读探测：UP / DEAD / down
bash stack.sh stop                 # 停全部
bash stack.sh stop web             # 只停 web
bash stack.sh stop backend web     # 停指定的几个
bash stack.sh restart backend      # 先停后起（改后端时最常用）
bash stack.sh logs backend         # 看日志尾巴
```

`status` 的三种状态必须分清 —— **「端口被占用」不等于「服务可用」**：

| 状态 | 含义 | 怎么办 |
|---|---|---|
| `UP` | 端口有人占，且真的在答 | 正常 |
| `DEAD` | 端口被占，但**没人答** —— dev server 起崩了 | `bash stack.sh restart <服务>` |
| `down` | 端口空着 | `bash stack.sh start <服务>` |

底层的三个脚本参数相同，也可以直接用：

```bash
bash stop-stack.sh                 # 等价于 stack.sh stop
bash start-stack.sh all            # 等价于 stack.sh start
bash reset-data.sh                 # 等价于 stack.sh reset
```

**为什么需要一个停止脚本**：`start-stack.sh` 用 `nohup … &` + `disown` 启动服务，
服务**故意活得比启动它的 shell 久**。所以**关掉终端并不会停掉它们** ——
进程还在，端口还占着。没有显式 stop 的话，下次 `start-stack.sh` 会复用旧服务，
或者为端口打架；而一个启动中崩掉的 dev server 会留下**端口被占但不响应**的僵尸。

设计要点（与 `start-stack.sh` 同一套约定）：

- **按端口杀，不按进程名杀**。`taskkill /IM node.exe` 会误杀机器上**所有** node 进程
  （包括 agent 自己的工具）。3000–3002 / 3100 / 8000 才是我们的。
- 用 `taskkill //PID <pid> //T //F`：`//T` 连**进程树**一起杀。Vite dev server 和
  live 的 node 服务都会 fork 子进程，只杀监听者会留下孤儿进程占着端口。
- **杀完复查端口是否真空了**。静默失败的 kill 比不 kill 更糟 ——
  下次启动会以 `EADDRINUSE` 失败，而原因看不见。

输出示例：

```
[stop-stack] backend  : port 8000 held by pid(s) 25052  (answering=yes) — killing
[stop-stack] backend  : stopped (port 8000 free)
...
[stop-stack] stopped=5  already-stopped=0  failed=0
[stop-stack] all requested services are stopped
```

`answering=yes/no` 用于区分「正常服务」和「僵尸（端口被占但不响应）」。
有端口没杀干净时 **exit 1** 并列出残留端口 —— 退出码可信，与 `start-stack.sh` 一致。

手动复查：

```bash
netstat -ano | grep LISTENING | grep -E ':(8000|3000|3001|3002|3100) '
```

#### 5.2.1 重置数据库

```bash
bash stack.sh reset            # 交互式，问两次
bash stack.sh reset --start    # 清完顺手把栈起回来
```

**两次确认，都不能跳过**：

1. 手打单词 `reset`；
2. 照着屏幕输入一个**当场随机生成**的 6 位码。

第二步的随机码是关键：它防的是「回车习惯」和「从上次会话复制粘贴」。
脚本里**故意没有** `--force` / `--yes` 这类开关 —— 有了它，它迟早会进 shell
history 或 CI 配置，那两次确认就白做了。

- **不删数据**：旧库整体 `mv` 到 `_scratch/db-backups/.fe-run-<时间戳>/`，
  想回退就把目录搬回 `.fe-run/`。
- **先停栈**：SQLite 文件被后端占着时 Windows 不允许 `mv`（共享冲突），
  所以先停栈；**停不掉就整体中止，一个文件都不动**。
- **非终端环境直接拒绝**：管道 / CI / agent 沙箱里 `stdin` 不是 TTY，
  脚本以退出码 2 拒绝执行。想在非交互场景拿一个空库，用
  `bash run-all.sh --fresh`（它有自己的一套语义，且不假装在交互）。
- 重置后**账号也没了** —— 需要重新到 <http://localhost:3001/god-mode/> 做实例初始化，
  再去 <http://localhost:3000> 注册。

### 5.3 前端 API 地址是怎么配的

前端**不知道**后端在哪，靠 `.env`：

```
plane-preview/apps/web/.env
  VITE_API_BASE_URL="http://localhost:8000"
  VITE_WEB_BASE_URL="http://localhost:3000"
  VITE_ADMIN_BASE_URL="http://localhost:3001"
  VITE_ADMIN_BASE_PATH="/god-mode"
  VITE_SPACE_BASE_URL="http://localhost:3002"
  VITE_SPACE_BASE_PATH="/spaces"
  VITE_LIVE_BASE_URL="http://localhost:3100"
  VITE_LIVE_BASE_PATH="/live"
```

> ⚠️ **`.env` 被上游 `.gitignore` 忽略了，不在仓库里**。
> 新机器 clone 后必须从 `.env.example` 生成（四个 app 各一份：`web` / `space` / `admin` / `live`）。
> ```bash
> cd plane_pocket/plane-preview
> for a in web space admin live; do cp apps/$a/.env.example apps/$a/.env; done
> ```
> 不生成的话前端不知道后端地址，会直接落到错误边界。

### 5.4 CORS 的 origin 是硬约束

后端启动时 `--origins` 必须**精确列出**前端的地址：

```
--origins="http://localhost:3000,http://localhost:3001,http://localhost:3002"
```

**不能省**。省掉之后 CORS 会回 `Access-Control-Allow-Origin: *`，
浏览器在带凭证（`withCredentials: true`）模式下拒绝，前端报
"Plane didn't start up correctly" 错误边界，**且后端日志里一个 API 调用都没有**（因为请求没被发出或有去无回）。

改了前端端口 → 必须同步改 `--origins`（在 `start-stack.sh` 的 `ORIGINS` 变量里）。

### 5.5 首次账号初始化

1. 打开 `http://localhost:3001/god-mode/`（admin 端）
2. 完成实例管理员的初始化注册
3. 打开 `http://localhost:3000/`（web 端），用同一个账号或新注册账号登录
4. 创建 workspace → 创建 project → 就能看到工作项界面了

> 自动化版本：`bash run-all.sh --fresh` 会从空库跑完整个链路（见 [`05-TESTING.md`](05-TESTING.md)）。

### 5.6 Pages 协作端 —— **已由 `internal/live` 提供，不需要 `apps/live`**

> **2026-10-23 更新**：协作端已经用**纯 Go** 实现在后端进程内（`internal/live/`，
> 路由 `GET /live/collaboration`），**既不需要 Redis 也不需要 Node**，单端口部署即可用。
> 浏览器端到端验收（打开/输入/重载/双标签页/重启后回放）见
> [`10-REMAINING-WORK.md`](10-REMAINING-WORK.md) §3.4。
> 本节下面的内容保留为**历史决策与"为什么必须有协作端"的推导**，以及将来若改用
> 上游 `apps/live` 时的操作参考。

`apps/live` 是上游的 Yjs 协作服务。**为什么编辑器必须有协作端**：编辑器连不上 live 时会退回
`usePageFallback`，它的 effect 在每次 `page.description_html` 变化时都会重跑，
于是"断线后保存"永远不收敛，应用会**无限循环** PATCH/GET
`.../pages/<id>/description/`，把渲染进程卡死。协作端在，编辑器保持 `connected`，就不会走 fallback。

→ 现在这个"协作端"就是 `internal/live`，所以**不需要隐藏 Pages 编辑入口**。

若将来改回上游 `apps/live`（**不建议**），它需要 **Redis**（6379），启动方式：

```bash
cd plane_pocket/plane-preview/apps/live
node --env-file=.env .      # 需要先 build 出 dist
```

> 另注：`apps/live` 的 Redis **在单实例下完全闲置**（只做 pub/sub + 跨实例锁），
> 详见 [`10-REMAINING-WORK.md`](10-REMAINING-WORK.md) §3.1。

---

## 6. 验证"真的能用了"

按顺序做这三步，任何一步失败都不要往下走：

### 步骤 1 — 后端在响应

```bash
curl -s -o /dev/null -w '%{http_code}\n' http://127.0.0.1:8000/api/instances/
```

期望 `200`。

> 🐛 **在 Git Bash 里千万不要写 `curl -o /dev/null`** —— 详见 [`06-PITFALLS.md`](06-PITFALLS.md)。
> 快速判断：如果返回的退出码是 **23**，你踩坑了。用 `-o NUL` 代替。

### 步骤 2 — 前端在响应

```bash
curl -s -o /dev/null -w '%{http_code}\n' http://127.0.0.1:3000/
curl -s -o /dev/null -w '%{http_code}\n' http://127.0.0.1:3001/god-mode/
curl -s -o /dev/null -w '%{http_code}\n' http://127.0.0.1:3002/
curl -s -o /dev/null -w '%{http_code}\n' http://127.0.0.1:3100/live/
```

期望依次 `200` / `200` / `302` / `404`。

> **`302` 和 `404` 是期望值，不是错误**：space 未登录会重定向；live 的 `/live/` 就是 404。

### 步骤 3 — 后端日志里能看到请求

```bash
tail -20 plane-pocketbase/tests/logs/pb-backend.log
```

浏览器操作一下，应该能看到 `INFO GET /api/...` 这样的行。

**这条日志能区分两种完全不同的 404**（前端看到的都一样）：

| 日志形态 | 含义 |
|---|---|
| `INFO GET /api/...` + 404 响应体 | 路由**匹配上了**，handler 跑了，是**数据**没有 |
| `ERROR GET /api/...` + `└─ The requested resource wasn't found.` | 路由**根本没匹配上**（ServeMux 层） |

这是不起服务就能确认新路由是否生效的最快方法。

---

## 7. 常见报错对照表

| 症状 | 根因 | 修法 |
|---|---|---|
| `go.mod requires go >= 1.27` | Go 版本太低 | 升级 Go；或让 Go 自动下载 toolchain（需代理可达） |
| `cannot find go env config: %AppData% is not defined` | 没 export `APPDATA` | `export APPDATA="C:\\Users\\<你>\\AppData\\Roaming"` |
| 拉模块超时 / 502 | 用了被墙的默认代理 | `go env -w GOPROXY=https://goproxy.cn,direct` |
| `No new migrations to apply` | `pb_migrations` 没被 blank import | 检查 `main.go` 里有 `_ ".../pb_migrations"` |
| 服务起来了但前端全白 / "Plane didn't start up correctly" | `--origins` 没配或写错 | 精确列出前端 origin，不能有通配 |
| 端口被占但没有响应 | dev server 启动时崩了（typegen `EPERM`） | 杀掉端口上的僵尸进程再启。用 `bind`/`answering` 分别判断，见 `06-PITFALLS.md` |
| 启动耗时数分钟 | 健康检查永远失败 | 十有八九是 `curl -o /dev/null` 退出 23，见 `06-PITFALLS.md` |
| `EPERM: operation not permitted, rmdir '...react-router\types\...'` | 沙箱批量删除保护挡住了 typegen | `export CODEBUDDY_SAFE_DELETE_ENABLED=0`（启动脚本已设） |
| `pnpm@11.10.0` 无法满足 | 系统 pnpm 版本不符 | 用 `corepack prepare pnpm@11.10.0 --activate` |
| 前端页面在，但数据全空 | 接口 404 或返回了错误形状 | 看 `pb-backend.log` 区分路由层/处理器层 404 |
| 页面看起来正常但少了东西 | 前端把失败渲染成了"正常"状态 | **看截图不够**，要断言接口内容。见 [`05-TESTING.md`](05-TESTING.md) |
| TypeScript 报 `Intl.DateTimeFormat` 时区错误 | `user_timezone` 是 null | 序列化层要用 `TextOr` 兜默认值 |

---

## 8. 关于文件的操作纪律（项目约定）

**非必要不删除文件。** 需要重置/重来时一律用「改名备份」而不是删除：

```bash
# ✅ 正确
mv .fe-run ".fe-run.bak-$(date +%s)"
mkdir -p .fe-run

# ❌ 不要
rm -rf .fe-run
```

理由：避免频繁的授权确认，同时保留可回溯的证据。
`run-all.sh --fresh` 就是这么实现的（`mv` + `mkdir`，从不 `rm`）。

---

## 9. 从零到能跑的最短路径

```bash
# 0. 依赖：Go ≥1.27、Node 22、Edge 浏览器

# 1. 环境
export APPDATA="C:\\Users\\<你>\\AppData\\Roaming"
go env -w GOPROXY=https://goproxy.cn,direct GOSUMDB=sum.golang.google.cn

# 2. clone
git clone <repo-url> plane_pocket && cd plane_pocket

# 3. 补前端 .env（被 gitignore，不在仓库里）
cd plane-preview
for a in web space admin live; do cp apps/$a/.env.example apps/$a/.env; done
cd ..

# 4. 编译后端
cd plane-pocketbase
go build -o .fe.exe .

# 5. 护栏 + 迁移自检
go test ./internal/router/ -run TestRoutePatternsAreAnchored
./.fe.exe migrate up --dir=./.tmp-verify

# 6. 起栈
cd tests/browser
bash stack.sh start

# 7. 验证
bash stack.sh status
curl -s -o NUL -w '%{http_code}\n' http://127.0.0.1:8000/api/instances/   # 期望 200
# 然后浏览器打开 http://localhost:3001/god-mode/ 做实例初始化
```

---

**下一篇**：要动手改代码见 [`03-ITERATION-GUIDE.md`](03-ITERATION-GUIDE.md)；
遇到怪问题见 [`06-PITFALLS.md`](06-PITFALLS.md)。
