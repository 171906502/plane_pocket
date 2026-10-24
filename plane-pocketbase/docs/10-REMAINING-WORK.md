# 10 · 剩余工作编排（任务表）

> ⚠️ **2026-10-24 起，新任务不再登记在这里** —— 排期入口改为
> [`11-TASKLIST.md`](11-TASKLIST.md)（实现完整度复核 + L 系列任务卡）。
> 本文件保留**历史决策与实施记录**：§3.1 live 调研、§3.2 路线选择、
> §3.3–§3.5 中继实施与运维收尾。T-1…T-5 仍是有效的验收记录。
>
> 这份文档原是**"还没做什么"的权威清单**，把散落在 `04` §9（N 系列）、
> `09` §5（审计记账项）、`07` §3（缺口分类）里的剩余项收敛成一张表，
> 每项都写明**要求 / 目标 / 验收标准 / 独占文件**，可直接派给 sub-agent 实现。
>
> **生成日期**：2026-10-15。**基线**：339 条已注册路由，26 条路径形状未匹配
> （全部已归类，见 T-4），METHOD-MISSING = 0。

---

## 0. 编排原则（为什么这样切）

1. **按包切分，不按功能切分。** 多个 agent 并行改同一个 Go 包会互相打断编译，
   所以每个任务声明**独占文件**，禁止越界。
2. **验收标准必须是可证伪的。** 每条都要求"内容断言 + 反向验证"（把实现改回去，
   测试必须 FAIL）。"测试通过"本身不是验收标准。
3. **契约优先。** 一切行为以上游 `plane-preview/apps/api/plane/` 的 Django 源码
   与 `spec/*` 为准；不得凭印象发明规则。发现契约空白时**记录而不是发明**。
4. **不做为了消数字而做的实现。** 排除项保持 404。

---

## 1. 任务表

| ID | 任务 | 来源 | 独占文件 | 优先级 | 状态 |
|---|---|---|---|---|---|
| **T-1** | 凭据类端点速率限制 | `04` §9 N-4 | `internal/middleware/ratelimit*.go`(新)、`internal/router/auth.go` | P1 | ✅ 完成（验收通过，含现场探针） |
| **T-2** | 启动配置自检（`--origins`） | `04` §9 N-8 | `main.go`、`internal/config/*`(新) | P1 | ✅ 完成（验收通过） |
| **T-3** | 健康检查端点 | 新增（运维缺口） | `internal/handlers/health/*`(新)、`internal/router/router.go` | P2 | ✅ 完成（验收通过，含现场探针） |
| **T-4** | N-1 重新核账 + 文档同步 | `04` §9 N-1 | `docs/04-DEPLOYMENT.md`、`docs/07-STATE-AND-ROADMAP.md` | P1 | ✅ 完成（26 项归类复核通过） |
| **T-5** | 密码重置 token 走同一投递通道 | 会话提出 | `internal/handlers/auth/auth.go` | P2 | ✅ 完成（验收通过） |

> **验收方式（编排方执行，不采信子代理自述）**：`gofmt` 干净、`go build ./...`、`go vet ./internal/...` 全过；
> 定向包测试全 `ok`；**反向验证 5/5**（把实现故意改坏 → 对应测试必须 FAIL → 还原后 PASS，且文件逐字节复原）；
> 起真实服务做**现场探针**（T-1 限流边界、T-3 `/health`）。其中 T-1 曾暴露一个**单测看不见的真实缺陷**：
> 分桶键用了带端口的 `RemoteAddr`，每条新连接各占一桶，限流在真实 HTTP 下**完全失效**（13 次请求全 302）；
> 修复为剥端口后现场实测 `302×10 → 429`，见 §2 T-1 与 `_scratch/ratelimit-live-probe.txt`。

---

## 2. 任务详情

### T-1 · 凭据类端点速率限制（N-4）

**为什么**：`/auth/*` 的凭据校验端点是唯一的暴力破解面，当前**无限速**——
密码可被在线爆破。这是安全审计（`09`）完成后剩下的最大缺口。

**目标**：每 IP 令牌桶，只作用于凭据校验类端点。

**要求**

- 作用范围**仅限**：`sign-in`、`magic-generate`、`magic-sign-in`、`request-password-reset`
  （以 `internal/router/auth.go` 实际注册路径为准；**不要**限制 `sign-up`/`sign-out`/
  `get-csrf-token`/`auth-check`——它们是正常流程且非爆破面）。
- 每 IP 令牌桶；默认 **30 次/分钟**、突发 10。
- `PLANE_RATELIMIT_AUTH_PER_MIN` 覆盖默认值；`0` 表示**关闭**（供测试与排障）。
- 超限响应：**429** + `Retry-After: <秒>` 头 + body 与既有错误形状一致
  （`{"detail": "..."}`，走 `serializers`）。
- 客户端 IP：优先 `X-Forwarded-For` 首段，回退 `RemoteAddr`。
  **必须在代码注释里写明"信任反代头"这个前提**（直连部署时该头可伪造）。
- 内存表需有**清理**（过期的桶要回收），不能无界增长。
- `go vet` 干净；不改 `internal/middleware/scope.go`；不改其他 handler 包。

**验收标准（我必须能逐条复核）**

1. 表驱动测试覆盖：第 N+1 次请求 429；`Retry-After` 头存在且为正整数；
   不同 IP 互不影响；`PLANE_RATELIMIT_AUTH_PER_MIN=0` 时永不 429。
2. **反向验证**：把判定改成"永远允许"，测试必须 FAIL（报告里给出实际失败输出）。
3. `go build ./...` + `go vet ./internal/middleware/` + `go test ./internal/middleware/` 通过。
4. 不改变任何现有测试的期望。

