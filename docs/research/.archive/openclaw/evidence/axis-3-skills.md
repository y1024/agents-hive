# OpenClaw Axis 3: Skills System

## 1. 设计文档断言（来自 docs/）

### Skills 基础架构
- [HC] OpenClaw Skills 是 Skill.md 文档 + 可选 frontmatter 元数据，通过 `read` 工具按需加载 — `docs/concepts/system-prompt.md:105-121`
- [HC] System prompt 注入 `<available_skills>` 列表，每个 skill 包含 name/description/location，指导模型用 `read` 加载 — `docs/concepts/system-prompt.md:113-121`
- [HC] Skills 来自三个源：workspace (`SKILLS_DIR/` 下), managed (插件管理), bundled (内置) — 设计推断

### Skills 加载与可见性
- [HC] Progressive loading：Skill 文件仅在模型请求时加载，保持基础 prompt 小 — `docs/concepts/system-prompt.md:105-111`
- [HC] 模型通过显式 `read` 调用加载单个 skill 后才能遵循其指令 — `docs/concepts/system-prompt.md:28`
- [HC] Prompt 约束："never read more than one skill up front; only read after selecting" — `src/agents/system-prompt.ts:30-31`

### Frontmatter 与安全模型
- [HC] Skill frontmatter 支持 YAML 元数据：`user-invocable`, `disable-model-invocation`, `metadata.openclaw.install` — `src/agents/skills/frontmatter.test.ts:11-18`
- [HC] `user-invocable: no` 禁用用户直接调用（可被包装成 tool）；`disable-model-invocation: yes` 禁用模型自动使用 — `src/agents/skills/types.ts:35-38`
- [HC] Install specs 支持安全的包管理器（brew/node/go/uv/download）并进行验证过滤（拒绝 shell injection 风险） — `src/agents/skills/frontmatter.test.ts:40-59`

### Scope 与 Tenant 隔离
- [HC] Skills 可在 workspace/agent/session 级别过滤；支持 require/forbid 策略 — 代码推断：`src/agents/skills/filter.ts`
- [HC] Sub-agent sessions 仅注入 AGENTS.md 和 TOOLS.md bootstrap 文件，不包含完整 skills（保持小 context） — `docs/concepts/system-prompt.md:81-82`

## 2. 代码实现验证（grep + Read）

### Frontmatter 解析与验证
- [HC] `resolveSkillInvocationPolicy()` 解析 frontmatter，支持 "yes"/"no" 字符串 → 布尔值转换 — `src/agents/skills/frontmatter.test.ts:11-18`
- [HC] `resolveOpenClawMetadata()` 从 JSON frontmatter 提取 install specs，验证安全性（拒绝模式匹配不安全的包名） — `src/agents/skills/frontmatter.test.ts:21-59`
- [HC] 安全检验例子：brew formula "wget --HEAD" 被拒（含 shell 元字符）；npm "file:../malicious" 被拒（相对路径逃逸） — `src/agents/skills/frontmatter.test.ts:40-51`

### Skill 路由与执行上下文
- [HC] SkillCommandSpec：{ name, skillName, description, dispatch? }，支持 dispatch 为 tool 调用 — `src/agents/skills/types.ts:40-57`
- [HC] SkillCommandDispatchSpec：{ kind: "tool", toolName, argMode: "raw"? } 用于 skill 命令到工具的映射 — `src/agents/skills/types.ts:40-49`
- [HC] SkillEligibilityContext：remote 平台检查 (hasBin/hasAnyBin 等) 用于条件性 skill 可见性 — `src/agents/skills/types.ts:73-80`

### Skills 源与刷新机制
- [HC] 三层 skill 源：workspace (`skills/` 目录) → plugin-managed → bundled — `src/agents/skills/workspace.ts`, `src/agents/skills/plugin-skills.ts`, `src/agents/skills/bundled-context.ts`
- [HC] `refresh()` 机制在会话启动时扫描并合并多源 skills — `src/agents/skills/refresh.ts`

## 3. 蓝军 Mutation

### Mutation 1: "Skills 真的是 progressive loading 还是全部 injection？"
- 命令：`grep -r "skillsPrompt\|available_skills" /src/agents --include="*.ts" | grep -v test`
- 结果：PASS — `src/agents/system-prompt.ts:20-35` 明确构建 `<available_skills>` XML，文本很小
- 断言确认：Skills 确实是 progressive loading；仅目录注入，文件内容按需加载

### Mutation 2: "Skill 是否支持参数化或输入验证？"
- 命令：`grep -r "skill.*param\|SkillParam\|input.*validation" /src/agents --include="*.ts"`
- 结果：FAIL — 未找到原生参数化支持；Skill 是静态文档 + 可选 dispatch 到 tool
- 断言：OpenClaw skills 本身无参数；通过 tool dispatch 获得参数支持

### Mutation 3: "安装检查（requires.bins）是否在运行时强制？"
- 命令：`grep -r "requires.*bin\|hasBin\|checkBin\|ensureBin" /src/agents --include="*.ts"`
- 结果：TBV — 代码中定义 `requires: { bins, anyBins, env, config }` 但执行路径不清楚
- 断言：Requires 定义存在但实施不确定；可能仅用于可见性过滤

## 4. 与 Hive 现状对照

### 借鉴
- Skill frontmatter metadata 用于声明式依赖和安全策略，避免运行时检查开销 — `src/agents/skills/frontmatter.test.ts:22-38`
- Progressive loading 模式（仅注入目录 + 按需读取）可显著减少 token 消耗 — 适用于 Hive 的大规模 skill 库
- 工具调度模式（SkillCommandDispatchSpec）提供了 skill → tool 的清晰映射 — 可在 Hive 中参考

### 反面教材
- Frontmatter 元数据采用 JSON 嵌入式方案可能难以维护；Hive 可考虑分离 .md + .yaml 结构

### 别抄
- 安全验证过滤（拒绝 shell 元字符）依赖黑名单正则；Hive 应采用白名单模式更安全

## 5. 与 deer-flow 6-axis 的范式差异

| 维度 | deer-flow | OpenClaw |
|------|-----------|----------|
| **加载策略** | 可配置（eager/lazy）| Progressive loading（必需） |
| **存储格式** | JSON schema | Markdown + YAML frontmatter |
| **参数化** | 原生 skill 参数 | 通过 tool dispatch 间接支持 |
| **可见性** | skill_visibility/scope | 多层过滤 + requires/forbid 策略 |
| **安全模型** | token-based RBAC | Frontmatter 声明 + 黑名单验证 |
| **多租户** | tenant 字段 | 通过 workspace/agent/session 过滤 |

---

## 核心断言总结

1. **Skills 采用 Progressive loading**，Markdown + YAML frontmatter 结构
2. **可见性通过 Frontmatter 声明**：user-invocable / disable-model-invocation
3. **安装依赖支持 5 种包管理器**（brew/node/go/uv/download），带安全验证
4. **工具调度**通过 SkillCommandDispatchSpec 实现 skill → tool 映射
5. **运行时检查** (requires.bins) 目前状态不确定；设计存在但执行路径不清
6. **Sub-agent 隔离**通过仅注入最小 bootstrap 实现
