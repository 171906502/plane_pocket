# 11 · 实现完整度复核与后续任务清单

> **生成日期**：2026-10-24（实测基线）。**本文是后续排期的唯一入口**。
> `10-REMAINING-WORK.md` 保留历史决策与实施记录（§3.1–§3.5），不再新增待办；
> 新任务一律登记到本文件。
>
> **基线**：342 个已注册路由模式 / 82 张业务表（另有 2 张 SQLite 统计表）/ 三端 SPA 单端口 + 进程内 Yjs 中继。
> 上一轮门禁：`bash tests/release-check.sh --fast --full-single` → **7 passed / 0 failed**。

---

## 0. 结论（只看这一段也够）

**契约面（REST 路径与方法）已经做满**，剩下的不是"少接口"，而是**"接口在、数据永远空"**。

| 轴 | 结论 |
|---|---|
| **路径/方法覆盖** | 342 模式；26 条未匹配**全部已归类**（15 项方案内排除 + 10 项 `epics` 幽灵 + 1 项三元模板误报）；`METHOD-MISSING = 0`。**真实缺口 0** |
| **写入路径覆盖** | 82 张业务表：53 张有写入口、14 张只读、15 张从未被引用。**其中 5 张只读表对应 5 个真实功能缺口** |
| **浏览器端到端** | `run1`–`run5` 全绿（含 Pages 协作编辑、发布到 Space） |
| **功能完整度** | 主体可用；**4 处派生数据缺失**（活动流 / 版本历史 / 通知 / 最近访问）+ **1 处建模缺失**（估算点） |

一句话：**能点、能存、能读，但"系统自己产生的事件"没有产生**。
根因只有一个 —— 这个后端**没有副作用层**（全仓 0 个 `OnRecord*` 钩子，见 §4）。

### 0.1 分域评分

| 域 | 状态 | 说明 |
|---|---|---|
| 认证 / 实例 / 速率限制 | ✅ 完整 | 密码重置走日志文件（既定决策） |
| 工作区 / 成员 / 邀请 | ✅ 完整 | |
| 项目 / 设置 / 项目成员 | ✅ 完整 | |
| 工作项 CRUD / 评论 / 关系 / 附件 / 订阅 / 反应 / 投票 | ⚠️ 主体完整 | 缺活动流、版本历史、估算点（L-1/L-2/L-3） |
| 周期（Cycle） | ⚠️ 主体可用 | 估算合计恒 0、燃尽图空、分组视图退化（L-2/L-6） |
| 模块（Module） | ⚠️ 主体可用 | 估算合计恒 0（L-2） |
| 视图 / 筛选 / 布局 | ✅ 完整 | `group_by` 在 cycle-issues 上未实现（L-6） |
| Intake | ✅ 完整 | |
| Pages（含协作编辑） | ✅ 完整 | 版本历史空（L-3） |
| 分析 / Dashboard | ⚠️ 部分 | 「最近活动」空（依赖 L-1）、燃尽图空（依赖 L-1） |
| 通知 / 收件箱 | ❌ 空壳 | 只有消费端，没有生产端（L-4） |
| 公共 API v1 | ⚠️ 端点完整 | `/api/v1/` 文档页是占位 HTML（L-8） |
| 管理后台（admin） | ✅ 完整 | |
| 运维（备份 / 健康 / 门禁） | ⚠️ 一处缺口 | 备份脚本漏了 `live/`（L-5） |

---

## 1. 评估方法（可复现）

三条轴，缺一条都会得出错误结论（路径全绿 ≠ 功能可用）。

**轴 1 · 路径与方法审计**（契约面）

```bash
bash tests/release-check.sh --fast      # 静态层，含 L2/L3 审计
# 输出：registered patterns / path UNSERVED / method UNSERVED / METHOD-MISSING
```

**轴 2 · 写入路径扫描**（功能面）—— 本次新增的检查手段

对 `data.db` 里每张表，在 `internal/**/*.go`（不含 `_test.go`）中查找表名字面量，
判断其 400 字符窗口内是否出现 `NewRecord` / `insert into`：

