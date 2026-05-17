# OInfer 自动化测试生成计划

## 目标

通过 Hive agent + OInfer MCP，实现：
1. 根据 GitHub 项目代码自动生成 OInfer 测试数据结构（API 定义 + 测试场景）
2. 自动执行测试计划
3. 自动分析测试报告，输出结构化结论

**V1 范围：** Happy path 覆盖，OpenAPI spec 优先，单服务单环境。测试数据隔离、fixture 管理、非幂等写操作保护为 V2。

---

## 架构

```
用户 → Hive agent (skill: oinfer-test)
         │
         ├─ memory search → 查是否已有该服务的测试上下文（按 repo+branch 精确匹配）
         │
         ├─ [首次 / force_recreate] GitHub MCP（优先）/ webfetch raw URL（fallback）
         │   读取 repo 文件结构 → 找 openapi.yaml / swagger.json
         │   fallback: 找路由文件（best-effort，不保证完整性）
         │   LLM 理解接口定义 → 构造 OInfer 数据结构
         │
         ├─ OInfer MCP tools（写入，先查再建）
         │   oinfer_list_targets  → 查已有 target，避免重复
         │   oinfer_save_target   → 创建 API 定义
         │   oinfer_save_scene    → 创建测试场景
         │   oinfer_save_flow     → 写入场景节点和边
         │   oinfer_save_auto_plan → 创建自动化计划
         │   oinfer_attach_scene_to_plan → 关联场景到计划（返回 cloned_scene_id）
         │
         ├─ OInfer MCP tools（执行）
         │   oinfer_run_auto_plan → 触发执行，返回 report_id
         │
         ├─ OInfer MCP tools（报告）
         │   oinfer_get_report_detail → 轮询直到 status=completed/failed
         │
         ├─ LLM 分析报告 → 输出结论
         │
         └─ memory save/update → 持久化测试上下文（含 memory_id 用于后续 update）
```

---

## 分工

| 模块 | 负责方 | 状态 |
|---|---|---|
| OInfer MCP server 基础框架 | OInfer 终端 | 开发中 |
| OInfer MCP 工具实现 | OInfer 终端 | 待开始 |
| Hive skill: oinfer-test | Hive 终端 | 待开始 |
| 记忆结构设计 | 已确定 | ✅ |
| MCP 工具列表 | 已确定 | ✅ |

---

## OInfer MCP 工具列表

> brain 接口路径是 OInfer MCP server 的内部实现细节，Hive 侧不需要关心。以下只定义工具名、参数和用途。

### 查询类（上下文获取）

| 工具名 | 参数 | 用途 |
|---|---|---|
| `oinfer_list_teams` | 无 | 获取可用团队列表，拿 team_id |
| `oinfer_list_folders` | `team_id` | 获取文件夹树，拿 parent_id（写入时必须指定） |
| `oinfer_list_envs` | `team_id` | 获取环境列表，拿 env_id（类型：string） |
| `oinfer_list_targets` | `team_id, folder_id?` | 查已有 target（按 method+url 去重用） |
| `oinfer_list_scenes` | `team_id, folder_id?` | 查已有场景，避免重复创建 |
| `oinfer_list_auto_plans` | `team_id, folder_id?` | 查已有计划 |

### 写入类

| 工具名 | 关键参数 | 用途 |
|---|---|---|
| `oinfer_save_target` | `team_id, parent_id, name, method, url, request` | 创建 API 定义 |
| `oinfer_save_scene` | `team_id, parent_id, name` | 创建测试场景 |
| `oinfer_save_flow` | `team_id, scene_id, nodes, edges` | 写入场景节点和边 |
| `oinfer_save_auto_plan` | `team_id, plan_name` | 创建自动化计划，返回 plan_id |
| `oinfer_attach_scene_to_plan` | `team_id, scene_id, plan_id` | 把场景克隆一份关联到计划，**返回 cloned_scene_id** |

### 执行类

| 工具名 | 关键参数 | 用途 |
|---|---|---|
| `oinfer_run_auto_plan` | `team_id, plan_id, env_id` | 触发执行，返回 report_id |
| `oinfer_stop_auto_plan` | `team_id, plan_id, report_id` | 停止指定执行实例 |

