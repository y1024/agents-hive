# Layer 3 施工 Spec：W9 Memory + W10 Skills + W11 MCP + W12 Spec-driven 大重构

> **依赖**：Layer 2（W5+W6+W7）完成；W12 依赖 W7（不依赖 W8）
> **工期**：~9 周（W9-W11 部分并行 + W12 串行 3 周）
> **施工后**：启动 Layer 4
> **日期**：2026-04-25

---

## §1 W9 — Memory 治理

### 1.1 Why now
Hive 当前 `internal/memory/` 有 pgvec + hybrid + extractor + injector + vecindex 完整向量栈，但缺：
- **Pre-compaction memory flush**（OpenClaw 三层证据 [HC-MAIN-VERIFIED]）
- **Date-named append-only daily log**
- **Nightly distill 离线批处理**
- **Structured summary 模板**（Hermes context_compressor 10 项改进）
- **5-provider embedding fallback**（OpenClaw [HC-MAIN-VERIFIED]）
- **MemoryManager 单一入口**（Hermes pattern）
- **bootstrap caps**（OpenClaw 20K/150K）

### 1.2 接口设计

#### 1.2.1 MemoryManager 单一入口

`internal/memory/manager.go`（新建）：

```go
package memory

type MemoryManager struct {
    builtin   Provider  // BuiltinMemoryProvider 总是 first
    external  Provider  // 仅一个 external plugin 允许
    extractor *Extractor
    injector  *Injector
    compactor *Compactor
}

type Provider interface {
    Name() string
    Search(ctx context.Context, query string, topK int) ([]MemoryEntry, error)
    Get(ctx context.Context, key string) (MemoryEntry, error)
    Write(ctx context.Context, entry MemoryEntry) error
}

func (m *MemoryManager) AddProvider(p Provider) error {
    if !m.builtin.IsBuiltin() {
        return errors.New("BuiltinMemoryProvider must be registered first")
    }
    if m.external != nil {
        return errors.New("only one external memory plugin allowed")
    }
    m.external = p
    return nil
}

// Workflow（参照 Hermes）
func (m *MemoryManager) BuildSystemPrompt() string { ... }
func (m *MemoryManager) PrefetchAll(ctx context.Context, userMessage string) Context { ... }
func (m *MemoryManager) SyncAll(ctx context.Context, userMsg, assistantResp string) error { ... }
func (m *MemoryManager) QueuePrefetchAll(ctx context.Context, userMsg string) { ... }
```

#### 1.2.2 Pre-compaction Memory Flush（P0-4）

`internal/master/compaction.go`（新建）：

```go
package master

type CompactionGuard struct {
    softThresholdTokens int     // 默认 4000
    reserveTokensFloor  int     // 默认 20000
    contextWindow       int     // 由 LLM provider 决定
    
    silentTurn          *SilentTurnTrigger
    nightlyDistiller    *NightlyDistiller   // 接 W9.1.4
    structuredSummary   *StructuredSummary  // 接 W9.1.5
}

// Check 在每个 ReAct turn 开始前调用
//   currentTokens 当前 session 估算 tokens
//   返回 nil 表示无需 flush；返回 *FlushAction 表示需触发
func (g *CompactionGuard) Check(ctx context.Context, sessionID string, currentTokens int) *FlushAction {
    threshold := g.contextWindow - g.reserveTokensFloor - g.softThresholdTokens
    if currentTokens >= threshold {
        return &FlushAction{
            Type: FlushTypeSilentTurn,
            Reason: fmt.Sprintf("approaching compaction (%d / %d)", currentTokens, threshold),
        }
    }
    return nil
}

// SilentTurn 触发一个无声 agentic turn（参照 OpenClaw memoryFlush）
//   - 不 broadcast 给前端 / channel
//   - prompt: "Session nearing compaction. Store durable memories now."
//   - user prompt: "Write any lasting notes to memory/YYYY-MM-DD.md; reply with NO_REPLY if nothing to store."
//   - LLM 回 NO_REPLY → 跳过；回内容 → 写 daily log
type SilentTurnTrigger interface {
    Trigger(ctx context.Context, sessionID string) error
}
```

