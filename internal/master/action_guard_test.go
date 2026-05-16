package master

import (
	"context"
	"encoding/json"
	"testing"

	"go.uber.org/zap"

	"github.com/chef-guo/agents-hive/internal/mcphost"
	"github.com/chef-guo/agents-hive/internal/router"
	"github.com/chef-guo/agents-hive/internal/security"
)

func TestActionGuardShellPolicy(t *testing.T) {
	guard := newDeterministicActionGuard()
	executor := security.NewSafeExecutor(nil, zap.NewNop())

	tests := []struct {
		name    string
		command string
		want    string
	}{
		{name: "rm root ask", command: "rm -rf /", want: ActionGuardAsk},
		{name: "force push ask", command: "git push --force origin main", want: ActionGuardAsk},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			decision := guard.Decide(context.Background(), ActionGuardInput{
				ToolName:     "bash",
				Arguments:    mustRawMessage(t, map[string]string{"command": tt.command}),
				SafeExecutor: executor,
			})
			if decision.Action != tt.want {
				t.Fatalf("decision = %q, want %q, full=%+v", decision.Action, tt.want, decision)
			}
		})
	}
}

func TestActionGuardReadFileAllow(t *testing.T) {
	guard := newDeterministicActionGuard()

	decision := guard.Decide(context.Background(), ActionGuardInput{
		ToolName:  "read_file",
		Arguments: json.RawMessage(`{"path":"README.md"}`),
	})
	if decision.Action != ActionGuardAllow {
		t.Fatalf("decision = %q, want %q, full=%+v", decision.Action, ActionGuardAllow, decision)
	}
}

func TestActionGuardToolSearchDiscoveryEntrypointAllow(t *testing.T) {
	guard := newDeterministicActionGuard()

	decision := guard.Decide(context.Background(), ActionGuardInput{
		ToolName:  "tool_search",
		Arguments: json.RawMessage(`{"query":"memory"}`),
	})
	if decision.Action != ActionGuardAllow {
		t.Fatalf("decision = %q, want %q, full=%+v", decision.Action, ActionGuardAllow, decision)
	}
	if decision.Policy.Reason != "discovery_entrypoint" {
		t.Fatalf("policy reason = %q, want discovery_entrypoint, full=%+v", decision.Policy.Reason, decision)
	}
}

func TestActionGuardQuestionEntrypointAllowUnderExternalSendIntent(t *testing.T) {
	guard := newDeterministicActionGuard()

	decision := guard.Decide(context.Background(), ActionGuardInput{
		ToolName:  "question",
		Arguments: json.RawMessage(`{"question":"请选择发送平台","options":["飞书","微信"]}`),
		Route: router.RouteDecision{
			Intent: router.IntentFrame{
				Kind:               router.IntentExternalWrite,
				RequiresExternal:   true,
				AllowsSideEffects:  true,
				AllowedDomainsHint: []string{"feishu", "wechatbot"},
			},
		},
	})
	if decision.Action != ActionGuardAllow {
		t.Fatalf("decision = %q, want %q, full=%+v", decision.Action, ActionGuardAllow, decision)
	}
	if decision.Policy.Reason != "question_entrypoint" || decision.Policy.RiskClass != router.ToolRiskReadOnly {
		t.Fatalf("question should be read-only entrypoint allow: %+v", decision)
	}
}

func TestActionGuardSkillListOnlyAllow(t *testing.T) {
	guard := newDeterministicActionGuard()

	decision := guard.Decide(context.Background(), ActionGuardInput{
		ToolName:  "skill",
		Arguments: json.RawMessage(`{}`),
	})
	if decision.Action != ActionGuardAllow {
		t.Fatalf("decision = %q, want %q, full=%+v", decision.Action, ActionGuardAllow, decision)
	}
	if decision.Policy.Reason != "skill_list" || decision.Policy.RiskClass != router.ToolRiskReadOnly {
		t.Fatalf("skill list should be read-only allow: %+v", decision)
	}
}

