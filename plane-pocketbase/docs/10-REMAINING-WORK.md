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

### 3.7 L-1 工作项活动流（2026-10-24）：第一次引入记录钩子

`issue_activities` 有 4 处读、0 处写，是"接口在、数据永远空"的典型。根因是全仓
**0 个 `OnRecord*` 钩子**，所有跨表写入都手写在 handler 里。本轮把这条通道建起来。

**三条钩子前提（动手前先探针固化，结论写在 `06-PITFALLS.md` D-12）**

1. 内部 `app.Save()` **会**触发 `OnRecordAfterCreateSuccess` / `OnRecordAfterUpdateSuccess`
   （模型级钩子，与 HTTP 无关）→ 钩子里不能 Save 被监听的 collection；写未被监听的
   `issue_activities` 只会为新记录多触发一次即停止，**无需防重入**。
2. `record.Original()` **可读旧值，但有前提**：只有从 DB 加载过的记录才有。用
   `core.NewRecord` 造的对象直接 Save，`Original()` 是空白默认值。真实 handler 都是
   `FindByUUID → Set → Save`，正式路径没问题。
3. **钩子返回 error 会让主保存失败，且行已经写进去了** → 派生数据错误必须吞掉只记日志。

**实现**：新包 `internal/hooks/`（`hooks.go` 注册 + `contain()` 外壳吞 panic/error；
`activity.go` 做 diff），`internal/router/router.go` 只加一行 `hooks.Register(app)`。
issues 白名单字段 diff、`issue_comments`（`verb=comment`）、`issue_relations`（上下游双侧）、
assignee/label/cycle/module 桥表；关联对象单条 `FindRecordById` 解析名字填
`old_identifier`/`new_identifier`；批量更新 5s 窗口合并成一条。

**验收（主 agent 用新二进制 + 真 curl 独立复跑）**

- `PATCH {"state_id":…}` → `GET …/history/` 得
  `{"verb":"updated","field":"state","old_value":"Backlog","new_value":"Cancelled", …}`
- 挂估算点 → `{"field":"estimate_point","old_value":null,"new_value":"5"}`
- 批量更新 3 个 issue → 只 1 条汇总（1/0/0）
- 反向：注释掉 `hooks.Register(app)` → `wiring_test` FAIL → 还原 PASS
- 浏览器：Activity 页签 DOM 原文 `Wilber set the state to In Progress.` /
  `Wilber set the estimate point to 5.` / `added this work item to the cycle Sprint 1`
  （截图 `tests/evidence/screenshots/L1-activity-tab.png`）

**已知限制**：assignee/label 的"移除"不产生活动（handler 走 `app.Delete` 硬删除，
超出 AfterCreate/AfterUpdate 的观察范围）；批量汇总活动锚定在批内第一个 issue 上。

---

### 3.8 L-2 估算点（2026-10-24）：`issues.estimate_point` + 真实聚合

周期/模块卡片的「估算」此前**恒 0**：`issues` 没有 `estimate_point` 列，
`wave1.go:405` 注释直接写着"被忽略"，`cycle.go:604-609` 六个 `*_estimate_points` 硬编码 0。

**实现**

- 迁移 `1759000014_issue_estimate_point.go`：加 `estimate_point`（relation → `estimate_points`，
  可空，SET_NULL），带字段存在性守卫保证重跑 no-op。
- `internal/handlers/issues/issues.go` 的 `resolveEstimatePoint`：校验属于同一 project，
  否则上游原文错误；缺键 / `null` / `""` → 清除（前端清空时发 `null`）。
- `wave1.go` 的 `applyBulkProperties` 加入该字段，删掉过时的"被忽略"注释。
- `internal/serializers/cycle.go` 新增 `BridgeEstimatePoints`：按 `state.group` 聚合
  `CAST(ep.value AS REAL)`，**只算 `estimates.type='points'`**，issue 群体与
  `bridgeIssueCounts` 完全一致；`module.go` 六个字段改真实值。
- `cycle.go` 的 analytics 读 `?type=`，`type=points` 返回 assignees/labels 的
  `total/completed/pending_estimates` + 按点燃尽图（未来日 `null`）。
- `serializers/issues.go` 输出**平铺 UUID**（对齐前端 `TIssue.estimate_point` 与上游 app serializer）。

**验收（主 agent 独立复跑，含手工 Σ 对照）**

- 迁移幂等：`Applied` → 再跑 `No new migrations to apply`
- 挂 value=5 的点到 1 个 cancelled 状态的 issue → `cancelled_estimate_points=5`、
  `total_estimate_points=5`，与手工计算逐字段一致