配置 schema（接 W3 capacity governance）：
```json
{
  "memory": {
    "compaction": {
      "soft_threshold_tokens": 4000,
      "reserve_tokens_floor": 20000,
      "memory_flush": {
        "enabled": true,
        "system_prompt": "Session nearing compaction. Store durable memories now.",
        "prompt": "Write any lasting notes to memory/YYYY-MM-DD.md; reply with NO_REPLY if nothing to store."
      }
    }
  }
}
```

#### 1.2.3 Daily Log + Nightly Distill

**F11 修复**：原始写 → append-only journal（不直接改 MEMORY.md）；distill 只消费不可变 journal entries；输出原子 rename；时钟基准固定 UTC（防时钟回拨）。

`internal/memory/daily_log.go`：

```go
type DailyLog struct {
    workspace string
    clock     Clock   // F11: 固定 UTC，避免本地时区 / 时钟回拨
}

// Entry 不可变记录
type JournalEntry struct {
    ID         string    // monotonic ULID（含时间戳 + 顺序）
    SessionID  string
    Content    string
    CreatedAt  time.Time // UTC
    SchemaVer  int       // schema 版本，未来扩展
}

// Append 写到 append-only journal（不是直接改 MEMORY.md）
//   文件：memory/journal/YYYY-MM-DD.jsonl（每行一个 JournalEntry，UTC 日期）
//   每个 session 走独立 fd 写自己的行（无锁），fsync 后再返回
//   绝不修改已写入的行
func (d *DailyLog) Append(ctx context.Context, sessionID string, entry string) error {
    je := JournalEntry{
        ID:        ulid.MustNew(ulid.Timestamp(d.clock.NowUTC()), rand.Reader),
        SessionID: sessionID,
        Content:   entry,
        CreatedAt: d.clock.NowUTC(),
        SchemaVer: 1,
    }
    return d.journal.AppendLine(je)
}
```

`internal/memory/nightly_distill.go`：

```go
// NightlyDistiller 每晚跑一次，消费**不可变 journal**生成 MEMORY.md + topic files
type NightlyDistiller struct {
    schedule *cron.Schedule  // 默认每天 03:07 UTC（避开整点 + 跨时区不冲突）
    llm      LLMProvider
    clock    Clock           // F11: UTC
    journal  Journal         // F11: 只读 journal
}

// Distill 离线批处理
//   1. 读最近 N 天 journal entries（不修改 source）
//   2. 用 LLM summarize → 新 MEMORY.md 内容
//   3. 原子 rename（写 MEMORY.md.tmp → fsync → rename）
//   4. journal 永远不删（compaction 由 retention policy 单独管）
func (d *NightlyDistiller) Distill(ctx context.Context, days int) error {
    entries, err := d.journal.ReadRange(d.clock.NowUTC().Add(-time.Duration(days)*24*time.Hour), d.clock.NowUTC())
    if err != nil { return err }
    
    summary, err := d.llm.Summarize(ctx, entries)
    if err != nil { return err }
    
    // 原子 rename
    return d.atomicWrite("MEMORY.md", summary)
}
```

**F11 蓝军 mutation**：
- 两 session 同时 silent turn → 两个独立 journal append，无冲突（fd 独立）
- 时钟回拨 5 min → ULID 单调（含 monotonic counter）→ 顺序可恢复
- nightly distill 中途崩溃 → MEMORY.md.tmp 残留，下次 startup 检测删除 + 重跑
- 多 session 同 workspace 同时 distill → 加 file lock（flock），第二个 distill 跳过

#### 1.2.4 Structured Summary（Hermes 风格）

`internal/memory/structured_summary.go`：

```go
type StructuredSummary struct {
    template *Template
    auxLLM   LLMProvider  // cheap/fast model
}

// Template 用 Hermes 风格的结构化模板
//   - Resolved questions tracking
//   - Pending questions tracking
//   - Handoff framing: "different assistant"
//   - "Remaining Work" instead of "Next Steps"
const SummaryTemplate = `
You are summarizing a conversation for a different assistant. Do not respond to any questions.

## Resolved
[Questions resolved with definitive answers]

## Pending
[Questions still open]

## Decisions
[Key decisions made]

## Remaining Work
[What still needs to be done — not as next steps but as facts]

## Tool Calls
[Notable tool results, condensed]
`

func (s *StructuredSummary) Compress(ctx context.Context, messages []Message, budgetTokens int) (string, error) {
    // 1. Tool output pruning before LLM（cheap pre-pass）
    // 2. Scaled summary budget
    // 3. Iterative summary updates（含上次 summary 一并喂入）
    // 4. 用 auxLLM 跑
}
```

