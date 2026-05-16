package title

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/chef-guo/agents-hive/internal/errs"
	"github.com/chef-guo/agents-hive/internal/llm"
	"github.com/chef-guo/agents-hive/internal/subagent"
)

func testLogger() *zap.Logger {
	l, _ := zap.NewDevelopment()
	return l
}

// TestGenerateTitle_Empty 空消息处理
func TestGenerateTitle_Empty(t *testing.T) {
	// 这个测试不需要调用 LLM，可以直接测试
	agent := &Agent{}
	agent.BaseAgent = subagent.NewBaseAgent(
		subagent.AgentCard{ID: "test"},
		func(ctx context.Context, req subagent.TaskRequest) subagent.TaskResponse {
			return subagent.TaskResponse{}
		},
		nil,
		testLogger(),
	)

	ctx := context.Background()
	title, err := agent.GenerateTitle(ctx, []llm.MessageWithTools{})

	require.Error(t, err, "空消息应返回错误")
	assert.True(t, errs.IsCode(err, errs.CodeInvalidInput), "应返回 CodeInvalidInput 错误")
	assert.Empty(t, title, "标题应为空")
}

// TestGenerateSummary_Empty 空消息处理
func TestGenerateSummary_Empty(t *testing.T) {
	// 这个测试不需要调用 LLM
	agent := &Agent{}
	agent.BaseAgent = subagent.NewBaseAgent(
		subagent.AgentCard{ID: "test"},
		func(ctx context.Context, req subagent.TaskRequest) subagent.TaskResponse {
			return subagent.TaskResponse{}
		},
		nil,
		testLogger(),
	)

	ctx := context.Background()
	summary, err := agent.GenerateSummary(ctx, []llm.MessageWithTools{})

	require.Error(t, err, "空消息应返回错误")
	assert.True(t, errs.IsCode(err, errs.CodeInvalidInput), "应返回 CodeInvalidInput 错误")
	assert.Empty(t, summary, "摘要应为空")
}

// TestHandleTask_InvalidPayload 测试无效 Payload 处理
func TestHandleTask_InvalidPayload(t *testing.T) {
	agent := &Agent{}
	agent.BaseAgent = subagent.NewBaseAgent(
		subagent.AgentCard{ID: "test"},
		agent.handleTask,
		nil,
		testLogger(),
	)

	ctx := context.Background()
	resp := agent.handleTask(ctx, subagent.TaskRequest{
		ID:      "req-1",
		Type:    "title",
		Payload: []byte("invalid json"),
	})

	assert.Equal(t, "failed", resp.Status, "任务应失败")
	assert.Contains(t, resp.Error, "解析请求失败", "错误信息应包含解析失败")
}

// TestExtractTextContent_PlainText 测试纯文本内容提取
func TestExtractTextContent_PlainText(t *testing.T) {
	agent := &Agent{}

	msg := llm.MessageWithTools{
		Role:    "user",
		Content: llm.NewTextContent("纯文本消息"),
	}

	text := agent.extractTextContent(msg)
	assert.Equal(t, "纯文本消息", text, "应返回纯文本内容")
}

// TestExtractTextContent_Multimodal 测试多模态内容提取
func TestExtractTextContent_Multimodal(t *testing.T) {
	agent := &Agent{}

	// 创建多模态内容
	multiContent := llm.NewMultiContent(
		llm.ContentPart{Type: llm.ContentText, Text: "这是文本部分1"},
		llm.ContentPart{Type: llm.ContentImage, ImageURL: "http://example.com/image.jpg"},
		llm.ContentPart{Type: llm.ContentText, Text: "这是文本部分2"},
	)

	msg := llm.MessageWithTools{
		Role:    "user",
		Content: multiContent,
	}

	// 提取文本内容
	text := agent.extractTextContent(msg)

	// 验证只提取了文本部分
	assert.Contains(t, text, "这是文本部分1", "应包含第一个文本部分")
	assert.Contains(t, text, "这是文本部分2", "应包含第二个文本部分")
	assert.NotContains(t, text, "http://", "不应包含图片 URL")
}

// TestFormatMessages 测试消息格式化
func TestFormatMessages(t *testing.T) {
	agent := &Agent{}

	messages := []llm.MessageWithTools{
		{
			Role:    "user",
			Content: llm.NewTextContent("第一条消息"),
		},
		{
			Role:    "assistant",
			Content: llm.NewTextContent("第二条消息"),
		},
		{
			Role:    "user",
			Content: llm.NewTextContent("第三条消息"),
		},
	}

	formatted := agent.formatMessages(messages)

	// 验证格式
	assert.Contains(t, formatted, "[1] user: 第一条消息", "应包含第 1 条消息")
	assert.Contains(t, formatted, "[2] assistant: 第二条消息", "应包含第 2 条消息")
	assert.Contains(t, formatted, "[3] user: 第三条消息", "应包含第 3 条消息")
}

