# 04 · 部署与运维

> ⚠️ **先读 §9「未就绪清单」**。这个项目当前**尚未达到生产就绪**。
> 本文描述的是**目标形态与已知的准备工作**，不是"照做即可上线"。

---

## 1. 部署形态

整个后端是**一个 ~39 MB 的静态链接二进制 + 一个数据目录**。没有 Postgres、没有 Redis（除非要 Page 协作编辑）、没有 Python。

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

前端三端（web / space / admin）是静态产物，可以由任意静态服务器/Nginx 托管。

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

| 应用 | 服务端产物 | 客户端静态资源 |
|---|---|---|
| web | `apps/web/build/server/index.js` | `apps/web/build/client/` |
| space | `apps/space/build/server/index.js` | `apps/space/build/client/` |
| admin | `apps/admin/build/client/` | 同左 |

> **重要**：`web` 和 `space` 是 **SSR 应用**（有 `build/server/`），不是纯静态站点。
> 它们的 `package.json` 里有 `react-router-serve ./build/server/index.js`。
> `admin` 是 SPA。

构建时的 API 地址**在构建期就烧进了产物**（Vite 的 `VITE_*` 变量），
所以**生产环境的 `.env` 必须在 `build` 之前设好**：

```
plane-preview/apps/web/.env.production
  VITE_API_BASE_URL="https://api.example.com"
  VITE_WEB_BASE_URL="https://app.example.com"
  VITE_ADMIN_BASE_URL="https://admin.example.com"
  VITE_ADMIN_BASE_PATH="/god-mode"
  VITE_SPACE_BASE_URL="https://space.example.com"
  VITE_SPACE_BASE_PATH="/spaces"
  VITE_LIVE_BASE_URL="https://live.example.com"
  VITE_LIVE_BASE_PATH="/live"
```

构建完再改 `.env` 是**没用的**，必须重新 build。

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

# ── 前端 web（SSR，需要 node 运行时）──────────────────────
server {
    listen 443 ssl http2;
    server_name app.example.com;

    location / {
        proxy_pass http://127.0.0.1:3000;
        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-Proto $scheme;
    }
}
```

> **web 与 space 是 SSR 应用**，必须有一个 Node 进程跑 `react-router-serve ./build/server/index.js`，
> 不能只用 Nginx 托管静态文件。admin 是 SPA，可以纯静态托管。

### 4.3 端口规划（生产示例）

| 服务 | 内部端口 | 对外 |
|---|---|---|
| plane-pocketbase | `8000` | `api.example.com` |
| web (SSR node) | `3000` | `app.example.com` |
| admin (SPA 静态) | — | `admin.example.com` |
| space (SSR node) | `3002` | `space.example.com` |
| live (可选) | `3100` | `live.example.com` |

---

## 5. HTTPS 与 Cookie

这部分容易出错，因为**跨域 + `Secure` + `SameSite` 三者互相牵制**。

### 当前 Cookie 策略（读 `internal/middleware/auth.go`）

| cookie | HttpOnly | SameSite | Secure |
|---|---|---|---|
| `session-id` | ✅ 是 | `Lax` | 仅当请求是 TLS（或 `X-Forwarded-Proto: https`） |
| `admin-session-id` | ✅ 是 | `Lax` | 同上 |
| `csrftoken` | ❌ 否（前端要读） | `Lax` | 同上 |

### ⚠️ 生产部署必须注意的两点

**① `SameSite=Lax` 在跨站部署下会挡掉会话 cookie。**

`Lax` 的语义是：顶层导航的 GET 会带 cookie，但**跨站的 XHR/fetch 不带**。

- 如果前端与 API **同站**（例如 `app.example.com` 与 `api.example.com` —— 同一注册域 `example.com`），`Lax` 可以工作。
- 如果前端与 API **跨站**（例如 `app.example.com` 与 `api.other-domain.com`），
  会话 cookie 不会被带上，**所有接口都会 401**。

**这是当前代码的已知限制。** 跨站部署需要把 `SameSite` 改成 `None` 并**强制要求 `Secure`**，
当前 `SetSessionCookie` 硬编码了 `SameSiteLaxMode`，需要改代码。

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
└── logs.db           ← 请求日志
```

### 6.2 备份

