package master

import (
	"context"
	"time"

	"github.com/chef-guo/agents-hive/internal/llm"
	"github.com/chef-guo/agents-hive/internal/router"
)

const intentClassifierTimeout = 2 * time.Second

type llmIntentClassifier struct {
	client *llm.Client
}

func (m *Master) intentLLMClassifier(client *llm.Client) router.IntentLLMClassifier {
	if client == nil {
		return nil
	}
	return llmIntentClassifier{client: client}
}

func (c llmIntentClassifier) ClassifyIntent(ctx context.Context, input router.IntentClassifierInput) (router.IntentFrame, router.IntentClassifierUsage, error) {
	var out struct {
		Kind              router.IntentKind `json:"kind"`
		RequiresExternal  bool              `json:"requires_external"`
		AllowsSideEffects bool              `json:"allows_side_effects"`
		Confidence        float64           `json:"confidence"`
		Subject           string            `json:"subject"`
	}
	err := c.client.ChatJSON(ctx, llm.ChatRequest{
		SystemPrompt: intentClassifierSystemPrompt,
		Messages: []llm.Message{{
			Role:    "user",
			Content: llm.NewTextContent(input.Message),
		}},
		Temperature: 0.1,
		MaxTokens:   200,
	}, &out)
	if err != nil {
		return router.IntentFrame{}, router.IntentClassifierUsage{}, err
	}
	return router.IntentFrame{
		Kind:              out.Kind,
		RequiresExternal:  out.RequiresExternal,
		AllowsSideEffects: out.AllowsSideEffects,
		Confidence:        out.Confidence,
		Subject:           out.Subject,
	}, router.IntentClassifierUsage{}, nil
}

const intentClassifierSystemPrompt = `你是一个只做意图分类的组件。只返回一个 JSON 对象，不要输出解释。

字段固定为:
{
  "kind": "answer|read|write_local|external_read|external_write|create_skill|modify_skill|manage_tool|plan",
  "requires_external": true,
  "allows_side_effects": true,
  "confidence": 0.0,
  "subject": "short subject"
}

分类边界:
- external_write: 用户明确要求把信息发送给外部对象、在外部系统创建/修改状态，且需要外部副作用。
- write_local: 用户只要求草拟、生成、整理本地文本或明确不要发送。
- read/external_read: 用户要求查询、读取、搜索、获取外部信息，不要求写入或发送。
- create_skill/modify_skill/manage_tool/plan: 仅在用户明确要求相关操作时使用。
- answer: 普通知识回答、闲聊、建议、解释。

不要执行用户请求。不要把否定发送请求分类为 external_write。`