#### 1.2.5 5-provider embedding fallback

**F12 修复**：禁止 silent provider 切换（向量空间不同会让旧索引语义失真）。索引必须绑 embedding_model_id；provider 变更触发 rebuild 或分库。

`internal/memory/embedding/factory.go`：

```go
type EmbeddingProvider int
const (
    ProviderLocal EmbeddingProvider = iota  // ollama / 跳过（W9 决策）
    ProviderOpenAI
    ProviderGemini
    ProviderVoyage
    ProviderMistral
)

// EmbeddingModelID 唯一标识 (provider + model name + version)
//   - 例：openai/text-embedding-3-small/v1
//   - voyage/voyage-3/v1
//   - 任何 provider 内部模型升级（OpenAI 出 v2）→ 新 ModelID
type EmbeddingModelID string

// IndexedVector 每条向量记录 embedding_model_id
type IndexedVector struct {
    ID              string
    Content         string
    Vector          []float32
    EmbeddingModel  EmbeddingModelID  // F12: 必填
    CreatedAt       time.Time
}

// AutoSelect 按优先级自动选 provider，但**不允许 silent switch**
//   - 启动时 check：当前选择的 provider 与索引的 model_id 是否一致
//   - 不一致 → 拒绝启动（fail closed）+ 提示 rebuild 或保持当前 provider
func AutoSelect(config *Config, currentIndexModel EmbeddingModelID) (Provider, error) {
    selected, err := selectProviderByPriority(config)
    if err != nil { return nil, err }
    
    if currentIndexModel != "" && selected.ModelID() != currentIndexModel {
        return nil, fmt.Errorf(
            "embedding model mismatch: index uses %s but config selects %s. "+
            "Run `hive memory rebuild` to rebuild index with new model, or revert config.",
            currentIndexModel, selected.ModelID(),
        )
    }
    
    return selected, nil
}

// 索引迁移路径
//   1. 用户 explicit `hive memory rebuild --new-model voyage/voyage-3/v1`
//   2. rebuild 跑 dual-index 模式（旧索引继续可读，新索引并发构建）
//   3. 新索引完成 → 切换 query 到新索引 → 旧索引 retention 延迟删除
//   4. 中途失败 → 保留旧索引继续工作
```

**F12 蓝军 mutation**：
- 用 OpenAI 建索引 → 改 config 切 Voyage → server 启动失败（fail closed）
- 显式跑 `hive memory rebuild` → dual-index 期间查询走旧 → 切换瞬间走新 → 旧索引 30 天后删
- 启动检测：index 中 EmbeddingModel 字段空（旧数据）→ 视作"unknown" + 强制 rebuild 或 user 显式标记同 provider

#### 1.2.6 Bootstrap caps

`internal/memory/bootstrap.go`：

```go
const (
    MAX_ENTRYPOINT_LINES  = 200    // 单 MEMORY.md 文件
    MAX_ENTRYPOINT_BYTES  = 25_000 // 同上字节
    BOOTSTRAP_MAX_CHARS   = 20_000  // 注入 system prompt 的单文件 cap
    BOOTSTRAP_TOTAL_MAX   = 150_000 // 总 cap
)

// Truncate 双触发先达
func Truncate(content string) (string, bool) {
    lineCount := strings.Count(content, "\n")
    if lineCount > MAX_ENTRYPOINT_LINES { ... }
    if len(content) > MAX_ENTRYPOINT_BYTES { ... }
}
```

### 1.3 改动文件清单

| 文件 | 操作 |
|---|---|
| `internal/memory/manager.go` | 新建 MemoryManager |
| `internal/memory/daily_log.go` | 新建 |
| `internal/memory/nightly_distill.go` | 新建 |
| `internal/memory/structured_summary.go` | 新建 |
| `internal/memory/embedding/factory.go` | 改：实现 AutoSelect |
| `internal/memory/bootstrap.go` | 新建 caps |
| `internal/master/compaction.go` | 新建 CompactionGuard |
| `internal/master/react_processor.go` | 改：每 turn 前调 CompactionGuard.Check |
| `internal/config/memory.go` | 改：加 compaction section |

### 1.4 测试 plan

