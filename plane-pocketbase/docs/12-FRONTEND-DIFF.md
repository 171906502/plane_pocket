# 12 · 前端差异治理（保持可升级性）

> **一句话**：`plane-preview/` 是 `makeplane/plane v1.4.2` 的 vendored 副本（无 `.git`）。
> 上游升级 = 用新树覆盖它，所以**树里的每一行本地改动都是升级成本**。
> 本文件记录：现状 13 个文件里哪些是真必需、哪些可以外置，以及剩余改动的三种替代方案。

---

## 0. 原则

1. **能外置的绝不进树**：构建期变量、安装参数、dev 服务器保护 → 放进 `plane-pocketbase/tests/` 的脚本，而不是改前端配置。
2. **必需的最少化**：树里只保留"没有前端代码之外的实现方式"的改动。
3. **失败必须响亮**：升级后若补丁打不上，构建立即失败；绝不允许"静默漂移"（搜索/发布悄悄坏掉）。

---

## 1. 现状盘点（13 个文件里已外置 6 个，树内剩 7 个）

| # | 文件 | 改了什么 | 类别 | 现状 |
|---|---|---|---|---|
| 1 | `.npmrc` | `node-linker isolated→hoisted` | 环境 | **已外置**（2026-10-26）→ `tests/frontend/install-deps.sh` 的 `--node-linker hoisted` |
| 2 | `pnpm-workspace.yaml` | `storeDir: D:/.pnpm-store` | 环境 | **已外置** → 同一脚本的 `--store-dir`（本机默认已是该路径，纯保险） |
| 3 | `apps/web/vite.config.ts` | watch 忽略 `node_modules.*` | 环境 | **已外置** → `tests/frontend/clean-stale-modules.sh`，由 `stack.sh start` 调用 |
| 4 | `apps/admin/vite.config.ts` | 同上 | 环境 | **已外置** → 同上 |
| 5 | `apps/space/vite.config.ts` | 同上 | 环境 | **已外置** → 同上 |
| 6 | `apps/space/package.json` | `start/preview` 改 `serve -s` | 环境 | **已外置** → 我们不用该 app 的 `pnpm start`（Go 托管静态文件），已还原上游 |
| 7 | `packages/tailwind-config/index.css` | `@import "tailwindcss"` → `"tailwindcss/index.css"` | 构建硬修复 | **必需**：回退后 `ENOENT … apps/web/tailwindcss`（Vite 8 postcss-import 不解析 exports） |
| 8 | `apps/space/react-router.config.ts` | `ssr: true→false` | 单端口 SPA 化 | **必需**：静态托管的前提 |
| 9 | `apps/space/app/root.tsx` | 删 `headers` 导出 | 单端口 SPA 化 | **必需**：`SPA Mode: invalid route export(s) … headers` |
| 10 | `apps/space/app/page.tsx` | 删 `headers` 导出 | 单端口 SPA 化 | **必需**：同上 |
| 11 | `apps/space/app/issues/[anchor]/layout.tsx` | `loader → clientLoader` | 单端口 SPA 化 | **必需**：`SPA Mode: invalid route export(s) … loader` |
| 12 | `apps/web/hooks/use-collab-description-sync.ts` | 141 行协作回写钩子 | 协作回写 | 见 §2 |
| 13 | `apps/web/components/pages/editor/page-root.tsx` | 挂接上述钩子（4 行） | 协作回写 | 见 §2 |

### 外置到哪（1–6 号）

| 原来改在哪 | 现在放在哪 | 为什么这样可以 |
|---|---|---|
| `.npmrc` 的 `node-linker` | `tests/frontend/install-deps.sh` | 安装参数是**机器**属性，不是项目属性；命令行走 `--node-linker hoisted` 等价（C-14） |
| `pnpm-workspace.yaml` 的 `storeDir` | 同一脚本的 `--store-dir` | 同上（C-16） |
| 三个 `vite.config.ts` 的 `server.watch.ignored` | `tests/frontend/clean-stale-modules.sh` | 根因是**残留目录**（`node_modules.broken-*` 等），删掉就不需要忽略规则 |
| `apps/space/package.json` 的 `start`/`preview` | 无需替代 | 我们从不执行该 app 的 `pnpm start` |

```bash
# 安装依赖（唯一入口）
bash plane-pocketbase/tests/frontend/install-deps.sh

# 清理残留依赖树（stack.sh start 会自动调用）
bash plane-pocketbase/tests/frontend/clean-stale-modules.sh --dry-run
```

### 实测证据（可复现）

