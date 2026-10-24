# 05 · 测试与验收

> **本项目最重要的一条测试原则**：
> **"页面没有崩溃"不是验收证据。** 前端会把失败渲染成看起来完全正常的状态
> （空挂件网格、礼貌的空态文案、全屏权限 gate）。验收必须断言**具体内容**。

---

## 1. 测试分层

| 层 | 手段 | 需要起服务 | 耗时 | 防什么 |
|---|---|---|---|---|
| **L1** 编译 | `go build` | ❌ | 秒 | 语法/类型错误 |
| **L2** 路由护栏 | `go test ./internal/router/` | ❌ | 毫秒 | **`{$}` 锚点漏洞**（静默 200） |
| **L3** 迁移自检 | `.fe.exe migrate up --dir=.tmp` + 读 SQLite | ❌ | 秒 | collection/字段定义错误 |
| **L4** 接口级 | `curl` + 看 `pb-backend.log` | ✅ 后端 | 秒 | 路由是否注册、响应形状 |
| **L5** 静态审计 | `python audit-endpoints.py` | ❌ | 秒 | **全局接口缺口** |
| **L6** 单页浏览器 | `bash run-one.sh <script>.mjs` | ✅ 全栈 | 分钟 | 单页真实交互 |
| **L7** 全链路 | `bash run-all.sh --fresh` | ✅ 全栈 | 十分钟级 | 端到端回归 + 30 张截图 |
| **L8** Shell 冒烟 | `tests/smoke/*.sh` | ✅ 全栈 | 分钟 | 特定子系统（instance / public api） |

**原则**：从便宜的层开始。**不要**一上来就跑 L7。

---

## 2. L2 —— 路由护栏（最重要的一层）

```bash
cd plane-pocketbase
go test ./internal/router/ -run TestRoutePatternsAreAnchored -v
```

`internal/router/patterns_test.go` 用 `go/ast` 解析整个 router 包，
断言**每一个以 `/` 结尾的路由路径字面量也以 `{$}` 结尾**。

**为什么这层不可省**：Go 1.22+ 的 `http.ServeMux` 里，以 `/` 结尾的模式是**子树模式**，
会匹配它下面的**每一条**路径。PocketBase 的路由器把组前缀和路由路径原样拼接后交给 ServeMux，
所以 `Group("/api/workspaces/{slug}")` 上的 `GET("/", h.Detail)`
会生成 subtree 模式 `/api/workspaces/{slug}/`，把**所有未注册的子路径**用
`200 + 工作区对象`回答掉。

这个测试**只需要毫秒**、不需要起服务，失败会精确指出违规行。

> **改动任何路由之后都必须跑。** 这个测试失败 = 有人引入了静默 200。

---

## 3. L5 —— 静态缺口审计

```bash
cd plane-pocketbase
python tests/browser/audit-endpoints.py
```

**纯静态分析，不需要起服务。** 它做两件事：

1. 从前端 `packages/services/src/**` 里提取所有 `/api/...` 调用形状（模板字符串、`services/**` 模块）
2. 从 `internal/router/` 提取所有已注册的 ServeMux 模式（解析组前缀，去掉 `{$}`）
3. 归一化（`${expr}` / `{param}` / `:id` 都塌缩成 `{}`）后按段数与占位符位置比对，打印差集

**这比"一轮轮跑浏览器发现 404"快几个数量级。** 当前基线：

```
registered patterns : 257
frontend call shapes: 150
UNSERVED            : 74
```

每次批量补完接口后重跑，看数字下降。

---

## 4. L6/L7 —— 浏览器工作流

### 4.1 为什么必须放在一个脚本里

`tests/browser/run-all.sh` 的注释解释了这件事，很值得理解：

> agent 沙箱把每个派生的 shell 放进一个 **Windows Job Object**，工具调用返回时整个 Job 被拆掉。
> `nohup`、`disown`、`&` **都逃不出 Job Object**，
> 所以在一个工具调用里启动的后端，到下一个调用时已经死了。
>
> 把整套服务 + 全部测试脚本放进**同一个进程树**，才能让每个服务恰好活到它被需要的时候。