```python
# 简化版：有引用 + 有写入 = 可用；有引用 + 无写入 = 只读壳；无引用 = 死表
for t in tables:
    refs  = [f for f,s in src.items() if '"%s"' % t in s]
    write = any("NewRecord" in window or "insert into" in window for ...)
```

> ⚠️ 该方法有**假阳性**：经 `createJoin("draft_issue_assignees", …)` 这类
> 辅助函数写入的表，字面量离 `NewRecord` 太远，会被误判为只读
> （`draft_issue_*` 四张即属此类）。判定为"只读"后必须**人工打开读取点确认**。

**轴 3 · 前端入口核对**（用户面）

对每个只读表，反查前端是否有可见入口（`components/` 下的组件 + `services/` 下的调用）：

```bash
grep -rn "versions/" --include=*.tsx apps/web/components/pages/
grep -rn "activity_type" --include=*.tsx apps/web/components/issues/
```

三者交叉后剩下的，才是真缺口。

---

## 2. 当前基线（2026-10-24 实测）

```
registered patterns : 342
path UNSERVED       : 26   (baseline 26 —— 全部已归类，见 07 §3.1/§3.1b)
method UNSERVED     : 49   (baseline 49 —— 同上)
METHOD-MISSING      : 0
```

**82 张业务表的写路径分布**

| 分类 | 数量 | 代表 |
|---|---|---|
| 有写入口 | 53 | `issues`、`pages`、`cycles`、`file_assets`、`issue_relations` … |
| 只读（读接口在、写入口无） | 14 | `issue_activities`、`page_versions`、`notifications`、`user_recent_visits`、`page_logs` … |
| 从未被引用（死表） | 15 | `teams`、`workspace_themes`、`issue_types`、`issue_blockers`、`descriptions` … |

**14 张只读表逐条定性**

| 表 | 定性 | 依据 |
|---|---|---|
| `issue_activities` | ❌ **缺口 L-1** | 4 处读（issue history / dashboard / 活动导出 / public API），0 处写 |
| `page_versions` | ❌ **缺口 L-3** | `page.VersionsList` 读，无写 |
| `issue_description_versions` | ❌ **缺口 L-3** | issue/intake 两处读，无写 |
| `notifications` | ❌ **缺口 L-4** | 路由只有 GET/PATCH/DELETE + mark-read，**没有创建入口** |
| `user_recent_visits` | ❌ **缺口 L-7** | `view/recent.go` 读，无写 |
| `page_logs` | ⚠️ L-9（前端无入口） | `page.go:1140` 读，无写；前端无 service |
| `description_versions` | 死表 | 上游同名 `db_table`，本项目用 `issue_description_versions` |
| `draft_issue_*`（4 张） | 正常 | 经 `createJoin` 写入（扫描假阳性） |
| `accounts` / `changelogs` / `instance_configurations` | 正常 | 只读即合理（OAuth 未纳入 / 本地变更日志 / 实例配置） |

---

## 3. 任务清单

> 每项都写明 **独占文件**（避免并行改同一包互相打断编译）、**验收标准**（可证伪）、**风险**。
> 优先级定义：**P0 = 用户能看见的残缺**；**P1 = 一致性与运维**；**P2 = 清理**。

