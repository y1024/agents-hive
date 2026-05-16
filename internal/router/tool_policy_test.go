package router

import (
	"encoding/json"
	"testing"

	"github.com/chef-guo/agents-hive/internal/mcphost"
)

func TestEvaluateToolPolicyUnifiedMatrix(t *testing.T) {
	tests := []struct {
		name         string
		def          mcphost.ToolDefinition
		intent       IntentFrame
		input        json.RawMessage
		wantAction   ToolPolicyAction
		wantRoute    ToolRouteStatus
		wantCallable bool
		wantApproval bool
		wantMayAsk   bool
		wantRisk     ToolRiskClass
	}{
		{
			name:         "builtin read only allow",
			def:          mcphost.ToolDefinition{Name: "read_file", Core: true},
			intent:       IntentFrame{Kind: IntentRead},
			wantAction:   ToolPolicyAllow,
			wantRoute:    ToolRouteCallableReadOnly,
			wantCallable: true,
			wantRisk:     ToolRiskReadOnly,
		},
		{
			name:         "concurrency safe custom allow",
			def:          mcphost.ToolDefinition{Name: "project_status", Description: "查询项目状态", IsConcurrencySafe: true},
			intent:       IntentFrame{Kind: IntentAnswer},
			wantAction:   ToolPolicyAllow,
			wantRoute:    ToolRouteCallableReadOnly,
			wantCallable: true,
			wantRisk:     ToolRiskReadOnly,
		},
		{
			name:       "unknown custom deny",
			def:        mcphost.ToolDefinition{Name: "opaque_candidate", Description: "opaque extension"},
			intent:     IntentFrame{Kind: IntentAnswer},
			wantAction: ToolPolicyDeny,
			wantRoute:  ToolRouteBlockedUnknown,
			wantMayAsk: true,
			wantRisk:   ToolRiskUnknown,
		},
		{
			name:         "trusted remote read allow",
			def:          mcphost.ToolDefinition{Name: "metamcp__query_prometheus", Description: "Query Prometheus metrics", SourceServer: "metamcp", Trusted: true},
			intent:       IntentFrame{Kind: IntentExternalRead, RequiresExternal: true},
			wantAction:   ToolPolicyAllow,
			wantRoute:    ToolRouteCallableReadOnly,
			wantCallable: true,
			wantRisk:     ToolRiskReadOnly,
		},
		{
			name:       "trusted remote side effect requires write intent",
			def:        mcphost.ToolDefinition{Name: "metamcp__create_annotation", Description: "Create Grafana annotation", SourceServer: "metamcp", Trusted: true},
			intent:     IntentFrame{Kind: IntentExternalRead, RequiresExternal: true},
			wantAction: ToolPolicyAsk,
			wantRoute:  ToolRouteRequiresSideEffectIntent,
			wantApproval: true,
			wantMayAsk: true,
			wantRisk:   ToolRiskPrivilegedSideEffect,
		},
		{
			name:         "trusted remote side effect asks under write intent",
			def:          mcphost.ToolDefinition{Name: "metamcp__create_annotation", Description: "Create Grafana annotation", SourceServer: "metamcp", Trusted: true},
			intent:       IntentFrame{Kind: IntentExternalWrite, RequiresExternal: true, AllowsSideEffects: true},
			wantAction:   ToolPolicyAsk,
			wantRoute:    ToolRouteRequiresSideEffectIntent,
			wantCallable: true,
			wantApproval: true,
			wantMayAsk:   true,
			wantRisk:     ToolRiskPrivilegedSideEffect,
		},
		{
			name:       "trusted remote destructive deny",
			def:        mcphost.ToolDefinition{Name: "metamcp__delete_dashboard", Description: "Delete Grafana dashboard", SourceServer: "metamcp", Trusted: true},
			intent:     IntentFrame{Kind: IntentExternalWrite, RequiresExternal: true, AllowsSideEffects: true},
			wantAction: ToolPolicyAsk,
			wantRoute:  ToolRouteBlockedDangerous,
			wantCallable: true,
			wantApproval: true,
			wantMayAsk: true,
			wantRisk:   ToolRiskDestructive,
		},
		{
			name:       "untrusted remote deny",
			def:        mcphost.ToolDefinition{Name: "github__create_issue", Description: "Create GitHub issue"},
			intent:     IntentFrame{Kind: IntentExternalWrite, RequiresExternal: true, AllowsSideEffects: true},
			wantAction: ToolPolicyAsk,
			wantRoute:  ToolRouteBlockedDangerous,
			wantCallable: true,
			wantApproval: true,
			wantMayAsk: true,
			wantRisk:   ToolRiskDestructive,
		},
		{
			name:         "mixed read action allow with constraints",
			def:          mcphost.ToolDefinition{Name: "memory", Core: true},
			intent:       IntentFrame{Kind: IntentRead},
			input:        json.RawMessage(`{"operation":"search","query":"tool policy"}`),
			wantAction:   ToolPolicyAllow,
			wantRoute:    ToolRouteCallableWithActionConstraints,
			wantCallable: true,
			wantMayAsk:   true,
			wantRisk:     ToolRiskReadOnly,
		},
		{
			name:         "mixed dangerous action asks",
			def:          mcphost.ToolDefinition{Name: "memory", Core: true},
			intent:       IntentFrame{Kind: IntentWriteLocal, AllowsSideEffects: true},
			input:        json.RawMessage(`{"operation":"delete","id":"m1"}`),
			wantAction:   ToolPolicyAsk,
			wantRoute:    ToolRouteRequiresSideEffectIntent,
			wantCallable: true,
			wantApproval: true,
			wantMayAsk:   true,
			wantRisk:     ToolRiskPrivilegedSideEffect,
		},
		{
			name:         "memory save is routine local write under write intent",
			def:          mcphost.ToolDefinition{Name: "memory", Core: true},
			intent:       IntentFrame{Kind: IntentWriteLocal, AllowsSideEffects: true},
			input:        json.RawMessage(`{"operation":"save","content":"note"}`),
			wantAction:   ToolPolicyAllow,
			wantRoute:    ToolRouteCallableWithActionConstraints,
			wantCallable: true,
			wantMayAsk:   true,
			wantRisk:     ToolRiskRoutineSideEffect,
		},
		{
			name:       "runtime exec read intent blocked",
			def:        mcphost.ToolDefinition{Name: "bash", Core: true},
			intent:     IntentFrame{Kind: IntentRead},
			input:      json.RawMessage(`{"command":"pwd"}`),
			wantAction: ToolPolicyAsk,
			wantRoute:  ToolRouteBlockedDangerous,
			wantCallable: true,
			wantApproval: true,
			wantMayAsk: true,
			wantRisk:   ToolRiskRuntimeExec,
		},
		{
			name:         "runtime exec manage intent still requires safe executor approval path",
			def:          mcphost.ToolDefinition{Name: "bash", Core: true},
			intent:       IntentFrame{Kind: IntentManageTool, AllowsSideEffects: true},
			input:        json.RawMessage(`{"command":"pwd"}`),
			wantAction:   ToolPolicyAsk,
			wantRoute:    ToolRouteRequiresMatchingIntent,
			wantCallable: true,
			wantApproval: true,
			wantMayAsk:   true,
			wantRisk:     ToolRiskRuntimeExec,
		},
		{
			name:         "mixed external upload action requires approval",
			def:          mcphost.ToolDefinition{Name: "feishu_api", Core: true},
			intent:       IntentFrame{Kind: IntentExternalWrite, RequiresExternal: true, AllowsSideEffects: true},
			input:        json.RawMessage(`{"action":"upload_file","data":"base64","filename":"a.txt"}`),
			wantAction:   ToolPolicyAsk,
			wantRoute:    ToolRouteRequiresSideEffectIntent,
			wantCallable: true,
			wantApproval: true,
			wantMayAsk:   true,
			wantRisk:     ToolRiskPrivilegedSideEffect,
		},
		{
			name:         "mixed routine external send action allow under write intent",
			def:          mcphost.ToolDefinition{Name: "feishu_api", Core: true},
			intent:       IntentFrame{Kind: IntentExternalWrite, RequiresExternal: true, AllowsSideEffects: true},
			input:        json.RawMessage(`{"action":"send_message","chat_id":"oc_1","content":"hi"}`),
			wantAction:   ToolPolicyAllow,
			wantRoute:    ToolRouteCallableWithActionConstraints,
			wantCallable: true,
			wantMayAsk:   true,
			wantRisk:     ToolRiskRoutineSideEffect,
		},
		{
			name:         "im api routine send action allow under write intent",
			def:          mcphost.ToolDefinition{Name: "im_api", Core: true},
			intent:       IntentFrame{Kind: IntentExternalWrite, RequiresExternal: true, AllowsSideEffects: true},
			input:        json.RawMessage(`{"action":"send_message","platform":"feishu","recipient_id":"user-1","content":"hi"}`),
			wantAction:   ToolPolicyAllow,
			wantRoute:    ToolRouteCallableWithActionConstraints,
			wantCallable: true,
			wantRisk:     ToolRiskRoutineSideEffect,
		},
		{
			name:         "send im routine text allow under write intent",
			def:          mcphost.ToolDefinition{Name: "send_im_message", Core: true},
			intent:       IntentFrame{Kind: IntentExternalWrite, RequiresExternal: true, AllowsSideEffects: true},
			input:        json.RawMessage(`{"platform":"feishu","recipient":"oc_1","content":"hi"}`),
			wantAction:   ToolPolicyAllow,
			wantRoute:    ToolRouteCallableWithActionConstraints,
			wantCallable: true,
			wantRisk:     ToolRiskRoutineSideEffect,
		},
		{
			name:         "send im media payload asks under write intent",
			def:          mcphost.ToolDefinition{Name: "send_im_message", Core: true},
			intent:       IntentFrame{Kind: IntentExternalWrite, RequiresExternal: true, AllowsSideEffects: true},
			input:        json.RawMessage(`{"platform":"feishu","recipient":"oc_1","content":"hi","file_key":"file"}`),
			wantAction:   ToolPolicyAsk,
			wantRoute:    ToolRouteRequiresSideEffectIntent,
			wantCallable: true,
			wantApproval: true,
			wantMayAsk:   true,
			wantRisk:     ToolRiskPrivilegedSideEffect,
		},
		{
			name:       "mixed external send action blocked for read intent",
			def:        mcphost.ToolDefinition{Name: "feishu_api", Core: true},
			intent:     IntentFrame{Kind: IntentRead},
			input:      json.RawMessage(`{"action":"send_message","chat_id":"oc_1","content":"hi"}`),
			wantAction: ToolPolicyAsk,
			wantRoute:  ToolRouteRequiresSideEffectIntent,
			wantCallable: true,
			wantApproval: true,
			wantMayAsk: true,
			wantRisk:   ToolRiskRoutineSideEffect,
		},
		{
			name:       "mixed unknown action blocked",
			def:        mcphost.ToolDefinition{Name: "memory", Core: true},
			intent:     IntentFrame{Kind: IntentWriteLocal, AllowsSideEffects: true},
			input:      json.RawMessage(`{"operation":"saev","content":"typo"}`),
			wantAction: ToolPolicyAsk,
			wantRoute:  ToolRouteBlockedUnknown,
			wantCallable: true,
			wantApproval: true,
			wantMayAsk: true,
			wantRisk:   ToolRiskUnknown,
		},
		{
			name:       "unclassified deny still requires attention",
			def:        mcphost.ToolDefinition{Name: "question", Core: true},
			intent:     IntentFrame{Kind: IntentAnswer},
			input:      json.RawMessage(`{}`),
			wantAction: ToolPolicyAsk,
			wantRoute:  ToolRouteBlockedUnknown,
			wantCallable: true,
			wantApproval: true,
			wantMayAsk: true,
			wantRisk:   ToolRiskUnknown,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			profile := InferToolProfile(tt.def, ProfileHint{})
			if tt.name == "unclassified deny still requires attention" {
				profile.ReadOnly = false
				profile.Risk = RiskReadOnly
			}
			got := EvaluateToolPolicy(profile, ToolPolicyContext{
				Intent:    tt.intent,
				Input:     tt.input,
				ForRoute:  true,
				ForAction: true,
			})
			if got.Action != tt.wantAction {
				t.Fatalf("Action = %q, want %q, full=%+v profile=%+v", got.Action, tt.wantAction, got, profile)
			}
			if got.RouteStatus != tt.wantRoute {
				t.Fatalf("RouteStatus = %q, want %q, full=%+v profile=%+v", got.RouteStatus, tt.wantRoute, got, profile)
			}
			if got.CallableNow != tt.wantCallable {
				t.Fatalf("CallableNow = %v, want %v, full=%+v profile=%+v", got.CallableNow, tt.wantCallable, got, profile)
			}
			if got.RequiresApproval != tt.wantApproval {
				t.Fatalf("RequiresApproval = %v, want %v, full=%+v profile=%+v", got.RequiresApproval, tt.wantApproval, got, profile)
			}
			if got.MayRequireApproval != tt.wantMayAsk {
				t.Fatalf("MayRequireApproval = %v, want %v, full=%+v profile=%+v", got.MayRequireApproval, tt.wantMayAsk, got, profile)
			}
			if got.RiskClass != tt.wantRisk {
				t.Fatalf("RiskClass = %q, want %q, full=%+v profile=%+v", got.RiskClass, tt.wantRisk, got, profile)
			}
			if tt.name == "unclassified deny still requires attention" && got.Reason != "unclassified_tool_policy" {
				t.Fatalf("Reason = %q, want unclassified_tool_policy, full=%+v profile=%+v", got.Reason, got, profile)
			}
		})
	}
}

