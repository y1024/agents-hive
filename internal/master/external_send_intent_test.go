package master

import (
	"encoding/json"
	"testing"

	"github.com/chef-guo/agents-hive/internal/llm"
	"github.com/chef-guo/agents-hive/internal/router"
)

func TestResolveTurnIntent_DetectsExplicitExternalSendRequest(t *testing.T) {
	intent := resolveTurnIntent(&SessionState{ID: "s1"}, "给郭松发一下今天的天气信息", router.IntentFrame{Kind: router.IntentAnswer})

	if !isStructuredExternalSendIntent(intent) {
		t.Fatalf("explicit send request should become external-send intent: %+v", intent)
	}
}

func TestResolveTurnIntent_ContinuationRequiresPendingExternalSend(t *testing.T) {
	session := &SessionState{ID: "s1"}
	withoutPending := resolveTurnIntent(session, "现在能不能发", router.IntentFrame{Kind: router.IntentAnswer})
	if isStructuredExternalSendIntent(withoutPending) {
		t.Fatalf("continuation without pending external-send must not expose send intent: %+v", withoutPending)
	}

	session.RememberPendingExternalSendIntent(router.IntentFrame{
		Kind:              router.IntentExternalWrite,
		RequiresExternal:  true,
		AllowsSideEffects: true,
		Subject:           "给郭松发天气",
	})
	withPending := resolveTurnIntent(session, "现在能不能发", router.IntentFrame{Kind: router.IntentAnswer})
	if !isStructuredExternalSendIntent(withPending) {
		t.Fatalf("continuation with pending external-send should inherit send intent: %+v", withPending)
	}
}

func TestResolveTurnIntent_DoesNotTreatBrainstormAsExternalSend(t *testing.T) {
	intent := resolveTurnIntent(&SessionState{ID: "s1"}, "给我发散一下思路", router.IntentFrame{Kind: router.IntentAnswer})
	if isStructuredExternalSendIntent(intent) {
		t.Fatalf("brainstorm wording must not become external-send intent: %+v", intent)
	}
}

func TestResolveTurnIntent_RecoversRecentUnfinishedExternalSendFromHistory(t *testing.T) {
	session := &SessionState{ID: "s1", Messages: []llm.MessageWithTools{
		{Role: "user", Content: llm.NewTextContent("给郭松发一下今天的天气信息")},
		{Role: "assistant", Content: llm.NewTextContent("我现在没法直接在飞书里发。")},
		{Role: "user", Content: llm.NewTextContent("现在能不能发")},
	}}

	intent := resolveTurnIntent(session, "现在能不能发", router.IntentFrame{Kind: router.IntentAnswer})
	if !isStructuredExternalSendIntent(intent) {
		t.Fatalf("recent unfinished send should be recovered as external-send: %+v", intent)
	}
}

func TestResolveTurnIntent_DoesNotRecoverAlreadySatisfiedExternalSend(t *testing.T) {
	session := &SessionState{ID: "s1", Messages: []llm.MessageWithTools{
		{Role: "user", Content: llm.NewTextContent("给郭松发一下今天的天气信息")},
		{Role: "assistant", ToolCalls: []llm.ToolCall{{
			ID:        "send-1",
			Name:      "feishu_api",
			Arguments: json.RawMessage(`{"action":"send_message","chat_id":"oc_1","content":"天气"}`),
		}}},
		{Role: "tool", ToolCallID: "send-1", ToolName: "feishu_api", Content: llm.NewTextContent(`{"ok":true}`)},
		{Role: "user", Content: llm.NewTextContent("现在能不能发")},
	}}

	intent := resolveTurnIntent(session, "现在能不能发", router.IntentFrame{Kind: router.IntentAnswer})
	if isStructuredExternalSendIntent(intent) {
		t.Fatalf("already satisfied send must not be recovered: %+v", intent)
	}
}

func TestResolveTurnIntent_RecoversExplicitSkillAndToolManagementIntents(t *testing.T) {
	cases := []struct {
		name string
		q    string
		want router.IntentKind
	}{
		{name: "create skill", q: "创建一个跟我打招呼的技能", want: router.IntentCreateSkill},
		{name: "modify skill", q: "修改 skill-creator 让它支持审核", want: router.IntentModifySkill},
		{name: "manage tool", q: "创建 MCP server 接入 GitHub API", want: router.IntentManageTool},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			intent := resolveTurnIntent(&SessionState{ID: "s1"}, tc.q, router.IntentFrame{Kind: router.IntentAnswer})
			if intent.Kind != tc.want || !intent.AllowsSideEffects {
				t.Fatalf("intent = %+v, want kind=%s with side effects", intent, tc.want)
			}
		})
	}
}

func TestResolveTurnIntent_DoesNotRecoverNegatedMCPManagement(t *testing.T) {
	intent := resolveTurnIntent(&SessionState{ID: "s1"}, "创建一个 skill，MCP 只是实现背景，不要创建 MCP server", router.IntentFrame{Kind: router.IntentAnswer})
	if intent.Kind == router.IntentManageTool {
		t.Fatalf("negated MCP wording must not become manage-tool intent: %+v", intent)
	}
}