- `analytics/?type=points`：`completion_chart` 过去日 5、未来日 `null`
- 跨项目引用：`400 {"error":"Estimate point is not valid please pass a valid estimate_point_id"}`，
  原值未被污染
- 反向：聚合改回恒 0 → 3 个测试 FAIL；`projectHasPointsEstimate` 改恒 false → 退化分布测试 FAIL

**踩到的坑（探针写法，非缺陷）**：`POST …/estimates/` 要的是**嵌套** body
`{"estimate":{…},"estimate_points":[…]}`，发扁平 body 会被静默忽略 → name 变随机串、
type 落默认 `categories`，而 `categories` 不计入任何聚合。第一次探针全 0 就是这个原因。

---

### 3.9 L-11 `projects.estimate`（2026-10-24）：浏览器验收逼出来的缺口

L-2 的 API 层自测全绿，但**浏览器验收发现用户根本看不到估算 UI**。这不是 L-2 的问题，
是一个被 defer 掉的字段把整条 UI 路径堵死了：

- `projects` 集合**没有 `estimate` 字段**（上游迁移 `1759000001:275` 注释
  "estimate deferred — `estimates` is outside this slice"，之后再未补上）；
- 前端 `sidebar.tsx:180` 把 Estimate 行门控在 `areEstimateEnabledByProjectId(projectId)` 上，
  `project.estimate` 为空时**整行不渲染**；
- `cycle.store.ts` 的 `getEstimateTypeByCycleId` 在 `project.estimate` 为假时**硬返回
  `"issues"`**，周期详情页的估算类型下拉点不动；
- `serializers/projects.go` 不输出、`handlers/projects/projects.go` 的 Update 白名单
  也不含 `estimate`（PATCH 返回 200 但静默丢弃）。

**实现**

- 迁移 `1759000015_project_estimate.go`：加 `estimate`（relation → `estimates`，可空，SET_NULL），
  带存在性守卫。
- `serializers/projects.go` 输出平铺 uuid（`PartialProject` 不加，上游 `ProjectListSerializer`
  的投影里也没有）。
- `handlers/projects/projects.go` Update 放行 `estimate`，校验属于同一 project。
- `handlers/estimate/estimate.go` 的 Create：事务内 `attachEstimateIfUnset`，项目当前没有
  estimate 时把新建的挂上（**本地对齐决策**，上游不自动挂，靠设置页开关；差异已写进注释和测试）。
- 前端零改动。

**验收（主 agent 独立复跑 + 浏览器 DOM）**

- 基线 `project.estimate = None` → 建 points 估算后**自动回写**为该估算 id
- 第二个估算**不覆盖**已有值
- `PATCH {"estimate":"<另一个>"}` → 200，读回生效
- 跨项目 → `400 {"error":"Estimate is not valid please pass a valid estimate_id"}`
- 浏览器：Properties 侧栏 DOM 出现 `Estimate` 行 + `aria-label="Estimates, 5"`；
  Cycle 详情下拉 `["Work items","Estimates"]` 可切，图例 `Current Points / Ideal Points`，
  `header: Points 2/7`、`Started 71% of 7`、`Completed 29% of 7` —— 与 API 的
  `total_estimate_points=7 / started=5 / completed=2` 逐项一致
- 反向：`PATCH {"estimate":null}` → 硬刷新 → Estimate 行从 DOM 消失（截图 L11-c）

### 3.10 L-3 版本历史（2026-10-24）：`page_versions` + `issue_description_versions` 的生产端

页面 `⋯ → Version history` 与工作项「描述历史」三个读接口都在（`page.VersionsList` /
`VersionDetail` / `RestoreVersion`、`intake/wave2.go`、`issues/wave2.go`），但**没有任何
写入路径** —— 版本历史永远空，`RestoreVersion` 也只搬得动文本，搬不动 yjs 二进制。
本轮把生产端建起来，仍挂在 L-1 引入的记录钩子上（`internal/hooks/`，D-12 三条前提直接复用，
`contain()` 外壳原样复用）。

**实现**

