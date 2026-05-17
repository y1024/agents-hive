# filesystem 统一工具实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use `executing-plans` or equivalent task-by-task execution. Steps use checkbox (`- [ ]`) syntax for tracking.
>
> **Control plan:** Do not implement this child plan in isolation. First read `docs/计划与路线/归档/2026-05-16-工具域统一与分层收敛总控实施验收计划.md`; Phase 1 is only complete when the control plan hard gates also pass.

**Goal:** 把 `ls` / `glob` / `grep` / `read_file` / `write_file` / `edit` / `multiedit` 收敛为一个结构化的 `filesystem` 工具入口，同时保留现有安全边界、read-before-edit、路由约束、Plan mode 限制和旧工具兼容能力。

**Architecture:** 新增 `filesystem` 作为 domain-level mixed action 工具，使用 `action` 字段区分只读与写入动作。底层优先复用现有文件工具实现和共享 helper，不重新发明文件读写/搜索逻辑；策略层把 `filesystem` 纳入 `mixedActionRules`，通过 `AllowedToolInputs["filesystem"]["action"]` 精确限制当前意图允许的动作。旧工具先保留并可调用，默认可见性逐步迁移到 `filesystem`；旧只读工具在 v1 不彻底下线，因为 `filesystem` 只能设置工具级 `IsConcurrencySafe=false`，仍需要 `read_file` / `grep` / `glob` / `ls` 承担 batch 并发读性能。

**Tech Stack:** Go backend (`internal/tools`, `internal/router`, `internal/master`, `internal/toolruntime`, `internal/mcphost`, `internal/config`, `internal/bootstrap`, `internal/store`, `internal/agentquality`, `internal/observability`), React i18n (`frontend/src/i18n/locales`), existing table-driven Go tests, focused `go test` packages before full regression.

**Status:** COMPLETED / ARCHIVED, 2026-05-16. Phase 1 代码硬门已通过；旧只读工具默认可见性降级属于 Phase 7 指标观察项，不在本子计划内标完成。验收报告见 `docs/验收报告/2026-05-16-工具域统一与分层收敛实施验收报告.md`。

**Final acceptance note:** 下方实施切片中的 checkbox 已按 2026-05-16 二次验收结果收口。`8.2`、`8.3`、`9.3` 是指标驱动迁移窗口，仍保持观察状态；它们不是 Phase 1 代码归档阻塞项。

---

## 0. 决策结论

应当做 `filesystem`，但它不是 `bash` 的替代品，也不是把所有工具改名成 `edit`。

`bash` 是命令执行层，schema 只有 `command`，适合测试、构建、git、包管理、系统命令和逃生场景。它的能力开放，运行风险是 `RiskRuntimeExec`。

`filesystem` 是结构化文件系统语义层，适合常规文件探索、搜索、读取和编辑。它的关键价值是系统能直接从 `action` 判断风险：

| action | 原工具 | 风险 | 默认策略 |
|---|---|---|---|
| `list` | `ls` | read-only | 允许 |
| `glob` | `glob` | read-only | 允许 |
| `grep` | `grep` | read-only | 允许 |
| `read` | `read_file` | read-only | 允许 |
| `write` | `write_file` | local-write | 需要本地写入意图 |
| `edit` | `edit` | local-write | 需要本地写入意图，保留 read-before-edit |
| `multiedit` | `multiedit` | local-write | 需要本地写入意图，保留原子回滚 |

第一版不并入 `bash`、`git`、构建测试、LSP、formatter。`apply_patch` 先保留独立工具，稳定后再评估是否作为 `filesystem.patch` 加入 v2。

### 0.1 Eng 审阅采纳决议

Claude Code Eng 审阅指出的关键问题全部进入本计划执行范围：

| 决策 | 结论 | 落点 |
|---|---|---|
| mixed action 基础设施 | 不重建，只把 `filesystem` 接入既有 `mixedActionRules` / `AllowedToolInputs` / runtime gate | Task 3, Task 4 |
| 批量编辑命名 | 只保留 `multiedit` 作为新 schema / 策略 / 文案名称；`multi_edit` 只作为旧兼容清理对象 | Task 3, Task 7 |
| `apply_patch` 关系 | v1 不并入 `filesystem`，prompt 明确 `filesystem.edit|multiedit` 与 `apply_patch` 的选择优先级 | Task 5 |
| feature flag | 新增 `config.Tools.FilesystemEnabled`，默认开启前必须可通过配置关闭 | Task 2, Task 7 |
| DB 默认权限 | `pgDefaultPermissionRulesJSON` 增加 `filesystem`，同时清理 `multi_edit` 默认写回 | Task 7 |
| WebUI i18n | 增加 `filesystem` 和各 action 的中英文文案 | Task 7 |
| agentquality | 迁移期允许 `filesystem` 与旧文件工具双轨，质量报告标记首选 `filesystem` | Task 6 |
| 观测指标 | 使用既有 `policy_decision_total` / `action_guard_ask_total` / `hive_plan_mode_gate_denied_total`，新增项统一 `hive_*_total` | Task 6, Task 7 |
| audit | `filesystem` 七个 action 统一审计，路径脱敏，内容不入指标或审计字段 | Task 6 |
| batch 并发 | `filesystem.IsConcurrencySafe=false`，旧只读工具长期保留；per-action concurrency 留到协议 v2 | Task 2, Task 8 |
| runtime disable | feature flag 既要影响启动注册，也要影响 visibility / policy / admission，配置变更后可阻止新调用 | Task 2, Task 4 |
| Replay / file_change | timeline 展示 `tool.action`，file_change 识别 `filesystem.write/edit/multiedit` | Task 7 |
| 错误归一 | `filesystem` 错误 code 化、路径脱敏，不直接拼接绝对路径和内容 | Task 2 |
| 长会话状态 | compaction / service restart 后 read-before-edit 需要重新读取，不能靠 stale `ReadTracker` 放行 | Task 4, Task 6 |
| 权威文档 | 同步 `Tool-Routing.md`、安全权限模型、README/CLAUDE 工具说明 | Task 10 |

## 1. 当前代码事实

现有工具在 `internal/tools/tools.go` 中分别注册：

- `registerReadFile`
- `registerWriteFile`
- `registerGlob`
- `registerGrep`
- `registerBash`
- `registerEdit`
- `registerLS`
- `registerMultiEdit`

其中 `read_file` / `glob` / `grep` 标记 `IsConcurrencySafe: true`，而 `edit` / `write_file` / `multiedit` 走文件锁、`ReadTracker` 和 `FileTracker`。

策略侧现状：

- `internal/router/capability_registry.go` 的 `builtinToolRules` 把 `ls` / `glob` / `grep` / `read_file` 标成 read-only，把 `write_file` / `edit` / `multiedit` 标成 local-write。
- 同文件的 `mixedActionRules` 已经支持 `memory`、`taskboard`、`browser_interact` 这类 mixed action 工具。
- `internal/router/tool_policy.go` 的 `evaluateMixedToolPolicy` 已经能按 action 区分只读、本地写入、外部发送和高风险动作。
- `internal/router/decision.go` 的 `MixedAllowedToolInputsForIntent` 会把当前 intent 下允许的 action 写入 `RouteDecision.AllowedToolInputs`。
- `internal/toolruntime/action.go` 和 `internal/master/plan_runtime.go` 会在运行时继续检查 allowed inputs 和 plan mode。
- `internal/router/capability_registry.go`、`internal/config/defaults.go`、`internal/store/postgres_migrate.go`、`frontend/src/i18n/locales/{zh,en}.json` 仍同时出现 `multiedit` 和 `multi_edit`。
- `internal/store/postgres_migrate.go` 的 `pgDefaultPermissionRulesJSON` 还没有 `filesystem` 默认权限。
- `frontend/src/i18n/locales/{zh,en}.json` 还没有 `filesystem` 和 action 级展示文案。
- `internal/agentquality/testdata/route_eval/corpus.json` 仍以旧文件工具作为期望，需要迁移期双轨。
- `internal/agentquality/testdata/aq*.json` 和 `internal/agentquality/testdata/longrun/` 也可能硬编码 `grep` / `read_file` 期望，不能只改 corpus。
- `frontend/src/components/replay/ReplayTimeline.tsx` 当前按 `tool_name` 展示，`filesystem` 上线后需要展示为 `filesystem.<action>`。
- `internal/master/react_processor.go` 的 file-change 识别仍以旧工具名为主，必须补 `filesystem.write/edit/multiedit`，否则 replay 和审计会漏文件变化。
- 当前指标命名不是 `tool_call_total` 这一类；已存在策略指标是 `policy_decision_total`、`action_guard_ask_total` 和 `hive_plan_mode_gate_denied_total`。`internal/master/react_processor.go` 现有主路径还会发 `hive.tool.duration_ms` 和 `hive.tool.errors`，并带 `session_id` label；这些是既有兼容指标，不作为本计划新增计数器的命名模板。

因此实施重点不是发明新策略，也不是大规模重写文件工具；而是把 `filesystem` 纳入现有 mixed action 架构，并在确有共享需要时从旧工具闭包里抽最小 private helper。