SQLite 在 WAL 模式下**不能直接 `cp data.db`** —— 会拿到不一致的快照。
三种可行方式，按推荐度排：

```bash
# 方式 1（推荐）：用 SQLite 的在线备份，得到一致快照
sqlite3 pb_data/data.db ".backup '/backup/data-$(date +%F).db'"

# 方式 2：停服后整体打包（最简单，有停机）
systemctl stop plane-pocketbase
tar czf /backup/pb_data-$(date +%F).tar.gz pb_data/
systemctl start plane-pocketbase

# 方式 3：VACUUM INTO（SQLite 3.27+）
sqlite3 pb_data/data.db "VACUUM INTO '/backup/data-$(date +%F).db'"
```

**备份必须包含 `storage/` 目录**，否则附件全丢。

### 6.3 恢复

```bash
systemctl stop plane-pocketbase
mv pb_data "pb_data.bak-$(date +%s)"      # 不删除（项目约定）
mkdir -p pb_data
tar xzf /backup/pb_data-2026-10-04.tar.gz
systemctl start plane-pocketbase
```

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
# 1. 进程活着
curl -s -o NUL -w '%{http_code}\n' http://127.0.0.1:8000/api/instances/     # 期望 200

# 2. 迁移已应用
./plane-pocketbase migrate up --dir=/var/lib/plane-pocketbase/pb_data

# 3. 看日志
tail -f pb_data/logs.db    # 或应用日志
```

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
| **N-1** | **74 个接口未实现** | 用户点了会 404，或页面显示空 | 见 [`07-STATE-AND-ROADMAP.md`](07-STATE-AND-ROADMAP.md) 的三类清单 |
| **N-2** | **`SameSite=Lax` 在跨站部署下不工作** | 跨站部署时所有接口 401 | 改 `internal/middleware/auth.go` 的 `SetSessionCookie`，跨站时用 `SameSite=None` + 强制 `Secure` |
| **N-3** | **未做安全审计** | 未知 | 至少审查 `spec/permissions.md` 的逐端点守卫是否都已实现 |
| **N-4** | **无速率限制** | 暴力破解/滥用风险 | 反代层加，或加中间件 |
| **N-5** | **无邮件发送（Magic Link 依赖它）** | Magic Link 登录不通 | Magic Link 需要发信能力，当前未配置 |
| **N-6** | **未验证并发/负载** | 未知 | SQLite 是单写者模型，写密集场景需评估 |
| **N-7** | **备份策略未自动化** | 数据丢失风险 | 按 §6.2 配置定时任务 |
| **N-8** | **`--origins` 容易配错** | 配错 → 前端全白且日志无请求 | 部署检查清单里加一项 |
| **N-9** | **未做版本升级/迁移流程演练** | 升级可能失败 | 先在预发环境演练 `03-ITERATION-GUIDE.md` §3/§4 的流程 |
| **N-10** | **`apps/live` 依赖 Redis 且未评估生产部署** | Page 协作编辑不可用 | 若不需 Page 功能可砍；需要则要部署 live + Redis |

### 上线前最小检查清单

```
□ 74 个未覆盖接口中，属于「用户可见入口」的已补完
□ --origins 精确列出所有前端 origin（无通配符）
□ 反代转发 X-Forwarded-Proto
□ 前端与 API 同站（否则需处理 N-2）
□ --encryptionEnv 已设且密钥已妥善保管
□ PB_ENCRYPTION_KEY 等密钥不入库、不进日志
□ PLANE_PB_DEV / PLANE_PB_CSRF 未被设置
□ client_max_body_size 已调大（附件上传）
□ 备份任务已配置，且**验证过恢复流程**
□ /_/ 管理界面已限制访问
□ 已做一次完整的预发部署演练
```

---

## 10. 部署方式建议（按规模）

| 场景 | 建议 |
|---|---|
| 个人 / 小团队（< 50 人） | 单机 + 单二进制 + SQLite + Nginx 反代。最简单，够用 |
| 中型（< 500 人） | 同上，但加 systemd 托管 + 定时备份 + 监控告警。**注意 SQLite 写并发上限** |
| 大型 / 高可用 | **当前架构不适用**。SQLite 是单文件单写者，没有内建复制 |
| 不想运维前端 SSR | 把 web/space 也构建进来，用同一个 Nginx 前置 |

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
