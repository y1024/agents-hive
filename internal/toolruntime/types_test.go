package toolruntime

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/chef-guo/agents-hive/internal/mcphost"
	"github.com/chef-guo/agents-hive/internal/router"
	"go.uber.org/zap/zaptest"
)

func TestDescriptorFromDefinitionCarriesCapabilityEntry(t *testing.T) {
	descriptor := DescriptorFromDefinition(mcphost.ToolDefinition{Name: "feishu_api", Core: true, InputSchema: json.RawMessage(`{"type":"object"}`)})
	if descriptor.Profile.Name != "feishu_api" {
		t.Fatalf("profile name = %q, want feishu_api", descriptor.Profile.Name)
	}
	if descriptor.Entry.Name != "feishu_api" || descriptor.Entry.Kind != router.CapabilityKindBuiltinTool {
		t.Fatalf("entry = %+v, want builtin feishu_api", descriptor.Entry)
	}
	if descriptor.Entry.InputSchemaHash == "" {
		t.Fatalf("entry should carry input schema hash: %+v", descriptor.Entry)
	}
	if descriptor.Entry.PolicyProfile == "" || descriptor.Entry.Visibility == "" || descriptor.Entry.Version == "" {
		t.Fatalf("entry should carry audit metadata: %+v", descriptor.Entry)
	}
}