### 1.1 复用优先 source-of-truth

实现 `filesystem` 前必须先做 source-of-truth 映射。目标不是把旧工具迁到一套全新实现，而是让旧工具和新 `filesystem` 同时调用同一批已有能力。

| 新行为 | 必须复用的现有能力 | 允许新增的最小代码 |
|---|---|---|
| `filesystem.list` | `lsInput`、`registerLS` 当前路径默认值、目录遍历和输出格式 | `filesystem` 参数到 `lsInput` 的 mapper；必要时从 `registerLS` 抽私有 helper |
| `filesystem.glob` | `globInput`、`registerGlob` 当前匹配和输出逻辑 | `filesystem.pattern` 到 `globInput.Pattern` 的 mapper |
| `filesystem.grep` | `grepInput`、`registerGrep` 当前搜索、context、max_results、multiline 逻辑 | `filesystem` 字段到 `grepInput` 的 mapper |
| `filesystem.read` | `readFileInput`、`registerReadFile` 当前文件大小限制、二进制检测、PDF/图片 data URI、输出截断、`ReadTracker.RecordRead` | `filesystem` 字段到 `readFileInput` 的 mapper |
| `filesystem.write` | `writeFileInput`、`registerWriteFile` 当前 read-before-write、`globalFileLock`、`globalFileTracker.Track`、诊断触发 | `filesystem` 字段到 `writeFileInput` 的 mapper |
| `filesystem.edit` | `editInput`、`registerEdit` 当前 read-before-edit、外部修改检测、`globalFileLock`、`globalFileTracker`、诊断触发 | `filesystem` 字段到 `editInput` 的 mapper |
| `filesystem.multiedit` | `multieditInput`、`executeMultiEdit`、all-or-rollback、`globalFileLock`、`ReadTracker`、`globalFileTracker` | `filesystem.edits` 到 `[]editOperation` / `multieditInput` 的 mapper |
| action 风险 | `mixedActionRules`、`EvaluateToolPolicy`、`MixedAllowedToolInputsForIntent`、`RouteInputDenyReason`、Plan mode gate | 只新增 `filesystem` rule 和测试 |
| 回放 / file change | `changedFilesFromToolCall`、`logFileChangeIfNeeded`、前端 journal 分类 | 解析 `arguments.action` 和 `edits[].path` |

不得新增：

- 第二套文件搜索、读取、写入、编辑或批量编辑算法。
- 第二套文件锁、read tracker、file tracker、外部修改检测或 LSP diagnostics。
- 第二套 action policy、Plan mode gate、ActionGuard 替代逻辑。
- 第二套 IM 发送路径或通用 domain runtime。

## 2. 目标行为

### 2.1 常规读代码

用户问“看一下这个仓库里 tool policy 怎么实现的”：

1. 模型默认看到 `filesystem`。
2. 调用 `filesystem {"action":"grep", ...}` 搜索。
3. 调用 `filesystem {"action":"read", ...}` 读取文件。
4. 路由和运行时都把这些 action 判定为 read-only。

### 2.2 修改代码

用户说“修一下这个 bug”：

1. route intent 是 `IntentWriteLocal` 且 `AllowsSideEffects=true`。
2. `filesystem` 可调用，`AllowedToolInputs["filesystem"]["action"]` 包含 `list|glob|grep|read|write|edit|multiedit`。
3. `filesystem.edit` 必须先 `filesystem.read` 或旧 `read_file` 记录过同一路径。
4. 编辑成功后继续记录 `FileTracker` hash 和 LSP diagnostics。

### 2.3 Plan mode

Plan mode 中只允许 `filesystem` 的只读 action：

```json
{
  "filesystem": {
    "action": "list|glob|grep|read"
  }
}
```

`write` / `edit` / `multiedit` 即使工具名在 plan allow-list 中，也必须被 route input gate 或 runtime action policy 拒绝。

### 2.4 旧工具兼容

迁移期内以下调用必须继续工作：

- `ls`
- `glob`
- `grep`
- `read_file`
- `write_file`
- `edit`
- `multiedit`

旧工具不能立即删除，因为：

- 历史 prompt、skill、sub-agent 和测试可能仍直接调用旧工具名。
- 当前 `ReadTracker` 行为依赖读工具记录；新旧读入口应共享同一个 tracker。
- Tool recall / fast-path / plan mode 已有测试覆盖旧工具。

## 3. 非目标

第一版不做：

- 不把 `bash` 并入 `filesystem`。
- 不把 `git`、`go test`、`npm run build` 等命令语义并入 `filesystem`。
- 不把 LSP 工具并入 `filesystem`。
- 不新增 `delete` / `move` / `copy` / `chmod` / `mkdir`。
- 不移除旧工具。
- 不彻底下线旧只读工具；除非先完成 `mcphost` per-action concurrency 升级，否则 `read_file` / `grep` / `glob` / `ls` 仍需保留给 batch 并发读。
- 不改变文件路径 sandbox 规则。
- 不放宽 read-before-edit 或外部修改检测。
- 不在 v1 重构全局 `ReadTracker` / `FileTracker` 的 session 作用域；但必须记录并测试其 compaction、重启和跨 session 风险。
- 不把 HITL `PermissionRule.pattern` 扩展成 action policy；如需 action-aware HITL，另开设计。

## 4. Schema 设计

`filesystem` 使用单工具、多 action 的 flat schema。第一版不要使用复杂 `oneOf`，因为部分模型和 MCP 客户端对 `oneOf` 遵循不稳定。采用所有字段可选、由后端按 action 做强校验。

```json
{
  "type": "object",
  "properties": {
    "action": {
      "type": "string",
      "enum": ["list", "glob", "grep", "read", "write", "edit", "multiedit"]
    },
    "path": {
      "type": "string"
    },
    "recursive": {
      "type": "boolean"
    },
    "max_depth": {
      "type": "integer"
    },
    "pattern": {
      "type": "string"
    },
    "glob": {
      "type": "string"
    },
    "type": {
      "type": "string"
    },
    "context": {
      "type": "integer"
    },
    "before": {
      "type": "integer"
    },
    "after": {
      "type": "integer"
    },
    "max_results": {
      "type": "integer"
    },
    "multiline": {
      "type": "boolean"
    },
    "offset": {
      "type": "integer"
    },
    "limit": {
      "type": "integer"
    },
    "show_line_numbers": {
      "type": "boolean"
    },
    "content": {
      "type": "string"
    },
    "old_string": {
      "type": "string"
    },
    "new_string": {
      "type": "string"
    },
    "replace_all": {
      "type": "boolean"
    },
    "edits": {
      "type": "array",
      "items": {
        "type": "object",
        "properties": {
          "path": {"type": "string"},
          "old_string": {"type": "string"},
          "new_string": {"type": "string"},
          "replace_all": {"type": "boolean"}
        },
        "required": ["path", "old_string", "new_string"]
      }
    }
  },
  "required": ["action"]
}
```

后端 action 校验规则：

| action | 必填字段 | 可选字段 | 输出兼容 |
|---|---|---|---|
| `list` | `path` 可为空 | `recursive`, `max_depth` | 与 `ls` 一致 |
| `glob` | `pattern` | `path` | 与 `glob` 一致 |
| `grep` | `pattern` | `path`, `glob`, `type`, `context`, `before`, `after`, `max_results`, `multiline` | 与 `grep` 一致 |
| `read` | `path` | `offset`, `limit`, `show_line_numbers` | 与 `read_file` 一致 |
| `write` | `path`, `content` | 无 | 与 `write_file` 一致 |
| `edit` | `path`, `old_string`, `new_string` | `replace_all` | 与 `edit` 一致 |
| `multiedit` | `edits` | 无 | 与 `multiedit` 一致 |

错误格式沿用现有 `errorResult`，但错误文案必须包含 action，例如：

```text
filesystem.edit: 缺少 path
filesystem.grep: 缺少 pattern
filesystem.multiedit: edits 不能为空
```

## 5. 实施切片

### Task 1: 复用现有文件工具执行能力

**Files:**

- Modify: `internal/tools/tools.go`
- Modify: `internal/tools/ls.go`
- Modify: `internal/tools/multiedit.go`
- Test: `internal/tools/tools_test.go`
- Test: `internal/tools/ls_test.go`
- Test: `internal/tools/multiedit_test.go`

- [x] **Step 1.1: 先提交 source-of-truth 映射**

在改代码前，把第 1.1 节映射核对到当前代码，形成施工记录：

```markdown
| filesystem action | existing code owner | extraction needed | reason |
|---|---|---|---|
| list | internal/tools/ls.go registerLS | yes/no | ... |
| glob | internal/tools/tools.go registerGlob | yes/no | ... |
| grep | internal/tools/tools.go registerGrep | yes/no | ... |
| read | internal/tools/tools.go registerReadFile | yes/no | ... |
| write | internal/tools/tools.go registerWriteFile | yes/no | ... |
| edit | internal/tools/tools.go registerEdit | yes/no | ... |
| multiedit | internal/tools/multiedit.go executeMultiEdit | yes/no | ... |
```

判断规则：