func TestToolSearchRouteAndActionSemanticsAreSplit(t *testing.T) {
	profile := InferToolProfile(mcphost.ToolDefinition{Name: "tool_search", Core: true}, ProfileHint{})

	routePolicy := EvaluateToolPolicy(profile, ToolPolicyContext{
		Intent:   IntentFrame{Kind: IntentRead},
		ForRoute: true,
	})
	if routePolicy.Action != ToolPolicyDeny || routePolicy.CallableNow || routePolicy.Reason != "discovery_only" {
		t.Fatalf("route policy = %+v, want visible-only discovery deny", routePolicy)
	}

	actionPolicy := EvaluateToolPolicy(profile, ToolPolicyContext{
		Intent:    IntentFrame{Kind: IntentRead},
		Input:     json.RawMessage(`{"query":"memory"}`),
		ForAction: true,
	})
	if actionPolicy.Action != ToolPolicyAllow || !actionPolicy.CallableNow || actionPolicy.Reason != "discovery_entrypoint" {
		t.Fatalf("action policy = %+v, want discovery entrypoint allow", actionPolicy)
	}
	if actionPolicy.RouteStatus != ToolRouteDiscoveryOnly || actionPolicy.RequiresApproval || actionPolicy.MayRequireApproval {
		t.Fatalf("action policy should preserve discovery-only metadata without approval: %+v", actionPolicy)
	}
}