**实践含义**：不要分开"起服务"和"跑测试"两步。用 `run-all.sh` / `run-one.sh`。

### 4.2 `run-all.sh` —— 全链路

```bash
cd plane-pocketbase/tests/browser

bash run-all.sh              # 复用现有数据库
bash run-all.sh --fresh      # 先把 .fe-run 挪走，从空库开始（会重新走注册流程）
bash run-all.sh --from 4     # 从第 4 个脚本开始
bash run-all.sh <PROJECT_ID> # 显式指定 project id
```

执行链：

| 脚本 | 覆盖范围 | 输出 |
|---|---|---|
| `run1-bootstrap.mjs` | admin 实例初始化 → web onboarding → 创建 workspace | `D1–A3` |
| `run2-project.mjs` | 创建项目 | `B0–B2` |
| `run3-issue.mjs` | 创建工作项（**并输出 `PROJECT_ID=`**，后续脚本靠 grep 它拿到 id） | `C1–C2` |
| `run4-project-pages.mjs` | 项目内各页面 | `D1–D5` |
| `run5-publish-space.mjs` | 发布到 Space | `E1–E5` |
| `verify-profile.mjs` | 契约回归断言套件（见 §5） | `ALL CHECKS PASSED` |

`--fresh` 的实现是 **`mv .fe-run .fe-run.pre-run-<时间戳>` + `mkdir`**，从不 `rm`（项目约定）。

#### 4.2.1 同一套 case 跑「单端口 Go 服务」

三个 origin 现在可由环境变量覆盖（默认仍是 dev 栈的 3000/3001/3002），
所以不用改脚本就能验证「前端编译后由单一 Go 二进制托管」的形态：

```bash
cd plane-pocketbase/tests/browser
. ./node-env.sh; resolve_node
export PLANE_WEB_URL=http://localhost:8000
export PLANE_ADMIN_URL=http://localhost:8000    # 脚本自己拼 /god-mode/
export PLANE_SPACE_URL=http://localhost:8000    # 脚本自己拼 /spaces/...
export MSYS_NO_PATHCONV=1 MSYS2_ARG_CONV_EXCL='*'

"$NODE" "$(cygpath -w "$PWD/run1-bootstrap.mjs")"
"$NODE" "$(cygpath -w "$PWD/run2-project.mjs")"
"$NODE" "$(cygpath -w "$PWD/run3-issue.mjs")"     # 日志末尾有 PROJECT_ID=
"$NODE" "$(cygpath -w "$PWD/run4-project-pages.mjs")" <PROJECT_ID>   # 富文本步骤，慢
"$NODE" "$(cygpath -w "$PWD/run5-publish-space.mjs")" <PROJECT_ID>
```

前提：

- 单端口服务已起（见 [04 §4.4](04-DEPLOYMENT.md)），三个 `PLANE_*_DIR` 指向 `build/client`；
- **库必须是全新的**（`is_setup_done=false`），否则 `run1` 走登录分支、`admin@hrms.local` 不存在会失败；
- `run-all.sh` **不能**用于这个形态——它会用 `start-stack.sh` 起 dev 栈（3000/3001/3002）；
- `run4` 要过富文本编辑器，单次可能 5 分钟以上，别用默认短超时。


### 4.3 `run-one.sh` —— 单点迭代

```bash
bash run-one.sh verify-profile.mjs
bash run-one.sh recon.mjs
```

只起栈 + 跑一个脚本。用于 recon → 修 → 重跑的紧凑循环，不用付 1–4 轮的钱。

### 4.4 `stack.sh` —— 起栈 / 停栈 / 重启 / 重置

```bash
bash stack.sh start   [服务...]    # 起全部（backend/web/admin/space/live），或指定的几个
bash stack.sh stop    [服务...]    # 停
bash stack.sh restart [服务...]    # 先停后起；停失败就不起（避免复用死端口）
bash stack.sh status               # 只读探测：UP / DEAD / down
bash stack.sh reset   [--start]    # 清库（两次确认 + 自动备份），见 02 §5.2.1
```

