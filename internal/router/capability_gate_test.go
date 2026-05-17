package router

import (
	"strings"
	"testing"

	"github.com/chef-guo/agents-hive/internal/mcphost"
)

func TestCapabilityGateAllowsWhenIntentToolSessionAndPlanIntersect(t *testing.T) {
	got := CheckCapabilityGate(CapabilityGateInput{
		IntentRequired: []Capability{CapabilityExternalSend},
		ToolGranted:    []Capability{CapabilityExternalSend, CapabilityRuntimeExec},
		SessionGranted: []Capability{CapabilityExternalSend},
		PlanAllowed:    []Capability{CapabilityExternalSend},
	})
	if !got.Allowed {
		t.Fatalf("Allowed = false, want true: %+v", got)
	}
}

func TestCapabilityGateFailsClosedOnMissingOrDeniedCapability(t *testing.T) {
	missing := CheckCapabilityGate(CapabilityGateInput{
		IntentRequired: []Capability{CapabilityMetaToolRegister},
		ToolGranted:    []Capability{CapabilityMetaSkillCreate},
	})
	if missing.Allowed || missing.Reason != "capability missing" {
		t.Fatalf("missing result = %+v, want capability missing", missing)
	}

	denied := CheckCapabilityGate(CapabilityGateInput{
		IntentRequired: []Capability{CapabilityExternalSend},
		ToolGranted:    []Capability{CapabilityExternalSend},
		Deny:           []Capability{CapabilityExternalSend},
	})
	if denied.Allowed || denied.Reason != "capability denied" {
		t.Fatalf("denied result = %+v, want capability denied", denied)
	}
}

func TestRequiredCapabilitiesForIntent(t *testing.T) {
	tests := []struct {
		intent IntentKind
		want   Capability
	}{
		{IntentCreateSkill, CapabilityMetaSkillCreate},
		{IntentModifySkill, CapabilityMetaSkillModify},
		{IntentManageTool, CapabilityMetaToolRegister},
		{IntentExternalWrite, CapabilityExternalSend},
	}
	for _, tt := range tests {
		got := RequiredCapabilitiesForIntent(IntentFrame{Kind: tt.intent})
		if len(got) != 1 || got[0] != tt.want {
			t.Fatalf("RequiredCapabilitiesForIntent(%q) = %+v, want %q", tt.intent, got, tt.want)
		}
	}
	if got := RequiredCapabilitiesForIntent(IntentFrame{Kind: IntentRead}); len(got) != 0 {
		t.Fatalf("read intent should not require write capability, got %+v", got)
	}
}

func TestCapabilityGateExternalSendPlatformHints(t *testing.T) {
	tests := []struct {
		name  string
		hints []string
		want  Capability
	}{
		{name: "feishu", hints: []string{"feishu"}, want: CapabilityExternalSendFeishu},
		{name: "wechatbot", hints: []string{"wechatbot"}, want: CapabilityExternalSendWechatBot},
		{name: "wecom", hints: []string{"wecom"}, want: CapabilityExternalSendWeCom},
		{name: "dingtalk", hints: []string{"dingtalk"}, want: CapabilityExternalSendDingTalk},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			required := RequiredCapabilitiesForIntent(IntentFrame{Kind: IntentExternalWrite, AllowedDomainsHint: tt.hints})
			if len(required) != 1 || required[0] != tt.want {
				t.Fatalf("RequiredCapabilitiesForIntent(%q) = %+v, want %q", tt.name, required, tt.want)
			}
			umbrellaOnly := CheckCapabilityGate(CapabilityGateInput{
				IntentRequired: required,
				ToolGranted:    []Capability{CapabilityExternalSend},
			})
			if umbrellaOnly.Allowed {
				t.Fatalf("umbrella external-send must not satisfy %q platform route", tt.name)
			}
		})
	}
}

func TestCapabilityGateMultiPlatformExternalSendIsUnsatisfied(t *testing.T) {
	required := RequiredCapabilitiesForIntent(IntentFrame{
		Kind:               IntentExternalWrite,
		AllowedDomainsHint: []string{"feishu", "wechatbot"},
	})
	if len(required) != 1 || required[0] == CapabilityExternalSend {
		t.Fatalf("multi-platform requirements = %+v, want explicit unsatisfied capability", required)
	}
	got := CheckCapabilityGate(CapabilityGateInput{
		IntentRequired: required,
		ToolGranted:    allExternalSendCapabilities(),
	})
	if got.Allowed {
		t.Fatalf("multi-platform external send must stay unauthorized: %+v", got)
	}
}

