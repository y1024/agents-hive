# P0-A 静态 AST 检测器 — 已知盲区与运行时锁补丁草案

收尾日期：2026-04-22
对应测试：`internal/master/react_processor_ordering_test.go::TestReActLoop_OrderingInvariants`
对应基线：`internal/master/react_processor.go`（GREEN，已通过 round-22 全部 53 条 HIGH 闭环）

## 1. 为什么停止迭代

P0-A 红/蓝对抗循环走到 round-22。HIGH 编号从 1 推进到 58，每轮 Codex 仍能稳定产出 4-6 条新 HIGH，**没有衰减趋势**。

根本原因是数学问题：

- 静态 AST 检测器规则集是**有限**的
- Go 源码的语法变体空间是**无限**的（IIFE、shadow scope、reflect、unsafe、cgo、go:linkname、interface dispatch、generic、init() 副作用、生成代码……）
- 每堵一条新形态，红方就再造一条

继续 round-N 修补只是把检测器写得更复杂，不会真正关闭攻击面。**真正的修法是把不变量从静态检查搬到运行时类型系统**（见 §4）。

当前 round-22 检测器锁定的是**已枚举的 53 类攻击模式**。它仍是有价值的回归网，但不应被视为完备证明。

## 2. round-22 已锁定的 53 类攻击面（摘要）

| 攻击类别 | 代表 HIGH | 检测机制 |
|---------|----------|---------|
| 直接 pre-gate `appendSessionMessage(Role:"assistant")` | 1, 6 | AST 直扫 |
| pre-gate `BroadcastSessionMessage` | 7, 32 | AST 直扫 + payload taint |
| `EventBus.Broadcast` / `BroadcastGenericMessage` | 20, 26 | 全广播汇聚点扫描 |
| helper wrapper / 包级 FuncLit / 工厂返回 | 8, 13, 45, 52 | `helpers` + `synthesizeFuncLitDecls` |
| 多层 helper 参数传播 / variadic | 39-A, 39-B | 不动点闭包 + variadic idx |
| reflect 动态 dispatch + import 别名 + 点导入 | 37, 38, 38-A | per-file alias 集合 |
| channel 泄漏 `ch <- msg` | 42 | SendStmt 扫描 |
| `resp.Content` 别名链（Ident/SelectorExpr/SliceExpr/Sprintf） | 33, 44, 50, 51 | `collectRespContentAliases` + pre-gate scoped |
| 多阶指针 + TypeAssert 链 | 25, 29, 36, 40 | `stripPtr` + 不动点 |
| 常量 role 折叠（BasicLit/Ident/BinaryExpr/IndexExpr/`+=`/IIFE） | 28, 33, 41, 43, 47, 48, 49 | `constStringValue` + event sequence |
| 复合 map key 文本对比 | 53 | `exprText` printer 序列化 |
| package const 字符串预填 | 33 | `packageConstStrings` |

## 3. round-22 后已知静态盲区（按 Codex round-22 + 可预见延伸）

下列**5 类**是 Codex round-22 明确给出的攻击形态，当前检测器**确实漏**。它们不会触发测试失败，但能让 P0-A assistant 消息再次绕过 gate 泄漏：

### 3.1 参数化 IIFE（HIGH 54）

```go
role := func(s string) string { return s }("assistant")
m.appendSessionMessage(session, llm.MessageWithTools{Role: role, ...})
```

`constStringFromFuncLit` 仅接受零参 FuncLit。带参 IIFE 折叠需要做参数 substitution（把 body 内 Ident 按 param→arg 表替换后再折叠）。

### 3.2 shadow scope 污染 `+=` 折叠（HIGH 55）

```go
status := "assistant"
if true { status := "tool"; _ = status }
m.appendSessionMessage(session, llm.MessageWithTools{Role: status, ...})
```

`collectConstStringIdents` 的事件序列按变量名（string）建模，shadow 的内层 `status` 与外层混入同一序列，触发假冲突 → `constStringIdents["status"]` 被 block，外层的 "assistant" 折叠失效。修法：events key 改成 `*ast.Object`（go/parser 默认会做 scope resolution，Ident.Obj 可用）。

### 3.3 `strings.Builder` 双向盲区（HIGH 56）

```go
var b strings.Builder
b.WriteString(resp.Content)
errText := b.String()
m.eventBus.BroadcastSessionMessage(...payload: {"message": errText, ...})
```

`collectRespContentAliasesInPreGate` 只识别 RHS 字面引用 `resp.Content` 或已知 alias；`b.String()` 两者都不是。需要单独建 builder taint 表：扫 `var b strings.Builder` → 扫 `b.WriteString(taint)` → 标 `taintedBuilders[*ast.Object]` → `X.String()` 调用查表。同样问题适用于 `bytes.Buffer`、`io.Pipe`、`fmt.Fprintf(&b, ...)`。

### 3.4 多返回值工厂 + receiver method 工厂（HIGH 57）

