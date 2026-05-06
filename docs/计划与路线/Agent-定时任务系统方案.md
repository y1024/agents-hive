# Agent 定时任务系统方案

> 日期:2026-05-05
> 状态:DRAFT,待评审 — 已经过 /plan-ceo-review + outside voice 一轮,详见 §16 决策清单
> 触发背景:仓库现有 `scheduled_pushes`(IM 渠道定时推送)+ `internal/master/cron.go`(进程内 ticker 调度)只完成 80% 基础设施,但 (1) 没有前端管理页 (2) target 仅限 IM 推送 (3) 仅支持固定间隔 interval_sec,不支持 cron 表达式 (4) 内存版调度,master 重启数据丢失。生产 `scheduled_pushes` 表 0 行 = 实际无人在用。本计划把"IM 推送定时"泛化为通用"用户级定时任务系统",前端可视化管理。
>
> **业务驱动场景**:_(D2=B 用户判定业务侧有明确场景,具体场景由业务侧补充。outside voice OV10 关切已记录。请在实施前回填,作为本期价值衡量的 anchor。)_

## 1. 现状评估(基于代码证据)

| 已有 | 缺失 |
|---|---|
| `scheduled_pushes` PG 表(id/name/platform/prompt/interval_sec/created_by/last_run_at/next_run_at/last_error/enabled)| 前端管理页 |
| `cron.go` 进程内 ticker 调度 | cron 表达式("每天 9 点") |
| `/api/v1/channels/push/schedules` POST/GET/DELETE 三接口 | `target_type` 字段(只能 IM 推送)|
| `auth.UserFrom(ctx)` 用户身份注入 | 用户隔离 query(创建有 created_by,但 list 没按 user 过滤)|
| `pg_advisory_xact_lock` 模式现成 | 多实例 leader election(重启数据丢失)|
| `scheduledPromptDispatcher` 钩子 | 时区处理(用户时区 vs UTC)|

**生产 0 行**说明此能力上线但无人用。直接原因是缺前端 UI 加上 target 太窄。

## 2. 设计目标

1. 统一概念:用户在 web 后台一个页面管理所有定时任务,不分 push / session 入口
2. **三种 target**:
   - `im_push` — 复用现有飞书/企微/钉钉推送链路
   - `session` — 在用户名下新建 web session 跑 prompt,结果可在 web 看到
   - `skill` — 触发某个 Skill(预留,本期不实现)
3. **用户隔离**:list/get/update/delete 都按 `created_by` 过滤,用户只能看见自己的任务
4. **cron 表达式**:支持标准 5 字段 cron + 时区(IANA)
5. **持久化**:重启后任务自动恢复(基于 PG 表 + bootstrap 时 reload)
6. **多实例安全**:advisory lock + `last_run_at` 版本号,确保任务不被重复触发
7. **前端 CRUD UI**:新建/编辑/删除/启停/查看历史(最近 N 次执行)

## 3. 不做什么

- 不引入 K8s CronJob / Argo Workflows 这种外部调度系统(本地 cron parser 够用)
- 不做"任务依赖图"(任务 A 完成后触发 B)— 本期单任务独立
- 不做并发控制("同一任务 N 个实例并行"的高级配置)— 本期单任务串行
- 不做用户级配额("用户最多建 N 个定时任务")— 简单上限 100 写死,follow-up 再做配额体系
- 不做权限委派(运营给某个用户代建任务)— 仅 created_by 可见可改
- 不做 Skill target 的实际触发逻辑(Skill 当前缺基础设施)— 字段预留
- **不重写 cron.go**:升级它,不替换

## 4. 数据模型

### 4.1 表结构演进:`scheduled_pushes` → `scheduled_tasks`

不新建并行表,**演进现有表 + 重命名**(一次 PG migration):

```sql
-- migration: 20260505_scheduled_tasks_evolve.sql
ALTER TABLE scheduled_pushes RENAME TO scheduled_tasks;

-- 新增 target 抽象
ALTER TABLE scheduled_tasks ADD COLUMN target_type TEXT NOT NULL DEFAULT 'im_push'
  CHECK (target_type IN ('im_push', 'session', 'skill'));

-- 老 platform 字段语义保留,但仅 target_type='im_push' 时使用
COMMENT ON COLUMN scheduled_tasks.platform IS '仅 target_type=im_push 时使用,im 渠道 (feishu/wechat/dingtalk)';

-- 新增 target 配置 JSON,按 target_type 解释:
--   im_push: {"platform":"feishu","conversation_id":"oc_xxx"}
--   session: {"session_id":"...","auto_create":true}
--   skill:   {"skill_name":"xxx","args":{...}}
ALTER TABLE scheduled_tasks ADD COLUMN target_config JSONB NOT NULL DEFAULT '{}'::jsonb;

-- cron 表达式(可选,与 interval_sec 互斥)
ALTER TABLE scheduled_tasks ADD COLUMN cron_expr TEXT NOT NULL DEFAULT '';
ALTER TABLE scheduled_tasks ADD COLUMN timezone TEXT NOT NULL DEFAULT 'UTC';

-- 互斥校验:cron_expr 和 interval_sec 不能同时设(一个为空)
ALTER TABLE scheduled_tasks ADD CONSTRAINT cron_or_interval_check
  CHECK ((cron_expr <> '' AND interval_sec = 0) OR (cron_expr = '' AND interval_sec > 0));

-- 历史执行记录表
CREATE TABLE IF NOT EXISTS scheduled_task_runs (
    id           TEXT NOT NULL,
    task_id      TEXT NOT NULL REFERENCES scheduled_tasks(id) ON DELETE CASCADE,
    started_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    finished_at  TIMESTAMPTZ,
    status       TEXT NOT NULL CHECK (status IN ('running','succeeded','failed','timeout')),
    output       TEXT NOT NULL DEFAULT '',
    error        TEXT NOT NULL DEFAULT '',
    session_id   TEXT,                  -- target_type='session' 时记录创建的 session
    PRIMARY KEY (id),
    INDEX (task_id, started_at DESC)    -- 按任务查最近执行
);

CREATE INDEX IF NOT EXISTS idx_scheduled_tasks_user ON scheduled_tasks(created_by, enabled);
```

### 4.2 老接口兼容

`/api/v1/channels/push/schedules` 三个接口**保留不动**作为 alias,内部委托给新 `/api/v1/scheduled-tasks` 实现 + 强制 `target_type='im_push'`。3 个月后下线 alias。

### 4.3 Go 类型

```go
type ScheduledTask struct {
    ID           string                 `json:"id"`
    Name         string                 `json:"name"`
    TargetType   string                 `json:"target_type"`         // im_push | session | skill
    TargetConfig map[string]any         `json:"target_config"`        // 按 target_type 解释
    Prompt       string                 `json:"prompt"`
    CronExpr     string                 `json:"cron_expr,omitempty"`  // "0 9 * * *" 形式;与 IntervalSec 互斥
    IntervalSec  int                    `json:"interval_sec,omitempty"`
    Timezone     string                 `json:"timezone"`             // "Asia/Shanghai"
    Enabled      bool                   `json:"enabled"`
    CreatedBy    string                 `json:"created_by"`           // 强制 = auth.UserFrom(ctx).ID
    LastRunAt    *time.Time             `json:"last_run_at,omitempty"`
    NextRunAt    *time.Time             `json:"next_run_at,omitempty"`
    LastError    string                 `json:"last_error,omitempty"`
    CreatedAt    time.Time              `json:"created_at"`
    UpdatedAt    time.Time              `json:"updated_at"`
}

type ScheduledTaskRun struct {
    ID         string     `json:"id"`
    TaskID     string     `json:"task_id"`
    StartedAt  time.Time  `json:"started_at"`
    FinishedAt *time.Time `json:"finished_at,omitempty"`
    Status     string     `json:"status"`     // running | succeeded | failed | timeout
    Output     string     `json:"output"`
    Error      string     `json:"error"`
    SessionID  string     `json:"session_id,omitempty"`
}
```

