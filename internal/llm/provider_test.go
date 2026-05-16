package llm

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestLookupProvider_Known(t *testing.T) {
	tests := []struct {
		name     string
		expected string
		baseURL  string
		model    string
	}{
		{"openai", "openai", "https://www.gmini.xyz", "gpt-5.2"},
		{"deepseek", "deepseek", "https://api.deepseek.com", "deepseek-chat"},
		{"anthropic", "anthropic", "https://api.anthropic.com", "claude-sonnet-4-20250514"},
		{"google", "google", "https://generativelanguage.googleapis.com", "gemini-1.5-pro-latest"},
		{"azure", "azure", "", "gpt-5"},
		{"groq", "groq", "https://api.groq.com/openai", "llama3-70b-8192"},
		{"mistral", "mistral", "https://api.mistral.ai", "mistral-large-latest"},
		{"bedrock", "bedrock", "", "anthropic.claude-3-sonnet-20240229-v1:0"},
		{"OpenAI", "openai", "https://www.gmini.xyz", "gpt-5.2"}, // 大小写不敏感
		// 中国 LLM Provider
		{"doubao", "doubao", "https://ark.cn-beijing.volces.com/api", "doubao-1.5-pro-32k"},
		{"qwen", "qwen", "https://dashscope.aliyuncs.com/compatible-mode", "qwen-max"},
		{"qianfan", "qianfan", "https://qianfan.baidubce.com/v2", "ernie-4.0-8k"},
		{"moonshot", "moonshot", "https://api.moonshot.cn", "moonshot-v1-128k"},
		{"minimax", "minimax", "https://api.minimax.chat/v1", "MiniMax-Text-01"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			def := LookupProvider(tt.name)
			assert.Equal(t, tt.expected, def.Name)
			assert.Equal(t, tt.baseURL, def.BaseURL)
			assert.Equal(t, tt.model, def.DefaultModel)
		})
	}
}

func TestLookupProvider_Unknown(t *testing.T) {
	def := LookupProvider("unknown-provider")
	assert.Equal(t, "custom", def.Name)
	assert.Empty(t, def.BaseURL)
	assert.Empty(t, def.DefaultModel)
}

func TestLookupProvider_Capabilities(t *testing.T) {
	openai := LookupProvider("openai")
	assert.True(t, openai.HasCapability(CapVision))
	assert.True(t, openai.HasCapability(CapTools))
	assert.True(t, openai.HasCapability(CapJSONMode))
	assert.True(t, openai.HasCapability(CapAudio))

	deepseek := LookupProvider("deepseek")
	assert.True(t, deepseek.HasCapability(CapTools))
	assert.False(t, deepseek.HasCapability(CapVision))
	assert.True(t, deepseek.DisableJSONMode)

	anthropic := LookupProvider("anthropic")
	assert.True(t, anthropic.HasCapability(CapVision))
	assert.False(t, anthropic.HasCapability(CapJSONMode))

	// Google Gemini
	google := LookupProvider("google")
	assert.True(t, google.HasCapability(CapVision))
	assert.True(t, google.HasCapability(CapTools))
	assert.False(t, google.HasCapability(CapJSONMode))

	// Azure OpenAI
	azure := LookupProvider("azure")
	assert.True(t, azure.HasCapability(CapVision))
	assert.True(t, azure.HasCapability(CapTools))
	assert.True(t, azure.HasCapability(CapJSONMode))
	assert.True(t, azure.HasCapability(CapAudio))

	// Groq
	groq := LookupProvider("groq")
	assert.True(t, groq.HasCapability(CapTools))
	assert.True(t, groq.HasCapability(CapJSONMode))
	assert.False(t, groq.HasCapability(CapVision))

	// Mistral
	mistral := LookupProvider("mistral")
	assert.True(t, mistral.HasCapability(CapTools))
	assert.True(t, mistral.HasCapability(CapJSONMode))
	assert.False(t, mistral.HasCapability(CapVision))

	// AWS Bedrock
	bedrock := LookupProvider("bedrock")
	assert.True(t, bedrock.HasCapability(CapVision))
	assert.True(t, bedrock.HasCapability(CapTools))
	assert.False(t, bedrock.HasCapability(CapJSONMode))

	// 豆包 (Doubao)
	doubao := LookupProvider("doubao")
	assert.False(t, doubao.HasCapability(CapVision))
	assert.True(t, doubao.HasCapability(CapTools))
	assert.True(t, doubao.DisableJSONMode)

	// 通义千问 (Qwen)
	qwen := LookupProvider("qwen")
	assert.True(t, qwen.HasCapability(CapVision))
	assert.True(t, qwen.HasCapability(CapTools))
	assert.True(t, qwen.HasCapability(CapJSONMode))

	// 百度千帆 (Qianfan)
	qianfan := LookupProvider("qianfan")
	assert.True(t, qianfan.HasCapability(CapTools))
	assert.False(t, qianfan.HasCapability(CapVision))
	assert.True(t, qianfan.DisableJSONMode)

	// Moonshot
	moonshot := LookupProvider("moonshot")
	assert.True(t, moonshot.HasCapability(CapTools))
	assert.True(t, moonshot.HasCapability(CapJSONMode))
	assert.False(t, moonshot.HasCapability(CapVision))

	// MiniMax
	minimax := LookupProvider("minimax")
	assert.True(t, minimax.HasCapability(CapVision))
	assert.True(t, minimax.HasCapability(CapTools))
	assert.False(t, minimax.HasCapability(CapJSONMode))
}

