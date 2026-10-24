# 04 · 部署与运维

> ⚠️ **先读 §9「未就绪清单」**。这个项目当前**尚未达到生产就绪**。
> 本文描述的是**目标形态与已知的准备工作**，不是"照做即可上线"。

---

## 1. 部署形态

整个后端是**一个 ~39 MB 的静态链接二进制 + 一个数据目录**。没有 Postgres、没有 Redis、没有 Python。
Pages 富文本协作由同进程内的 `internal/live` 提供（2026-10-23），**不需要 `apps/live` 也不需要 Redis**。

```
目标机器上只需要：
/
├── plane-pocketbase          ← 单个可执行文件（Linux/Windows/macOS）
├── pb_data/                  ← 数据目录（SQLite + 上传文件 + 日志）
│   ├── data.db
│   ├── auxiliary.db
│   ├── storage/              ← 附件、头像
│   └── logs.db
└── (可选) pb_public/          ← 静态文件目录
```

前端三端（web / admin / space）**都是纯静态产物**（`react-router.config.ts` 里 `ssr: false`），
可由任意静态服务器 / Nginx 托管，也可以直接由这个 Go 二进制托管（见 §4.4），
**生产环境不需要任何 Node 进程**。

---

## 2. 构建

### 2.1 后端

```bash
cd plane-pocketbase
export APPDATA="C:\\Users\\<你>\\AppData\\Roaming"    # Git Bash 下必须

# 交叉编译到 Linux 服务器（在 Windows 上构建）
GOOS=linux GOARCH=amd64 go build -ldflags "-s -w" -o plane-pocketbase-linux-amd64 .

# 或本机平台
go build -ldflags "-s -w" -o plane-pocketbase .
```

`-ldflags "-s -w"` 去掉符号表和调试信息，体积明显减小。

> **CGO 不需要**。SQLite 驱动是 `modernc.org/sqlite`（纯 Go），
> 所以交叉编译开箱可用，不需要装 C 交叉工具链。

### 2.2 前端静态产物

```bash
cd plane-preview

# 三端分别构建（web / space / admin）
pnpm --filter web build
pnpm --filter space build
pnpm --filter admin build
# 或一次全建
pnpm build
```

产物位置（`react-router build` 的约定）：

| 应用 | 服务端产物 | 客户端静态资源 | 运行时 |
|---|---|---|---|
| web | 无（`ssr: false`） | `apps/web/build/client/` | 静态托管 |
| admin | 无（`ssr: false`） | `apps/admin/build/client/` | 静态托管 |
| space | 无（`ssr: false`，2026-10-18 起） | `apps/space/build/client/` | 静态托管 |

> **三端现在都是纯静态 SPA**（`react-router.config.ts` 里都是 `ssr: false`），
> 产物只有 `build/client/`，`package.json` 的 `start` 都是 `serve -s build/client`。
> **生产环境不需要任何 Node 进程。**
>
> `space` 原先用 SSR，为了能由单个 Go 二进制托管改成了 SPA。代价与改动见 §4.4。
> 注意：SPA 模式的 `react-router build` 仍会生成 `build/server/`——那是**构建期**
> 用来渲染 `index.html` 壳的，**部署时不需要**，只需拷 `build/client/`。

构建时的 API 地址**在构建期就烧进了产物**（Vite 的 `VITE_*` 变量），
所以**生产环境的地址必须在 `build` 之前设好**，构建完再改是没用的。

**但不要写 `.env.production`** —— 每个 app 的 `vite.config.ts` 里是：

```ts
dotenv.config({ path: path.resolve(__dirname, ".env") });   // 只读 .env
define: { "process.env": JSON.stringify(viteEnv) }          // 整体替换
```

`.env.production` / `.env.local` 一概不看，写了不生效（2026-10-19 实测，见 **C-19**）。
可用的两条路：

```bash
# 路线 A：单端口模式（Go 一个二进制同时托管三端 + API，推荐先读这个）
cd plane-pocketbase/tests/browser
bash build-frontends.sh          # 内部 export VITE_* 后逐个构建，自带产物自检

# 路线 B：自己控制地址 —— 改 apps/<app>/.env 里的多端口/多域名值，再逐个构建
cd plane-preview/apps/web   && ./node_modules/.bin/react-router build
cd plane-preview/apps/admin && ./node_modules/.bin/react-router build
cd plane-preview/apps/space && ./node_modules/.bin/react-router build
```

变量清单（`build-frontends.sh` 顶部改 `BASE_URL` 一处即可，默认 `http://localhost:8000`）：

```
VITE_API_BASE_URL    后端地址
VITE_WEB_BASE_URL    web 端地址（单端口下 = API 地址）
VITE_ADMIN_BASE_URL  admin 端地址
VITE_ADMIN_BASE_PATH /god-mode
VITE_SPACE_BASE_URL  space 端地址
VITE_SPACE_BASE_PATH /spaces
VITE_LIVE_BASE_URL   live 协作服务地址
VITE_LIVE_BASE_PATH  /live
```

> 为什么 export 能赢：dotenv **不覆盖已存在的 process.env 变量**。
> `vite.config.ts` 的 `define: { "process.env": ... }` 会整体替换，
> 所以预先 export 的值会一路带进产物；`.env` 里的旧值则只在 dev 时生效。
> dev 用 `.env` 的多端口值跑 `react-router dev`，两者互不干扰。

---

## 3. 后端启动参数

`serve` 命令的完整选项：

| 参数 | 说明 |
|---|---|
| `--http` | 监听地址。**默认 `127.0.0.1:8090`**（仅本机），部署时一般设 `0.0.0.0:8000` 或交给反代 |
| `--https` | HTTPS 监听地址。设了之后 HTTP 会自动重定向到 HTTPS |
| `--origins` | **CORS 允许的 origin 列表，必填**，精确匹配，逗号分隔。默认 `[*]` —— **生产环境绝不能用默认值** |
| `--dir` | 数据目录，默认 `./pb_data` |
| `--encryptionEnv` | 环境变量名，其值（32 字符）用作配置加密密钥 |
| `--queryTimeout` | SELECT 查询超时秒数，默认 30 |
| `--dev` | dev 模式：把日志和 SQL 打到控制台 |