## 5. 后端架构

### 5.1 调度器升级(internal/master/cron.go)

现有 `cron.go` 仅支持 `Interval time.Duration`。升级:

```go
type ScheduleSpec struct {
    Interval time.Duration  // 与 CronExpr 互斥
    CronExpr string         // 5 字段 cron("M H DoM Mon DoW")
    Timezone string         // IANA "Asia/Shanghai"
}

type CronJob struct {
    ID           string
    Name         string
    Schedule     ScheduleSpec
    UserID       string                                  // 用户隔离
    Callback     func(context.Context) error
}
```

引入 `github.com/robfig/cron/v3` 作为 cron 表达式 parser(成熟、Go 生态首选,~100KB)。Interval 路径保留不动(向后兼容)。

### 5.2 多 target 分发(新增 dispatcher 注册表)

```go
type TargetDispatcher func(ctx context.Context, task ScheduledTask) (sessionID string, err error)

// 注册表:每个 target_type 一个实现
m.RegisterTargetDispatcher("im_push", imPushDispatcher)    // 调 push.SendChannelMessage
m.RegisterTargetDispatcher("session", sessionDispatcher)   // 调 sm.ProcessRequestWithResponse
m.RegisterTargetDispatcher("skill", nil)                    // 本期 nil,工具返回 "not implemented"
```

执行流:cron 触发 → 查 task.target_type → 走对应 dispatcher → 写 `scheduled_task_runs` 记录。

### 5.3 多实例 leader election(advisory lock)

bootstrap 时 reload 任务,但**不同实例不能同时跑同一任务**。每次 cron tick:

```go
func (m *Master) tryRunTask(ctx context.Context, taskID string) {
    err := m.db.BeginFunc(ctx, func(tx pgx.Tx) error {
        // 用 task_id hash 作 advisory lock key,事务级锁
        var locked bool
        tx.QueryRow(ctx, "SELECT pg_try_advisory_xact_lock(hashtext($1))", taskID).Scan(&locked)
        if !locked {
            return nil  // 别的实例正在跑这个 task,跳过
        }
        // 在锁保护下执行 + 更新 last_run_at
        return runTaskAndRecord(ctx, tx, taskID)
    })
}
```

锁随 transaction 释放,无悬空风险。复用现有 `internal/auth/pg_store.go:466` 已有的 advisory lock 模式。

### 5.4 持久化 reload(bootstrap)

`internal/bootstrap/server.go` 启动时:

```go
// 启动后从 PG 加载所有 enabled=true 的任务,注册到内存 cron
tasks, _ := store.ListEnabledScheduledTasks(ctx)
for _, t := range tasks {
    m.CronCreate(toCronJob(t))
}
```

任务 CRUD 时双写:PG + 内存 cron。删 task 同时停 cron job。

### 5.5 用户隔离 query

handlers 改造:

```go
func (s *Server) handleListScheduledTasks(w http.ResponseWriter, r *http.Request) {
    user := auth.UserFrom(r.Context())
    if user == nil { return 401 }
    tasks, err := s.store.ListScheduledTasksByUser(ctx, user.ID)  // ← 强制按 created_by 过滤
    ...
}

func (s *Server) handleGetScheduledTask(w http.ResponseWriter, r *http.Request) {
    user := auth.UserFrom(r.Context())
    task, err := s.store.GetScheduledTask(ctx, taskID)
    if task.CreatedBy != user.ID { return 404 }   // ← 不漏存在性
    ...
}
```

更新/删除同理。**Admin role 例外**(可看所有用户任务)走 `/api/v1/admin/scheduled-tasks/*`。

### 5.6 后端 API endpoints(新)

| Method | Path | 说明 |
|---|---|---|
| POST | `/api/v1/scheduled-tasks` | 新建任务,强制 created_by=auth user |
| GET | `/api/v1/scheduled-tasks` | 列出当前用户任务 |
| GET | `/api/v1/scheduled-tasks/{id}` | 详情(校验 ownership)|
| PUT | `/api/v1/scheduled-tasks/{id}` | 修改(校验 ownership)|
| DELETE | `/api/v1/scheduled-tasks/{id}` | 删除(校验 ownership)|
| POST | `/api/v1/scheduled-tasks/{id}/toggle` | 启用/禁用切换 |
| POST | `/api/v1/scheduled-tasks/{id}/run-now` | 手动触发一次(调试用)|
| GET | `/api/v1/scheduled-tasks/{id}/runs` | 最近 N 次执行历史(默认 N=20)|
| GET | `/api/v1/admin/scheduled-tasks` | 管理员列出全部任务 |

老 `/api/v1/channels/push/schedules` 三接口保留 alias 3 个月。

## 6. 前端 UI

### 6.1 路由

新增 `frontend/src/pages/ScheduledTasks.tsx`,路由 `/scheduled-tasks`(放在 Admin 侧边栏 + 普通用户侧边栏)。

### 6.2 页面结构

```
┌─────────────────────────────────────────────────────────────────┐
│ 定时任务  [+ 新建任务]                              [搜索框]      │
├─────────────────────────────────────────────────────────────────┤
│ ☑ 启用 │ 名称       │ 调度       │ 目标          │ 上次执行  │ 操作│
│ ☑      │ 早会播报   │ 每天 9:00  │ IM 飞书 #研发 │ 2 小时前 │ ⋮  │
│ ☐      │ 周报分析   │ 每周一 8:00│ Web Session  │ 6 天前   │ ⋮  │
│ ☑      │ Lint 巡检 │ 每 2 小时  │ Web Session  │ 23 分钟前│ ⋮  │
└─────────────────────────────────────────────────────────────────┘
            点击任意行展开 → 最近 5 次执行历史 + Output preview
            操作菜单:编辑 / 立即运行 / 删除 / 复制
```

### 6.3 新建/编辑表单

```
名称 *           [______________]
描述              [______________]

调度方式 *       (●) 简单间隔  ( ) Cron 表达式
  ┌ 简单间隔 ─────────────┐    ┌ Cron 表达式 ──────────────┐
  │ 每 [_30_] 分钟 ▼      │    │ [0 9 * * *      ]        │
  │ (秒/分钟/小时/天)     │    │ 例:每天 9:00,周一 8:30   │
  └────────────────────────┘    │ 时区 [Asia/Shanghai ▼]  │
                                 └────────────────────────┘

目标 *           ( ) IM 推送  (●) Web Session  ( ) Skill (即将上线)

  ┌ Web Session ─────────────┐   ┌ IM 推送 ────────────────┐
  │ 会话名称 [______________] │   │ 平台 [飞书 ▼]            │
  │ ☑ 自动创建新会话           │   │ 群/会话 ID [_______]    │
  └────────────────────────────┘   └──────────────────────────┘

Prompt *          [大文本框,800 char 上限]

☑ 启用            [取消]  [保存]
```

### 6.4 五态 UI

| 状态 | 渲染 |
|---|---|
| loading | 表格骨架屏 3 行 + animate-pulse |
| empty | 大图标 + "还没有定时任务,点击「新建任务」开始" + 按钮 |
| success | 表格列表 |
| error | 顶部红条 "加载失败 [重试]" + 缓存内容灰度 |
| partial | 顶部黄条 "调度服务异常,任务可能未按时执行" |

### 6.5 复用现有组件

- 表格走 `frontend/src/components/ui/Table.tsx`(已有)
- 表单走 react-hook-form(仓库已用)
- cron 表达式解释器:`cronstrue` npm 包(把 `0 9 * * *` 翻译成中文"每天 9:00")
- 时区下拉:`Intl.supportedValuesOf('timeZone')`(浏览器 native)

### 6.6 i18n

新增 keys 进 `frontend/src/i18n/locales/{en,zh}.json`:
- `scheduledTasks.title`、`.create`、`.cronExpr`、`.timezone`、`.targetIm`、`.targetSession` 等约 30 条

