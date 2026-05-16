package llm

import (
	"strings"
	"sync"
)

// Capability 表示 LLM Provider 支持的功能
type Capability string

const (
	CapVision    Capability = "vision"
	CapTools     Capability = "tools"
	CapJSONMode  Capability = "json_mode"
	CapAudio     Capability = "audio"
	CapFile      Capability = "file"
	CapStreaming Capability = "streaming"
)

// ProviderDef 定义一个 LLM 提供商的元信息
type ProviderDef struct {
	Name            string
	BaseURL         string // 如 "https://www.gmini.xyz"（不含 /v1）
	DefaultModel    string
	Capabilities    map[Capability]bool
	DisableJSONMode bool
	APIFormat       string // "chat"（Chat Completions）或 "responses"（Responses API），空字符串视为 "chat"
}

// UseResponsesAPI 返回该 Provider 是否使用 Responses API
func (p ProviderDef) UseResponsesAPI() bool {
	return p.APIFormat == "responses"
}

// HasCapability 检查 Provider 是否支持指定功能
func (p ProviderDef) HasCapability(cap Capability) bool {
	return p.Capabilities[cap]
}

var (
	registryMu sync.RWMutex
	registry   = map[string]ProviderDef{
		"openai": {
			Name:         "openai",
			BaseURL:      "https://www.gmini.xyz",
			DefaultModel: "gpt-5.2",
			Capabilities: map[Capability]bool{
				CapVision:    true,
				CapTools:     true,
				CapJSONMode:  true,
				CapAudio:     true,
				CapFile:      true,
				CapStreaming: true,
			},
		},
		"deepseek": {
			Name:            "deepseek",
			BaseURL:         "https://api.deepseek.com",
			DefaultModel:    "deepseek-chat",
			DisableJSONMode: true,
			Capabilities: map[Capability]bool{
				CapTools:     true,
				CapStreaming: true,
			},
		},
		"anthropic": {
			Name:         "anthropic",
			BaseURL:      "https://api.anthropic.com",
			DefaultModel: "claude-sonnet-4-20250514",
			Capabilities: map[Capability]bool{
				CapVision:    true,
				CapTools:     true,
				CapStreaming: true,
			},
		},
		"google": {
			Name:         "google",
			BaseURL:      "https://generativelanguage.googleapis.com",
			DefaultModel: "gemini-1.5-pro-latest",
			Capabilities: map[Capability]bool{
				CapVision:    true,
				CapTools:     true,
				CapStreaming: true,
			},
		},
		"azure": {
			Name:         "azure",
			BaseURL:      "", // Azure 需要自定义 endpoint: https://{resource}.openai.azure.com
			DefaultModel: "gpt-5",
			Capabilities: map[Capability]bool{
				CapVision:    true,
				CapTools:     true,
				CapJSONMode:  true,
				CapAudio:     true,
				CapFile:      true,
				CapStreaming: true,
			},
		},
		"groq": {
			Name:         "groq",
			BaseURL:      "https://api.groq.com/openai",
			DefaultModel: "llama3-70b-8192",
			Capabilities: map[Capability]bool{
				CapTools:     true,
				CapJSONMode:  true,
				CapStreaming: true,
			},
		},
		"mistral": {
			Name:         "mistral",
			BaseURL:      "https://api.mistral.ai",
			DefaultModel: "mistral-large-latest",
			Capabilities: map[Capability]bool{
				CapTools:     true,
				CapJSONMode:  true,
				CapStreaming: true,
			},
		},
		"doubao": {
			Name:            "doubao",
			BaseURL:         "https://ark.cn-beijing.volces.com/api",
			DefaultModel:    "doubao-1.5-pro-32k",
			DisableJSONMode: true, // 部分豆包模型不支持 response_format
			Capabilities: map[Capability]bool{
				CapTools:     true,
				CapStreaming: true,
			},
		},
		"qwen": {
			Name:         "qwen",
			BaseURL:      "https://dashscope.aliyuncs.com/compatible-mode",
			DefaultModel: "qwen-max",
			Capabilities: map[Capability]bool{
				CapVision:    true,
				CapTools:     true,
				CapJSONMode:  true,
				CapStreaming: true,
			},
		},
		"qianfan": {
			Name:            "qianfan",
			BaseURL:         "https://qianfan.baidubce.com/v2",
			DefaultModel:    "ernie-4.0-8k",
			DisableJSONMode: true, // 千帆平台部分模型不支持 response_format
			Capabilities: map[Capability]bool{
				CapTools:     true,
				CapStreaming: true,
			},
		},
		"moonshot": {
			Name:         "moonshot",
			BaseURL:      "https://api.moonshot.cn",
			DefaultModel: "moonshot-v1-128k",
			Capabilities: map[Capability]bool{
				CapTools:     true,
				CapJSONMode:  true,
				CapStreaming: true,
			},
		},
		"minimax": {
			Name:         "minimax",
			BaseURL:      "https://api.minimax.chat/v1",
			DefaultModel: "MiniMax-Text-01",
			Capabilities: map[Capability]bool{
				CapVision:    true,
				CapTools:     true,
				CapStreaming: true,
			},
		},
		"xai": {
			Name:         "xai",
			BaseURL:      "https://api.x.ai/v1",
			DefaultModel: "grok-3",
			Capabilities: map[Capability]bool{
				CapVision:    true,
				CapTools:     true,
				CapJSONMode:  true,
				CapStreaming: true,
			},
		},
		// AWS Bedrock：endpoint 按 region 动态构造（bedrock-runtime.{region}.amazonaws.com），
		// 故 BaseURL 留空由 client 侧配置。DefaultModel 取 Anthropic Claude 3 Sonnet 的
		// Bedrock 模型 ID——与 provider_test.go 的断言锁定。JSONMode 不支持（Bedrock
		// Claude 不提供原生 response_format），audio 未开放。
		"bedrock": {
			Name:         "bedrock",
			BaseURL:      "",
			DefaultModel: "anthropic.claude-3-sonnet-20240229-v1:0",
			Capabilities: map[Capability]bool{
				CapVision:    true,
				CapTools:     true,
				CapStreaming: true,
			},
		},
		"custom": {
			Name: "custom",
			Capabilities: map[Capability]bool{
				CapTools:     true,
				CapStreaming: true,
			},
		},
	}
)

