# Feishu Phase 2B Ingress Switching Design

日期：2026-04-24

## 目标

把飞书 Phase 2B 的第一块独立切出来：实现 `webhook | longconn` 的显式单入口切换，并支持运行中热切换。

这一阶段只解决入口切换问题，不打开 watchdog、gap fetch、`/debug`、`/audit` 等扩展治理能力。

## 不在本阶段

以下内容明确排除在本设计之外：

- reconnect watchdog
- gap fetch
- `/debug`
- `/audit`
- ops mute API
- P2P 命令白名单扩展
- longconn 补偿可靠性增强

这些内容统一留在后续 Phase 2B 子阶段。

## 阶段边界

本阶段包含：

- 显式 `ingress_mode`
- 旧配置到新模式的兼容推导
- webhook 常驻路由 + 运行时 gate
- bootstrap 启动期单入口装配
- reload 运行时 `webhook -> longconn` / `longconn -> webhook` 切换
- fail-closed 切换与错误暴露

本阶段不要求：

- longconn 断线补偿
- 长连接健康探测
- 双入口并行过渡

## 设计原则

### 1. 单入口必须有显式真相源

Phase 0/2A 的 `LongconnEnabled` 与 `WebhookURL` 是隐式组合，不适合继续扩展热切换。

Phase 2B 第一原则是：

- 运行时只认一个字段：`ingress_mode`

所有“是否放行 webhook”“是否启动 longconn”“reload 后当前活跃入口是谁”的判断，都必须改读这个字段，不能继续散落在布尔值和 URL 组合上推断。

### 2. 热切换允许短暂不可用，不允许双入口并存

切换窗口里，系统可以接受一个很短的“都不收消息”窗口。

系统不能接受：

- webhook 还在收
- longconn 也已经开始收

因为这会重新打开红队链 B：双入口 × dedup 漏洞 × 双回复。

所以切换策略必须偏向 correctness：

- fail-closed
- 不追求零损切换
- 绝不允许并存

### 3. HTTP 路由物理常驻，语义运行时切换

当前 `http.ServeMux` 与 `Server.SetChannelRouter(...)` 是启动期一次性注册模型，不适合做运行中动态增删路由。

因此本阶段采用更稳妥的方案：

- Feishu webhook URL 始终注册在 mux 上
- 但请求是否真正进入 Feishu webhook handler，由运行时 gate 决定

这是一种“路由常驻，入口语义切换”的模型。

## 配置模型

新增字段：

- `feishu.ingress_mode`

合法值：

- `webhook`
- `longconn`

建议语义：

- `webhook`：HTTP webhook 是唯一入站入口
- `longconn`：WebSocket longconn 是唯一入站入口，HTTP webhook 明确拒绝

### 兼容规则

为避免打断现有配置与测试，保留旧字段：

- `LongconnEnabled`
- `WebhookURL`

兼容推导规则：

1. 若显式设置了 `ingress_mode`，运行时只认它
2. 若未设置 `ingress_mode`：
   - `LongconnEnabled=true` → 推导为 `longconn`
   - 其他情况 → 推导为 `webhook`

### 校验规则

- `ingress_mode` 非法值 → `Validate()` fail-closed
- 显式 `ingress_mode` 存在时，不再允许其它运行时逻辑直接读取 `LongconnEnabled`
- `WebhookURL` 不再参与入口模式判定

### 归一化目标

配置读入后应统一提供：

- `IngressMode() FeishuIngressMode`

后续所有调用点都应基于这个只读视图做判定。

## HTTP 入口模型

### 常驻 webhook 路由

保留固定 HTTP 路由：

- `POST /api/v1/channel/feishu/webhook`

但这个路由不再直接调用 `router.WebhookHandler(channel.PlatformFeishu)`。

改为：

- 一个常驻 `FeishuIngressGateHandler`

### Gate 语义

当 `ingress_mode=webhook`：

- gate 把请求转发给真实 Feishu webhook handler

当 `ingress_mode=longconn`：

- gate 直接拒绝请求
- 不进入 plugin
- 不触发 webhook 解密、验签、dispatcher、router

### 拒绝语义

拒绝响应需要满足：

- 明确
- 稳定
- 不泄露内部状态

推荐：

- 返回 `404` 或 `410`
- body 明确表示 “feishu webhook ingress disabled”

本阶段推荐 `404`，因为对飞书侧语义最接近“该入口当前未开放”，也避免给外部观察者暴露过多切换细节。

## Bootstrap 装配模型

启动期装配统一读 `IngressMode()`。

### `webhook` 模式

- 注册 Feishu plugin
- 不启动 longconn
- webhook gate 放行

### `longconn` 模式