### 生产启动示例

```bash
# 后端在 127.0.0.1:8000，由 Nginx 反代
./plane-pocketbase serve \
  --http=127.0.0.1:8000 \
  --dir=/var/lib/plane-pocketbase/pb_data \
  --origins="https://app.example.com,https://space.example.com,https://admin.example.com" \
  --encryptionEnv=PB_ENCRYPTION_KEY
```

> **`--origins` 值必须是前端的真实 origin（scheme + host + port）**，
> 且**不能用通配符** —— 前端 axios 配了 `withCredentials: true`，浏览器在带凭证模式下拒绝 `*`。
> 见 `01-ARCHITECTURE.md` §8.2。

### 环境变量

| 变量 | 建议 | 说明 |
|---|---|---|
| `PB_ENCRYPTION_KEY` | 设（32 字符） | 供 `--encryptionEnv` 使用，加密 app settings |
| `PLANE_ADMIN_BASE_URL` | 设 | 管理端基地址 |
| `PLANE_PB_DEV` | **不要设** | 生产环境关掉 dev 模式 |
| `PLANE_PB_CSRF` | **不要设** | 生产环境必须保持 CSRF 校验开启 |
| `PLANE_COOKIE_SAMESITE` | 不设（默认 `lax`） | Cookie 的 SameSite 属性：`lax` / `strict` / `none`。**仅跨站部署时设 `none`**（`app.foo.com` + `api.bar.com`）；设为 `none` 会强制 `Secure`，因为浏览器拒绝不带 `Secure` 的 `SameSite=None` cookie。同站部署保持默认即可 |
| `PLANE_MAGIC_LOG` | 设（路径） | 凭据投递目标文件（兼容旧名）。**当前不发邮件**（无 SMTP），Magic Link 验证码与密码重置 token 都写入该文件供管理员转达。**不设则退回进程日志**（开发用）。文件以 `0600` 追加打开（POSIX；Windows 需靠 NTFS ACL 限制目录）。见 N-5 |
| `PLANE_MAIL_LOG` | 可选（路径） | 上项的**通用别名**；两者都设时以本变量为准。名字更中性，因为该文件现在同时承载验证码和重置 token（T-5 统一投递通道）。格式 `RFC3339<TAB>kind=<magic-sign-in\|password-reset><TAB>email<TAB>code/token<TAB>expires_at=…` |
| `PLANE_RATELIMIT_AUTH_PER_MIN` | 不设（默认 `30`） | 凭据类端点（`sign-in` / `magic-generate` / `magic-sign-in` / `forgot-password`）的**每 IP 每分钟**令牌数，突发上限 `10`。**`0` = 关闭限流**（仅测试/排障用）。见 N-4 |
| `PLANE_WEB_DIR` | 不设（默认走 Nginx） | 设成 `apps/web/build/client` 的绝对路径，即可**由 Go 二进制直接托管 web 前端**，挂在 `/`，未知路径回退 `index.html`。见 §4.4 |
| `PLANE_ADMIN_DIR` | 不设（默认走 Nginx） | 同上，指向 `apps/admin/build/client`，挂在 `/god-mode`。见 §4.4 |
| `PLANE_SPACE_DIR` | 不设（默认走 Nginx） | 同上，指向 `apps/space/build/client`，挂在 `/spaces`。见 §4.4 |
| `PLANE_GZIP` | 不设（默认开启） | 设 `0` 关闭内置 gzip 中间件。只有在**前面已有 Nginx/CDN 做压缩**、或怀疑压缩导致问题时才关。见 §4.4 |

---

## 4. 反向代理

### 4.1 关键要求

| 要求 | 原因 |
|---|---|
| 必须转发 `Host` 和 `X-Forwarded-Proto` | `isSecureRequest()` 靠 `X-Forwarded-Proto: https` 判断要不要给 cookie 打 `Secure` 标记。不转发的话 cookie 不带 `Secure`，有安全风险 |
| 前端与后端**同站不同源**时，CORS 的 `--origins` 必须列全 | 见上 |
| 请求体大小上限 | 附件上传需要，默认 Nginx `client_max_body_size` 是 1MB，**必须调大** |
| 不要缓存 `/api/` | 动态数据 |

### 4.2 Nginx 骨架

```nginx
# ── 后端 API ──────────────────────────────────────────────
server {
    listen 443 ssl http2;
    server_name api.example.com;

    ssl_certificate     /etc/letsencrypt/live/api.example.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/api.example.com/privkey.pem;

    # 附件上传：默认 1m 会直接 413
    client_max_body_size 50m;

    location / {
        proxy_pass http://127.0.0.1:8000;
        proxy_http_version 1.1;

        proxy_set_header Host              $host;
        proxy_set_header X-Real-IP         $remote_addr;
        proxy_set_header X-Forwarded-For   $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;   # ← 必须，控制 cookie Secure 标记

        # 实时协作需要长连接
        proxy_set_header Upgrade    $http_upgrade;
        proxy_set_header Connection "upgrade";
        proxy_read_timeout 3600s;
    }
}

# ── 前端 web（纯静态，不需要 node）────────────────────────
server {
    listen 443 ssl http2;
    server_name app.example.com;

    root /srv/plane/web;          # apps/web/build/client
    index index.html;

    # SPA 路由回退；admin / space 的路径前缀要先于这条命中
    location / {
        try_files $uri $uri/ /index.html;
    }
}
```

> **三端都是纯静态 SPA**（`ssr: false`），Nginx 托管各自的 `build/client/` 即可，
> 记得加 `try_files ... /index.html` 做路由回退。**不需要 Node。**