## 7. 关键产品决策(需评审拍板)

下面是必须在实施前定的事:

**Q1 — cron 表达式 vs 简单间隔 vs 都支持**:推荐都支持,简单间隔保留兼容老 push;cron 表达式适合"每天 9 点"。

**Q2 — 时区:用户级 vs 任务级 vs 服务器 UTC**:推荐**任务级**(每个任务带 timezone),最灵活;UI 默认填浏览器时区。

**Q3 — 失败重试**:推荐"自动重试 3 次,指数退避(1min/5min/15min)";超过失败次数 enabled 自动关闭并发邮件/IM 通知。

**Q4 — 单任务并发**:推荐"严格串行"(同一 task 上次没跑完就不开下次),advisory lock 自然保证。

**Q5 — `target_type=session` 时 session 行为**:推荐"每次执行新建 sub-session"(避免跨次污染上下文),session_id 命名为 `scheduled-{task_id}-{run_id}`。

**Q6 — 用户最多建多少任务**:推荐"上限 100 写死,超出报 429"。配额体系作 follow-up。

**Q7 — 历史执行保留多久**:推荐"30 天滚动删除",配 cron 任务 daily 清理。

## 8. 实施阶段

### Phase 1 — 数据模型迁移

**Files:**
- Modify: `internal/store/postgres_migrate.go`(新增 migration)
- Create: `internal/store/scheduled_task_store.go`(增删改查 + 用户隔离)
- Create: `internal/store/scheduled_task_store_test.go`

**Tasks:**
- [ ] PG migration:`scheduled_pushes` → `scheduled_tasks` + 5 个新列 + 1 个新表
- [ ] migration 兼容性测试:老数据自动 `target_type='im_push'`、`platform` 字段保留
- [ ] Store 接口:`Save/Get/List/ListByUser/Update/Delete/ListEnabled` + run history `RecordRun/ListRuns`
- [ ] 测试:CRUD 路径 + ownership 校验 + cron_or_interval CHECK 约束

### Phase 2 — cron 调度器升级

**Files:**
- Modify: `internal/master/cron.go`(加 ScheduleSpec / cron_expr 解析)
- Modify: `internal/master/cron_test.go`
- Add dependency: `github.com/robfig/cron/v3`

**Tasks:**
- [ ] `ScheduleSpec` + `parseSchedule()` 函数(interval 或 cron_expr 二选一)
- [ ] `runCronJob` 改用 `cron.Schedule.Next()` 计算下次触发
- [ ] 时区:`time.LoadLocation(spec.Timezone)` 加载;非法时区报错
- [ ] 多实例 advisory lock(`pg_try_advisory_xact_lock`)包 callback
- [ ] 失败重试逻辑(3 次指数退避 + enabled=false 兜底)
- [ ] 测试:cron 表达式解析 / interval 兼容 / 时区切换 / advisory lock 互斥

### Phase 3 — 多 target dispatcher

**Files:**
- Create: `internal/master/dispatcher_im_push.go`(复用现有 push 链路)
- Create: `internal/master/dispatcher_session.go`(调 sm.ProcessRequestWithResponse)
- Modify: `internal/master/master.go`(注册 dispatcher map)

**Tasks:**
- [ ] dispatcher 接口 + 注册表(im_push / session / skill)
- [ ] `im_push` 实现:从 `target_config` 读 platform/conversation_id,调 push.SendChannelMessage
- [ ] `session` 实现:`session_id = scheduled-{task_id}-{run_id}`,调 sessionManager 创建 + 投递 prompt
- [ ] `skill` 占位 dispatcher 返回 `ErrNotImplemented`
- [ ] 每次执行写 `scheduled_task_runs` 记录(running → succeeded/failed)
- [ ] 测试:每个 dispatcher 独立单测 + 端到端集成测

### Phase 4 — REST API

**Files:**
- Create: `internal/api/scheduled_tasks_handler.go`
- Create: `internal/api/scheduled_tasks_handler_test.go`
- Modify: `internal/api/routes.go`
- Modify: `internal/api/push_schedule_handler.go`(改成委托新 handler)

**Tasks:**
- [ ] 7 个新 endpoints(POST/GET/GET-id/PUT/DELETE/toggle/run-now)
- [ ] `/runs` 历史接口(分页,默认 20 条)
- [ ] 用户隔离强制 — list 走 ListByUser,get/update/delete 校验 ownership
- [ ] 老 `/channels/push/schedules` alias 委托新 handler + 强制 target_type='im_push'
- [ ] Admin endpoint `/api/v1/admin/scheduled-tasks` 走 admin role check
- [ ] 输入校验:cron_expr 用 robfig/cron 预解析、timezone IANA 校验、prompt 长度 ≤ 8000
- [ ] 测试:每个 endpoint + ownership 跨用户隔离 + 校验失败路径

### Phase 5 — bootstrap reload + 历史清理

**Files:**
- Modify: `internal/bootstrap/server.go`
- Create: `internal/master/scheduled_task_history_gc.go`

**Tasks:**
- [ ] 启动时 ListEnabledScheduledTasks → 注册所有任务进内存 cron
- [ ] 启动失败任务(cron_expr 解析失败)记录到 `last_error` + enabled=false
- [ ] 历史清理 cron(每天 03:00 删除 `scheduled_task_runs.started_at < now - 30d`)
- [ ] 测试:进程重启后任务自动恢复 + 老任务字段兼容

### Phase 6 — 前端 UI

**Files:**
- Create: `frontend/src/pages/ScheduledTasks.tsx`
- Create: `frontend/src/components/scheduled-tasks/TaskList.tsx`
- Create: `frontend/src/components/scheduled-tasks/TaskForm.tsx`
- Create: `frontend/src/components/scheduled-tasks/RunHistoryDrawer.tsx`
- Create: `frontend/src/store/scheduledTasks.ts`
- Modify: `frontend/src/api/node-client.ts`(7 个新 API client)
- Modify: `frontend/src/layouts/AdminSidebar.tsx`(加菜单)
- Modify: `frontend/src/i18n/locales/{en,zh}.json`(~30 keys)
- Add dependency: `cronstrue`

**Tasks:**
- [ ] TaskList 表格(列名/调度/目标/上次执行/操作)+ 搜索/筛选
- [ ] TaskForm 新建/编辑(简单间隔 vs cron_expr 切换 + cronstrue 实时翻译 + 时区下拉)
- [ ] RunHistoryDrawer(展开任意行显示最近 5 次,链接到完整历史页)
- [ ] 五态 UI(loading 骨架/empty 空提示/error 重试条/partial WS 断条)
- [ ] toggle/run-now 按钮 + Toast 反馈
- [ ] 用户视角与 Admin 视角 UI 复用,通过 prop `mode` 切换
- [ ] e2e 测:create → list → run-now → check history → delete 全链路

## 9. 测试计划

### 9.1 后端测试矩阵

```
NEW CODEPATHS:
  ├── cron.go::ScheduleSpec.Next()         单测 cron + interval + timezone
  ├── cron.go::tryRunTask + advisory lock  单测两实例并发 + 锁互斥
  ├── dispatcher_im_push.Send                单测复用 push 链路
  ├── dispatcher_session.Send                单测 session 创建 + prompt 投递
  ├── handler_create + ownership check       单测跨用户访问拒绝
  ├── handler_list_by_user                   单测仅返回 created_by=user.ID
  ├── handler_run_now                        单测手动触发 + run 记录
  └── bootstrap_reload                       单测启动后任务恢复

REGRESSION 关键:
  ├── 老 /channels/push/schedules 三接口语义不变(转发新 handler)
  ├── 老 scheduled_pushes 数据自动 target_type='im_push'
  └── interval_sec 路径继续工作(cron_or_interval CHECK 兼容)
```

### 9.2 前端测试

