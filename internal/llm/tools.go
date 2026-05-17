package llm

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/packages/param"
	"github.com/openai/openai-go/responses"

	"github.com/chef-guo/agents-hive/internal/errs"
	"github.com/chef-guo/agents-hive/internal/mcphost"
)

const maxLLMToolNameLength = 64

var llmToolNamePattern = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

type toolNameAliases struct {
	toAPI      map[string]string
	toInternal map[string]string
}

func toolNameAliasesForTools(tools []mcphost.ToolDefinition) toolNameAliases {
	aliases := toolNameAliases{
		toAPI:      make(map[string]string, len(tools)),
		toInternal: make(map[string]string, len(tools)),
	}
	used := make(map[string]string, len(tools))
	for _, tool := range stableToolDefinitions(tools) {
		original := tool.Name
		alias := preferredLLMToolAlias(original)
		if prior, ok := used[alias]; ok && prior != original {
			alias = disambiguateLLMToolAlias(alias, original, used)
		}
		used[alias] = original
		aliases.toAPI[original] = alias
		aliases.toInternal[alias] = original
	}
	return aliases
}

func (a toolNameAliases) APIName(name string) string {
	if a.toAPI != nil {
		if alias, ok := a.toAPI[name]; ok {
			return alias
		}
	}
	return preferredLLMToolAlias(name)
}

func (a toolNameAliases) InternalName(name string) string {
	if a.toInternal != nil {
		if original, ok := a.toInternal[name]; ok {
			return original
		}
	}
	return name
}

func isValidLLMToolName(name string) bool {
	return len(name) > 0 && len(name) <= maxLLMToolNameLength && llmToolNamePattern.MatchString(name)
}

func preferredLLMToolAlias(name string) string {
	if isValidLLMToolName(name) {
		return name
	}

	var b strings.Builder
	for _, r := range strings.TrimSpace(name) {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '_' || r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	alias := strings.Trim(b.String(), "_-")
	if alias == "" {
		alias = "tool"
	}
	return shortenLLMToolAlias(alias, name)
}

func disambiguateLLMToolAlias(base, original string, used map[string]string) string {
	hash := hashToolName(original)
	for i := 0; ; i++ {
		suffix := "_" + hash
		if i > 0 {
			suffix = fmt.Sprintf("_%s_%d", hash[:10], i)
		}
		alias := joinLLMToolAlias(base, suffix)
		if prior, ok := used[alias]; !ok || prior == original {
			return alias
		}
	}
}

func shortenLLMToolAlias(alias, original string) string {
	if len(alias) <= maxLLMToolNameLength {
		return alias
	}
	return joinLLMToolAlias(alias, "_"+hashToolName(original))
}

func joinLLMToolAlias(prefix, suffix string) string {
	maxPrefixLen := maxLLMToolNameLength - len(suffix)
	if maxPrefixLen < 1 {
		maxPrefixLen = 1
	}
	if len(prefix) > maxPrefixLen {
		prefix = prefix[:maxPrefixLen]
	}
	prefix = strings.Trim(prefix, "_-")
	if prefix == "" {
		prefix = "tool"
		if len(prefix) > maxPrefixLen {
			prefix = prefix[:maxPrefixLen]
		}
	}
	return prefix + suffix
}

func hashToolName(name string) string {
	sum := sha1.Sum([]byte(name))
	return hex.EncodeToString(sum[:])[:12]
}

func stableToolDefinitions(tools []mcphost.ToolDefinition) []mcphost.ToolDefinition {
	if len(tools) <= 1 {
		return tools
	}
	out := append([]mcphost.ToolDefinition(nil), tools...)
	sort.SliceStable(out, func(i, j int) bool {
		left := strings.TrimSpace(out[i].Name)
		right := strings.TrimSpace(out[j].Name)
		if left == right {
			return out[i].Description < out[j].Description
		}
		return left < right
	})
	return out
}

func convertToolsForChatCompletions(tools []mcphost.ToolDefinition) ([]openai.ChatCompletionToolParam, error) {
	result := make([]openai.ChatCompletionToolParam, 0, len(tools))
	aliases := toolNameAliasesForTools(tools)
	for _, tool := range stableToolDefinitions(tools) {
		var inputSchema map[string]interface{}
		if err := json.Unmarshal(tool.InputSchema, &inputSchema); err != nil {
			return nil, errs.Wrap(errs.CodePlanGenFailed, fmt.Sprintf("解析工具输入 schema 失败 %s", tool.Name), err)
		}
		apiName := aliases.APIName(tool.Name)
		if !isValidLLMToolName(apiName) {
			return nil, errs.New(errs.CodePlanGenFailed, fmt.Sprintf("工具名无法转换为合法 LLM function name: %s", tool.Name))
		}

		result = append(result, openai.ChatCompletionToolParam{
			Function: openai.FunctionDefinitionParam{
				Name:        apiName,
				Description: openai.String(tool.Description),
				Parameters:  openai.FunctionParameters(inputSchema),
			},
		})
	}
	return result, nil
}

// convertToolsForResponses 将 mcphost.ToolDefinition 列表转换为 Responses API 工具格式。
func convertToolsForResponses(tools []mcphost.ToolDefinition) ([]responses.ToolUnionParam, error) {
	result := make([]responses.ToolUnionParam, 0, len(tools))
	aliases := toolNameAliasesForTools(tools)
	for _, tool := range stableToolDefinitions(tools) {
		var params map[string]any
		if tool.InputSchema != nil {
			if err := json.Unmarshal(tool.InputSchema, &params); err != nil {
				return nil, errs.Wrap(errs.CodePlanGenFailed, fmt.Sprintf("解析工具输入 schema 失败 %s", tool.Name), err)
			}
		}
		apiName := aliases.APIName(tool.Name)
		if !isValidLLMToolName(apiName) {
			return nil, errs.New(errs.CodePlanGenFailed, fmt.Sprintf("工具名无法转换为合法 LLM function name: %s", tool.Name))
		}

		ft := &responses.FunctionToolParam{
			Name:       apiName,
			Parameters: params,
		}
		if tool.Description != "" {
			ft.Description = param.NewOpt(tool.Description)
		}

		result = append(result, responses.ToolUnionParam{
			OfFunction: ft,
		})
	}
	return result, nil
}
