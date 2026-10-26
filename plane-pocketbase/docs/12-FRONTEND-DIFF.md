# 12 · 前端差异治理（保持可升级性）

> **一句话**：`plane-preview/` 现在是**上游纯净树**（逐字节一致），本项目的全部前端改动
> 以 **4 个构建补丁** 的形式存在 `plane-pocketbase/frontend-patches/`，构建时打入、构建后还原。
> 上游升级 = 换树 + 重打补丁；补丁打不上就**构建立即失败**，不会静默漂移。

---

## 0. 原则

1. **树必须与上游逐字节一致**：`git status -- plane-preview` 在非构建期必须为空。任何"先改树再提交"的做法都会让下次升级无从判断差异。
2. **能外置的绝不进补丁**：构建期变量、安装参数、dev 服务器保护 → 放进 `plane-pocketbase/tests/` 的脚本，连补丁都不占。
3. **必需的最少化**：补丁里只保留"没有前端代码之外的实现方式"的改动。
4. **失败必须响亮**：补丁打不上 → 构建退出非零；`apply.sh` 每次 apply/revert 后都反向 `--check` 自证，杜绝"报了 applied 其实没改"（C-22）。

---

## 1. 现状：树内 0 个文件，补丁 4 个（覆盖 9 个文件）

| # | 补丁 | 覆盖文件 | 改动 | 类别 |
|---|---|---|---|---|
| 1 | `0001-tailwind-import-subpath.patch` | `packages/tailwind-config/index.css` | `@import "tailwindcss"` → `"tailwindcss/index.css"` | 构建硬修复（§1.3） |
| 2 | `0002-space-spa-mode.patch` | `apps/space/react-router.config.ts`、`app/root.tsx`、`app/page.tsx`、`app/issues/[anchor]/layout.tsx` | `ssr:false`、删 `headers` 导出、`loader → clientLoader` | 单端口 SPA 化 |
| 3 | `0003-collab-description-sync.patch` | 新增 `apps/web/hooks/use-collab-description-sync.ts`（141 行）+ `apps/web/components/pages/editor/page-root.tsx`（+2 行） | 协作描述防抖回写 | 协作回写（§2） |
| 4 | `0004-csrf-header-injection.patch` | `packages/services/src/api.service.ts`、`apps/web/services/api.service.ts` | 读 `csrftoken` cookie，非安全方法带 `X-CSRFTOKEN` | 后端 CSRF 双提交要求 |

> 第 4 个补丁此前是"隐形"的：它被混在第一次 vendoring 提交里，直到把树重建为纯净克隆才浮出来。
> 现在它是独立补丁，升级时若上游改了 `api.service.ts`，会**明确报冲突**而不是悄悄丢掉 CSRF 头（丢了就是所有写操作 403）。

### 1.1 基线锚点：**不是** tag v1.4.2

`plane-preview/package.json` 写着 `1.4.2`，但那是 `preview` 分支上没跟着 bump 的版本号。
用 blob 哈希比对 5631 个文件后确认，实际内容是：

| 项 | 值 |
|---|---|
| 仓库 | `git@github.com:makeplane/plane.git` |
| 分支 | `preview` |
| commit | `e72bf10fa529d0764a671b19d0657fb244882811` |
| 日期 | 2026-09-29 |
| 提交标题 | `fix: accept numeric-string size on issue attachment upload (#9911)` |
| 文件匹配 | 5620 / 5631 逐字节相同，11 个例外正是本地改动 + 一个临时脚本 |

这个 sha 记录在 `frontend-patches/BASE`，`verify-upstream-patches.sh` 直接读它来 clone。
**结论：不要用 tag 名做锚点**，用 sha。

### 1.2 已外置（不占补丁的 6 项环境适配）