| ID | 任务 | 优先级 | 状态 | 独占文件 | 预估 |
|---|---|---|---|---|---|
| **L-1** | 工作项活动流（`issue_activities` 写入） | P0 | 待开始 | `internal/hooks/activity*.go`(新)、`internal/router/router.go`（挂注册） | 中 |
| **L-2** | 估算点（`issues.estimate_point` + 聚合） | P0 | 待开始 | `pb_migrations/`(新)、`internal/handlers/issues/wave1.go`、`internal/serializers/{issue,module}.go`、`internal/handlers/cycle/cycle.go` | 小 |
| **L-3** | 版本历史（页面 + 工作项） | P0 | 待开始 | `internal/hooks/version*.go`(新)、`internal/handlers/page/page.go`（仅恢复路径） | 中 |
| **L-4** | 通知生成（分配 / 提及 / 评论） | P0 | 待开始 | `internal/hooks/notify*.go`(新)、`internal/handlers/notification/notification.go` | 中大 |
| **L-5** | 备份覆盖 `live/` 目录 | P1 | **已验收** | `tests/backup.sh`、`tests/restore.sh`、`tests/backup-drill.sh` | 小 |
| **L-6** | Cycle 分组视图（`group_by`/`sub_group_by`） | P1 | 待开始 | `internal/handlers/cycle/issue.go`、`internal/serializers/issues.go` | 中 |
| **L-7** | 最近访问（`user_recent_visits` 写入） | P1 | 待开始 | `internal/handlers/view/recent.go`、`internal/handlers/issues/*.go`（仅补一行记录） | 小 |
| **L-8** | 公共 API 文档页 | P1 | 待开始 | `internal/handlers/publicapi/publicapi.go` | 小 |
| **L-9** | 页面 ↔ 工作项关联（`page_logs`） | P2 | **暂不做** | `internal/handlers/page/page.go` | 小 |
| **L-10** | 死表与文档一致性清理 | P2 | 待开始 | `pb_migrations/`(新)、`docs/07` | 小 |

**状态取值**：`待开始` / `进行中` / `已验收` / `暂不做`。完成一项就改一格 —— 这张表是排期真相。

---

### L-1 · 工作项活动流（`issue_activities` 写入）

**现象**：工作项详情的 Activity 页签**只剩评论**；周期燃尽图空；Dashboard「最近活动」空；
工作区活动 CSV 导出空；个人主页活动空。

**证据**

- 读：`internal/handlers/issues/wave2.go:132`（`GET …/issues/{id}/history/`）、
  `internal/handlers/dashboard/stats.go:149`（recent activity widget）、
  `internal/handlers/workspaces/activity_export.go:128`、`internal/handlers/publicapi/subresources.go:252`
- 写：**无**（全仓 `grep OnRecord` 为空）
- 前端：`components/issues/issue-detail/issue-activity/activity/actions/*.tsx`
  已实现 assignee / cycle / module / state / priority / attachment 等十余种渲染器 —— 只是没有数据

**目标**：工作项的**属性变更**产生一条 `issue_activities` 记录，形状与上游一致。

**契约（上游权威）**

- `apps/api/plane/bgtasks/issue_activities_task.py:557` `create_issue_activity(...)`
  —— 创建时写一条 `verb="created"`、`comment="created the issue"`；
  之后每个被改字段写一条，字段名进 `field`，新旧值进 `old_value/new_value`，
  关联对象另存 `old_identifier/new_identifier`。
- 表结构（已存在）：`actor, verb, field, old_value, new_value, old_identifier,
  new_identifier, comment, issue, issue_comment, project, workspace, epoch, attachments`。

**实现要点**

1. 新建 `internal/hooks/` 包，在 `router` 里 `hooks.Register(app)` 挂 `app.OnRecordAfterCreateSuccess` /
   `OnRecordAfterUpdateSuccess`（**这是本项目第一次引入记录钩子**，见 §4）。
2. 监听 `issues`（属性）、`issue_comments`（`verb="comment"`）、`issue_relations`。
3. 用 `record.Original()` 拿旧值做 diff；**只记录白名单字段**：`state, priority,
   assignees, cycle, module, labels, start_date, target_date, estimate_point, parent, name`。
4. 写 `issue_activities` 时**必须**先置一个"正在写派生数据"的标记，否则钩子递归。
   PocketBase 的 `OnRecordAfterCreateSuccess` 只对 HTTP 请求触发的保存生效，
   内部 `app.Save()` 不触发 —— **先验证这一点**，再决定是否需要防重入。
5. 批量操作（bulk update）只写一条 `verb="updated"` 汇总，不要按行爆炸。

**验收（可证伪）**