- TaskForm:cron_expr 与 interval_sec 切换 + cronstrue 翻译正确
- TaskList:用户隔离仅看自己任务 + admin 视角看全部
- e2e:用 playwright 跑 create → list → toggle → run-now → history → delete

### 9.3 测试命令

```bash
env GOCACHE=/tmp/go-build go test ./internal/store ./internal/master ./internal/api -run "Scheduled" -count=1
cd frontend && npm test -- --run src/pages/ScheduledTasks
```

## 10. 验收矩阵

| 场景 | 期望 |
|---|---|
| 用户 A 建 1 个任务 | 用户 B list 看不到,admin list 能看到 |
| 用户 B 拿到 A 的 task_id 调 GET/PUT/DELETE | 返回 404,不漏存在性 |
| 任务 cron_expr `0 9 * * *` Asia/Shanghai | 每天北京时间 9:00 触发 |
| 任务 interval_sec=300 | 每 5 分钟触发(老路径兼容)|
| 两 master 实例同时跑同一任务 | advisory lock 保证只一个跑 |
| target=im_push | 复用 push 链路,飞书群收到消息 |
| target=session | 用户登录 web 看到新 session 名 `scheduled-{id}-{run}`,prompt 已执行 |
| 任务 cron_expr 非法 | API 创建返回 400,错误体包含解析错误 |
| 任务连续失败 3 次 | enabled 自动关,通知 created_by |
| master 重启 | enabled=true 任务自动恢复调度,不丢任务 |
| 30 天前的 run 记录 | history GC 自动清理 |
| 老 /channels/push/schedules 调用 | 透传到新 handler,强制 target_type='im_push' |

## 11. 灰度与兼容

- **数据**:migration 完成后老 `scheduled_pushes.platform` 字段保留,通过 `target_type='im_push'` 区分;target_config 默认 `{}`,im_push 自动从 platform/conversation_id 字段重建
- **API**:老接口保留 alias 3 个月,带 `Deprecation: true` HTTP 头
- **前端**:管理页 lazy-loaded,不影响主流程;feature flag `agent.scheduled_tasks.enabled=true` 默认开
- **回退**:如出现严重问题,关 feature flag → 前端菜单隐藏,但已注册的 cron job 仍跑(不破坏现有 IM 推送)

## 12. 风险与 follow-up

| 风险 | 处理 |
|---|---|
| 用户建 100 个 cron 任务挤占 master 资源 | 上限 100 + advisory lock 串行 + 单任务超时 30 分钟 |
| cron_expr 解析失败但任务仍 enabled | 启动 reload 时校验失败 → enabled=false + 写 last_error |
| `target_type=session` 时新 session 占 db 空间 | session 加 ttl + GC,跟 sessiontodo Wave 1 内存 GC 同模式 |
| 用户 prompt 含敏感数据持久化到 PG | 在表上加 `prompt_redacted bool` 标记,follow-up 加密存储 |
| 时区夏令时跨日处理 | robfig/cron/v3 内置正确处理 DST,不需要手撸 |

**follow-up 不进本期**:

- T1:用户级配额(每天最多 N 次执行 / 每月 token 上限)
- T2:任务依赖图(A → B 串行触发)
- T3:Skill target dispatcher 实现(等 Skills 系统补齐)
- T4:Webhook 出站 target(任务执行完发 callback)
- T5:任务模板市场(分享给团队复用)
- T6:per-user timezone 设置(用户 profile 默认时区,个体任务可覆盖)
- T7:prompt 加密 + 审计日志

## 13. 估算

| Phase | 人/团队 | CC+gstack |
|---|---|---|
| Phase 1 (数据)| 1.5 天 | 30 分钟 |
| Phase 2 (cron 升级)| 2 天 | 1 小时 |
| Phase 3 (dispatcher)| 1.5 天 | 30 分钟 |
| Phase 4 (REST API)| 2 天 | 45 分钟 |
| Phase 5 (bootstrap+GC)| 1 天 | 20 分钟 |
| Phase 6 (前端)| 4 天 | 1.5 小时 |
| **总计** | **~12 天** | **~5 小时** |

## 14. 最小可交付切片

如果需要更小起步范围,Phase 1+2+4+6 是最小切片(数据+调度+API+前端),Phase 3 dispatcher 只实现 `im_push`(就是把 push schedule 加 UI),Phase 5 bootstrap reload 推后。这样**~3 天完成**(CC+gstack ~1.5 小时)。但故事 C 的"target=session"能力会缺失。

不推荐做这个切片——故事 C 的核心价值在 `target=session`(让 agent 定时帮用户跑任务),没这条 = 退化成"故事 A:仅 IM 推送泛化",失去最大产品价值。

---

## 15. Claude Code 审核重点

请重点审核:

1. **scheduled_pushes → scheduled_tasks 迁移**是否会破坏老 IM 推送数据?ALTER + RENAME 是否需要 downtime?
2. **多实例 advisory lock** 用 `pg_try_advisory_xact_lock(hashtext(task_id))` 是否会与其他模块的锁冲突?(hashtext 可能重叠)
3. **target=session 时新 session 的 user 上下文怎么注入?** session 创建用的 ctx 没有 HTTP request 的 auth.UserFrom 信息,需手动注入 user_id
4. **bootstrap reload 失败任务多 = 启动慢**?如果 100 个任务有 50 个 cron_expr 非法,启动时一个个解析失败要写 50 次 PG;能否批量更新
5. **前端 cron 表达式输入** UX 是否够友好?用户能不能正确写出 "每周一 8:00"?需不需要做 visual cron builder
6. **失败重试 3 次后 enabled=false** 是否合理?会不会出现"运维半夜被动收到一堆任务自动关闭通知"
7. **历史 GC 30 天**是否合理?长任务可能 30 天内每天跑 → 30 条记录,但 IM 推送可能每分钟跑一次 → 43200 条 / 月,要不要按任务类型不同 ttl

---

> 整体判断:**80% 基础设施已存在,本期是泛化 + 补 UI + 补持久化**,不是从零做新功能。比看起来工程量小很多。

---

## 16. CEO Review + Outside Voice 决策清单(2026-05-05)

本节记录 `/plan-ceo-review` + outside voice 一轮后的决议。**plan 主体 §3-§13 按本节修订实施**(实施工程师对照本节,不重写主体)。

### 16.1 已锁定的产品决策(D1=A 7 项一拨拍)

| ID | 决策 | 主体落点 |
|---|---|---|
| Q1 | cron 表达式 + interval_sec 都支持(互斥)| §4.1 已写 cron_or_interval CHECK |
| Q2 | 时区任务级,IANA 字符串 | §4.1 timezone 列已加 |
| Q3 | ~~3 次指数退避~~ → **被 OV7 替换** | 见 16.2-OV7 |
| Q4 | 严格串行(单任务) | §5.3 advisory lock 自然保证 |
| Q5 | ~~每次新 sub-session~~ → **被 OV3 替换** | 见 16.2-OV3 |
| Q6 | ~~100 写死~~ → **被 OV5 强化** | 见 16.2-OV5 |
| Q7 | ~~30 天 DELETE GC~~ → **被 OV6 替换** | 见 16.2-OV6 |

### 16.2 Outside Voice 9 项工程修复全接受(D3=A)

#### OV1 ALTER + RENAME **不是零停机** → 改两阶段 migration

**问题**:`ALTER TABLE scheduled_pushes RENAME TO scheduled_tasks` 持 ACCESS EXCLUSIVE 瞬时锁,但滚动部署期间老 master replica 仍跑 `SELECT FROM scheduled_pushes`,RENAME 完老进程 500。

**修订**:§4.1 migration 改两阶段(跨 2 个发布版本):