- 如果旧 handler 已经调用可复用 helper，`filesystem` 直接复用 helper。
- 如果旧 handler 的核心逻辑还在闭包里，只抽最小 private helper；不要借机重排文件结构。
- 如果某个 action 只需要 mapper，不抽 executor。
- `executeMultiEdit` 已存在，默认直接复用，不要改写其核心算法。

- [x] **Step 1.2: 为必须共享的旧工具抽最小 executor**

将旧工具 handler 中的核心逻辑抽成内部函数，旧 register 函数只负责 JSON decode 和调用 executor。

建议函数签名只是目标形态，不是必须一次性全部落地；以 Step 1.1 的映射表为准：

```go
func executeReadFile(ctx context.Context, params readFileInput, tracker *ReadTracker) (*mcphost.ToolResult, error)
func executeWriteFile(ctx context.Context, params writeFileInput, tracker *ReadTracker) (*mcphost.ToolResult, error)
func executeGlob(ctx context.Context, params globInput) (*mcphost.ToolResult, error)
func executeGrep(ctx context.Context, params grepInput) (*mcphost.ToolResult, error)
func executeEdit(ctx context.Context, params editInput, tracker *ReadTracker, logger *zap.Logger) (*mcphost.ToolResult, error)
func executeLS(ctx context.Context, params lsInput) (*mcphost.ToolResult, error)
```

`executeMultiEdit` 已存在，保留其核心逻辑；如 `filesystem.multiedit` 需要 `multieditInput` 入口，只补一个 thin wrapper：

```go
func executeMultiEditInput(ctx context.Context, params multieditInput, tracker *ReadTracker, logger *zap.Logger) (*mcphost.ToolResult, error)
```

- [x] **Step 1.3: 保持旧工具 byte-level 行为尽量不变**

旧工具的成功输出和主要错误文案不能因为抽函数被无意改变。特别注意：

- `read_file` 图片/PDF data URI 行为不变。
- `read_file` binary detection 行为不变。
- `write_file` 对已存在文件仍要求 read-before-write。
- `edit` 仍要求 read-before-edit。
- `edit` 仍检查 `FileTracker.HasChanged`。
- `multiedit` 仍是 all-or-rollback。
- `ls` 默认 path 仍是 `"."`。
- `filesystem` 不能复制一份绕过旧工具安全约束的新实现；必须复用这些 executor 或复用相同 helper。
- `filesystem` 的写入 action 必须继续触发 `globalFileLock`、`ReadTracker.CheckRead`、`globalFileTracker.HasChanged/Track`、LSP diagnostics。
- `filesystem` 的只读 action 必须继续触发原有路径解析、二进制检测、PDF/图片 data URI、输出截断。

- [x] **Step 1.4: executor 等价性反例测试**

新增旧工具行为锁定测试，防止抽函数时改变语义：

```go
func TestFileExecutorWriteExistingRequiresRead(t *testing.T) {
	tracker := NewReadTracker(5 * time.Minute)
	path := filepath.Join(t.TempDir(), "a.txt")
	require.NoError(t, os.WriteFile(path, []byte("old"), 0o600))

	res, err := executeWriteFile(context.Background(), writeFileInput{Path: path, Content: "new"}, tracker)
	require.NoError(t, err)
	require.True(t, res.IsError)
	require.Contains(t, res.DecodeContent(), "has not been read")
}
```

```go
func TestFileExecutorEditRejectsExternalModification(t *testing.T) {
	tracker := NewReadTracker(5 * time.Minute)
	path := filepath.Join(t.TempDir(), "a.txt")
	require.NoError(t, os.WriteFile(path, []byte("old"), 0o600))
	tracker.RecordRead(path)
	require.NoError(t, globalFileTracker.Track(path))
	require.NoError(t, os.WriteFile(path, []byte("changed elsewhere"), 0o600))

	res, err := executeEdit(context.Background(), editInput{Path: path, OldString: "changed", NewString: "updated"}, tracker, zap.NewNop())
	require.NoError(t, err)
	require.True(t, res.IsError)
	require.Contains(t, res.DecodeContent(), "外部修改")
}
```

如果 executor 当前返回 `(string, error)` 而不是 `*mcphost.ToolResult`，测试应断言错误 code / 文案等价；不要为了迁就测试改变用户可见输出。

- [x] **Step 1.5: 运行旧工具测试**

Run:

```bash
go test ./internal/tools -run 'Test(Read|Write|Edit|MultiEdit|LS|Glob|Grep)' -v
```

Expected:

```text
PASS
```

如果正则未覆盖现有测试名，则运行：

```bash
go test ./internal/tools -v
```

Expected:

```text
PASS
```

### Task 2: 新增 `filesystem` 工具

**Files:**

- Create: `internal/tools/filesystem.go`
- Test: `internal/tools/filesystem_test.go`
- Modify: `internal/tools/tools.go`
- Modify: `internal/config/config.go`
- Modify: `internal/config/defaults.go`
- Modify: `internal/config/config_test.go`
- Optional Modify: `config.example.json`

- [x] **Step 2.1: 增加 feature flag**

在 `internal/config/config.go` 的 `ToolsConfig` 增加指针布尔配置，避免 JSON 省略字段和显式 `false` 无法区分：

```go
type ToolsConfig struct {
	CreateRequiresApproval bool     `json:"create_requires_approval"`
	AllowedDomains         []string `json:"allowed_domains,omitempty"`
	FilesystemEnabled      *bool    `json:"filesystem_enabled,omitempty"`
}

func (t ToolsConfig) IsFilesystemEnabled() bool {
	return t.FilesystemEnabled == nil || *t.FilesystemEnabled
}
```

在 `internal/config/defaults.go` 增加 helper：

```go
func boolPtr(v bool) *bool {
	return &v
}
```

并把 `Default()` 中的配置改为：

```go
c.Tools = ToolsConfig{
	CreateRequiresApproval: true,
	FilesystemEnabled:      boolPtr(true),
}
```

如果担心通用 `boolPtr` 命名冲突，可以命名为 `defaultBoolPtr`。

- [x] **Step 2.2: feature flag 测试**

在 `internal/config/config_test.go` 增加：

```go
func TestToolsFilesystemEnabledDefault(t *testing.T) {
	cfg := Default()
	if !cfg.Tools.IsFilesystemEnabled() {
		t.Fatalf("filesystem should be enabled by default")
	}
}

func TestToolsFilesystemEnabledExplicitFalse(t *testing.T) {
	var cfg Config
	if err := json.Unmarshal([]byte(`{"tools":{"filesystem_enabled":false}}`), &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Tools.IsFilesystemEnabled() {
		t.Fatalf("explicit false should disable filesystem")
	}
}
```

Run:

```bash
go test ./internal/config -run 'TestToolsFilesystemEnabled' -v
```

Expected:

```text
PASS
```

- [x] **Step 2.3: 创建输入结构**

在 `internal/tools/filesystem.go` 定义：

```go
type filesystemInput struct {
	Action          string           `json:"action"`
	Path            string           `json:"path,omitempty"`
	Recursive       bool             `json:"recursive,omitempty"`
	MaxDepth        int              `json:"max_depth,omitempty"`
	Pattern         string           `json:"pattern,omitempty"`
	Glob            string           `json:"glob,omitempty"`
	TypeFilter      string           `json:"type,omitempty"`
	Context         int              `json:"context,omitempty"`
	Before          int              `json:"before,omitempty"`
	After           int              `json:"after,omitempty"`
	MaxResults      int              `json:"max_results,omitempty"`
	Multiline       bool             `json:"multiline,omitempty"`
	Offset          int              `json:"offset,omitempty"`
	Limit           int              `json:"limit,omitempty"`
	ShowLineNumbers bool             `json:"show_line_numbers,omitempty"`
	Content         string           `json:"content,omitempty"`
	OldString       string           `json:"old_string,omitempty"`
	NewString       string           `json:"new_string,omitempty"`
	ReplaceAll      bool             `json:"replace_all,omitempty"`
	Edits           []filesystemEdit `json:"edits,omitempty"`
}

type filesystemEdit struct {
	Path       string `json:"path"`
	OldString  string `json:"old_string"`
	NewString  string `json:"new_string"`
	ReplaceAll bool   `json:"replace_all,omitempty"`
}
```

- [x] **Step 2.4: 实现 action 分发**

实现：

```go
func registerFilesystem(host *mcphost.Host, logger *zap.Logger, tracker *ReadTracker) {
	// schema 定义见本计划第 4 节
	host.RegisterTool(mcphost.ToolDefinition{
		Name:        "filesystem",
		Description: "结构化文件系统工具。通过 action 执行目录列表、文件匹配、内容搜索、文件读取、文件写入、精确编辑和多文件原子编辑。用于常规文件系统读写；需要运行测试、构建、git 或任意 shell 命令时使用 bash。",
		InputSchema: schema,
		Core:        true,
		IsConcurrencySafe: false,
	}, func(ctx context.Context, input json.RawMessage) (*mcphost.ToolResult, error) {
		var params filesystemInput
		if err := json.Unmarshal(input, &params); err != nil {
			return errorResult("filesystem: 输入无效: " + err.Error()), nil
		}
		return executeFilesystem(ctx, params, tracker, logger)
	})
}
```