### 4.3 端口规划（生产示例）

**对外只暴露 `443` 一个端口**。内部端口只监听 `127.0.0.1`，由反代转发；
`web` / `admin` / `space` 都是静态文件，**根本不占端口**。

| 服务 | 生产形态 | 内部端口 | 必需性 |
|---|---|---|---|
| plane-pocketbase | Go 二进制 | `8000` | **必需** |
| web | 静态文件 | — | **必需** |
| admin | 静态文件（`basename=/god-mode`） | — | 可选（不要后台管理可省） |
| space | 静态文件（`basename=/spaces`） | — | 可选（不要公开分享页可省） |
| live | Node + Redis | `3100` | **不需要**（协作端已在 Go 后端内，2026-10-23） |

两种拓扑都支持：

- **同域路径前缀**（推荐，仓库自带的 `apps/proxy/Caddyfile.ce` 就是这个设计）：
  `/` → web 静态，`/god-mode` → admin 静态，`/spaces` → space 静态，
  `/api`、`/auth` → `127.0.0.1:8000`。
- **多子域**：`app.` / `admin.` / `space.` / `api.` 各自一个 server 块。

> 路径前缀是**构建期**烧进产物的（`VITE_ADMIN_BASE_PATH` / `VITE_SPACE_BASE_PATH`），
> 所以改了前缀必须重新 build，不能只改 Nginx。
> 最小可用部署 = **api（8000）+ 三个静态目录 + 一个 Nginx**，**零 Node 进程**。

### 4.4 让 Go 直接托管前端（不用 Nginx 托管静态文件）

**可以，三端都能托管。** `internal/router/static.go` 提供了 opt-in 的静态托管，
用的是 PocketBase 自带的 `apis.Static(fsys, indexFallback=true)`——自带 SPA 路由回退，
不需要自己写 `try_files`。

```bash
# 三个环境变量，都不设就等于没这个功能（默认走 Nginx）
PLANE_WEB_DIR=/srv/plane/web      # apps/web/build/client   → 挂在 /
PLANE_ADMIN_DIR=/srv/plane/admin  # apps/admin/build/client → 挂在 /god-mode
PLANE_SPACE_DIR=/srv/plane/space  # apps/space/build/client → 挂在 /spaces
```

启动后一个进程同时提供 API 与前端：

```
/           → web 静态（未知路径回退 index.html）
/assets/*   → web 静态资源
/god-mode/* → admin 静态
/spaces/*   → space 静态（未知路径回退 index.html）
/api/*      → Plane REST API
/_/         → PocketBase 自带后台
```

**已实测**（2026-10-18，8099 端口，用 admin/space 的**真实构建产物**验证）：

| 请求 | 结果 |
|---|---|
| `/spaces/` | `200`，`Cache-Control: no-cache`，返回 space 的 index.html |
| `/spaces/issues/demo-anchor` | `200`，回退 index.html（**SPA 深链可用**） |
| `/spaces/assets/<hash>.js` | `200`，`application/javascript`，`Cache-Control: public, max-age=31536000, immutable` |
| `/spaces/assets/missing.js` | `200` 回退 index.html，但 `Cache-Control: no-cache`（**不会把 HTML 缓存一年**） |
| `/god-mode/`、`/god-mode/assets/<hash>.js` | `200`，缓存策略同上 |
| `/api/instances/` | 正常返回 API JSON，**没有被 catch-all 吞掉** |
| `/_/` | `200`，PocketBase 自带后台仍可用 |
| `/spaces/assets/../../../pb_data/data.db` | `404`（路径穿越被挡） |

`/spaces/` 还会带上原先由 `headers` 导出的安全头：`Referrer-Policy`、
`X-Content-Type-Options`、`X-DNS-Prefetch-Control`、`Strict-Transport-Security`、
`X-Frame-Options: SAMEORIGIN`。

> 为什么不会吞掉 `/api`：catch-all 注册为 `/{path...}`，Go 1.22 的 `ServeMux`
> **永远优先匹配更具体的模式**，所以 `/api/instances/` 这类精确路由先命中。
> 静态路由绑定在 `Priority: 999`，确保其它路由都已注册完。

**缓存策略按「文件是否真实存在」判定，而不是按 URL 前缀。**
因为 `apis.Static` 对未知路径回退 `index.html`，若按 `/assets/` 前缀一刀切，
那么 `/assets/typo` 这种深链会把 HTML 缓存一年，重新发布后仍指向旧的 bundle hash。
所以只有「真实存在且位于 `assets/` 下」的文件才给 `immutable`。

**space 从 SSR 改成 SPA 的三个连带改动**（都在 `apps/space`）：

1. `app/root.tsx`、`app/page.tsx` 里的 `headers` 导出被删除——SPA 模式直接报
   `SPA Mode: 1 invalid route export(s)`。这些头改由 Go 静态处理器下发（见上）。
2. `app/issues/[anchor]/layout.tsx` 的 `loader` 改成 `clientLoader`——SPA 模式同样禁止 `loader`。
3. `package.json` 的 `start` 从 `react-router-serve ./build/server/index.js`
   改成 `serve -s build/client`。

> **代价**：爬虫拿到的是空 HTML 壳，分享出去的 issue 链接不再有服务端渲染的 OG 标签。
> 人访问不受影响——元数据在水合后仍会拉取并写进 `<head>`。
> 如果哪天 SEO/链接预览比「零 Node」更重要，把 `ssr` 改回 `true` 并恢复这三处即可。

**gzip 已内置**（`internal/middleware/gzip.go`，绑定在 CORS 之后、所有路由之前，
对 API 与静态资源同时生效）：

- 只压「文本类」响应：HTML / JS / CSS / JSON / SVG / XML / WASM；
- 只压 ≥ 1 KB 的响应。小响应先缓冲再原样回放，并补上 `Content-Length`，
  避免每个小 JSON 都退化成 chunked；