// TestFormatMessages_EmptyContent 测试空内容过滤
func TestFormatMessages_EmptyContent(t *testing.T) {
	agent := &Agent{}

	messages := []llm.MessageWithTools{
		{
			Role:    "user",
			Content: llm.NewTextContent("有内容的消息"),
		},
		{
			Role:    "assistant",
			Content: llm.NewTextContent(""),
		},
		{
			Role:    "user",
			Content: llm.NewTextContent("另一条消息"),
		},
	}

	formatted := agent.formatMessages(messages)

	// 验证空内容被跳过
	lines := strings.Split(strings.TrimSpace(formatted), "\n\n")
	// 应该只有 2 条非空消息（空内容被过滤）
	assert.Equal(t, 2, len(lines), "应只有 2 条非空消息")
	assert.Contains(t, formatted, "有内容的消息", "应包含第 1 条消息")
	assert.Contains(t, formatted, "另一条消息", "应包含第 3 条消息")
}

// TestConstants 测试常量定义
func TestConstants(t *testing.T) {
	assert.Equal(t, 50, maxTitleLength, "标题最大长度应为 50")
	assert.Equal(t, 0.3, lowTemperature, "低温度应为 0.3")
}

// TestTitleRequest_JSON 测试 TitleRequest JSON 序列化
func TestTitleRequest_JSON(t *testing.T) {
	req := TitleRequest{
		Messages: []llm.MessageWithTools{
			{
				Role:    "user",
				Content: llm.NewTextContent("测试"),
			},
		},
	}

	data, err := json.Marshal(req)
	require.NoError(t, err, "序列化不应失败")

	var decoded TitleRequest
	err = json.Unmarshal(data, &decoded)
	require.NoError(t, err, "反序列化不应失败")

	assert.Equal(t, 1, len(decoded.Messages), "应有 1 条消息")
}

// TestTitleResult_JSON 测试 TitleResult JSON 序列化
func TestTitleResult_JSON(t *testing.T) {
	result := TitleResult{
		Title:   "测试标题",
		Summary: "测试摘要",
	}

	data, err := json.Marshal(result)
	require.NoError(t, err, "序列化不应失败")

	var decoded TitleResult
	err = json.Unmarshal(data, &decoded)
	require.NoError(t, err, "反序列化不应失败")

	assert.Equal(t, "测试标题", decoded.Title, "标题应匹配")
	assert.Equal(t, "测试摘要", decoded.Summary, "摘要应匹配")
}

// =============================================================================
// 真实 LLM 集成测试
// 注意：这些测试需要有效的 LLM API 配置（OPENAI_API_KEY 环境变量）
// =============================================================================

// TestGenerateTitle_RealLLM 使用真实 LLM 测试标题生成
func TestGenerateTitle_RealLLM(t *testing.T) {
	// 创建真实的 LLM Client（使用环境变量配置）
	logger := testLogger()
	llmClient := llm.NewClient(llm.ClientConfig{
		Model:   getModel(t),   // 从环境变量获取模型
		APIKey:  getAPIKey(t),  // 从环境变量获取 API key
		BaseURL: getBaseURL(t), // 从环境变量获取 BaseURL
	}, logger)

	// 创建 Title Agent
	agent := New(llmClient, logger)

	// 准备测试消息
	messages := []llm.MessageWithTools{
		{
			Role:    "user",
			Content: llm.NewTextContent("请帮我写一个 Go 语言的 HTTP 服务器示例"),
		},
		{
			Role:    "assistant",
			Content: llm.NewTextContent("好的，我来帮你写一个简单的 HTTP 服务器..."),
		},
	}

	ctx := context.Background()
	title, err := agent.GenerateTitle(ctx, messages)

	require.NoError(t, err, "标题生成不应失败")
	assert.NotEmpty(t, title, "标题不应为空")
	assert.LessOrEqual(t, len(title), maxTitleLength, "标题长度不应超过限制")
	t.Logf("生成的标题: %s", title)
}

// TestGenerateSummary_RealLLM 使用真实 LLM 测试摘要生成
func TestGenerateSummary_RealLLM(t *testing.T) {
	logger := testLogger()
	llmClient := llm.NewClient(llm.ClientConfig{
		Model:   getModel(t),
		APIKey:  getAPIKey(t),
		BaseURL: getBaseURL(t),
	}, logger)

	agent := New(llmClient, logger)

	// 多轮对话
	messages := []llm.MessageWithTools{
		{
			Role:    "user",
			Content: llm.NewTextContent("Go 语言的并发模型是什么？"),
		},
		{
			Role:    "assistant",
			Content: llm.NewTextContent("Go 使用 goroutine 和 channel 实现并发..."),
		},
		{
			Role:    "user",
			Content: llm.NewTextContent("能给个具体例子吗？"),
		},
		{
			Role:    "assistant",
			Content: llm.NewTextContent("当然可以，这里是一个简单的生产者-消费者模式..."),
		},
	}

	ctx := context.Background()
	summary, err := agent.GenerateSummary(ctx, messages)

	require.NoError(t, err, "摘要生成不应失败")
	assert.NotEmpty(t, summary, "摘要不应为空")
	t.Logf("生成的摘要: %s", summary)
}