func TestActionGuardNamedSkillRequiresRouteIntent(t *testing.T) {
	guard := newDeterministicActionGuard()

	denied := guard.Decide(context.Background(), ActionGuardInput{
		ToolName:  "skill",
		Arguments: json.RawMessage(`{"name":"skill-creator"}`),
		Intent:    router.IntentFrame{Kind: router.IntentRead},
	})
	if denied.Action != ActionGuardAsk || denied.Policy.Reason != "skill_invocation_requires_route" {
		t.Fatalf("denied decision = %+v, want named skill route approval", denied)
	}

	allowed := guard.Decide(context.Background(), ActionGuardInput{
		ToolName:  "skill",
		Arguments: json.RawMessage(`{"name":"skill-creator"}`),
		Route: router.RouteDecision{
			Intent: router.IntentFrame{Kind: router.IntentCreateSkill},
		},
	})
	if allowed.Action != ActionGuardAllow || allowed.Policy.Reason != "skill_invocation_route_allowed" {
		t.Fatalf("allowed decision = %+v, want route-authorized named skill allow", allowed)
	}
}

func TestActionGuardPlanControlActionAllow(t *testing.T) {
	guard := newDeterministicActionGuard()

	decision := guard.Decide(context.Background(), ActionGuardInput{
		ToolName:  "todo_write",
		Arguments: json.RawMessage(`{"expected_plan_version":0,"todos":[]}`),
	})
	if decision.Action != ActionGuardAllow {
		t.Fatalf("decision = %q, want %q, full=%+v", decision.Action, ActionGuardAllow, decision)
	}
	if decision.Policy.Reason != "plan_control_action" || decision.Policy.RiskClass != router.ToolRiskRoutineSideEffect {
		t.Fatalf("todo_write should use routine plan control policy: %+v", decision)
	}
}

func TestActionGuardPlainTextIMSendAllow(t *testing.T) {
	guard := newDeterministicActionGuard()

	tests := []struct {
		name string
		tool string
		raw  string
	}{
		{name: "feishu send message", tool: "feishu_api", raw: `{"action":"send_message","content":"hi","receive_id":"oc_1"}`},
		{name: "send im message", tool: "send_im_message", raw: `{"platform":"feishu","content":"hi","recipient":"oc_1"}`},
		{name: "im api send", tool: "im_api", raw: `{"action":"send_message","platform":"feishu","content":"hi","conversation_id":"oc_1"}`},
		{name: "legacy chat id", tool: "send_im_message", raw: `{"platform":"feishu","content":"hi","chat_id":"oc_1"}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			decision := guard.Decide(context.Background(), ActionGuardInput{
				ToolName:  tt.tool,
				Arguments: json.RawMessage(tt.raw),
			})
			if decision.Action != ActionGuardAllow {
				t.Fatalf("decision = %q, want %q, full=%+v", decision.Action, ActionGuardAllow, decision)
			}
		})
	}
}

func TestActionGuardExternalSendMediaAndUploadAsk(t *testing.T) {
	guard := newDeterministicActionGuard()

	tests := []struct {
		name string
		tool string
		raw  string
	}{
		{name: "feishu send file", tool: "feishu_api", raw: `{"action":"send_file","file_key":"file"}`},
		{name: "feishu send image", tool: "feishu_api", raw: `{"action":"send_image","image_key":"img"}`},
		{name: "feishu upload file", tool: "feishu_api", raw: `{"action":"upload_file","data":"base64","filename":"a.txt"}`},
		{name: "feishu upload image", tool: "feishu_api", raw: `{"action":"upload_image","data":"base64"}`},
		{name: "send im with media key", tool: "send_im_message", raw: `{"platform":"feishu","recipient":"oc_1","content":"hi","file_key":"file"}`},
		{name: "feishu send message image type", tool: "feishu_api", raw: `{"action":"send_message","chat_id":"oc_1","content":"hi","msg_type":"image","image_key":"img"}`},
		{name: "im api bulk recipients", tool: "im_api", raw: `{"action":"send_message","platform":"feishu","recipients":["u1","u2"],"content":"hi"}`},
		{name: "send im mention all", tool: "send_im_message", raw: `{"platform":"feishu","conversation_id":"oc_1","content":"hi","mention_all":true}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			decision := guard.Decide(context.Background(), ActionGuardInput{
				ToolName:  tt.tool,
				Arguments: json.RawMessage(tt.raw),
			})
			if decision.Action != ActionGuardAsk {
				t.Fatalf("decision = %q, want %q, full=%+v", decision.Action, ActionGuardAsk, decision)
			}
			if decision.Policy.Source != "tool_policy" || !decision.Policy.RequiresApproval {
				t.Fatalf("decision should come from unified tool policy approval path, full=%+v", decision)
			}
		})
	}
}

