# KB P0 Tree-Mode 后端闭环 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

> **状态：已实施并归档。** 2026-05-16 已落地 Markdown tree-mode 后端闭环、平台级 `kb.read`、KB 三工具、binding resolver、evidence ledger、服务端 citation 汇总和基础 ACL/状态/时间边界测试。P1/P2 已继续补齐 API、前端、资产、PDF/DOCX 和客服试点；后续遗留项见 `TODOS.md`。

**Goal:** 交付平台级 KB markdown tree-mode 后端闭环，让现有 Agent 能通过授权的 KB 三工具读取文档结构、取证节点原文，并生成可审计 citation。

**Architecture:** 新增 `internal/kb`，但不新建 router、quality、LLM、memory 或权限体系。KB 首发只支持 Markdown tree-mode：regex 建树、可选 thinning、airouter 摘要、PostgreSQL 存储、`KBBindingResolver`、`kb.doc.meta` / `kb.doc.structure` / `kb.section.text` 三个只读工具、服务端 evidence ledger。所有工具调用继续走现有 router/tool runtime/master 链路，Agent 可用知识由服务端 binding 解析，domain/owner/bound namespace/status/time 过滤在 KB store 层 fail closed。

**Tech Stack:** Go、PostgreSQL、现有 `internal/router`、`internal/tools`、`internal/store`、`internal/airouter`、`internal/domainpolicy`、`internal/agentquality`、table-driven tests。

---

## 0. 范围边界

### 做

- Markdown heading -> tree。
- Tree node 持久化。
- Namespace/document/tree-node/evidence 基础模型。
- KB binding 与 `KBBindingResolver`，让用户和 Agent 无需直接知道 namespace。
- 三个只读 KB 工具。
- 平台级 `kb.read` capability 口径。
- Evidence ledger 和 citation 校验最小实现。
- ACL/status/time fail-closed 测试。

### 不做

- 不做 PDF/DOCX ingest。
- 不做对象存储和图片资产。
- 不做前端页面。
- 不做客服状态机、webhook、人工升级。
- 不做 vector/hybrid。
- 不把 KB 文档存进 memory。
- 不引入 LiteLLM、OpenAI Agents SDK 或新 LLM SDK。

### PageIndex 源码校准

本计划吸收 PageIndex 的 tree-mode 机制，但按 Hive 生产边界重做：

- Markdown pipeline 对齐 PageIndex：跳过 fenced code block 内 heading，用 stack 建树。
- PageIndex `md_to_tree` 最终调用 `write_node_id`，所以对外 `node_id` 从 `0000` 开始，而不是 `0001`。
- `node_id` 必须在 tree thinning 完成后统一重排；被 thinning 删除的 child 不得保留可引用 ID。
- PageIndex Markdown retrieval 用 `line_num` 当 “page” 参数；Hive 改为 `node_id` 精确取节点，这是有意改进。
- PageIndex 只用以 triple backtick 开头的行识别 fenced code block，未处理 `~~~`；Hive 实现必须把 `~~~` 纳入测试并显式支持，或在 ingest 时把含 `~~~` 且可能包含 heading 的文档标记 degraded/fail。
- PageIndex 会忽略首个 heading 前的正文；Hive 不得静默丢内容。P0 推荐策略：首 heading 前有非空正文时生成 `title="Preamble"` 的前言节点，参与 `node_id` 重排和 evidence。
- 重复标题、跨级跳跃（例如 `# A` 后直接 `### B`）和空标题都必须有确定行为：重复标题靠 `node_path` 区分；跨级跳跃允许但 `node_path` 保持 ordinal；空标题返回 ingest 错误。
- PageIndex client/workspace、LiteLLM 调用和 Agents SDK demo 不进入生产设计。
- Scope/owner/domain 来自现有 auth、route decision、tool context 或服务端配置，不能来自 LLM 工具参数。

## 1. 文件结构

### 1.0 与现有系统的兼容接入点

P0 不能实现成独立 PageIndex 服务。它必须按以下接入点进入 Hive：

1. `internal/router`：只新增 capability 和 tool profile，继续使用 `EvaluateToolPolicy` / `CheckCapabilityGate`。
2. `internal/tools`：新增 KB wrapper 注册到现有 MCP host；工具 schema 不暴露 owner/domain/session。
3. `internal/toolctx` + `internal/auth`：wrapper 从 context 派生 session、user、trace、turn，再构造 `kb.Scope` / `EvidenceScope`。
4. `internal/store`：沿用现有 PostgreSQL migration 和 store 注入方式，不能引入 PageIndex JSON workspace。
5. `internal/airouter`：summary/doc description 通过已有 router/provider/model 配置调用，不能直连 LiteLLM。
6. `internal/domainpolicy`：只读 KB 能力作为平台 read capability 接入，不能被客服外发权限绑死。
7. `internal/agentquality`：KB retrieval/evidence violation 只扩展 event name 和 attributes，不创建独立质量系统。

兼容验收红线：

- 新工具出现在现有 tool catalog / tool_search / route decision 中。
- 现有 memory、filesystem、IM、customer_service 工具策略测试不因 KB 注册而改变。
- 关闭或未配置 KB service 时，工具应返回可恢复错误，不影响其他工具注册。
- `kb_search` 保留兼容，但 P0 文档和 prompt 不把它作为 tree-mode 主路径。
- `hostToolGroups` 需要新增 `kb` 组；KB 三工具不要塞进现有 `customer_service` 组。
- `hostToolPolicyProfiles["master_direct"]` 和 `internal/config/defaults.go` 的 `defaultToolPolicyConfig()` 都要纳入 `group:kb` / `kb` 组；只改 router 静态 profile 不够，因为默认配置构造函数当前只枚举 `fs/runtime/web/lsp/agent/discovery`。
- `DomainCustomerService` 当前默认要求 `external.send` 且 disabled，P0 的 `kb.read` 不能依赖该 domain admission。客服试点在 P2 通过 KB binding 配置可用 namespace，而不是让客服域拥有 KB capability。
- 普通用户和 Agent prompt 不需要知道 namespace；namespace 由服务端 binding 解析，工具里的 `namespace_id` 只做可选 narrowing。
- 未配置任何 effective binding 时，KB 工具必须返回可恢复的 “no KB bound” 结果，不能自动查全量 namespace。

