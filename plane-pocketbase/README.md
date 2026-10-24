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

# 4. 起全套服务（后端 8000 / web 3000 / admin 3001 / space 3002；live 3100 暂不部署，见 N-10）
cd tests/browser && bash stack.sh start

# 5. 看状态 / 验证
bash stack.sh status
curl -s -o NUL -w '%{http_code}\n' http://127.0.0.1:8000/api/instances/   # 期望 200

# 6. 用完停掉（服务是 disown 起的，关终端不会停它们）
bash stack.sh stop
```

### 栈管理：`stack.sh`

`tests/browser/stack.sh` 是唯一的入口，它只做转发，真正的实现在
`start-stack.sh` / `stop-stack.sh` / `reset-data.sh` 里：

```bash
bash stack.sh start   [backend|web|admin|space|live ...]   # 不写服务名 = 全部（live 属 N-10，暂不实现）
bash stack.sh stop    [服务...]
bash stack.sh restart [服务...]        # 先停后起；停失败就不起，避免复用死端口
bash stack.sh status                   # 只读探测，UP / DEAD / down
bash stack.sh logs    [服务]
bash stack.sh reset   [--start]        # 清库，见下
```

`status` 区分三种状态：`UP`（在答）、`DEAD`（端口被占但没人答 = dev server 起崩了）、
`down`（端口空着）。**"端口被占用"不等于"服务可用"**，见 `docs/06-PITFALLS.md`。

### 重置数据库：`stack.sh reset`

```bash
bash stack.sh reset          # 交互式，问两次
bash stack.sh reset --start  # 清完顺手把栈起回来
```

- **两次确认**：第一次要手打 `reset`，第二次要照着屏幕输入一个**当场随机生成**的
  6 位码。随机码是防"回车习惯"和防复制粘贴的，脚本里**没有** `--force` 之类跳过确认的开关。
- **不删数据**：旧库整体 `mv` 到 `_scratch/db-backups/.fe-run-<时间戳>/`，随时可搬回来。
- **先停栈**：SQLite 文件被后端占着时 Windows 不允许移动，所以会先停栈；停不掉就整体中止。
- 非终端环境（管道 / CI / agent 沙箱）直接拒绝执行。
- 想非交互地拿一个空库，用 `bash run-all.sh --fresh`。

> ⚠️ 必须在 **Git Bash** 里跑。在 cmd.exe / 另一个 shell 里敲 `bash`
> 会被解析成 WSL 启动器，报一堆 `cd: No such file or directory`。
> 详见 [`docs/06-PITFALLS.md`](docs/06-PITFALLS.md) §C-11。

然后浏览器打开 <http://localhost:3001/god-mode/> 完成实例初始化，再打开 <http://localhost:3000/>。
`localhost` 与 `127.0.0.1` 两种写法都已放进后端 `--origins`，随便用哪个都行。

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
| 已注册路由 | 339 个模式 |
| 静态审计未匹配 | **26 种路径形状**：14 项方案内排除、12 项不可达的 `epics` 分支误报；不等于 26 个真实缺口，见 [`docs/08-IMPLEMENTATION-PLAN.md`](docs/08-IMPLEMENTATION-PLAN.md) §11 |
| 方法级覆盖 | ✅ 已全域盘点并清零（2026-10-10）：前端 368 个 (方法, 路径形状) 调用，METHOD-MISSING **0**；剩余 UNSERVED 均为排除项 / `epics` 幽灵 / 三元模板误报 |
| 模块行为覆盖 | ✅ Cycle / View / Intake 复核完成（2026-10-11）：多值过滤、`?filters=<JSON 表达式树>` 富过滤、收件箱过滤条与排序均已补齐并配内容断言测试；已知降级见 [`docs/07-STATE-AND-ROADMAP.md`](docs/07-STATE-AND-ROADMAP.md) §3.2 |
| 已修缺陷 | 25 项已索引（完整流程报告叙述其中 21 项） |
| 前端代码改动 | **0 行** |
| 生产就绪 | ⚠️ **未就绪**，见 [`docs/04-DEPLOYMENT.md`](docs/04-DEPLOYMENT.md) §9 |

自查缺口（纯静态，不用起服务）：

```bash
python tests/browser/audit-endpoints.py   # 路径级
python tests/browser/audit-methods.py     # 方法级（覆盖 #23/#24 类"路径在、方法缺"缺陷）
bash tests/release-check.sh               # 发布前一键验收：build/vet/测试/迁移/审计基线比对
```