- T9.1 长会话压缩前后事实召回准确率 > 90%（mutation：故意压缩到 30% context，测 50 个事实问题）
- T9.2 silent turn 不 broadcast 给前端（mutation：检查 channel adapter 收到的 event 不含 silent turn）
- T9.3 nightly distill 跑通：5 天 daily log → MEMORY.md 合理合并
- T9.4 5-provider fallback：禁用 OpenAI key → fallback 到 Gemini
- T9.5 bootstrap caps：250 行的 MEMORY.md 被截断到 200 行 + 警告标记
- T9.6 structured summary：含 Resolved/Pending sections + handoff framing 正确

### 1.5 工期：3 周

### 1.6 验收

- ✅ MemoryManager 单一入口运行
- ✅ Pre-compaction silent turn 工作
- ✅ Nightly distill 上线（cron 定时）
- ✅ 5-provider fallback 验证
- ✅ Layer 0 metric：compaction_triggered / silent_turn_emitted / nightly_distill_runs

---

## §2 W10 — Skills 重构

### 2.1 Why now
Hive `internal/skills/` 已有 finder/discovery/executor/hooks/metrics/on_demand_api，**已具备 progressive 基础**但需核实是否真做到 frontmatter-only startup。

### 2.2 接口设计

#### 2.2.1 Progressive Loading 核实 + 完善

`internal/skills/loader.go`（改造现有）：

**F13 修复**：cache 加 sync.RWMutex + singleflight 防 data race + 重复 IO。

```go
import "golang.org/x/sync/singleflight"

type Loader struct {
    skillsDir string
    
    // F13: 并发安全 cache
    mu    sync.RWMutex
    cache map[string]*Skill  // 缓存全文（命中时填）
    
    // F13: singleflight 防同一 skill 并发 LoadFull 重复读 IO
    sf singleflight.Group
}

// LoadAll startup 时只读 frontmatter（~100 token/skill）
func (l *Loader) LoadAll(ctx context.Context) ([]*SkillMetadata, error) {
    // 不读 SKILL.md 全文
    // 只读 frontmatter（name + description + when_to_use + paths + tools 等）
}

// LoadFull 命中时读全文（~5K token）— F13 并发安全
func (l *Loader) LoadFull(ctx context.Context, skillName string) (*Skill, error) {
    // 1. 先 RLock 查 cache
    l.mu.RLock()
    if cached, ok := l.cache[skillName]; ok {
        l.mu.RUnlock()
        return cached, nil
    }
    l.mu.RUnlock()
    
    // 2. cache miss → singleflight（同 skill 并发 LoadFull 仅 1 次 IO）
    val, err, _ := l.sf.Do(skillName, func() (any, error) {
        skill, err := l.readSkillFile(skillName)
        if err != nil { return nil, err }
        
        l.mu.Lock()
        l.cache[skillName] = skill
        l.mu.Unlock()
        
        return skill, nil
    })
    if err != nil { return nil, err }
    return val.(*Skill), nil
}
```

**F13 蓝军 mutation**：
- `go test -race` 100 并发 LoadFull("same-skill") → 无 data race
- LoadFull("same-skill") × 1000 并发 → readSkillFile 实际调用 1 次（singleflight 验证）

#### 2.2.2 Token budget 主动估算

`internal/skills/token_estimator.go`：

```go
// roughTokenCountEstimation 粗略估算 skill content 装入 token
//   - 用字符数 ÷ 3.5 作为近似（参照 Claude Code roughTokenCountEstimation）
func roughTokenCountEstimation(content string) int {
    return len(content) * 4 / 14  // ≈ char/3.5
}
```

#### 2.2.3 mcpSkillBuilders MCP-as-Skills

`internal/skills/mcp_skill_builders.go`：

```go
package skills

// MCPSkillBuilder 把 MCP server 包装成 skill
//   - server 提供的每个 tool / prompt / resource 自动生成对应 skill
//   - skill description 来自 MCP server 的 description
type MCPSkillBuilder interface {
    BuildFromServer(ctx context.Context, mcpServer *mcphost.Server) ([]*Skill, error)
}

// 实现思路（参照 Claude Code mcpSkillBuilders.ts）：
//   - 注册时机：MCP server 连接后
//   - skill 命名空间：`mcp:<server_name>:<tool_name>`
//   - skill body：动态生成 prompt 段
```

### 2.3 改动文件清单