### 1.0.1 用户入口与 Agent 接入

P0 后端必须为 P1/P2 的产品入口打好基础：

- 管理员后续在 P1 UI/API 中把 namespace 绑定到 Agent、domain、session template、tenant 或 user。
- 普通用户只在聊天里发问；Agent 通过工具自动取证，并在回答中展示服务端 citation。
- Agent runtime 在每轮工具调用前从 context 解析 `user/session/agent/domain/tenant`，调用 `KBBindingResolver` 得到 allowed namespace set。
- prompt/tool description 只提示“当前会话有可用 KB，可先调用 `kb.doc.meta`”；不把 binding 当作 prompt 里的授权事实。
- `kb.doc.meta` 支持不传 `namespace_id`，默认列出 bound namespaces 下的 active documents；传 `namespace_id` 时必须收窄到 bound set 内。
- `kb.doc.structure` 和 `kb.section.text` 的 `doc_id` / `node_ids` 仍由模型选择，但 document 和 node 必须属于 resolver 产出的 bound set。
- no-evidence 时，工具返回结构化空结果；回答阶段必须拒答、降级或交给业务升级，不能用泛化知识冒充文档证据。

### 新增 `internal/kb`

| 文件 | 职责 |
|---|---|
| `internal/kb/types.go` | Namespace、Document、TreeNode、EvidenceRef、查询 scope 类型 |
| `internal/kb/errors.go` | KB 领域错误，统一 fail closed 语义 |
| `internal/kb/store.go` | Store interface |
| `internal/kb/memory_store.go` | 单测用内存实现 |
| `internal/kb/pg_store.go` | PostgreSQL 实现 |
| `internal/kb/binding.go` | KB binding 类型、校验和 effective binding 查询 |
| `internal/kb/binding_resolver.go` | 从 user/session/agent/domain/template 解析 allowed namespace set |
| `internal/kb/tree_builder.go` | Markdown heading 提取和 stack 建树 |
| `internal/kb/tree_thinning.go` | token 阈值合并碎节点 |
| `internal/kb/tree_summary.go` | summary/prefix_summary 生成，走 airouter LLM client 或测试 fake |
| `internal/kb/ingest.go` | markdown ingest pipeline 编排 |
| `internal/kb/evidence.go` | Evidence ledger、EvidenceToken、输出校验 |
| `internal/kb/tool_doc_meta.go` | `kb.doc.meta` 执行逻辑 |
| `internal/kb/tool_doc_structure.go` | `kb.doc.structure` 执行逻辑 |
| `internal/kb/tool_section_text.go` | `kb.section.text` 执行逻辑 |
| `internal/kb/context.go` | 从服务端上下文派生 `Scope` / `EvidenceScope` 的 helper |
| `internal/kb/*_test.go` | 单元测试 |

### 修改现有文件

| 文件 | 改动 |
|---|---|
| `internal/router/capability_entry.go` | 新增平台级 `CapabilityKBRead = "kb.read"` |
| `internal/router/capability_registry.go` | 注册 `kb.doc.meta` / `kb.doc.structure` / `kb.section.text`，保留 `kb_search` 但不作为 tree-mode 主路径 |
| `internal/router/types_test.go` | 覆盖 KB 新工具 profile |
| `internal/config/defaults.go` | `defaultToolPolicyConfig()` 纳入 `kb` group，并让 `master_direct` profile 可用 KB 三工具 |
| `internal/store/postgres_migrate.go` | 新增 KB 表 migration |
| `internal/tools/tools.go` 或相邻 builtin 注册文件 | 注册 KB 三工具 wrapper，wrapper 只做参数解析和调用 `kb.Service` |
| `internal/bootstrap/server.go` / `internal/cli/app.go` | 注入 KB store/service 到 tool registry，若当前注册点不支持依赖注入，先做最小构造函数扩展 |
| `internal/domainpolicy/policy.go` | 只读 KB capability 不依赖 `external.send`；客服外部写入仍保留外发门禁 |

### 1.1 工具依赖注入口径

`internal/tools.RegisterBuiltinTools` 当前已有 variadic `agentSpawnerI ...interface{}`，P0 不要扩大主签名。新增一个窄接口并通过 variadic 注入：

```go
type KBService interface {
    ResolveBinding(ctx context.Context, input kb.BindingResolveInput) ([]string, error)
    DocMeta(ctx context.Context, scope kb.Scope, input kb.DocMetaInput) (*kb.DocMetaResult, error)
    DocStructure(ctx context.Context, scope kb.Scope, input kb.DocStructureInput) (*kb.DocStructureResult, error)
    SectionText(ctx context.Context, scope kb.Scope, evidence kb.EvidenceScope, input kb.SectionTextInput) (*kb.SectionTextResult, error)
}
```

`ResolveBinding` 可由 `kb.Service` 内部委托 `KBBindingResolver`，也可作为独立 optional dependency 注入 wrapper；但 wrapper 必须先解析 allowed namespace set，再构造 `kb.Scope`。不要让工具调用直接拿用户传入的 `namespace_id` 构造 scope。

注册策略二选一，但必须在测试中固定：

- 推荐：始终注册 KB 三工具；`KBService == nil` 时 executor 返回 `recoverable` 工具错误，提示 KB service 未启用。
- 可选：未注入时不注册 KB 三工具；同时 `tool_search` 和 catalog 测试必须证明不会出现空壳工具。

首发推荐前者，因为它不影响其他工具注册，也能让部署配置错误可观测。

## 2. 数据模型

### 2.1 Go 类型

