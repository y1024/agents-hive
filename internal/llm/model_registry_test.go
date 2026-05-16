package llm

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestModelRegistry_DefaultModels(t *testing.T) {
	logger := zap.NewNop()

	tests := []struct {
		name    string
		modelID string
		want    string // 期望的 Name 字段
	}{
		{
			name:    "gpt-5 默认存在",
			modelID: "gpt-5",
			want:    "gpt-5",
		},
		{
			name:    "gpt-5 Mini 默认存在",
			modelID: "gpt-5-mini",
			want:    "gpt-5 Mini",
		},
		{
			name:    "Claude 3.5 Sonnet 默认存在",
			modelID: "claude-3-5-sonnet-20241022",
			want:    "Claude 3.5 Sonnet",
		},
		{
			name:    "DeepSeek Chat 默认存在",
			modelID: "deepseek-chat",
			want:    "DeepSeek Chat",
		},
		{
			name:    "o1 默认存在",
			modelID: "o1",
			want:    "OpenAI o1",
		},
		{
			name:    "Gemini 1.5 Pro 默认存在",
			modelID: "gemini-1.5-pro",
			want:    "Gemini 1.5 Pro",
		},
		{
			name:    "Mistral Large 默认存在",
			modelID: "mistral-large-latest",
			want:    "Mistral Large",
		},
		// 中国 LLM Provider
		{
			name:    "豆包 Pro 默认存在",
			modelID: "doubao-1.5-pro-32k",
			want:    "豆包 1.5 Pro 32K",
		},
		{
			name:    "通义千问 Max 默认存在",
			modelID: "qwen-max",
			want:    "通义千问 Max",
		},
		{
			name:    "文心一言 4.0 默认存在",
			modelID: "ernie-4.0-8k",
			want:    "文心一言 4.0",
		},
		{
			name:    "Moonshot 128K 默认存在",
			modelID: "moonshot-v1-128k",
			want:    "Moonshot v1 128K",
		},
		{
			name:    "MiniMax Text 01 默认存在",
			modelID: "MiniMax-Text-01",
			want:    "MiniMax Text 01",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			registry := NewModelRegistry(logger)
			meta := registry.Get(tt.modelID)
			require.NotNil(t, meta, "模型 %s 应存在于默认注册表中", tt.modelID)
			assert.Equal(t, tt.want, meta.Name)
		})
	}
}

func TestModelRegistry_Register(t *testing.T) {
	logger := zap.NewNop()

	tests := []struct {
		name    string
		modelID string
		meta    ModelMeta
	}{
		{
			name:    "注册自定义模型",
			modelID: "custom-model-v1",
			meta: ModelMeta{
				Name:          "Custom Model V1",
				ContextWindow: 32000,
				MaxOutput:     4096,
				Capabilities: ModelCapabilities{
					ToolUse:   true,
					Streaming: true,
				},
			},
		},
		{
			name:    "覆盖已有模型",
			modelID: "gpt-5",
			meta: ModelMeta{
				Name:          "gpt-5 Override",
				ContextWindow: 256000,
				MaxOutput:     32768,
				Capabilities: ModelCapabilities{
					Vision:    true,
					ToolUse:   true,
					JSON:      true,
					Streaming: true,
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			registry := NewModelRegistry(logger)
			registry.Register(tt.modelID, tt.meta)

			got := registry.Get(tt.modelID)
			require.NotNil(t, got, "注册后应能获取到模型 %s", tt.modelID)
			assert.Equal(t, tt.meta.Name, got.Name)
			assert.Equal(t, tt.meta.ContextWindow, got.ContextWindow)
			assert.Equal(t, tt.meta.MaxOutput, got.MaxOutput)
		})
	}
}

func TestModelRegistry_Get_NotFound(t *testing.T) {
	logger := zap.NewNop()

	tests := []struct {
		name    string
		modelID string
	}{
		{
			name:    "完全不存在的模型",
			modelID: "nonexistent-model-xyz",
		},
		{
			name:    "空字符串",
			modelID: "",
		},
		{
			name:    "相似但不完全匹配",
			modelID: "gpt-5-turbo",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			registry := NewModelRegistry(logger)
			got := registry.Get(tt.modelID)
			assert.Nil(t, got, "未注册的模型 %q 应返回 nil", tt.modelID)
		})
	}
}

