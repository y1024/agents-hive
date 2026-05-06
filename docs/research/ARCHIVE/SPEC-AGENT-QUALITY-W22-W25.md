# Phase 0 施工 Spec：W22 Prompt + W23 工具质量 + W24 Skill 管理 + W25 Context 治理

> **范围**：Hive **Agent 质量本质**升级（头等大事）
> **依赖**：W1 Observability（支撑所有 eval）— 与 Phase 0 并行启动
> **总工期**：12-17 周（3-4 个月）
> **施工后**：Hive agent 在用户感知层面 measurable 优于 v1（一次完成率 50% → 80%；选对工具 70% → 95%；选对 skill 50% → 85%）
> **日期**：2026-04-27

---

## §1 W22 — Prompt 工程化（4-6 周）

### 1.1 Why now（最高优先级）

LLM 行为 90% 由 prompt 决定。Hive 当前：
- `internal/master/prompt_builder.go` 单文件 builder
- `internal/i18n/prompts/{system,subagents,tools}/` MD 文件
- **无 prompt eval suite / regression test / 工具级专门 prompt / cache 优化 / redact**

对照 Claude Code：3 prefix + system.ts + prompts.ts (914 行) + 42 工具各自 prompt.ts（含 git safety / undercover / 用户类型分流），累计 30-50K token system prompt — 这才是 LLM 行为可控的根。

### 1.2 接口设计

#### 1.2.1 工具级 prompt 模块

每工具自带专门 prompt 段（参照 Claude Code BashTool prompt.ts 369 行风格）：

```go
// internal/tools/<tool_name>/prompt.go
package <tool_name>

// SystemPromptSection 该工具贡献的 system prompt 段
//   - 使用教导（如 git safety / commit conventions）
//   - LLM 错误模式防御（如 "NEVER use git add -A"）
//   - 风格控制 / 工具协同（如 "after edit always read again"）
func SystemPromptSection(ctx PromptContext) string

type PromptContext struct {
    UserType        string  // "internal" / "external" / "ant"-style 分流
    Mode            string  // "default" / "plan" / "auto" / "dontAsk"
    HasGitInstructions bool
    HasBackgroundTasks bool
    // ... 视需求扩展
}
```

#### 1.2.2 System prompt 模块化构建

`internal/master/prompt_builder.go` 重构：

```go
type PromptBuilder struct {
    prefix       PromptPrefix  // 3 个 prefix 动态选择（CLI / Agent SDK CLI preset / Agent SDK）
    sections     []PromptSection
    redactor     *Redactor
    cacheManager *CacheManager
}

type PromptSection interface {
    ID() string                       // 唯一标识，eval 用
    ShouldInclude(ctx PromptContext) bool
    Render(ctx PromptContext) string
    DependsOn() []string              // 段间依赖
}

// Build 拼装最终 system prompt
//   1. 选 prefix
//   2. 按 ShouldInclude 过滤段
//   3. 拓扑排序按 DependsOn
//   4. redact 敏感信息
//   5. cache 静态段（变动段挪到 user message 首条以复用 cache）
func (b *PromptBuilder) Build(ctx PromptContext) (PromptResult, error)

type PromptResult struct {
    SystemPrompt   string
    CachedSections []string  // 可缓存段
    DynamicSections []string // 每次变动段
    TokenCount     int
    SectionsUsed   []string  // 用于 eval 追溯
}
```

#### 1.2.3 Prompt eval suite

`internal/promptval/` 新建包：

```go
// EvalSuite 跑 N 个测试用例，测 LLM 行为是否符合 prompt 教导
type EvalSuite struct {
    cases []EvalCase
    llm   LLMProvider  // 用 cheap model 跑（成本控制）
}

type EvalCase struct {
    ID            string
    Category      string  // "git_safety" / "tool_selection" / "danger_handling" / etc
    UserPrompt    string
    Setup         func(ctx context.Context) // 准备环境（如 mock git status）
    Expected      ExpectedBehavior
    PromptSections []string  // 哪些 prompt 段贡献此行为（追溯用）
}

type ExpectedBehavior struct {
    ToolCall       *ToolCallExpect    // 期望调哪个工具 + 哪些参数
    ResponseRegex  *regexp.Regexp     // 响应匹配
    Forbidden      []string           // 禁止行为（如 "NEVER call rm -rf"）
}

// Run 跑全部 cases，返回评分
func (s *EvalSuite) Run(ctx context.Context, builder *PromptBuilder) EvalReport

type EvalReport struct {
    Total      int
    Passed     int
    Failed     int
    PassRate   float64
    Failures   []EvalFailure  // 每个失败 case 包含：期望/实际/触发的 prompt 段
}
```