| 文件 | 操作 |
|---|---|
| `internal/skills/loader.go` | 改：strict 区分 LoadAll（frontmatter only）vs LoadFull |
| `internal/skills/token_estimator.go` | 新建 |
| `internal/skills/mcp_skill_builders.go` | 新建 |
| `internal/skills/finder.go` | 改：用 token budget 过滤候选 |

### 2.4 测试 plan

- T10.1 startup 100 个 skills，context 占用 < 10K token（vs 全量加载 500K+）
- T10.2 命中 skill 时 LoadFull 在 < 10ms 完成
- T10.3 MCP server 注册后自动生成 skill（mock MCP server）
- T10.4 mutation：startup 时不允许调 LoadFull（grep 测试）

### 2.5 工期：2 周

### 2.6 验收

- ✅ Progressive loading 在 100 skills 场景下 token 占用 < 10K
- ✅ MCP server 自动包装成 skill
- ✅ Token budget 估算与实际偏差 < 10%

---

## §3 W11 — MCP 生态

### 3.1 Why now
Hive `internal/mcphost/` 26 文件已具备完整 MCP host（client/host/oauth/hitl/transport×3），但缺：
- **MCP 工具结果 collapse 分类**（Claude Code classifyForCollapse）
- **mcporter CLI 集成作为 skill**（OpenClaw 设计）
- **chrome-mcp 浏览器 MCP server**（OpenClaw）

### 3.2 接口设计

#### 3.2.1 collapse 分类

`internal/mcphost/collapse.go`（新建）：

```go
type CollapsePolicy struct {
    AlwaysCollapse []string  // tool name patterns
    NeverCollapse  []string
    ByResultSize   int       // result > N 行自动 collapse
}

// Classify 决定 MCP tool result 是否前端折叠展示
func (p *CollapsePolicy) Classify(toolName string, result string) bool { ... }
```

#### 3.2.2 mcporter skill

`skills/mcporter/SKILL.md`（新建 skill 文件）：

```markdown
---
name: mcporter
description: Use mcporter CLI to list, configure, auth, and call MCP servers/tools directly
when_to_use:
  - User wants to call an MCP server tool ad-hoc
  - User wants to configure a new MCP server
  - User wants to OAuth-auth an MCP server
tools: [bash]
---

# mcporter — 外部 MCP CLI 包装为 skill

## Quick start
- `mcporter list` — 列已配置的 MCP servers
- `mcporter call <server.tool> arg1=value1` — 调用 MCP tool
- `mcporter auth <server>` — OAuth flow
- `mcporter daemon start|status|stop` — 后台 daemon

## Hive 集成
（参照 OpenClaw mcporter skill 的内容）
```

#### 3.2.3 chrome-mcp

**F6 修复**：把 browser 暴露为外部 MCP server 是 SSRF + 内网探测攻击面。必须加 capability token + auth + SSRF 边界 + 复用 Hive permission 链。

`internal/tools/browser/mcp_server.go`（新建）：