// LookupProvider 查找 provider，未知则返回 "custom"
func LookupProvider(name string) ProviderDef {
	registryMu.RLock()
	defer registryMu.RUnlock()

	lower := strings.ToLower(name)
	if def, ok := registry[lower]; ok {
		return def
	}
	return registry["custom"]
}

// RegisterProvider 注册或覆盖一个 provider 定义
func RegisterProvider(def ProviderDef) {
	registryMu.Lock()
	defer registryMu.Unlock()

	registry[strings.ToLower(def.Name)] = def
}

// InferProviderFromURL 从 URL 推断 provider 名称
// 用于向后兼容：旧配置有 base_url 但无 provider 时自动推断
func InferProviderFromURL(baseURL string) string {
	if baseURL == "" {
		return ""
	}

	lower := strings.ToLower(baseURL)

	// Azure 特殊处理：检测 openai.azure.com 域名
	if strings.Contains(lower, "openai.azure.com") {
		return "azure"
	}

	// xAI 特殊处理：api.x.ai 域名
	if strings.Contains(lower, "api.x.ai") {
		return "xai"
	}

	// AWS Bedrock 特殊处理：bedrock-runtime.{region}.amazonaws.com 由 region 变化，
	// registry 中 BaseURL 空串无法走通用匹配，这里显式按 bedrock-runtime 前缀识别。
	if strings.Contains(lower, "bedrock-runtime") && strings.Contains(lower, "amazonaws.com") {
		return "bedrock"
	}

	// 豆包特殊处理：火山引擎域名 volces.com
	if strings.Contains(lower, "volces.com") {
		return "doubao"
	}

	// 通义千问特殊处理：阿里云 DashScope 域名
	if strings.Contains(lower, "dashscope") && strings.Contains(lower, "aliyuncs.com") {
		return "qwen"
	}

	// 百度千帆特殊处理：百度智能云域名
	if strings.Contains(lower, "qianfan") && strings.Contains(lower, "baidubce.com") {
		return "qianfan"
	}

	registryMu.RLock()
	defer registryMu.RUnlock()

	for name, def := range registry {
		if name == "custom" || def.BaseURL == "" {
			continue
		}
		// 检查 URL 是否包含 provider 的 base URL 域名
		defLower := strings.ToLower(def.BaseURL)
		// 从 baseURL 提取域名部分做匹配
		if strings.Contains(lower, extractDomain(defLower)) {
			return name
		}
	}
	return ""
}

// extractDomain 从 URL 中提取域名（简单实现）
// "https://www.gmini.xyz" → "api.openai.com"
// "https://api.deepseek.com/v1" → "api.deepseek.com"
func extractDomain(u string) string {
	// 去掉 scheme
	s := u
	if idx := strings.Index(s, "://"); idx >= 0 {
		s = s[idx+3:]
	}
	// 去掉路径
	if idx := strings.Index(s, "/"); idx >= 0 {
		s = s[:idx]
	}
	// 去掉端口
	if idx := strings.Index(s, ":"); idx >= 0 {
		s = s[:idx]
	}
	return s
}