```bash
# 1) 改状态 → history 出现一条 state 活动
PATCH /api/workspaces/hrms/projects/<p>/issues/<i>/ {"state":"<other-state-uuid>"}
GET   /api/workspaces/hrms/projects/<p>/issues/<i>/history/?activity_type=issue-property
#   → 至少 1 条 {"field":"state","old_value":"Todo","new_value":"In Progress"}

# 2) 反向验证：注释掉 hooks.Register 里的 activity 注册 → 上面的 GET 返回 []
#    → 自动化测试必须 FAIL，还原后 PASS

# 3) 燃尽图不再为空（依赖本条 + L-2 的数据）
GET /api/workspaces/hrms/projects/<p>/cycles/<c>/analytics/ → completion_chart 非 {}
```

**风险**：钩子里做重查询会拖慢每次保存。**只允许**用 `record.Original()` + 单条 insert，
禁止在钩子里再查列表。

---

### L-2 · 估算点（`issues.estimate_point` + 聚合）

**现象**：周期/模块卡片上的「估算」永远是 0；周期分析 `type=points` 退化成按工作项数。

**证据**

- `data.db` 的 `issues` **没有 `estimate_point` 列**（`pragma table_info(issues)` 实测）
- `internal/handlers/issues/wave1.go:405` 注释直接写着 `estimate_point` **"被忽略"**，
  批量更新（`:414`）与单条 PATCH 的白名单里都没有它 —— 前端选了估算点，后端**静默丢弃**
- 前端 UI 是完整的：`components/issues/issue-detail/sidebar.tsx:183-185` 直接
  `issueOperations.update(…, { estimate_point: val })`；`packages/types/src/issues/issue.ts` 有该字段
- `internal/handlers/cycle/cycle.go:604-609` 六个 `*_estimate_points` 硬编码 `0`
- `internal/serializers/module.go:49-53` 同样硬编码
- `cycle.go:621-623` 注释已声明这是"降级"，不是遗漏

**目标**：工作项可以挂一个估算点；周期/模块的合计与分布**真的算出来**。

**实现要点**

1. 迁移新增 `issues.estimate_point`（relation → `estimate_points`，可空）。
   `estimate_points` 表已有写入口（`internal/handlers/estimate/estimate.go`）。
2. `issues` 的创建/更新（`internal/handlers/issues/*.go`）接受 `estimate_point`（uuid）。
3. `serializers.Issue` 输出该字段（前端 `TIssue.estimate_point` 已有类型）。
4. `cycle.go` 的六个字段改为按 `state.group` 聚合 `estimate_point.value`；
   `module.go` 同理。
5. 周期分析 `type=points` 走真实聚合。

**验收（可证伪）**

```bash
# 1) 设置估算点
PATCH …/issues/<i>/ {"estimate_point":"<ep-uuid>"}    # → 204
GET   …/issues/<i>/                                    # → estimate_point 非 null

# 2) 聚合正确
GET …/cycles/<c>/                                        # total_estimate_points == Σ
# 3) 反向：把 cycle.go 的聚合改回 0 → 上面的断言必须 FAIL
```

**风险**：低。唯一的坑是 `estimate_points` 是项目级的，跨项目引用要在保存时校验。

---

### L-3 · 版本历史（`page_versions` + `issue_description_versions`）

**现象**：页面 `⋯ → Version history` 永远空；工作项的「描述历史」永远空。
**这是目前唯一"内容写坏了还能救回来"的能力，而它没有生产端。**

**证据**

- `page.VersionsList` / `VersionDetail` / `RestoreVersion` 三个接口都在，但没有任何写入
- `issue_description_versions` 同理（`intake/wave2.go` 与 issue 路由只读）
- 前端 UI 完整：`components/pages/version/`、`components/pages/navigation-pane/tab-panels/info/version-history.tsx`、
  `components/core/description-versions/`

**契约（上游权威）**：`apps/api/plane/bgtasks/page_version_task.py`

```
PAGE_VERSION_TASK_TIMEOUT = 600 秒
description_html 变了才记；
最近一条 version 属于同一用户 且 距今 ≤ 600s  → 原地更新
否则                                          → 新建
保留上限 20 条，超出删 last_saved_at 最早的一条
```