func TestActionGuardFlagsDangerousActionArgument(t *testing.T) {
	guard := newDeterministicActionGuard()
	def := mcphost.ToolDefinition{
		Name:         "feishu_api",
		Description:  "飞书应用 API 工具",
		SourceServer: "metamcp",
		Trusted:      true,
	}

	decision := guard.Decide(context.Background(), ActionGuardInput{
		ToolName:  def.Name,
		Arguments: json.RawMessage(`{"nested":{"action":"complete_task"},"summary":"follow up"}`),
		ToolDef:   &def,
	})

	if decision.Action != ActionGuardAsk {
		t.Fatalf("decision = %q, want %q, full=%+v", decision.Action, ActionGuardAsk, decision)
	}
	if decision.Reason != "argument_side_effect" {
		t.Fatalf("reason = %q, want argument_side_effect, full=%+v", decision.Reason, decision)
	}
}

func TestActionGuardRepairsIMSendWithEmptyContentAsInvalidInput(t *testing.T) {
	guard := newDeterministicActionGuard()

	tests := []struct {
		name string
		tool string
		raw  string
	}{
		{name: "send im message", tool: "send_im_message", raw: `{"platform":"feishu","chat_id":"oc_1","content":" "}`},
		{name: "im api send", tool: "im_api", raw: `{"action":"send_message","receive_id":"oc_1","content":""}`},
		{name: "feishu send message", tool: "feishu_api", raw: `{"action":"send_message","chat_id":"oc_1"}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			decision := guard.Decide(context.Background(), ActionGuardInput{
				ToolName:  tt.tool,
				Arguments: json.RawMessage(tt.raw),
			})
			if decision.Action != ActionGuardRepair {
				t.Fatalf("decision = %q, want %q, full=%+v", decision.Action, ActionGuardRepair, decision)
			}
			if decision.Reason != "im_send_missing_content" {
				t.Fatalf("reason = %q, want im_send_missing_content, full=%+v", decision.Reason, decision)
			}
		})
	}
}

func TestActionGuardRepairsIMSendWithEmptyRecipientAsInvalidInput(t *testing.T) {
	guard := newDeterministicActionGuard()

	tests := []struct {
		name string
		tool string
		raw  string
	}{
		{name: "send im message", tool: "send_im_message", raw: `{"platform":"feishu","content":"hi","recipient":" "}`},
		{name: "im api send", tool: "im_api", raw: `{"action":"send_message","content":"hi"}`},
		{name: "feishu send message", tool: "feishu_api", raw: `{"action":"send_message","content":"hi","receive_id":""}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			decision := guard.Decide(context.Background(), ActionGuardInput{
				ToolName:  tt.tool,
				Arguments: json.RawMessage(tt.raw),
			})
			if decision.Action != ActionGuardRepair {
				t.Fatalf("decision = %q, want %q, full=%+v", decision.Action, ActionGuardRepair, decision)
			}
			if decision.Reason != "im_send_missing_recipient" {
				t.Fatalf("reason = %q, want im_send_missing_recipient, full=%+v", decision.Reason, decision)
			}
		})
	}
}

func TestActionGuardUnknownToolRepair(t *testing.T) {
	guard := newDeterministicActionGuard()

	decision := guard.Decide(context.Background(), ActionGuardInput{
		ToolName:  "unknown_tool",
		Arguments: json.RawMessage(`{"anything":true}`),
	})
	if decision.Action != ActionGuardRepair {
		t.Fatalf("decision = %q, want %q, full=%+v", decision.Action, ActionGuardRepair, decision)
	}
}

func TestActionGuardConcurrencySafeCustomToolAllow(t *testing.T) {
	guard := newDeterministicActionGuard()
	def := mcphost.ToolDefinition{
		Name:              "project_status",
		Description:       "查询项目状态",
		IsConcurrencySafe: true,
	}

	decision := guard.Decide(context.Background(), ActionGuardInput{
		ToolName:  def.Name,
		Arguments: json.RawMessage(`{"project":"agents-hive"}`),
		ToolDef:   &def,
	})
	if decision.Action != ActionGuardAllow {
		t.Fatalf("decision = %q, want %q, full=%+v", decision.Action, ActionGuardAllow, decision)
	}
}