#### 1.2.4 Prompt regression test

CI gate：每次改 prompt 文件 → 自动跑 EvalSuite → pass rate 不允许下降 > 2%

```bash
# scripts/prompt_regression.sh
go test ./internal/promptval/ -run TestPromptRegression
# 比对 baseline EvalReport.PassRate
```

#### 1.2.5 Prompt cache 优化

参考 Hermes `prompt_caching.py` + Claude Code `--exclude-dynamic-system-prompt-sections`：

```go
type CacheManager struct {
    breakpoints []CacheBreakpoint
}

type CacheBreakpoint struct {
    SectionID string
    TTLSeconds int  // Anthropic prompt cache TTL
}

// 把动态段（cwd / git status / 当前时间）挪到首条 user message
// 静态段（system / tool defs / skill 列表）放 system prompt 头，加 cache_control 标记
```

#### 1.2.6 Prompt redact

```go
type Redactor struct {
    patterns []RedactPattern
}

type RedactPattern struct {
    Match   *regexp.Regexp
    Replace string  // 通常 "[REDACTED]"
    Context string  // 仅在某些上下文 redact
}

// 默认 patterns：
//   - API key（OPENAI_KEY / ANTHROPIC_KEY / AWS_SECRET）
//   - JWT / Bearer token
//   - 内部 codename（USER_TYPE=ant 之外用户看不到的项目代号）
//   - PII（手机号 / 身份证 — 国内合规）
```

### 1.3 改动文件清单

| 文件 | 操作 |
|---|---|
| `internal/tools/<tool>/prompt.go` × 15 | 每工具新增（参照 Claude Code BashTool prompt.ts）|
| `internal/master/prompt_builder.go` | 重构为模块化 PromptSection |
| `internal/promptval/` | 新建（EvalSuite + EvalCase + EvalReport）|
| `internal/promptval/cases/` | 新建 50-100 个 eval case（按 category）|
| `internal/promptval/baseline.json` | 新建（EvalReport 基线）|
| `internal/master/prompt_cache.go` | 新建（CacheManager + breakpoints）|
| `internal/master/prompt_redact.go` | 新建（Redactor + patterns）|
| `scripts/prompt_regression.sh` | 新建 CI gate |

### 1.4 测试 plan

#### Happy path
- T22.1 EvalSuite 50 case 跑通 + pass rate ≥ 95%
- T22.2 改一条 prompt 段 → regression test 自动跑 → 报 pass rate 变化
- T22.3 prompt cache 命中率 ≥ 60%（多轮 turn 后）
- T22.4 redact 验证：API key 在 system prompt 不出现

#### 蓝军 mutation
- M22.1 故意删除某 prompt 段 → 对应 eval case fail（验证段贡献追溯）
- M22.2 改 prompt 段使用 git add -A → eval case "防误提交"应 fail
- M22.3 改 prompt 删 destructive warning 段 → "danger_handling" eval case fail
- M22.4 cache breakpoint 错配 → cache 命中率显著下降
- M22.5 redact 漏配 → 故意构造含 API key 的 prompt → key 暴露 → CI gate 失败

#### 关键 metric
- LLM 行为符合 prompt 教导率：60% → **95%**
- 已知错误模式触发率：~20% → **<2%**
- prompt token 通过 cache 节省：**40-60%**

### 1.5 工期：4-6 周

| 周 | 工作 |
|---|---|
| 1 | PromptBuilder 重构 + 5 核心工具 prompt.go |
| 2 | 剩余 10 工具 prompt.go + Redactor |
| 3 | EvalSuite 框架 + 30 case |
| 4 | EvalSuite 扩到 100 case + regression test CI gate |
| 5 | CacheManager + breakpoints + cache 命中率优化 |
| 6 | 蓝军 mutation 跑 + iter |

### 1.6 验收

- ✅ EvalSuite 100 case + pass rate ≥ 95%
- ✅ 每工具有专门 prompt.go
- ✅ regression test CI gate 上线
- ✅ prompt cache 命中率 ≥ 60%
- ✅ Redactor 防 API key / PII 暴露
- ✅ 蓝军 mutation 5 条全过

---

## §2 W23 — 工具质量管理（3-4 周）

### 2.1 Why now