| 原来改在哪 | 现在放在哪 | 为什么这样可以 |
|---|---|---|
| `.npmrc` 的 `node-linker isolated→hoisted` | `tests/frontend/install-deps.sh` 的 `--node-linker hoisted` | 安装参数是**机器**属性，不是项目属性（C-14） |
| `pnpm-workspace.yaml` 的 `storeDir` | 同一脚本的 `--store-dir D:/.pnpm-store` | 同上（C-16） |
| 三个 `vite.config.ts` 的 `server.watch.ignored` | `tests/frontend/clean-stale-modules.sh`（`stack.sh start` 自动调用） | 根因是**残留目录**（`node_modules.broken-*`），删掉就不需要忽略规则 |
| `apps/space/package.json` 的 `start/preview` 改 `serve -s` | 无需替代 | 我们从不执行该 app 的 `pnpm start`（Go 托管静态文件） |

```bash
# 安装依赖（唯一入口：install + 工作区链接 + packages/* 构建）
bash plane-pocketbase/tests/frontend/install-deps.sh

# 清理残留依赖树（stack.sh start 会自动调用）
bash plane-pocketbase/tests/frontend/clean-stale-modules.sh --dry-run
```

`install-deps.sh` 是一条链，三步各有原因：

| 步骤 | 脚本 | 为什么不能省 |
|---|---|---|
| 1. `pnpm install --frozen-lockfile --node-linker hoisted` | （内联） | `isolated` 在本机建不了链接（C-14）；`--frozen-lockfile` 保证 `pnpm-lock.yaml` 不被改写 |
| 2. 工作区 junction | `link-workspace-packages.sh` | pnpm 建 `node_modules/@plane/*` 静默失败，`mklink /J` 才行（C-14） |
| 3. `packages/*` 构建 | `build-workspace-packages.sh` → `build-packages.mjs` | apps 通过 `exports → dist/` 引用它们，`dist/` 被 gitignore；**不能用 turbo/pnpm 跑**（C-14b 第 3 条：每次 `pnpm run` 都重装整个 workspace） |

外置的 6 项回退成上游版本后，三端构建仍全绿（2026-10-26 实测：web 34M / admin 13M / space 15M）。

### 1.3 补丁 1 到底是谁的 bug（2026-10-26 定位，可复现）

结论先说：**是 Vite 8.0.16 的 bug，上游用同样的版本跑同样的命令一样会失败**，不是我们的安装方式
（hoisted）造成的，也不是 Tailwind 的问题。

证据链，四条：

**① 失败的解析器是 Vite 内置的 postcss-import，不是 Tailwind 的插件。**

```
[plugin vite:css] apps/web/styles/globals.css?transform-only:undefined:NaN
Error: [postcss] ENOENT: … open 'D:\…\apps\web\tailwindcss'
    at loadImportContent (vite/dist/node/chunks/postcss-import.js:392:19)
```

Tailwind 自己的解析器处理裸 `tailwindcss` **完全正常**：直接拿 `@tailwindcss/postcss` 处理
`@import "tailwindcss"` 与 `@import "tailwindcss/index.css"`，两者输出**逐字节相同**（140 824 B）。

**② 同一份配置，client 环境能解析，ssr 环境不能。** 用 Vite 自己的 `createResolver`
（即 `createCSSResolvers` 里 `css` 那个，options 与源码一致：`extensions:['.css']`、
`mainFields:['style']`、`conditions:['style','production']`、`preferRelative:true`、`tryIndex:false`）
对 `apps/web/*` 这个 importer 求值：

| 环境 | `tailwindcss` | `tailwindcss/index.css` | `@makeplane/propel/styles` |
|---|---|---|---|
| client | `…/node_modules/tailwindcss/index.css` | 同左 | `…/propel/dist/styles/propel.css` |
| **ssr** | **原样返回 `tailwindcss`（未解析）** | 正常 | 正常 |

`react-router build` 即使在 `ssr: false` 下也会跑一遍 **ssr environment**（日志里
`building ssr environment for production...`），于是这一遍必然失败。

**③ 机制：ssr 那一遍的条件集合里没有 `style`（也没有 `import`/`require`）。**