**实现要点**

1. 钩子监听 `pages` 与 `issues` 的 `description_html` 变化。
2. **必须节流**：协作编辑是高频写入（1.5s 一次 PATCH），不节流会在几分钟内刷满 20 条上限。
   按上游规则：同用户 10 分钟内原地更新。
3. 版本行要一并快照 `description_binary`（yjs 全量），恢复时才不会把编辑器搞空
   —— 这正是 `live` 中继的 `description_binary` 修复（`10` §3.5）带来的额外收益。
4. 恢复接口已存在（`RestoreVersion`），确认它写回 `description_binary` 后，
   **同时清掉该页的 live 日志**，否则中继回放会把旧内容盖回来。

**验收（可证伪）**

```bash
# 1) 编辑页面两次（间隔 > 10 分钟，或换一个用户）→ 版本数 +1
GET …/pages/<id>/versions/    # → 长度从 0 变 1 再变 2

# 2) 恢复
POST …/pages/<id>/versions/<v>/restore/   # → 页面正文回到 v 的内容
GET  …/pages/<id>/                        # → description_html 与 v 一致

# 3) 上限
连续保存 25 次 → versions 长度恒为 20

# 4) 反向：去掉节流（每次都新建）→ 用例 3 必须 FAIL
```

**风险**：中。写入放大（每次保存多一行 + 一份 binary）。**上限 20 条必须实现**，否则磁盘会涨。

---

### L-4 · 通知生成（`notifications` + `issue_mentions`）

**现象**：收件箱永远空；被分配、被 @ 提及都收不到任何东西。
`notifications` 的路由只有消费端（list / detail / mark-read / archive），**没有创建入口**。

**证据**

- `internal/router/notification.go`：GET/PATCH/DELETE + mark-read/archive，无 POST create
- `internal/handlers/notification/notification.go` 的 `Save(` 只出现在 mark-read / prefs，
  不是"生成通知"
- `issue_mentions` 表**从未被引用**（`NO-REF` 桶）

**目标**：分配给我、@ 我、评论我订阅的工作项 → 我收到一条通知（含未读数）。

**契约（上游权威）**

- 生成点：`apps/api/plane/bgtasks/email_notification_task.py` 与各 view 里的
  `IssueNotification` 创建；通知种类见前端 `components/workspaces/notification/`
  的 `notification-card/*` 分支（assigned / mentioned / commented / state_change / …）。
- `notifications` 字段：`data(json), entity_identifier, entity_name, message, message_html,
  message_stripped, project, receiver, sender, triggered_by, read_at, archived_at, snoozed_till`。

**实现要点**

1. 先做**最小闭环**：`assigned`（assignee 变化）+ `mentioned`（`<mention-component>` 解析）
   + `commented`（评论给订阅者）。其余类型留 TODO。
2. 提及解析：从 `description_html` 里抽 `entity_identifier`（前端渲染的是
   `<mention-component entity_name="user_mention" entity_identifier="<uuid>">`），
   同时写 `issue_mentions`。
3. 尊重 `user_notification_preferences`（表已有写入口）：
   关闭了对应类型的用户不生成。
4. 自己触发的不通知自己；同一人同一实体 5 分钟内不重复。

**验收（可证伪）**

```bash
# A 把 issue 分配给 B
PATCH …/issues/<i>/ {"assignee_ids":["<B>"]}
# 以 B 登录
GET …/users/notifications/unread/     # → {"count": 1}（此前恒 0）
GET …/users/notifications/            # → 1 条 {"entity_name":"issue","triggered_by":"assigned"}

# 反向：注释掉 notify 注册 → 上面的断言必须 FAIL
```

**风险**：中高。通知是**扇出写**（一个动作 N 个接收者），必须限流 + 去重，
否则批量改 200 个工作项会给 200 个人各发 200 条。

---

### L-5 · 备份覆盖 `live/` 目录 ✅ 已验收（2026-10-24）