LLM 调工具的准确率取决于：
- description 引导清晰度
- 参数 schema 边界明确度
- 结果反馈结构化（让 LLM 下一步决策对）

Hive 当前 15+ 工具，**0 个有 description quality eval**，**0 个有 selection eval**，**0 个有失败模式 catalog**。

### 2.2 接口设计

#### 2.2.1 工具描述 quality eval

```go
// internal/toolval/description_eval.go
type DescriptionEval struct {
    cases []DescriptionEvalCase
    llm   LLMProvider
}

type DescriptionEvalCase struct {
    UserIntent  string  // "用户想搜索文件内容"
    Tools       []ToolMeta  // 候选工具集
    ExpectedTool string     // 应该被选中的工具
}

// Run 测每个 case 下 LLM 选对工具的概率（10 次采样取均值）
func (e *DescriptionEval) Run(ctx) DescriptionReport

type DescriptionReport struct {
    PerTool     map[string]float64  // tool_name → call accuracy
    PerIntent   map[string]float64
    Overall     float64
    Confusions  []ConfusionPair  // 哪两个工具最容易混淆（应改 description）
}
```

#### 2.2.2 工具描述 A/B 测试 framework

```go
// 改一个工具 description → 跑 EvalSuite 看 LLM 行为变化
type DescriptionABTest struct {
    toolName   string
    variantA   string  // 旧 description
    variantB   string  // 新 description
    cases      []DescriptionEvalCase
}

func (t *DescriptionABTest) Run(ctx) ABReport
type ABReport struct {
    CallAccuracyA  float64
    CallAccuracyB  float64
    Significance   float64  // 统计显著性（z-test 或 chi-square）
    Recommendation string   // "adopt B" / "keep A" / "需更多样本"
}
```

#### 2.2.3 工具失败模式 catalog

每工具维护"LLM 常见误用"列表 + prompt 防御：

```go
// internal/tools/<tool>/failure_modes.go
package <tool>

// FailureModes 记录 LLM 用此工具的常见失败模式
//   - 每条对应 prompt.go 中的一段防御教导
//   - 新发现的失败模式必须先加到 catalog 再补 prompt
var FailureModes = []FailureMode{
    {
        ID:          "bash.git_add_all",
        Description: "LLM 用 'git add -A' 容易误提交 .env / credentials",
        Defense:     "prompt 加：'NEVER use git add -A; prefer specific files by name'",
        EvalCase:    "test_git_safety_no_blanket_add",  // 对应的 eval case
    },
    // ...
}
```

#### 2.2.4 工具结果 schema 标准化

```go
// internal/tools/result.go
type ToolResult struct {
    Status    Status    // success / error / partial
    Output    string    // 主结果（给 LLM 看）
    Metadata  map[string]any  // 结构化（如 file_path / line_count / etc）
    
    // 错误结构化
    Error     *ToolError  // status=error 时填
    
    // 提示 LLM 下一步
    NextHints []NextHint  // 如 "you can use tool X to refine"
}

type ToolError struct {
    Code      string  // "file_not_found" / "permission_denied" / "rate_limited"
    Message   string  // 给 LLM 看的短描述
    Retryable bool    // LLM 应该重试还是放弃
    Suggestion string // 给 LLM 的修复建议
}
```

#### 2.2.5 工具 schema 版本管理

```go
// internal/tools/<tool>/schema.go
package <tool>

const SchemaVersion = "v2"  // 每次 breaking change 升版

// CompatibilityMatrix 列举 schema 演化路径
var CompatibilityMatrix = []SchemaMigration{
    {From: "v1", To: "v2", BreakingChange: "param 'path' renamed to 'file_path'"},
}
```

### 2.3 改动文件清单

| 文件 | 操作 |
|---|---|
| `internal/toolval/description_eval.go` | 新建 |
| `internal/toolval/ab_test.go` | 新建 |
| `internal/tools/<tool>/failure_modes.go` × 15 | 每工具新增 |
| `internal/tools/result.go` | 新建标准化 ToolResult |
| 现有工具 result 返回 | 改为 ToolResult 结构 |
| `internal/tools/<tool>/schema.go` | 每工具 SchemaVersion 字段 |

### 2.4 测试 plan

#### Happy path
- T23.1 DescriptionEval 跑 100 case → 整体 call accuracy ≥ 95%
- T23.2 找出 confusion pair（如 read_file vs read_lines）→ 改 description → A/B test → 显著提升
- T23.3 ToolError 结构化 → LLM 看 error.Suggestion 后下一步成功率 ≥ 80%