`executeFilesystem` 必须按 action 调用 Task 1 抽出的 executor。

`filesystem.go` 的职责只允许包含：

- tool definition schema。
- action 参数强校验。
- `filesystemInput` 到旧工具 input / edit operation 的 mapper。
- 调用旧 executor / helper。
- 对外错误脱敏包装。
- action 级低基数字段指标。

`filesystem.go` 不允许包含目录遍历、grep 搜索、文件读写、字符串替换、多编辑回滚、文件锁、read tracker、file tracker 或诊断调度的独立实现。

`filesystem` 不能设置 `IsConcurrencySafe: true`。原因是 `StreamingExecutor` 和 `executeToolsConcurrent` 只按工具级 `IsConcurrencySafe` 决定是否并发执行，无法在调度前理解 `action`。如果把整个 `filesystem` 标成 concurrency-safe，`filesystem.write/edit/multiedit` 会和其他 unsafe 写入并发，绕过现有 unsafe 串行队列。v1 的正确选择是工具级 unsafe，由文件锁保证同文件串行；跨文件并发优化放到 v2 评估。

旧只读工具 `read_file` / `grep` / `glob` / `ls` 在 v1 必须继续注册并保留 `IsConcurrencySafe=true`。原因是 batch 并发读仍依赖这些工具级并发标志；除非后续升级 `mcphost.ToolDefinition` 支持 `ConcurrencySafeActions []string` 或等价能力，否则不得把旧只读工具彻底下线。

- [x] **Step 2.4.1: filesystem 错误归一和脱敏**

新增 filesystem 专属错误构造 helper，不直接把 `err.Error()` 拼进返回内容：

```go
type filesystemErrorCode string

const (
	filesystemErrInvalidInput     filesystemErrorCode = "invalid_input"
	filesystemErrMissingField     filesystemErrorCode = "missing_field"
	filesystemErrUnknownAction    filesystemErrorCode = "unknown_action"
	filesystemErrFileTooLarge     filesystemErrorCode = "file_too_large"
	filesystemErrPermissionDenied filesystemErrorCode = "permission_denied"
	filesystemErrReadRequired     filesystemErrorCode = "read_required"
	filesystemErrExternalChange   filesystemErrorCode = "external_change"
	filesystemErrIO               filesystemErrorCode = "io_error"
)

func filesystemErrorResult(action string, code filesystemErrorCode, message string) *mcphost.ToolResult {
	// message 只能包含 action、错误 code、basename 或 path_hash，不包含绝对路径、content、old_string、new_string。
	return errorResult(fmt.Sprintf("filesystem.%s: %s: %s", action, code, message))
}
```

要求：

- 缺字段、未知 action、JSON 解析失败都走 code 化错误。
- 从旧 executor 返回的错误如含绝对路径，filesystem wrapper 要改写成脱敏错误；完整错误只进 debug log，且 log 字段也不能含 content。
- `filesystem.read` 必须继承旧 `read_file` 的文件大小限制，不能因为抽 executor 绕过限制。
- `filesystem.write` 的输入内容大小限制沿用旧 `write_file`；如旧工具没有明确限制，本计划只新增告警和指标，不在 v1 引入破坏性限制。

- [x] **Step 2.5: action 参数强校验**

新增 helper：

```go
func requireFilesystemField(action, name, value string) error {
	if strings.TrimSpace(value) == "" {
		return errs.New(errs.CodeInvalidInput, fmt.Sprintf("filesystem.%s: 缺少 %s", action, name))
	}
	return nil
}
```

校验规则：

- `list`: path 为空时使用 `"."`。
- `glob`: `pattern` 必填。
- `grep`: `pattern` 必填。
- `read`: `path` 必填。
- `write`: `path` 必填，`content` 允许空字符串，因为创建空文件是合法动作。
- `edit`: `path` / `old_string` / `new_string` 必填，且 `old_string != new_string` 的既有校验仍由 executor 执行。
- `multiedit`: `edits` 非空。

- [x] **Step 2.6: 注册顺序和回滚开关**

在 `RegisterBuiltinTools` 中先按 feature flag 注册 `filesystem`，再注册旧工具：

```go
if cfg == nil || cfg.Tools.IsFilesystemEnabled() {
	registerFilesystem(host, logger, globalReadTracker)
}
registerReadFile(host, logger, globalReadTracker)
registerWriteFile(host, logger, globalReadTracker)
registerGlob(host, logger)
registerGrep(host, logger)
registerBash(host, logger, globalShellPool)
registerEdit(host, logger, globalReadTracker)
registerLS(host, logger)
registerMultiEdit(host, logger, globalReadTracker)
```

第一阶段旧工具仍 `Core: true`，避免一次性改变模型行为。

- [x] **Step 2.7: 工具层测试**

在 `internal/tools/filesystem_test.go` 覆盖：

- `filesystem.list` 输出与 `ls` 等价。
- `filesystem.glob` 能找到匹配文件。
- `filesystem.grep` 能返回 `file:line:content`。
- `filesystem.read` 会记录 `ReadTracker`，随后 `filesystem.edit` 成功。
- 旧 `read_file` 后 `filesystem.edit` 成功。
- `filesystem.read` 后旧 `edit` 成功。
- `filesystem.write` 写新文件不要求 read-before-write。
- `filesystem.write` 覆盖旧文件要求 read-before-write。
- `filesystem.edit` 检测外部修改并拒绝。
- `filesystem.multiedit` 失败回滚。
- 未知 action 返回 error result。
- 缺必填字段返回包含 `filesystem.<action>` 的错误。
- `filesystem.read` 对超大文件返回与 `read_file` 同等限制错误，不能成功读取。
- `filesystem` 错误和指标不暴露绝对路径、content、old_string、new_string。
- `cfg.Tools.FilesystemEnabled=false` 时不注册 `filesystem`，旧 7 个文件工具仍注册。
- `filesystem` 的 tool definition `IsConcurrencySafe == false`。
- 旧 `read_file` / `grep` / `glob` 的 tool definition 仍 `IsConcurrencySafe == true`。

Run:

```bash
go test ./internal/tools -run 'TestFilesystem' -v
go test ./internal/tools -run 'TestRegisterBuiltinTools.*Filesystem' -v
```

Expected:

```text
PASS
```

### Task 3: 路由和权限画像接入

**Files:**

- Modify: `internal/router/capability_registry.go`
- Modify: `internal/router/capability_gate_test.go`
- Modify: `internal/router/types_test.go`
- Modify: `internal/router/tool_policy_test.go`
- Modify: `internal/router/policy_consistency_test.go`
- Modify: `internal/master/react_processor.go`

- [x] **Step 3.1: builtin rule 增加 filesystem**

在 `builtinToolRules` 增加：

```go
"filesystem": {Domain: "filesystem", Invocation: InvocationDirectTool, Risk: RiskLocalWrite, SideEffect: true},
```

它必须标成 mixed local-write profile，而不是 read-only。具体只读动作由 action-level policy 降级。

- [x] **Step 3.2: 清理 `multi_edit` 策略兼容名**

在 `internal/router/capability_registry.go` 中：

- 从 `builtinToolRules` 移除 `"multi_edit"`，保留 `"multiedit"`。
- 从 `hostToolGroups["fs"]` 移除 `"multi_edit"`。
- 不在任何新 schema、prompt、DB 默认规则、WebUI 默认规则里继续出现 `multi_edit`。

在 `internal/master/react_processor.go` 中：

- `changedFilesFromToolCall` 可以暂时保留 `case "multiedit", "multi_edit"`，用于读取历史 journal。
- 新的 `filesystem.multiedit` 路径必须能解析 `edits[].path`，否则文件变更 journal 会漏记。

目标测试：

```go
func TestFilesystemToolNamesDoNotExposeMultiEditAlias(t *testing.T) {
	if _, ok := builtinToolRules["multi_edit"]; ok {
		t.Fatalf("multi_edit should not be a builtin rule")
	}
	if contains(hostToolGroups["fs"], "multi_edit") {
		t.Fatalf("fs group should expose multiedit, not multi_edit")
	}
}
```

- [x] **Step 3.3: mixed action rules 增加 filesystem**

在 `mixedActionRules` 增加：

```go
"filesystem": {
	Field:             "action",
	ReadOnlyActions:   []string{"list", "glob", "grep", "read"},
	LocalWriteActions: []string{"write", "edit", "multiedit"},
},
```

第一版不要加入 destructive 或 privileged action。

- [x] **Step 3.4: host tool set 和 profile 接入**

更新：

- `HostToolSetDefaultVisible`: 增加 `filesystem`。
- `HostToolSetPlanAllowed`: 增加 `filesystem`。
- `hostToolGroups["fs"]`: 增加 `filesystem`。
- `hostToolPolicyProfiles["readonly"]`: 增加 `filesystem`。

注意：Plan mode 允许 `filesystem` 这个工具名，但必须依赖 `AllowedToolInputs` 把 action 限制在 `list|glob|grep|read`。

- [x] **Step 3.5: MixedAllowedToolInputsForIntent 确认**