```bash
# 1–6 号回退成上游版本后，三端构建仍全绿（2026-10-26 实测）：
#   [build] web OK (34M) / admin OK (13M) / space OK (15M)  →  EXIT=0
for f in .npmrc pnpm-workspace.yaml apps/web/vite.config.ts \
         apps/admin/vite.config.ts apps/space/vite.config.ts apps/space/package.json; do
  git checkout ce5a255~1 -- "plane-preview/$f"
done
cd plane-pocketbase && bash tests/browser/build-frontends.sh

# 回退 7 号（tailwind）→ 构建失败：
#   Error: [postcss] ENOENT: no such file or directory, open '…\apps\web\tailwindcss'

# 回退 9–11 号（保留 ssr:false）→ 构建失败：
#   Error: SPA Mode: 1 invalid route export(s) in `root.tsx`: `headers`.
#   Error: SPA Mode: 1 invalid route export(s) in `./page.tsx`: `headers`.
#   Error: SPA Mode: 1 invalid route export(s) in `./issues/[anchor]/layout.tsx`: `loader`.
```

**结论：树内 7 个文件**（7–13）。其中 5 个是“静态托管 + 构建兼容”的硬约束，2 个是协作回写。

---

## 2. 协作回写：为什么它落在前端，以及四种替代

### 根因（有代码证据）

| 事实 | 位置 |
|---|---|
| 客户端用 `@hocuspocus/provider` 连 `/live/collaboration` | `packages/editor/src/contexts/collaboration-context.tsx`、`apps/web/components/pages/editor/editor-body.tsx:85-89` |
| 上游 `apps/live` 的 HTML 派生**调用 JS 序列化器**，不是自己实现 | `apps/live/src/extensions/database.ts:42` → `getAllDocumentFormatsFromDocumentEditorBinaryData`（来自 `@plane/editor`） |
| 上游 live 通过 service 层写库 | 同上 → `service.updateDescriptionBinary(pageId, payload)` |
| 本移植的 Go 中继**只转发不解析** | `internal/live/`（设计见 10-REMAINING-WORK §3.3） |

即：`description_html` 的派生本质上是"用编辑器 schema 跑一遍 ProseMirror DOMSerializer"。编辑器 schema 只存在于 JS 侧。

### 四种可能

| 方案 | 做法 | 成本 | 风险 |
|---|---|---|---|
| **A. 保留 2 文件补丁**（现状） | 前端编辑器把 `getDocument()` 的结果防抖回写（复用既有 `updatePageDescription` 管道） | 已完成 | 低。新增文件，不改既有交互 |
| **B. 跑上游 `apps/live` 侧车** | 前端零改；Node 进程跑上游 live，Go 反代 `/live/*` | 低（实测见 §2.1：Go 侧只缺 1 个接口） | 中：多一个运行时；但用的是上游自己的序列化器，天然跟随升级 |
| **C. 内嵌 JS 引擎（goja）** | Go 内嵌 `@plane/editor` 的序列化器 bundle + DOM shim | 未知：需 1–2 天 spike 判定 | 高：DOM/React 依赖，长期脆弱 |
| **D. Go 复刻序列化器** | ygo 解码 CRDT + 手写 renderHTML | 高：~30 节点/标记、固定 class 约定、lowlight 高亮树 | 高：编辑器升级即静默漂移 |

### 2.1 B 方案到底多一个什么进程（2026-10-25 核对源码）

**一个** Node 进程，不是一套服务栈：

```
node plane-preview/apps/live/dist/start.mjs      # Hocuspocus server, PORT=3100
```

| 问题 | 实测答案 | 依据 |
|---|---|---|
| 要 Redis 吗？ | **不要**。`RedisManager` 全仓零引用（死代码），`.env` 里的 `REDIS_URL` 是历史遗留 | `apps/live/src/redis.ts` 无任何 import 方 |
| 要数据库吗？ | 不要。数据全部 HTTP 回源 Go（`API_BASE_URL=http://localhost:8000`） | `src/services/page/*.service.ts` |
| 怎么认证？ | WS 的 token 里带 `{id, cookie}`，回源时转发 `Cookie` 头；Go 的 `/api/users/me/` 已具备 | `src/lib/auth.ts:44-52`、`internal/router/users.go:15` |
| 要哪些后端接口？ | `GET pages/{id}/`、`GET|PATCH pages/{id}/description/` **已有**；`GET pages/{id}/mentions/` **缺** | `src/services/page/core.service.ts:26-135`、`internal/router/page.go:30,58-59` |
| Go 还要做什么？ | 只加两件事：`pages/{id}/mentions/` 接口 + `/live/collaboration` 的 WS 反代到 `127.0.0.1:3100` | — |