func TestInferProviderFromURL(t *testing.T) {
	tests := []struct {
		url      string
		expected string
	}{
		{"http://45.205.26.177:9999", ""},
		{"https://www.gmini.xyz", "openai"},
		{"https://api.deepseek.com/v1", "deepseek"},
		{"https://api.deepseek.com", "deepseek"},
		{"https://api.anthropic.com/v1", "anthropic"},
		{"https://generativelanguage.googleapis.com/v1beta", "google"},
		{"https://generativelanguage.googleapis.com", "google"},
		{"https://my-resource.openai.azure.com", "azure"},
		{"https://my-resource.openai.azure.com/openai/deployments/gpt-4", "azure"},
		{"https://api.groq.com/openai/v1", "groq"},
		{"https://api.groq.com", "groq"},
		{"https://api.mistral.ai/v1", "mistral"},
		{"https://api.mistral.ai", "mistral"},
		{"https://bedrock-runtime.us-east-1.amazonaws.com", "bedrock"},
		// 中国 LLM Provider URL 推断
		{"https://ark.cn-beijing.volces.com/api/v3", "doubao"},
		{"https://ark.cn-shanghai.volces.com/api/v3", "doubao"},
		{"https://dashscope.aliyuncs.com/compatible-mode/v1", "qwen"},
		{"https://qianfan.baidubce.com/v2/chat/completions", "qianfan"},
		{"https://api.moonshot.cn/v1", "moonshot"},
		{"https://api.minimax.chat/v1/text/chatcompletion_v2", "minimax"},
		{"https://my-proxy.com/v1", ""},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.url, func(t *testing.T) {
			result := InferProviderFromURL(tt.url)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestRegisterProvider(t *testing.T) {
	RegisterProvider(ProviderDef{
		Name:         "my-custom",
		BaseURL:      "https://api.my-custom.com",
		DefaultModel: "my-model",
		Capabilities: map[Capability]bool{
			CapTools: true,
		},
	})

	def := LookupProvider("my-custom")
	assert.Equal(t, "my-custom", def.Name)
	assert.Equal(t, "https://api.my-custom.com", def.BaseURL)
	assert.Equal(t, "my-model", def.DefaultModel)
	assert.True(t, def.HasCapability(CapTools))
	assert.False(t, def.HasCapability(CapVision))

	// cleanup: remove from registry
	registryMu.Lock()
	delete(registry, "my-custom")
	registryMu.Unlock()
}

// TestNewProviders 测试新增的 5 个 Provider
func TestNewProviders(t *testing.T) {
	tests := []struct {
		name         string
		provider     string
		wantVision   bool
		wantTools    bool
		wantJSONMode bool
		wantAudio    bool
	}{
		{
			name:         "Google Gemini",
			provider:     "google",
			wantVision:   true,
			wantTools:    true,
			wantJSONMode: false,
			wantAudio:    false,
		},
		{
			name:         "Azure OpenAI",
			provider:     "azure",
			wantVision:   true,
			wantTools:    true,
			wantJSONMode: true,
			wantAudio:    true,
		},
		{
			name:         "Groq",
			provider:     "groq",
			wantVision:   false,
			wantTools:    true,
			wantJSONMode: true,
			wantAudio:    false,
		},
		{
			name:         "Mistral",
			provider:     "mistral",
			wantVision:   false,
			wantTools:    true,
			wantJSONMode: true,
			wantAudio:    false,
		},
		{
			name:         "AWS Bedrock",
			provider:     "bedrock",
			wantVision:   true,
			wantTools:    true,
			wantJSONMode: false,
			wantAudio:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			def := LookupProvider(tt.provider)
			assert.Equal(t, tt.provider, def.Name)
			assert.Equal(t, tt.wantVision, def.HasCapability(CapVision), "Vision capability mismatch")
			assert.Equal(t, tt.wantTools, def.HasCapability(CapTools), "Tools capability mismatch")
			assert.Equal(t, tt.wantJSONMode, def.HasCapability(CapJSONMode), "JSON Mode capability mismatch")
			assert.Equal(t, tt.wantAudio, def.HasCapability(CapAudio), "Audio capability mismatch")
		})
	}
}

// TestProviderBaseURLs 测试各 Provider 的默认 BaseURL
func TestProviderBaseURLs(t *testing.T) {
	tests := []struct {
		provider string
		baseURL  string
	}{
		{"google", "https://generativelanguage.googleapis.com"},
		{"azure", ""}, // Azure 需要自定义 endpoint
		{"groq", "https://api.groq.com/openai"},
		{"mistral", "https://api.mistral.ai"},
		{"bedrock", ""}, // Bedrock 需要自定义配置
		// 中国 LLM Provider
		{"doubao", "https://ark.cn-beijing.volces.com/api"},
		{"qwen", "https://dashscope.aliyuncs.com/compatible-mode"},
		{"qianfan", "https://qianfan.baidubce.com/v2"},
		{"moonshot", "https://api.moonshot.cn"},
		{"minimax", "https://api.minimax.chat/v1"},
	}

	for _, tt := range tests {
		t.Run(tt.provider, func(t *testing.T) {
			def := LookupProvider(tt.provider)
			assert.Equal(t, tt.baseURL, def.BaseURL)
		})
	}
}

// TestProviderDefaultModels 测试各 Provider 的默认模型
func TestProviderDefaultModels(t *testing.T) {
	tests := []struct {
		provider     string
		defaultModel string
	}{
		{"google", "gemini-1.5-pro-latest"},
		{"azure", "gpt-5"},
		{"groq", "llama3-70b-8192"},
		{"mistral", "mistral-large-latest"},
		{"bedrock", "anthropic.claude-3-sonnet-20240229-v1:0"},
		// 中国 LLM Provider
		{"doubao", "doubao-1.5-pro-32k"},
		{"qwen", "qwen-max"},
		{"qianfan", "ernie-4.0-8k"},
		{"moonshot", "moonshot-v1-128k"},
		{"minimax", "MiniMax-Text-01"},
	}

	for _, tt := range tests {
		t.Run(tt.provider, func(t *testing.T) {
			def := LookupProvider(tt.provider)
			assert.Equal(t, tt.defaultModel, def.DefaultModel)
		})
	}
}

// TestModelMatcher_ChineseProviders 测试中国 LLM 模型别名
func TestModelMatcher_ChineseProviders(t *testing.T) {
	m := NewModelMatcher()

	tests := []struct {
		alias    string
		expected string
	}{
		// 豆包
		{"doubao", "doubao-1.5-pro-32k"},
		{"doubao-pro", "doubao-1.5-pro-32k"},
		// 通义千问
		{"qwen", "qwen-max"},
		{"qwen-max", "qwen-max"},
		{"qwen-plus", "qwen-plus"},
		{"qwen-turbo", "qwen-turbo"},
		// 百度千帆
		{"qianfan", "ernie-4.0-8k"},
		{"ernie", "ernie-4.0-8k"},
		{"ernie-4.0", "ernie-4.0-8k"},
		// Moonshot / Kimi
		{"kimi", "moonshot-v1-128k"},
		{"moonshot", "moonshot-v1-128k"},
		{"moonshot-128k", "moonshot-v1-128k"},
		{"moonshot-32k", "moonshot-v1-32k"},
		// MiniMax
		{"minimax", "MiniMax-Text-01"},
	}

	for _, tt := range tests {
		t.Run(tt.alias, func(t *testing.T) {
			matched, _ := m.Match(tt.alias)
			assert.Equal(t, tt.expected, matched, "别名 %q 应匹配模型 %q", tt.alias, tt.expected)
		})
	}
}
