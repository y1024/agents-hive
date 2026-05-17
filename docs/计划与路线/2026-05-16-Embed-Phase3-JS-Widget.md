# Phase 3：JS Widget 前端实现（修正版）

> **执行优先级:** P4
> **前置:** TypeScript Headless SDK 可用。
> **目标:** 在 SDK 之上实现可嵌入 Widget，支持一行脚本挂载、上下文注入、流式展示、样式隔离和移动端适配。

## 核心修正

旧计划的问题：

- Widget 不能直接复用后台 Chat 全局状态。
- 不能把长期 token 放进 `data-token`。
- 不能把 Widget 构建产物写进 `internal/webui/dist`。
- Widget 应复用 Headless SDK，而不是自己再写一套 API client。

## 文件结构

```
frontend/src/embed/widget/
  index.tsx
  HiveWidget.tsx
  WidgetLauncher.tsx
  WidgetPanel.tsx
  WidgetMessages.tsx
  WidgetInput.tsx
  styles.css
  types.ts

frontend/src/embed/sdk/
  client.ts
  session.ts
  stream.ts
  types.ts
```

构建配置：

```
frontend/vite.config.embed-widget.ts
frontend/vite.config.embed-sdk.ts
```

输出目录建议：

```
frontend/dist/embed/
```

不要手工改 `internal/webui/dist`。

## 初始化方式

推荐方式：业务系统提供 token endpoint。

```html
<script
  src="https://cdn.example.com/hive-widget/v1/hive-widget.js"
  data-api-url="https://hive.example.com"
  data-agent="customer-service"
  data-token-endpoint="/api/hive/embed-token"
  data-context='{"page":"/orders/detail"}'
  async>
</script>
```

高级方式：JS 初始化。

```html
<script src="https://cdn.example.com/hive-widget/v1/hive-widget.js" async></script>
<script>
  window.addEventListener('HiveWidgetReady', () => {
    window.HiveWidget.init({
      apiUrl: 'https://hive.example.com',
      agentId: 'customer-service',
      tokenProvider: async () => {
        const res = await fetch('/api/hive/embed-token');
        const data = await res.json();
        return data.token;
      },
      context: {
        page: location.pathname,
        order_id: window.currentOrderId,
      },
      theme: {
        position: 'bottom-right',
        primaryColor: '#4f46e5',
      },
    });
  });
</script>
```

短期 PoC 可接受：

```html
<script data-token="short_lived_scoped_token"></script>
```

但文档必须明确：`data-token` 只能放短期 scoped token，不能放长期 token 或平台 admin token。

## Widget API

```ts
export interface HiveWidgetConfig {
  apiUrl: string;
  agentId: string;
  token?: string;
  tokenProvider?: () => Promise<string>;
  context?: Record<string, unknown>;
  env?: Record<string, string>;
  theme?: HiveWidgetTheme;
  locale?: 'zh-CN' | 'en-US';
  defaultOpen?: boolean;
  onReady?: (instance: HiveWidgetInstance) => void;
  onEvent?: (event: EmbedStreamEvent) => void;
  onError?: (error: Error) => void;
}

export interface HiveWidgetInstance {
  open(): void;
  close(): void;
  toggle(): void;
  sendMessage(content: string): Promise<void>;
  updateContext(context: Record<string, unknown>): Promise<void>;
  destroy(): void;
}
```

## UI 约束

- 使用独立根节点，推荐 Shadow DOM 或严格命名空间 CSS。
- 不依赖后台 app 的 Zustand store、router、auth、theme provider。
- 消息内容 MVP 按纯文本渲染；如果支持 Markdown，必须 sanitize。
- 移动端最大宽度使用 viewport 约束，不能遮挡业务页面主要操作。
- 所有按钮和输入要有 loading、disabled、error 状态。
- 支持 `destroy()` 清理 DOM、事件监听、stream 连接。

## 流式体验

Widget 启动流程：

1. 初始化 SDK client。
2. 获取 agent config，渲染名称、欢迎语、主题。
3. 创建 embed session。
4. 打开 SSE stream。
5. 用户发送消息，SDK 返回 ack。
6. 通过 stream 追加 assistant/tool/status 事件。

断线处理：

- stream 断开后指数退避重连。
- token 401 时调用 `tokenProvider` 刷新短期 token。
- 发送中的消息失败要展示可重试状态。

## 构建与验证

新增脚本建议：

```json
{
  "scripts": {
    "build:embed-widget": "vite build --config vite.config.embed-widget.ts",
    "dev:embed-demo": "vite --config vite.config.embed-demo.ts"
  }
}
```

验证命令：

- `cd frontend && npm run lint`
- `cd frontend && npm run build`
- `cd frontend && npm run build:embed-widget`
- 用本地 demo 页面挂载 widget，检查桌面和移动端布局。

关键用例：

- 无 token/token endpoint 时显示可读错误。
- 403 origin denied 时展示接入配置错误。
- `updateContext` 后下一条消息使用新上下文。
- `destroy()` 后 DOM 和网络连接清理。
- Widget CSS 不影响业务页面原有按钮、字体和布局。

