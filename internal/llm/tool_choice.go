package llm

import (
	"github.com/openai/openai-go"
	"github.com/openai/openai-go/packages/param"
	"github.com/openai/openai-go/responses"
)

// buildChatCompletionsToolChoice 把业务层的 ToolChoice 字符串转成
// openai-go Chat Completions API 所需的 union 参数。
//
// 合法输入：
//   - "" → 返回 (zero, false)，由调用方跳过 ToolChoice 设置（等价于 auto 默认行为）
//   - "auto" / "required" / "none" → 模式枚举
//   - 其他非空字符串 → 视为具体工具名，强制走 named tool 分支
//
// 见 docs/计划与路线/Agent-质量护栏治理计划.md P0-A。
func buildChatCompletionsToolChoice(choice string) (openai.ChatCompletionToolChoiceOptionUnionParam, bool) {
	return buildChatCompletionsToolChoiceWithAliases(choice, toolNameAliases{})
}

func buildChatCompletionsToolChoiceWithAliases(choice string, aliases toolNameAliases) (openai.ChatCompletionToolChoiceOptionUnionParam, bool) {
	if choice == "" {
		return openai.ChatCompletionToolChoiceOptionUnionParam{}, false
	}
	switch choice {
	case "auto", "required", "none":
		return openai.ChatCompletionToolChoiceOptionUnionParam{
			OfAuto: param.NewOpt(choice),
		}, true
	default:
		return openai.ChatCompletionToolChoiceOptionUnionParam{
			OfChatCompletionNamedToolChoice: &openai.ChatCompletionNamedToolChoiceParam{
				Function: openai.ChatCompletionNamedToolChoiceFunctionParam{
					Name: aliases.APIName(choice),
				},
			},
		}, true
	}
}

// buildResponsesToolChoice 把业务层的 ToolChoice 字符串转成
// openai-go Responses API 所需的 union 参数。
//
// 对于 "auto"/"required"/"none"：走 mode 分支。
// 对于具体工具名：走 function tool 分支。
// 空字符串返回 (zero, false)，由调用方跳过设置。
func buildResponsesToolChoice(choice string) (responses.ResponseNewParamsToolChoiceUnion, bool) {
	return buildResponsesToolChoiceWithAliases(choice, toolNameAliases{})
}

func buildResponsesToolChoiceWithAliases(choice string, aliases toolNameAliases) (responses.ResponseNewParamsToolChoiceUnion, bool) {
	if choice == "" {
		return responses.ResponseNewParamsToolChoiceUnion{}, false
	}
	switch choice {
	case "auto":
		return responses.ResponseNewParamsToolChoiceUnion{
			OfToolChoiceMode: param.NewOpt(responses.ToolChoiceOptionsAuto),
		}, true
	case "required":
		return responses.ResponseNewParamsToolChoiceUnion{
			OfToolChoiceMode: param.NewOpt(responses.ToolChoiceOptionsRequired),
		}, true
	case "none":
		return responses.ResponseNewParamsToolChoiceUnion{
			OfToolChoiceMode: param.NewOpt(responses.ToolChoiceOptionsNone),
		}, true
	default:
		return responses.ResponseNewParamsToolChoiceUnion{
			OfFunctionTool: &responses.ToolChoiceFunctionParam{
				Name: aliases.APIName(choice),
			},
		}, true
	}
}