**验收结论（编排方，2026-10-15）**：全部通过，但**验收中发现一个单测看不见的真实缺陷**并已修复：

- 子代理实现的 `clientIP()` 在回退分支直接返回 `req.RemoteAddr`，即 `"IP:port"`。
  真实 HTTP 下每条 curl 都是**新 TCP 连接、新临时端口**，于是**每个请求各占一个桶**，
  限流在真实环境下**完全失效**——现场 13 次 `POST /auth/sign-in/` **全部 302，一次 429 都没有**。
  单测之所以通过，是因为它们复用同一个 `httptest` 请求、`RemoteAddr` 固定。
- 修复：分桶键**剥掉端口**（`net.SplitHostPort` + `netip` 归一化），与 PocketBase 自身
  `router.Event.RemoteIP()` 语义一致。现场实测（`PLANE_RATELIMIT_AUTH_PER_MIN=1`）：
  `302 ×10 → 429`，`Retry-After: 55`，body `{"detail":"Too many requests. Please try again later."}`，
  且未绑定的 `GET /auth/get-csrf-token/` 仍 200。证据：`_scratch/ratelimit-live-probe.txt`。
- 新增回归测试 `TestRateLimiterBucketsIgnoreEphemeralPort`（同 IP 不同端口必须共用一个桶），
  并已纳入反向验证（T-1b）：故意还原端口 bug 时该测试 FAIL。

---

### T-2 · 启动配置自检（N-8）

**为什么**：`--origins` 配错的表现是"**前端全白 + 日志里没有任何请求**"——
排查成本极高（见 `04` §9 N-8）。应在**启动时**就失败并给出可操作提示。

**目标**：生产模式下 `--origins` 为空或含通配符 → 拒绝启动；dev 模式仅告警。

**要求**

- 先**调查** `--origins` 如何被读取：它是 PocketBase `serve` 子命令的 flag。
  可行路径例如在 `app.OnServe()`/`OnBootstrap()` 里读 `app.RootCmd` 的 flag 值。
  **如果确实无法在启动前读到，允许降级为环境变量 `PLANE_ORIGINS`**，
  但必须在报告里说明原因，并在 `docs/04` 的对应位置留下说明（该文件的
  §3 环境变量表由 T-4 之外的**我方**合并，你只需在报告里给出建议文本）。
- 判定规则：
  - 生产（未设 `PLANE_PB_DEV=1`）且 origins 为空 → **报错退出**，提示
    "请用 `--origins` 精确列出前端 origin"。
  - 生产且含 `*` → **报错退出**（通配符 + credentialed CORS 不安全）。
  - dev 且为空/`*` → 打印**告警**，继续启动。
  - 生产且为具体 origin 列表 → 通过。
- **不得影响 `migrate` 等子命令**（自检只在 serve 路径生效）。
- 提示文案要包含"怎么修"，不要只说"配置错误"。

**验收标准**

1. 表驱动测试覆盖上述 4 种组合，断言**具体文案**（不是只断言 error != nil）。
2. **反向验证**：把生产校验改成空实现，测试必须 FAIL。
3. `go build ./...` 通过；`migrate up` 仍能在空 origins 下正常执行
   （即自检没有误伤迁移路径）。
4. 报告里写明 `--origins` 的实际读取方式与证据（代码位置 / 实测命令输出）。

---

### T-3 · 健康检查端点

**为什么**：`04` §10 建议"中型部署加监控告警"，但**当前没有任何可探活的端点**——
反代和 systemd 只能探 TCP，分不清"进程活着但数据库坏了"。

**目标**：一个未认证的探活端点，能区分"服务可用"与"依赖不可用"。

**要求**

- **先确认 PocketBase 是否已内建 `/api/health`**（PocketBase 0.22+ 有）。
  - 若已内建且足够：任务降级为"文档化内建端点 + 只在确有缺口时新增"，
    **不要重复造轮子**。在报告里给出实测输出（`curl` 状态码 + body）。
  - 若不足（例如不校验数据库）：在其之外补一个我们自己的端点。
- 端点要求（新增时）：
  - `GET /health`，**未认证**，且**必须位于认证/作用域/CSRF 中间件之外**。
  - 健康：200，body `{"status":"ok","version":"...","db":"ok"}`。
  - DB 不可用：**503**，body `{"status":"degraded","db":"error"}`。
  - 不得泄露内部信息（路径、连接串、表名一概不出现在响应里）。
  - 版本号取自构建信息；取不到时用 `"unknown"`，不得 panic。
- 不改变任何现有路由的行为；不动 `internal/router/auth.go`。

**验收标准**

1. 内容断言：200 时的 body 字段齐全；DB 不可用时 503 且 `status=degraded`
   （可用指向不存在目录的 app 或关闭 DB 来构造）。
2. **反向验证**：去掉 DB 检查（恒返回 ok），DB 故障用例必须 FAIL。
3. `go build ./...` + 该包测试通过；`internal/router` 的锚点护栏测试仍通过。

---

### T-4 · N-1 重新核账 + 文档同步

**为什么**：`04` §9 的 **N-1 写的是"74 个接口未实现"**，但 2026-10-15 实测
`audit-endpoints.py` 是 **26 条路径形状未匹配**，且逐条看全部是
"方案内排除"或"三元模板/epics 误报"。**这个数字是过时且误导的**——
它会让部署者以为有 74 个接口是坏的。

**目标**：把 N-1 改成有证据的结论，并让"真实未实现接口数"一目了然。

**要求**

1. 重跑两个审计脚本，记录**原始输出**：
   ```bash
   python tests/browser/audit-endpoints.py
   python tests/browser/audit-methods.py
   ```
