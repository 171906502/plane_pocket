# plane-pocketbase

用 **Go + PocketBase + SQLite** 复刻 Plane v1.4.2 的 REST API，
让 Plane 现有的三个 React 前端（`web` / `space` / `admin`）**零改动**运行在单个 Go 二进制上。

---

## 文档

📖 **知识库入口：[`docs/00-INDEX.md`](docs/00-INDEX.md)**

按你的角色选入口：

| 我要… | 看 |
|---|---|
| 把它跑起来 | [`docs/02-GETTING-STARTED.md`](docs/02-GETTING-STARTED.md) |
| 加/改后端接口 | [`docs/01-ARCHITECTURE.md`](docs/01-ARCHITECTURE.md) → [`docs/03-ITERATION-GUIDE.md`](docs/03-ITERATION-GUIDE.md) |
| 部署上线 | [`docs/04-DEPLOYMENT.md`](docs/04-DEPLOYMENT.md) |
| 跑测试/验收 | [`docs/05-TESTING.md`](docs/05-TESTING.md) |
| **排障** | [`docs/06-PITFALLS.md`](docs/06-PITFALLS.md) ← **只读一篇的话读这个** |
| 看进度/缺口 | [`docs/07-STATE-AND-ROADMAP.md`](docs/07-STATE-AND-ROADMAP.md) |
| 看测试报告与缺陷叙事 | [`tests/WORKFLOW_TEST_REPORT.md`](tests/WORKFLOW_TEST_REPORT.md) |

契约冻结文档（实现的权威来源）：[`spec/`](spec/) —— `api-spec.yaml` / `schema-spec.md` / `permissions.md` / `exclusions.md`

---

## 快速开始

```bash
# 0. 依赖：Go ≥ 1.27、Node 22、Microsoft Edge

# 1. 环境（Git Bash 下 APPDATA 必须 export）
export APPDATA="C:\\Users\\<你>\\AppData\\Roaming"
go env -w GOPROXY=https://goproxy.cn,direct GOSUMDB=sum.golang.google.cn

# 2. 编译
go build -o .fe.exe .

# 3. 路由锚点护栏 + 迁移自检
go test ./internal/router/ -run TestRoutePatternsAreAnchored
./.fe.exe migrate up --dir=./.tmp-verify

# 4. 起全套服务（后端 8000 / web 3000 / admin 3001 / space 3002 / live 3100）
cd tests/browser && bash start-stack.sh all

# 5. 验证
curl -s -o NUL -w '%{http_code}\n' http://127.0.0.1:8000/api/instances/   # 期望 200
```

然后浏览器打开 <http://localhost:3001/god-mode/> 完成实例初始化，再打开 <http://localhost:3000/>。

> ⚠️ 在 Git Bash 里**不要**写 `curl -o /dev/null`（会静默退出 23）。用 `-o NUL`。见 [`docs/06-PITFALLS.md`](docs/06-PITFALLS.md) C-1。

---

## 仓库结构

本目录是更大的 `plane_pocket/` 仓库的一部分（三棵树合一）：

```
plane_pocket/
├── plane-pocketbase/      ← 本目录：我们写的后端（唯一可写）
│   ├── main.go                    入口
│   ├── internal/
│   │   ├── router/                路由注册（每域一文件 + AST 护栏测试）
│   │   ├── handlers/<domain>/     业务逻辑
│   │   ├── serializers/           响应形状（Plane JSON 契约层）
│   │   └── middleware/            认证 / CSRF / 作用域解析
│   ├── pb_migrations/             79 个 collection 的定义（12 个迁移）
│   ├── spec/                      契约冻结文档（权威，改代码前先读）
│   ├── docs/                      ★ 知识库
│   └── tests/                     测试链路 + 证据
├── plane-preview/         Plane v1.4.2 前端源码（只读，绝不修改）
└── pocketbase-master/     PocketBase 源码（只读，go.mod replace 的本地依赖）
```

---

## 三条铁律

1. **绝不修改 `plane-preview/`。** 它是"前端零改动"这个核心承诺的物证。
2. **所有路由必须 `{$}` 锚定。** 未锚定的子树模式会把未注册路径用 `200 + 错对象` 回答掉，
   静默污染前端状态。改路由后必跑 `go test ./internal/router/`。
3. **响应形状以 `spec/` 为准，不凭记忆。** 形状错了前端不报错，只是渲染出貌似正常的东西。

---

## 当前状态

| | |
|---|---|
| 端到端主流程 | ✅ 已在真实浏览器中从空库跑通全链路 |
| 已注册路由 | 258 个模式 |
| 未覆盖接口 | **73 个**（约 20 个按方案剔除 / 约 30 个整模块未做 / 约 24 个边角操作） |
| 已修缺陷 | 21 个 |
| 前端代码改动 | **0 行** |
| 生产就绪 | ⚠️ **未就绪**，见 [`docs/04-DEPLOYMENT.md`](docs/04-DEPLOYMENT.md) §9 |

自查缺口（纯静态，不用起服务）：

```bash
python tests/browser/audit-endpoints.py
```