func TestCapabilityRegistryReturnsCopies(t *testing.T) {
	required := RequiredCapabilitiesForIntent(IntentFrame{Kind: IntentCreateSkill})
	required[0] = CapabilityExternalSend
	got := RequiredCapabilitiesForIntent(IntentFrame{Kind: IntentCreateSkill})
	if len(got) != 1 || got[0] != CapabilityMetaSkillCreate {
		t.Fatalf("intent rule leaked mutable slice: %+v", got)
	}

	caps := inferSkillWorkflowCapabilities("skill_authoring")
	caps[0] = CapabilityRuntimeExec
	gotCaps := inferSkillWorkflowCapabilities("skill_authoring")
	if len(gotCaps) != 2 || gotCaps[0] != CapabilityMetaSkillCreate {
		t.Fatalf("skill domain rule leaked mutable slice: %+v", gotCaps)
	}
}

func TestBuiltinCapabilityRegistryCoversCommonHostToolNames(t *testing.T) {
	for _, name := range []string{
		"websearch",
		"webfetch",
		"browser_interact",
		"filesystem",
		"todo_write",
		"taskboard",
		"im_api",
		"send_im_message",
		"skill_install",
		"skill_search",
		"generate_video",
		"lsp_diagnostics",
	} {
		t.Run(name, func(t *testing.T) {
			rule, ok := builtinToolRule(name)
			if !ok {
				t.Fatalf("builtinToolRule(%q) missing", name)
			}
			if rule.Domain == "" || rule.Invocation == "" || rule.Risk == "" {
				t.Fatalf("builtinToolRule(%q) incomplete: %+v", name, rule)
			}
		})
	}
}

func TestHostToolSets(t *testing.T) {
	if !IsHostToolInSet(HostToolSetDefaultVisible, "tool_search") {
		t.Fatal("tool_search should be default-visible as discovery entrypoint")
	}
	if !IsHostToolInSet(HostToolSetDefaultVisible, "filesystem") {
		t.Fatal("filesystem should be default-visible with read-only action constraints")
	}
	if IsHostToolInSet(HostToolSetDefaultVisible, "send_im_message") {
		t.Fatal("side-effect messaging tool must not be default-visible")
	}
	for _, name := range []string{"batch", "task", "spawn_agent", "parallel_dispatch"} {
		if IsHostToolInSet(HostToolSetDefaultVisible, name) {
			t.Fatalf("execution entrypoint %q must not be default-visible", name)
		}
	}
	if !IsHostToolInSet(HostToolSetPlanControl, "finish_plan") {
		t.Fatal("finish_plan should be a plan control tool")
	}
	if !IsHostToolInSet(HostToolSetPlanAllowed, "read_file") {
		t.Fatal("read_file should be allowed in plan mode")
	}
	if !IsHostToolInSet(HostToolSetPlanAllowed, "filesystem") {
		t.Fatal("filesystem should be allowed in plan mode with read-only action constraints")
	}
	if IsHostToolInSet(HostToolSetPlanAllowed, "bash") {
		t.Fatal("bash must not be allowed in plan mode")
	}
	members := HostToolSetMembers(HostToolSetPlanAllowed)
	if len(members) == 0 {
		t.Fatal("plan allowed set should not be empty")
	}
	members[0] = "mutated"
	if IsHostToolInSet(HostToolSetPlanAllowed, "mutated") {
		t.Fatal("HostToolSetMembers must return a copy")
	}
}

func TestCapabilityRegistryHostToolSetsAreSubsetsOfBuiltinRules(t *testing.T) {
	for _, set := range []HostToolSet{HostToolSetDefaultVisible, HostToolSetPlanControl, HostToolSetPlanAllowed} {
		for _, name := range HostToolSetMembers(set) {
			if !IsKnownHostTool(name) {
				t.Fatalf("%s contains unknown host tool %q", set, name)
			}
		}
	}
}