**阶段一 v_N**(纯加列,不改名):
```sql
ALTER TABLE scheduled_pushes ADD COLUMN target_type TEXT NOT NULL DEFAULT 'im_push'
  CHECK (target_type IN ('im_push', 'session'));      -- skill 不加(OV8 cut)
ALTER TABLE scheduled_pushes ADD COLUMN target_config JSONB NOT NULL DEFAULT '{}'::jsonb;
ALTER TABLE scheduled_pushes ADD COLUMN cron_expr TEXT NOT NULL DEFAULT '';
ALTER TABLE scheduled_pushes ADD COLUMN timezone TEXT NOT NULL DEFAULT 'UTC';
ALTER TABLE scheduled_pushes ADD CONSTRAINT cron_or_interval_check ...;
-- 老进程仍读 platform/prompt/interval_sec 字段,新列默认值不影响
```

**阶段二 v_N+1**(rename + view alias):
```sql
ALTER TABLE scheduled_pushes RENAME TO scheduled_tasks;
CREATE OR REPLACE VIEW scheduled_pushes AS SELECT * FROM scheduled_tasks;
-- 老进程通过 view 仍能读写;新代码用 scheduled_tasks。等 v_N+2 删 view
```

**新增**:`scheduled_task_runs` 表见 16.2-OV6(分区表替代普通表)。

#### OV2 advisory lock namespace 防碰撞 → 用 2-key 形式

**问题**:`hashtext(task_id)` 32-bit 空间,与 `hashtext('auth_providers_delete')` 等其他模块的 advisory lock 物理不可分。

**修订**:§5.3 lock 改:
```go
const lockNamespaceScheduledTasks = 0x5C8E  // 16-bit 自定 namespace,与 auth/sessiontodo 等隔离
tx.QueryRow(ctx, `SELECT pg_try_advisory_xact_lock($1, hashtext($2))`,
    lockNamespaceScheduledTasks, taskID).Scan(&locked)
```

仓内其他模块使用 advisory lock 时也应分配自己的 namespace 整数前缀,文档化在 `docs/架构设计/`。

#### OV3 session dispatcher 复用 Plan Runtime,不自建生命周期

**问题**:Plan Runtime 刚上线有完整 long-running task lifecycle(`session_loop` + Plan Runtime Guard + `runtime_epoch`)。我自建 `scheduled-{task_id}-{run_id}` session 命名 + 自己 GC,**两套生命周期不对齐 = 孤儿 session、统计重复**。

**修订**:`internal/master/scheduled_task_dispatch.go` 单文件 + switch(2 分支),不抽 dispatcher 注册表(OV8 + ER-A1=B 决议):

```go
// dispatchScheduledTask 在 cron callback 内调,2 分支 ~30 行
func (m *Master) dispatchScheduledTask(ctx context.Context, task ScheduledTask, runID string) (string, error) {
    // user 上下文注入 — auth.WithUser 接受整个 *auth.User 对象,不是字符串
    user, err := m.authStore.GetUser(ctx, task.CreatedBy)
    if err != nil {
        return "", fmt.Errorf("scheduled task %s: owner %s not found: %w", task.ID, task.CreatedBy, err)
    }
    ctx = auth.WithUser(ctx, user)

    switch task.TargetType {
    case "im_push":
        // 复用现有 push 链路
        platform, _ := task.TargetConfig["platform"].(string)
        convID, _ := task.TargetConfig["conversation_id"].(string)
        return "", push.SendChannelMessage(ctx, platform, convID, task.Prompt)

    case "session":
        // 触发 web session — 自动复用 Plan Runtime sessiontodo Wave GC、runtime_epoch、paused/resume
        sessionID := fmt.Sprintf("scheduled-%s-%s", task.ID, runID)
        _, err := m.sessionMgr.ProcessRequestWithResponse(ctx, SessionRequest{
            SessionID: sessionID,
            Input:     task.Prompt,
        })
        return sessionID, err

    default:
        return "", fmt.Errorf("unsupported target_type: %s", task.TargetType)
    }
}
```

**关键修正**(eng review 验证后):
- `auth.WithUser(ctx, *auth.User)`,**不是** `auth.WithUserID(ctx, string)`(`internal/auth/middleware.go:125` 真函数)
- `SessionRequest` 不含 `OwnerUserID` 字段,user 通过 ctx 注入(sessionManager 内部读)
- user 不存在时 fail-fast 并写 `last_error`,不 fallback admin 身份
- session 自动走 Plan Runtime sessiontodo Wave 1 GC、runtime_epoch、paused/resume,不自建生命周期

**收益不变**(对齐原 OV3 意图):
- session 自动支持 paused/resume(Plan Runtime Guard)
- statistics/observability 复用 sessiontodo trace span
- 不重新发明 GC 链路

**收益**:
- session 自动走 Plan Runtime sessiontodo Wave 1 GC
- session 自动支持 paused/resume(用户在 web 看到这个 session 卡了可以手动续)
- 统计/observability 复用 Plan Runtime 的 trace span,不重复造

#### OV4 bootstrap reload 异步 + per-task recover

**问题**:1000 任务串行解析、单条失败写一次 PG `last_error`,启动 50s 卡死;一条 cron_expr panic(robfig 不会 panic 但 target_config JSON unmarshal 可能)整个 server 起不来。

**修订**:§5.4 bootstrap reload 改:

```go
func (m *Master) startScheduledTasksAsync(ctx context.Context) {
    go func() {
        tasks, err := store.ListEnabledScheduledTasks(ctx)
        if err != nil {
            m.logger.Error("scheduled tasks reload failed", zap.Error(err))
            return  // 健康检查不依赖此函数
        }
        // 批量校验 cron_expr,单条失败用 defer recover() 隔离,不影响其他任务
        var failures []scheduledTaskFailure
        for _, t := range tasks {
            func() {
                defer func() {
                    if r := recover(); r != nil {
                        failures = append(failures, scheduledTaskFailure{ID: t.ID, Err: fmt.Errorf("panic: %v", r)})
                    }
                }()
                if err := m.CronCreate(toCronJob(t)); err != nil {
                    failures = append(failures, scheduledTaskFailure{ID: t.ID, Err: err})
                }
            }()
        }
        // 一次性批量 UPDATE last_error + enabled=false,而非逐条
        if len(failures) > 0 {
            store.BulkMarkScheduledTaskFailures(ctx, failures)
        }
    }()
}
```

健康检查 `/health` **不依赖** scheduled tasks reload 完成 — server 立即可服务,scheduled tasks 后台并发就绪。

#### OV5 用户任务上限明确 = per-user + 429

**问题**:plan 写"上限 100 写死"但没说 per-user 还是全局。Admin 算不算?

**修订**:§7 Q6 修正:
- **per-user 100**(普通用户,通过 `created_by`)
- Admin role 不受限(运维系统级任务)
- 超限返回 `429 Too Many Requests`,错误体:
  ```json
  {
    "error": "scheduled_tasks_quota_exceeded",
    "code": 429,
    "limit": 100,
    "current": 100,
    "message": "已达到每用户 100 个定时任务上限,请删除旧任务或联系管理员"
  }
  ```
- 前端 UI 收 429 时显示 toast + 引导用户去删除页

#### OV6 history GC 改分区表 + DROP PARTITION

**问题**:`scheduled_task_runs` 高频任务月产 43200 行/任务,btree 索引 + 大量 DELETE → dead tuple → VACUUM 跟不上。

**修订**:§4.1 表结构改:
```sql
CREATE TABLE scheduled_task_runs (
    id           TEXT NOT NULL,
    task_id      TEXT NOT NULL,
    started_at   TIMESTAMPTZ NOT NULL,
    finished_at  TIMESTAMPTZ,
    status       TEXT NOT NULL,
    output       TEXT NOT NULL DEFAULT '',
    error        TEXT NOT NULL DEFAULT '',
    session_id   TEXT,
    PRIMARY KEY (started_at, id)            -- 分区键必须在 PK 里
) PARTITION BY RANGE (started_at);

-- 每周一个分区,bootstrap 时按需 CREATE PARTITION
CREATE TABLE scheduled_task_runs_2026_w19 PARTITION OF scheduled_task_runs
    FOR VALUES FROM ('2026-05-04') TO ('2026-05-11');

-- 索引:每分区独立 (task_id, started_at DESC),BRIN(started_at) 全表汇总
CREATE INDEX ON scheduled_task_runs_2026_w19 (task_id, started_at DESC);
```

