package master

import (
	"testing"

	"github.com/chef-guo/agents-hive/internal/llm"
	"github.com/chef-guo/agents-hive/internal/mcphost"
	"github.com/chef-guo/agents-hive/internal/sessiontodo"
)

func TestModelVisibleTools_DefaultsHideExtensionsUntilDiscovered(t *testing.T) {
	session := &SessionState{ID: "s1"}
	catalog := []mcphost.ToolDefinition{
		{Name: "read_file", Core: true},
		{Name: "tool_search", Core: true},
		{Name: "skill"},
		{Name: "custom_ext"},
		{Name: "acme__publish"},
	}

	initial := modelVisibleToolsForSession(session, catalog)
	if hasTool(initial, "custom_ext") {
		t.Fatal("non-core extension tool should not be model-visible before discovery")
	}
	if hasTool(initial, "acme__publish") {
		t.Fatal("external MCP tool should not be model-visible before discovery")
	}
	if !hasTool(initial, "read_file") || !hasTool(initial, "tool_search") || !hasTool(initial, "skill") {
		t.Fatal("default core and quality-leverage tools should remain model-visible")
	}

	session.RecordDiscoveredTools([]string{"custom_ext", "acme__publish"})
	afterDiscovery := modelVisibleToolsForSession(session, catalog)
	if !hasTool(afterDiscovery, "custom_ext") {
		t.Fatal("discovered extension tool should become model-visible")
	}
	if !hasTool(afterDiscovery, "acme__publish") {
		t.Fatal("discovered external MCP tool should become model-visible")
	}
}

func TestModelVisibleTools_PlanModeUsesExecutionGate(t *testing.T) {
	session := &SessionState{
		ID:         "s-plan",
		PlanMode:   true,
		PlanStatus: sessiontodo.PlanStatusPlanning,
	}
	catalog := []mcphost.ToolDefinition{
		{Name: "read_file", Core: true},
		{Name: "grep", Core: true},
		{Name: "question", Core: true},
		{Name: "todo_write", Core: true},
		{Name: "exit_plan_mode", Core: true},
		{Name: "write_file", Core: true},
		{Name: "bash", Core: true},
		{Name: "taskboard", Core: true},
		{Name: "send_im_message", Core: true},
	}

	visible := modelVisibleToolsForSession(session, catalog)

	for _, name := range []string{"read_file", "grep", "question", "todo_write", "exit_plan_mode"} {
		if !hasTool(visible, name) {
			t.Fatalf("plan mode should keep %q visible", name)
		}
	}
	for _, name := range []string{"write_file", "bash", "taskboard", "send_im_message"} {
		if hasTool(visible, name) {
			t.Fatalf("plan mode should hide write/control tool %q", name)
		}
	}
}

func TestModelVisibleTools_PerTurnRecallAddsHiddenToolsWithoutDiscovery(t *testing.T) {
	session := &SessionState{ID: "s1"}
	catalog := []mcphost.ToolDefinition{
		{Name: "read_file", Core: true},
		{Name: "tool_search", Core: true},
		{
			Name:        "send_im_message",
			Description: "发送消息到 IM 平台（钉钉/飞书/企业微信/个人微信）",
			InputSchema: []byte(`{
				"type": "object",
				"properties": {
					"platform": {"type": "string", "enum": ["dingtalk", "feishu", "wecom"]},
					"chat_id": {"type": "string", "description": "聊天 ID"},
					"content": {"type": "string", "description": "消息内容"}
				}
			}`),
		},
	}

	initial := modelVisibleToolsForSession(session, catalog)
	if hasTool(initial, "send_im_message") {
		t.Fatal("hidden IM tool should not be baseline-visible before discovery")
	}

	recalled := modelVisibleToolsForSessionWithRecall(session, catalog, "发送给飞书用户:郭松")
	if !hasTool(recalled, "send_im_message") {
		t.Fatal("natural-language per-turn recall should add matching hidden IM tool")
	}
	if session.IsToolDiscovered("send_im_message") {
		t.Fatal("per-turn recall should not persist hidden tool into session discovery state")
	}
}