**现象**：`tests/backup.sh` 只备份 `data.db` + `storage/`，**没有 `live/`**。
页面正文除了 `description_binary` 之外还有一份在 `<data>/live/*.log` 里，
恢复后可能出现"页面回到了最后一次 PATCH 的状态"（最多丢 1.5s + settle 的编辑），
更糟的情况是日志与 `description_binary` 不一致。

**证据**：原 `tests/backup.sh:104-113` 只 copy `storage/`；`live/` 是 2026-10-23 新增的目录。

**实际实现**

| 文件 | 改动 |
|---|---|
| `tests/backup.sh` | 新增 `live/` 拷贝块；`MANIFEST.txt` 增加 `live_files=` 与 `live_sha256=`（整棵树的哈希，光看文件数发现不了被截断的日志）；`--tar` 模式下如实报告"已包含在归档里" |
| `tests/restore.sh` | 还原 `live/` 后重算树哈希并与 manifest 比对，不符即中止（`--force` 可越过）；备份里没有 `live/` 时明确提示而非静默跳过 |
| `tests/backup-drill.sh` | 种一棵真实格式的 live 日志（`[varuint len][bytes]` + `.name` sidecar）；破坏阶段连 `live/` 一起删；新增 2 条断言：备份必须逐字节捕获 `live/`、恢复后 `live/` 指纹必须一致 |
| `tests/pbdata.py` | 新增 `hashdir <dir>` 子命令（任意目录树的 sha256）；`seed` 接受可选第三参数 live 目录 |

**两个值得记下的判断**

1. **顺序**：先快照 `data.db`，后拷 `live/` —— 即"日志可能比库新"。这是安全的：
   中继回放幂等，多出来的 update 无害；反过来（库比日志新）则会让最后一次编辑
   消失在快照与日志之间。
2. **`live/` 可以直接 `cp`**，不像 `data.db` 必须走在线备份 API。日志是
   `[varuint 长度][字节]` 顺序追加，读取端跳过被截断的最后一条记录
   （`internal/live/store.go`），所以服务运行中拷贝最坏只丢最新一次编辑。

**验收（可证伪，全部实跑）**

| 验证项 | 结果 |
|---|---|
| 演练全绿 | ✅ `bash tests/backup-drill.sh` → **19 passed / 0 failed**（原 17 + 新增 2） |
| **反向验证** | ✅ 把 `live/` 拷贝改成 `elif false` 后重跑 → **17 passed / 2 failed**，失败的正是新增的两条（`backup has no live/` / `restored live/ differs`）；还原后逐字节一致 |
| 真实实例往返 | ✅ `.fe-run`（5 个 live 文件 / 38441 B）→ 备份 → 恢复到临时目录 → `dir_sha256=db91d147…` 两边完全相同 |
| manifest 自检 | ✅ 恢复输出含 `restored live/ (5 files)` + `live/ tree matches the manifest checksum` |
| 升级演练 | ✅ `bash tests/upgrade-drill.sh` 全绿（脚本改动按 `03-ITERATION-GUIDE.md` 要求同跑） |

**风险（已处理）**：`live/` 里的 `.bak`（compaction 前的旧日志）也一并备份，
体积最多各 1 MiB；保留策略本来就只留 N 份，可接受。

---

### L-6 · Cycle 分组视图（`group_by` / `sub_group_by`）

**现象**：周期内的工作项列表在"按状态分组"视图下退化成平铺。
**证据**：`internal/handlers/cycle/issue.go:18-20` 注释已声明未实现。

**目标**：`GET …/cycles/{id}/cycle-issues/?group_by=state_id&sub_group_by=priority`
返回上游的分组信封（`grouped_by` + `sub_grouped_by` + `issues`）。

**验收**：同一组数据，带 `group_by` 时返回结构含 `grouped_by`；不带时是平铺信封。
反向：把分组分支删掉 → 结构断言 FAIL。

**风险**：低，但**必须逐字段对齐上游** `CycleIssueViewSet.list` 的分组形状，不要自己发明。

---