func TestMCPHostAdapterListsAndInvokesDescriptors(t *testing.T) {
	host := mcphost.NewHost(zaptest.NewLogger(t))
	host.RegisterTool(mcphost.ToolDefinition{Name: "read_file", Core: true}, func(ctx context.Context, input json.RawMessage) (*mcphost.ToolResult, error) {
		return &mcphost.ToolResult{Content: json.RawMessage(`"ok"`)}, nil
	})

	adapter := NewMCPHostAdapter(host)
	descriptors, err := adapter.ListToolDescriptors(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(descriptors) != 1 || descriptors[0].Entry.Name != "read_file" {
		t.Fatalf("descriptors = %+v, want read_file", descriptors)
	}
	if _, ok := adapter.LookupToolDescriptor(context.Background(), "read_file"); !ok {
		t.Fatal("LookupToolDescriptor(read_file) = false")
	}
	result, err := adapter.InvokeTool(context.Background(), Invocation{Name: "read_file"})
	if err != nil {
		t.Fatal(err)
	}
	if result == nil || result.DecodeContent() != "ok" {
		t.Fatalf("result = %+v, want ok", result)
	}
}

func TestAdmitUsesUnifiedToolPolicy(t *testing.T) {
	descriptor := DescriptorFromDefinition(mcphost.ToolDefinition{Name: "send_im_message", Core: true})
	admission := Admit(descriptor, router.ToolPolicyContext{
		Intent: router.IntentFrame{Kind: router.IntentExternalWrite, AllowsSideEffects: true, RequiresExternal: true, AllowedDomainsHint: []string{"feishu"}},
		Input:  json.RawMessage(`{"recipient_id":"ou_1","content":"hi"}`),
	})
	if admission.Policy.Action != router.ToolPolicyAllow || admission.Policy.RequiresApproval {
		t.Fatalf("admission policy = %+v, want routine allow without approval", admission.Policy)
	}
}

func TestDecideExecutionUsesInvocationIntent(t *testing.T) {
	descriptor := DescriptorFromDefinition(mcphost.ToolDefinition{Name: "send_im_message", Core: true})
	decision := DecideExecution(descriptor, Invocation{
		Name:      "send_im_message",
		Arguments: json.RawMessage(`{"platform":"feishu","recipient":"ou_1","content":"hi"}`),
		Intent:    router.IntentFrame{Kind: router.IntentExternalWrite, AllowsSideEffects: true, RequiresExternal: true},
	})

	if decision.Action != ExecutionActionAllow || decision.Policy.RequiresApproval {
		t.Fatalf("decision = %+v, want routine send allow without approval", decision)
	}
}

func TestDecideExecutionFallsBackToRouteIntent(t *testing.T) {
	descriptor := DescriptorFromDefinition(mcphost.ToolDefinition{Name: "memory", Core: true})
	decision := DecideExecution(descriptor, Invocation{
		Name:      "memory",
		Arguments: json.RawMessage(`{"operation":"save","content":"note"}`),
		Route: router.RouteDecision{
			Intent: router.IntentFrame{Kind: router.IntentWriteLocal, AllowsSideEffects: true},
		},
	})

	if decision.Action != ExecutionActionAllow || decision.Policy.RiskClass != router.ToolRiskRoutineSideEffect {
		t.Fatalf("decision = %+v, want routine local write allow from route intent", decision)
	}
}

func TestDecideExecutionAllowsDiscoveryEntrypointWithoutGrantingDiscoveredTools(t *testing.T) {
	descriptor := DescriptorFromDefinition(mcphost.ToolDefinition{Name: "tool_search", Core: true})

	routeAdmission := Admit(descriptor, router.ToolPolicyContext{
		Intent:   router.IntentFrame{Kind: router.IntentRead},
		ForRoute: true,
	})
	if routeAdmission.Policy.Action != router.ToolPolicyDeny || routeAdmission.Policy.CallableNow {
		t.Fatalf("route admission = %+v, want discovery-only deny", routeAdmission.Policy)
	}

	decision := DecideExecution(descriptor, Invocation{
		Name:      "tool_search",
		Arguments: json.RawMessage(`{"query":"memory"}`),
	})
	if decision.Action != ExecutionActionAllow || decision.Policy.Reason != "discovery_entrypoint" {
		t.Fatalf("execution decision = %+v, want discovery entrypoint allow", decision)
	}
	if decision.Policy.RouteStatus != router.ToolRouteDiscoveryOnly || decision.Policy.RequiresApproval {
		t.Fatalf("execution policy should preserve discovery metadata without approval: %+v", decision.Policy)
	}
}

func TestDecideExecutionProjectsNonCallableAskToDeny(t *testing.T) {
	descriptor := DescriptorFromDefinition(mcphost.ToolDefinition{Name: "skill", Core: true})
	decision := DecideExecution(descriptor, Invocation{
		Name:      "skill",
		Arguments: json.RawMessage(`{"name":"skill-creator"}`),
		Intent:    router.IntentFrame{Kind: router.IntentRead},
	})

	if decision.Action != ExecutionActionAsk || decision.Policy.Reason != "skill_invocation_requires_route" {
		t.Fatalf("decision = %+v, want non-callable ask preserved as ask", decision)
	}
	if !decision.RequiresConfirmation {
		t.Fatalf("non-callable ask should still carry confirmation intent: %+v", decision)
	}
}

func TestDecideExecutionAllowsNamedSkillWhenRouteIntentAuthorizesIt(t *testing.T) {
	descriptor := DescriptorFromDefinition(mcphost.ToolDefinition{Name: "skill", Core: true})
	decision := DecideExecution(descriptor, Invocation{
		Name:      "skill",
		Arguments: json.RawMessage(`{"name":"skill-creator"}`),
		Route: router.RouteDecision{
			Intent: router.IntentFrame{Kind: router.IntentCreateSkill},
		},
	})

	if decision.Action != ExecutionActionAllow || decision.Policy.Reason != "skill_invocation_route_allowed" {
		t.Fatalf("decision = %+v, want named skill allow from route intent", decision)
	}
}

func TestDecideExecutionRepairsInputsOutsideRouteDecision(t *testing.T) {
	descriptor := DescriptorFromDefinition(mcphost.ToolDefinition{Name: "feishu_api", Core: true})
	decision := DecideExecution(descriptor, Invocation{
		Name:      "feishu_api",
		Arguments: json.RawMessage(`{"action":"send_message","receive_id":"ou_1","content":"hi"}`),
		Route: router.RouteDecision{
			Intent: router.IntentFrame{Kind: router.IntentExternalRead, RequiresExternal: true},
			AllowedToolInputs: map[string]map[string]string{
				"feishu_api": {"action": "get_doc_content|read_sheet"},
			},
		},
	})

	if decision.Action != ExecutionActionRepair || decision.Source != "route_decision" || decision.Reason != "route_input_denied" {
		t.Fatalf("decision = %+v, want route input repair", decision)
	}
	if decision.RequiresConfirmation || decision.Policy.RequiresApproval {
		t.Fatalf("route input repair must not request confirmation: %+v", decision)
	}
}

func TestDecideExecutionRepairsFilesystemWriteActionOutsideReadRouteDecision(t *testing.T) {
	descriptor := DescriptorFromDefinition(mcphost.ToolDefinition{Name: "filesystem", Core: true})
	decision := DecideExecution(descriptor, Invocation{
		Name:      "filesystem",
		Arguments: json.RawMessage(`{"action":"edit","path":"README.md","old_string":"a","new_string":"b"}`),
		Route: router.RouteDecision{
			Intent: router.IntentFrame{Kind: router.IntentRead},
			AllowedToolInputs: map[string]map[string]string{
				"filesystem": {"action": "glob|grep|list|read"},
			},
		},
	})

	if decision.Action != ExecutionActionRepair || decision.Source != "route_decision" || decision.Reason != "route_input_denied" {
		t.Fatalf("decision = %+v, want route input repair", decision)
	}
	if decision.RequiresConfirmation || decision.Policy.RequiresApproval {
		t.Fatalf("filesystem route input repair must not request confirmation: %+v", decision)
	}
}

func TestDecideExecutionAllowsFilesystemReadActionInsideReadRouteDecision(t *testing.T) {
	descriptor := DescriptorFromDefinition(mcphost.ToolDefinition{Name: "filesystem", Core: true})
	decision := DecideExecution(descriptor, Invocation{
		Name:      "filesystem",
		Arguments: json.RawMessage(`{"action":"grep","pattern":"MixedAllowedToolInputsForIntent"}`),
		Route: router.RouteDecision{
			Intent: router.IntentFrame{Kind: router.IntentRead},
			AllowedToolInputs: map[string]map[string]string{
				"filesystem": {"action": "glob|grep|list|read"},
			},
		},
	})

	if decision.Action != ExecutionActionAllow || decision.Policy.Reason != "mixed_read_action" {
		t.Fatalf("decision = %+v, want filesystem read action allow", decision)
	}
}

func TestRouteInputDenyReasonFilesystemAction(t *testing.T) {
	reason := RouteInputDenyReason(Invocation{
		Name:      "filesystem",
		Arguments: json.RawMessage(`{"action":"write","path":"README.md","content":"x"}`),
		Route: router.RouteDecision{
			AllowedToolInputs: map[string]map[string]string{
				"filesystem": {"action": "glob|grep|list|read"},
			},
		},
	})

	if reason != "route_input_denied" {
		t.Fatalf("RouteInputDenyReason = %q, want route_input_denied", reason)
	}
}

func TestDecideExecutionAllowsInputsInsideRouteDecisionThenAppliesPolicy(t *testing.T) {
	descriptor := DescriptorFromDefinition(mcphost.ToolDefinition{Name: "feishu_api", Core: true})
	decision := DecideExecution(descriptor, Invocation{
		Name:      "feishu_api",
		Arguments: json.RawMessage(`{"action":"send_message","receive_id":"ou_1","content":"hi"}`),
		Route: router.RouteDecision{
			Intent: router.IntentFrame{Kind: router.IntentExternalWrite, RequiresExternal: true, AllowsSideEffects: true},
			AllowedToolInputs: map[string]map[string]string{
				"feishu_api": {"action": "search_contacts|send_message"},
			},
		},
	})

	if decision.Action != ExecutionActionAllow || decision.Policy.Reason != "routine_external_send_action" {
		t.Fatalf("decision = %+v, want route input allow then routine send allow", decision)
	}
}

func TestDecideExecutionAppliesIntentCapabilityGate(t *testing.T) {
	descriptor := DescriptorFromDefinition(mcphost.ToolDefinition{Name: "feishu_api", Core: true})
	decision := DecideExecution(descriptor, Invocation{
		Name:      "feishu_api",
		Arguments: json.RawMessage(`{"action":"send_message","receive_id":"ou_1","content":"hi"}`),
		Intent: router.IntentFrame{
			Kind:               router.IntentExternalWrite,
			RequiresExternal:   true,
			AllowsSideEffects:  true,
			AllowedDomainsHint: []string{"wechatbot"},
		},
	})

	if decision.Action != ExecutionActionAsk || decision.Policy.Reason != "capability missing" {
		t.Fatalf("decision = %+v, want platform capability approval", decision)
	}
	if !decision.RequiresConfirmation || !decision.Policy.RequiresApproval {
		t.Fatalf("capability mismatch should be confirmable, decision=%+v", decision)
	}
}

func TestDecideExecutionAllowsReadOnlyPreparationUnderExternalWriteIntent(t *testing.T) {
	descriptor := DescriptorFromDefinition(mcphost.ToolDefinition{Name: "read_file", Core: true})
	decision := DecideExecution(descriptor, Invocation{
		Name:      "read_file",
		Arguments: json.RawMessage(`{"path":"/tmp/tool-policy-smoke.txt"}`),
		Intent: router.IntentFrame{
			Kind:               router.IntentExternalWrite,
			RequiresExternal:   true,
			AllowsSideEffects:  true,
			AllowedDomainsHint: []string{"feishu"},
		},
	})

	if decision.Action != ExecutionActionAllow || decision.Policy.Reason != "read_only" {
		t.Fatalf("decision = %+v, want read-only preparation allow under external write intent", decision)
	}
}