func TestQuestionEntrypointActionBypassesExternalSendCapabilityGate(t *testing.T) {
	profile := InferToolProfile(mcphost.ToolDefinition{Name: "question", Core: true}, ProfileHint{})
	policy := EvaluateToolPolicy(profile, ToolPolicyContext{
		Intent: IntentFrame{
			Kind:               IntentExternalWrite,
			RequiresExternal:   true,
			AllowsSideEffects:  true,
			AllowedDomainsHint: []string{"feishu", "wechatbot"},
		},
		Input:     json.RawMessage(`{"question":"请选择发送平台","options":["飞书","微信"]}`),
		ForAction: true,
	})

	if policy.Action != ToolPolicyAllow || !policy.CallableNow || policy.Reason != "question_entrypoint" {
		t.Fatalf("question action policy = %+v, want read-only entrypoint allow", policy)
	}
	if policy.RequiresApproval || policy.MayRequireApproval || policy.RiskClass != ToolRiskReadOnly {
		t.Fatalf("question should not inherit external-send approval/capability requirement: %+v", policy)
	}
}

func TestSkillListOnlyActionIsReadOnlyWhileNamedSkillStillRequiresIntent(t *testing.T) {
	profile := InferToolProfile(mcphost.ToolDefinition{Name: "skill", Core: true}, ProfileHint{})

	listPolicy := EvaluateToolPolicy(profile, ToolPolicyContext{
		Input:     json.RawMessage(`{}`),
		ForAction: true,
	})
	if listPolicy.Action != ToolPolicyAllow || listPolicy.RiskClass != ToolRiskReadOnly || listPolicy.RequiresApproval {
		t.Fatalf("skill list policy = %+v, want read-only allow", listPolicy)
	}

	invokePolicy := EvaluateToolPolicy(profile, ToolPolicyContext{
		Input:     json.RawMessage(`{"name":"frontend-design"}`),
		ForAction: true,
	})
	if invokePolicy.Action != ToolPolicyAsk || !invokePolicy.CallableNow || !invokePolicy.RequiresSideEffectIntent {
		t.Fatalf("named skill policy = %+v, want confirmable side-effect intent requirement", invokePolicy)
	}
	if !invokePolicy.RequiresApproval {
		t.Fatalf("named skill ask should require approval metadata: %+v", invokePolicy)
	}
}