2. 把 26 条形状**逐条**列出并归类，每条给出依据（引用
   `docs/08-IMPLEMENTATION-PLAN.md` §11 或 `spec/exclusions.md` 的具体条目）：
   - 「方案内排除」——webhooks / integrations / importers / AI / export / assets-v2 等；
   - 「三元模板误报」——前端 `${this.serviceType === EPICS ? ... : ...}` 无法静态解析；
   - 「epics 幽灵分支」——上游无该 API，**不实现**。
3. **如果发现有真实缺口**（既非排除也非误报）：不要自己实现，
   **在报告里单独列出**并标记为"应升级为代码任务"。
4. 修改文档：
   - `docs/04-DEPLOYMENT.md` §9 的 N-1 行：改为实测数字 + 结论 + 指向本表；
   - `docs/07-STATE-AND-ROADMAP.md` §1 基线与 §3 的分类说明：与实测对齐；
   - 两处都要保留"**不要为消数字而实现排除项**"的告诫。
5. **只改文档**，不碰任何 `.go` 文件。

**验收标准**

1. 我能用 `audit-endpoints.py` 的输出逐条复核你的归类（26 条一条不漏）。
2. 文档里不再出现无法追溯来源的数字。
3. `git status` 里只有文档文件被改。

---

### T-5 · 密码重置 token 走同一投递通道

**为什么**：Magic Link 已经走独立日志文件（`PLANE_MAGIC_LOG`），但**密码重置 token
仍然只打进程日志**（`internal/handlers/auth/auth.go` 里 `log.Printf("[plane] password
reset token for %s: %s", ...)`）。两者是同一类"被记录的凭据"，通道不一致会让运维
在两处找。

**目标**：重置 token 与验证码走同一投递通道，语义一致。

**要求**

- 复用同一写入函数与同一个文件；**行内加类型标记**，例如
  `kind=password-reset` / `kind=magic-sign-in`，避免运维分不清两种码。
- **保持向后兼容**：`PLANE_MAGIC_LOG` 仍是主变量名；额外支持 `PLANE_MAIL_LOG`
  作为优先覆盖（若两者都设，`PLANE_MAIL_LOG` 优先）。
- 未配置时仍回退进程日志；路径不可写时**仍然打印到进程日志**（不吞码）。
- 文件仍以 `0600` 追加打开；保留"验证码是 bearer 凭据"的注释。
- **不改变任何 HTTP 响应形状**；不改 `MagicGenerate`/`MagicSignIn` 的对外行为。
- **不要改文档**（`docs/04` 由我方在验收后合并，避免与其他任务冲突）。

**验收标准**

1. 内容断言：设置了 env 时，重置 token 出现在**同一文件**且带 `kind=password-reset`；
   未设置时出现在进程日志；坏路径不吞码。
2. 既有的 4 个 magic 测试**仍然通过**（向后兼容）。
3. **反向验证**：把重置路径改回只 `log.Printf`，新测试必须 FAIL。
4. `gofmt` 干净、`go test ./internal/handlers/auth/` 通过。

---

## 3. 明确**不派发**的剩余项（附理由）

覆盖"所有未做的"，但以下项**有意不做**，理由如下——不是遗漏。

| 项 | 来源 | 处置 | 理由 |
|---|---|---|---|
| **N-10** `apps/live` + Redis | `04` §9 | **已完成（2026-10-23，走 B 路线）** | **2026-10-16 决定**：不部署也不改造 `apps/live`。源码级分析（§3.1）表明 live 侧 Redis 只做 pub/sub + 跨实例锁，**单实例下完全闲置**；改造只需一个文件，但属改上游服务代码，且 Page 协作编辑不在本项目目标内，故整体搁置。**除非用户主动重新发起**。<br>**2026-10-23 用户重新发起**：Pages 编辑卡死已影响使用。复研见 §3.2——Redis 不是瓶颈，缺的是 WS 协作端。<br>**2026-10-23 落地**：用户选定 B（Go 版 Yjs 中继），`internal/live/` 已实现，浏览器端到端验收通过（§3.3、§3.4），含两处补漏（`description_stripped` 派生、加载时回写）。 |
| **P3** Magic Link 真发邮件 | `07` §5 | **不做** | 已定：日志文件级即可（用户决定），N-5 已降级记录。 |
| **P3** 吞吐压测 | `07` §5 | **不做** | 已定：设计目标是小团队内部使用（用户决定），N-6 已改为"按设计目标可接受"。 |
| **§5.7** 403 文案对齐上游 | `09` §5.7 | **不做** | 约 40 处统一用 DRF 文案 `{"detail": …}`，上游 `allow_permission` 用 `{"error": …}`。前端只按状态码 403 处理，**行为无差异**；已在 `scope.go` 收敛为常量 `deniedMessage`，将来要改是一行。属纯文案，churn 大收益小。 |
| **§5.8.4** 上游用裸 `IsAuthenticated` 的方法 | `09` §5.8.4 | **不做（更严是安全的）** | `IntakeViewSet.retrieve/partial_update`、`CycleIssueViewSet.retrieve/update/partial_update` 上游只要求登录，Go 要求项目成员/角色。按"契约优先、不得凭空放宽"原则**保持更严**；放宽等于复制上游 IDOR 缺口。 |
| `spec/exclusions.md` §7-A~F 待决项 | `spec/exclusions.md` §7 | **不派发** | 是**方案剔除范围**的待决问题（Unsplash 去留、Jira 是否随 integrations 删除等），与"接口实现"正交；需产品决策，不是代码任务。 |
| `07` §3.2 的两处**有意降级** | `07` §3.2 | **不做** | cycle/module 估算点聚合恒 0、`completion_chart` 为空——依赖 `issues.estimate_point` 与 issue 状态历史，本切片未建模。是范围取舍，不是 bug。 |

### 3.1 N-10 深入分析：`apps/live` 对 Redis 的真实依赖（2026-10-16）