func TestActionGuardTrustedRemoteReadOnlyAllow(t *testing.T) {
	guard := newDeterministicActionGuard()
	def := mcphost.ToolDefinition{
		Name:         "metamcp__query_prometheus",
		Description:  "Query Prometheus metrics",
		SourceServer: "metamcp",
		Trusted:      true,
	}

	decision := guard.Decide(context.Background(), ActionGuardInput{
		ToolName:  def.Name,
		Arguments: json.RawMessage(`{"query":"up"}`),
		ToolDef:   &def,
	})
	if decision.Action != ActionGuardAllow {
		t.Fatalf("decision = %q, want %q, full=%+v", decision.Action, ActionGuardAllow, decision)
	}
}

func TestActionGuardTrustedRemoteSideEffectAsk(t *testing.T) {
	guard := newDeterministicActionGuard()
	def := mcphost.ToolDefinition{
		Name:         "metamcp__create_annotation",
		Description:  "Create Grafana annotation",
		SourceServer: "metamcp",
		Trusted:      true,
	}

	decision := guard.Decide(context.Background(), ActionGuardInput{
		ToolName:  def.Name,
		Arguments: json.RawMessage(`{"text":"deploy started"}`),
		ToolDef:   &def,
	})
	if decision.Action != ActionGuardAsk {
		t.Fatalf("decision = %q, want %q, full=%+v", decision.Action, ActionGuardAsk, decision)
	}
}

func TestActionGuardUsesRouteDecisionIntentForRoutineLocalWrite(t *testing.T) {
	guard := newDeterministicActionGuard()

	decision := guard.Decide(context.Background(), ActionGuardInput{
		ToolName:  "memory",
		Arguments: json.RawMessage(`{"operation":"save","content":"note"}`),
		Route: router.RouteDecision{
			Intent: router.IntentFrame{Kind: router.IntentWriteLocal, AllowsSideEffects: true},
		},
	})

	if decision.Action != ActionGuardAllow {
		t.Fatalf("decision = %q, want %q, full=%+v", decision.Action, ActionGuardAllow, decision)
	}
	if decision.Policy.Reason != "routine_local_write_action" {
		t.Fatalf("policy reason = %q, want routine_local_write_action, full=%+v", decision.Policy.Reason, decision)
	}
}

func TestActionGuardTrustedRemoteSQLWriteAsk(t *testing.T) {
	guard := newDeterministicActionGuard()
	def := mcphost.ToolDefinition{
		Name:         "metamcp__dbhub__execute_sql",
		Description:  "Execute SQL against read-only database",
		SourceServer: "metamcp",
		Trusted:      true,
	}

	decision := guard.Decide(context.Background(), ActionGuardInput{
		ToolName:  def.Name,
		Arguments: json.RawMessage(`{"sql":"DROP TABLE users"}`),
		ToolDef:   &def,
	})
	if decision.Action != ActionGuardAsk {
		t.Fatalf("decision = %q, want %q, full=%+v", decision.Action, ActionGuardAsk, decision)
	}
}

func TestActionGuardTrustedRemoteReadSQLAllow(t *testing.T) {
	guard := newDeterministicActionGuard()
	def := mcphost.ToolDefinition{
		Name:         "metamcp__dbhub__execute_sql",
		Description:  "Execute SQL against read-only database",
		SourceServer: "metamcp",
		Trusted:      true,
	}

	decision := guard.Decide(context.Background(), ActionGuardInput{
		ToolName:  def.Name,
		Arguments: json.RawMessage(`{"sql":"SELECT count(*) FROM users"}`),
		ToolDef:   &def,
	})
	if decision.Action != ActionGuardAllow {
		t.Fatalf("decision = %q, want %q, full=%+v", decision.Action, ActionGuardAllow, decision)
	}
}

func TestActionGuardTrustedRemoteArgumentScannerIgnoresNonActionSubstrings(t *testing.T) {
	guard := newDeterministicActionGuard()
	def := mcphost.ToolDefinition{
		Name:         "metamcp__query_postgres_rows",
		Description:  "Query Postgres rows",
		SourceServer: "metamcp",
		Trusted:      true,
	}

	decision := guard.Decide(context.Background(), ActionGuardInput{
		ToolName:  def.Name,
		Arguments: json.RawMessage(`{"database":"postgres","column":"updated_at","query_name":"daily_user_count"}`),
		ToolDef:   &def,
	})
	if decision.Action != ActionGuardAllow {
		t.Fatalf("decision = %q, want %q, full=%+v", decision.Action, ActionGuardAllow, decision)
	}
}