```jsonc
// tailwindcss@4.1.17 package.json
"exports": { ".": { "types": …, "style": "./index.css", "require": …, "import": … } }   // ← 没有 "default"
```

`. `里没有任何"无条件"分支，ssr 的条件集匹配不上 → 解析器返回 undefined →
postcss-import 把它当相对路径 → `apps/web/tailwindcss` ENOENT。
对照组 `@makeplane/propel/styles` 的值是**裸字符串** `"./dist/styles/propel.css"`，
不依赖条件，所以两个环境都能解析——这也解释了为什么只有 tailwind 这一行炸。

**④ 与安装布局无关（排除"是我们改成 hoisted 才坏的"）。**
把 `tailwindcss` 整个复制进 `apps/web/node_modules/` 后重跑：client 那一路的解析结果变成了
`apps/web/node_modules/tailwindcss/index.css`（说明 node_modules 查找没问题），
**ssr 那一路仍然原样返回 `tailwindcss`，报错路径一字不变**。

**所以改法与代价**：`@import "tailwindcss/index.css"` 与 `@import "tailwindcss"` 语义等价
（后者靠 `style` 字段指向同一个文件），只绕开 ssr 那一遍的解析缺陷。1 行 + 注释，
上游升级时只要不动第一行就不会冲突。

**备选（都没采用）**：`pnpm patch vite` 改解析器（比改一行 CSS 重得多）、
升级到修好这个 bug 的 Vite 版本（要动 lockfile，且未验证是否已修）、
把 `tailwindcss` 的 `exports["."]` 加 `default`（那是改 node_modules，不可持续）。

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

### 补丁 3 的做法（当前实现）

复用上游自己的事务事件 `editor.onStateChange`（不新增监听、不侵入上游逻辑），
在 `isEditorReady && isSynced` 后起 **1.5 s 防抖**，把
`{description_html, description_json, description_binary}` 通过既有的
`updatePageDescription` 管道写回。允许"数秒内的更新丢失"——这是明确接受的取舍。
`description_stripped` 由后端 `DescriptionPatch` 路径负责，补丁不碰。

### 四种可能

| 方案 | 做法 | 成本 | 风险 |
|---|---|---|---|
| **A. 保留补丁 3**（现状） | 前端编辑器把 `getDocument()` 的结果防抖回写（复用既有 `updatePageDescription` 管道） | 已完成 | 低。新增文件，不改既有交互 |
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
| 静态 SPA（现状，补丁 2） | 4 个文件 | 分享链接的 OG 标签丢失（爬虫拿不到 SSR HTML） |
| Node SSR 侧车 + Go 反代 | 0 个文件 | 生产多一个 Node 进程 |

若将来 OG 预览变重要，切到侧车即可把补丁 2 整块去掉。

---

## 4. 补丁层：目录与用法

```
plane-pocketbase/frontend-patches/
├── BASE                                # 上游 sha/branch/date/subject（锚点，别用 tag）
├── README.md                           # 补丁表 + 用法 + 升级步骤
├── apply.sh                            # apply | revert | check | status  [--target DIR]
├── 0001-tailwind-import-subpath.patch
├── 0002-space-spa-mode.patch
├── 0003-collab-description-sync.patch
└── 0004-csrf-header-injection.patch
```

```bash
# 日常（默认目标是 ../plane-preview）
bash plane-pocketbase/frontend-patches/apply.sh status     # clean | applied | partial
bash plane-pocketbase/frontend-patches/apply.sh apply
bash plane-pocketbase/frontend-patches/apply.sh revert
bash plane-pocketbase/frontend-patches/apply.sh check      # 只试打，不动树

# 对任意树（例如刚 clone 的上游）
bash plane-pocketbase/frontend-patches/apply.sh apply --target /path/to/fresh/clone
```

### 4.1 三个必须知道的坑（都已修进 `apply.sh`）