**结论先行**：live 侧 Redis **只当消息总线**（pub/sub）+ 一把跨实例分布式锁，**没有任何"存储"用途**。
它**不是功能必需**，只在**多实例横向扩展**时才必需；而默认部署 `LIVE_REPLICAS=1`，这些能力**完全闲置**。
嵌入式 KV **不能**替代 pub/sub；但**单实例**下用**进程内事件总线**可完整替代，且**无需任何新组件**。

**A. 特性清单（逐条带出处）**

| Redis 特性 | 用途 | 出处 |
|---|---|---|
| Pub/Sub（二进制） | 多实例间同步 Yjs 文档增量 + awareness（光标/在线） | `@hocuspocus/extension-redis@2.15.2`：`sub.subscribe` / `pub.publishBuffer`（`dist/hocuspocus-redis.esm.js:144,162,170`） |
| 分布式锁（Redlock = SET NX PX + Lua） | 多实例同时持久化同一文档时互斥 | 同上：`import Redlock from 'redlock'`（:2）、`new Redlock([this.pub], …)`（:107）、`redlock.lock(lockKey(doc), …)`（:180） |
| Pub/Sub | 管理命令广播（强制关闭文档等） | `src/extensions/redis.ts:26,37,98`，频道 `hocuspocus:admin` |
| Pub/Sub | 按文档广播（标题同步、错误提示） | `src/extensions/redis.ts:126-140`（`publishBuffer`）；调用方 `src/utils/broadcast-message.ts:34`、`src/extensions/title-sync.ts:131` |
| KV + TTL（`SETEX/GET/DEL/EXISTS/EXPIRE`） | 封装了便捷方法 | `src/redis.ts:150-216` —— **全仓库无任何调用点（死代码）** |

> `apps/live` **不把文档存 Redis**。持久化走 `@hocuspocus/extension-database` → Plane API
> （`src/extensions/database.ts:91` `service.updateDescriptionBinary`）→ 数据库。

**B. 是不是必须的？**

- **按现状：启动即必须。** `src/extensions/index.ts:16` 无条件 `new Redis()`，其构造函数里
  `getRedisClient()` 在无客户端时 `throw new AppError("Redis client not initialized")`
  （`src/extensions/redis.ts:16-22,29`）；异常上抛到 `src/start.ts:18-21` → `process.exit(1)`。
  **不配 Redis，live 根本起不来。**
- **功能上：只有多实例才需要。** 上述每一项都是"进程间协调"。单实例下 pub/sub 没有第二个订阅者，
  Redlock 也没有第二个写者，**全是空转**。默认 `LIVE_REPLICAS=1`
  （`deployments/cli/community/docker-compose.yml:101`）。

**C. 嵌入式 KV 能替代吗？**

- **不能替代 pub/sub 与分布式锁**：嵌入式 KV（SQLite / bbolt / BadgerDB / LevelDB）是**单进程存储**，
  没有跨进程 `PUBLISH/SUBSCRIBE`，也没有分布式锁语义。**范式不对**，不是实现差异。
- **能替代的只有那块死代码**：`RedisManager.set/get/del/exists/expire` 换成嵌入式 KV 毫无障碍（反正没人调用）。
- **真正可行的替代路径**：

  | 部署形态 | 替代方案 | 额外基建 |
  |---|---|---|
  | **单实例**（默认，也是本项目目标） | Redis 扩展改为**可选**（未配 `REDIS_URL` 即不加载）；`hocuspocus:admin` 与按文档广播改用**进程内 EventEmitter** | **无** |
  | 多实例 | 跨进程总线：NATS ／ 单节点 Redis ／ Postgres `LISTEN/NOTIFY` | 视选型 |

  > 本项目迁移目标是 **PocketBase + SQLite 单二进制**，**没有 Postgres**，故 `LISTEN/NOTIFY` 不可用。
  > PocketBase 自带 realtime(SSE) 可做"标题变更/强制关闭"这类**应用级**广播，但 Hocuspocus 的
  > 文档增量是**自有二进制协议**，不能直接套用。

**D. 与已有决策的一致性**
API 层的 Redis 角色本项目**早已用进程内方案替代**：magic code 用 `h.App.Store()`
（`internal/handlers/auth/auth.go:284`）、改邮箱验证码同理（`internal/handlers/users/invitations.go:96`）、
限流用进程内令牌桶（T-1）。live 侧单实例化是同一思路的延伸，不是新方向。

**E. 决策（2026-10-16）：暂不实现**

**`apps/live` 既不部署也不改造，除非用户主动重新发起。** N-10 就此搁置。

- **理由**：live 侧 Redis 在**单实例**（默认 `LIVE_REPLICAS=1`）下完全闲置；改造要动上游服务代码
  （`src/extensions/index.ts` + 一个 in-process bus），而收益是让 Page 协作编辑可用——**不在本项目目标内**。
- **接受的功能缺口**：Page 富文本**协作编辑**（多人实时同编 + 光标/在线状态）不可用。
- **必须记住的连带风险**：不跑 live 时编辑器会退回 `usePageFallback`，其 effect 每次 description 变化都重跑
  → **PATCH/GET 无限循环**（见 `01` D5、`06` D-9、`tests/WORKFLOW_TEST_REPORT.md` #10）。
  因此部署时应**隐藏/不暴露 Pages 富文本编辑入口**（已加入 `04` §9 上线清单）。
- **若将来要启用**：按上表"单实例"行改造即可（零额外基建，只需不配 `REDIS_URL` 时跳过 Redis 扩展 +
  进程内总线顶上）；真要横向扩展再考虑多实例方案。届时由用户主动发起。

### 3.2 复研（2026-10-23，用户主动重新发起）：不引入 Redis 也要让 Pages 能用