- **不压**：`Range` 请求（字节偏移和压缩流对不上）、已带 `Content-Encoding` 的响应、
  1xx/204/304、`text/event-stream`（`/api/realtime` 必须逐条直达客户端）、
  以及 PNG / WebP / woff2 / mp4 / zip 这类已压缩格式；
- 压缩时补 `Vary: Accept-Encoding`，并删掉 `Content-Length` / `Accept-Ranges`。

实测（2026-10-18，`/spaces` 的**真实构建产物**，同一份二进制）：

| 请求 | 原始 | 线上传输 | 压缩比 |
|---|---|---|---|
| `/spaces/assets/page--3ScAE-D.js` | 1,989,435 | 563,150 | **3.5×** |
| `/spaces/index.html` | 4,284 | 1,713 | 2.5× |
| `/api/health`（50 B） | 50 | 50 | 不压 |
| `/spaces/favicon/android-chrome-512x512.png` | 14,419 | 14,419 | 不压 |
| `Range: bytes=0-99` | — | `206` + 100 B | 不压 |

> 压缩后的响应仍保留原来的 `Cache-Control`（hashed 资源 `immutable`、HTML `no-cache`），
> 配合 `Vary` 由浏览器/CDN 分别缓存两种变体。
> 关掉的办法：`PLANE_GZIP=0`（只在前面已有 Nginx/CDN 压缩时才有必要）。

**三个已知代价**（都是实测的，不是猜测）：

1. **只有 gzip，没有 brotli**。Go 标准库不带 brotli，接入要引第三方包，收益是
   再省约 15%。若前面有 Nginx，让它做 brotli、`PLANE_GZIP=0` 关掉内置压缩即可。
2. **安全头硬编码在 Go 里**。`space` 那五个头写在 `internal/router/static.go` 的
   `spaceSecurityHeaders`，改规则要重新编译；走 Nginx 则改配置即可。
3. **没有 CDN / 限流 / HTTP/2 多路复用**。这些要靠前面的 Nginx 或 CDN。

> **Pages 富文本编辑：2026-10-23 起已由 Go 端补齐，不再需要 Node。**
> 早前（2026-10-19）单端口下进入 Pages 编辑器会整页卡死：编辑器连
> `ws://<origin>/live/collaboration`，而这个路径落到 SPA catch-all，返回 `200 text/html`
> 而不是 `101 Switching Protocols`，provider 无限重连。
> 现在 `internal/live` 在同一个二进制里实现了这个端点（Hocuspocus 帧 + yjs 更新中继 +
> 落盘回放 + awareness），详见 [`10-REMAINING-WORK.md`](10-REMAINING-WORK.md) §3.3。
> 仍然**不需要 Redis**（那是 `apps/live` 多实例协调才需要的东西），也**不需要 Node**。
>
> 代价与限制（已知并接受）：
> - 日志**会合并**：单文档超过 1 MiB 时向在线客户端要一次全量快照，把 N 条 update 压成
>   1 条（旧文件留 `<log>.bak`）；`PLANE_LIVE_COMPACT_BYTES=0` 可关掉。
> - 日志**会清理**：启动时扫描 `<data>/live/`，页面已从库里彻底消失的日志连同 `.name`
>   sidecar 一起删除（软删除的页面保留，恢复后内容还在）。
> - 服务端不解析 CRDT，派生 HTML 由前端回写（`use-collab-description-sync.ts`，防抖 1.5s，
>   并在**页面加载后**补写一次——中继回放不触发编辑器的 `onStateChange`）；同一次回写还
>   存 `description_binary`（yjs 全量快照），日志万一丢了可以据此重建文档。
> - 只支持单进程：多实例横向扩展仍需跨进程总线（本项目目标就是单二进制）。
>
> 浏览器端到端验收（2026-10-23）：打开/输入/重载/双标签页实时同步/服务重启后回放
> 全部通过，`description_html`、`description_json`、`description_stripped` 均正确落库。
>
> Issues、Cycles、Modules、Views、项目设置、Space 只读页原本就正常
> （`run1`–`run3`、`run5` 全过）。

> 缓存这块已经补上了：`assets/` 下**真实存在**的文件给
> `public, max-age=31536000, immutable`，其余（index.html、SPA 回退）给 `no-cache`。

**什么时候值得这么做**：小团队、单机、想少一个 Nginx 进程。现在三端都能托管，
**整个生产环境只剩一个二进制**。
**什么时候不值得**：需要 CDN / HTTP/2 多路复用 / 限流。

#### 单端口模式：从零到跑起来（4 步）

```bash
# 1) 构建三端静态产物（BASE_URL 默认 http://localhost:8000，脚本内改）
cd plane-pocketbase/tests/browser
bash build-frontends.sh

# 2) 编译后端（改了 Go 必须重编，跑旧二进制是「改了没效果」的头号原因）
cd ../..
go build -o .fe.exe .

# 3) 单端口启动：一个命令，脚本自己 export 三个静态目录 + origins
bash tests/browser/serve-single.sh          # 默认 127.0.0.1:8000
bash tests/browser/serve-single.sh 9000     # 换端口（必须先按新 BASE_URL 重 build）
```

第 4 步是浏览器打开 `http://localhost:8000/`——web、`/god-mode/`、`/spaces/`、
`/api/*`、`/_/` 全在同一个端口，没有任何 Node 进程。

`serve-single.sh` 只是把那串容易漏的环境变量固化下来，等价于手敲：

```bash
export PLANE_WEB_DIR=../plane-preview/apps/web/build/client
export PLANE_ADMIN_DIR=../plane-preview/apps/admin/build/client
export PLANE_SPACE_DIR=../plane-preview/apps/space/build/client
./.fe.exe serve --http=127.0.0.1:8000 --dir=.fe-run \
  --origins="http://localhost:8000,http://127.0.0.1:8000"
```