#### 蓝军 mutation
- M23.1 改某 tool description 让它含糊 → call accuracy 下降
- M23.2 把 ToolError.Retryable 字段去掉 → LLM 重试逻辑乱
- M23.3 schema breaking change 不升版 → 现有 caller 编译失败（CI 拦）

#### 关键 metric
- LLM 选对工具率：70% → **95%**
- 工具调用一次成功率：60% → **85%**
- 工具误用导致 task 失败：25% → **5%**

### 2.5 工期：3-4 周

### 2.6 验收

- ✅ DescriptionEval 100 case + 整体 call accuracy ≥ 95%
- ✅ 每工具有 FailureModes catalog + 对应 prompt 防御
- ✅ ToolResult / ToolError 结构化全部工具改造
- ✅ A/B test framework 可用
- ✅ 蓝军 mutation 3 条全过

---

## §3 W24 — Skill 管理本质（3-4 周）

### 3.1 Why now

Skill 是"教 LLM 怎么完成特定任务的指令包"。质量直接决定：
- LLM router 选不选这个 skill
- 选了之后能不能正确执行

Hive `internal/skills/` 已有 finder / discovery / executor / metrics 等，但 **0 个 quality eval**，**0 个 router eval**，**0 个失败追溯**。

### 3.2 接口设计

#### 3.2.1 Skill quality eval

```go
// internal/skillval/quality_eval.go
type SkillQualityEval struct {
    cases []SkillEvalCase
    llm   LLMProvider
}

type SkillEvalCase struct {
    SkillName    string
    Scenario     string         // 用户场景
    Setup        func(ctx)      // 环境准备
    Expected     SkillOutcome   // 期望结果
}

type SkillOutcome struct {
    Steps         []ExpectedStep  // 期望的执行步骤
    FinalState    StateMatcher    // 最终状态校验
    QualityScore  int             // 1-5 分主观评分（人工 review）
}

// Run 跑 N 个 case，每个 case 让 LLM 用 skill 完成任务，评分
func (e *SkillQualityEval) Run(ctx) SkillQualityReport

type SkillQualityReport struct {
    PerSkill map[string]SkillScore
}

type SkillScore struct {
    HitRate      float64  // skill router 选中此 skill 的概率
    SuccessRate  float64  // 选中后任务真完成的概率
    AvgQuality   float64  // 平均质量分
    Issues       []string // 失败原因
}
```

#### 3.2.2 Skill router eval

```go
// 多 skill 场景下 LLM 选对率
type SkillRouterEval struct {
    cases []RouterCase
}

type RouterCase struct {
    UserIntent     string
    AvailableSkills []SkillMeta
    ExpectedSkill   string  // 应该被选的
}

func (e *SkillRouterEval) Run(ctx) RouterReport
// 整体 router 选对率 + 每对 skill 的混淆矩阵
```

#### 3.2.3 Skill 失败追溯

```go
// internal/skills/failure_trace.go
type FailureTracer struct {
    store FailureStore  // 写 hive_skill_failures 表
}

type SkillFailure struct {
    SkillName     string
    SessionID     string
    UserIntent    string
    StepFailed    string  // 哪一步失败
    Error         string
    FullTrace     []TraceEntry  // 完整执行 trace
    UserFeedback  string  // 用户反馈（thumbs down / 文字）
    DiagnosedAs   string  // 自动归类（router 选错 / 步骤不清 / 工具调失败 / etc）
}

// 每次 skill 跑完后记录（不论成功失败）
// 失败的 trace 进入 weekly review，识别 skill 质量问题
```

#### 3.2.4 Skill 内容 LLM-friendly 度评分

```go
// internal/skillval/content_quality.go
//   - 用 cheap LLM 评分 skill 内容
//   - 维度：步骤清晰度 / 边界明确度 / 示例充分度 / 防错教导
type ContentQualityScorer struct {
    llm LLMProvider
}

func (s *ContentQualityScorer) Score(skill *Skill) ContentScore

type ContentScore struct {
    Clarity     int  // 1-5
    Boundary    int
    Examples    int
    Defense     int
    Total       int
    Suggestions []string  // 具体改进建议
}
```

#### 3.2.5 Skill 版本演化 + drift 检测

```go
// 检测：prompt 改了但 skill 没改
//      或 skill 描述与内容不一致
type DriftDetector struct {
    promptVersion  string
    skillVersions  map[string]string
}

func (d *DriftDetector) Detect(ctx) []Drift
type Drift struct {
    SkillName       string
    Type            string  // "prompt_drift" / "description_content_mismatch" / etc
    Severity        string
    SuggestedAction string
}
```

