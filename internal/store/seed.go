package store

import (
	"context"
	"encoding/json"

	"go.uber.org/zap"
)

// SeedLLMProvider 种子用提供商参数（避免 import cycle，不引用 config 包）
type SeedLLMProvider struct {
	Name         string
	ProviderType string
	APIKey       string
	BaseURL      string
	ExtraConfig  map[string]any // 提供商特有配置（Azure/AWS/Google 等）
}

// SeedLLMModel 种子用模型参数
type SeedLLMModel struct {
	Name         string
	ProviderName string // 为空时使用默认 provider name
	Model        string
	BaseURL      string
	APIKey       string
}

// SeedLLMConfig 种子 LLM 配置的输入参数
type SeedLLMConfig struct {
	Provider     SeedLLMProvider
	Models       []SeedLLMModel
	DefaultModel string // config.json 中的 llm.model，用于标记 is_default
}

// SeedLLMFromConfig 从配置种子 LLM 提供商和模型到数据库。
// 仅在 llm_providers 表为空时执行，尊重运行时修改。
func SeedLLMFromConfig(ctx context.Context, db Store, cfg SeedLLMConfig, logger *zap.Logger) error {
	if cfg.Provider.Name == "" && cfg.Provider.APIKey == "" {
		logger.Debug("LLM 配置为空，跳过种子")
		return nil
	}

	// 检查是否已有提供商数据（非空则跳过）
	existing, err := db.ListLLMProviders(ctx)
	if err != nil {
		return err
	}
	if len(existing) > 0 {
		logger.Debug("LLM 提供商表已有数据，跳过种子", zap.Int("count", len(existing)))
		return nil
	}

	logger.Info("从配置文件种子 LLM 配置到数据库")

	providerName := cfg.Provider.Name
	if providerName == "" {
		providerName = "openai"
	}
	providerType := cfg.Provider.ProviderType
	if providerType == "" {
		providerType = providerName
	}

	// 序列化提供商特有配置
	configJSON := "{}"
	if len(cfg.Provider.ExtraConfig) > 0 {
		if data, err := json.Marshal(cfg.Provider.ExtraConfig); err == nil {
			configJSON = string(data)
		}
	}

	// 种子默认提供商
	provider := &LLMProviderRecord{
		Name:         providerName,
		ProviderType: providerType,
		APIKey:       cfg.Provider.APIKey,
		BaseURL:      cfg.Provider.BaseURL,
		IsDefault:    true,
		Enabled:      true,
		ConfigJSON:   configJSON,
	}
	if err := db.CreateLLMProvider(ctx, provider); err != nil {
		return err
	}
	logger.Info("已种子 LLM 提供商", zap.String("name", providerName))

	// 种子模型列表
	seededCount := 0
	for _, sm := range cfg.Models {
		pName := sm.ProviderName
		if pName == "" {
			pName = providerName
		}

		model := &LLMModelRecord{
			Name:         sm.Name,
			ProviderName: pName,
			Model:        sm.Model,
			BaseURL:      sm.BaseURL,
			APIKey:       sm.APIKey,
			IsDefault:    sm.Model == cfg.DefaultModel,
			Enabled:      true,
			ConfigJSON:   "{}",
		}
		if err := db.CreateLLMModel(ctx, model); err != nil {
			logger.Warn("种子 LLM 模型失败", zap.String("name", sm.Name), zap.Error(err))
			continue
		}
		seededCount++
	}

	// 如果 Models 列表为空但有默认模型，也创建一条
	if len(cfg.Models) == 0 && cfg.DefaultModel != "" {
		model := &LLMModelRecord{
			Name:         cfg.DefaultModel,
			ProviderName: providerName,
			Model:        cfg.DefaultModel,
			IsDefault:    true,
			Enabled:      true,
			ConfigJSON:   "{}",
		}
		if err := db.CreateLLMModel(ctx, model); err != nil {
			logger.Warn("种子默认 LLM 模型失败", zap.String("model", cfg.DefaultModel), zap.Error(err))
		} else {
			seededCount++
		}
	}

	logger.Info("LLM 配置种子完成", zap.Int("models", seededCount))
	return nil
}
