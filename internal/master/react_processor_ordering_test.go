package master

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"strings"
	"testing"

	"go.uber.org/zap"

	"github.com/chef-guo/agents-hive/internal/llm"
)

// TestReActLoop_StructuralLock 是 P0-A round-22 之后的崩缩版顺序锁测试。
//
// 历史：原 round-22 检测器约 3520 行，用 AST 启发式静态扫 53 类攻击形态。Codex
// 红蓝对抗循环走到 round-22 后仍能稳定每轮新增 4-6 条 HIGH，无衰减。根因是数学
// 问题：静态规则集有限，Go 语法变体空间无限。
//
// 现版改为 capability + sink-side runtime panic 的双层结构性约束（见 docs/
// p0a-static-detector-limits.md §4 落地实现）：
//   - 编译期：assistantcap.Capability 私有 unexported field 阻断跨包伪造
//   - 运行时：appendSessionMessage / EventBus.Broadcast 在 assistant role 上 panic
//
// 这样本测试不再追"红方今天又写出哪种 IIFE / shadow / Builder / reflect"，只锁
// 四条结构不变量 + 两条 runtime smoke：
//  1. react_processor.go 内 MessageWithTools{Role:"assistant",...} 字面量仅可
//     出现在 persistAssistant 函数体（其他地方写就触发 runtime panic，但 AST
//     规则提前一步拦在 build 阶段）
//  2. payload["role"] = "assistant" 字面赋值仅可出现在 broadcastAssistant 函数体
//  3. assistantcap.GrantPass 第二参数必须是 requiredGuardPass 标识符（防止
//     GrantPass(0, 0) 之类伪造调用骗过编译期检查）
//  4. IntentFulfillmentGate 必须在 persistAssistant 之前运行，避免未完成意图的
//     assistant 最终回复先污染 DB/UI
//
// runtime smoke：直接调 appendSessionMessage(Role:"assistant") 与
// EventBus.Broadcast(payload role:"assistant") 必须 panic 且消息含 lock prefix。
func TestReActLoop_StructuralLock(t *testing.T) {
	const src = "react_processor.go"
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, src, nil, parser.AllErrors)
	if err != nil {
		t.Fatalf("解析 %s 失败: %v", src, err)
	}

	// ── 收集函数边界，按行号查反向定位"哪段代码在哪个函数体内"
	type funcRange struct {
		name      string
		startLine int
		endLine   int
	}
	var funcs []funcRange
	for _, decl := range f.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if !ok || fd.Body == nil {
			continue
		}
		funcs = append(funcs, funcRange{
			name:      fd.Name.Name,
			startLine: fset.Position(fd.Body.Lbrace).Line,
			endLine:   fset.Position(fd.Body.Rbrace).Line,
		})
	}
	enclosingFunc := func(line int) string {
		for _, fr := range funcs {
			if line >= fr.startLine && line <= fr.endLine {
				return fr.name
			}
		}
		return ""
	}

	// ── 规则 1: MessageWithTools{Role:"assistant",...} 字面量仅在 persistAssistant
	t.Run("rule1_assistant_role_literal_scope", func(t *testing.T) {
		ast.Inspect(f, func(n ast.Node) bool {
			cl, ok := n.(*ast.CompositeLit)
			if !ok {
				return true
			}
			if !isMessageWithToolsType(cl.Type) {
				return true
			}
			for _, elt := range cl.Elts {
				kv, ok := elt.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				key, ok := kv.Key.(*ast.Ident)
				if !ok || key.Name != "Role" {
					continue
				}
				lit, ok := kv.Value.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					continue
				}
				val, _ := strconv.Unquote(lit.Value)
				if val != "assistant" {
					continue
				}
				line := fset.Position(lit.Pos()).Line
				owner := enclosingFunc(line)
				if owner != "persistAssistant" {
					t.Errorf("L%d: MessageWithTools{Role:%q} 出现在 %q 函数体内；按 P0-A structural lock 仅 persistAssistant 可写此字面",
						line, val, owner)
				}
			}
			return true
		})
	})

	// ── 规则 2: payload["role"] = "assistant" 赋值仅在 broadcastAssistant
	t.Run("rule2_assistant_payload_assignment_scope", func(t *testing.T) {
		ast.Inspect(f, func(n ast.Node) bool {
			as, ok := n.(*ast.AssignStmt)
			if !ok || len(as.Lhs) != 1 || len(as.Rhs) != 1 {
				return true
			}
			idx, ok := as.Lhs[0].(*ast.IndexExpr)
			if !ok {
				return true
			}
			keyLit, ok := idx.Index.(*ast.BasicLit)
			if !ok || keyLit.Kind != token.STRING {
				return true
			}
			keyVal, _ := strconv.Unquote(keyLit.Value)
			if keyVal != "role" {
				return true
			}
			rhsLit, ok := as.Rhs[0].(*ast.BasicLit)
			if !ok || rhsLit.Kind != token.STRING {
				return true
			}
			rhsVal, _ := strconv.Unquote(rhsLit.Value)
			if rhsVal != "assistant" {
				return true
			}
			line := fset.Position(as.Pos()).Line
			owner := enclosingFunc(line)
			if owner != "broadcastAssistant" {
				t.Errorf("L%d: payload[\"role\"] = %q 出现在 %q 函数体内；按 P0-A structural lock 仅 broadcastAssistant 可写此赋值",
					line, rhsVal, owner)
			}
			return true
		})
	})

	// ── 规则 3: assistantcap.GrantPass 第二参数必须是 requiredGuardPass 标识符
	t.Run("rule3_grantpass_second_arg_is_constant", func(t *testing.T) {
		var seen int
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			pkg, ok := sel.X.(*ast.Ident)
			if !ok || pkg.Name != "assistantcap" || sel.Sel.Name != "GrantPass" {
				return true
			}
			seen++
			if len(call.Args) != 2 {
				t.Errorf("L%d: assistantcap.GrantPass 必须 2 参；got %d",
					fset.Position(call.Pos()).Line, len(call.Args))
				return true
			}
			line := fset.Position(call.Pos()).Line
			passSrc := exprIdentChain(call.Args[1])
			if !strings.Contains(passSrc, "requiredGuardPass") {
				t.Errorf("L%d: assistantcap.GrantPass 第二参数必须是 requiredGuardPass 常量；got %s",
					line, passSrc)
			}
			return true
		})
		if seen == 0 {
			t.Fatal("未发现 assistantcap.GrantPass 调用 —— 结构性锁的 token 颁发口被绕过？")
		}
	})

	t.Run("rule4_fulfillment_gate_before_persist", func(t *testing.T) {
		var gateLine, persistLine int
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Decide" {
				if isIntentFulfillmentGateDecideCall(sel) {
					gateLine = fset.Position(call.Pos()).Line
				}
			}
			if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "persistAssistant" {
				persistLine = fset.Position(call.Pos()).Line
			}
			return true
		})
		if gateLine == 0 {
			t.Fatal("未发现 IntentFulfillmentGate Decide 调用")
		}
		if persistLine == 0 {
			t.Fatal("未发现 persistAssistant 调用")
		}
		if gateLine > persistLine {
			t.Fatalf("IntentFulfillmentGate must run before persistAssistant; gate L%d persist L%d", gateLine, persistLine)
		}
	})
}