### 3.3 改动文件清单

| 文件 | 操作 |
|---|---|
| `internal/skillval/quality_eval.go` | 新建 |
| `internal/skillval/router_eval.go` | 新建 |
| `internal/skillval/content_quality.go` | 新建 |
| `internal/skills/failure_trace.go` | 新建 |
| `internal/skills/drift_detector.go` | 新建 |
| `migrations/` | 加 hive_skill_failures 表 |
| 现有 skill 文件 | 加 SchemaVersion + 元数据 |

### 3.4 测试 plan

#### Happy path
- T24.1 SkillQualityEval 100 case → router 选对率 ≥ 85% + skill 任务成功率 ≥ 80%
- T24.2 ContentQualityScorer 跑全 skill → 输出每 skill 评分 + suggestions
- T24.3 FailureTracer 记录失败 → weekly review 识别 top 3 问题 skill
- T24.4 DriftDetector 检测：人为改 prompt 不改 skill → 报 drift

#### 蓝军 mutation
- M24.1 把某 skill description 改模糊 → router 选对率显著下降
- M24.2 skill 内容删一步 → 任务成功率下降
- M24.3 改 system prompt 删 skill router 段 → 完全不选 skill

#### 关键 metric
- Skill router 选对率：50% → **85%**
- Skill 跑完任务真完成率：60% → **90%**
- 失败可追溯率：30% → **90%**

### 3.5 工期：3-4 周

### 3.6 验收

- ✅ SkillQualityEval + RouterEval 100 case
- ✅ ContentQualityScorer 跑全 skill + 改进 top 5 低分 skill
- ✅ FailureTracer 上线 + weekly review 流程
- ✅ DriftDetector CI gate
- ✅ 蓝军 mutation 3 条全过

---

## §4 W25 — Context 治理（2-3 周）

### 4.1 Why now

Long-running session 中：
- Context 越长越容易 LLM 跑偏（attention 分散）
- 某段 prompt 污染所有后续 turn 难定位
- Compaction 不当 → 关键信息丢失

W9 Memory 治理覆盖了 pre-compaction flush（内存写入），但缺 **context 监控 + 污染追溯 + compaction eval**。

### 4.2 接口设计

#### 4.2.1 Context size 实时监控

```go
// internal/context/monitor.go
type ContextMonitor struct {
    metrics MetricsWriter  // W1 接入
}

// SnapshotPerTurn 每 turn 记录 context 使用情况
type ContextSnapshot struct {
    SessionID     string
    TurnID        int
    TotalTokens   int
    
    // 每段占比（用于诊断）
    SystemPrompt  TokenSegment
    Tools         TokenSegment
    Skills        TokenSegment
    Memory        TokenSegment
    Conversation  TokenSegment  // 用户/助手对话
    
    Timestamp     time.Time
}

type TokenSegment struct {
    Tokens int
    Ratio  float64
    SectionsIncluded []string  // 具体哪些段
}

// PerTurnReport 输出长会话 context 增长曲线
func (m *ContextMonitor) Report(sessionID string) ContextReport
```

#### 4.2.2 段污染追溯

```go
// internal/context/pollution_trace.go
//   - 当 LLM 跑偏（如调错工具 / 给错答案）时
//   - 反向定位：哪段 prompt / context 让它跑偏
type PollutionTracer struct {
    snapshots []ContextSnapshot
    behaviors []LLMBehavior
}

type LLMBehavior struct {
    TurnID       int
    Action       string   // tool_call / response / etc
    Outcome      string   // "success" / "deviation" / "failure"
    DeviationType string  // "wrong_tool" / "wrong_answer" / "ignored_instruction"
}

// Trace 当出现 deviation 时，diff 当前 turn vs 上 turn context
//   找出哪段新增/变化的 context 段最可能是污染源
//   返回排序后的 candidate sections
func (t *PollutionTracer) Trace(turnID int) []PollutionCandidate

type PollutionCandidate struct {
    SectionID       string
    Confidence      float64  // 是污染源的可信度
    Evidence        string   // 具体哪部分内容
    SuggestedAction string   // "remove" / "rephrase" / "ignore"
}
```

#### 4.2.3 Compaction 质量 eval

