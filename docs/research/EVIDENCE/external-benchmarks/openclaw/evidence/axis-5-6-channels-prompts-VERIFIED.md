# OpenClaw Axis 5 (Channels) + Axis 6 (Prompts) — 主线程逐条核实

> **日期**：2026-04-25

---

## §1 axis-5 channels 核实

### C1 [REVISED] 通道扩展数量
- 原断言："22+ 通道扩展硬编码"
- **真相** (`extensions/` ls)：实际 40 个目录，但其中**真正的 IM/messaging channel 是 20 个**：
  - bluebubbles / discord / feishu / googlechat / imessage / irc / line / lobster / matrix / mattermost / msteams / nextcloud-talk / nostr / phone-control / signal / slack / synology-chat / telegram / tlon / twitch
  - 其他 20 个目录是辅助 extension（acpx / copilot-proxy / device-pair / diagnostics-otel / diffs / google-gemini-cli-auth / llm-task / memory-core / memory-lancedb / minimax-portal-auth / ollama / open-prose / qwen-portal-auth / sglang / shared / talk-voice / test-utils / thread-ownership / vllm / voice-call）
- **修正**：实际 20 个 IM channel，不是 22

### C2 [VERIFIED] 飞书集成存在
- `extensions/feishu/` 完整目录，30+ TS 文件
- 包括 accounts/bot/card-action/chat/client/dedup/directory/docx/send 等

### C3 [REVISED ⚠️ 关键] OpenClaw 飞书也有完整 dedup/debounce/PATCH 管道
- **新证据 1** — dedup：
  - `extensions/feishu/src/dedup.ts` 文件
  - `monitor.account.ts:194` `function dedupeFeishuDebounceEntriesByMessageId`
- **新证据 2** — debounce：
  - `monitor.account.ts:249` `core.channel.debounce.resolveInboundDebounceMs`
  - `monitor.account.ts:307` `core.channel.debounce.createInboundDebouncer<FeishuMessageEvent>`
- **新证据 3** — PATCH 增量更新：
  - `extensions/feishu/src/send.ts:389` `const response = await client.im.message.patch({...})`
  - `extensions/feishu/src/send.ts:470` `const response = await client.im.message.update({...})`
- **结论**：之前 SYNTHESIS §0 #3 "Hive 在 chunk/debounce/dedup/retry 完整管道领先"是错误的。**OpenClaw 飞书也实现了**

### C4 [VERIFIED] interactive card-action 支持
- `extensions/feishu/src/card-action.ts:25-`：`handleFeishuCardAction` 处理 card 按钮回调
- `bot.card-action.test.ts` 测试覆盖

---

## §2 axis-6 prompts 核实

### P1 [VERIFIED] PromptMode = "full" | "minimal" | "none"
- `src/agents/system-prompt.ts:11-17` 完整定义：
  ```typescript
  /**
   * - "full": All sections (default, for main agent)
   * - "minimal": Reduced sections (Tooling, Workspace, Runtime) - used for subagents
   * - "none": Just basic identity line, no sections
   */
  export type PromptMode = "full" | "minimal" | "none";
  ```
- `:380` `const isMinimal = promptMode === "minimal" || promptMode === "none"`
- `:417` "For 'none' mode, return just the basic identity line"

### P2 [VERIFIED] Safety section 仅是 prompt advisories
- `docs/concepts/system-prompt.md:18`：
  > "**Safety**: short guardrail reminder to avoid power-seeking behavior or bypassing oversight."
- 这就是 prompt 段而非代码层强制
- **Hive 反面教材结论保持**：safety 必须代码层强制，OpenClaw 仅 prompt level 是反面教材

### P3 [VERIFIED] HITL `/approve` 命令
- `src/infra/exec-approval-reply.ts:90` `lines.push(buildFence(\`/approve ${approvalCommandId} allow-once\`, "txt"));`
- 测试：`exec-approval-reply.test.ts:70` `/approve slug-1 allow-once`
- 三态：`allow-once` / `allow-always` / `deny`
- IM 上下文支持：`outbound/deliver.test.ts:215-262` 通过 telegram 等 IM `callback_data` 完整集成
- **战略影响**：与 Hive `internal/security/SafeExecutor` + IM 通道集成对照，OpenClaw 的三态审批 (once/always/deny) 比 Hive 当前 (Allow/Ask/Deny) 在用户体验上更清晰

### P4 [VERIFIED] Prompt 模块化分段
- `docs/concepts/system-prompt.md:13-25` 列出至少 7 个固定段：
  - Tooling / Safety / Skills / OpenClaw Self-Update / Workspace / Documentation / Workspace Files
- 加上 system-prompt.ts 实际实现里的 Memory Recall / User Identity / Date & Time 等段
- **保守估计 9+ 段**，子 agent 报"9+"基本对

---

## §3 重大新发现

### F11 — OpenClaw HITL 三态比 Hive 设计更直接
`/approve <id> allow-once|allow-always|deny` 通过 IM callback button 一次点击搞定。Hive 当前 SafeExecutor 是 (Allow/Ask/Deny) 三态但 IM 集成实际效果还需对照。**可能借鉴点**：把 Hive 的 Ask 进一步细化为 `allow-once` / `allow-always`。

### F12 — OpenClaw 飞书 client 是 Lark.EventDispatcher
`extensions/feishu/src/monitor.account.ts:244` `eventDispatcher: Lark.EventDispatcher` —— 用飞书官方 lark/oapi-sdk-js（与 Hive memory 锁定的"飞书必须用 larksuite/oapi-sdk-go"完全一致）。**双方策略一致**

### F13 — OpenClaw IM 渠道覆盖 — 国内只有飞书一家
- 真正国内 IM 在 OpenClaw 中只有 **feishu** 一个
- WeChat/WeChatPad/钉钉/企微 OpenClaw **都没有**
- **Hive 真正领先点**：钉钉/微信/企微集成（OpenClaw 完全不支持，因为它定位是个人助手，不是企业 IM）

---

## §4 对 SYNTHESIS 的影响（修正清单）

| SYNTHESIS 项 | 旧断言 | 修正后真相 |
|---|---|---|
| §0 #3 | "Hive 国内 IM 渠道工程领先所有三家" | **降级**：Hive 在"国内 IM 平台数量覆盖"上领先（4 vs OpenClaw 只支持飞书 1），但**单平台工程深度（飞书 dedup/debounce/PATCH）势均力敌** |
| §3 P1-1 | "工具分组快捷键 group:*" | **加强**：axis-1 验证 9 个组合法 |
| §5 反面教材 #4 | "工具级无并发限制" | **保留措辞** — agent 工具调用层确实无 cap |
| §0 新增 #11 | — | OpenClaw HITL `/approve` 三态比 Hive 当前 Allow/Ask/Deny 更细，**Hive 可借鉴 allow-once vs allow-always 区分** |

---

## §5 仍待核实

- OpenClaw 飞书是否做飞书 SDK 限流 (429 处理 / `ErrPatchRateLimited` 等价物)？grep 没明显找到，**Hive 可能在限流细节上仍领先**
- 心跳触发的 PATCH（Hive 有，OpenClaw 是否有？）

---

*— End of axis-5 + axis-6 主线程核实 —*