```go
// 把现有 browser tool 暴露为 MCP server
//   - 让外部 MCP client（如其他 agent）可以调 Hive 的 browser
//   - **SECURITY 加固**：F6 修复
type ChromeMCPServer struct {
    browserTool   *browser.Tool
    authChecker   AuthChecker        // capability token 校验
    permEngine    *permissions.Engine // 复用 Hive permission 链（W6 同款）
    capacityGov   capacity.Governance // 复用 Hive capacity（W3 同款）
    auditLogger   AuditLogger
    ssrfGuard     *SSRFGuard         // F6 新增：URL allowlist + 内网拒绝
}

// SSRFGuard 防 SSRF + 内网探测
type SSRFGuard struct {
    allowExternalDomains  []string  // 显式 allowlist（默认空 = 禁全部外网）
    blockPrivateNetworks  bool      // 默认 true：拒 RFC 1918 + 169.254.0.0/16 + 127.0.0.0/8 + ::1
    blockMetadataServices bool      // 默认 true：拒 169.254.169.254 (AWS) / metadata.google.internal / etc
    blockLoopback         bool      // 默认 true：拒 localhost / 127.x / ::1 / 0.0.0.0
}

func (g *SSRFGuard) Check(targetURL string) error {
    u, err := url.Parse(targetURL)
    if err != nil { return err }
    
    host := u.Hostname()
    ip := net.ParseIP(host)
    
    // 1. 直接 IP？解析 → 检查私网 / metadata / loopback
    if ip != nil {
        if g.blockPrivateNetworks && isPrivate(ip) { return ErrSSRFPrivateNetwork }
        if g.blockMetadataServices && isMetadata(ip) { return ErrSSRFMetadata }
        if g.blockLoopback && ip.IsLoopback() { return ErrSSRFLoopback }
    }
    
    // 2. 域名 → 解析后检查 IP（防 DNS rebinding）
    ips, _ := net.LookupIP(host)
    for _, ip := range ips {
        if g.blockPrivateNetworks && isPrivate(ip) { return ErrSSRFPrivateNetwork }
        // ... 同上
    }
    
    // 3. allowlist 校验
    if len(g.allowExternalDomains) > 0 && !matchAny(host, g.allowExternalDomains) {
        return ErrSSRFNotInAllowlist
    }
    
    return nil
}

// MCP 调用前必走的链
func (s *ChromeMCPServer) Call(ctx context.Context, name string, args map[string]any) (string, error) {
    // 1. 鉴权：必须有效 capability token
    cap, err := s.authChecker.Check(ctx)
    if err != nil { return "", ErrUnauthorized }
    
    // 2. SSRF 防御
    if url, ok := args["url"].(string); ok {
        if err := s.ssrfGuard.Check(url); err != nil {
            s.auditLogger.Log(ctx, "ssrf_blocked", url, err)
            return "", err
        }
    }
    
    // 3. 复用 Hive permission（与本地工具调用一视同仁）
    decision := s.permEngine.Evaluate(ctx, fmt.Sprintf("browser.%s", name), cap.SessionContext)
    if decision == DecisionDeny { return "", ErrPermissionDenied }
    
    // 4. 复用 capacity governance
    perm := s.capacityGov.RequestToolConcurrency(ctx, "chrome-mcp", cap.SessionID)
    defer perm.Release()
    if !perm.Allowed { return "", ErrCapacityExceeded }
    
    // 5. 执行 + audit
    result, err := s.browserTool.Execute(ctx, args)
    s.auditLogger.Log(ctx, "chrome_mcp_call", name, args, err)
    return result, err
}
```

**默认配置（保守）**：
```json
{
  "tools": {
    "chrome_mcp": {
      "enabled": false,            // 默认 OFF
      "allow_external_domains": [], // 默认空 = 禁全部外网
      "require_capability_token": true,
      "block_private_networks": true,
      "block_metadata_services": true,
      "block_loopback": true
    }
  }
}
```

**蓝军 mutation**（强制验收）：
- 未带 token 调 chrome-mcp.navigate → ErrUnauthorized
- 带 token 但 url=http://169.254.169.254 → ErrSSRFMetadata
- 带 token 但 url=http://10.0.0.1:8080 → ErrSSRFPrivateNetwork
- 带 token 但 url=http://localhost:5432 → ErrSSRFLoopback
- DNS rebinding：域名解析为 internal IP → 仍拒（lookup 后再次 check）

### 3.3 改动文件清单

| 文件 | 操作 |
|---|---|
| `internal/mcphost/collapse.go` | 新建 |
| `internal/mcphost/host.go` | 改：调用 Classify 决定 result 渲染 |
| `skills/mcporter/SKILL.md` | 新建 |
| `internal/tools/browser/mcp_server.go` | 新建 |

### 3.4 工期：1 周

### 3.5 验收

- ✅ MCP tool result 大于 100 行自动 collapse
- ✅ mcporter skill 可用（用户能 ad-hoc 调用任何 MCP server）
- ✅ Hive browser 能作为 MCP server 暴露给外部

---

## §4 W12 — Spec-driven 大重构（OpenSpec 真意落地）

### 4.1 Why now
Hive 当前 `internal/specdriven/` 14 文件 + 7 测试 + workflow，**Phase 1 已上线**（SafeExecutor 权限极简），但 **Phase 2 hidden 实现走偏 OpenSpec 真意**：
- ❌ 当前：todos / spec / tasks 完全 hidden（DB 持久化，不上前端）
- ✅ OpenSpec 真意：artifact 显式可见（markdown 文件 + git 追踪）