// ModelMatcher 模型模糊匹配器
type ModelMatcher struct {
	aliases map[string]string // 别名映射
}

// NewModelMatcher 创建一个新的模型匹配器
func NewModelMatcher() *ModelMatcher {
	return &ModelMatcher{
		aliases: map[string]string{
			// OpenAI
			"gpt4":        "gpt-4",
			"gpt4o":       "gpt-5-2024-11-20",
			"gpt4-turbo":  "gpt-4-turbo-preview",
			"gpt-4-turbo": "gpt-4-turbo-preview",
			"gpt-3.5":     "gpt-3.5-turbo",
			"gpt3.5":      "gpt-3.5-turbo",
			"gpt-5":       "gpt-5.2",
			"gpt5":        "gpt-5.2",

			// Anthropic
			"sonnet":        "claude-sonnet-4-20250514",
			"claude-sonnet": "claude-sonnet-4-20250514",
			"opus":          "claude-3-opus-20240229",
			"claude-opus":   "claude-3-opus-20240229",
			"haiku":         "claude-3-haiku-20240307",
			"claude-haiku":  "claude-3-haiku-20240307",

			// DeepSeek
			"deepseek":      "deepseek-chat",
			"deepseek-chat": "deepseek-chat",

			// Google
			"gemini":       "gemini-1.5-pro-latest",
			"gemini-pro":   "gemini-1.5-pro-latest",
			"gemini-1.5":   "gemini-1.5-pro-latest",
			"gemini-flash": "gemini-1.5-flash-latest",

			// Groq
			"llama":  "llama3-70b-8192",
			"llama3": "llama3-70b-8192",

			// Mistral
			"mistral":       "mistral-large-latest",
			"mistral-large": "mistral-large-latest",

			// 豆包 (Doubao / 火山引擎)
			"doubao":     "doubao-1.5-pro-32k",
			"doubao-pro": "doubao-1.5-pro-32k",

			// 通义千问 (Qwen / 阿里云)
			"qwen":       "qwen-max",
			"qwen-max":   "qwen-max",
			"qwen-plus":  "qwen-plus",
			"qwen-turbo": "qwen-turbo",

			// 百度千帆 (Qianfan / 文心一言)
			"qianfan":   "ernie-4.0-8k",
			"ernie":     "ernie-4.0-8k",
			"ernie-4.0": "ernie-4.0-8k",

			// Moonshot / Kimi
			"kimi":          "moonshot-v1-128k",
			"moonshot":      "moonshot-v1-128k",
			"moonshot-128k": "moonshot-v1-128k",
			"moonshot-32k":  "moonshot-v1-32k",

			// MiniMax / 海螺 AI
			"minimax": "MiniMax-Text-01",
		},
	}
}

// Match 模糊匹配模型名
// 返回匹配的模型名和建议列表
func (m *ModelMatcher) Match(input string) (matched string, suggestions []string) {
	rawInput := strings.TrimSpace(input)
	input = strings.ToLower(rawInput)

	// 已注册的精确模型名优先于别名映射，避免把用户明确配置的
	// gpt-5 之类稳定模型 ID 自动改写成其他默认别名。
	if rawInput != "" {
		if GetModelMeta(rawInput) != nil {
			return rawInput, nil
		}
		if rawInput != input && GetModelMeta(input) != nil {
			return input, nil
		}
	}

	// 1. 精确匹配别名
	if model, ok := m.aliases[input]; ok {
		return model, nil
	}

	// 2. 前缀匹配
	for alias, model := range m.aliases {
		if strings.HasPrefix(alias, input) && len(input) >= 3 {
			suggestions = append(suggestions, alias+" → "+model)
		}
	}

	// 3. 如果前缀匹配没结果，尝试包含匹配
	if len(suggestions) == 0 {
		for alias, model := range m.aliases {
			if strings.Contains(alias, input) && len(input) >= 3 {
				suggestions = append(suggestions, alias+" → "+model)
			}
		}
	}

	return "", suggestions
}

// SuggestModel 给出模型建议（用于错误提示）
func (m *ModelMatcher) SuggestModel(input string) string {
	matched, suggestions := m.Match(input)
	if matched != "" {
		return matched
	}

	if len(suggestions) > 0 {
		// 最多显示 5 个建议
		if len(suggestions) > 5 {
			suggestions = suggestions[:5]
		}
		return "未找到模型 '" + input + "'，可能的选项:\n  " + strings.Join(suggestions, "\n  ")
	}

	return "未找到模型 '" + input + "'"
}