```go
// internal/context/compaction_eval.go
//   - 压缩前后跑相同 LLM 测试，比较行为差异
type CompactionEval struct {
    cases []CompactionCase
    llm   LLMProvider
}

type CompactionCase struct {
    BeforeContext []Message  // 压缩前完整历史
    AfterContext  []Message  // 压缩后历史
    Probe         string     // 测试 query（如 "用户之前提到的偏好是什么"）
    Expected      string     // 期望 LLM 回答
}

func (e *CompactionEval) Run(ctx) CompactionReport

type CompactionReport struct {
    BehaviorDifference float64  // 压缩前后 LLM 行为差异（0 = 完全一致）
    FactRecallRate     float64  // 关键事实召回率
    Recommendations    []string // 改进建议
}
```

#### 4.2.4 Long-session 稳定性测试

```go
// 自动跑 100 turn 长对话 → 检测 LLM 行为漂移
type LongSessionTest struct {
    template ConversationTemplate
    turns    int  // 默认 100
}

// 在 turn 50 / turn 100 插入相同 probe → 比较 LLM 行为
func (t *LongSessionTest) Run(ctx) DriftReport
```

### 4.3 改动文件清单

| 文件 | 操作 |
|---|---|
| `internal/context/monitor.go` | 新建 |
| `internal/context/pollution_trace.go` | 新建 |
| `internal/context/compaction_eval.go` | 新建 |
| `internal/context/long_session_test.go` | 新建 |
| `internal/master/react_processor.go` | 改：每 turn 调 monitor.SnapshotPerTurn |
| `migrations/` | 加 hive_context_snapshots 表 |

### 4.4 测试 plan

#### Happy path
- T25.1 ContextMonitor 记录 100 turn → 每段占比可视化（饼图）
- T25.2 PollutionTracer：构造 LLM 跑偏 → 准确定位污染源
- T25.3 CompactionEval：压缩前后行为差异 < 10%
- T25.4 LongSessionTest 100 turn：行为漂移 < 15%

#### 蓝军 mutation
- M25.1 故意注入污染段（如冲突指令）→ PollutionTracer 应检出
- M25.2 压缩算法改差 → CompactionEval 行为差异显著上升
- M25.3 100 turn 后 LLM 完全跑偏 → LongSessionTest 报警

### 4.5 工期：2-3 周

### 4.6 验收

- ✅ Context size 监控 + 每段占比可视化
- ✅ 污染追溯能定位至少 70% 跑偏 case
- ✅ Compaction 行为差异 < 10%
- ✅ Long session 100 turn 漂移 < 15%

---

## §5 Phase 0 联合验收

W22 + W23 + W24 + W25 完成后必须满足：

| 验收 | before → after |
|---|---|
| LLM 行为符合 prompt 教导率 | 60% → **95%** |
| LLM 选对工具率 | 70% → **95%** |
| Skill router 选对率 | 50% → **85%** |
| Skill 任务真完成率 | 60% → **90%** |
| 复杂任务一次完成率 | ~50% → **80%** |
| 失败可追溯率 | ~30% → **90%** |
| Long session 行为漂移 | 未测 → **< 15%** |
| Compaction 行为差异 | 未测 → **< 10%** |

**这些是 measurable 用户感知提升，不是工程美学**。

---

## §6 Phase 0 与 Phase 1 并行表

| 周 | Phase 0（agent 质量）| Phase 1（工程支撑）|
|---|---|---|
| 1-2 | W22 PromptBuilder 重构 | W1 Observability 基础 + W2 Tool timeout |
| 3-4 | W22 EvalSuite + W23 启动 | W1 完整接入 + W3 Capacity |
| 5-6 | W22 cache + W23 description eval | W5 BashTool 关键 vector |
| 7-8 | W23 完成 + W24 启动 | W6 Permission 核心 |
| 9-10 | W24 quality eval + W25 启动 | W9 pre-compaction flush + W25 合并 |
| 11-12 | W24 完成 + W25 完成 | Phase 1 收尾 |
| 13-14 | Phase 0 联合验收 + 数据收集 | 监控生产指标 |

**~3.5 个月 Phase 0 + Phase 1 双完成**。

---

## §7 文件索引

```
docs/research/
├── IMPLEMENTATION-PLAN-V2-AGENT-QUALITY-FIRST.md  # ⭐ V2 总实施计划
├── SPEC-AGENT-QUALITY-W22-W25.md                  # ⭐ 本文件（Phase 0 详细 spec）
└── （v1 历史文件保留）
```

---

*— End of W22-W25 Spec —*