三个 `PLANE_*_DIR` **都是可选的**：一个都不设，二进制就退回 API-only 模式
（`/api/*`、`/_/` 正常，所有 SPA 路由返回 404 JSON）。看到 404 先查这三个变量，
别去查路由——这是最容易误判的一步。`--origins` 里 localhost 与 127.0.0.1
**必须都写**，浏览器视其为两个不同 origin。

**端到端实测**（2026-10-19，全新数据目录，真实构建产物，单一进程）：

| 验证项 | 结果 |
|---|---|
| `/`、`/god-mode/`、`/spaces/`、`/api/instances/`、`/_/` | 全部 `200` |
| 三端入口 JS/CSS（`entry.client-*.js`、`root-*.css`） | 全部 `200`，`content-type` 正确 |
| 深链回退 `/workspaces/.../issues/...`、`/god-mode/profile`、`/spaces/<anchor>` | 全部 `200` 回退 index.html |
| 监听端口 | **只有 8000**（`netstat` 确认，3000/3001/3002/3100 均未监听） |
| 产物残留多端口地址 | `web`/`admin`/`space` 全量 grep `localhost:300[012]` → **0 个文件** |
| gzip | `entry.client-*.js` 4,600→1,817 B；`/api/timezones/` 12,011→2,185 B，均带 `Content-Encoding: gzip` |
| space 安全头 | 五个头全部随 `/spaces/` 下发，`Cache-Control: no-cache` |
| admin 重定向 | `POST /api/instances/admins/sign-up/`（缺字段）→ `302` + `Location: /god-mode/?error_code=...`，**相对地址**，单端口下落在同一 origin，正确 |

> `admin_base_url` 在单端口模式下**留空就是对的**：`adminBaseHost()` 无 env 时返回
> 相对路径 `/god-mode/`，浏览器对着 8000 解析即中。
> 多端口部署才需要 `PLANE_ADMIN_BASE_URL`（见 `internal/handlers/instance/auth.go` 注释）。

---

## 5. HTTPS 与 Cookie

这部分容易出错，因为**跨域 + `Secure` + `SameSite` 三者互相牵制**。

### 当前 Cookie 策略（读 `internal/middleware/auth.go`）

| cookie | HttpOnly | SameSite | Secure |
|---|---|---|---|
| `session-id` | ✅ 是 | `PLANE_COOKIE_SAMESITE`（默认 `Lax`） | 仅当请求是 TLS（或 `X-Forwarded-Proto: https`）；`SameSite=none` 时强制 |
| `admin-session-id` | ✅ 是 | 同上 | 同上 |
| `csrftoken` | ❌ 否（前端要读） | 同上 | 同上 |

### ⚠️ 生产部署必须注意的两点

**① `SameSite=Lax` 在跨站部署下会挡掉会话 cookie。**

`Lax` 的语义是：顶层导航的 GET 会带 cookie，但**跨站的 XHR/fetch 不带**。

- 如果前端与 API **同站**（例如 `app.example.com` 与 `api.example.com` —— 同一注册域 `example.com`），`Lax` 可以工作。
- 如果前端与 API **跨站**（例如 `app.example.com` 与 `api.other-domain.com`），
  会话 cookie 不会被带上，**所有接口都会 401**。

**跨站部署的解法（2026-10-11 已实现）**：设置 `PLANE_COOKIE_SAMESITE=none`。
三个会话相关 cookie（`session-id`、`admin-session-id`、`csrftoken`）都会以
`SameSite=None` 下发，并**强制 `Secure`**——浏览器本来就拒绝不带 `Secure` 的
`SameSite=None` cookie，所以跨站部署必须上 HTTPS。清除 cookie 时会重复相同
属性，保证浏览器真的替换掉旧值。同站部署保持默认（不设该变量）即可。

**② `Secure` 依赖 `X-Forwarded-Proto`。**

`isSecureRequest()` 的实现是：

```go
func isSecureRequest(e *core.RequestEvent) bool {
    if e.Request.TLS != nil { return true }
    return strings.EqualFold(e.Request.Header.Get("X-Forwarded-Proto"), "https")
}
```

所以**反代必须转发这个头**，否则 cookie 不带 `Secure`，在 HTTPS 站点上会被浏览器拒绝或降级。

### 建议的部署拓扑

**推荐：前端与 API 同站不同子域**（`app.example.com` + `api.example.com`），
这样 `SameSite=Lax` 直接可用，不用改代码。

---

## 6. 数据与备份

### 6.1 数据目录内容

```
pb_data/
├── data.db           ← 主库（所有 Plane 数据）
├── data.db-wal       ← WAL 日志（必须一起备份）
├── data.db-shm
├── auxiliary.db      ← PocketBase 内部（日志、设置等）
├── storage/          ← 上传的文件（附件、头像、logo）
├── live/             ← 协作编辑的页面正文日志（漏了页面正文就丢，见 §4.4）
└── logs.db           ← 请求日志
```

### 6.2 备份（已自动化并实测）

**推荐：直接用仓库里的脚本**（`tests/backup.sh`）。它做的是在线备份 API，
**服务运行中也能用**；`cp data.db` 不行（WAL 下会拿到不一致快照）。

```bash
# 备份到 backups/<UTC 时间戳>/，保留最近 7 份
bash tests/backup.sh
# 常用参数
bash tests/backup.sh --data-dir /srv/plane/pb_data --dest /backup --keep 30 --label nightly
```

脚本产出：

```
backups/20261015T123143Z/
├── data.db              ← sqlite3 `.backup`（读穿 WAL，一致快照）
├── auxiliary.db
├── storage/             ← 附件/头像/logo（漏了就全丢）
├── live/                ← 协作页面正文日志（漏了正文回退到上次快照）
└── MANIFEST.txt         ← 每个快照的 sha256 + 关键表行数 + storage/live 文件数 + 所用工具
```