```go
package kb

import "time"

type OwnerScope string

const (
    OwnerScopeUser   OwnerScope = "user"
    OwnerScopeTenant OwnerScope = "tenant"
    OwnerScopeSystem OwnerScope = "system"
)

type DocumentStatus string

const (
    DocumentDraft    DocumentStatus = "draft"
    DocumentActive   DocumentStatus = "active"
    DocumentArchived DocumentStatus = "archived"
    DocumentRevoked  DocumentStatus = "revoked"
)

type Namespace struct {
    ID                     string
    Name                   string
    DomainID               string
    OwnerScope             OwnerScope
    OwnerID                string
    IndexStrategy          string
    ThinningEnabled        bool
    ThinningTokenThreshold int
    SummaryTokenThreshold  int
    SummaryModel           string
    CreatedAt              time.Time
    UpdatedAt              time.Time
}

type Document struct {
    ID          string
    NamespaceID string
    DomainID    string
    OwnerScope  OwnerScope
    OwnerID     string
    SourceURI   string
    Title       string
    ContentHash string
    Version     string
    Status      DocumentStatus
    EffectiveAt time.Time
    ExpiresAt   *time.Time
    CreatedAt   time.Time
    UpdatedAt   time.Time
}

type TreeNode struct {
    ID            string
    DocumentID    string
    NamespaceID   string
    DomainID      string
    OwnerScope    OwnerScope
    OwnerID       string
    ParentNodeID  *string
    NodePath      string
    Title         string
    Level         int
    Text          string
    TokenCount    int
    Summary       string
    PrefixSummary string
    StartLine     int
    EndLine       int
    ContentHash   string
    CreatedAt     time.Time
}

type Scope struct {
    DomainID          string
    OwnerScope        OwnerScope
    OwnerID           string
    NamespaceIDs      []string // effective bindings resolved by server
    NamespaceNarrowing string   // optional model/API selector; must be in NamespaceIDs
    Now               time.Time
}

type BindingType string

const (
    BindingTypeAgent           BindingType = "agent"
    BindingTypeDomain          BindingType = "domain"
    BindingTypeSessionTemplate BindingType = "session_template"
    BindingTypeSession         BindingType = "session"
    BindingTypeTenant          BindingType = "tenant"
    BindingTypeUser            BindingType = "user"
    BindingTypeSystem          BindingType = "system"
)

type Binding struct {
    ID            string
    DomainID      string
    OwnerScope    OwnerScope
    OwnerID       string
    NamespaceID   string
    BindingType   BindingType
    BindingTarget string
    Enabled       bool
    EffectiveAt   time.Time
    ExpiresAt     *time.Time
    CreatedBy     string
    CreatedAt     time.Time
    UpdatedAt     time.Time
}

type BindingResolveInput struct {
    DomainID          string
    OwnerScope        OwnerScope
    OwnerID           string
    UserID            string
    TenantID          string
    AgentID           string
    SessionTemplateID string
    SessionID         string
    Now               time.Time
}

type EvidenceScope struct {
    SessionID string
    TurnID    string
    TraceID   string
    ToolCallID string
    DomainID  string
    OwnerScope OwnerScope
    OwnerID   string
    Now       time.Time
}
```

### 2.2 PostgreSQL 表

```sql
CREATE TABLE IF NOT EXISTS kb_namespaces (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  domain_id TEXT NOT NULL,
  owner_scope TEXT NOT NULL,
  owner_id TEXT NOT NULL,
  index_strategy TEXT NOT NULL DEFAULT 'tree',
  thinning_enabled BOOLEAN NOT NULL DEFAULT false,
  thinning_token_threshold INT NOT NULL DEFAULT 0,
  summary_token_threshold INT NOT NULL DEFAULT 200,
  summary_model TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE(owner_scope, owner_id, domain_id, name)
);

CREATE TABLE IF NOT EXISTS kb_documents (
  id TEXT PRIMARY KEY,
  namespace_id TEXT NOT NULL REFERENCES kb_namespaces(id) ON DELETE CASCADE,
  domain_id TEXT NOT NULL,
  owner_scope TEXT NOT NULL,
  owner_id TEXT NOT NULL,
  source_uri TEXT NOT NULL DEFAULT '',
  title TEXT NOT NULL,
  content_hash TEXT NOT NULL,
  version TEXT NOT NULL,
  status TEXT NOT NULL,
  effective_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  expires_at TIMESTAMPTZ NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE(namespace_id, content_hash, version)
);

CREATE INDEX IF NOT EXISTS idx_kb_documents_scope
  ON kb_documents(owner_scope, owner_id, domain_id, namespace_id, status);

CREATE TABLE IF NOT EXISTS kb_tree_nodes (
  id TEXT NOT NULL,
  document_id TEXT NOT NULL REFERENCES kb_documents(id) ON DELETE CASCADE,
  namespace_id TEXT NOT NULL REFERENCES kb_namespaces(id) ON DELETE CASCADE,
  domain_id TEXT NOT NULL,
  owner_scope TEXT NOT NULL,
  owner_id TEXT NOT NULL,
  parent_node_id TEXT NULL,
  node_path TEXT NOT NULL,
  title TEXT NOT NULL,
  level INT NOT NULL,
  text TEXT NOT NULL,
  token_count INT NOT NULL DEFAULT 0,
  summary TEXT NOT NULL DEFAULT '',
  prefix_summary TEXT NOT NULL DEFAULT '',
  start_line INT NOT NULL,
  end_line INT NOT NULL,
  content_hash TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY(document_id, id)
);

CREATE INDEX IF NOT EXISTS idx_kb_tree_nodes_scope
  ON kb_tree_nodes(owner_scope, owner_id, domain_id, namespace_id, document_id);

CREATE TABLE IF NOT EXISTS kb_bindings (
  id TEXT PRIMARY KEY,
  owner_scope TEXT NOT NULL,
  owner_id TEXT NOT NULL,
  domain_id TEXT NOT NULL,
  namespace_id TEXT NOT NULL REFERENCES kb_namespaces(id) ON DELETE CASCADE,
  binding_type TEXT NOT NULL,
  binding_target TEXT NOT NULL,
  enabled BOOLEAN NOT NULL DEFAULT true,
  effective_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  expires_at TIMESTAMPTZ NULL,
  created_by TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_kb_bindings_resolve
  ON kb_bindings(owner_scope, owner_id, domain_id, binding_type, binding_target, enabled);

CREATE INDEX IF NOT EXISTS idx_kb_bindings_namespace
  ON kb_bindings(owner_scope, owner_id, domain_id, namespace_id, enabled);

CREATE TABLE IF NOT EXISTS kb_evidence_events (
  id TEXT PRIMARY KEY,
  session_id TEXT NOT NULL DEFAULT '',
  turn_id TEXT NOT NULL DEFAULT '',
  trace_id TEXT NOT NULL DEFAULT '',
  domain_id TEXT NOT NULL,
  namespace_id TEXT NOT NULL,
  document_id TEXT NOT NULL,
  document_version TEXT NOT NULL,
  node_id TEXT NOT NULL,
  node_path TEXT NOT NULL,
  owner_scope TEXT NOT NULL,
  owner_id TEXT NOT NULL,
  evidence_token TEXT NOT NULL,
  citation_text TEXT NOT NULL DEFAULT '',
  verified BOOLEAN NOT NULL DEFAULT false,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_kb_evidence_events_turn
  ON kb_evidence_events(session_id, turn_id, trace_id);
```