**副作用（必须一起考虑）**：上游 live 的持久化模型是"文档状态写进 `description_binary`"，不再有 `live/` 日志目录。
因此切 B 之后：`internal/live` 的日志/回放对 pages 不再使用，L-5 的 `live/` 备份对象也随之失效；
但描述写入改走 `PATCH …/description/`，L-1 活动流与 L-3 版本历史的钩子仍会正常触发。

如果 space 也要零改（SSR），再额外加**第二个**进程 `react-router-serve`（3002）——即"完全零改 = 2 个 Node 进程"。

### D 的实测（2026-10-25，`.scratch/ygo-spike`）
`reearth/ygo v1.54.0`（纯 Go、MIT、无 cgo）对真实 `.fe-run/live` 日志 **55/55 帧 apply 成功**，
`GetXmlFragment("default")` 结构完整。**解码不是问题**，问题在序列化：

```
ygo 给出:   <paragraph id="903e…">4545656</paragraph><heading id="ef33…" level="3">4545514</heading>
库里存的:   <p class="editor-paragraph-block" data-id="903e…">4545656</p>
            <h3 class="editor-heading-block" data-id="ef33…">4545514</h3>
```

映射 = 节点名→标签 + `id`→`data-id` + 每个节点固定 class + marks→span + mention 自定义元素 +
code 块 lowlight 的 `<span class="hljs-…">`。等价于用 Go 重写 `DOMSerializer` 并永久跟随编辑器演进。

**建议**：默认 A；若"前端零改"是硬指标，选 B（把差异从前端移到 Go 侧的 Django 兼容适配层，且序列化器仍归上游维护）；C/D 暂不投入。

---

## 3. space 的 SSR：两条路

| 方案 | 前端差异 | 代价 |
|---|---|---|
| 静态 SPA（现状） | 4 个文件（8–11） | 分享链接的 OG 标签丢失（爬虫拿不到 SSR HTML） |
| Node SSR 侧车 + Go 反代 | 0 个文件 | 生产多一个 Node 进程 |

若将来 OG 预览变重要，切到侧车即可把 4 个文件全部还原。

---

## 4. 推荐落地：补丁层 + 升级演练

目标状态：**前端树与上游逐字节一致**，本地改动以补丁形式存在。

```
plane-pocketbase/frontend-patches/
├── 0001-tailwind-import-subpath.patch   # 7 号，待生成
├── 0002-space-spa-mode.patch            # 8–11 四个文件合一，待生成
└── 0003-collab-description-sync.patch   # 12–13，已生成（2026-10-26）
```

> 补丁基线是 `ce5a255~1`：该提交的 `plane-preview/` 是上游纯净树。
> 生成方式 `git diff ce5a255~1 -- <文件>`；校验方式 `git apply --check -R`。

配套脚本（建议）：

- `tests/frontend/apply-patches.sh`：对 `plane-preview/` 依次 `git apply`（树由 `git -C .. status` 校验干净）
- `tests/frontend/verify-patches.sh`：在临时副本上应用补丁 + `build-frontends.sh`，失败即非零退出
- `tests/frontend/sync-upstream.sh <新版本目录>`：覆盖 → 应用补丁 → 构建；任一步失败就停在原地并打印冲突文件

升级流程（每次上游发版）：

1. 记录新版本号（当前锚点：`plane-preview/package.json` `1.4.2`）；
2. 覆盖 `plane-preview/`（保留 `.env`，它被 gitignore）；
3. `apply-patches.sh` —— 打不上就是上游改了同一处，**这是唯一需要人看的地方**；
4. `verify-patches.sh` + `release-check.sh --fast --full-single`；
5. 补丁数量应长期稳定在 3 个；若开始增长，说明有改动没有走"外置优先"。

---

## 5. 决策表

| 你的约束 | 选择 | 树内差异 |
|---|---|---|
| 单 Go 二进制（当前） | 静态 SPA + 前端回写 | 7 个文件（3 个补丁） |
| 前端绝对零改 | Node 侧车（space SSR + apps/live） | 0 个文件，多 2 个 Node 进程 + Go 侧 Django 兼容适配 |
| 只想去掉协作补丁 | apps/live 侧车（space 仍静态） | 5 个文件（2 个补丁） |

> 无论选哪条，**7→0 的差异都来自"谁跑编辑器序列化器"这一个问题**，不是前端代码质量的问题。