`stack.sh` **只做转发**，实现分别在 `start-stack.sh` / `stop-stack.sh` /
`reset-data.sh`；底层脚本参数相同，也可以直接用。之所以不让它自己实现一遍：
`start-stack.sh` 里塞满了踩出来的约定（CORS origins、`curl -o /dev/null` 的坑、
「崩了但端口还占着」的判定），**复制一份就等于埋一个会漂移的副本**。

`run-all.sh` / `run-one.sh` 里跑的 Node 由 `node-env.sh` 的 `resolve_node()` 解析
（不是硬编码路径）—— 托管运行时目录名带版本后缀，升级后会变，写死会让
`apps/live` 静默起不来，见 C-13。（协作端已改由 Go 的 `internal/live` 提供，
栈里不再需要 `apps/live`；此处保留为**前瞻性防护**：将来若启用 live 时脚本不会再踩这个坑。）

**单端口模式下跑这些用例**：不要用 `run-one.sh`（它会起 5 个 dev 服务，占用 8000）。
用 `run-chain-single.sh`：

```bash
cd plane-pocketbase/tests/browser
bash run-chain-single.sh --fresh          # 用临时数据目录 + 自起服务 + 跑 run1→run5 + 停服务
bash run-chain-single.sh                  # 复用已经在 :8000 上的服务（库不清空）
bash run-chain-single.sh --from 4 <PROJECT_UUID>   # 只跑 pages + publish
```

**为什么整链必须 `--fresh`**：`run1` / `run2` 是按"空实例"写的 ——
`run1` 的 A1 依赖 admin 的注册/登录表单（已登录时 `/god-mode/` 直接进 dashboard，没有表单可填），
A4 依赖 onboarding 的工作区表单（已初始化就不渲染），`run2` 的 B2 依赖空态的"创建第一个项目"入口。
在已有数据的库上跑，这几步会**真实失败**，不是回归。`--fresh` 用 `PLANE_DATA_DIR`
指向一个全新的 `.fe-run-chain-<时间戳>`，**不碰你的 `.fe-run`**；它要求 `:8000` 空闲，
且只停自己起的服务。

两个容易踩的点（详见 `06` E-6）：

1. **`run*.mjs` 退出码 0 不代表通过**——步骤失败只写一行 `  !! <step> FAILED: …` 然后继续。
   `run-chain-single.sh` 会统计 `!!` 行数，`>0` 即整体 FAIL；`run-all.sh` 目前只看退出码。
2. **`PLANE_WEB_URL` / `PLANE_ADMIN_URL` / `PLANE_SPACE_URL` 三个都要指到 `:8000`**，
   只设第一个会让 run1 去打 dev 栈的 `:3001/god-mode/`（连接被拒，然后级联失败）。
   脚本里已经替你设好。

**单端口实例上的发布前门禁**：

```bash
bash tests/release-check.sh --fast --full-single
```

`--full` 走 `run-all.sh --fresh`（起 dev 栈）；`--full-single` 走
`run-chain-single.sh --fresh`（起单端口服务），两者都是"从空库开始"的等价回归。

**`stop-stack.sh` 不是可有可无的**：服务是用 `nohup … &` + `disown` 起的，
**故意活得比 shell 久**，所以**关掉终端不会停掉它们**。没有 stop 的话，
下次起栈会复用旧服务或为端口打架，而启动中崩掉的 dev server 会留下
「端口被占但不响应」的僵尸。

停止脚本按**端口**杀（不按进程名，否则会误杀机器上所有 node 进程），
用 `taskkill //PID <pid> //T //F` 连**进程树**一起杀（Vite/live 都会 fork），
杀完**复查端口是否真空**，有残留则 exit 1。

> 📖 与之配套的坑：C-11（假成功 / WSL）、C-12（`|| echo 000` 把死服务判成活的）、
> C-13（硬编码工具链路径会随版本升级失效）。

### 4.5 recon 脚本（探测用）

`tests/browser/recon*.mjs` 是一组**侦察脚本**，不是断言套件：