### L-7 · 最近访问（`user_recent_visits`）

**现象**：侧边栏 / 命令面板的「最近访问」永远空。
**证据**：`internal/handlers/view/recent.go:34`、`view/view.go:647` 只读。

**实现要点**：在 `GET …/issues/{id}/`、`GET …/pages/{id}/` 成功返回后 upsert 一行
（`user, entity_name, entity_identifier, project, visited_at`）。
**用 upsert 而不是 insert**，否则每次打开都新增一行。

**验收**：打开 3 个工作项 → `GET …/recent-visits/` 返回 3 条且按 `visited_at` 倒序；
重复打开同一个 → 仍是 3 条（`visited_at` 更新）。

**风险**：低。写放大（每个详情页一次 upsert），可以只在 `visited_at` 距今 > 60s 时写。

---

### L-8 · 公共 API 文档页

**现象**：`/api/v1/` 的 Swagger/ReDoc 页只有一行占位文字。
**证据**：`internal/handlers/publicapi/publicapi.go:104-105` 两个 `placeholder` HTML。

**实现要点**：OpenAPI 文档（`/api/v1/schema/`）已经能生成，直接把
Swagger UI / ReDoc 的 CDN 资源引进去即可；**离线部署不能依赖 CDN**，
所以优先做"零依赖"版本：一个内嵌的 HTML 用 `redoc.standalone.js`（打包进二进制）
或干脆把 schema 渲染成静态 HTML 表格。

**验收**：浏览器打开 `/api/v1/` 能看到分组与端点列表（截图 + DOM 断言，不接受"200 就通过"）。

**风险**：低。注意 CSP / 安全头不要放开过头。

---

### L-9 · 页面 ↔ 工作项关联（`page_logs`）

**现象**：`page_logs` 只读，`page.linkedIssueIDs` 永远返回空。
**现状**：前端**没有**对应入口（`grep linkedIssue components/pages/` 为空），
所以这不是"坏了"，而是"没做，也没人用"。

**建议**：**先不做**。等前端出现入口（或用户明确要求页面里能嵌工作项）再补，
补的时候照 `PageLog` 的 Django 模型写 `entity_name/entity_identifier`。

---

### L-10 · 死表与文档一致性

**现状**：15 张业务表从未被 Go 引用（另 2 张 `sqlite_stat*` 是 SQLite 自己的统计表）。其中**真正没用**的：

| 表 | 判断 |
|---|---|
| `descriptions` / `issue_versions` / `issue_attachments` / `issue_blockers` / `project_deploy_boards` | 上游遗留表；本项目用别的表代替（`issue_relations` / `file_assets` / `deploy_boards`） |
| `teams` / `workspace_themes` / `issue_types` / `project_issue_types` | 上游功能，本项目**未纳入**，且前端不调用 |
| `accounts` / `api_activity_logs` / `email_notification_logs` | OAuth / API 审计 / 邮件日志（既定决策：邮件走文件） |

**建议**：不要删表（迁移里删表风险大于收益），只在 `docs/07` 里加一张
"已知未使用表"清单，避免下一个 agent 又把它们当成缺口去实现。

---

## 4. 根因：缺少"副作用层"

这是上面 4 个 P0 的共同原因，值得单独写一段。

全仓 **0 个 `OnRecord*` 钩子**（`grep -rn "OnRecord" --include=*.go .` 为空）。
所有跨表写入都是**在 handler 里手写**的：创建 issue 时顺手写 assignees、
创建评论时顺手写 reactions……于是**没有被手写到的派生数据就永远不会产生**：

```
issues 表被写 ──┬─→ issue_assignees        ✅ handler 里手写
                ├─→ issue_activities       ❌ 没人写   → 活动流/燃尽图/仪表盘空
                ├─→ notifications          ❌ 没人写   → 收件箱空
                ├─→ issue_mentions         ❌ 没人写   → @提及无索引
                └─→ issue_description_versions ❌ 没人写 → 版本历史空
```

