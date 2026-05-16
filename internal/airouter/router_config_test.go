package airouter

import (
	"testing"

	"go.uber.org/zap"

	"github.com/chef-guo/agents-hive/internal/store"
)

func TestRouterReloadIgnoresInvalidModelOverrides(t *testing.T) {
	st := store.NewMemoryStore()
	ctx := t.Context()

	if err := st.SaveLLMProvider(ctx, &store.LLMProviderRecord{
		Name:         "openai",
		ProviderType: "openai",
		APIKey:       "sk-provider-secret",
		BaseURL:      "https://api.example.com",
		Enabled:      true,
		APIFormat:    "chat",
		ServiceType:  "llm",
		ConfigJSON:   "{}",
	}); err != nil {
		t.Fatalf("SaveLLMProvider failed: %v", err)
	}
	if err := st.SaveLLMModel(ctx, &store.LLMModelRecord{
		Name:         "gpt-5.2",
		ProviderName: "openai",
		Model:        "gpt-5.2",
		APIKey:       "xiyun",
		BaseURL:      "xiyun",
		IsDefault:    true,
		Enabled:      true,
		ConfigJSON:   "{}",
	}); err != nil {
		t.Fatalf("SaveLLMModel failed: %v", err)
	}

	r := NewRouter(RouterConfig{Store: st, Logger: zap.NewNop()})
	model := r.selectBestModel(TaskChat)
	if model == nil {
		t.Fatal("expected selected model")
	}
	if model.APIKey != "sk-provider-secret" {
		t.Fatalf("APIKey = %q, want provider key", model.APIKey)
	}
	if model.BaseURL != "https://api.example.com" {
		t.Fatalf("BaseURL = %q, want provider URL", model.BaseURL)
	}
}

func TestSwitchUserModelDoesNotMutateModelConfig(t *testing.T) {
	r := newTestRouter([]ModelScore{
		{Name: "main", Model: "gpt-5.2", Provider: "openai", BaseURL: "https://api.example.com", APIFormat: "chat", APIKey: "sk-secret"},
	}, "main")
	r.logger = zap.NewNop()

	if ok := r.SwitchUserModel("main"); !ok {
		t.Fatal("SwitchUserModel returned false")
	}

	model := r.selectBestModel(TaskChat)
	if model == nil {
		t.Fatal("expected selected model")
	}
	if model.Model != "gpt-5.2" || model.BaseURL != "https://api.example.com" || model.Provider != "openai" || model.APIFormat != "chat" {
		t.Fatalf("model config mutated unexpectedly: %+v", *model)
	}
}