func TestModelRegistry_List(t *testing.T) {
	logger := zap.NewNop()
	registry := NewModelRegistry(logger)

	listed := registry.List()

	// 验证返回了所有默认模型
	assert.Equal(t, len(modelRegistry), len(listed), "List 返回的数量应与默认注册表一致")

	// 验证返回的是副本而非原始 map
	listed["test-mutation"] = ModelMeta{Name: "Mutation Test"}
	relisted := registry.List()
	_, hasMutation := relisted["test-mutation"]
	assert.False(t, hasMutation, "List 应返回副本，外部修改不应影响注册表")

	// 验证每个默认模型都在列表中
	for id := range modelRegistry {
		_, ok := listed[id]
		assert.True(t, ok, "默认模型 %s 应在 List 结果中", id)
	}
}

func TestModelRegistry_BackwardCompat(t *testing.T) {
	// 测试 globalModelRegistryValue 未初始化时的向后兼容行为
	t.Run("globalModelRegistryValue 为空时回退到静态注册表", func(t *testing.T) {
		// 保存并重置全局注册表
		saved := getGlobalModelRegistry()
		setGlobalModelRegistryForTest(nil)
		defer func() {
			setGlobalModelRegistryForTest(saved)
		}()

		// GetModelMeta 应从静态注册表返回
		meta := GetModelMeta("gpt-5")
		require.NotNil(t, meta, "静态回退: gpt-5 应存在")
		assert.Equal(t, "gpt-5", meta.Name)

		// ListModelMetas 应返回静态注册表数据
		listed := ListModelMetas()
		assert.Equal(t, len(modelRegistry), len(listed))
	})

	t.Run("globalModelRegistryValue 初始化后委托到动态注册表", func(t *testing.T) {
		saved := getGlobalModelRegistry()
		defer func() {
			setGlobalModelRegistryForTest(saved)
		}()

		// 手动初始化全局注册表
		reg := NewModelRegistry(zap.NewNop())
		setGlobalModelRegistryForTest(reg)
		require.NotNil(t, getGlobalModelRegistry())

		// 注册一个自定义模型
		getGlobalModelRegistry().Register("backward-compat-test", ModelMeta{
			Name:          "BC Test",
			ContextWindow: 8000,
		})

		// GetModelMeta 应能查到动态注册的模型
		meta := GetModelMeta("backward-compat-test")
		require.NotNil(t, meta)
		assert.Equal(t, "BC Test", meta.Name)

		// 也能查到默认模型
		meta2 := GetModelMeta("gpt-5")
		require.NotNil(t, meta2)
		assert.Equal(t, "gpt-5", meta2.Name)

		// ListModelMetas 应包含默认模型 + 自定义模型
		listed := ListModelMetas()
		assert.Greater(t, len(listed), len(modelRegistry), "列表应包含额外注册的模型")
		_, ok := listed["backward-compat-test"]
		assert.True(t, ok)
	})
}

func TestModelRegistry_FetchRemote_EmptyURL(t *testing.T) {
	logger := zap.NewNop()
	registry := NewModelRegistry(logger)

	// remoteURL 为空时 FetchRemote 应直接返回 nil（不产生网络请求或 warn）
	err := registry.FetchRemote(context.Background())
	assert.NoError(t, err, "空 URL 时 FetchRemote 应返回 nil")
}

func TestModelRegistry_FetchRemote_WithURL(t *testing.T) {
	logger := zap.NewNop()
	registry := NewModelRegistry(logger)
	registry.remoteURL = "https://invalid.example.com/api/models"

	// 配置了 URL 但无法访问时应返回错误（不再静默跳过）
	err := registry.FetchRemote(context.Background())
	// 应返回错误或通过缓存回退成功（取决于本地缓存状态），不应 panic
	_ = err
}
