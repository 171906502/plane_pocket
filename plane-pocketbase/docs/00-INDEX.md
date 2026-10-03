# Plane × PocketBase 项目知识库

> **一句话**：把 Plane v1.4.2 的后端（Django + PostgreSQL）替换成一个 **Go 单二进制 + SQLite**，
> 并完整复刻 Plane 的 REST API，使 Plane 现有的三个 React 前端（`web` / `space` / `admin`）
> **一行都不用改**就能跑起来。

---

## 0. 这个知识库解决什么问题

三种人（或 agent）会接手这个项目，各自需要完全不同的信息。请按你的角色选入口：

| 你的角色 | 建议阅读顺序 | 你会得到什么 |
|---|---|---|
| **我要把它跑起来** | [`02-GETTING-STARTED.md`](02-GETTING-STARTED.md) → [`06-PITFALLS.md`](06-PITFALLS.md) | 从零到浏览器里能点，以及踩坑对照表 |
| **我要加/改后端接口** | [`01-ARCHITECTURE.md`](01-ARCHITECTURE.md) → [`03-ITERATION-GUIDE.md`](03-ITERATION-GUIDE.md) → [`05-TESTING.md`](05-TESTING.md) | 架构约束、标准改法、怎么验证 |
| **我要把它部署上线** | [`04-DEPLOYMENT.md`](04-DEPLOYMENT.md) → [`07-STATE-AND-ROADMAP.md`](07-STATE-AND-ROADMAP.md) | 构建/反代/HTTPS/备份，以及**哪些还没做完** |
| **我是新来的 agent** | 本文件 → [`01-ARCHITECTURE.md`](01-ARCHITECTURE.md) → [`07-STATE-AND-ROADMAP.md`](07-STATE-AND-ROADMAP.md) | 全局心智模型 + 当前真实进度 + 下一步 |

> ⚠️ **动手前必读**：如果你只读一份，读 [`06-PITFALLS.md`](06-PITFALLS.md)。
> 这个项目里最耗时的 bug 都不是"不会写代码"，而是踩了一个静默失败的环境/框架陷阱
> （错误的路由模式返回 200、`curl -o /dev/null` 静默退出 23、端口被占 ≠ 服务可用）。这些坑每一个都花了数小时。

---

## 1. 文档地图

```
docs/
├── 00-INDEX.md              ← 你在这里：导航 + 术语表
├── 01-ARCHITECTURE.md       架构、请求生命周期、数据模型策略、认证/权限
├── 02-GETTING-STARTED.md    本地从零搭建，依赖版本，启动与验证
├── 03-ITERATION-GUIDE.md    加接口 / 改 schema / 升级依赖的标准作业流程
├── 04-DEPLOYMENT.md         生产部署、反向代理、HTTPS、备份、运维
├── 05-TESTING.md            测试分层、浏览器验收、证据规范、静态审计
├── 06-PITFALLS.md           ★ 陷阱与雷区总集（症状 → 根因 → 修法）
└── 07-STATE-AND-ROADMAP.md  已完成 / 未完成 / 优先级 / 契约文档索引
```

**契约冻结文档**（在 `plane-pocketbase/spec/`，是实现的**权威来源**，不要凭记忆写代码）：

| 文件 | 作用 |
|---|---|
| `spec/api-spec.yaml` | 391 个端点的完整清单（方法/视图类/请求响应形状/是否被前端调用） |
| `spec/schema-spec.md` | 每个 Django 模型 → PocketBase collection/field 的权威映射 |
| `spec/permissions.md` | 授权模型：两级角色轴、每个端点的守卫 |
| `spec/exclusions.md` | 明确剔除的功能（AI / GitHub / Slack / OAuth / 导入导出 / 出站 Webhook） |

---

## 2. 仓库布局

仓库根 = `plane_pocket/`（一个 Git 仓库，分支 `main`）。

```
plane_pocket/
├── plane-pocketbase/      ← ★ 我们写的代码（唯一可写）
│   ├── main.go                    入口：装配 PocketBase + 注册路由
│   ├── internal/
│   │   ├── router/                路由注册（每个 Plane 域一个文件）
│   │   ├── handlers/<domain>/     业务处理（每个域一个目录）
│   │   ├── serializers/           响应形状（Plane JSON 契约层）
│   │   └── middleware/            认证 / CSRF / 工作区与项目作用域解析
│   ├── pb_migrations/             PocketBase collection 定义（12 个迁移）
│   ├── spec/                      契约冻结文档（只读参考，是权威）
│   └── tests/                     测试链路 + 证据
├── plane-preview/         ← Plane v1.4.2 前端源码（只读，绝不修改）
└── pocketbase-master/     ← PocketBase 源码（只读，go.mod replace 的本地依赖）
```

**三条铁律**：

1. `plane-preview/` **绝对不能修改**。它是"前端零改动"这个核心承诺的物证。要改前端行为 = 改错了地方。
2. 只往 `plane-pocketbase/` 里写代码。
3. **不要在 `plane_pocket/` 之外或之内随意 `git init`** —— 嵌套 `.git` 会被 Git 记成 gitlink，导致整个子树不被纳管（详见 `06-PITFALLS.md`）。

---

## 3. 当前状态速览