- 快照工具优先用 `sqlite3` CLI（`sqlite3 src ".backup dst"`），没有则退回
  python 的 backup API；两者等价。
- `MANIFEST.txt` 是**恢复时的校验依据**——没有它 `restore.sh` 会拒绝恢复。
- **`live/` 不能漏**：页面正文的真相在这里，`pages.description_binary` 只是
  压缩时落下的周期性快照（§4.4）。漏掉它，恢复出来的页面要么空、要么退回
  上一次压缩的版本。`live/` 用普通 `cp` 就够——日志是
  `[varuint 长度][字节]` 顺序追加，读取端会跳过被截断的最后一条记录
  （`internal/live/store.go`），最坏情况只丢最新一次编辑，不会整页损坏。
- 保留策略只删 `--dest` 下、名字形如时间戳的目录，不会碰到别的东西。
- 两者都没有（既无 sqlite3 也无 python）时用 `--tar`：**必须先停服**。
  `--tar` 归档整个目录，`live/` 天然包含在内。

定时任务示例（crontab，每天 03:30，保留 30 份）：

```cron
30 3 * * *  cd /srv/plane/plane-pocketbase && bash tests/backup.sh --dest /backup --keep 30 --label nightly >> /var/log/plane-backup.log 2>&1
```

systemd 用户也可以写成 `plane-backup.service` + `plane-backup.timer`（`Type=oneshot`）。

### 6.3 恢复（先校验，再换，不删旧数据）

```bash
bash tests/restore.sh backups/20261015T123143Z
```

三条硬规则：

1. **先校验**：逐个比对 `MANIFEST.txt` 里的 sha256，任一不符即中止
   （要强行恢复得显式 `--force`）。
2. **旧数据不删**：现有 `pb_data` 被 `mv` 成 `pb_data.bak-<epoch>`，恢复出错时
   `mv` 回来即可。
3. **恢复后复查**：与 manifest 的行数逐表比对，不符则报错。

恢复会一并还原 `storage/` 与 `live/`。备份里没有 `live/` 时脚本会明确提示
（而不是静默跳过），此时页面正文只能退到 `description_binary` 快照。

### 6.4 演练结果（不是"应该能恢复"，是"实测能恢复"）

两个演练脚本都是可重复执行的，证据落在 `tests/evidence/`：

| 脚本 | 断言数 | 实测结果 | 证据 |
|---|---|---|---|
| `tests/backup-drill.sh` | 19 | **19 passed / 0 failed** | `evidence/backup-drill-20261024T033534Z.txt` |
| `tests/upgrade-drill.sh` | 12 | **12 passed / 0 failed** | `evidence/upgrade-drill-20261024T032700Z.txt` |

**`backup-drill.sh` 测出的关键事实**（2026-10-15）：

- 造一个**未 checkpoint 的 WAL**（写入方持连接、`wal_autocheckpoint=0`，200 行只在
  `data.db-wal` 里），然后：
  - **负对照**：`cp data.db` 的副本**一行都读不到**（连表都不存在，因为建表语句也在 WAL 里）
    ——证明这个演练真的能识别"直接拷文件"这种错误做法；
  - 在线备份**200 行全在**。这正是 §6.2 说"不能直接 cp"的实证。
- 备份 → 删 7 条 issue + 改 1 个用户 + 删 1 个附件 **+ 删掉整个 `live/`** → 恢复 →
  **数据库、storage、live 三者的内容指纹与备份前逐字节一致**。
- 恢复后的库再跑 `migrate up` 是 **0 条重放**（schema 完好）。
- 被篡改的备份（末尾加 1 字节）**被拒绝**，且没有写出任何目录。
- `--keep 3` 跑 5 次后**恰好留 3 份**。

**2026-10-24 补测**：新增 `live/` 相关的两条断言（备份必须逐字节捕获 `live/`、恢复后
`live/` 树哈希必须与 manifest 一致），并做了**反向验证**——把 `live/` 从
`backup.sh` 里摘掉后重跑，恰好这两条 FAIL，其余 17 条照旧 PASS，证明它们不是空断言。
同一天也修掉了 `upgrade-drill.sh` 的一个启动问题：它起服时没带 `--origins`，
落到 PocketBase 的 `*` 默认值，被生产 CORS 守卫（D-8）拒绝启动；现在显式传
`http://127.0.0.1:$PORT,http://localhost:$PORT`。

**`upgrade-drill.sh` 测出的关键事实**（2026-10-15）：

- **重复 `migrate up` 是幂等的**：0 条重放，数据指纹不变 → 部署新版本本身安全。
- 迁移后的库能**正常起服并响应**（`/api/instances/` 200）。
- ⚠️ **`migrate down` 不是回滚手段**：它**会要交互确认**（`Do you really want to
  revert the last N applied migration(s)? (y/N)`），**且会连数据一起毁掉**
  （实测回退 21 条后 `issues` 表都没了）。
  **回滚 = 恢复备份**，见 §6.3。
- ⚠️ **迁移历史行丢失时 `migrate up` 会失败**（实测：删掉
  `1759000013_init_dashboards.go` 的历史行后重跑，报
  `Collection name must be unique`）。这种状态（部分恢复/部署中断）**用备份恢复即可救回**。
- `migrate history-sync` 能清掉指向已不存在迁移文件的历史行。

---

## 7. PocketBase 内建管理界面

PocketBase 自带一个数据库管理界面（`/_/`），可以用来直接看/改 collection 与记录。
**它已编译进二进制**（通过 `pocketbase-master/ui/embed.go` 的 `//go:embed all:dist`）。

> 这解释了一个仓库配置细节：**根 `.gitignore` 绝对不能加 `**/dist/`**，
> 否则 `ui/dist` 被忽略，`go build` 会以 `pattern all:dist: no matching files found` 直接失败。
> 详见 [`06-PITFALLS.md`](06-PITFALLS.md)。

