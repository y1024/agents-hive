package master

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/chef-guo/agents-hive/internal/llm"
	"github.com/chef-guo/agents-hive/internal/mcphost"
	"github.com/chef-guo/agents-hive/internal/tools"
)

var defaultModelVisibleTools = map[string]bool{
	"batch":             true,
	"ls":                true,
	"memory":            true,
	"parallel_dispatch": true,
	"question":          true,
	"skill":             true,
	"task":              true,
	"tool_search":       true,
}

const perTurnToolRecallLimit = 5

// modelVisibleToolsForSession 收窄模型默认候选集：核心工具和质量杠杆工具默认可见，
// 其他扩展/MCP/自定义工具需要先通过 tool_search 发现。
func modelVisibleToolsForSession(session *SessionState, catalog []mcphost.ToolDefinition) []mcphost.ToolDefinition {
	return modelVisibleToolsForSessionWithRecall(session, catalog, "")
}

// modelVisibleToolsForSessionWithRecall 在默认可见集基础上，把当前用户消息召回到的少量隐藏工具
// 临时加入本轮模型候选。召回结果不写入 session discovered state，显式 tool_search 成功后才持久可见。
func modelVisibleToolsForSessionWithRecall(session *SessionState, catalog []mcphost.ToolDefinition, latestUserQuery string) []mcphost.ToolDefinition {
	if len(catalog) == 0 {
		return nil
	}
	recallSet := perTurnRecalledToolSet(catalog, latestUserQuery, perTurnToolRecallLimit)
	out := make([]mcphost.ToolDefinition, 0, len(catalog))
	for _, tool := range catalog {
		if decision := EvaluatePlanToolGate(context.Background(), session, tool.Name); !decision.Allowed {
			continue
		}
		if tool.Core || defaultModelVisibleTools[tool.Name] || (session != nil && session.IsToolDiscovered(tool.Name)) || recallSet[tool.Name] {
			out = append(out, tool)
		}
	}
	return out
}

func modelVisibleToolsForPreparedMessages(session *SessionState, catalog []mcphost.ToolDefinition, messages []llm.MessageWithTools) []mcphost.ToolDefinition {
	return modelVisibleToolsForSessionWithRecall(session, catalog, extractLatestUserQuery(messages))
}

func perTurnRecalledToolSet(catalog []mcphost.ToolDefinition, latestUserQuery string, limit int) map[string]bool {
	if strings.TrimSpace(latestUserQuery) == "" || len(catalog) == 0 || limit <= 0 {
		return nil
	}
	recalls := tools.RecallToolCatalog(catalog, latestUserQuery, limit)
	if len(recalls) == 0 {
		return nil
	}
	out := make(map[string]bool, len(recalls))
	for _, recall := range recalls {
		name := strings.TrimSpace(recall.Tool.Name)
		if name == "" {
			continue
		}
		out[name] = true
	}
	return out
}

func recordToolDiscoveryFromResult(session *SessionState, toolCall llm.ToolCall, content string, isError bool) {
	if session == nil || isError || toolCall.Name != "tool_search" {
		return
	}
	session.RecordDiscoveredTools(discoveredToolNamesFromToolSearchResult(content))
}

func discoveredToolNamesFromToolSearchResult(content string) []string {
	content = strings.TrimSpace(content)
	if content == "" {
		return nil
	}
	var payload struct {
		Results []struct {
			Name string `json:"name"`
		} `json:"results"`
	}
	if err := json.Unmarshal([]byte(content), &payload); err != nil {
		return nil
	}
	names := make([]string, 0, len(payload.Results))
	seen := make(map[string]bool, len(payload.Results))
	for _, result := range payload.Results {
		name := strings.TrimSpace(result.Name)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		names = append(names, name)
	}
	return names
}