func TestNamedSkillActionUsesRouteIntentBeforeCapabilityGate(t *testing.T) {
	profile := InferToolProfile(mcphost.ToolDefinition{Name: "skill", Core: true}, ProfileHint{})

	allowed := EvaluateToolPolicy(profile, ToolPolicyContext{
		Intent:    IntentFrame{Kind: IntentCreateSkill},
		Input:     json.RawMessage(`{"name":"skill-creator"}`),
		ForAction: true,
	})
	if allowed.Action != ToolPolicyAllow || !allowed.CallableNow || allowed.Reason != "skill_invocation_route_allowed" {
		t.Fatalf("named skill with route intent = %+v, want route-authorized allow", allowed)
	}

	blocked := EvaluateToolPolicy(profile, ToolPolicyContext{
		Intent:    IntentFrame{Kind: IntentRead},
		Input:     json.RawMessage(`{"name":"skill-creator"}`),
		ForAction: true,
	})
	if blocked.Action != ToolPolicyAsk || !blocked.CallableNow || blocked.Reason != "skill_invocation_requires_route" {
		t.Fatalf("named skill without route intent = %+v, want confirmable route requirement", blocked)
	}
	if !blocked.RequiresApproval {
		t.Fatalf("named skill outside route should become HITL approval: %+v", blocked)
	}
}

func TestPlanControlActionUsesUnifiedRoutineSideEffectPolicy(t *testing.T) {
	profile := InferToolProfile(mcphost.ToolDefinition{Name: "todo_write", Core: true}, ProfileHint{})
	policy := EvaluateToolPolicy(profile, ToolPolicyContext{
		Input:     json.RawMessage(`{"expected_plan_version":0,"todos":[]}`),
		ForAction: true,
	})

	if policy.Action != ToolPolicyAllow || !policy.CallableNow {
		t.Fatalf("todo_write action policy = %+v, want routine plan control allow", policy)
	}
	if policy.RiskClass != ToolRiskRoutineSideEffect || policy.RequiresApproval {
		t.Fatalf("todo_write should be routine side-effect without blanket approval: %+v", policy)
	}
}