- `internal/hooks/version.go`（新）：监听 `pages` / `issues` 的 create+update。
  - `description_html` 与 `record.Original()` 相同 → 不记（改名、改属性、同值重存都不产版本；
    新建时空正文不产、有正文产第一条）。
  - **600s 同用户合并窗口**（上游 `PAGE_VERSION_TASK_TIMEOUT`）：取该页/该 issue 最新一行，
    `owned_by == actor` 且 `last_saved_at` 距今 ≤ 600s → 原地刷新（html/json/stripped/binary/
    `last_saved_at`），否则插入。协作编辑 1.5s 一次 PATCH，没有这条 20 条上限 30 秒就会烧穿。
  - **上限 20**：仅插入后检查，超限按 `last_saved_at` 升序 `app.Delete` 硬删
    （PocketBase 的 `__pbFilesManagerDelete__` 会连带删掉版本行的文件）。
  - `description_binary` 是 protected file 字段：源记录字节经 `app.NewFilesystem()` 读出，
    以 `filesystem.NewFileFromBytes` 写入版本行 —— 直接赋文件名/字节会验证失败（PITFALLS 里
    `description_binary` 老坑的同款）。
  - `issue_description_versions` 的 `project` / `workspace` 从 issue 记录解析（两列均 required）。
- `internal/handlers/page/page.go`（只动恢复路径）：`RestoreVersion` 补 binary 回写
  （版本行字节 → 页面新文件），保存后清该页 live 日志。
- `internal/live`（唯一导出新增）：`(*Handler).PurgeDocument(documentName)` 按
  `store.go safeFileName` 定位日志，先摘内存 room 并关文件句柄（hub.mu→room.mu 分两段，
  避开与 `evict` 的死锁），再删 `.log`/`.name`/`.bak`。`New` 改为按 app 记忆化 ——
  否则恢复路径新建的 hub 摘不掉 socket 正在服务的 room，Windows 上连删文件都会因句柄未关失败。
- 不清 live 日志的后果：Yjs 中继在 join 时回放旧 CRDT 更新，把恢复前的内容盖回去，
  恢复"看起来没发生"。

**验收（`go test -count=1 ./...` 31 包全 ok）**：合并窗口（含 600s/601s 边界表驱动）、
25 用户 25 次保存恒 20 行且幸存者为最新 20 条、不相关编辑 0 版本、恢复写回含 binary、
恢复后 live 日志三件套消失、30 次连打合并成 1 行（防重入 + 不误触上限）。
逐条反向验证表见 `11-TASKLIST.md` L-3 卡片。

---

### 3.11 L-4 通知生成（2026-10-24）：`notifications` + `issue_mentions` 从零到有

收件箱永远空：`internal/router/notification.go` 只有消费端（list / detail /
mark-read / unread-count / preferences），**没有任何写入路径** insert 过
`notifications`；`issue_mentions` 全仓 0 引用（审计 `NO-REF` 桶）。本轮把生产端
建起来，挂在 L-1 已引入的记录钩子上（`internal/hooks/`，D-12 三条前提直接复用）。

**实现**

- `internal/hooks/notify.go`（新）：写入主路径 + 四条守卫 —— 不通知自己、
  `user_notification_preferences` 门控（缺行 = 默认全开，对齐 handler 的惰性
  种子）、**插入前查询**的 5 分钟去重（同 receiver + entity + kind）、5s
  合并窗口（同 L-1 `commitBulk` 思路）；**每次 save 扇出上限 50**。
  `sender` 写触发种类（`assigned`/`mentioned`/`commented`），`mentioned`
  字面包含消费端 `sender ~ 'mentioned'` 的子串；`entity_name="issue"`；
  `entity_identifier` 写 issue 的 **Plane UUID**（不是 PocketBase 记录 id）。
- `internal/hooks/notify_mentions.go`（新）：`<mention-component
  entity_name="user_mention" entity_identifier="<用户 uuid>">` 解析（只认
  user_mention、按用户去重）；`issue_mentions` 簿记先查后写（吃 partial
  UNIQUE `(issue, mention)`，重存同 html 零新增）；三个触发点：
  `assigned`（`issue_assignees` 创建）、`commented`（`issue_comments` 创建 →
  订阅者，**无订阅者行时回退 assignees + creator**）、`mentioned`（issue 描述与
  评论，create/update 都走 `Original()` diff，只通知**新增**提及）。
- `internal/hooks/hooks.go`：两个 switch 各加用例，全包在既有 `contain()` 里。
- `docs/06-PITFALLS.md`：追加 **D-16**（通知字段写错=没写的四个点）与 **D-17**（扇出写三层防线）。

**三个产品判断（代码注释里都有）**

1. `assigned` 上游**没有专属偏好列** → 门控在 `property_change`（兜底列），
   默认全开保持上游行为。
2. 扇出三层防线：每次 save 上限 50 行 / 同 (actor, receiver, kind) 5s 合并 /
   DB 5 分钟去重（重启不丢）。
3. 评论接收者有订阅者行就只发订阅者（排除评论者）；完全没有订阅行才回退
   assignees + creator——订阅只能从显式 subscribe 端点产生，新 issue 通常没有。

**验收（`go test -count=1 ./internal/hooks/` 全绿，11 条新测试）**