用户提出的假设是「用纯 Go 写一个 Redis 协议模拟器」。**结论：这条路修不好 Pages**，
因为本项目里 **Redis 与 Pages 之间没有因果关系**——把因果链摊开就清楚了：

```
web 编辑器 ──ws──> /live/collaboration ──> apps/live(Node) ──HTTP PATCH──> Go API
                        ↑ 缺这一段                ↑ Redis 只在这里面，且只做多实例协调
```

**A. 三条实测证据**

1. **Go 侧零 Redis**：`grep -rn redis --include=*.go plane-pocketbase` → 无匹配；`go.mod` 无 redis 依赖。
   前端三个 app 也零 Redis 引用。唯一消费方是 `apps/live`。
2. **live 的 Redis 耦合只有 3 行**：`src/extensions/index.ts:13-19` 无条件 `new Redis()`，
   而 `new Redis()` 里 `getRedisClient()` 在无 `REDIS_URL` 时 `throw`（`src/extensions/redis.ts:16-22,29`），
   异常上抛 `src/start.ts` → `process.exit(1)`。功能上 Redis 只做 pub/sub + redlock，
   `RedisManager.set/get/del` 是**全仓库无调用点的死代码**（§3.1 已核）。
3. **单端口下 WS 根本没打到任何 live 逻辑**：向 `http://127.0.0.1:8000/live/collaboration`
   发标准 WS 升级请求，返回的是
   `HTTP/1.1 200 OK / Content-Type: text/html / Content-Length: 6085`
   —— 被 web 的 SPA catch-all 当成未知路由回退成 `index.html` 了，**不是 101**。
   provider 拿不到升级，只能按退避策略无限重连，每次重连都再吃一份 6 KB HTML。

**B. 「报错卡顿」的真实根因是死循环，不是断线**

`apps/web/hooks/use-page-fallback.ts:77-84`：`useEffect` 依赖 `handleUpdateDescription`，
而后者依赖 `page.description_html`（mobx observable，见 :75）。于是
保存 → `description_html` 变 → callback 身份变 → effect 重跑 → 再保存 …… 每轮还夹一次
`fetchPageDescription()`（GET）。断线在 live 里是**瞬态**，这个 fallback 才成立；
单端口模式下断线是**永久**的，于是变成**永动机**：页面一直转圈、请求打满、编辑器抢不到渲染。

> 所以「隐藏 Pages 入口」是 2026-10-16 的权宜之计；只要还想让用户用 Pages，
> 就必须让编辑器**不再走协作模式**，而不是把 fallback 修得更好。

**C. 三条可行路线**

| 路线 | 做法 | 纯 Go？ | Redis？ | 实时协作 | 工作量 |
|---|---|---|---|---|---|
| **A. 静态页模式**（推荐） | 前端不渲染协作编辑器，改渲染已有的非协作 `DocumentEditor`（`packages/editor/src/components/editors/document/editor.tsx`，目前**全仓库零引用**），`onChange` 防抖 → `PATCH …/pages/{id}/description/` 写 `description_html` + `description_json`；同时**摘掉** `usePageFallback` | ✅ | ❌ | ❌（单人编辑） | 0.5～1 天 |
| **B. Go 版 Yjs 中继** | Go 在 8000 上实现 `/live/collaboration`：Hocuspocus 消息分帧 + Yjs sync/awareness **原样转发**，不解析 CRDT，只做「更新日志 + 回放」；HTML/JSON 由客户端防抖 PATCH 补齐 | ✅ | ❌ | ✅ | 3～5 天 |
| **C. 改造 live** | `getExtensions()` 改成「无 `REDIS_URL` 就不加载 Redis 扩展」，`hocuspocus:admin` 与按文档广播换进程内 EventEmitter | ❌（要 Node） | ❌ | ✅ | 0.5 天（但要常驻 Node） |
| D. 假 Redis | 纯 Go 实现 RESP + pub/sub + `SET NX PX` + redlock 的 `EVAL` 脚本 | ✅ | 假装有 | ✅ | 3～4 天，**且仍要 Node** |

**D. 为什么「假 Redis」是最差的一格**

- 它**不解除 Node 依赖**：Redis 只是 `apps/live` 启动时的门票，拿到了门票还得跑 Node。
- 要骗过 `ioredis` + `redlock` + `@hocuspocus/extension-redis`，至少要支持：
  `PING` / `INFO` / `CLIENT SETINFO` / `SELECT` / `SUBSCRIBE` / `UNSUBSCRIBE` / `PUBLISH`（二进制安全）/
  `SET k v NX PX ms` / `GET` / `DEL` / `EVAL`（redlock 的 compare-and-del 脚本）。
  自己写一个进程，只为让另一个进程以为自己有 Redis——**净增一个进程，没减少任何依赖**。
- 反观 C 路线：同样的效果，**改 3 行**，不需要任何新进程。

**E. 建议**

先做 **A**（纯 Go、当天可用、不碰上游 Node 代码），把 `VITE_PAGES_COLLAB=0` 作为
单端口构建的开关，dev 多端口栈仍保持协作模式。等确实需要多人同编，再上 **B**；
C 只在「愿意常驻一个 Node」时才有意义。

A 的落地点（供实施时照做）：

1. `tests/browser/build-frontends.sh`：单端口构建时 `export VITE_PAGES_COLLAB=0`。
2. `packages/constants/src/endpoints.ts`：加 `export const PAGES_COLLAB_ENABLED = process.env.VITE_PAGES_COLLAB !== "0";`
   （`process.env` 在构建期被整块 define，键必须在 export 里先存在，同 C-19）。