生产环境建议：
- 要么在反代层把 `/_/` 限制到内网/IP 白名单
- 要么直接屏蔽（数据维护走 API）

---

## 8. 运维检查项

### 启动后自检

```bash
# 1. 健康检查（无需认证；会真的对库跑 SELECT 1）
curl -s http://127.0.0.1:8000/health          # 期望 200 {"status":"ok","version":"…","db":"ok"}
#   库不可用时：503 {"status":"degraded","version":"…","db":"error"}

# 2. 业务路由活着
curl -s -o NUL -w '%{http_code}\n' http://127.0.0.1:8000/api/instances/     # 期望 200

# 3. 迁移已应用
./plane-pocketbase migrate up --dir=/var/lib/plane-pocketbase/pb_data

# 4. 看日志
tail -f pb_data/logs.db    # 或应用日志
```

> `/health` 是**探活+探库**：PocketBase 内建的 `/api/health` 恒返回 200 且**从不碰数据库**，
> 库挂了它照样报健康，不能作为就绪探针。本端点的 `db` 字段来自真实的 `SELECT 1`，
> 适合直接接到负载均衡/容器编排的 liveness/readiness 探针。
> 它不经过 auth 与 CSRF 中间件（探针不带 cookie），只暴露状态与版本，不泄露任何业务数据。

### 日志观察

应用日志（`--dev` 模式或 stderr）里有两种关键形态：

```
INFO  GET /api/workspaces/acme/issues/            ← 路由匹配，handler 跑了
ERROR GET /api/workspaces/acme/typo-path/
└─ The requested resource wasn't found.            ← 路由没匹配上（mux 层）
```

排障时这是**第一手资料**，能立刻区分"接口没实现"和"数据没有"。

### 磁盘

SQLite 不会自动回收空间。长期运行后 `data.db` 可能膨胀：

```bash
sqlite3 pb_data/data.db "PRAGMA wal_checkpoint(TRUNCATE);"
sqlite3 pb_data/data.db "VACUUM;"
```

---

## 9. ⚠️ 未就绪清单（上线前必须处理）

**这是最重要的一节。** 当前状态**不适合直接对外服务**。

| # | 问题 | 影响 | 处理方向 |
|---|---|---|---|
| **N-1** | **静态审计 26 种路径形状未匹配，其中真实待办为 0**（旧文的接口缺口计数已作废） | 无用户可见的坏接口 | 2026-10-15 实测 `tests/browser/audit-endpoints.py`：已注册 **339** 个模式 / 前端 **150** 个调用形状 / **26** UNSERVED。逐条归类 = **15 项方案内排除 + 10 项 `epics` 幽灵分支 + 1 项三元模板误报**，真实缺口 **0**。逐条明细（含每条依据）见 [`10-REMAINING-WORK.md`](10-REMAINING-WORK.md) §T-4 与 [`07-STATE-AND-ROADMAP.md`](07-STATE-AND-ROADMAP.md) §3。**不要为消数字而实现排除项**（见 `spec/exclusions.md`）。 |
| ~~**N-2**~~ | ~~**`SameSite=Lax` 在跨站部署下不工作**~~ | ~~跨站部署时所有接口 401~~ | ✅ **已解决**（2026-10-11）：`PLANE_COOKIE_SAMESITE=none` 即 `SameSite=None` + 强制 `Secure`（session/admin/CSRF 三个 cookie 一并生效），见 §3 环境变量表与 §5 |
| ~~**N-3**~~ | ~~**未做安全审计**~~ | ~~未知~~ | ✅ **已完成**（2026-10-15，三轮）：525 条路由逐域核对守卫，三轮合计修 **10 处真实偏离**（含 1 处读取型泄漏、1 处 Guest 越权），全部配内容断言 + 反向验证，见 [`09-SECURITY-AUDIT.md`](09-SECURITY-AUDIT.md) |
| ~~**N-4**~~ | ~~**无速率限制**~~ | ~~暴力破解/滥用风险~~ | ✅ **已解决**（2026-10-15）：`internal/middleware/ratelimit.go` 对凭据类端点（`sign-in` / `magic-generate` / `magic-sign-in` / `forgot-password`）做**每 IP 令牌桶限流**，默认 30/min、突发 10，超限返回 `429` + `Retry-After` + `{"detail": …}`（与 DRF 节流同形）。`PLANE_RATELIMIT_AUTH_PER_MIN=0` 可关闭。**按 IP 分桶依赖 `X-Forwarded-For`**，仅当反代**覆写**该头时可信；且分桶键**已剥掉源端口**（否则每条新连接各占一桶，限流形同虚设），见 §3 环境变量表 |
| **N-5** | **无邮件发送（Magic Link 依赖它）** | Magic Link 登录不通 | **日志文件级已实现**（2026-10-15）：设 `PLANE_MAGIC_LOG` 后验证码写入独立文件（0600、追加、含邮箱/验证码/过期时间），管理员可转达；**真正的 SMTP 发信仍未做**，属功能降级而非缺口。见 §3 环境变量表 |
| ~~**N-6**~~ | ~~**未验证并发/负载**~~ | 按设计目标**可接受** | **不做压测**（2026-10-15 决定）：本项目设计目标是**小团队内部使用**，性能余量足够；SQLite 单写者是**有意取舍**而非待优化项。容量判断见 §10，其中已明确标注"非实测数字" |
| ~~**N-7**~~ | ~~**备份策略未自动化**~~ | ~~数据丢失风险~~ | ✅ **已解决**（2026-10-15）：`tests/backup.sh`（在线快照 + `storage/` + `MANIFEST.txt` 校验和 + 保留策略）与 `tests/restore.sh`（先校验、旧目录 `mv` 不删、恢复后对账）。**恢复流程已实测**：`tests/backup-drill.sh` 19/19 通过，含"未 checkpoint 的 WAL 下 `cp data.db` 会丢数据、在线备份不丢"的负对照，见 §6.2–§6.4 |
| ~~**N-8**~~ | ~~**`--origins` 容易配错**~~ | ~~配错 → 前端全白且日志无请求~~ | ✅ **已解决**（2026-10-15）：`internal/config/startup.go` 在 `OnServe` 时自检——**生产环境**下 `--origins` 为空或含 `*` 时**拒绝启动**（exit≠0）并打印可操作的修复提示；**dev** 下仅告警放行。部署检查清单仍保留该项作为第二道防线（见下） |
| ~~**N-9**~~ | ~~**未做版本升级/迁移流程演练**~~ | ~~升级可能失败~~ | ✅ **已完成**（2026-10-15）：`tests/upgrade-drill.sh` 12/12 通过。实测结论：重复 `migrate up` 幂等；迁移后起服正常；**`migrate down` 会要交互确认且连数据一起毁掉 → 回滚只能靠恢复备份**；迁移历史行丢失时 `migrate up` 会失败，用备份恢复可救回。见 §6.4 |
| ~~**N-10**~~ | ~~**`apps/live` 依赖 Redis 且未评估生产部署**~~ | ~~Page 富文本**协作编辑**不可用~~ | ✅ **已解决**（2026-10-23）：不引入 Redis、不跑 Node，改为后端进程内的纯 Go 中继 `internal/live/`（`GET /live/collaboration`）。update 原样落盘 `<data>/live/<doc>-<hash>.log` 并按序回放，awareness 同步；派生 HTML 由前端回写。浏览器端到端验收通过（打开/输入/重载/双标签页/重启后回放），见 [`10-REMAINING-WORK.md`](10-REMAINING-WORK.md) §3.3 §3.4。**代价**：只支持单进程。日志超过 1 MiB 时向在线客户端要一次全量快照做 compaction（旧文件留 `.bak`），启动时清理页面已消失的孤儿日志；`description_binary` 存 yjs 全量快照，日志丢了可据此重建（见 `10-REMAINING-WORK.md` §3.5）。原决策（2026-10-16 暂不实现）作废 |