```go
func blueFactory() (func(*Master, *SessionState, string), error) {
    return func(...){ m.appendSessionMessage(...) }, nil
}
var blueLeak, _ = blueFactory()

// 或：
func (b *Builder) Build() func(...) { return func(...){...} }
var blueLeak2 = (&Builder{}).Build()
```

`synthesizeFuncLitDecls` Pass 1 只记录 `len(ret.Results)==1 && fd.Recv==nil` 的 factory。多返回值 + method receiver 都不进 `returnsFuncLit` 表。

### 3.5 复合 map key 变量化 + 嵌套 map（HIGH 58）

```go
var k = [2]int{0, 0}
role := map[[2]int]string{[2]int{0,0}: "assistant"}[k]

// 或嵌套：
role := map[string]map[string]string{"x":{"y":"assistant"}}["x"]["y"]
```

`exprText` 文本对比要求 `exprText(v.Index) == exprText(kv.Key)`，`k` vs `[2]int{0,0}` 不相等。嵌套 IndexExpr 走 `indexExprComposite`，根本没 `exprText` fallback。

### 3.6 可预见但 Codex 还没给出的形态

记录在此供未来回归参考：

- **interface method dispatch**：`var I LeakIface = impl{}; I.Leak(resp.Content)` —— 通过接口调用，静态拿不到 concrete method body
- **unsafe.Pointer / go:linkname**：从外部包反射进入广播路径
- **cgo callback**：C 端回调 Go，静态分析不可达
- **`init()` / package-level side effect**：在 import 时就把 leak helper 注册进全局回调表
- **运行时代码生成**：text/template / `plugin.Open` / `protoc-gen` 生成的 helper
- **goroutine / context 传递**：`go func(){ broadcast(<-ch) }()` + channel 在 pre-gate 入队
- **sync.OnceFunc / sync.Pool 包装**
- **第三方库间接调用**：`logger.Hook(func(...){ broadcast(...) })`
- **defer 泄漏**：`defer func(){ if recover()!=nil { broadcast(resp.Content) } }()`

## 4. 真正的修法：运行时 structural lock（**已实现，2026-04-22**）

### 实施摘要

落地版本与 §4 草案略有差异，关键决策记录在此：

1. **token 类型**：草案是 `type emitToken struct{ _ struct{} }`。实施验证发现 Go spec 允许任意包对零值 struct 用空 composite literal `pkg.S{}` 构造，即使带 unexported field —— **跨包伪造 `assistantcap.Capability{}` 真的能编过**。改为 **interface + unexported method** 形态：

   ```go
   type Capability interface { assistantcap() }
   type grant struct{}
   func (grant) assistantcap() {}
   var granted Capability = grant{}
   ```

   外部包无法定义满足 `assistantcap()` 的类型，编译期报 "does not implement assistantcap.Capability (missing assistantcap method)"。蓝军 mutation DC 验证通过。

2. **唯一颁发口**：`assistantcap.GrantPass(action, passValue int)` + `GrantStream(toolChoice, requiredValue string)`。AST rule 3 锁定第二参数必须是 `requiredGuardPass` 标识符（防止 `GrantPass(0, 0)` 字面伪造）。

3. **wrapper 唯一字面量来源**：
   - `Master.persistAssistant(cap, ...)` 是全包唯一写 `MessageWithTools{Role:"assistant"}` 字面量的位置（AST rule 1）
   - `Master.broadcastAssistant(cap, ...)` 是全包唯一写 `payload["role"] = "assistant"` 赋值的位置（AST rule 2）

4. **sink-side runtime panic**：双层防御
   - `Master.appendSessionMessage(session, msg)` 检测 `msg.Role == "assistant"` 立即 panic
   - `EventBus.Broadcast(msg)` 检测 payload `role:"assistant"`（覆盖 `map[string]any` + `map[string]string`）立即 panic
   - panic msg 含 `[P0-A structural lock]` prefix 便于日志聚合

5. **测试崩缩**：原 `react_processor_ordering_test.go` 3681 行 → ~280 行（含完整中文注释 + helpers）。仅保留 3 条 capability-respecting AST 规则 + 3 条 runtime panic smoke test。

6. **蓝军 4 条 mutation 全部按期表现**：
   | mutation | 期望 | 实际 |
   |---------|------|------|
   | DA: pre-gate `appendSessionMessage(Role:"assistant")` | runtime panic | ✅ TestStructuralLock_AppendSessionMessagePanic |
   | DB: pre-gate `Broadcast(Payload:{role:"assistant"})` | runtime panic | ✅ TestStructuralLock_BroadcastPanic |
   | DC: cross-pkg `assistantcap.Capability{}` | 编译失败 | ✅ "invalid composite literal type" |
   | DD: `GrantPass(0, 0)` 字面伪造 | rule3 红 | ✅ "got 0" |

### 信任边界（明确放行）

下列形态属于**已知信任边界**，不在结构性锁覆盖内，需写代码评审 / SECURITY.md：