**GC**:不 DELETE,直接 `DROP TABLE scheduled_task_runs_<old_week>`(瞬时 + 0 dead tuple)。新增 `internal/master/scheduled_task_history_gc.go`:

```go
// 每周一 03:00 创建下周分区 + DROP 4 周前分区
func (m *Master) rotatePartitions(ctx context.Context) {
    nextWeek := time.Now().Add(7 * 24 * time.Hour)
    db.Exec(ctx, fmt.Sprintf(`CREATE TABLE IF NOT EXISTS scheduled_task_runs_%s PARTITION OF ...`, weekKey(nextWeek)))
    oldWeek := time.Now().Add(-28 * 24 * time.Hour)
    db.Exec(ctx, fmt.Sprintf(`DROP TABLE IF EXISTS scheduled_task_runs_%s`, weekKey(oldWeek)))
}
```

保留 4 周历史(28 天),更长由 partition rotate 控制。

#### OV7 重试与调度解耦

**问题**:plan §7 Q3 写"3 次指数退避(1/5/15min)",但 cron 任务 `0 9 * * *` 每天一次,9:00 失败 → 9:01/9:06/9:21 重试 → 与下一日 cron 错位 + 互相覆盖 last_error。

**修订**:§5.1 增加"调度 vs 执行"两轴:

```
调度轴(cron tick / interval):决定 何时 应该跑
   └─ 触发执行轴(retry policy):决定 失败时怎么重试 / 多久放弃
```

每次调度 tick 创建一条新的 `scheduled_task_runs.status='running'` 记录。该 run 内部最多重试 3 次(配置可调,默认 3),超过则 status='failed' + 写 `last_error`。**下次调度 tick 是新 run,与上次 run 完全独立**,不存在跨 tick 重试。

`task.last_error` 字段保留"最近一次失败原因"供 UI 显示,但不再驱动调度逻辑。"连续失败 N 次 → enabled=false" 的判定改为:**最近 5 次 run 全 failed 才自动关**(由 GC cron 顺手判,而不是 dispatcher 内联判)。

#### OV8 cut over-engineering — 本期不做 skill / prompt_redacted / Webhook / 模板市场

**修订**:§3 不做什么 + §4.1 schema 调整:

- **删除** `target_type='skill'` 选项(CHECK 约束去掉)
- **删除** `prompt_redacted` 字段
- **删除** dispatcher 注册表抽象 — 2 个实现直接在 cron callback 里 `switch task.TargetType`,~20 行
- §12 follow-up 中的 T3/T4/T5/T7 全部移除:**T3 Skill target / T4 Webhook 出站 / T5 模板市场 / T7 prompt 加密** 不再列为本期 follow-up。如果未来真有需求再开新 plan。

YAGNI 减重:估算从 12 天 → ~9 天(CC ~4 小时)。

#### OV9 Phase 5 并入 Phase 2

**问题**:Phase 5(bootstrap reload)与 Phase 2(cron 升级)是同一个事的两面 — 没有 reload 没法测多实例 advisory lock。

**修订**:§8 Phase 顺序变为:

```
Phase 1: 数据迁移(两阶段 migration)
Phase 2: cron 升级 + bootstrap reload(原 Phase 2 + 5 合并)
Phase 3: 多 target dispatcher(im_push + session,不要 skill)
Phase 4: REST API
Phase 5(原 Phase 6): 前端 UI
Phase 6(原 GC):分区 GC + 历史清理 cron
```

### 16.3 估算修订

| Phase | 人/团队 | CC+gstack |
|---|---|---|
| Phase 1 数据(两阶段 migration)| 1.5 天 | 30 分 |
| Phase 2 cron + bootstrap reload + advisory lock 修复 | 2.5 天 | 1 小时 |
| Phase 3 dispatcher(im_push + session,直接 switch)| 1 天 | 20 分 |
| Phase 4 REST API + 用户隔离 | 2 天 | 45 分 |
| Phase 5 前端 UI | 4 天 | 1.5 小时 |
| Phase 6 分区 GC | 0.5 天 | 15 分 |
| **总计** | **~11.5 天 → 实际 ~9 天**(并行 + cut)| **~4 小时** |

### 16.4 必修项 checklist(给实施工程师)

实施前对照本表,plan 主体读到对应章节时同时读本节修订:

- [ ] OV1 阶段一 migration 只加列 + 不 RENAME(本期发布)
- [ ] OV1 阶段二 migration 在 v_N+1 发布,带 view alias
- [ ] OV2 advisory lock 加 namespace `0x5C8E` + 2-key 形式
- [ ] OV3 dispatcher_session 改为调 `sessionMgr.CreateScheduledSession()` 复用 Plan Runtime,不自建 sub-session
- [ ] OV4 bootstrap reload 异步 + per-task `defer recover` + 批量 BulkMarkScheduledTaskFailures
- [ ] OV4 `/health` 不依赖 reload 完成
- [ ] OV5 `created_by` 用户级 100 上限,Admin 例外,429 错误体格式按 §16.2-OV5 标准化
- [ ] OV6 `scheduled_task_runs` 用 PARTITION BY RANGE(started_at) 分区表
- [ ] OV6 GC cron 走 DROP PARTITION,保留 4 周
- [ ] OV7 重试与调度解耦,run 级别重试,跨 tick 不串
- [ ] OV7 enabled 自动关判定改为"最近 5 次 run 全 failed",不在 dispatcher 内联判
- [ ] OV8 schema 不加 `target_type='skill'`、不加 `prompt_redacted`
- [ ] OV8 dispatcher 直接 switch,不抽注册表
- [ ] OV9 Phase 5(bootstrap)合并到 Phase 2

### 16.5 业务场景占位(D2=B 用户判定但未补)

> 本期 plan 价值 anchor 由业务侧提供。Outside voice OV10 关切是合理的:`scheduled_pushes` 历史表 0 行用,本期 plan 上线后避免重蹈需要 1-2 个验证过的真实场景。
>
> 实施工程师在 PR 描述中补充:"本 PR 解决场景 X(描述)+ 场景 Y(描述),用户故事:____。"

---


---

## 17. Eng Review 决策清单(2026-05-05 /plan-eng-review)

本节由 `/plan-eng-review` 在 2026-05-05 追加,验证 §16 决议在代码层的可行性。

### 17.1 已发现的代码层假设错误(已修)

| ID | 假设错误 | 真实情况 | 修订 |
|---|---|---|---|
| **ER-A1** | OV3 写"注册表"+OV8 写"直接 switch",**plan 内部矛盾** | — | D1=B 拍定 → 保留 OV8,**§16.2-OV3 已重写为 switch 实现**(见上方修订)|
| **ER-A2** | `auth.WithUserID(ctx, string)` 不存在 | `internal/auth/middleware.go:125` 实际是 `auth.WithUser(ctx, *auth.User)` | §16.2-OV3 dispatcher 代码已修 |
| **ER-A3** | `SessionRequest` 含 `OwnerUserID` 字段 | `session.go:125` 真实字段是 `SessionID/Input/Command/...`,user 走 ctx 注入 | §16.2-OV3 dispatcher 代码已修 |
| **ER-A4** | cron map 改 by ID | `cron.go:49` 现状 by `Name`,**不需改** — 用 `Name: "scheduled-task-" + task.ID` 即可 | 唯一性策略:**`(created_by, name)` 复合唯一**,plan §6.3 form 校验 |
| **ER-A5** | robfig/cron/v3 引入但 §16.4 checklist 没列 `go get` | `go.sum` 0 hits | 见 17.3 checklist 补 |