| 脚本 | 用途 |
|---|---|
| `recon.mjs` | 通用页面侦察 |
| `recon-admin.mjs` / `recon-space.mjs` / `recon-profile.mjs` / `recon-publish.mjs` | 各端页面侦察 |
| `recon-workitem.mjs` | 工作项界面侦察 |
| `recon-peek.mjs` | 快速窥视某个状态 |
| `probe.mjs` / `probe-lock.mjs` | 小探针 |
| `sweep.mjs` | 批量扫页面 |
| `debug-project.mjs` | 项目页调试 |

它们的价值在于**发现之前你不知道要测什么**。遇到一个没有现成脚本的页面，
先写一个 recon 脚本把它打开、截图、打印 DOM 和网络，再决定要断言什么。

---

## 5. 验收套件应该长什么样

`verify-profile.mjs` 是范本。它的结构：

```js
let failures = 0;
function check(name, ok, detail) {
  log.log(`  ${ok ? "PASS" : "FAIL"}  ${name}${detail ? "  — " + detail : ""}`);
  if (!ok) failures++;
}

/** 在页面内 fetch，这样会带上 session cookie */
async function probe(path) {
  // page.evaluate 没有超时：渲染进程忙时（富文本编辑器会一直让它忙）
  // 调用永不 resolve，整个脚本卡死。用 race 把它降级成一次可报告失败。
  return Promise.race([
    page.evaluate(async ([url]) => {
      const res = await fetch(url, { credentials: "include" });
      const text = await res.text();
      let json = null;
      try { json = JSON.parse(text); } catch { /* not json */ }
      return { status: res.status, json, text };
    }, [url]),
    new Promise((r) => setTimeout(() => r({ status: 0, json: null, text: "timeout" }), 15000)),
  ]);
}
```

### 5.1 正确的断言 vs 错误的断言

```js
// ✅ 断言具体内容
check("GET /workspaces/{slug}/home-preferences/ returns an array",
      res.status === 200 && Array.isArray(res.json));

check("home widget seeds are present",
      ["quick_links", "recents", "my_stickies"].every(
        (k) => res.json.some((w) => w.key === k)));

check("PATCH home-preferences returns no `config` key",
      !("config" in patchRes.json));

check("PATCH home-preferences is reversible",
      after.is_enabled === before.is_enabled);

check("workspace-members/me identifies the caller",
      res.status === 200 && res.json.member?.id != null);

// ❌ 毫无价值的断言 —— 它们永远为真
check("page did not crash", true);
check("screenshot exists", fs.existsSync(pngPath));
check("status is not 500", res.status !== 500);
```

### 5.2 为什么"截图没报错"不能用

项目实测三个反例：

| 缺失的接口 | 前端表现 | 截图看起来 |
|---|---|---|
| `home-preferences/` | 首页挂件全部不加载 | 完全正常，显示礼貌文案 **"It's Quiet Without Widgets, Turn Them On"** |
| `workspace-members/me/` | 全屏 "Not Authorized!" gate 盖住整个应用 | 像一个权限问题，实际是接口没实现 |
| `user-profile/` | 页面渲染到一半崩在 `state_distribution.find(...)` | 部分内容正常 |

**结论**：截图是**辅助证据**（用来给人看"长什么样"），
**接口级断言才是验收证据**（用来机器判定通过与否）。两者都要，但后者才决定成败。

### 5.3 三个真实的"形状陷阱"（断言时必须知道）

| 接口 | GET | PATCH | 子资源缺失 |
|---|---|---|---|
| `sidebar-preferences/` | **map**（以 key 为键） | 收 **list** | — |
| `home-preferences/` | **list**（元素含 `config`） | **object**（**不含** `config`） | **400** `{"detail":"Preference not found"}` |
| `user-properties/` | object（**GET 有副作用**，会 `get_or_create`） | object | — |

**验收脚本必须为每个接口单独写断言，不能复用一个通用模板。**

---

## 6. 证据规范

### 6.1 目录