现有 `MixedAllowedToolInputsForIntent` 应自动根据 `mixedActionRules` 返回：

- read / answer / plan intent: `action=list|glob|grep|read`
- local write intent: `action=list|glob|grep|read|write|edit|multiedit`

如现有实现没有包含 read actions + local write actions 的合并，需要补齐。

- [x] **Step 3.6: 路由测试**

新增或修改测试断言：

```go
func TestFilesystemMixedActionPolicy(t *testing.T) {
	profile := InferToolProfile(mcphost.ToolDefinition{Name: "filesystem", Core: true}, ProfileHint{})

	readDecision := EvaluateToolPolicy(profile, ToolPolicyContext{
		Intent: IntentFrame{Kind: IntentRead},
		Input:  json.RawMessage(`{"action":"read","path":"README.md"}`),
		ForAction: true,
	})
	if readDecision.Action != ToolPolicyAllow || readDecision.RiskClass != ToolRiskReadOnly {
		t.Fatalf("read decision = %+v, want allow read_only", readDecision)
	}

	writeDecision := EvaluateToolPolicy(profile, ToolPolicyContext{
		Intent: IntentFrame{Kind: IntentRead},
		Input:  json.RawMessage(`{"action":"edit","path":"README.md","old_string":"a","new_string":"b"}`),
		ForAction: true,
	})
	if writeDecision.Action != ToolPolicyDeny {
		t.Fatalf("edit under read intent = %+v, want deny", writeDecision)
	}
}
```

还要覆盖：

- unknown action -> deny / unknown reason。
- `IntentWriteLocal` + `action=edit` -> allow 或 ask 语义与现有 local-write 一致。
- `BuildRouteDecision` 对 read intent 生成 `AllowedToolInputs["filesystem"]["action"] == "list|glob|grep|read"`。
- `BuildRouteDecision` 对 local write intent 生成包含写 action 的约束。

Run:

```bash
go test ./internal/router -run 'Test.*Filesystem|TestCapabilityRegistryMixedOperationToolsAreActionAware|TestPolicyConsistency' -v
```

Expected:

```text
PASS
```

- [x] **Step 3.7: HITL 权限规则不作为 action 级安全边界**

现有 `skills.PermissionRule.pattern` 通过 `extractInputValue()` 提取单个输入值后做 glob 匹配；它不是 JSON action policy。`filesystem` 的 `action` 安全边界必须由 router / runtime gate / ActionGuard 负责，不能依赖类似 `{"tool_name":"filesystem","pattern":"edit","action":"ask"}` 这种规则。

实现约束：

- DB / config 默认权限可以有 `filesystem allow`，但它只能表示“工具名级默认允许进入统一策略链路”。
- `filesystem.edit/write/multiedit` 在 read intent、plan mode、allowed inputs 不匹配时，必须先被 route/runtime gate 拒绝。
- 如果后续要让 HITL 对 `filesystem.action` 做二次审批，必须新增显式 action-aware permission evaluator，不能复用当前 `Pattern` 字段做隐式授权。

新增测试：

```go
func TestFilesystemPermissionRulesCannotOverrideRouteInputDeny(t *testing.T) {
	decision := toolruntime.DecideExecution(
		toolruntime.Descriptor{
			Definition: mcphost.ToolDefinition{Name: "filesystem", Core: true},
			Profile:    mustBuiltinToolProfileForTest(t, "filesystem"),
		},
		toolruntime.Invocation{
			Name:      "filesystem",
			Arguments: json.RawMessage(`{"action":"edit","path":"README.md","old_string":"a","new_string":"b"}`),
			Route: router.RouteDecision{
				AllowedToolInputs: map[string]map[string]string{
					"filesystem": {"action": "list|glob|grep|read"},
				},
			},
		},
	)
	if decision.Action != toolruntime.ExecutionActionDeny || decision.Reason != "route_input_denied" {
		t.Fatalf("decision = %+v, want route_input_denied", decision)
	}
}
```

Run:

```bash
go test ./internal/toolruntime ./internal/skills -run 'TestFilesystem.*Permission|Test.*RouteInput' -v
```

Expected:

```text
PASS
```

### Task 4: 模型可见性和运行时输入约束

**Files:**

- Modify: `internal/master/tool_visibility.go`
- Modify: `internal/master/tool_visibility_test.go`
- Modify: `internal/master/middleware_tool_test.go`
- Modify: `internal/master/action_guard_runtime_test.go`
- Modify: `internal/toolruntime/types_test.go`

- [x] **Step 4.1: fast-path 默认可见加入 filesystem**

更新 `isFastPathDefaultVisibleTool`：

```go
case "filesystem", "memory", "question", "skill", "tool_search":
	return true
```

是否保留旧 `ls/read_file/grep/glob` 在 fast-path 默认可见里，按迁移阶段处理：

- Phase 1: 保留旧工具，新增 `filesystem`。
- Phase 2: 指标稳定后，把旧工具从 fast-path 默认可见移除，但仍可通过 recall 或 explicit route 出现。

本计划第一轮只做 Phase 1。

- [x] **Step 4.2: allowed inputs merge 覆盖 filesystem**

`mergeAllowedToolInputsWithMixedReadDefaults` 应能自动识别 `filesystem` 的 mixed read actions。如果当前实现只处理部分工具，补测试后修正。

目标：

```go
session.AllowedToolInputsSnapshot()["filesystem"]["action"] == "list|glob|grep|read"
```

在只读 turn 中成立。

- [x] **Step 4.3: runtime route input gate 覆盖 filesystem**

确保 `routeInputDenyReason` 对 `action` 字段生效。测试：

- session allowed inputs: `filesystem.action=list|glob|grep|read`
- 调用 `filesystem {"action":"read"}` -> allow
- 调用 `filesystem {"action":"edit"}` -> deny `route_input_denied`
- deny 必须生成 recoverable tool result，提示模型按 `allowed_inputs` 重构参数。
- deny 必须写入 `ToolCallEvent`、`quality.tool_decision`，并记录 `actual_inputs` / `allowed_inputs`。
- deny 必须新增或更新 `hive_route_input_denied_total{tool="filesystem",field="action",action="edit"}`。

- [x] **Step 4.4: Plan mode 行为测试**

新增测试：

- Plan mode 下 `filesystem.read` 允许。
- Plan mode 下 `filesystem.edit` 被 runtime input gate 拒绝。
- Plan mode 下旧 `edit` 仍被工具名 gate 拒绝。
- Plan mode 下 `filesystem.edit` 的拒绝不应走 HITL，不应产生 action_guard ask。

- [x] **Step 4.5: ActionGuard 与 route input gate 顺序测试**

现有主路径是先 `enforceToolExecutionGate`，再 ActionGuard / HITL。必须锁定顺序：

```go
func TestFilesystemRouteInputDenyHappensBeforeActionGuard(t *testing.T) {
	// 构造 session.AllowedToolInputs["filesystem"]["action"]="list|glob|grep|read"
	// 调用 filesystem.edit
	// 断言：
	// 1. 返回 recoverable route_input_outside_allowed_values
	// 2. 未触发 HITL request
	// 3. 未产生 action_guard_ask_total
}
```

这条测试的目的不是业务正确性，而是防止未来把 ActionGuard/HITL 提前，导致本应直接拒绝的 action 先弹审批。

Run:

```bash
go test ./internal/master ./internal/toolruntime -run 'Test.*Filesystem|Test.*Plan.*Tool|Test.*AllowedToolInputs|Test.*RouteInput' -v
```

Expected:

```text
PASS
```

### Task 5: 旧工具迁移和提示语收敛

**Files:**

- Modify: `internal/master/prompt_builder.go`
- Modify: `internal/master/prompt_builder_test.go`
- Modify: `internal/tools/tools.go`
- Modify: `internal/tools/tool_search_test.go`
- Optional Modify: `README.md`

- [x] **Step 5.1: system prompt 引导优先使用 filesystem**

在工具使用说明中加入短规则：

```text
For filesystem operations, prefer `filesystem` with a structured `action` over legacy `ls`, `glob`, `grep`, `read_file`, `write_file`, `edit`, or `multiedit`. Use `filesystem.action="edit"` for one precise replacement and `filesystem.action="multiedit"` for multiple precise replacements. Use `apply_patch` for hunk-level patches, generated diffs, or broad structural edits. Use `bash` for tests, builds, git, package managers, and commands not represented by filesystem actions.
```

如果系统 prompt 是中文主导，可加中文：

```text
文件系统操作优先使用 `filesystem.action`。单处精确替换使用 `filesystem.action="edit"`，多处精确替换使用 `filesystem.action="multiedit"`，大段补丁或 unified diff 使用 `apply_patch`。运行测试、构建、git、包管理和任意 shell 命令时使用 `bash`。
```

工具选择优先级必须固定为：

```text
1. 查找、搜索、读取、小范围精确替换：filesystem.action=list|glob|grep|read|edit
2. 多文件或多处精确替换：filesystem.action=multiedit
3. 大段 hunk-level 修改、generated diff、需要 patch 语义：apply_patch
4. 测试、构建、git、包管理、系统命令：bash
```