> 详细版本见 [`07-STATE-AND-ROADMAP.md`](07-STATE-AND-ROADMAP.md)。这里是给新人建立预期的。

| 维度 | 状态 |
|---|---|
| 端到端主流程 | ✅ **已在真实浏览器中从空库跑通全链路**（admin 初始化 → 注册 → 工作区 → 项目 → 工作项 → 页面 → 设置 → 发布 Space） |
| 后端接口覆盖 | ⚠️ 注册 **257 条路由**，前端调用形状 150 个，其中 **74 个未覆盖** |
| 已修后端缺陷 | 21 个（其中 20 个只有真实浏览器交互才暴露） |
| 前端代码改动 | **0 行** —— 所有问题都出在复刻层 |
| 生产就绪度 | ⚠️ **未就绪**，见 `04-DEPLOYMENT.md` 的「未就绪清单」 |

**74 个未覆盖接口分三类**：约 20 个是方案内明确剔除（预期缺口）、约 30 个是整模块未做（Cycle / View / Estimate / Intake）、约 24 个是已实现模块的边角操作（`bulk-*` 批量操作、`favorite-pages/` 等列表页可见入口）。

> 自查命令：`python plane-pocketbase/tests/browser/audit-endpoints.py`（纯静态，不用起服务）

---

## 4. 术语表

代码和文档里反复出现的词，先统一含义，避免误解：

| 术语 | 含义 |
|---|---|
| **兼容层 / 复刻层** | 我们写的这套 PocketBase 后端。它的唯一职责是让 Plane 前端以为自己在跟 Django 说话 |
| **Plane UUID vs PocketBase id** | Plane 用 36 位 UUID 做主键；PocketBase 原生主键是 15 位随机 id。**两者并存**：`id` 归 PocketBase 用，Plane 的 UUID 存在独立 `uuid` 文本列里，序列化层做双向映射。详见 `01-ARCHITECTURE.md` §4 |
| **`{$}` 锚定** | Go 1.22+ `http.ServeMux` 里以 `/` 结尾的模式是**子树模式**（会吞掉所有子路径）。`{$}` 把它收窄成精确匹配。**本项目所有路由都必须锚定** |
| **子树陷阱 / Fake-200** | 未锚定的路由把未注册的子路径用 `200 + 语义无关的对象` 回答掉。这是本项目最高频、最隐蔽的 bug 类别 |
| **作用域（Scope）** | 请求被授权后解析出的上下文：当前 workspace、当前 project、调用者在两者中的角色 |
| **角色码** | `RoleGuest=5` / `RoleMember=15` / `RoleAdmin=20`。**工作区角色和项目角色是两条独立的轴**，工作区 Admin 不代表有项目角色 |
| **分页信封（Envelope）** | Plane 的游标分页响应体：`{count, total_results, next_cursor, next_page_results, results, ...}`。字段名和字段顺序都是承重的 |
| **分组信封（GroupedEnvelope）** | 当 `group_by_field_name` 存在时，`results` 从数组变成以 group id 为键的对象，且**每个分组都出现（含空组）** |
| **证据（Evidence）** | 测试产出的可审查物：截图、网络日志、脚本退出码。存放在 `tests/evidence/` 与 `tests/logs/` |
| **栈（Stack）** | 五个服务：后端 `:8000`、web `:3000`、admin `:3001`、space `:3002`、live `:3100` |
| **`.fe-run`** | 后端运行时数据目录（SQLite 库 + 上传文件）。`run-all.sh --fresh` 会把它挪走重建，**从不删除** |

---

## 5. 五分钟快速定位

想知道"某个东西在哪"时，查这张表：

| 我想找… | 去哪 |
|---|---|
| 某个 API 的响应应该长什么样 | `spec/api-spec.yaml` 搜路径；再看 `internal/serializers/` 里对应文件 |
| 某个路由注册在哪 | `internal/router/<domain>.go`，搜路径字符串 |
| 某个路由的业务逻辑 | `internal/handlers/<domain>/`，函数名与 router 里注册的一致 |
| Django 里这个模型原来是什么 | `spec/schema-spec.md` 搜 `### MODEL:` |
| 这个端点的权限要求 | `spec/permissions.md`；实现看 `internal/middleware/scope.go` |
| PocketBase 的某个字段类型有哪些选项 | `pocketbase-master/core/field_*.go`（**不要猜**） |
| 前端是怎么调用这个接口的 | `plane-preview/packages/services/src/**` |
| 这个坑之前踩过没有 | `tests/WORKFLOW_TEST_REPORT.md` 搜缺陷编号；`docs/06-PITFALLS.md` |
| 为什么某个设计是这样 | `docs/01-ARCHITECTURE.md` §8 设计决策记录 |

---

## 6. 相关文档（不在本知识库内）

| 文档 | 位置 | 说明 |
|---|---|---|
| 全流程测试报告 | `plane-pocketbase/tests/WORKFLOW_TEST_REPORT.md` | 21 个已修缺陷的完整叙事 + 逐阶段截图证据 + 最终验收 |
| 迁移总方案 | `PLANE_TO_POCKETBASE_MIGRATION_PLAN.md` | 最初的总体方案与进度 |

---

**下一步**：按上表选你的入口。第一次上手请直接看 [`02-GETTING-STARTED.md`](02-GETTING-STARTED.md)。