3. `apps/web/components/pages/editor/editor-body.tsx`：`!PAGES_COLLAB_ENABLED` 时走非协作分支
   （`DocumentEditor` + `value={description_json ?? description_html ?? "<p></p>"}` + 防抖保存），
   标题改用受控输入框写 `page.name`（协作模式下标题存在 Y.Doc 的 `title` 字段里）。
4. `apps/web/components/pages/editor/page-root.tsx`：`!PAGES_COLLAB_ENABLED` 时不调用 `usePageFallback`（死循环源头）。
5. `plane-pocketbase/internal/serializers/page.go` 的 `PageDetail`：补 `description_json`
   （现在只回 `description_html`），否则非协作编辑器每次都要先解析 HTML 才能恢复内容。
6. 保存走已有的 `PATCH …/pages/{id}/description/`，Go 侧已支持三个字段
   （`internal/handlers/page/page.go:514-559`），**不需要改后端逻辑**。

> 兼容性：A 模式写的是 `description_html` + `description_json`，`description_binary` 保持为空。
> 将来若启用 live，`apps/live/src/extensions/database.ts:33-57` 会在 binary 为空时
> **自动把 `description_html` 转成 Yjs binary**，历史内容不会丢。

### 3.3 实施记录（2026-10-23）：走了 B 路线，纯 Go 中继已跑通

用户选定 **B（Go 版 Yjs 中继）**。已落地，`go test ./internal/live/` 与真实 yjs 客户端双向验证通过。

**新增代码**

| 文件 | 职责 |
|---|---|
| `internal/live/protocol.go` | Hocuspocus 帧格式（`[varstring doc][varuint type][payload]`）、LEB128 varint、sync/awareness/auth 编解码 |
| `internal/live/store.go` | 每文档一个 append-only 日志（`<data>/live/<doc>-<hash>.log`，记录格式 `[varuint len][bytes]`） |
| `internal/live/hub.go` | 房间表：连接集合、更新历史、awareness 快照、断线时撤回光标 |
| `internal/live/conn.go` | 单连接写协程（gorilla 只允许一个写者）+ ping/pong + 有界发送队列 |
| `internal/live/handler.go` | 升级、鉴权、授权、首帧取文档名、帧分发 |
| `internal/router/live.go` | 挂载 `GET /live/collaboration` |
| `internal/live/protocol_test.go` | 用真实库生成的黄金字节做断言（`_scratch/gen-vectors2.cjs` 可重生成） |

**协议上的关键取舍（读代码前先看这段）**

- **不解析 CRDT，只当中继。** 每个 update 原样落盘并转发；新客户端连上来时按顺序回放整段日志。
  因为 yjs update 幂等且可交换，客户端收到"按序的完整历史"与真实服务端算出的 diff 结果一致。
- **对 SyncStep1 回一个空 SyncStep2。** 客户端收到 SyncStep2 才会置 `synced`（见
  `MessageReceiver.applySyncMessage`），空 diff 之后紧跟日志回放，所以编辑器不会闪空。
- **服务端主动发 SyncStep1（空状态向量）**，用于向带 IndexedDB 缓存的客户端索要全量状态——
  断线期间的编辑靠这一步回到服务器。
- **不能派生 HTML。** 这是本方案唯一的真实缺口，由前端补：新增
  `apps/web/hooks/use-collab-description-sync.ts`，在页面根组件里监听编辑器事务，
  防抖 1.5s 后把 `getDocument()` 的 html/json PATCH 回 `…/pages/{id}/description/`。
  `description_binary` 不动（yjs 历史以中继日志为准，覆盖会分叉）。

**鉴权（踩过的两个坑）**

1. 升级请求带 `session-id` cookie → `FindAuthRecordByToken` 解析用户；
2. **前端 Auth 帧里的 id 是 `users.uuid`，不是 PocketBase 记录 id**（`serializers.UUID` 的输出）。
   两处都要认，否则日志里会看到 `auth mismatch ... token id "8394…" vs session "gloe0…"`。
3. 文档名（页面 id）和 `projectId` 同样是 uuid，鉴权时必须
   `uuid = {:id}` 查记录，再拿记录 id 去查 `project_pages` / `workspace_members`。

**其他落地细节**

- `/live/collaboration` 必须 `Unbind(gzipMiddlewareID)`：gzip 中间件把 `e.Response`
  换成不实现 `http.Hijacker` 的包装，WebSocket 升级会直接失败。
- 单端口下该路径此前落到 SPA catch-all，返回 `200 text/html`（不是 101），
  这就是"编辑器一直转圈"的直接原因；现在返回 401（未登录）或 101（已登录）。
- 当时的已知限制：日志只增不减（无 compaction），单文档 64 MiB 时打警告；多实例不共享
  （本方案是单进程单二进制，多实例需要另做跨进程总线）。
  → **compaction 与孤儿清理已于 2026-10-24 补上，见 §3.5**；多实例仍不支持。

**验证记录**

```bash
# 协议层（黄金字节来自真实 yjs/y-protocols/@hocuspocus）
go test ./internal/live/ -v        # 10 passed

# 端到端：用真实 yjs 客户端复刻 provider 握手
#   写入 → SYNCED text="" → FINAL text="hello from go relay + 中继写入成功"
#   重连（不带本地状态）→ SYNCED text="hello from go relay + 中继写入成功"
#   日志文件 79 bytes
```

### 3.4 浏览器端到端验收（2026-10-23 深夜）+ 两处补漏

真实 Chromium（agent-browser）跑完整个 Pages 流程，逐项结论：