- [x] **Step 5.2: tool descriptions 调整**

旧工具 description 不要写“primary tool”，避免和 `filesystem` 冲突。迁移期可改为：

```text
Legacy direct file-read tool. Prefer filesystem.action="read" for new calls.
```

但第一轮不要降低旧工具功能。

- [x] **Step 5.3: tool_search 输出验证**

`tool_search` 搜“读文件”“grep”“编辑文件”时应该能返回 `filesystem`。如果召回只看工具名/描述/schema，确保 `filesystem` 描述包含这些关键词：

- list directory
- glob
- grep
- read file
- write file
- edit file
- multi edit
- 文件系统
- 搜索
- 读取
- 编辑

Run:

```bash
go test ./internal/tools -run 'TestToolSearch|TestFilesystem' -v
go test ./internal/master -run 'Test.*Prompt.*Filesystem|Test.*ToolVisibility.*Filesystem' -v
```

Expected:

```text
PASS
```

### Task 6: 观测、审计和质量回放

**Files:**

- Modify: `internal/master/tool_visibility.go`
- Modify: `internal/router/decision_span_test.go`
- Modify: `internal/agentquality/types.go`
- Modify: `internal/agentquality/route_decision.go`
- Modify: `internal/agentquality/route_decision_test.go`
- Modify: `internal/agentquality/testdata/route_eval/corpus.json`
- Modify: `cmd/agentquality/main_test.go`
- Optional Modify: `internal/observability/labels.go`

- [x] **Step 6.1: admission entries 显示 filesystem action 约束**

确保 `toolRecallObservation.Entries["filesystem"].AllowedInputs` 能展示当前 action 约束。

只读场景示例：

```json
{
  "filesystem": {
    "visible_to_model": true,
    "executable_by_runtime": true,
    "allowed_inputs": {
      "action": "list|glob|grep|read"
    }
  }
}
```

- [x] **Step 6.2: DecisionSpan 包含 filesystem**

新增 route decision span 测试：

- read intent: allowed tools 包含 `filesystem`。
- blocked entries 不应包含 `filesystem`。
- allowed inputs 包含 read actions。

- [x] **Step 6.3: agentquality 迁移期双轨期望**

在 `internal/agentquality/route_decision.go` 扩展 `RouteEvalCase`，让单个 case 能表达迁移期首选工具和兼容工具：

```go
type RouteEvalCase struct {
	ID                 string                       `json:"id"`
	Tags               []string                     `json:"tags,omitempty"`
	Intent             router.IntentFrame           `json:"intent"`
	Candidates         []router.ToolProfile         `json:"candidates"`
	WantMode           router.DecisionMode          `json:"want_mode,omitempty"`
	WantReason         string                       `json:"want_reason,omitempty"`
	WantAllowedTools   []string                     `json:"want_allowed_tools,omitempty"`
	WantPreferredTools []string                     `json:"want_preferred_tools,omitempty"`
	WantCompatTools    []string                     `json:"want_compat_tools,omitempty"`
	WantBlockedTools   []string                     `json:"want_blocked_tools,omitempty"`
	WantBlockedReason  map[string]string            `json:"want_blocked_reason,omitempty"`
	WantVisibleOnly    []string                     `json:"want_visible_only,omitempty"`
	WantAllowedInputs  map[string]map[string]string `json:"want_allowed_inputs,omitempty"`
}
```

失败判断规则：

```go
func allowedToolsSatisfyMigration(got []string, c RouteEvalCase) bool {
	if len(c.WantPreferredTools) == 0 && len(c.WantCompatTools) == 0 {
		return sameStringSet(got, c.WantAllowedTools)
	}
	for _, preferred := range c.WantPreferredTools {
		if stringSliceContains(got, preferred) {
			return true
		}
	}
	for _, compat := range c.WantCompatTools {
		if stringSliceContains(got, compat) {
			return true
		}
	}
	return false
}
```

`RouteEvalResult` 增加迁移标记，便于报告统计：

```go
type RouteEvalResult struct {
	CaseID               string
	Decision             router.RouteDecision
	Passed               bool
	FilesystemPreferred  bool
	FilesystemCompatUsed bool
	Failures             []string
}
```

在 `RunRouteEvalCases` 中：

```go
preferred := stringSliceContains(decision.AllowedTools, "filesystem")
compat := !preferred && intersects(decision.AllowedTools, c.WantCompatTools)
```

- [x] **Step 6.4: agentquality 回放样例**

新增或修改一个工具选择质量样例：

用户输入：

```text
帮我在仓库里找一下 RegisterBuiltinTools 在哪里定义
```

期望 tool decision:

```json
{
  "id": "filesystem_search_prefers_unified_tool",
  "tags": ["read-only", "filesystem-migration"],
  "intent": {
    "kind": "read",
    "subject": "Find where RegisterBuiltinTools is defined"
  },
  "candidates": [
    {
      "name": "filesystem",
      "kind": "builtin_tool",
      "domain": "filesystem",
      "source": "builtin",
      "invocation": "direct_tool",
      "risk": "local_write",
      "trust": "built_in",
      "side_effect": true
    },
    {
      "name": "grep",
      "kind": "builtin_tool",
      "domain": "filesystem",
      "source": "builtin",
      "invocation": "direct_tool",
      "risk": "read_only",
      "trust": "built_in",
      "read_only": true
    }
  ],
  "want_mode": "allow",
  "want_preferred_tools": ["filesystem"],
  "want_compat_tools": ["grep"],
  "want_allowed_inputs": {
    "filesystem": {
      "action": "list|glob|grep|read"
    }
  }
}
```

迁移期可以允许旧 `grep` 作为兼容，但要在质量报告中记录 `filesystem_preferred=true`。

Run:

```bash
go test ./internal/agentquality -run 'TestRouteDecision|TestRunRouteEval' -v
go test ./cmd/agentquality -v
```

Expected:

```text
PASS
```

- [x] **Step 6.5: 指标命名落地**

复用既有指标：

- `policy_decision_total`
- `action_guard_ask_total`
- `hive_plan_mode_gate_denied_total`

新增或确认存在以下 `hive_*_total` 指标，落代码前必须先 grep 现有 writer，避免重复命名：

```go
const (
	MetricToolCallTotal         = "hive_tool_call_total"
	MetricRouteInputDeniedTotal = "hive_route_input_denied_total"
	MetricFilesystemActionTotal = "hive_filesystem_action_total"
	MetricToolErrorTotal        = "hive_tool_error_total"
)
```

标签约束：

- `hive_tool_call_total{tool,action,status}`
- `hive_route_input_denied_total{tool,field,action}`
- `hive_filesystem_action_total{action,status}`
- `hive_tool_error_total{tool,action,reason}`

不要使用计划旧稿里的 `tool_call_total`、`route_input_denied_total`、`tool_error` 作为正式指标名。

不要把现有 `hive.tool.duration_ms` / `hive.tool.errors` 直接改名或删除。处理方式：

- 保留既有点号指标，避免破坏现有看板。
- 新增 `hive_*_total` counter，作为工具域统一迁移看板的数据源。
- 新 counter 不带 `session_id` / `user_id` / path / content。
- 如果要降低现有点号指标的高基数风险，另开 cleanup 任务，不夹在 filesystem 迁移里。

接入点要求：

- `hive_tool_call_total` 在实际 host tool 调用完成后打点，优先接在 `mcphost.Host.ExecuteTool` 或 `skills.ToolBridge` 的统一路径；不能只在 `filesystem` executor 内打，否则旧工具对比缺分母。
- `hive_filesystem_action_total` 在 `executeFilesystem` action 分发后打点，必须包含 `status=ok|error`。
- `hive_tool_error_total` 只统计 tool result `IsError=true` 或 executor error；缺字段、未知 action、read-before-edit 失败都要归入 error。
- `hive_route_input_denied_total` 在 `master.enforceToolExecutionGate` 的 allowed inputs 分支和 `toolruntime.DecideExecution` 的 `RouteInputDenyReason` 分支都要覆盖，避免主路径和 direct/toolbridge 路径观测不一致。
- 指标 label 不允许包含 path、pattern、old_string、new_string、content、session_id、user_id，防止高基数和敏感信息泄漏。

测试：

```go
func TestFilesystemMetricsDoNotExposePathOrContent(t *testing.T) {
	metric := filesystemActionMetric("edit", "error", json.RawMessage(`{"path":"secret.txt","old_string":"token","new_string":"x"}`))
	for key := range metric.Labels {
		if key == "path" || key == "content" || key == "old_string" || key == "new_string" {
			t.Fatalf("sensitive/high-cardinality label leaked: %s", key)
		}
	}
}
```

### Task 7: 配置、权限默认值和 WebUI 收敛

**Files:**

- Modify: `internal/config/defaults.go`
- Modify: `internal/config/config_test.go`
- Modify: `internal/store/postgres_migrate.go`
- Modify: `internal/store/postgres_migrate_test.go`
- Modify: `frontend/src/components/settings/PermissionRulesSettings.tsx`
- Modify: `frontend/src/i18n/locales/zh.json`
- Modify: `frontend/src/i18n/locales/en.json`
- Modify: `frontend/src/types/journal.ts`