### 报告类

| 工具名 | 关键参数 | 用途 |
|---|---|---|
| `oinfer_get_report_list` | `team_id, plan_id` | 列出报告 |
| `oinfer_get_report_detail` | `team_id, report_id` | 读取报告详情（含 pass/fail 统计） |

### OInfer MCP 返回值契约（需 OInfer 终端确认）

所有写入工具返回：
```json
{ "id": "string", "error": "string|null" }
```

`oinfer_get_report_detail` 返回的 `status` 枚举：
- `running` — 执行中
- `completed` — 完成（含部分失败）
- `failed` — 执行本身失败（非测试失败）

> ⚠️ 这是 Hive 侧的假设，需 OInfer 终端确认实际枚举值。

### 命名规范

所有由 Hive 创建的 OInfer 资源，名称格式：
```
hive/{repo_name}/{branch}/{resource_type}/{name}
```
例：`hive/api-service/main/scene/用户登录流程`

这样可以按前缀过滤，避免与手动创建的资源碰撞。

### 最小可用集（第一版）

```
oinfer_list_teams
→ oinfer_list_folders
→ oinfer_list_envs
→ oinfer_list_targets   ← 新增，先查再建
→ oinfer_save_target
→ oinfer_save_scene
→ oinfer_save_flow
→ oinfer_save_auto_plan
→ oinfer_attach_scene_to_plan  ← 需返回 cloned_scene_id
→ oinfer_run_auto_plan
→ oinfer_get_report_detail
```

---

## Hive 记忆结构

每个被测服务存一条 `project` 类型记忆，以 `repo_url + branch` 作为唯一标识：

```json
{
  "operation": "save",
  "type": "project",
  "tags": ["oinfer-service", "api-service", "main", "test-context"],
  "content": "{\"service_name\":\"api-service\",\"repo\":\"github.com/vast/api-service\",\"branch\":\"main\",\"commit_sha\":\"abc123\",\"spec_hash\":\"sha256:def456\",\"tech_stack\":\"Go/Gin\",\"team_id\":\"xxx-team-id\",\"folder_id\":\"yyy-folder-id\",\"env_id\":\"1\",\"auto_plan_id\":\"yyy-plan-id\",\"memory_id\":12345,\"scenes\":[{\"name\":\"用户登录流程\",\"source_scene_id\":\"aaa\",\"cloned_scene_id\":\"aaa-clone\"}],\"last_report_id\":\"zzz-report-id\",\"last_run_at\":\"2026-04-14T10:00:00Z\",\"known_issues\":[]}"
}
```

**关键字段说明：**

| 字段 | 用途 |
|---|---|
| `repo + branch` | 唯一标识，精确匹配，不用 fuzzy search |
| `commit_sha` | 检测代码变更，决定是否需要 force_recreate |
| `spec_hash` | OpenAPI spec 内容 hash，比 commit 更精确 |
| `memory_id` | Hive memory 记录 ID，用于后续 update 操作 |
| `folder_id` | 所有资源放在同一个 folder 下，便于清理 |
| `cloned_scene_id` | attach 后的克隆 ID，不是原始 scene_id |

**查询方式（精确匹配，不用 fuzzy）：**
```json
{ "operation": "search", "query": "oinfer-service api-service main test-context" }
```
拿到结果后，代码层面验证 `repo` 和 `branch` 字段完全匹配。

**注意：** Hive memory 的 `content` 字段是 `string` 类型。存入时需 `json.Marshal`，读取后需 `json.Unmarshal`。

---

## Hive Skill 设计：`oinfer-test`

### 输入参数

| 参数 | 类型 | 必填 | 说明 |
|---|---|---|---|
| `repo_url` | string | 是 | GitHub repo URL |
| `branch` | string | 否 | 分支名，默认 main |
| `team_id` | string | 否 | 指定 OInfer 团队，不填则用 oinfer_list_teams 第一个 |
| `env_id` | string | 否 | 指定测试环境（string 类型），不填则用 oinfer_list_envs 第一个 |
| `force_recreate` | bool | 否 | 强制重新读代码创建（默认 false） |

### 执行流程