- A 分配 issue 给 B → B 未读数 0 → 1（UnreadCount 过滤器逐字复刻），行里
  `entity_name="issue"`、`sender="assigned"`、`title`/`message_html` 非空、
  `entity_identifier` = issue Plane UUID
- 两个 mention 标签 → 2 行 `issue_mentions` + 2 条 `sender` 含 `mentioned`
  的通知；**重存同 html 零新增**；重复标签按用户去重、非 user_mention 被忽略
- 自动作 0 行；5 分钟内重复动作 0 第二行（含"换个人再评论"隔离去重的用例）；
  `mention`/`comment`/`property_change` 关闭各 0 行；缺偏好行默认全开
- 扇出：一条评论 200 订阅者 → 恰好 50 行；批量分配同一人到 200 个 issue → 1 条
- 反向验证：`hooks.go` 去掉对应注册、`notify.go` 去掉四条守卫各会让对应测试
  FAIL（逐条清单在 `docs/11-TASKLIST.md` L-4 的验收表）

**已知限制**：assignee "移除"不通知（handler 走 `app.Delete`，钩子观察不到，
同 L-1）；5s 合并窗口内同一人对同类触发只留第一条；`state_change` /
`issue_completed` / `property_change` 三类通知按最小闭环要求留 TODO。

### 3.12 L-6 Cycle 分组视图（2026-10-25）：`group_by` / `sub_group_by` 落地

`GET …/cycles/{id}/cycle-issues/` 之前无视分组参数一律返回平铺信封（handler 注释
自认未实现）。本轮把上游 `CycleIssueViewSet.list` 的 Grouped/SubGroupedOffsetPaginator
形状补齐。

**实现**

- `internal/serializers/pagination.go`（共享分组助手，space 的 issues 板已在用）：
  - `GroupQuery` 加 `SubGroupedBy` / `SubGroupKeys` / `SubGroupFilter`，不填时行为
    与之前逐字节相同（既有调用零影响）；
  - `GroupBucket.Results` 改 `any`，子分组模式是嵌套桶 map（每组 `{results,
    total_results}`，`results` 再嵌 `{子组id: {results, total_results}}`）——
    与前端 `packages/types/src/issues/issue.ts` 的 `TIssueResponseResults` 逐字段一致；
  - 新增 `GroupBuckets`：自定义 `MarshalJSON` 的**有序**桶 map。不加它，encoding/json
    会对 map 按字母序输出，把上游 `issue_group_values()` 的展示顺序（states 按
    `sequence`、priority 按 `["low","medium","high","urgent","none"]`）打乱。
  - 子分组语义对齐上游 `__get_field_dict`：组级 `total_results` = 组内全量计数；
    每组只 seed **有行**的子组（空组 → `"results": {}`）；分页按 (组,子组) 格子切。
- `internal/handlers/cycle/grouping.go`（新）：`issueGroupKeys` 按上游
  `issue_group_values()` 产出桶——`state_id`（项目 states 排除 triage，按
  `sequence`）、`priority`、`state__group`；其余 key `ok=false`。priority 的
  `none` 桶过滤器带 `|| ({:p} = 'none' && priority = '')` 兜住 DB 空串（PocketBase
  在解析 filter 前把 `{:param}` 替换成带引号字面量，参数=字面量比较合法）。
- `internal/handlers/cycle/issue.go`：`group_by`==`sub_group_by` → 400（上游同文）；
  受支持 → 分组信封；**未知 key 整体退化平铺**（L-6 契约：fallback 不是 500）；
  无参数路径一行未动。

**验收（`go test -count=1 ./...` 31 包全 ok；gofmt/vet 干净）**：双层
`state_id+priority` 的嵌套/桶序/两级 `total_results`/空格子省略、单层含空组
`results: []`、`state__group` 与 priority 固定组序、无参数平铺信封不变、未知 key
退化平铺、相等 key 400，各有测试钉住（`cycle/grouping_test.go`，7 条）。反向：
注释掉 `issue.go` 的分组分支 → 5 条分组测试实测 FAIL、2 条平铺测试仍 PASS
（逐条清单见 `docs/11-TASKLIST.md` L-6 卡片）。

**已知限制**：`labels__id`/`assignees__id`/`target_date` 等分组 key 未实现（退化
平铺）；组桶里不放组元数据（state 名/色由 states 接口提供，与上游一致——前端
`processIssueResponse` 只按 id 取键）。

---

### 3.13 L-7 最近访问（2026-10-25）：`user_recent_visits` 从只读到一个请求路径内的 upsert