## 3. Task 1：核心模型和内存 Store

**Files:**

- Create: `internal/kb/types.go`
- Create: `internal/kb/errors.go`
- Create: `internal/kb/store.go`
- Create: `internal/kb/memory_store.go`
- Create: `internal/kb/binding.go`
- Create: `internal/kb/binding_resolver.go`
- Test: `internal/kb/store_test.go`
- Test: `internal/kb/binding_resolver_test.go`

- [ ] **Step 1: 写 Store interface 和 fail-closed 测试**

测试必须覆盖：

```go
func TestMemoryStoreRejectsEmptyScope(t *testing.T) {
    store := kb.NewMemoryStore()
	    _, err := store.ListDocuments(context.Background(), kb.Scope{
	        DomainID: "",
	        OwnerScope: kb.OwnerScopeTenant,
	        OwnerID: "tenant-1",
	        NamespaceIDs: []string{"ns-1"},
	        Now: time.Now(),
	    })
	    require.ErrorIs(t, err, kb.ErrInvalidScope)
	}
	```

- [ ] **Step 2: 实现 Scope 校验**

规则：

```go
func ValidateScope(scope Scope) error {
    if scope.DomainID == "" || scope.OwnerScope == "" || scope.OwnerID == "" {
        return ErrInvalidScope
    }
    if len(scope.NamespaceIDs) == 0 {
        return ErrNoKBBinding
    }
    if scope.NamespaceNarrowing != "" && !contains(scope.NamespaceIDs, scope.NamespaceNarrowing) {
        return ErrNamespaceNotBound
    }
    return nil
}
```

补充规则：

- auth disabled 且没有服务端显式 system namespace 绑定时，不能自动给模型 tenant/system 权限；返回 `ErrInvalidScope`。
- `OwnerScopeSystem` 只能由服务端配置或 bootstrap 注入，不能由 tool args 推导。
- `NamespaceIDs` 必须来自 `KBBindingResolver`，不是模型参数。
- `NamespaceNarrowing` 是可选选择项，不是授权证明；store 查询仍必须带 owner/domain/bound namespace/status/time filter。

- [ ] **Step 2.1: 实现 BindingResolver**

测试必须覆盖：

- 没有 binding -> `ErrNoKBBinding`，不会返回所有 namespace。
- agent binding 命中 namespace。
- domain binding 命中 namespace。
- session template/session binding 命中 namespace。
- user/tenant binding 命中 namespace。
- `enabled=false`、未生效、已过期 binding 不命中。
- 多个有效 binding 取 namespace 并集并去重。
- 模型传入未绑定 `namespace_id` narrowing 时返回 `ErrNamespaceNotBound` 或 empty/not found。

建议接口：

```go
type BindingResolver interface {
    Resolve(ctx context.Context, input BindingResolveInput) ([]string, error)
}
```

resolver 只能读取服务端上下文和 `kb_bindings`，不能读取 prompt 或 tool args 里的授权字段。

- [ ] **Step 3: 实现内存 Store**

内存 Store 只服务测试：

- 保存 namespace/document/tree nodes/bindings。
- `ListDocuments` 只返回 scope 匹配、`active`、`effective_at <= now`、`expires_at == nil || expires_at > now`。
- `GetStructure` 不返回 `Text`。
- `GetSectionText` 必须按 scope 和 document status 过滤。

- [ ] **Step 4: 运行测试**

Run:

```bash
go test ./internal/kb -run '(MemoryStore|Scope)' -count=1
```

Expected: PASS。

## 4. Task 2：Markdown Tree Builder

**Files:**

- Create: `internal/kb/tree_builder.go`
- Test: `internal/kb/tree_builder_test.go`

- [ ] **Step 1: 写建树测试**

覆盖：

- `#` 到 `###` 层级构建。
- code block 内 `# not heading` 不被识别。
- triple backtick 和 tilde fence 内 heading 都不被识别，或 tilde fence 被明确拒绝并产生 degraded/fail 结果。
- node_id 从 `0000` 开始，preorder 递增，4 位 zero padded。
- `start_line/end_line` 正确。
- `node_path` 稳定。
- 首个 heading 前正文生成 `Preamble` 节点或按计划固定的错误策略处理。
- 重复标题不覆盖节点，跨级跳跃仍能生成可导航 tree。

示例：