// TestHandleTask_Title_RealLLM 测试通过 TaskRequest 生成标题
func TestHandleTask_Title_RealLLM(t *testing.T) {
	logger := testLogger()
	llmClient := llm.NewClient(llm.ClientConfig{
		Model:   getModel(t),
		APIKey:  getAPIKey(t),
		BaseURL: getBaseURL(t),
	}, logger)

	agent := New(llmClient, logger)

	// 构造 TaskRequest
	req := TitleRequest{
		Messages: []llm.MessageWithTools{
			{
				Role:    "user",
				Content: llm.NewTextContent("测试消息"),
			},
		},
	}
	payload, _ := json.Marshal(req)

	ctx := context.Background()
	resp := agent.handleTask(ctx, subagent.TaskRequest{
		ID:      "req-1",
		Type:    "title",
		Payload: payload,
	})

	assert.Equal(t, "completed", resp.Status, "任务应成功完成")
	assert.Empty(t, resp.Error, "不应有错误")

	var result TitleResult
	err := json.Unmarshal(resp.Result, &result)
	require.NoError(t, err, "结果应能反序列化")
	assert.NotEmpty(t, result.Title, "标题不应为空")
	t.Logf("生成的标题: %s", result.Title)
}

// getAPIKey 从环境变量获取 API Key，如果没有则跳过测试
func getAPIKey(t *testing.T) string {
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		t.Skip("跳过真实 LLM 测试：需要设置 OPENAI_API_KEY 环境变量")
	}
	return apiKey
}

// getModel 从环境变量获取模型名称，默认使用 gpt-5-mini
func getModel(t *testing.T) string {
	model := os.Getenv("OPENAI_MODEL")
	if model == "" {
		return "gpt-5-mini" // 默认使用便宜的模型
	}
	return model
}

// getBaseURL 从环境变量获取 BaseURL，默认为空（使用 OpenAI 官方）
func getBaseURL(t *testing.T) string {
	return os.Getenv("OPENAI_BASE_URL")
}

// TestNew_WithCallbacks 验证构造函数正确设置 LLMComplete 回调
func TestNew_WithCallbacks(t *testing.T) {
	logger := testLogger()

	t.Run("no callbacks", func(t *testing.T) {
		agent := New(nil, logger)
		assert.Nil(t, agent.llmCompleteFn)
	})

	t.Run("with LLMCompleteFn", func(t *testing.T) {
		cb := subagent.AgentCallbacks{
			LLMCompleteFn: func(agentID, sessionID, userID, model string, usage llm.Usage) {},
		}
		agent := New(nil, logger, cb)
		assert.NotNil(t, agent.llmCompleteFn)
	})

	t.Run("with nil LLMCompleteFn", func(t *testing.T) {
		cb := subagent.AgentCallbacks{LLMCompleteFn: nil}
		agent := New(nil, logger, cb)
		assert.Nil(t, agent.llmCompleteFn)
	})
}

// TestHandleTask_SessionIDPassthrough 验证 handleTask 从 TaskRequest 提取 sessionID
func TestHandleTask_SessionIDPassthrough(t *testing.T) {
	agent := &Agent{}
	agent.BaseAgent = subagent.NewBaseAgent(
		subagent.AgentCard{ID: "test-title"},
		agent.handleTask, nil, testLogger(),
	)

	// 构造一个会失败的请求（空 payload），但 sessionID 应该已被提取
	agent.handleTask(context.Background(), subagent.TaskRequest{
		ID:        "req-1",
		SessionID: "sess-abc-123",
		Payload:   []byte(`{}`),
	})

	assert.Equal(t, "sess-abc-123", agent.sessionID, "sessionID should be extracted from TaskRequest")
}

// TestLLMCompleteCallback_RealLLM 验证回调被调用且参数正确（需要真实 LLM）
func TestLLMCompleteCallback_RealLLM(t *testing.T) {
	logger := testLogger()
	llmClient := llm.NewClient(llm.ClientConfig{
		Model:   getModel(t),
		APIKey:  getAPIKey(t),
		BaseURL: getBaseURL(t),
	}, logger)

	var captured struct {
		agentID   string
		sessionID string
		userID    string
		model     string
		usage     llm.Usage
	}
	cb := subagent.AgentCallbacks{
		LLMCompleteFn: func(agentID, sessionID, userID, model string, usage llm.Usage) {
			captured.agentID = agentID
			captured.sessionID = sessionID
			captured.userID = userID
			captured.model = model
			captured.usage = usage
		},
	}

	agent := New(llmClient, logger, cb)

	payload, _ := json.Marshal(TitleRequest{
		Messages: []llm.MessageWithTools{
			{Role: "user", Content: llm.NewTextContent("测试回调")},
		},
	})

	resp := agent.handleTask(context.Background(), subagent.TaskRequest{
		ID:        "req-cb",
		Type:      "title",
		SessionID: "sess-callback-test",
		Payload:   payload,
	})

	assert.Equal(t, "completed", resp.Status)
	assert.Equal(t, "title-agent", captured.agentID)
	assert.Equal(t, "sess-callback-test", captured.sessionID)
	assert.NotEmpty(t, captured.model)
	assert.Greater(t, captured.usage.PromptTokens+captured.usage.CompletionTokens, int64(0),
		"usage should have non-zero tokens")
}