```
1. memory search "oinfer-service {service_name} {branch} test-context"
   代码层验证 repo + branch 精确匹配
   ├─ 找到 + force_recreate=false + commit_sha 未变
   │   → 直接用已有 plan_id 执行（跳到步骤5）
   ├─ 找到 + commit_sha 已变（或 force_recreate=true）
   │   → 走完整流程，复用已有 folder_id 和 plan_id（覆盖更新）
   └─ 没找到
       → 走完整流程

2. 读 GitHub repo（优先 GitHub MCP，fallback webfetch raw URL）
   - 优先找 openapi.yaml / swagger.json（可靠）
   - fallback: 找路由文件（best-effort，V1 限制：不保证完整性）
   - 记录 spec_hash（OpenAPI 内容 sha256）和 commit_sha

3. 调用 OInfer MCP 写入（先查再建）
   - oinfer_list_teams → 确认 team_id
   - oinfer_list_folders → 确认或创建 folder（命名：hive/{repo}/{branch}）
   - oinfer_list_envs → 确认 env_id
   - oinfer_list_targets → 查已有 target（按 method+url 匹配）
   - oinfer_save_target × N（跳过已存在的）
     ⚠️ 写入失败：记录已成功 target_id 到 memory draft，下次重跑跳过
   - oinfer_list_scenes → 查已有场景
   - oinfer_save_scene（按业务流程分组，命名规范：hive/{repo}/{branch}/scene/{name}）
   - oinfer_save_flow（LLM 推断依赖顺序，V1 限制：仅 happy path，不处理 fixture/teardown）
   - oinfer_save_auto_plan（命名规范：hive/{repo}/{branch}/plan）
   - oinfer_attach_scene_to_plan → 记录返回的 cloned_scene_id

4. memory save 测试上下文
   - 包含 memory_id（save 操作返回的 ID，用于后续 update）
   - 包含 commit_sha、spec_hash、folder_id、cloned_scene_id
   - draft 状态：写入部分成功时先存 draft，全部成功后改为 confirmed

5. oinfer_run_auto_plan → 触发执行，拿到 report_id

6. 轮询 oinfer_get_report_detail 直到 status=completed/failed
   - 间隔：30s
   - 超时：30min
   - 超时后明确标注"测试仍在进行中，以下为不完整数据"，不作为最终结论

7. LLM 分析报告
   - 总体通过率
   - 失败接口列表 + 失败原因（区分：测试失败 vs 环境错误 vs 生成问题）
   - 建议

8. memory update（用 memory_id 精确更新）
   - 更新 last_report_id、last_run_at
   - known_issues 存结构化数据：{endpoint, status_code, error_type, report_id}
```

---

## 已确定决策

| 问题 | 决策 |
|---|---|
| GitHub 访问方式 | GitHub MCP 优先，webfetch raw URL 作为 fallback（两者不等价，不自动选择） |
| OInfer MCP 认证 | token 写在 Hive MCP server 配置里；多租户场景需额外 per-user 授权（V2） |
| 场景编排策略 | LLM 自动判断接口依赖关系，V1 仅 happy path，不处理 fixture/teardown |
| 服务唯一标识 | `repo_url + branch`，精确匹配，不用 fuzzy search |
| env_id 类型 | string（不是 int） |
| 命名规范 | `hive/{repo_name}/{branch}/{type}/{name}` |

---

## 待确定（需 OInfer 终端确认）

- [ ] `oinfer_get_report_detail` 返回的 status 枚举值（running/completed/failed 还是其他？）
- [ ] `oinfer_attach_scene_to_plan` 是否返回 cloned_scene_id？
- [ ] `oinfer_save_target` 同名幂等性：同名 target 是覆盖还是报错？
- [ ] OInfer MCP server 地址和 token 的配置 key 名称

## 已知 V1 限制（V2 处理）

- 测试数据隔离：无 namespace 隔离，stateful 测试（登录、创建订单）会污染共享环境
- Fixture 管理：无 seed/reset hooks，无补偿操作
- 多租户授权：token 全局共享，无 per-user 权限控制
- 路由文件提取：best-effort，不保证完整性（GraphQL/gRPC/动态路由不支持）
- Rollback：写入失败无补偿，孤立资源需手动清理
- Observability：无跨系统 correlation ID