- [x] **Step 7.1: config 默认权限增加 filesystem 并清理 multi_edit**

在 `internal/config/defaults.go` 的 `defaultPermissionRules()` 中：

```go
{ToolName: "filesystem", Action: skills.PermissionAllow},
{ToolName: "multiedit", Action: skills.PermissionAllow},
```

并移除：

```go
{ToolName: "multi_edit", Action: skills.PermissionAllow},
```

保留旧 7 个文件工具的默认规则，迁移期不能删除。

- [x] **Step 7.2: DB 默认权限增加 filesystem 并清理 multi_edit**

在 `internal/store/postgres_migrate.go`：

- `pgDefaultPermissionRulesJSON` 增加 `{"tool_name":"filesystem","action":"allow"}`。
- `pgLegacyPermissionRulesJSON` 不新增 `filesystem`，保持 legacy 精确升级语义；只有从 legacy 升到 default 时才得到新规则。
- `pgDefaultPermissionRulesJSON` 移除 `{"tool_name":"multi_edit","action":"allow"}`。
- `pgFixDefaultPermissionRules` 不得把 `multi_edit` 写回生产 DB。

在 `internal/store/postgres_migrate_test.go` 增加：

```go
func TestDefaultPermissionRulesIncludeFilesystemAndNoMultiEditAlias(t *testing.T) {
	if !strings.Contains(pgDefaultPermissionRulesJSON, `"tool_name":"filesystem"`) {
		t.Fatalf("pgDefaultPermissionRulesJSON missing filesystem: %s", pgDefaultPermissionRulesJSON)
	}
	if strings.Contains(pgDefaultPermissionRulesJSON, `"tool_name":"multi_edit"`) {
		t.Fatalf("pgDefaultPermissionRulesJSON should not contain multi_edit alias: %s", pgDefaultPermissionRulesJSON)
	}
	if strings.Contains(pgFixDefaultPermissionRules, `"tool_name":"multi_edit"`) {
		t.Fatalf("pgFixDefaultPermissionRules should not write multi_edit alias")
	}
}
```

Run:

```bash
go test ./internal/store -run 'Test.*PermissionRules' -v
```

Expected:

```text
PASS
```

- [x] **Step 7.3: WebUI 默认规则和工具展示文案**

在 `frontend/src/components/settings/PermissionRulesSettings.tsx` 的 `DEFAULT_RULES`：

```ts
{ tool_name: 'filesystem', action: 'allow' },
{ tool_name: 'multiedit', action: 'allow' },
```

并移除：

```ts
{ tool_name: 'multi_edit', action: 'allow' },
```

在 `frontend/src/i18n/locales/zh.json` 的 `tools` 增加：

```json
"filesystem": "文件系统",
"filesystem.actions.list": "列目录",
"filesystem.actions.glob": "按文件名查找",
"filesystem.actions.grep": "搜索内容",
"filesystem.actions.read": "读取文件",
"filesystem.actions.write": "写入文件",
"filesystem.actions.edit": "编辑文件",
"filesystem.actions.multiedit": "批量编辑"
```

在 `frontend/src/i18n/locales/en.json` 的 `tools` 增加：

```json
"filesystem": "Filesystem",
"filesystem.actions.list": "List Directory",
"filesystem.actions.glob": "Find Files",
"filesystem.actions.grep": "Search Content",
"filesystem.actions.read": "Read File",
"filesystem.actions.write": "Write File",
"filesystem.actions.edit": "Edit File",
"filesystem.actions.multiedit": "Batch Edit"
```

同时删除 `tools.multi_edit`，保留 `tools.multiedit`。

- [x] **Step 7.4: journal 工具分类支持 filesystem**

在 `frontend/src/types/journal.ts`：

- `writeTools` 保留 `multiedit`，移除 `multi_edit`。
- 新增 `filesystemAction(event)`，基于 `event.arguments` 解析 action。
- `filesystem.action=list|glob|grep|read` 返回 `reading`。
- `filesystem.action=write|edit|multiedit` 返回 `coding`。
- `filesystem` 参数 JSON 解析失败时返回 `running`，不要误判为 reading。

建议代码：

```ts
function filesystemAction(event: JournalEvent): string | undefined {
  if (event.tool_name !== 'filesystem' || !event.arguments) return undefined;
  try {
    const parsed = JSON.parse(event.arguments) as { action?: unknown };
    return typeof parsed.action === 'string' ? parsed.action : undefined;
  } catch {
    return undefined;
  }
}
```

在 `getCharacterState` 的 tool 判断前加入：

```ts
if (tool === 'filesystem') {
  const action = filesystemAction(event);
  if (['list', 'glob', 'grep', 'read'].includes(action ?? '')) return 'reading';
  if (['write', 'edit', 'multiedit'].includes(action ?? '')) return 'coding';
  return 'running';
}
```

新增测试文件：

- Create: `frontend/src/types/journal.test.ts`

覆盖：

```ts
it('classifies filesystem read actions as reading', () => {
  expect(getCharacterState({
    type: 'tool_call',
    timestamp: '2026-05-15T00:00:00Z',
    tool_name: 'filesystem',
    arguments: JSON.stringify({ action: 'grep', pattern: 'RegisterBuiltinTools' }),
  })).toBe('reading');
});

it('classifies filesystem write actions as coding', () => {
  expect(getCharacterState({
    type: 'tool_call',
    timestamp: '2026-05-15T00:00:00Z',
    tool_name: 'filesystem',
    arguments: JSON.stringify({ action: 'edit', path: 'README.md' }),
  })).toBe('coding');
});

it('does not classify malformed filesystem arguments as reading', () => {
  expect(getCharacterState({
    type: 'tool_call',
    timestamp: '2026-05-15T00:00:00Z',
    tool_name: 'filesystem',
    arguments: '{bad json',
  })).toBe('running');
});
```

Run:

```bash
npm run lint
npm run build
```

Working directory:

```bash
frontend
```

Expected:

```text
PASS
```

### Task 8: 分阶段隐藏旧工具

**Files:**

- Modify: `internal/master/tool_visibility.go`
- Modify: `internal/router/capability_registry.go`
- Modify: `internal/master/tool_visibility_test.go`
- Modify: `docs/计划与路线/归档/2026-05-15-filesystem统一工具实施计划.md`

- [x] **Step 8.1: Phase 1 保留旧工具默认可见**

上线第一阶段：

- `filesystem` Core true。
- 旧工具 Core true。
- fast-path 同时保留 `filesystem` 和旧只读文件工具。

观察至少 7 天：

- `hive_tool_call_total{tool="filesystem"}` 是否上升。
- `hive_tool_call_total{tool="read_file|grep|glob|ls|edit|write_file|multiedit"}` 是否下降。
- `hive_route_input_denied_total{tool="filesystem"}` 是否异常高。
- `hive_tool_error_total{tool="filesystem", reason="missing_field|unknown_action"}` 是否异常高。

- [ ] **Step 8.2: OBSERVING - Phase 2 旧工具从默认可见降级**

满足 Phase 1 指标后：

- 旧工具仍注册、仍可执行。
- `filesystem` 保持默认可见。
- 旧 `ls` / `glob` / `grep` / `read_file` 从 fast-path 默认可见中移除，但可通过 recall/tool_search 出现。
- 写入旧工具不默认可见，只在明确召回或兼容路径出现。

- [ ] **Step 8.3: OBSERVING - Phase 3 决定是否移除旧工具 Core**

至少再观察 14 天后决定：

- 若 skill、自定义工具、sub-agent 没有依赖旧工具名，则旧工具 `Core=false`。
- 若仍有依赖，继续保留，但在 description 标记 legacy。

不要在没有指标前删除旧工具。

### Task 9: 兼容路径和历史数据保护

**Files:**

- Modify: `internal/master/react_processor.go`
- Modify: `frontend/src/types/journal.ts`
- Modify: `internal/subagent/explore/agent_test.go`
- Modify: `internal/router/capability_registry.go`
- Modify: `internal/config/defaults.go`
- Modify: `frontend/src/components/settings/PermissionRulesSettings.tsx`

- [x] **Step 9.1: 历史 journal/replay 继续识别 multi_edit**

`multi_edit` 不再作为新工具名暴露，但历史 journal、旧 replay、旧 session message 可能仍包含该名称。

保留兼容的位置：

- `changedFilesFromToolCall` 可继续 `case "multiedit", "multi_edit"`。
- replay / journal 解析可以继续把 `multi_edit` 显示为批量编辑。

移除的位置：

- `builtinToolRules`
- `hostToolGroups["fs"]`
- `defaultPermissionRules`
- `pgDefaultPermissionRulesJSON`
- `PermissionRulesSettings.DEFAULT_RULES`
- 新 prompt / schema / docs 示例

- [x] **Step 9.2: subagent 禁写列表改用 multiedit**

`internal/subagent/explore/agent_test.go` 当前可能仍把 `multi_edit` 当禁止工具名。迁移后要同时覆盖：

```go
forbiddenTools := []string{"write_file", "edit", "multiedit"}
```

如果需要历史兼容，可在测试里额外断言 `multi_edit` 不再出现在实际工具白名单中。