### 上线前最小检查清单

```
□ 26 项静态未匹配形状已逐条核对为「方案内排除 / `epics` 幽灵 / 模板误报」，真实待办为 0（见 §9 N-1、`10-REMAINING-WORK.md` §T-4）
□ --origins 精确列出所有前端 origin（无通配符）
□ 反代转发 X-Forwarded-Proto
□ 前端与 API 同站（否则需处理 N-2）
□ --encryptionEnv 已设且密钥已妥善保管
□ PB_ENCRYPTION_KEY 等密钥不入库、不进日志
□ PLANE_PB_DEV / PLANE_PB_CSRF 未被设置
□ PLANE_MAGIC_LOG / PLANE_MAIL_LOG 已指向受限目录（若启用 Magic Link 或密码重置）
□ 凭据类端点限流符合预期（默认 30/min；PLANE_RATELIMIT_AUTH_PER_MIN 可调，置 0 即关闭）
□ /health 已接入负载均衡 / 编排的存活与就绪探针
□ client_max_body_size 已调大（附件上传）
□ /_/ 管理界面已限制访问
□ **Pages 富文本编辑可用**：协作端由 `internal/live` 提供（`GET /live/collaboration`，同进程同端口），
  浏览器里打开任一页面能正常输入、刷新后内容仍在（见 `10-REMAINING-WORK.md` §3.4）；
  日志目录 `<data>/live/` 已纳入备份
□ 备份任务已配置，且**验证过恢复流程**（`bash tests/backup-drill.sh` 应 19/19 通过）
□ 已做一次完整的预发部署演练（`bash tests/upgrade-drill.sh` 应 12/12 通过）
□ 已明确**回滚方式 = 恢复备份**（不要用 `migrate down`，它会毁数据，见 §6.4）
```

---

## 10. 部署方式建议（按规模）

> **设计目标**：本后端面向**小团队内部使用**（几十人量级、写操作不密集）。
> 这是刻意选择的取舍，不是尚未优化的结果——因此**没有做吞吐压测**，
> 下表是按架构特性给出的判断，不是实测数字。真要上更大规模，先换掉 SQLite。

| 场景 | 建议 |
|---|---|
| 个人 / 小团队（< 50 人） | 单机 + 单二进制 + SQLite + Nginx 反代。最简单，够用 |
| 中型（< 500 人） | 同上，但加 systemd 托管 + 定时备份 + 监控告警。**注意 SQLite 写并发上限**（单写者：写事务串行，写密集时会出现等待） |
| 大型 / 高可用 | **当前架构不适用**。SQLite 是单文件单写者，没有内建复制 |
| 不想跑 Node 进程 | `web` / `admin` 是纯静态，只有 `space` 需要 Node；不需要公开分享页就**一个 Node 都不用起** |

**systemd 单元示例**：

```ini
[Unit]
Description=Plane PocketBase backend
After=network.target

[Service]
Type=simple
User=plane
WorkingDirectory=/opt/plane-pocketbase
Environment=PB_ENCRYPTION_KEY=<32字符密钥>
Environment=PLANE_ADMIN_BASE_URL=https://admin.example.com
ExecStart=/opt/plane-pocketbase/plane-pocketbase serve \
  --http=127.0.0.1:8000 \
  --dir=/var/lib/plane-pocketbase/pb_data \
  --origins="https://app.example.com,https://admin.example.com,https://space.example.com" \
  --encryptionEnv=PB_ENCRYPTION_KEY
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
```

> 注意：密钥写在 unit 文件里会进系统日志。生产环境用 `EnvironmentFile=` 指向一个 0600 权限的文件。

---

**下一篇**：测试与验收见 [`05-TESTING.md`](05-TESTING.md)；
未就绪项的具体清单见 [`07-STATE-AND-ROADMAP.md`](07-STATE-AND-ROADMAP.md)。