func TestCapabilityRegistryStructuredDangerousOperations(t *testing.T) {
	tests := []struct {
		name     string
		tool     string
		input    string
		wantRisk bool
	}{
		{name: "feishu send", tool: "feishu_api", input: `{"action":"send_message"}`, wantRisk: false},
		{name: "feishu read", tool: "feishu_api", input: `{"action":"get_user"}`, wantRisk: false},
		{name: "send im", tool: "send_im_message", input: `{"platform":"feishu"}`, wantRisk: false},
		{name: "feishu create approval", tool: "feishu_api", input: `{"action":"create_approval"}`, wantRisk: true},
		{name: "memory save", tool: "memory", input: `{"operation":"save","content":"note"}`, wantRisk: false},
		{name: "memory delete", tool: "memory", input: `{"operation":"delete","id":1}`, wantRisk: true},
		{name: "taskboard update", tool: "taskboard", input: `{"operation":"update","id":"task-1","status":"done"}`, wantRisk: false},
		{name: "taskboard delete", tool: "taskboard", input: `{"operation":"delete","id":"task-1"}`, wantRisk: true},
		{name: "create tool", tool: "create_tool", input: `{}`, wantRisk: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := StructuredDangerousOperation(tt.tool, []byte(tt.input)); got != tt.wantRisk {
				t.Fatalf("StructuredDangerousOperation(%q, %s) = %v, want %v", tt.tool, tt.input, got, tt.wantRisk)
			}
		})
	}
}

func TestCapabilityRegistryFeishuIsMixedActionAware(t *testing.T) {
	profile := InferToolProfile(mcphost.ToolDefinition{Name: "feishu_api"}, ProfileHint{})
	if !IsMixedReadWriteTool("feishu_api") {
		t.Fatal("feishu_api should be registered as a mixed read/write tool")
	}
	if ProfileRequiresApproval(profile) {
		t.Fatalf("feishu_api should not be blanket approval-required; dangerous actions are action-level: %+v", profile)
	}
	if !StructuredDangerousAction("feishu_api", "create_task") {
		t.Fatal("create_task should remain action-level dangerous")
	}
	if StructuredDangerousAction("feishu_api", "send_message") {
		t.Fatal("normal send_message should not be action-level dangerous")
	}
	readActions := strings.Join(MixedReadOnlyActions("feishu_api"), "|")
	if !strings.Contains(readActions, "get_doc_content") || strings.Contains(readActions, "send_message") {
		t.Fatalf("read-only action set wrong: %q", readActions)
	}
	sendActions := strings.Join(ExternalSendActions("feishu_api"), "|")
	if !strings.Contains(sendActions, "send_message") || strings.Contains(sendActions, "create_task") {
		t.Fatalf("external-send action set wrong: %q", sendActions)
	}
	routineActions := strings.Join(RoutineSideEffectActions("feishu_api"), "|")
	if routineActions != "send_message" {
		t.Fatalf("routine action set = %q, want send_message", routineActions)
	}
	privilegedActions := strings.Join(PrivilegedActions("feishu_api"), "|")
	if !strings.Contains(privilegedActions, "send_file") || !strings.Contains(privilegedActions, "create_task") || strings.Contains(privilegedActions, "send_message") {
		t.Fatalf("privileged action set wrong: %q", privilegedActions)
	}
	if !StructuredRoutineSideEffectAction("feishu_api", "send_message") {
		t.Fatal("send_message should be routine after concrete input validation")
	}
	if !StructuredPrivilegedAction("feishu_api", "send_file") || !StructuredPrivilegedAction("feishu_api", "create_task") {
		t.Fatal("send_file and create_task should remain privileged actions")
	}
}