// TestStructuralLock_AppendSessionMessagePanic 是 sink-side runtime smoke。
// 模拟红方在任意位置（pre-gate / post-gate / 别的 helper）调
// appendSessionMessage 写 Role:"assistant"，必须立刻 panic 且 panic msg 含
// `[P0-A structural lock]` 标记便于日志聚合。
func TestStructuralLock_AppendSessionMessagePanic(t *testing.T) {
	m := &Master{}
	session := &SessionState{ID: "test-session"}
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("appendSessionMessage(Role:\"assistant\") 必须 panic；未触发")
		}
		msg, _ := r.(string)
		if !strings.Contains(msg, "[P0-A structural lock]") {
			t.Fatalf("panic msg 必须含 [P0-A structural lock] prefix；got: %v", r)
		}
	}()
	m.appendSessionMessage(session, llm.MessageWithTools{
		Role:    "assistant",
		Content: llm.NewTextContent("blue-team injected pre-gate leak"),
	})
}

// TestStructuralLock_BroadcastPanic 是另一条 sink-side runtime smoke。
// EventBus.Broadcast 收到 payload["role"]="assistant" 必须 panic。
func TestStructuralLock_BroadcastPanic(t *testing.T) {
	eb := NewEventBus(zap.NewNop())
	defer eb.Close()
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("EventBus.Broadcast(payload role:\"assistant\") 必须 panic；未触发")
		}
		msg, _ := r.(string)
		if !strings.Contains(msg, "[P0-A structural lock]") {
			t.Fatalf("panic msg 必须含 [P0-A structural lock] prefix；got: %v", r)
		}
	}()
	eb.Broadcast(BroadcastMessage{
		Type:      EventTypeMessage,
		SessionID: "test-session",
		Payload: map[string]any{
			"role":    "assistant",
			"content": "blue-team injected raw broadcast leak",
		},
	})
}