**建议**：L-1/L-3/L-4 都落在同一个新包 `internal/hooks/` 里，
一次性把"记录变更 → 派生数据"这条通道建起来，而不是在 20 个 handler 里继续补。

**前提验证（做 L-1 之前必须先确认）**：

1. PocketBase 的 `OnRecordAfterUpdateSuccess` 对 `app.Save()` 的**内部调用**是否触发？
   （若触发，钩子里写 `issue_activities` 会递归 → 必须防重入）
2. 钩子里能不能拿到 `Original()`（旧值）？
3. 钩子抛错会不会让主保存失败？（应该**吞掉**派生数据的错误，不能因为写活动失败就存不了 issue）

这三条写进 `docs/06-PITFALLS.md`，作为后续所有钩子的前提。

---

## 5. 明确不做（附理由）

| 项 | 理由 |
|---|---|
| `epics` 别名路由 | 上游 Django/spec 没有 `epics` API；前端 `serviceType` 模板的不可达分支（`07` §3.1b） |
| Webhook / 导入 / 导出 / AI / 集成 | `spec/exclusions.md` 已剔除 |
| OAuth（`accounts` 表） | 未纳入本切片；如要加，先改 `spec/exclusions.md` |
| 邮件真实投递 | 既定决策：写日志文件（`email_notification_logs` 保持空） |
| 多实例横向扩展 | 本项目目标就是单二进制；跨进程总线不在范围内 |
| 性能压测 / 索引调优 | 约定不做（数据量级不构成瓶颈） |
| 删死表 | 迁移删表风险 > 收益，改为文档登记（L-10） |

---

## 6. 建议排期

依赖关系：

```
L-1（钩子通道）──┬─→ L-4（通知，复用同一钩子）
                  └─→ L-3（版本，复用同一钩子）
L-2（估算点）──→ 依赖 L-1 的 completion_chart 才有意义
L-5（备份）    ── 独立，随时可做，建议立刻做（数据安全）
L-6 / L-7 / L-8 ── 独立
```

**推荐顺序**

1. ~~**L-5**~~ ✅ **已完成（2026-10-24）** → 2. **L-1**（建钩子通道）→
3. **L-2**（小，立刻见效）→ 4. **L-3**（复用钩子）→ 5. **L-4**（最大，最后做）→
6. L-6 / L-7 / L-8 / L-10（按需）。

每个任务完成后：更新本文件的**状态列**，并在 `docs/10-REMAINING-WORK.md`
追加一节实施记录（格式照 §3.3–§3.5）。

---

## 7. 验收模板（每个任务都照这个来）

1. **静态**：`gofmt -l internal/` 空、`go build ./...`、`go vet ./internal/...` 全过
2. **单测**：新增用例覆盖新行为，且**做反向验证**（把实现改坏 → 用例必须 FAIL → 还原后 PASS）
3. **现场探针**：起真实服务，用 curl 走一遍验收清单里的命令（不接受单测代替）
4. **浏览器**：如果用户能看见（活动流 / 版本历史 / 收件箱 / 图表），
   必须用 `agent-browser` 打开页面截图 + 读 DOM，**"200 且没报错"不算通过**
5. **门禁**：`bash tests/release-check.sh --fast --full-single` → `0 failed`
6. **文档**：本文件状态列 + `10` 实施记录 + （若踩坑）`06` 新增条目

---

## 8. 与其它文档的关系

| 文档 | 关系 |
|---|---|
| `10-REMAINING-WORK.md` | 历史决策与实施记录（§3.1 live 调研、§3.3–§3.5 中继实施与运维）。**不再新增待办** |
| `07-STATE-AND-ROADMAP.md` §3 | 缺口分类（A/B/C），解释为什么某些 404 是**故意**的 |
| `09-SECURITY-AUDIT.md` §5 | 守卫层审计，已结清；新增端点时必须按该文的矩阵复核 |
| `05-TESTING.md` | 测试分层与门禁用法（`--full-single` 怎么跑） |
| `06-PITFALLS.md` | 踩坑对照表；做 L-1 前先读 §4 的三条前提 |