| 验证项 | 结果 | 证据 |
|---|---|---|
| 打开页面不再卡死/报错 | ✅ | `.ProseMirror` 正常渲染，工具栏/标题/占位符都在，无错误 toast |
| 敲键后 update 落盘 | ✅ | 空日志 → 754 B；`[live] … join (conns=1, stored=0)` |
| 敲键后 `description_html/json` 回写 | ✅ | `html_len=120 / json_len=191`，`updated_at` 与输入时刻一致 |
| 重载后内容回来（日志回放，非 DB） | ✅ | 把 `description_html/json` 清成空串后重载，正文依旧完整恢复（`data-id` 相同，说明是同一 Yjs 节点） |
| 双标签页实时同步 | ✅ | tab2 输入 `[from-tab2]`，tab1 不刷新即出现；服务端 `conns=2` |
| 服务重启后内容仍在 | ✅ | 重启二进制后重开页面，内容原样回放，日志文件跨进程保留 |

**补漏 1：`description_stripped` 一直是空的（Go 侧 bug，已修）**

`PATCH …/pages/{id}/description/` 走的是 `page.DescriptionPatch`，它只写
`description_html/json/binary`，**没有**像 Django `Page.save()` 那样派生
`description_stripped`。而 `internal/handlers/view/sticky.go` 的搜索过滤正是
`description_stripped ~ {:query}`，等于页面正文永远搜不到。
修法：`DescriptionPatch` 里补一行 `page.Set("description_stripped", stripTags(...))`。
另一条写路径（`page.Update`，line 232）本来就有，所以这是单条路径的遗漏。

**补漏 2：加载时也要回写一次（前端 hook，已修）**

中继回放是**远端 yjs update，不触发编辑器的 `onStateChange`**，于是
"只打开不编辑"的页面永远不会回写 `description_html`——若正文只存在于日志里
（早期会话 PATCH 失败、或内容早于本 hook 上线），发布到 Space 就是空白页。
现在 hook 在 attach 后做一次 settle 检查（800ms 起、500ms 一轮、最多 10s）：

- 基线已知 → 文档与基线不同才写；
- 基线未知 → 文档里有文字就写。

⚠️ 基线不能直接用 `page.description_html` 判空：`store/pages/base-page.ts:127`
写的是 `page?.description_html || undefined`，**空串会被折叠成 `undefined`**，
所以"空页面"和"尚未 hydrate"在类型上无法区分，只能靠上面这条兜底规则。
副作用已验证：页面内容与库一致时重复打开**不会**产生 PATCH（`updated_at` 不变）。

**测试时最容易踩的坑：Y.Text vs Y.XmlFragment**

探针若往 `Y.Text("default")` 写，而 tipTap 编辑器读的是 `Y.XmlFragment("default")`，
两者同名但类型不兼容，编辑器会显示空文档——很容易误判成"中继坏了"。
日志解码后 `keys: ['default'] → AbstractType` 就是这种情况。清掉脏日志重测即可。

### 3.5 运维收尾（2026-10-24）：compaction / 孤儿清理 / `description_binary` 快照

§3.4 验收之后，中继还剩三件"长期跑会疼"的事，现已全部落地。

**① 日志 compaction（`hub.go` + `store.go`）**

中继不解析 CRDT，所以它**不能**自己合并 update。唯一正确的瘦身方式是：向一个
已经同步完成的客户端要一次**全量快照**，然后用它替换整段日志。

- 触发：`PLANE_LIVE_COMPACT_BYTES`（默认 1 MiB）+ `PLANE_LIVE_COMPACT_RECORDS`
  （默认 64）同时满足，且当前没有正在进行的 compaction。
- 手法：给该连接发一个**空状态向量的 SyncStep1**；`y-protocols` 对它的回答就是
  `encodeStateAsUpdate(doc)`，即整篇文档一条 update。
- 合并规则：`新日志 = [快照] + [请求发出后新记录的 update]`。旧文件留 `<log>.bak`。
- 两个安全阀：客户端必须 `synced` 且已加入 ≥10s（否则分不清它的 SyncStep2 是
  握手回包还是快照回包）；快照为空则放弃，不动日志。
- **顺序要求（很重要）**：写盘、广播、发快照请求必须在同一把 `r.mu` 内完成，
  否则并发编辑可能让快照漏掉一条下标小于 `compactIdx` 的 update，compaction 会
  把它静默丢掉。详见 `06-PITFALLS.md` D-11 ⑥。

**② 孤儿日志清理（`gc.go`）**

日志按页写，REST 层不碰它们，所以删页会漏下文件。启动时扫一遍 `<data>/live/`：

- 每个日志旁边有 `<log>.name` sidecar（文件名是净化+哈希过的，不可逆，sidecar 记原始 uuid）；
- 页面在库里**彻底消失**才删（含 `.name` / `.bak` / `.tmp`）；
  **软删除（`deleted_at`）保留**，因为恢复页面要把内容带回来；
- 没有 sidecar 的旧日志只在 0 字节时删（无法归属，宁可留着）；
- 正在被 room 打开的日志跳过。

**③ `description_binary` 快照兜底**

这是本轮唯一发现的**真 bug**：`pages.description_binary` 是 PocketBase `FileField`
（列里存文件名，字节在 storage 下），而旧代码把前端发来的 base64 直接 `Set` 进去
——存成"文件名"后校验失败，于是 `GET …/pages/{id}/description/` **永远返回空**，
`usePageFallback` 的离线兜底也永远拿不到文档。

修法（`page.setDescriptionBinary` / `page.readBinary`）：

- 写入：base64 → `filesystem.NewFileFromBytes(data, "description.bin")` → `Set`；
  非 base64 的值按"客户端把文件名原样回传"处理，保留不清空；
- 读取：`app.NewFilesystem()` + `record.BaseFilesPath()`（不要自己拼
  `DataDir()/storage/<collection>/<record>`）；
- 三个写入口（`Create` / `Update` / `DescriptionPatch`）统一走同一个 helper；
- 前端 hook 每次回写时一并发 `description_binary`（`getDocument().binary` 的 base64）。