- 同包内 helper 拿到合法 cap 后转手给伪造调用 —— 由 AST rule 1+2 限定字面量唯一来源缓解
- `unsafe.Pointer` 强转构造 `grant{}` —— 在 Go 审计层面立刻可见
- `go:linkname` / cgo callback —— 同上
- struct 形 `BroadcastMessage.Payload`（如 `AgentProgressEvent`）—— sink runtime check 不覆盖，但这些 struct 不写 role 字段

### 历史草案（保留以追溯）

下面 §4.1-4.3 是 2026-04-21 提交时的初版骨架，与最终实施有差异，仅供版本对比参考。

---

## 4. 真正的修法：运行时 structural lock（草案历史版本）

把"必须在 gate 之后才能 emit assistant 消息"做成**编译期不可绕过的类型不变量**。这样无论红方写什么语法变体，编译器都拒绝 pre-gate 的调用。

### 4.1 设计骨架

```go
// 1. 引入不可伪造的 emit token
type emitToken struct{ _ struct{} }  // unexported field 防止外部构造

// 2. 把所有 emit 函数改造为只接受 token
func (m *Master) appendSessionMessage(_ emitToken, session *SessionState, msg llm.MessageWithTools) { ... }
func (b *EventBus) BroadcastSessionMessage(_ emitToken, sid string, msg BroadcastMessage) { ... }
func (b *EventBus) Broadcast(_ emitToken, msg BroadcastMessage) { ... }
func (b *EventBus) BroadcastGenericMessage(_ emitToken, ...) { ... }

// 3. 唯一颁发 token 的入口就是 gate 通过
func evaluateRequiredGuard(...) (action, breach, *emitToken) {
    a, b := /*原逻辑*/
    if a == requiredGuardPass { return a, b, &emitToken{} }
    return a, b, nil
}

// 4. emitAssistantMessage 改为消费 token：
//    func emitAssistantMessage(action, tok *emitToken) bool { return tok != nil && action == requiredGuardPass }
```

调用 site：

```go
action, nextBreach, tok := evaluateRequiredGuard(toolChoice, len(resp.ToolCalls), requiredBreachCount)
if !emitAssistantMessage(action, tok) { /* retry/fail 早退 */ }
m.appendSessionMessage(*tok, session, llm.MessageWithTools{Role:"assistant", Content:llm.NewTextContent(resp.Content), ...})
m.eventBus.BroadcastSessionMessage(*tok, session.ID, BroadcastMessage{...})
```

### 4.2 攻击面收敛证明

红方任何 pre-gate `m.appendSessionMessage(...)` 调用编译器都会报"missing argument of type emitToken"。这一条结构性约束等价于把 round-1 → round-22 全部 58 条 HIGH 全部静态消除，且不依赖 AST 启发式。

剩余攻击面收缩到：

1. **reflect 构造 token**：`reflect.New(reflect.TypeOf(emitToken{})).Elem()` —— 由于 `emitToken` 字段 unexported，reflect SetField 会 panic（go 1.17+ 严格）。需要用 `unsafe.Pointer` 强转，但这在审计层面立刻可见
2. **同包内 helper 拿到 token 后转手**：仍然存在，但缩小为「token 不能被未通过 gate 的代码持有」—— 这条用现在已有的 round-22 静态检测器（专攻"谁持有 token"）继续做即可，攻击面比原本"谁能调 emit"小 1-2 个数量级
3. **unsafe / cgo / linkname**：明确写入 SECURITY.md 作为已知信任边界

### 4.3 落地工作量评估

| 任务 | 文件 | 行数 | 难度 |
|-----|------|-----|------|
| 引入 `emitToken` 类型 + token 颁发口 | `react_processor.go`、`tool_choice_detector.go` | ~30 | 低 |
| 改造 `appendSessionMessage` / `BroadcastSessionMessage` 等 5 个 sink 签名 | `react_processor.go`、`event_bus.go`、`master.go`、`public_api.go`、`broadcast_api.go` | ~50 | 中 |
| 修复所有现有 caller（约 20 处）传入 token | 全包 | ~80 | 中（编译器辅助） |
| 测试：把现有 `react_processor_ordering_test.go` 收缩到只验"token 持有者扫描"和"reflect/unsafe 黑名单" | `react_processor_ordering_test.go` | -2000 | 中 |

预估 1-2 天完工。比继续修 AST round-23+ 经济得多，且**真正关闭攻击面**。

## 5. 短期建议

1. **保留 round-22 检测器作为回归网**——挡住已枚举的 53 类，仍有价值
2. **把本文档的 §3 五条已知盲区写进每次 PR review checklist**，由人工审 pre-gate 区域是否引入 IIFE 带参 / strings.Builder / 多返回值工厂等
3. **下一个迭代窗口实施 §4 structural lock**，完工后把现有 ordering test 收缩到 token 持有者审计

## 6. 引用

- 主测试文件：`internal/master/react_processor_ordering_test.go`（~3520 行）
- 主生产文件：`internal/master/react_processor.go`（gate 位于 line 572）
- 红蓝对抗历史：见 commit log `quality-guards` / `P0-A` 系列
- 原始 plan：`docs/计划与路线/归档/Agent-质量护栏治理计划.md`
