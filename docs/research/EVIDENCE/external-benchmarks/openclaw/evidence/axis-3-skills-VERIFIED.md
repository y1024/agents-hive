# OpenClaw Axis 3: Skills — 主线程逐条核实

> **核实方法**：L3 + L4 — Read docs/concepts/system-prompt.md + Read src/agents/skills/types.ts + grep 安全验证
> **日期**：2026-04-25

---

## 验证总览

| 类型 | 总数 | [VERIFIED] | [REVISED] | [FALSE] |
|---|---|---|---|---|
| 关键断言 | 6 | 5 | 1 | 0 |

---

## §1 关键断言核实

### S1 [VERIFIED] Progressive Loading
- 文档 `docs/concepts/system-prompt.md:104-126`：
  - "OpenClaw injects a compact **available skills list** (`formatSkillsForPrompt`) that includes the **file path** for each skill"
  - "instructs the model to use `read` to load the SKILL.md at the listed location"
  - "If no skills are eligible, the Skills section is omitted"
  - "This keeps the base prompt small while still enabling targeted skill usage"
- **关键设计点**：startup 只注入 skill 元数据列表（name + description + location），不读 SKILL.md 全文 — 这就是 progressive loading
- **SYNTHESIS P1-1 根据扎实** ✓

### S2 [VERIFIED] frontmatter 字段
- `src/agents/skills/types.ts:38-41`：
  ```typescript
  export type SkillInvocationPolicy = {
    userInvocable: boolean;
    disableModelInvocation: boolean;
  };
  ```

### S3 [VERIFIED] 5 个包管理器
- `src/agents/skills/types.ts:5` `kind: "brew" | "node" | "go" | "uv" | "download"` — 完全确认

### S4 [VERIFIED] OpenClawSkillMetadata 完整 schema
- `types.ts:23-37`：always / skillKey / primaryEnv / emoji / homepage / os / requires / install
- 比子 agent 报的更完整

### S5 [VERIFIED] formatSkillsForPrompt
- 文档 `docs/concepts/system-prompt.md:108`
- 输出 XML 格式 `<available_skills><skill><name/><description/><location/></skill></available_skills>`

### S6 [REVISED] frontmatter 安全验证
- 原断言："黑名单正则验证（易被绕过）"
- **真相**：`src/agents/skills/frontmatter.test.ts` 测试覆盖了：
  - `wget --HEAD`（命令注入）
  - `file:../malicious`（path traversal）
  - `https://evil.example/mod`（恶意 URL）
- 这看起来更像**白名单 + 多模式安全检查**，不只是黑名单
- 需要 Read frontmatter.ts 实际实现才能定论

---

## §2 重大新发现

### F8 — Skill 类型继承自 `@mariozechner/pi-coding-agent`
- `types.ts:1` `import type { Skill } from "@mariozechner/pi-coding-agent";`
- 与 axis-1 的 `pi-agent-core` 是**同一作者（Mario Zechner / Peter Steinberger?）的 framework family**
- **战略影响**：OpenClaw 不是独立 agent 系统，而是**基于 pi-* framework 之上的产品 wrapper**
- 这翻转了"OpenClaw 是 22 通道硬编码扩展的单体"的简单判断 — 它实际有清晰的"framework + extensions + product"分层

### F9 — Skills 通过 ClawHub 分发
- `system-prompt.md:128-130` 提到 ClawHub (https://clawhub.com) 用于 skills discovery
- 这是 OpenClaw 自有的 skill marketplace（之前 WebSearch agent 报告的"5700 skills"如果属实就在 ClawHub）

### F10 — SkillCommandSpec 显式声明 dispatch
- `types.ts:50-58` SkillCommandDispatchSpec：`{ kind: "tool", toolName: string, argMode?: "raw" }`
- skill 可以**直接绑定**到一个 tool，不经过 LLM 路由
- **战略影响**：与 Hive `internal/skills/finder.go:449` `FindBySpecRequirements`（requirement-driven activation）对照，OpenClaw 的 dispatch 更直接但更僵硬

---

## §3 对 SYNTHESIS 的影响

### §3 P1-1 Progressive Skill Loading（加强）
- S1 + S5 双重证据，**OpenClaw 真做 progressive**
- Hive `internal/skills/on_demand_api.go` 已有基础，需对照 OpenClaw `formatSkillsForPrompt` 实现
- 借鉴价值：可信度 [HC-MAIN-VERIFIED]

### §5 反面教材 #5（修正）
- 原"别抄 OpenClaw frontmatter 黑名单正则"
- **修正**：实际是多模式安全检查（命令注入 + path traversal + 恶意 URL），并非纯黑名单。需要看 frontmatter.ts 实现确认是否真的弱

---

## §4 仍待核实

- frontmatter.ts 实际安全检查实现（白名单 vs 黑名单 vs 综合）
- ClawHub 真实 skill 数量（之前 WebSearch agent 报"5700"未独立核实）

---

*— End of axis-3 主线程核实 —*