1. **`git apply` 按"仓库顶层"解析路径，不按当前目录。** 在 `<repo>/plane-preview` 里跑会打印
   `Skipped patch …` 并且**退出码 0**。所以 `apply.sh` 一律从 `git rev-parse --show-toplevel` 出发，
   带 `--directory=<show-prefix>`（C-22）。
2. **`.gitattributes` 的 `-text` 会把文件当二进制**，同样 `Skipped patch` + 退出 0。
   现在 `plane-preview/**` 用 `text eol=lf`。
3. **必须反向自证。** `apply.sh` 每次 apply 成功后立刻跑 `--reverse --check`，revert 后跑正向
   `--check`；不通过就 `FATAL: … reported success but the tree is unchanged`。这条断言就是上面两个坑的疫苗。

### 4.2 接入点

| 场景 | 行为 |
|---|---|
| `tests/browser/build-frontends.sh` | apply → 构建三端 → **EXIT trap 自动 revert**。若检测到树已是 `applied`（dev 会话占用），走"保留原样"分支 |
| `tests/browser/stack.sh start` | apply 后**保持 applied**（dev 服务器需要） |
| `tests/browser/serve-single.sh` | 只跑二进制，不碰补丁（产物已构建好） |
| `tests/frontend/verify-upstream-patches.sh` | 纯净克隆 → apply → install → 构建 → 断言树干净（见 §5） |

### 4.3 为什么构建期打补丁是安全的

补丁只改**源码文本**，构建产物（`apps/*/build`、`packages/*/dist`）一旦生成就与源码解耦；
`revert` 在构建结束的 EXIT trap 里执行，即使构建失败也会跑。生产运行时是单个 Go 二进制托管静态文件，
**没有任何 Node 进程**（`vite dev` 仅在开发期使用，属例外）。

---

## 5. 升级流程（每次上游发版）

1. `git clone git@github.com:makeplane/plane.git`（或用 `verify-upstream-patches.sh` 里的方式按 sha 取）；
2. 记录新 sha，写进 `frontend-patches/BASE`；
3. 覆盖 `plane-preview/`（**保留** `apps/{web,admin,space,live}/.env`，它们被 gitignore）；
   Windows 上还要把 `packages/i18n/locales`（上游是符号链接）物化成真实目录（C-14）；
4. `apply.sh apply` —— 打不上就是上游改了同一处，**这是唯一需要人看的地方**；
5. `bash tests/frontend/verify-upstream-patches.sh` —— 端到端自证；
6. `bash tests/release-check.sh --fast --full-single` —— 发布门禁。

补丁数量应长期稳定在 **4 个**；若开始增长，说明有改动没有走"外置优先"（§0 第 2 条）。

### 5.1 端到端验证脚本做了什么

`tests/frontend/verify-upstream-patches.sh`（`--no-build` 可只验补丁）：

```
clone @ BASE.sha  →  物化 packages/i18n/locales  →  apply.sh apply --target
  →  断言 git status 恰好是预期的 9 个文件（多一个少一个都失败）
  →  install-deps.sh（含工作区链接 + packages/* 构建）
  →  build-frontends.sh（PLANE_FE_DIR 指向克隆）
  →  断言构建结束后树仍然是干净的（revert 生效）
```

这条链路把"补丁能否在纯净上游上重放"从口头承诺变成一条命令。

---

## 6. 决策表

| 你的约束 | 选择 | 树内差异 |
|---|---|---|
| 单 Go 二进制（当前） | 静态 SPA + 前端回写 | 0 个文件 / 4 个补丁（9 个文件） |
| 前端绝对零改 | Node 侧车（space SSR + apps/live） | 0 个文件 / 1 个补丁（仅 CSRF），多 2 个 Node 进程 |
| 只想去掉协作补丁 | apps/live 侧车（space 仍静态） | 0 个文件 / 3 个补丁 |

> 无论选哪条，差异的根源都是同一个问题：**谁跑编辑器序列化器**。
> 补丁层只是把这个问题的成本变成了"可见、可测、可回退"的 4 个文件。