func TestModelVisibleTools_PerTurnRecallRespectsPlanModeGate(t *testing.T) {
	session := &SessionState{
		ID:         "s-plan",
		PlanMode:   true,
		PlanStatus: sessiontodo.PlanStatusPlanning,
	}
	catalog := []mcphost.ToolDefinition{
		{Name: "read_file", Core: true},
		{Name: "tool_search", Core: true},
		{
			Name:        "send_im_message",
			Description: "发送消息到 IM 平台（钉钉/飞书/企业微信/个人微信）",
			InputSchema: []byte(`{
				"type": "object",
				"properties": {
					"platform": {"type": "string", "enum": ["dingtalk", "feishu", "wecom"]},
					"chat_id": {"type": "string", "description": "聊天 ID"},
					"content": {"type": "string", "description": "消息内容"}
				}
			}`),
		},
	}

	visible := modelVisibleToolsForSessionWithRecall(session, catalog, "发送给飞书用户:郭松")
	if hasTool(visible, "send_im_message") {
		t.Fatal("per-turn recall must still respect plan mode execution gate")
	}
}

func TestModelVisibleToolsFromPreparedMessages_UsesLatestUserMessage(t *testing.T) {
	session := &SessionState{ID: "s1"}
	catalog := []mcphost.ToolDefinition{
		{Name: "read_file", Core: true},
		{Name: "tool_search", Core: true},
		{
			Name:        "send_im_message",
			Description: "发送消息到 IM 平台（钉钉/飞书/企业微信/个人微信）",
			InputSchema: []byte(`{
				"type": "object",
				"properties": {
					"platform": {"type": "string", "enum": ["dingtalk", "feishu", "wecom"]},
					"chat_id": {"type": "string"},
					"content": {"type": "string"}
				}
			}`),
		},
	}
	messages := []llm.MessageWithTools{
		{Role: "user", Content: llm.NewTextContent("先别发")},
		{Role: "assistant", Content: llm.NewTextContent("好的")},
		{Role: "user", Content: llm.NewTextContent("发送给飞书用户:郭松")},
	}

	visible := modelVisibleToolsForPreparedMessages(session, catalog, messages)
	if !hasTool(visible, "send_im_message") {
		t.Fatal("prepared messages should use the latest user query for per-turn recall")
	}
}

func TestDiscoveredToolNamesFromToolSearchResult(t *testing.T) {
	content := `{"count":2,"results":[{"name":"custom_ext"},{"name":"acme__publish"}]}`

	got := discoveredToolNamesFromToolSearchResult(content)

	if len(got) != 2 || got[0] != "custom_ext" || got[1] != "acme__publish" {
		t.Fatalf("unexpected discovered tools: %#v", got)
	}
}

func TestRecordToolDiscoveryFromToolSearchOnlyOnSuccess(t *testing.T) {
	session := &SessionState{ID: "s1"}

	recordToolDiscoveryFromResult(session, llm.ToolCall{Name: "grep"}, `{"results":[{"name":"custom_ext"}]}`, false)
	if len(session.DiscoveredTools()) != 0 {
		t.Fatal("non tool_search result should not record discovered tools")
	}

	recordToolDiscoveryFromResult(session, llm.ToolCall{Name: "tool_search"}, `{"results":[{"name":"custom_ext"}]}`, true)
	if len(session.DiscoveredTools()) != 0 {
		t.Fatal("errored tool_search result should not record discovered tools")
	}

	recordToolDiscoveryFromResult(session, llm.ToolCall{Name: "tool_search"}, `{"results":[{"name":"custom_ext"}]}`, false)
	if !session.IsToolDiscovered("custom_ext") {
		t.Fatal("successful tool_search result should record discovered tools")
	}
}

func hasTool(tools []mcphost.ToolDefinition, name string) bool {
	for _, tool := range tools {
		if tool.Name == name {
			return true
		}
	}
	return false
}