// TestStructuralLock_BroadcastPanic_StringMap 锁定 map[string]string 形式 payload
// 同样被 sink-side check 覆盖（与 map[string]any 形式平行）。
func TestStructuralLock_BroadcastPanic_StringMap(t *testing.T) {
	eb := NewEventBus(zap.NewNop())
	defer eb.Close()
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("EventBus.Broadcast(map[string]string payload role:\"assistant\") 必须 panic；未触发")
		}
		msg, _ := r.(string)
		if !strings.Contains(msg, "[P0-A structural lock]") {
			t.Fatalf("panic msg 必须含 [P0-A structural lock] prefix；got: %v", r)
		}
	}()
	eb.Broadcast(BroadcastMessage{
		Type:      EventTypeMessage,
		SessionID: "test-session",
		Payload: map[string]string{
			"role":    "assistant",
			"content": "blue-team injected raw broadcast leak (string map)",
		},
	})
}

// ── helpers ──────────────────────────────────────────────────────────────

// isMessageWithToolsType 判断 CompositeLit 的 Type 是否是 llm.MessageWithTools
// 或裸 MessageWithTools（同包别名）。
func isMessageWithToolsType(t ast.Expr) bool {
	switch e := t.(type) {
	case *ast.SelectorExpr:
		pkg, ok := e.X.(*ast.Ident)
		return ok && pkg.Name == "llm" && e.Sel.Name == "MessageWithTools"
	case *ast.Ident:
		return e.Name == "MessageWithTools"
	}
	return false
}

func isIntentFulfillmentGateDecideCall(sel *ast.SelectorExpr) bool {
	expr := sel.X
	if paren, ok := expr.(*ast.ParenExpr); ok {
		expr = paren.X
	}
	composite, ok := expr.(*ast.CompositeLit)
	if !ok {
		return false
	}
	ident, ok := composite.Type.(*ast.Ident)
	return ok && ident.Name == "IntentFulfillmentGate"
}

// exprIdentChain 把表达式树打成"x.y.z"风格字符串，仅识别 Ident/SelectorExpr/
// BasicLit/ParenExpr/StarExpr/CallExpr。CallExpr 在 Fun 是 builtin 数值类型转换
// （int/int8/uint/uint8/uintptr 等）时会"穿透"，把内部唯一参数当作主表达式输出，
// 保证 `int(requiredGuardPass)` 能被识别为含 `requiredGuardPass`。
func exprIdentChain(e ast.Expr) string {
	switch v := e.(type) {
	case *ast.Ident:
		return v.Name
	case *ast.SelectorExpr:
		return exprIdentChain(v.X) + "." + v.Sel.Name
	case *ast.BasicLit:
		return v.Value
	case *ast.CallExpr:
		if id, ok := v.Fun.(*ast.Ident); ok && isNumericConvIdent(id.Name) && len(v.Args) == 1 {
			return exprIdentChain(v.Args[0])
		}
		return exprIdentChain(v.Fun) + "(...)"
	case *ast.ParenExpr:
		return exprIdentChain(v.X)
	case *ast.StarExpr:
		return "*" + exprIdentChain(v.X)
	}
	return ""
}

// isNumericConvIdent 判断标识符是否是 Go 内建数值类型，用于识别 `int(x)` 形态
// 类型转换并允许穿透到内部唯一参数。
func isNumericConvIdent(name string) bool {
	switch name {
	case "int", "int8", "int16", "int32", "int64",
		"uint", "uint8", "uint16", "uint32", "uint64", "uintptr",
		"float32", "float64", "byte", "rune":
		return true
	}
	return false
}