```
tests/
├── evidence/
│   ├── screenshots/           ← 30 张关键节点截图（编号命名）
│   ├── network.log            ← 浏览器实际发出的 HTTP 对话
│   ├── backend-crash-tail.txt ← 后端崩溃时的日志尾部
│   └── *.bak-<时间戳>/         ← 历史证据（改名保留，不删除）
├── logs/
│   ├── pb-backend.log         ← 后端日志（区分 INFO/ERROR 的关键）
│   ├── run*.mjs.log           ← 每个测试脚本的完整输出
│   ├── stack-wait.log         ← 起栈等待循环的逐秒记录
│   ├── run-all-wait.log       ← 全链路等待循环记录
│   └── web-dev.log / admin-dev.log / space-dev.log / live-dev.log
├── smoke/                     ← shell 冒烟脚本
└── WORKFLOW_TEST_REPORT.md    ← 主测试报告（21 个缺陷的完整叙事）
```

### 6.2 命名约定

| 类型 | 格式 | 例子 |
|---|---|---|
| 正常截图 | `NN-<描述>.png` | `10-workspace-home-clean.png` |
| 错误截图（历史） | `ERR-NN-<描述>.png` | 保留作为缺陷叙事的引用 |
| 备份 | `X.bak-<unix时间戳>` | `screenshots.bak-1790960048/` |

**46 张截图**，其中 30 张是当前有效的关键节点（15:15–15:23 由最终二进制一次性生成），
其余是历史/错误证据。

### 6.3 为什么 `pb-backend.log` 特别重要

它是**唯一**能区分两种 404 的手段（前端看到的完全一样）：

```
INFO  GET /api/workspaces/hrms/states/          ← 路由匹配上了，handler 跑了
                                                  404 = 数据没有，不是路由缺失

ERROR GET /api/workspaces/hrms/not-a-route/
└─ The requested resource wasn't found.          ← 路由根本没匹配上（mux 层）
```

**排障第一站永远是这里。**

---

## 7. 沙箱 / 环境注意事项

| 注意事项 | 说明 |
|---|---|
| **Job Object 会杀进程** | 见 §4.1。服务和测试必须在同一进程树 |
| **等待循环必须落盘** | stdout 被 `\| tail` 缓冲时循环在做什么完全看不见。`stack-wait.log` / `run-all-wait.log` 就是为此存在 |
| **`curl -o /dev/null` 会静默失败** | 详见 `06-PITFALLS.md`。用 `NUL` |
| **健康检查超时要给足** | Vite dev server 首次请求要编译，命中 ~1.3s，冷启动后更久。用 5–10s，不要用 2s |
| **「端口被占」≠「服务可用」** | dev server 可能绑了端口但已崩溃（typegen `EPERM`）。要真发 HTTP 请求判断状态码不是 `000` |
| **`wmic` 被禁用** | 进程取证只能用 `ps -W`（列：PID PPID PGID WINPID + 命令） |
| **浏览器不用下载** | `playwright-core` 直接驱动本机 Edge |
| **探针要绕过系统代理** | 机器可能挂着系统代理，localhost 必须直连，否则导航挂到超时。见 `lib.mjs` 的 `--no-proxy-server` |

---

## 8. 各服务的健康探针期望值

`run-all.sh` 里的判定标准（注意有非 200 的期望值）：

| 服务 | 端口 | 探针路径 | 期望状态码 |
|---|---|---|---|
| backend | 8000 | `/api/instances/` | **200** |
| web | 3000 | `/` | **200** |
| admin | 3001 | `/god-mode/` | **200** |
| space | 3002 | `/` | **302**（未登录重定向） |
| live | 3100 | `/live/` | **404**（设计如此） |

> ⚠️ **后端 `/` 返回 404 是设计如此**，所以探针必须打真实路由 `/api/instances/`。
> 同理 live 的 `/live/` 就是 404 —— 探针的语义是"返回了真实的 HTTP 状态"，不是"返回 200"。

---

## 9. 完整验收流程（发布前）