W12 大重构方向（用户 Q1=A / Q2=B 决策）：
- 撤 hidden DB 路径（或降级为 audit log）
- 加 markdown artifact export 层
- 接 W7 Web Console todos UI（**不依赖 W8 飞书** — R6.1 一致性确认）
- 保留 propose → apply → archive 流程
- AI 质量 measurable metric

**R6.1 修复**：W12 依赖关系全文一致 — 仅依赖 W7（不依赖 W8）。本节 §4 各子节、`IMPLEMENTATION-PLAN.md §3`、本 spec 标题段全部对齐。

### 4.2 接口设计

#### 4.2.1 Markdown Artifact Export

**F7 修复**：撤销双向 Sync。**单向 export only**（DB canonical → filesystem 只读 projection）。

`internal/specdriven/markdown_export.go`（新建）：

```go
package specdriven

// Exporter 把 SpecChangeStore 中的 change 导出为 markdown 文件
//   目录结构（参照 OpenSpec）：
//     internal/storage/specs/<session_id>/<change_id>/
//       ├── proposal.md
//       ├── tasks.md
//       ├── design.md
//       └── specs/
//           └── <spec_id>/spec.md
type Exporter struct {
    store SpecChangeStore
    fs    Filesystem
    outbox OutboxQueue  // F7: 走 outbox pattern 保证 export 可靠 + 单向
}

// **F7 关键约束**：
//   - DB canonical（hive_spec_changes 表）— 唯一真源
//   - filesystem 只读 projection（用户 / git diff 可看，但任何回写被拒）
//   - 通过 outbox pattern：DB 事务提交时入 outbox → 异步 worker 消费写 fs
//   - filesystem 上手改 .md 文件**完全无效**（loader 不读 fs，只 export 时写）

func (e *Exporter) ExportChange(ctx context.Context, changeID string) error {
    change, err := e.store.Get(ctx, changeID)
    if err != nil { return err }
    
    // 1. 嵌入 exported_revision 防回放
    content := renderMarkdown(change, RenderOpts{
        ExportedRevision: change.Revision,
        ExportedAt:       time.Now(),
        SourceWarning:    "// THIS FILE IS A READ-ONLY PROJECTION FROM DB. EDITS WILL BE OVERWRITTEN. //",
    })
    
    // 2. 原子写入（先写 .tmp 再 rename）
    return e.fs.AtomicWrite(filepath.Join(...), content)
}

// **不存在 Sync 方法**（避免双向一致性 bug）
// 如果 fs 和 DB 不一致：DB 永远赢，下一次 ExportChange 直接覆盖

// **回写检测（防误改）**：
//   有一个 ReadOnlyEnforcer 后台监控 internal/storage/specs/，发现修改时立即覆盖回 DB 版本 + 记 audit log
type ReadOnlyEnforcer struct {
    fsWatcher *fsnotify.Watcher
    exporter  *Exporter
}

func (r *ReadOnlyEnforcer) Run(ctx context.Context) {
    for event := range r.fsWatcher.Events {
        if event.Op&(fsnotify.Write|fsnotify.Create) != 0 {
            // 用户改了 markdown → 立即覆盖回 DB 版本
            changeID := extractChangeIDFromPath(event.Name)
            r.exporter.ExportChange(ctx, changeID)
            r.auditLogger.Log(ctx, "fs_write_reverted", event.Name)
        }
    }
}
```

**F7 蓝军 mutation**：
- 手改 proposal.md → 1 秒内 ReadOnlyEnforcer 覆盖回 DB 版本 + audit log 记 "fs_write_reverted"
- 后台 DB 改 + 立即调 ExportChange → 一致（因为单向）
- ExportChange 失败（fs 不可写）→ 主路径不阻塞（outbox 重试 + audit log "specdriven_export_failure"）

#### 4.2.2 Todos 事件接 ChannelAdapter

`internal/specdriven/todos_emitter.go`（新建）：

```go
// 当 specdriven 生成 plan / 分解 tasks 时，emit TodoEvent
type TodosEmitter struct {
    eventBus streaming.EventBus
}

func (e *TodosEmitter) EmitPlan(ctx context.Context, sessionID string, plan *Plan) error {
    for i, step := range plan.Steps {
        e.eventBus.Emit(ctx, streaming.TodoEvent{
            EventID:     uuid.New().String(),
            SessionID:   sessionID,
            TodoID:      step.TaskKey,
            PlanID:      plan.ID,
            Status:      streaming.TodoStatusPending,
            Description: step.Description,
            Order:       i,
            Timestamp:   time.Now(),
        })
    }
}

func (e *TodosEmitter) UpdateStatus(ctx context.Context, sessionID, todoID string, newStatus streaming.TodoStatus) error
```