```go
func TestBuildMarkdownTreeSkipsCodeBlockHeadings(t *testing.T) {
    md := "# A\ntext\n```go\n# not heading\n```\n## B\nbody\n"
    nodes, err := kb.BuildMarkdownTree(md, kb.BuildTreeOptions{})
    require.NoError(t, err)
    require.Len(t, nodes, 2)
    require.Equal(t, "A", nodes[0].Title)
    require.Equal(t, "B", nodes[1].Title)
}
```

- [ ] **Step 2: 实现 heading 提取**

实现约束：

- 使用正则 `^(#{1,6})\s+(.+)$`。
- 遇到以 ``` 或 `~~~` 开头的行切换 code block 状态；如果实现选择不支持 `~~~`，必须在 ingest 前检测并返回结构化错误。
- 空文档返回结构化错误，不 panic。

- [ ] **Step 3: 实现 stack 建树需要的 flat node**

P0 存储可以先持久化 flat list，`ParentNodeID` 和 `NodePath` 必须正确。
`NodePath` 不应直接等于可变 `node_id`；建议使用 heading ordinal path（例如 `1`, `1.2`, `1.2.1`）或等价稳定路径，便于同一文档版本内定位和前端展示。

- [ ] **Step 4: 运行测试**

Run:

```bash
go test ./internal/kb -run 'TreeBuilder' -count=1
```

Expected: PASS。

## 5. Task 3：Tree Thinning 与 Summary

**Files:**

- Create: `internal/kb/tree_thinning.go`
- Create: `internal/kb/tree_summary.go`
- Test: `internal/kb/tree_thinning_test.go`
- Test: `internal/kb/tree_summary_test.go`

- [ ] **Step 1: 写 thinning 测试**

覆盖：

- 默认未开启 thinning 时不合并任何节点。
- 显式开启后，小于阈值的父节点合并 children text。
- 被合并 children 不再作为独立导航节点返回。
- 合并后重新分配 `node_id`，从 `0000` 连续递增。
- 合并后 `ContentHash` 更新。

- [ ] **Step 2: 实现 token counter 注入**

不要在 `internal/kb` 里直接绑定某个 tokenizer。定义：

```go
type TokenCounter interface {
    CountTokens(text string) int
}
```

生产实现可以先用保守估算 `len([]rune(text))/2`；如果现有 airouter/token 工具已有可复用 token counter，则优先薄封装复用，禁止在 `internal/kb` 里引入 LiteLLM 或独立 tokenizer SDK。测试使用 fake。

- [ ] **Step 3: 写 summary short-circuit 测试**

覆盖：

- `TokenCount < SummaryTokenThreshold` 时不调用 LLM，`Summary` 直接等于 text。
- 分支节点写 `PrefixSummary`，叶子节点写 `Summary`。

- [ ] **Step 4: 定义 SummaryGenerator 接口**

```go
type SummaryGenerator interface {
    Summarize(ctx context.Context, text string, model string) (string, error)
}
```

生产 adapter 在 P0 可以薄封装 airouter LLM client；测试用 fake。

- [ ] **Step 5: 运行测试**

Run:

```bash
go test ./internal/kb -run '(Thinning|Summary)' -count=1
```

Expected: PASS。

## 6. Task 4：PostgreSQL Migration 和 PG Store

**Files:**

- Modify: `internal/store/postgres_migrate.go`
- Create: `internal/kb/pg_store.go`
- Test: `internal/kb/pg_store_test.go`
- Modify/Test: `internal/store/postgres_migrate_test.go`

- [ ] **Step 1: 写 migration 测试**

断言表存在：

- `kb_namespaces`
- `kb_documents`
- `kb_tree_nodes`
- `kb_bindings`
- `kb_evidence_events`

- [ ] **Step 2: 修改 migration**

把 SQL 加到现有 migration 流程中，遵循当前 `postgres_migrate.go` 风格。不要创建单独 migration runner。

- [ ] **Step 3: 写 PG store owner/domain/status/time 测试**

关键用例：

- owner A 查不到 owner B。
- domain A 查不到 domain B。
- 没有 binding 不返回文档。
- disabled/expired binding 不返回文档。
- `NamespaceNarrowing` 不在 bound set 内时不返回文档。
- archived/revoked 不返回。
- expired 不返回。

- [ ] **Step 4: 实现 PG store**

查询必须把 scope filter 写进 SQL，不允许先查出再在 Go 层过滤。

- [ ] **Step 5: 运行测试**

Run:

```bash
go test ./internal/kb ./internal/store -run '(KB|PostgresMigrate)' -count=1
```

Expected: PASS。

## 7. Task 5：Markdown Ingest Pipeline

**Files:**

- Create: `internal/kb/ingest.go`
- Test: `internal/kb/ingest_test.go`

- [ ] **Step 1: 写 ingest 成功测试**

输入 markdown：

```markdown
# Refund Policy
Intro
## 7-day Return
Conditions
```

期望：

- 创建 active document。
- 写入 namespace/document/tree nodes。
- document `ContentHash` 稳定。
- 重复 ingest 同内容返回已有 document 或明确 duplicate error。

- [ ] **Step 2: 写 ingest 失败测试**

覆盖：

- 空 markdown fail。
- 无 heading fail。
- summary generator 失败时 document 不进入 active。

- [ ] **Step 3: 实现 IngestMarkdown**

签名：

```go
func (s *Service) IngestMarkdown(ctx context.Context, scope Scope, input IngestMarkdownInput) (*Document, error)
```

`input` 包含 title/sourceURI/version/content/effective/expires。

ingest 是管理动作，不走模型工具 narrowing。创建 namespace/document 后必须能被 `kb_bindings` 绑定；P0 单测可直接调用 store 创建 binding，P1 再补管理 API。

- [ ] **Step 4: 运行测试**

Run:

```bash
go test ./internal/kb -run 'Ingest' -count=1
```

Expected: PASS。

## 8. Task 6：KB 三工具执行逻辑

**Files:**

- Create: `internal/kb/tool_doc_meta.go`
- Create: `internal/kb/tool_doc_structure.go`
- Create: `internal/kb/tool_section_text.go`
- Test: `internal/kb/tool_test.go`

- [ ] **Step 1: 写 `kb.doc.meta` 测试**

断言：

- 返回授权 namespace 下 active documents。
- `namespace_id` 省略时返回所有 bound namespaces 下 active documents。
- `namespace_id` 传入且属于 bound set 时只返回该 namespace。
- `namespace_id` 传入但未绑定时返回 empty/not found，不泄漏 namespace 是否存在。
- 没有 binding 时返回可恢复 no-KB-bound 结果，不回退全量 namespace。
- 不返回 text。
- 包含 doc_id/title/version/status/doc_description。

- [ ] **Step 2: 写 `kb.doc.structure` 测试**

断言：

- 返回 tree nodes。
- 不包含 `Text`。
- 包含 title/node_id/node_path/summary/prefix_summary。
- `doc_id` 必须属于 bound namespace set；`namespace_id` 仅可选 narrowing。

- [ ] **Step 3: 写 `kb.section.text` 测试**

断言：

- 返回指定 node 原文。
- 生成 `EvidenceToken`。
- 写入 evidence ledger。
- 未授权 node 返回 not found。
- `doc_id/node_ids` 必填；`namespace_id` 可选。

- [ ] **Step 4: 实现三工具逻辑**

这些函数只依赖 `kb.Store` 和 `kb.Service`，不依赖 router。

- [ ] **Step 5: 运行测试**

Run:

```bash
go test ./internal/kb -run 'Tool' -count=1
```

Expected: PASS。

## 9. Task 7：Router Capability 注册

**Files:**

- Modify: `internal/router/capability_entry.go`
- Modify: `internal/router/capability_registry.go`
- Modify: `internal/router/types_test.go`

- [ ] **Step 1: 写 router profile 测试**

断言：

- `kb.doc.meta` risk 是 `read_only`。
- `kb.doc.structure` risk 是 `read_only`。
- `kb.section.text` risk 是 `read_only`。
- 三者 capability 都包含 `kb.read`。
- 三者 Domain 都是 `kb`，不属于 `customer_service`。
- `HostToolPolicyGroups()["kb"]` 包含三者；`HostToolPolicyGroups()["customer_service"]` 不新增这三者。
- `HostToolPolicyProfiles()["master_direct"]` 包含 `group:kb` 或三工具名。
- `defaultToolPolicyConfig()` 生成的 groups 包含 `kb`，profiles 中 `master_direct` 展开后可见 KB 三工具。
- `kb_search` 仍保留但不在 tree-mode 文档中作为主路径。

- [ ] **Step 2: 新增 capability**

```go
CapabilityKBRead Capability = "kb.read"
```

- [ ] **Step 3: 注册 builtin tool rules**

新增：

```go
"kb.doc.meta":      {Domain: "kb", Invocation: InvocationDirectTool, Risk: RiskReadOnly, ReadOnly: true, Capabilities: []Capability{CapabilityKBRead}},
"kb.doc.structure": {Domain: "kb", Invocation: InvocationDirectTool, Risk: RiskReadOnly, ReadOnly: true, Capabilities: []Capability{CapabilityKBRead}},
"kb.section.text":  {Domain: "kb", Invocation: InvocationDirectTool, Risk: RiskReadOnly, ReadOnly: true, Capabilities: []Capability{CapabilityKBRead}},
```

同时新增：

```go
hostToolGroups["kb"] = []string{"kb.doc.meta", "kb.doc.structure", "kb.section.text"}
```

不要修改 `hostToolGroups["customer_service"]` 来承载 KB 三工具；`customer_service` 组继续只放客服业务动作和兼容 `kb_search`。

同时修改 `hostToolPolicyProfiles`：

```go
"master_direct": {
    "group:fs", "group:runtime", "group:web", "group:lsp", "group:agent", "group:discovery", "group:kb",
    "create_tool", "remove_tool",
    "skill", "memory", "question", "taskboard", "batch",
    "send_im_message", "feishu_api",
},
```

并修改 `internal/config/defaults.go`：

```go
for _, name := range []string{"fs", "runtime", "web", "lsp", "agent", "discovery", "kb"} {
    ...
}
```

- [ ] **Step 4: 运行测试**

Run:

```bash
go test ./internal/router -run '(KB|Capability|ToolProfile)' -count=1
```

Expected: PASS。

## 10. Task 8：Tool Wrapper 注册

**Files:**

- Modify: `internal/tools/tools.go` 或当前 builtin 注册文件
- Create/Modify: `internal/tools/kb_tools.go`
- Create/Modify: `internal/kb/context.go`
- Test: `internal/tools/kb_tools_test.go`

- [ ] **Step 1: 写工具参数测试**

断言：

- 工具 schema 不暴露 `owner_id` / `owner_scope` / `domain_id` 这类授权字段。
- 缺 auth user、session 或服务端无法派生 scope 时返回工具错误。
- 模型参数里显式传 `owner_id` / `owner_scope` / `domain_id` / `session_id` 会被拒绝或忽略，绝不参与授权。
- `kb.doc.meta` 缺 `namespace_id` 是合法路径，会列出 bound namespaces；`namespace_id` 只是可选 narrowing，仍需通过服务端 binding/scope 过滤。
- 模型传入未绑定 `namespace_id` 不扩大权限，返回 empty/not found 或 recoverable `namespace_not_bound`。
- 没有 effective binding 时返回 recoverable `no_kb_bound`，不返回所有 namespace。
- `kb.doc.structure` 缺 `doc_id` 返回工具错误。
- `kb.section.text` 缺 `doc_id/node_ids` 返回工具错误。

- [ ] **Step 2: 实现 wrapper**

Wrapper 责任：

- 解析 JSON args。
- 从 `context.Context` 中的 auth user、`toolctx.GetSessionID(ctx)`、`toolctx.ToolContext` trace/turn、agent id、session template id，以及 route/domain runtime context 派生 `BindingResolveInput`。
- 调用 `KBBindingResolver` 得到 allowed namespace set，再构造 `kb.Scope` 和 `EvidenceScope`。
- 只把可选 `namespace_id` narrowing、`doc_id`、`node_ids` 这类模型选择项传给 KB service。
- 调用 `kb.Service`。
- 返回 JSON。
- 注册 `mcphost.ToolDefinition{IsConcurrencySafe: true}`；默认 `Core: false`，通过 `tool_search` / recall / route decision 发现，不塞进默认 system prompt。
- 若 `KBService == nil`，返回 `IsError=true` 且 `FailureType` 可恢复的工具结果，不能 panic，也不能影响其他 builtin tools。

Wrapper 不做：

- 不做 ACL 业务判断，ACL 在 store/service。
- 不直接访问 DB。
- 不调用 LLM。
- 不相信模型传入的 owner/domain/session 字段。
- 不把 `session.AllowedToolInputs` 当 ACL；它只能收窄 action/operation，KB 权限仍由 service/store 判断。

- [ ] **Step 3: 运行测试**

Run:

```bash
go test ./internal/tools -run 'KB' -count=1
```

Expected: PASS。

## 11. Task 9：Evidence 校验和质量事件最小挂钩

**Files:**

- Create: `internal/kb/evidence.go`
- Modify: `internal/agentquality/types.go`
- Test: `internal/kb/evidence_test.go`
- Test: `internal/agentquality/types_test.go`

- [ ] **Step 1: 写 Evidence ledger 测试**

覆盖：

- 本轮存在 token -> verified。
- 非本轮 token -> violation。
- token 格式合法但 owner/domain 不匹配 -> violation。
- token 格式合法但 session/turn/trace 不匹配 -> violation。

- [ ] **Step 2: 新增 event names**

```go
EventKBRetrieval         EventName = "quality.kb_retrieval"
EventKBEvidenceViolation EventName = "quality.kb_evidence_violation"
```

- [ ] **Step 3: 实现 VerifyEvidenceRefs**

签名：

```go
func (s *Service) VerifyEvidenceRefs(ctx context.Context, scope EvidenceScope, refs []EvidenceRef) ([]EvidenceRef, []EvidenceViolation, error)
```

`EvidenceScope` 必须由服务端上下文派生，不能来自模型输出。

- [ ] **Step 4: 运行测试**

Run:

```bash
go test ./internal/kb ./internal/agentquality -run '(Evidence|KB)' -count=1
```

Expected: PASS。

## 12. Task 10：Domain Policy 调整

**Files:**

- Modify: `internal/domainpolicy/policy.go`
- Test: `internal/domainpolicy/policy_test.go`

- [ ] **Step 1: 写只读 KB 不依赖 external send 的测试**

断言：

- `kb.read` 是只读能力。
- `customer_service` 外部写入仍要求 `external.send`。
- 用户文本里出现 `customer_service` 不会授权 KB。
- `DomainCustomerService` disabled 时，平台 `kb.read` 仍可在非客服域/通用域按授权 namespace 读取。
- `customer_service.escalate` / `external.send` 没有通过时，不影响 `kb.doc.*` read-only 工具策略。

- [ ] **Step 2: 调整 policy 结构**

如当前 `Policy.RequiredCapability` 不支持按操作拆分，P0 只新增注释和测试保护现有行为，不强行大改 policy。需要更大调整时另开工具/权限计划。

- [ ] **Step 3: 运行测试**

Run:

```bash
go test ./internal/domainpolicy ./internal/router -run '(CustomerService|KB|Admission)' -count=1
```

Expected: PASS。

## 13. Task 11：P0 集成验收

**Files:**

- Test: `internal/kb/integration_test.go`

- [ ] **Step 1: 写端到端后端测试**

流程：

1. 创建 namespace。
2. ingest markdown。
3. 创建 agent/domain/session binding。
4. 用 wrapper runtime context 解析 allowed namespace set。
5. 不传 `namespace_id` 调 `DocMeta`。
6. 调 `DocStructure`。
7. 调 `SectionText`。
8. 校验 evidence token。

- [ ] **Step 2: 写跨租户回归测试**

owner A 的同名 namespace/doc，owner B 不得看到。

- [ ] **Step 2.1: 写 binding 回归测试**

覆盖：

- no binding -> `kb.doc.meta` 返回 no-KB-bound，不泄漏文档。
- agent binding -> namespace 可见。
- user 不能通过传入其他 `namespace_id` 覆盖 binding。
- disabled binding -> 不可见。
- 省略 `namespace_id` 通过 bound namespaces 正常工作。

- [ ] **Step 3: 跑 P0 回归**

Run:

```bash
go test ./internal/kb ./internal/router ./internal/tools ./internal/domainpolicy ./internal/agentquality -run '(KB|Evidence|Tree|Namespace|ACL|CustomerService)' -count=1
```

Expected: PASS。

- [ ] **Step 4: 跑全量 Go 测试**

Run:

```bash
go test ./... -count=1
```

Expected: PASS。

## 13.1 细节红线清单

实现前逐条确认：

| 类别 | 红线 |
|---|---|
| Tool schema | `kb.doc.meta` 允许可选 `namespace_id`/`query`/`limit`；`kb.doc.structure` 允许可选 `namespace_id` 和必填 `doc_id`；`kb.section.text` 允许可选 `namespace_id` 和必填 `doc_id/node_ids`。不得出现 `owner_id`、`owner_scope`、`domain_id`、`session_id`、`agent_id` 这类授权字段。 |
| Binding resolver | `KBBindingResolver` 从服务端 user/session/agent/domain/template/tenant context 解析 allowed namespace set；prompt hint 不是 ACL；无 binding 时 fail closed。 |
| Scope 派生 | owner 优先来自 `auth.UserFrom(ctx)`；session 来自 `toolctx.GetSessionID(ctx)`；turn/trace/tool_call 来自 `toolctx.ToolContext`；domain 来自 route/domain runtime context 或服务端绑定，不能从工具 args 派生；`NamespaceIDs` 来自 binding resolver。 |
| Auth disabled | 没有 auth user 时只能访问服务端显式配置的 system namespace；否则 fail closed，不能把空 user 当公共租户。 |
| 错误语义 | 未授权 document/node 一律返回 not found/empty，不暴露存在性；无 KB binding 返回可恢复 no-KB-bound；缺上下文返回可恢复工具错误；KB service 未注入不影响其他工具。 |
| 事务 | ingest 要么完整写入 active document 和所有 tree nodes，要么失败后不产生 active document；summary 失败不得留下半 active 文档。 |
| 幂等 | 同 namespace + content_hash + version 重复 ingest 返回已有 document 或明确 duplicate error，不能生成多份 active 重复文档。 |
| 文本大小 | `kb.section.text` 必须限制 node_ids 数量和总输出字节/token；超过限制返回可恢复错误，要求缩小节点范围。 |
| Structure 输出 | `kb.doc.structure` 永不返回 `Text`；只返回 title/node_id/node_path/summary/prefix_summary/start_line/end_line/children。 |
| Evidence token | token 必须绑定 session/turn/trace/document/version/node/scope，不能只是 node_id；验证只认本轮 ledger。 |
| Metrics labels | doc_id/node_id/user_id/evidence_token 不进 metric label；只允许低基数字段如 domain/namespace/strategy/status/failure_type。 |
| Tool visibility | router group/profile、`defaultToolPolicyConfig()` 和 model visibility/recall 都要覆盖 KB；测试必须证明 `master_direct` 可调用三工具。 |
| Backward compatibility | `kb_search`、`customer_service` 三旧工具不删除；router 旧测试需保持通过。 |
| Prompt 引导 | P0 prompt/tool description 要引导模型在问题涉及政策、SOP、FAQ、规范、业务文档时先调用 `kb.doc.meta`，通常不传 `namespace_id`；再 `kb.doc.structure`；最后用 tight node_ids 调 `kb.section.text`；禁止要求“取整篇文档”。 |
| Migration | migration 遵循 `internal/store/postgres_migrate.go` 现有风格，使用 `IF NOT EXISTS`/`ADD COLUMN IF NOT EXISTS`；不得另起 migration runner。 |
| 测试数据 | 测试 markdown 必须覆盖 code block heading、重复标题、跨层级跳跃、空文档、无 heading、大节点、多 owner 同名 namespace。 |

## 14. 提交建议

建议按任务小提交：

1. `feat(kb): add core tree-mode models and stores`
2. `feat(kb): build markdown tree ingest pipeline`
3. `feat(kb): add tree retrieval tools and evidence ledger`
4. `feat(router): register platform kb read capabilities`
5. `test(kb): cover acl status and evidence boundaries`

## 15. P0 完成定义

- Markdown KB 可以 ingest 到 PG。
- Agent 不需要知道 namespace，可通过绑定解析和三工具取文档 meta、structure、section text。
- Structure 默认不含原文。
- Section text 写 evidence ledger。
- Citation 校验不依赖 LLM 自证。
- 跨 owner/domain/binding/namespace/status/time 的泄漏测试为红线。
- 没有新建 parallel router、parallel quality、parallel memory、parallel LLM SDK。

## 16. 2026-05-16 实施落地记录

本轮 P0 后端闭环已按“内嵌能力域”接入 Hive，而不是旁路 PageIndex/RAG 服务：

- `internal/kb` 已落地 tree-mode 核心模型、Markdown 建树、thinning、summary interface、memory store、PG store、binding resolver、三工具 service、evidence ledger。
- `internal/router` 已新增平台级 `kb.read`，`kb.doc.meta` / `kb.doc.structure` / `kb.section.text` 注册为 `kb` read-only 工具；`kb_search` 保留在 `customer_service` 兼容路径。
- `internal/config/defaults.go` 与 router profile 已包含 `group:kb`，默认 `master_direct` 可见 KB 三工具。
- `internal/tools` 已注册 KB wrapper，工具 schema 只暴露 `namespace_id` narrowing、`doc_id`、`node_ids`、`query`、`limit`，不暴露 owner/domain/session/user/tenant/agent 授权字段。
- `internal/bootstrap/server.go` 在 PG 可用时创建 `kb.NewService(kb.NewPGStore(pgPool))`，并同时注入工具注册和 Master evidence reader。
- `internal/master/react_processor.go` 在真实工具执行路径注入服务端派生的 `KBRuntimeContext`：默认 `owner_scope=user`、`owner_id=session.UserID/auth user`、`domain_id=RouteDecision.Intent.DomainID/generic`、`agent_id=master`。这些授权事实不来自工具参数。
- `kb.section.text` 写入本轮 evidence ledger 后，final assistant 消息会把当前 turn 的 evidence 汇总到 `metadata.citations`，WebSocket payload 和 `/api/v1/sessions/{id}/messages` 也透出 `citations`。
- P0 阶段不包含 KB 图片资产 ingest；当时 `IngestMarkdown` 对非 `asset://` 的 Markdown 图片引用返回 `ErrUnsupportedAsset`，避免本地路径或 base64 静默进入 active tree text。P2 已补齐图片资产 ingest 和 `asset_refs`。

P0 验收后的后续状态：

- P1 已补齐管理 API/前端创建 namespace、上传 Markdown、创建/停用 binding、查看 evidence、citation 透传和 QualityWorkbench 聚合。
- P2 已补齐 PDF/DOCX Markdown conversion、KB 图片资产、`kb_node_assets` / `asset_refs`、统一对象存储复用和客服试点。
- airouter-backed summary generator 已通过 `internal/bootstrap/kb_summary.go` 接入 `TaskSummary`，P0 的 `SummaryGenerator` interface 继续作为可替换抽象保留。
- tenant/system 资产 ACL resolver、大文件 multipart/streaming 上传、钉钉/企微 AttachmentDownloader 不阻塞 P0/P1/P2 归档，已纳入 `TODOS.md`。