- 注册 Feishu plugin
- 启动 longconn
- webhook gate 拒绝

说明：

- Feishu plugin 本身仍然存在，因为出站、治理、lifecycle/welcome 等能力仍要复用
- “入口关闭”与“插件不存在”是两个不同概念

## 热切换时序

热切换统一走现有：

- `ReloadChannelFunc("feishu")`

但其 Feishu 分支不再只是“卸载旧插件 + 重建新插件”，而是入口级状态机切换。

### `webhook -> longconn`

顺序必须是：

1. 把 webhook gate 切到拒绝态
2. 卸载/停止旧 webhook 工作链
3. 启动 longconn
4. 仅在 longconn 启动成功后，提交当前模式为 `longconn`

### `longconn -> webhook`

顺序必须是：

1. 先停止 longconn
2. 重建 webhook 工作链
3. 把 webhook gate 切到放行态
4. 仅在 webhook 工作链可用后，提交当前模式为 `webhook`

### 为什么不是先改 mode 再做动作

如果先改 mode，再去停旧入口/起新入口，会出现窗口期语义漂移：

- gate 已按新模式放行
- 实际新入口还没就绪

或更糟：

- 新旧入口短暂重叠

因此 mode 只能在切换成功后提交为最终态。

## 失败策略

本阶段所有切换失败都必须 fail-closed。

### 新入口启动失败

要求：

- 不回退到双入口同时开启
- 保持 gate 关闭旧入口
- reload 返回明确错误
- 运维必须能看到切换失败

允许结果：

- 进程短暂或持续处于“该 Feishu 入站入口当前关闭”的状态

不允许结果：

- webhook + longconn 同时活跃

### 状态一致性要求

任意时刻系统只能处于以下之一：

1. `webhook` 活跃，`longconn` 停止
2. `longconn` 活跃，webhook gate 拒绝
3. 切换失败后的 fail-closed 暂停态

不能出现第四种：

4. `webhook` 与 `longconn` 同时收消息

## 对现有代码的影响

### `internal/config/config.go`

需要新增：

- `FeishuIngressMode`
- `IngressMode()` 归一化方法

并调整：

- `Validate()`
- 旧字段兼容逻辑

### `internal/api/server.go`

需要改造：

- `SetChannelRouter(...)`

让 Feishu webhook 路由接入常驻 gate，而不是静态直连 router webhook handler。

### `internal/bootstrap/helpers.go`

需要改造：

- `BuildReloadChannelFunc(...)`
- `buildFeishuPlugin(...)`

让其按 `IngressMode()` 装配和切换。

### `internal/bootstrap/server.go`

需要改造：

- 启动期 Feishu 注册逻辑
- 注入 ingress gate 所需的运行时状态引用

## 测试设计

### 配置测试

- `ingress_mode=webhook` 合法
- `ingress_mode=longconn` 合法
- 非法值失败
- 未配置 `ingress_mode` 时，旧字段推导正确

### Gate 测试

- `webhook` 模式下，请求能进入真实 webhook handler
- `longconn` 模式下，请求被明确拒绝

### Reload 测试

- `webhook -> longconn` 后：
  - webhook 请求被 gate 拒绝
  - longconn 已启动
- `longconn -> webhook` 后：
  - longconn 已停止
  - webhook 请求恢复可达

### 失败测试

- longconn 启动失败时：
  - reload 返回错误
  - webhook 不被错误地重新放行
  - 不出现双入口活跃

### Bootstrap 测试

- Feishu webhook 路由始终存在
- 但是否放行取决于当前 `ingress_mode`

## 验收标准

- 运行时修改 Feishu 通道配置并触发 reload，可在 `webhook` 和 `longconn` 之间切换
- `webhook` 模式下 webhook 可用，longconn 未启动
- `longconn` 模式下 longconn 已启动，webhook 请求被 gate 拒绝
- 任意时刻不会同时启用两种入站入口
- 切换失败时系统保持 fail-closed，而不是退化为双入口

## 风险与取舍

### 1. 热切换不是零损

本阶段明确接受：

- 切换过程中可能有极短暂“都不收”的窗口

这是主动取舍，用来换：

- 不双投
- 不双回复
- 不把 longconn/watchdog/gap fetch 问题混进当前切片

### 2. HTTP 路由常驻不是问题本身

本设计不追求“物理删路由”，只追求“语义单入口”。

路由常驻但 gate 明确拒绝，已经满足：

- 热切换
- 单入口
- 低侵入

### 3. longconn 可靠性仍未解决

本阶段只是为后续开放 longconn 清入口基础障碍。

它不意味着：

- longconn 已具备生产可靠性

watchdog 与 gap fetch 仍是后续必须交付的独立切片。