### 17.2 Code Quality 修订

| ID | 修订 |
|---|---|
| **ER-Q1** | 文件命名:**`internal/master/scheduled_task_dispatch.go`**(单文件 + switch),不分 dispatcher_im_push.go / dispatcher_session.go |
| **ER-Q2** | `scheduled_tasks.last_error` schema COMMENT 添加:`'最近一次 run 的最终错误,展示用,不驱动调度逻辑(参 OV7 重试与调度解耦)'` |

### 17.3 Eng Review 补 checklist(累加到 §16.4)

- [ ] `go get github.com/robfig/cron/v3 && go mod tidy`(ER-A5)
- [ ] dispatcher 改成 `internal/master/scheduled_task_dispatch.go` 单文件 + switch(ER-A1=B / ER-Q1)
- [ ] dispatcher 用 `auth.WithUser(ctx, *auth.User)`,不是 `auth.WithUserID`(ER-A2)
- [ ] dispatcher 不传 `OwnerUserID` 到 SessionRequest,user 走 ctx(ER-A3)
- [ ] cron job `Name` 用 `"scheduled-task-" + task.ID` 模式(ER-A4)
- [ ] form 校验 `(created_by, name)` 复合唯一,违反返回 400 + 明确错误体(ER-A4)
- [ ] `last_error` 字段 COMMENT 加注释说明语义变化(ER-Q2)

### 17.4 Test 覆盖矩阵

```
NEW CODEPATHS                                                        TESTS
[+] internal/store/scheduled_task_store.go
    ├── SaveScheduledTask + (created_by,name) 复合唯一               unit
    ├── ListScheduledTasksByUser → 仅返回 created_by=user            unit + e2e 跨用户隔离
    ├── GetScheduledTask + ownership 校验 → 404 不漏存在性          unit
    ├── 两阶段 migration v_N → v_N+1 view alias                     [→E2E] integration
    └── BulkMarkScheduledTaskFailures(OV4)                         unit
[+] internal/master/cron.go(升级)
    ├── ScheduleSpec.Next() — cron + interval + timezone 三路径      table-driven unit
    ├── advisory lock 双 key namespace=0x5C8E                       [→E2E] 多实例并发
    ├── 重启后 reload 异步 + per-task recover + /health 不阻塞       [→E2E] startup
    └── parseSchedule cron_or_interval 互斥校验                      unit
[+] internal/master/scheduled_task_dispatch.go(switch)
    ├── case im_push → push.SendChannelMessage                     mock
    ├── case session → ProcessRequestWithResponse + WithUser        [→E2E]
    ├── case unsupported → fmt.Errorf                              unit
    └── user 不存在时 fail-fast(ER-A2)                            unit
[+] internal/api/scheduled_tasks_handler.go
    ├── 7 endpoints(create/list/get/update/delete/toggle/run-now) handler unit
    ├── ownership middleware 跨用户访问 → 404 不漏存在性             [→E2E] cross-user
    ├── per-user 100 上限 → 429 错误体格式 OV5 标准                  unit
    └── (created_by,name) 复合唯一冲突 → 400                         unit
[+] internal/master/scheduled_task_history_gc.go
    ├── DROP PARTITION 老周分区                                    integration
    └── CREATE PARTITION 下周 idempotent                           unit

REGRESSION 关键(IRON RULE):
[+] 老 /api/v1/channels/push/schedules 三接口语义不变               REG-CRITICAL
[+] 老 scheduled_pushes view alias 仍可读(阶段二)                 REG-CRITICAL
[+] 现有 sessiontodo_gc cron job(by Name)启动后正常工作          REG-CRITICAL
[+] 老 scheduled_pushes 数据自动 target_type='im_push'            REG-CRITICAL

COVERAGE 目标: 0/24 paths → 24/24(100%)
GAPS: 24(4 E2E)
```

### 17.5 Worktree 并行化

```
Lane A (数据 + cron 升级)          独立模块,无依赖
  Phase 1: store + 两阶段 migration
  Phase 2: cron.go 升级 + bootstrap reload + advisory lock
  ↓ 提供 sessiontodo.Store + cron + reload 给 Lane B/C/D

Lane B (REST API)                 等 Lane A Phase 1 store 接口 ready
  Phase 4: scheduled_tasks_handler.go 7 endpoints + ownership
  ↓ 暴露给 Lane D 前端

Lane C (dispatcher)               等 Lane A Phase 2 cron callback signature
  Phase 3: scheduled_task_dispatch.go 单文件 + switch
  ↓ 与 Lane B 测试整合

Lane D (前端 UI)                   等 Lane B API 契约 ready
  Phase 5: ScheduledTasks.tsx + form + history drawer + i18n
  ↓ e2e 测试串起来

Lane E (GC + 历史清理)             Phase 6 收尾
  分区 GC + 历史 cron + 周分区 rotate

执行顺序:
  Step 1: Lane A 起,~3 天(2 个 phase)
  Step 2: Lane B + C 并发(等 Lane A Phase 1 / Phase 2 各自 ready)
  Step 3: Lane D 等 Lane B → 1 天后启动
  Step 4: Lane E 收尾
  Step 5: 全部合并 + e2e 集成测试
```

### 17.6 Eng Review 完成总结

```
+====================================================================+
|         ENG REVIEW — Agent 定时任务系统                            |
+====================================================================+
| Mode                | FULL_REVIEW                                  |
| Architecture        | 5 issues, 1 必修拍板(ER-A1 dispatcher)     |
| Code Quality        | 2 findings,实施层修正                       |
| Test Review         | 24 paths,4 E2E 关键,4 REGRESSION CRITICAL  |
| Performance         | No issues — 分区 GC + advisory lock 都已优化 |
| Outside Voice       | CEO review 已跑(10 项发现已并入 §16)        |
| Critical Gaps       | 0(plan 内部矛盾已通过 ER-A1=B 解决)         |
+====================================================================+
```


---

## 18. Design Review 决策清单(2026-05-06 /plan-design-review)

由 `/plan-design-review` 在 2026-05-06 追加。整体评分 **5/10 → 8/10**。

### 18.1 7-Pass 评分

| Pass | 维度 | 初始 | 终评 |
|---|---|---|---|
| 1 | Information Architecture | 6 | 8 |
| 2 | Interaction State Coverage | 8 | 9 |
| 3 | User Journey & 情感弧 | 4 | 8 |
| 4 | AI Slop Risk | 7 | 9 |
| 5 | DESIGN.md 对齐 | 5 | 9 |
| 6 | Responsive & A11y | 1 | 8(desktop only)|
| 7 | Unresolved Decisions | — | 4 全 resolved |

### 18.2 4 项 Design 决策(DR-D1 ~ DR-D4)

**DR-D1 (DD1)**:cron 表达式输入 = **纯字符串 + cronstrue 实时翻译**(power user 快,简单任务走 interval fallback,与 Q1 一致)

**DR-D2 (DD2)** — 默认采纳:**失败行 = 状态 dot red-500 + 文字 "Failed"**,hover tooltip 显示 last_error 截断;**不**整行 bg 染色(避免视觉压迫,DESIGN.md calm 工业风一致)

**DR-D3 (DD3)** — 默认采纳:**历史展示双层** —
- 点击行 → 行内展开摘要 5 条(succeeded/failed dot + started_at + 截断 output)
- 操作菜单 → "查看完整历史" → 右侧 drawer(分页 20 条,完整 output + error)

**DR-D4 (DD4)**:**desktop only 本期**,移动端进 follow-up — 手机访问 `/scheduled-tasks` 显示引导页 "请在桌面浏览器使用定时任务管理"(>768px 才渲染主界面)。a11y 基本项 desktop 仍达标。

### 18.3 详细 UI 规格(写入 Phase 5)

#### 表格列宽 + 字体(对齐 DESIGN.md Geist/DM Sans)

