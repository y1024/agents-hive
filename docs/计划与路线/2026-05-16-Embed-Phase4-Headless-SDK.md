# Phase 4：Headless SDK（修正版）

> **执行优先级:** P3
> **前置:** P0-P2 后端协议稳定。
> **目标:** 先实现 TypeScript Headless SDK，Widget 复用 SDK，不在 MVP 同时维护 Go SDK。

## 核心修正

旧计划把 Widget 放在 SDK 前面，并同时规划 TypeScript 与 Go SDK。实际应反过来：

1. TypeScript SDK 先固化协议。
2. Widget 作为 SDK 的 UI consumer。
3. Go SDK 等协议稳定后再做。

## 包位置

MVP 推荐放在前端工程内，便于 Widget 共用：

```
frontend/src/embed/sdk/
  index.ts
  client.ts
  session.ts
  stream.ts
  types.ts
  errors.ts
```

后续发布 npm 包时，再抽到 `sdk/typescript/` 或 workspace package。

## SDK 配置

```ts
export interface HiveEmbedClientConfig {
  baseUrl: string;
  agentId: string;
  token?: string;
  tokenProvider?: () => Promise<string>;
  fetchImpl?: typeof fetch;
  defaultContext?: Record<string, unknown>;
  defaultEnv?: Record<string, string>;
}
```

规则：

- `tokenProvider` 优先于静态 `token`。
- 浏览器端静态 token 只适合短期 scoped token。
- SDK 不接收后台 admin token。

## 核心 API

```ts
export class HiveEmbedClient {
  constructor(config: HiveEmbedClientConfig);

  createSession(input?: CreateEmbedSessionInput): Promise<EmbedSession>;
  getAgentConfig(): Promise<EmbedAgentConfig>;
  updateSessionContext(sessionId: string, input: UpdateContextInput): Promise<void>;
  sendMessage(sessionId: string, input: SendMessageInput): Promise<SendMessageAck>;
  stream(sessionId: string, options?: StreamOptions): AsyncIterable<EmbedStreamEvent>;
  closeSession(sessionId: string): Promise<void>;
}
```

`EmbedSession` 封装：

```ts
export class EmbedSession {
  readonly id: string;

  send(content: string, options?: SendMessageOptions): Promise<SendMessageAck>;
  stream(options?: StreamOptions): AsyncIterable<EmbedStreamEvent>;
  updateContext(input: UpdateContextInput): Promise<void>;
  close(): Promise<void>;
}
```

## SSE 解析

SDK 使用 `fetch` 解析 SSE，因为浏览器 `EventSource` 不能设置 Authorization header。

需要正确处理：

- `event:` 行。
- 多行 `data:`。
- 注释 keepalive。
- chunk 跨行。
- 服务端断连。
- `AbortSignal`。

事件类型先映射后端现有 EventBus：

```ts
export type EmbedStreamEvent =
  | { type: 'input_received'; turnId?: string }
  | { type: 'message'; role: 'user' | 'assistant' | 'system'; content: string; turnId?: string }
  | { type: 'tool_call'; toolName: string; status: 'start' | 'success' | 'error'; turnId?: string; error?: string }
  | { type: 'agent_status'; status: string; turnId?: string }
  | { type: 'error'; message: string; code?: string };
```

## 使用示例

```ts
import { HiveEmbedClient } from '@agents-hive/embed-sdk';

const client = new HiveEmbedClient({
  baseUrl: 'https://hive.example.com',
  agentId: 'customer-service',
  tokenProvider: async () => {
    const res = await fetch('/api/hive/embed-token');
    const data = await res.json();
    return data.token;
  },
  defaultContext: {
    page: location.pathname,
  },
});

const session = await client.createSession({
  businessContext: {
    order_id: 'ORD-2026-12345',
    current_tab: 'logistics',
  },
});

const stream = session.stream();
await session.send('这个订单为什么还没发货？');

for await (const event of stream) {
  console.log(event);
}
```

## 构建与测试

新增脚本建议：

```json
{
  "scripts": {
    "build:embed-sdk": "vite build --config vite.config.embed-sdk.ts",
    "test:embed-sdk": "vitest run src/embed/sdk"
  }
}
```

验证命令：

- `cd frontend && npm run lint`
- `cd frontend && npm run build`
- `cd frontend && npm run test:embed-sdk`

关键用例：

- `tokenProvider` 会在 token 过期或 401 后重新取 token。
- SSE parser 能解析多行 data。
- `AbortController` 能取消 stream。
- 401/403/429 返回结构化错误。
- `sendMessage` 不把 token 放进 URL。