func TestActionCapabilityRegistryDrivesBusinessWriteSemantics(t *testing.T) {
	matches := MatchActionCapabilityRulesForText("创建一个飞书任务，标题是跟进合同")
	if len(matches) != 1 {
		t.Fatalf("task create query should match one action capability, got %+v", matches)
	}
	got := matches[0]
	if got.ToolName != "feishu_api" || got.Action != "create_task" || got.CapabilityID != ActionCapabilityExternalTaskCreate {
		t.Fatalf("task create capability = %+v", got)
	}
	if !containsString(got.RequiredFields, "summary") {
		t.Fatalf("create_task required fields = %+v, want summary", got.RequiredFields)
	}
	signal := ActionCapabilitySignal(got.CapabilityID)
	if signal != "action_capability:external.task.create" {
		t.Fatalf("ActionCapabilitySignal = %q", signal)
	}
	intent := IntentFrame{Signals: []string{IntentSignalExternalBusinessWrite, signal}}
	ids := IntentActionCapabilityIDs(intent)
	if len(ids) != 1 || ids[0] != ActionCapabilityExternalTaskCreate {
		t.Fatalf("IntentActionCapabilityIDs = %+v", ids)
	}
	actions := ExternalWriteActionsForIntent("feishu_api", IntentFrame{
		Kind:              IntentExternalWrite,
		AllowsSideEffects: true,
		Signals:           []string{IntentSignalExternalBusinessWrite, signal},
	})
	for _, action := range []string{"search_contacts", "get_user_info", "list_tasks", "create_task"} {
		if !containsString(actions, action) {
			t.Fatalf("task create actions missing %q: %+v", action, actions)
		}
	}
	for _, action := range []string{"write_sheet", "create_approval", "send_message"} {
		if containsString(actions, action) {
			t.Fatalf("task create actions should not include %q: %+v", action, actions)
		}
	}
}

func TestCapabilityRegistryMixedOperationToolsAreActionAware(t *testing.T) {
	for _, toolName := range []string{"memory", "taskboard", "browser_interact", "filesystem"} {
		t.Run(toolName, func(t *testing.T) {
			profile := InferToolProfile(mcphost.ToolDefinition{Name: toolName, Core: true}, ProfileHint{})
			if !IsMixedReadWriteTool(toolName) {
				t.Fatalf("%s should be registered as a mixed read/write tool", toolName)
			}
			if ProfileRequiresApproval(profile) {
				t.Fatalf("%s should not be blanket approval-required; dangerous actions are action-level: %+v", toolName, profile)
			}
			if len(MixedReadOnlyActions(toolName)) == 0 {
				t.Fatalf("%s should expose read-only actions", toolName)
			}
		})
	}
	if MixedActionField("memory") != "operation" || MixedActionField("taskboard") != "operation" {
		t.Fatalf("memory/taskboard should use operation field, got memory=%q taskboard=%q", MixedActionField("memory"), MixedActionField("taskboard"))
	}
	if MixedActionField("browser_interact") != "commands[].action" {
		t.Fatalf("browser_interact action field = %q, want commands[].action", MixedActionField("browser_interact"))
	}
	if MixedActionField("filesystem") != "action" {
		t.Fatalf("filesystem action field = %q, want action", MixedActionField("filesystem"))
	}
	if !StructuredDangerousAction("memory", "delete") || !StructuredDangerousAction("taskboard", "delete") {
		t.Fatal("delete operations should remain action-level dangerous")
	}
}

func TestCapabilityRegistryFilesystemActionsAndProfiles(t *testing.T) {
	profile := InferToolProfile(mcphost.ToolDefinition{Name: "filesystem", Core: true}, ProfileHint{})
	if profile.Domain != "filesystem" || profile.Risk != RiskLocalWrite || !profile.SideEffect {
		t.Fatalf("filesystem builtin profile = %+v, want filesystem local-write mixed entrypoint", profile)
	}

	readActions := strings.Join(MixedReadOnlyActions("filesystem"), "|")
	for _, action := range []string{"list", "glob", "grep", "read"} {
		if !containsPipeAction(readActions, action) {
			t.Fatalf("filesystem read actions missing %q: %q", action, readActions)
		}
	}
	for _, action := range []string{"write", "edit", "multiedit", "multi_edit"} {
		if containsPipeAction(readActions, action) {
			t.Fatalf("filesystem read actions must not include %q: %q", action, readActions)
		}
	}

	writeActions := strings.Join(MixedLocalWriteActions("filesystem"), "|")
	for _, action := range []string{"write", "edit", "multiedit"} {
		if !containsPipeAction(writeActions, action) {
			t.Fatalf("filesystem write actions missing %q: %q", action, writeActions)
		}
	}
	if containsPipeAction(writeActions, "multi_edit") {
		t.Fatalf("filesystem write actions must not include legacy multi_edit: %q", writeActions)
	}

	groups := HostToolPolicyGroups()
	fsGroup := strings.Join(groups["fs"], "|")
	if !strings.Contains(fsGroup, "filesystem") {
		t.Fatalf("fs group should include filesystem: %q", fsGroup)
	}
	if strings.Contains(fsGroup, "multi_edit") {
		t.Fatalf("fs group must not include legacy multi_edit default: %q", fsGroup)
	}
	readonly := strings.Join(HostToolPolicyProfiles()["readonly"], "|")
	if !strings.Contains(readonly, "filesystem") {
		t.Fatalf("readonly profile should include filesystem: %q", readonly)
	}
}