func TestActionGuardTrustedRemoteActionFieldAsksForDangerousOperation(t *testing.T) {
	guard := newDeterministicActionGuard()
	def := mcphost.ToolDefinition{
		Name:         "metamcp__query_prometheus",
		Description:  "Query Prometheus metrics",
		SourceServer: "metamcp",
		Trusted:      true,
	}

	decision := guard.Decide(context.Background(), ActionGuardInput{
		ToolName:  def.Name,
		Arguments: json.RawMessage(`{"action":"delete","target":"dashboard"}`),
		ToolDef:   &def,
	})
	if decision.Action != ActionGuardAsk {
		t.Fatalf("decision = %q, want %q, full=%+v", decision.Action, ActionGuardAsk, decision)
	}
}

func TestActionGuardTrustedRemoteDestructiveAsk(t *testing.T) {
	guard := newDeterministicActionGuard()
	def := mcphost.ToolDefinition{
		Name:         "metamcp__delete_dashboard",
		Description:  "Delete Grafana dashboard",
		SourceServer: "metamcp",
		Trusted:      true,
	}

	decision := guard.Decide(context.Background(), ActionGuardInput{
		ToolName:  def.Name,
		Arguments: json.RawMessage(`{"uid":"abc"}`),
		ToolDef:   &def,
	})
	if decision.Action != ActionGuardAsk {
		t.Fatalf("decision = %q, want %q, full=%+v", decision.Action, ActionGuardAsk, decision)
	}
}

func TestActionGuardMemoryDeleteAsk(t *testing.T) {
	guard := newDeterministicActionGuard()

	decision := guard.Decide(context.Background(), ActionGuardInput{
		ToolName:  "memory",
		Arguments: json.RawMessage(`{"operation":"delete","id":1}`),
	})
	if decision.Action != ActionGuardAsk {
		t.Fatalf("decision = %q, want %q, full=%+v", decision.Action, ActionGuardAsk, decision)
	}
}

func TestActionGuardUnifiedPolicyForAllToolSources(t *testing.T) {
	guard := newDeterministicActionGuard()
	tests := []struct {
		name string
		def  mcphost.ToolDefinition
		raw  json.RawMessage
		want string
	}{
		{
			name: "safe custom allow",
			def:  mcphost.ToolDefinition{Name: "project_status", Description: "查询项目状态", IsConcurrencySafe: true},
			raw:  json.RawMessage(`{"project":"agents-hive"}`),
			want: ActionGuardAllow,
		},
		{
			name: "unknown custom ask",
			def:  mcphost.ToolDefinition{Name: "opaque_candidate", Description: "opaque extension"},
			raw:  json.RawMessage(`{"x":true}`),
			want: ActionGuardAsk,
		},
		{
			name: "trusted remote read allow",
			def:  mcphost.ToolDefinition{Name: "metamcp__query_prometheus", Description: "Query Prometheus metrics", SourceServer: "metamcp", Trusted: true},
			raw:  json.RawMessage(`{"query":"up"}`),
			want: ActionGuardAllow,
		},
		{
			name: "trusted remote write ask",
			def:  mcphost.ToolDefinition{Name: "metamcp__create_annotation", Description: "Create Grafana annotation", SourceServer: "metamcp", Trusted: true},
			raw:  json.RawMessage(`{"text":"deploy started"}`),
			want: ActionGuardAsk,
		},
		{
			name: "destructive profile asks even with dangerous args",
			def:  mcphost.ToolDefinition{Name: "metamcp__delete_dashboard", Description: "Delete Grafana dashboard", SourceServer: "metamcp", Trusted: true},
			raw:  json.RawMessage(`{"action":"delete","uid":"abc"}`),
			want: ActionGuardAsk,
		},
		{
			name: "feishu upload asks through unified mixed policy",
			def:  mcphost.ToolDefinition{Name: "feishu_api", Core: true, Description: "飞书应用 API 工具"},
			raw:  json.RawMessage(`{"action":"upload_file","data":"base64","filename":"a.txt"}`),
			want: ActionGuardAsk,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			decision := guard.Decide(context.Background(), ActionGuardInput{
				ToolName:  tt.def.Name,
				Arguments: tt.raw,
				ToolDef:   &tt.def,
			})
			if decision.Action != tt.want {
				t.Fatalf("decision = %q, want %q, full=%+v", decision.Action, tt.want, decision)
			}
		})
	}
}

func mustRawMessage(t *testing.T, v any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}
	return raw
}