侧边栏 / 命令面板的「最近访问」永远空：`internal/handlers/view/recent.go` 只有
读端点，全仓没有任何写入口。本轮不在钩子层做（详情 GET 是读请求，钩子没有
附着点），而是在两个详情 handler 的 200 路径各加一次 `RecordVisit`。

**实现**

- `internal/handlers/view/recent.go`：导出助手 `RecordVisit` —— 先查
  `(user, entity_name, entity_identifier)`，命中且 `visited_at` 距今 < 60s
  直接返回（**写节流**，L-7 卡片明示许可）；命中且过期 → 原地 Save（
  `visited_at` 是 `OnUpdate` autodate 列，保存即刷新）；未命中 → 插入。
  `entity_identifier` 写实体的 **Plane UUID**（前端
  `fetchWorkspaceRecents` → `TActivityEntityData` 按它解析 `entity_data`，
  读侧 `FindByUUID` 同）。首访时间戳必须 `SetRaw`（autodate 的 `Set` 被吞）。
  错误与 panic 全部记日志吞掉（D-12 前提 3 的请求路径版），GET 永不因此失败。
- `internal/handlers/issues/issues.go` `Detail` / `internal/handlers/page/page.go`
  `Detail`：各加一行调用，都放在 404/可见性分支**之后**，只在真实 200 时记录。
- 读端点本就按 `-visited_at` 排序，无需改动。
- 新测试：`issues/recent_test.go`（3 行 + wire 倒序 + 节流 + SQL 回拨后刷新 +
  404 不记录）、`page/recent_visit_test.go`（页面 1 行 + 节流 + 私有页 404 不记录）。

**验收（`go test -count=1 ./...` 31 包全 ok）**：3 个 issue 详情 → 3 行且
`GET …/recent-visits/` 倒序；60s 内重访 ×2 → 仍 3 行、时间戳不变；回拨 2 分钟
后再访 → 原行刷新；404 → 0 行。反向验证：删 `issues.Detail` 的调用 → 两条测试
实测 FAIL（逐条清单在 `docs/11-TASKLIST.md` L-7 验收表）。

**已知限制**：60s 内重访不刷新时间戳（对上游"每次都更新"的有意偏离）；并发
首访理论上可产生两行（表无唯一约束，spec §3.13 REVIEW 既有决定）；
`entity_name=project` 仍无写入（不在本卡范围）。

---

### 3.14 L-8 公共 API 文档页（2026-10-25）：占位 HTML → 零依赖静态端点表

`/api/v1/` 的 Swagger/ReDoc 页一直是一行占位文字。离线部署不允许 CDN，
`redoc.standalone.js`（约 2 MB）既拿不到也不能整包进仓库，故选**服务端渲染
的静态分组表格**：无 JS、无外链、无字体请求，样式内联单个 `<style>`，
CSP 无需放开（`/api/v1` 本就无全局 CSP 中间件）。

**实现**

- `internal/handlers/publicapi/docs.go`（新）：端点表 `docEndpoints`
  （151 条 method/path/group/auth/description，与 `internal/router/publicapi.go`
  的注册一一对应，含 `work-items`/`issues` 双别名）+ `renderDocsHTML()`
  （18 个分组 section，进程启动时渲染一次）+ `Docs` handler。
- `internal/handlers/publicapi/publicapi.go`：删掉 `swaggerHTML`/`redocHTML`
  占位常量，`SchemaUI` 改返回同一份文档页。
- `internal/router/publicapi.go`：仅 1 行 `api.GET("/{$}", h.Docs)` ——
  `/api/v1/` 根此前 404，验收要求浏览器直开该路径（越界 1 行，已明报）。
- `internal/handlers/publicapi/docs_test.go`（新）：① `GET /api/v1/` 返回
  200 + 文档标记 + 151 行全量端点 + 无 `http(s)://`/`<script`/`<link`/`src=`/`url(`
  外部引用；② go/ast 解析路由注册文件（含 range 循环展开）与文档表
  **1:1 双向比对**——新增公开路由不补文档即 FAIL（实测抓出过 100+ 条漏
  `{$}` 锚点的表项）。

**验收（`go test -count=1 ./...` 全部包 ok）**：二进制 40,369,152 →
40,404,992 字节（+35,840 B ≈ +0.09%；若走 ReDoc 方案约 +2 MB）。
逐条验收表与反向验证清单在 `docs/11-TASKLIST.md` L-8。

**已知限制**：页面是端点清单而非交互式 explorer（请求/响应 schema 不在
页面上，`/api/v1/schema/` 仍是 OpenAPI 桩）；`/schema/swagger-ui/`、
`/schema/redoc/` 别名返回同一静态页。

---

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