func containsPipeAction(actions, want string) bool {
	return strings.Contains("|"+actions+"|", "|"+want+"|")
}

func TestProfileHasSideEffectUsesRiskAndCapabilityMetadata(t *testing.T) {
	tests := []struct {
		name    string
		profile ToolProfile
		want    bool
	}{
		{name: "read only", profile: ToolProfile{Risk: RiskReadOnly, ReadOnly: true}, want: false},
		{name: "external write", profile: ToolProfile{Risk: RiskExternalWrite}, want: true},
		{name: "runtime exec", profile: ToolProfile{Risk: RiskRuntimeExec}, want: true},
		{name: "unknown", profile: ToolProfile{Risk: RiskUnknown}, want: true},
		{name: "capability", profile: ToolProfile{Capabilities: []Capability{CapabilityExternalSend}}, want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ProfileHasSideEffect(tt.profile); got != tt.want {
				t.Fatalf("ProfileHasSideEffect() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestMatchActionCapabilityRulesForTextKeepsIndependentBusinessWriteActions(t *testing.T) {
	got := MatchActionCapabilityRulesForText("创建一个飞书任务，并写入飞书表格")
	ids := map[string]bool{}
	for _, rule := range got {
		ids[rule.CapabilityID] = true
	}
	if !ids[ActionCapabilityExternalTaskCreate] || !ids[ActionCapabilityExternalTableWrite] {
		t.Fatalf("independent actions should both survive, got %+v", got)
	}
	if ids[ActionCapabilityExternalRecordCreate] || ids[ActionCapabilityExternalRecordUpdate] {
		t.Fatalf("unmentioned record actions must not match, got %+v", got)
	}
}

func TestMatchActionCapabilityRulesForTextPrefersSpecificBitableRecordOverSheetWrite(t *testing.T) {
	got := MatchActionCapabilityRulesForText("更新飞书多维表格记录")
	ids := map[string]bool{}
	for _, rule := range got {
		ids[rule.CapabilityID] = true
	}
	if !ids[ActionCapabilityExternalRecordUpdate] {
		t.Fatalf("specific bitable record update missing, got %+v", got)
	}
	if ids[ActionCapabilityExternalTableWrite] {
		t.Fatalf("generic sheet write must be suppressed by specific bitable record update, got %+v", got)
	}
}

func TestRouteDecisionUsesCapabilityGate(t *testing.T) {
	decision := BuildRouteDecision(IntentFrame{
		Kind:              IntentExternalWrite,
		AllowsSideEffects: true,
	}, []ToolProfile{
		{
			Name:       "bad_sender",
			Risk:       RiskExternalWrite,
			SideEffect: true,
		},
	})

	if decision.Mode != DecisionModeDiscover {
		t.Fatalf("Mode = %q, want discover", decision.Mode)
	}
	if len(decision.BlockedTools) != 1 || decision.BlockedTools[0].Reason != "capability missing" {
		t.Fatalf("BlockedTools = %+v, want capability missing", decision.BlockedTools)
	}
}

func TestCapabilityGateDoesNotMaskMissingSideEffectIntent(t *testing.T) {
	profile := ToolProfile{
		Name:       "custom_skill_writer",
		Kind:       CapabilityKindCustomTool,
		Domain:     "skill_authoring",
		Source:     CapabilitySourceCustomDir,
		Invocation: InvocationDirectTool,
		Risk:       RiskLocalWrite,
		Trust:      TrustLocal,
		SideEffect: true,
	}
	intent := IntentFrame{
		Kind:              IntentCreateSkill,
		AllowsSideEffects: false,
	}
	decision := BuildRouteDecision(intent, []ToolProfile{
		profile,
	})

	if decision.Mode != DecisionModeDiscover {
		t.Fatalf("Mode = %q, want discover", decision.Mode)
	}
	if len(decision.BlockedTools) != 1 || decision.BlockedTools[0].Reason != "side effect not allowed by intent" {
		t.Fatalf("BlockedTools = %+v, want side effect not allowed by intent", decision.BlockedTools)
	}
}