```
启用 toggle    │ 40px  │ Switch 16px,checked=emerald-500
名称           │ flex  │ DM Sans Medium 14px,truncate + tooltip
调度           │ 200px │ DM Sans 13px + cronstrue 中文翻译(浅色)
                          示例:"0 9 * * *" → "每天 9:00 (Asia/Shanghai)"
目标           │ 140px │ Badge 组件:
                          ├ im_push  →  bg-blue-50 text-blue-900 "IM 飞书"
                          ├ session  →  bg-zinc-100 text-zinc-700 "Web Session"
                          └ skill    →  bg-amber-50 text-amber-900 "Skill"(未来)
上次执行       │ 120px │ DM Sans 13px text-zinc-500
                          ├ 成功  → "2 小时前"
                          ├ 失败  → 状态 dot red-500 + "2 小时前 失败"(hover 看 error)
                          ├ 运行中 → dot blue-500 animate-pulse + "执行中"
                          └ 从未  → "—"
操作           │ 60px  │ MoreVertical icon dropdown(编辑/立即运行/复制/删除)
```

#### 状态 dot 规格(DR-D2)

```
pending     #94a3b8 (slate-400)   │ 静止
in_progress #3b82f6 (blue-500)    │ animate-pulse 2s
succeeded   #10b981 (emerald-500) │ 静止 + Check glyph(可选)
failed      #ef4444 (red-500)     │ 静止 + hover tooltip 显示 last_error 截断
disabled    row opacity-60 + dot slate-400
```

#### 行展开历史(DR-D3 摘要)

```
点击行 → 折叠展开 → 显示最近 5 条 run:

  ▼ Lint 巡检                 每 2 小时        Web Session    23 分钟前      ⋮
    ┌────────────────────────────────────────────────────────────────────┐
    │ Recent runs:                                                        │
    │ ● 23 min ago    succeeded   "已扫描 47 个文件,无 lint error"        │
    │ ● 2h ago        succeeded   "已扫描 47 个文件..."                  │
    │ ● 4h ago        failed      "API timeout after 30s [详情 →]"      │
    │ ● 6h ago        succeeded   "..."                                  │
    │ ● 8h ago        succeeded   "..."                                  │
    │                                                              [查看完整历史 →]
    └────────────────────────────────────────────────────────────────────┘
```

操作菜单 "查看完整历史" → 右侧 drawer 分页 20 条,展示完整 output / error / session_id 链接。

#### Empty 状态(反 AI slop)

```
icon:        lucide CalendarPlus 64px text-zinc-300
            (不是 emoji,不是彩色圆圈,不是 SaaS 模板插图)
标题:        DM Sans 16px Medium "还没有定时任务"
副标题:      DM Sans 14px text-zinc-500
            "用 agent 自动跑代码审查、生成日报、推送提醒等"
CTA:         Button "新建任务" Geist Medium,主色按钮
```

#### plan banner 与状态(类比 sessiontodo plan_runtime banner 模式)

```
- success/empty 默认无顶部 banner
- error(API 加载失败):红条 "加载失败 [重试]" + 缓存内容灰度
- partial(WS 调度服务异常):
  触发条件 = 启动后 reload 失败任务数 > 0,或 metrics 指标 cron_tick_drift > 30s
  渲染 = 黄条 "调度服务异常,N 个任务可能未按时执行 [查看详情 →]"
```

#### a11y 完整规格(desktop)

- `<table role="table">` + `<th scope="col">`
- 行可键盘聚焦:`Tab` → 当前行,`Enter` 展开摘要,`E` 编辑,`R` 立即运行,`Del` 删除(带确认)
- 状态 dot 同时带 `aria-label="运行中"` / `"上次失败,错误:..."`
- `aria-live="polite"` 区域同步运行状态变化(立即运行后)
- 触摸目标 ≥ 44px(行 44px,操作按钮 44×44)
- 对比度 DM Sans 14px 用 `#1a1a1a` on `#fafafa`(>7:1) — DESIGN.md 默认

#### 响应式断点(DR-D4 desktop only)

| 断点 | 行为 |
|---|---|
| `>=1024px` desktop | full table 渲染 |
| `768-1023px` tablet | full table,操作菜单收紧文案 |
| `<768px` mobile | **不渲染主界面**,显示引导页 `<EmptyState icon="MonitorSmartphone" title="请在桌面浏览器使用" subtitle="定时任务管理需要更宽的屏幕" />` |

#### 复用现有组件

- `frontend/src/components/ui/Table.tsx` — 表格(已有)
- `frontend/src/components/ui/Sheet.tsx` — 历史 drawer(已有,跟 sessiontodo TodosList 同模式)
- `frontend/src/components/ui/Badge.tsx` — target 标识(已有)
- react-hook-form — 表单(仓库已用)
- `cronstrue` — npm install,~10KB,实时 cron 翻译
- `Intl.supportedValuesOf('timeZone')` — 浏览器原生,无依赖

### 18.4 follow-up(进 TODOS.md)

- **P3 scheduled-tasks: 移动端响应式 UI** — DR-D4 决议本期 desktop only,follow-up 做 table→card list + bottom sheet form。触发条件:产品反馈 ≥ 3 次"想在手机管理"。

### 18.5 Phase 5 实施 checklist(给前端工程师)

- [ ] `<768px` 引导页 + `>=768px` 才渲染主界面(DR-D4)
- [ ] cron_expr 输入 + cronstrue 实时翻译 + 错误体提示(DR-D1)
- [ ] 失败行状态 dot red-500 + hover last_error tooltip(DR-D2)
- [ ] 行点击展开 5 条历史摘要(DR-D3)
- [ ] 操作菜单 "查看完整历史" → drawer 分页 20 条(DR-D3)
- [ ] empty 状态用 lucide `CalendarPlus`,不要 emoji / 彩色圆圈
- [ ] 5 个 banner / Toast 文案 i18n 进 `frontend/src/i18n/locales/`
- [ ] 列宽固定(40 / flex / 200 / 140 / 120 / 60)
- [ ] 状态 dot 颜色走 DESIGN.md token,不写死 hex
- [ ] keyboard nav:Tab/Enter/E/R/Del 全键盘可达
- [ ] `aria-live` 区域绑运行状态变化
- [ ] e2e:create / list / run-now / 看历史 / delete 全链路

---

## GSTACK REVIEW REPORT

| Review | Trigger | Why | Runs | Status | Findings |
|--------|---------|-----|------|--------|----------|
| CEO Review | `/plan-ceo-review` | Scope & strategy | 1 | clean (PLAN) | SELECTIVE EXPANSION, 3 proposals accepted, OV10 业务场景占位 |
| Codex Review | `/codex review` | Independent 2nd opinion | 0 | — | not run (codex CLI unavailable, claude subagent ran in CEO+eng review) |
| Eng Review | `/plan-eng-review` | Architecture & tests (required) | 1 | clean (PLAN) | FULL_REVIEW, 7 issues addressed, 5 ER findings 全修 |
| Design Review | `/plan-design-review` | UI/UX gaps | 1 | clean (PLAN) | score 5/10 → 8/10, 4 design decisions resolved, full UI specs |
| DX Review | `/plan-devex-review` | Developer experience gaps | 0 | — | not applicable (internal admin UI, no external API) |

- **OUTSIDE VOICE (CEO):** 10 findings,9 已并入主体 + 1 战略 OV10 业务场景占位
- **OUTSIDE VOICE (Eng):** 5 ER findings,plan §16 OV3/OV8 内部矛盾通过 ER-A1=B 解决
- **CROSS-MODEL:** CEO + Eng outside voices independently flagged Phase 4/5 sequencing 与 advisory lock namespace,strong signal validating ER-A2/ER-A3
- **UNRESOLVED:** 0
- **VERDICT:** CEO + ENG + DESIGN ALL CLEARED — ready to implement. Lane A 可独立启动。