有了它，中继就能在**日志丢失时自愈**：房间为空且日志无内容 → 读该页的
`description_binary` 作为第一条 update 播种（`live.snapshot.go`），编辑器打开就有内容。

**验收（真实 Chromium + 真实库）**

| 验证项 | 结果 | 证据 |
|---|---|---|
| compaction 真的发生 | ✅ | 日志 78 条 / 3740 B → `compacted … into 1 snapshot + 1 tail update(s) = 265 bytes`，`.bak` 3740 B |
| compaction 后内容不变 | ✅ | 重载页面，`data-id` 与文字逐字一致 |
| `description_binary` 落库落盘 | ✅ | 列值 `description_v6tpfljjp2.bin`，文件在 `storage/pbc_…/6cmrk…/` |
| `GET …/description/` 有字节 | ✅ | `bytes=481`（此前恒为空） |
| 日志丢失后自愈 | ✅ | 停服 → 移走 `.log*` → 重启 → 打开页面正文完整；服务端日志 `seeded from the stored description_binary (481 bytes)`，`stored=1` |
| 孤儿日志被清理 | ✅ | 造一个 `ghost-page-deadbeef.log` + sidecar → 重启 → `collecting ghost-page-deadbeef.log: page … no longer exists`，文件消失 |
| 单测 | ✅ | `go test ./internal/live/` 16 passed（含 replace / sidecar / GC / seed） |

**仍然存在的限制**

- 日志增长是**有上限的**（compaction），但一次快照的大小 ≈ 文档大小；超大页面
  （几 MB 富文本）每次 compaction 会重写整个文件一次，属于可接受的瞬时 IO。
- 只有**在线**客户端能提供快照：一个长期无人打开、日志又很大的页面，要等到有人
  打开并编辑一次才会瘦身。
- 多实例仍然不支持（跨进程总线不在本项目范围内）。

---

### 3.6 备份补齐 `live/`（2026-10-24，对应 `11-TASKLIST.md` L-5）

§3.5 把中继做完之后，备份这块露出一个**直接导致正文丢失**的缺口：
`tests/backup.sh` 只备份 `data.db` + `storage/`，而**页面正文的另一份真相在
`<data>/live/*.log` 里**。恢复之后页面会退回 `description_binary` 最后一次落盘的快照，
最坏连快照都没有（页面打开即空白）。已修。

**改了什么**

| 文件 | 改动 |
|---|---|
| `tests/backup.sh` | 新增 `live/` 拷贝；`MANIFEST.txt` 增加 `live_files=` 与 `live_sha256=`（整棵树哈希，只看文件数发现不了被拷短的日志）；`--tar` 模式下如实报告"已含在归档里" |
| `tests/restore.sh` | 还原 `live/` 后重算树哈希与 manifest 比对，不符即中止（`--force` 可越）；备份里没有 `live/` 时**明确提示**而不是静默跳过 |
| `tests/backup-drill.sh` | 种一棵真实格式的 live 日志（`[varuint len][bytes]` + `.name` sidecar）；破坏阶段连 `live/` 一起删；新增 2 条断言 |
| `tests/pbdata.py` | 新增 `hashdir <dir>` 子命令；`seed` 接受可选第三参数（live 目录） |
| `tests/upgrade-drill.sh` | 起服补 `--origins=http://127.0.0.1:$PORT,http://localhost:$PORT`。它原先不传，落到 PocketBase 的 `*` 默认值，被生产 CORS 守卫拒绝启动（这是**上一轮加固 D-8 引入的兼容性问题**，与本项无关，顺手修掉） |

**两个判断值得留档**

1. **顺序是"先库后日志"**，即允许日志比库新。这是安全的：中继回放幂等，多出来的
   update 无害；反过来（库比日志新）会让最后一次编辑掉在两者之间。
2. **`live/` 可以直接 `cp`**，不像 `data.db` 必须走在线备份 API。日志是
   `[varuint 长度][字节]` 顺序追加，读取端跳过被截断的最后一条记录
   （`internal/live/store.go`），所以运行中拷贝最坏只丢最新一次编辑。
   这一点是"备份脚本不必像数据库那样停服"的前提。

**验收（全部实跑，不是推演）**

| 验证项 | 结果 |
|---|---|
| 演练 | ✅ `backup-drill.sh` **19 passed / 0 failed**；`upgrade-drill.sh` **12 passed / 0 failed** |
| **反向验证** | ✅ 把 `live/` 拷贝改成 `elif false` 重跑 → **17 passed / 2 failed**，失败的正是新增两条；还原后 `diff -q` 逐字节一致 |
| 真实实例往返 | ✅ 对 `.fe-run`（5 个 live 文件 / 38441 B）备份 → 恢复到临时目录 → `dir_sha256=db91d147…` 两边完全相同 |
| manifest 自检 | ✅ 恢复输出 `restored live/ (5 files)` + `live/ tree matches the manifest checksum` |

---

## 4. 验收流程（我方执行）

每个任务交付后，我按以下顺序独立复核，**不采信 agent 的自述**：

```bash
cd plane-pocketbase

# 1. 改动面是否越界（对照任务的「独占文件」）
git status --short

# 2. 编译 + 静态检查
gofmt -l internal/            # 必须为空
go build ./...
go vet ./internal/...

# 3. 目标包测试（含新增用例）
go test ./internal/<pkg>/ -count=1 -v

# 4. 反向验证：自己把实现改回去，确认测试 FAIL，再恢复
#    （agent 的报告只作为线索，必须亲自复现）

# 5. 全量回归 + 发布检查
go test ./internal/... -count=1
bash tests/release-check.sh --fast        # 期望 6 passed, 0 failed
```

**任一条不通过即打回**，并把实际输出贴回给对应 agent。