- [ ] **Step 9.3: OBSERVING - 旧工具删除前的硬门**

在 Phase 3 之前不得删除旧工具注册函数。旧工具从默认可见降级前必须满足：

- 最近 14 天 `hive_tool_call_total{tool="filesystem"}` 稳定。
- 最近 14 天旧文件工具调用下降，但不是 0 也可以，只要来源是历史 replay 或 explicit recall。
- `unknown_tool` 中没有 `read_file|grep|glob|ls|edit|write_file|multiedit`。
- skill / subagent 测试没有直接依赖被删除的旧工具名。

如果任一条件不满足，只能继续保留旧工具并标记 legacy。

## 6. 回滚方案

生产最小回滚优先使用配置开关，不做代码回滚：

```json
{
  "tools": {
    "filesystem_enabled": false
  }
}
```

配置关闭后的期望行为：

1. `RegisterBuiltinTools` 不注册 `filesystem`。
2. 旧 7 个文件工具仍完整可用。
3. router / prompt / DB / i18n 中保留 `filesystem` 配置不影响运行，因为工具不存在时不会进入可调用列表。
4. `hive_tool_call_total{tool="filesystem"}` 下降为 0。

代码回滚只在配置开关不能止血时使用，顺序如下：

1. 回滚 `registerFilesystem` 和 `internal/tools/filesystem.go`。
2. 回滚 `HostToolSetDefaultVisible` / `PlanAllowed` / `hostToolGroups` / `readonly` profile 的 `filesystem`。
3. 回滚 `mixedActionRules` 和 `builtinToolRules` 的 `filesystem`。
4. 保留 Task 1 抽出的 executor，因为旧工具仍使用它们；如抽取引发问题，再回滚对应 refactor。

上线时必须保证旧工具仍完整可用，因此回滚不需要数据迁移。`pgDefaultPermissionRulesJSON` 可以保留 `filesystem` 规则，WebUI i18n 也可以保留；但 `multi_edit` 清理不应随 filesystem 回滚恢复。

## 7. 风险和缓解

| 风险 | 影响 | 缓解 |
|---|---|---|
| schema 太大导致模型选错字段 | 工具调用失败 | flat schema + action 强校验 + 明确错误文案 |
| `filesystem` 标成 local-write 导致只读场景不可用 | 模型不能读文件 | mixed action rule + read action policy 测试 |
| Plan mode 中 `filesystem.edit` 绕过工具名 allow-list | 计划模式被写文件破坏 | allowed input gate 必须测 `action=edit` deny |
| 新旧 read tracker 不共享 | 读后编辑失败 | `filesystem.read` 和旧 `read_file` 共用 `globalReadTracker` |
| 旧工具隐藏过早 | skill/sub-agent 失败 | 三阶段迁移，先新增不删除 |
| 输出格式变化破坏模型习惯 | 质量回退 | 底层复用旧 executor，输出保持一致 |
| tool_search 召回旧工具不召回 filesystem | 模型继续用旧工具 | description/schema 加关键词，质量样例锁定 |
| action 约束没有进入 runtime | 只读意图下可写 | `toolruntime.RouteInputDenyReason` 和 master middleware 测试 |
| feature flag 使用普通 bool 导致显式 false 被默认值覆盖 | 生产无法快速关闭 `filesystem` | `*bool` + `IsFilesystemEnabled()` 测试 |
| DB / WebUI 默认规则继续写入 `multi_edit` | 命名分裂继续扩散 | store/config/frontend 三处同时移除 `multi_edit` 默认项 |
| agentquality 直接硬切期望到 `filesystem` | 迁移期质量门大量假失败 | `want_preferred_tools` + `want_compat_tools` 双轨 |
| 指标名沿用旧计划的非真实名称 | 看板查不到数据 | 只使用 `policy_decision_total`、`action_guard_ask_total`、`hive_*_total` |
| 把 `filesystem` 标成 `IsConcurrencySafe=true` | 写入 action 与其他 unsafe 工具并发，破坏文件锁外层调度 | v1 工具级 unsafe，靠 action policy 和文件锁保证安全 |
| HITL pattern 被误当 action 授权 | `filesystem allow` 被误解为允许所有写入 | 文档和测试锁定：action 安全边界在 router/runtime gate |
| route input deny 缺审计 | Plan mode / read intent 拦截后无法定位原因 | ToolCallEvent + quality event + `hive_route_input_denied_total` |
| 前端只按工具名分类 `filesystem` | 写入显示成 reading 或读取显示成 coding | 解析 `arguments.action` 分类 |
| 指标 label 带 path/content | 高基数、泄漏代码内容 | 指标 label 白名单只含 tool/action/status/reason/field |

## 8. 验收标准

功能验收：

- [x] `filesystem.list` / `glob` / `grep` / `read` / `write` / `edit` / `multiedit` 全部可用。
- [x] 旧 7 个工具仍可用。
- [x] 新旧读入口共享 read-before-edit 状态。
- [x] 只读 intent 下 `filesystem.edit` 被拒绝。
- [x] local-write intent 下 `filesystem.edit` 可用。
- [x] Plan mode 下 `filesystem.read` 可用，`filesystem.edit` 不可用。
- [x] `tools.filesystem_enabled=false` 时 `filesystem` 不注册，旧工具仍可用。
- [x] `multiedit` 是唯一新批量编辑名称；新 schema / DB 默认规则 / WebUI 默认规则不再出现 `multi_edit`。
- [x] DB 默认权限包含 `filesystem`。
- [x] WebUI 有 `filesystem` 和 action 级中英文文案。
- [x] `filesystem` tool definition 不设置 `IsConcurrencySafe=true`。
- [x] `filesystem.write/edit/multiedit` 复用旧写入 executor 的文件锁、read-before-write/edit、FileTracker 和 LSP diagnostics。
- [x] HITL `PermissionRule.pattern` 不承担 `filesystem.action` 级授权。
- [x] `filesystem.edit` 在 Plan mode / read intent 下被 route input gate 拒绝，且不触发 HITL / ActionGuard ask。
- [x] route input deny 有 ToolCallEvent、quality event 和 `hive_route_input_denied_total`。
- [x] 前端 replay / journal 能按 `filesystem.arguments.action` 区分 reading/coding。

补充验收：`filesystem.list/glob/grep/read` 已与 legacy `ls/glob/grep/read_file` 做输出等价回归；`filesystem action audit` 专项日志不携带 `session_id/user_id/path/content/old_string/new_string/raw args`。

测试验收：

```bash
go test ./internal/config -v
go test ./internal/tools -v
go test ./internal/router -v
go test ./internal/master -run 'Test.*Filesystem|Test.*ToolVisibility|Test.*Plan.*Tool|Test.*Middleware' -v
go test ./internal/toolruntime -v
go test ./internal/skills -v
go test ./internal/store -v
go test ./internal/agentquality -v
go test ./cmd/agentquality -v
```

如果修改了 `frontend/src`：

```bash
cd frontend
npm test -- journal
npm run lint
npm run build
```

最终合并前：

```bash
go test ./... -v
```

最终验收已执行 `go test ./... -count=1`、`cd frontend && npm test -- journal`、`cd frontend && npm run lint`、`cd frontend && npm run build`，结果均通过。

质量验收：

- 新增 agentquality 用例能识别文件搜索场景优先走 `filesystem.action=grep`。
- 工具目录 / admission diagnostics 能显示 `filesystem` 的 `allowed_inputs.action`。
- 没有新增 `unknown_tool`、`hive_route_input_denied_total` 异常尖峰。
- `policy_decision_total` 和 `action_guard_ask_total` 仍有连续数据。
- `hive_tool_call_total`、`hive_filesystem_action_total`、`hive_tool_error_total` 不包含 path/content/old_string/new_string label。

## 9. 推荐提交拆分

1. `refactor(tools): extract reusable filesystem executors`
2. `feat(config): add filesystem feature flag`
3. `feat(tools): add filesystem action tool`
4. `feat(router): classify filesystem actions by risk`
5. `fix(master): gate filesystem actions by route inputs`
6. `fix(config): remove multi_edit default alias`
7. `fix(store): add filesystem permission defaults`
8. `fix(i18n): add filesystem tool labels`
9. `test(agentquality): accept filesystem during migration`
10. `fix(replay): classify filesystem journal actions`
11. `test(master): audit filesystem route input denies`
12. `docs(tools): prefer filesystem for structured file operations`

每个提交都应能独立通过相关包测试。

## 10. v2 候选能力

只有在 v1 稳定后再考虑：

| action | 风险 | 备注 |
|---|---|---|
| `stat` | read-only | 低风险，可优先加入 |
| `mkdir` | local-write | 需要路径策略 |
| `copy` | local-write | 注意大文件和覆盖 |
| `move` | privileged local-write | 可能破坏引用，需审批策略 |
| `delete` | destructive | 默认 ask 或 deny |
| `patch` | local-write / privileged | 可从 `apply_patch` 迁移，但 schema 和错误模式复杂 |

v2 前必须先有 v1 指标和实际失败样本，不要为了“统一”提前扩大能力面。