> **自动化脚本（2026-10-11）**：`bash tests/release-check.sh` 已把本节的静态层
> 全部串起来（build / vet / 单元测试 / 迁移自检 / 双审计基线比对），加 `--fast`
> 跳过全量单元测试（保留路由锚点护栏），加 `--full` 追加动态层
> `run-all.sh --fresh`。审计 UNSERVED 超过记录基线即判失败（基线可用环境变量
> 覆盖）。下面的手工流程保留作为脚本内部步骤的说明。
>
> **发布前另加两个演练（2026-10-15）**——它们验证的是"出事能不能救回来"，
> 与上面的功能验收是正交的两件事：
>
> ```bash
> bash tests/backup-drill.sh     # 19 断言：WAL 下在线备份不丢数据、恢复逐字节一致（含 live/ 正文日志）、
>                                #           篡改被拒、保留策略（证据 tests/evidence/）
> bash tests/upgrade-drill.sh    # 12 断言：升级幂等、起服正常、migrate down 会毁数据、
>                                #           备份恢复能回滚（结论见 03 §4.1）
> ```
>
> 两者都会在 `tests/evidence/` 留下带时间戳的证据文件；失败时保留 `.drill-*` /
> `.upgrade-*` 现场供排查。

```bash
cd plane-pocketbase

# ── 静态层（不需要起服务，秒级）────────────────────────────
go build -o .fe.exe .                                    # L1
go test ./internal/router/ -run TestRoutePatternsAreAnchored   # L2 护栏
./.fe.exe migrate up --dir=./.tmp-verify                 # L3 迁移自检
python tests/browser/audit-endpoints.py                  # L5 缺口审计

# ── 动态层（全栈，十分钟级）────────────────────────────────
cd tests/browser
bash run-all.sh --fresh

# 逐项确认：
#   run1-bootstrap.mjs      exit=0
#   run2-project.mjs        exit=0
#   run3-issue.mjs          exit=0
#   run4-project-pages.mjs  exit=0   FAILED REQUESTS — 无
#   run5-publish-space.mjs  exit=0   FAILED REQUESTS — 无
#   verify-profile.mjs      exit=0   ALL CHECKS PASSED

# ── 人工复核 ────────────────────────────────────────────
# 打开 tests/evidence/screenshots/ 逐张看一遍（30 张）
# 确认 home 挂件、profile 面板等"曾经静默失败"的地方内容非空
```

### 已通过的验收记录（2026-10-03）

```
run1-bootstrap.mjs     exit=0   D1–A3 全过 ✅
run2-project.mjs       exit=0   B0–B2 全过 ✅
run3-issue.mjs         exit=0   C1–C2 全过 ✅
run4-project-pages.mjs exit=0   D1–D5 全过 ✅  FAILED REQUESTS — 无
run5-publish-space.mjs exit=0   E1–E5 全过 ✅  FAILED REQUESTS — 无
verify-profile.mjs     exit=0   ALL CHECKS PASSED ✅
```

最终二进制网络日志里能看到曾经被子树模式掩盖/从未实现的接口现在都返回 200：

```
200 GET /api/workspaces/hrms/workspace-members/me/
200 GET /api/workspaces/hrms/states/
200 GET /api/workspaces/hrms/user-properties/
200 GET /api/workspaces/hrms/sidebar-preferences/
200 GET /api/workspaces/hrms/home-preferences/
200 GET /api/workspaces/hrms/user-profile/<id>/
200 GET /api/workspaces/hrms/user-stats/<id>/
200 GET /api/workspaces/hrms/user-activity/<id>/
```

---

## 10. 添加一个新测试的清单

```
□ 先写 recon 脚本把页面打开、截图、打印 DOM 与网络（搞清楚要测什么）
□ 定出要断言的具体内容（key 存在 / 计数非零 / 文本出现 / 形状正确）
□ 每个接口单独写断言 —— 不要用通用模板（相邻接口形状不对称）
□ 用 probe() 的 race 超时模式，避免渲染进程忙时卡死脚本
□ 断言失败要打印 detail（把整个响应 JSON 带上，否则下次还得重跑）
□ 把用例合进 verify-*.mjs，或新建 verify-<domain>.mjs
□ 在 tests/WORKFLOW_TEST_REPORT.md 里记录本次发现
□ 若有新坑，追加到 docs/06-PITFALLS.md
```

---

**下一篇**：陷阱与雷区总集见 [`06-PITFALLS.md`](06-PITFALLS.md)。