#### 4.2.3 默认 OFF → 启用决策

W12 完成后：
- `spec_driven.mode` 默认改为 `"experiment"`（不是 `"legacy"` 也不是直接 `"on"`）
- `spec_driven.continuation.default` 默认 `"off"` 保持
- experiment mode 下：5% 流量走 spec-driven，95% legacy
- 收集 metric 验证：长任务完成率 / 跑偏率 / failure attribution accuracy

#### 4.2.4 AI 质量 Measurable Metric

`internal/specdriven/metrics.go`（扩展现有）：

```go
type QualityMetrics struct {
    // 长任务完成率
    LongTaskCompletionRate float64
    // 跑偏率（task 完成但偏离 user intent）
    DeviationRate float64
    // 失败可追溯性（failure 能定位到具体 step / tool call）
    FailureAttributionAccuracy float64
    
    // 对照组（legacy path）vs 实验组（spec-driven path）的 A/B
    ABComparisonReady bool
}
```

### 4.3 改动文件清单

| 文件 | 操作 |
|---|---|
| `internal/specdriven/markdown_export.go` | 新建 |
| `internal/specdriven/todos_emitter.go` | 新建 |
| `internal/specdriven/metrics.go` | 改：加 QualityMetrics |
| `internal/master/session_loop_specdriven_react.go` | 改：emit todos + 接 markdown export |
| `internal/specdriven/planner/` | 改：plan 生成时 emit TodoEvent |
| `internal/store/spec_change_store.go` | 改：加 audit log 路径（hidden DB 仍保留为 audit）|
| `internal/config/spec_driven.go` | 改：mode 加 `"experiment"` 选项 |

### 4.4 测试 plan

#### Happy path
- T12.1 specdriven 生成 plan → markdown 文件落到 `internal/storage/specs/<sid>/<cid>/{proposal,tasks,design}.md`
- T12.2 plan 各 step → 前端 W7 Web Console 看到 todos
- T12.3 用户改 todo → 后端持久化 + DB ↔ filesystem 同步
- T12.4 experiment mode 下 5% 流量分流正确（采样率 metric）

#### 蓝军 mutation
- M12.1 markdown export 失败（fs 不可写）→ 主路径不阻塞，仅 audit log（**audit log 形式**：写 `hive_logs` 表 source=`specdriven_export_failure`）
- M12.2 DB ↔ filesystem 不同步（手动改 markdown）→ exported_revision 检测 → fail closed
- M12.3 todos emit 漏（某 step 没 emit）→ E2E 测试 fail
- M12.4 long task A/B 对照：**统计显著性测试**（最小样本量 200 sessions × 实验+对照，alpha=0.05，power=0.8，预期效应 ≥ 20% 完成率提升）→ 实验组显著优于对照组

### 4.5 工期：3 周

### 4.6 验收

- ✅ Phase 2 撤 hidden 设计，artifact 显式可见
- ✅ markdown export 工作（DB canonical + fs 只读 projection）
- ✅ todos 通过 W7 Web 通道完整可见可干预
- ✅ AI 质量 measurable metric 上线
- ✅ Phase 2 dual-flag rollout 准备好（experiment mode）
- ⚠️ 实际 dual-flag rollout 验证移到 Q3（W12 ship 后启动数据收集 quarter）

---

## §5 Layer 3 联合验收

W9+W10+W11+W12 完成后必须满足：

| 验收 | 检查 |
|---|---|
| Memory 长任务召回准确率 > 90% | mutation test |
| Skills startup 100 skills < 10K token | benchmark |
| MCP collapse + mcporter skill + chrome-mcp 工作 | E2E |
| Spec-driven artifact 显式可见 + W7 Web 通道集成 | E2E |
| AI 质量 measurable metric 上线 | hive_metrics 表 query |

---

## §6 完成后下一步

Layer 3 ship → **Layer 4 启动**（W13 工具广度 + W14 ACP 生态 + W15 Multi-agent，可分批多 quarter）

详细 spec 见 `SPEC-LAYER4-5-W13-W16.md`。

---

*— End of Layer 3 Spec —*
