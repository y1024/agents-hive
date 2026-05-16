package airouter

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// add-spec-driven-cognition Phase 2 A-0 contract test:
// 验证 TaskPlanning 路由遵循 spec 硬约束：
//   cheapest(json) → cheapest(tools) → userSelectedModel
// 绝不回落到 TaskChat（planner 流量不得偷主对话模型预算）。
func newTestRouter(models []ModelScore, userModelName string) *Router {
	return &Router{
		models:    models,
		userModel: userModelName,
	}
}

func TestSelector_TaskPlanning_PrefersCheapestJSON(t *testing.T) {
	models := []ModelScore{
		{Name: "main", Model: "gpt-5", CostTier: TierMedium, Capabilities: []string{"tools", "json", "vision"}},
		{Name: "haiku", Model: "claude-haiku", CostTier: TierCheap, Capabilities: []string{"tools", "json"}},
		{Name: "opus", Model: "claude-opus", CostTier: TierExpensive, Capabilities: []string{"tools", "json", "reasoning"}},
	}
	r := newTestRouter(models, "main")
	m := r.selectBestModel(TaskPlanning)
	require.NotNil(t, m)
	assert.Equal(t, "haiku", m.Name, "应选最便宜且支持 json 的模型")
}

func TestSelector_TaskPlanning_FallbackToCheapestTools(t *testing.T) {
	// 场景：没有任何模型带 json capability
	models := []ModelScore{
		{Name: "main", Model: "gpt-5", CostTier: TierMedium, Capabilities: []string{"tools"}},
		{Name: "cheap-tools", Model: "gpt-5-mini", CostTier: TierCheap, Capabilities: []string{"tools"}},
	}
	r := newTestRouter(models, "main")
	m := r.selectBestModel(TaskPlanning)
	require.NotNil(t, m)
	assert.Equal(t, "cheap-tools", m.Name, "无 json 能力模型时应回落 cheapest(tools)")
}

func TestSelector_TaskPlanning_FallbackToUserModel(t *testing.T) {
	// 场景：既无 json 也无 tools
	models := []ModelScore{
		{Name: "basic", Model: "gpt-3.5", CostTier: TierCheap, Capabilities: []string{"chat"}},
		{Name: "main", Model: "gpt-4", CostTier: TierMedium, Capabilities: []string{"chat"}},
	}
	r := newTestRouter(models, "main")
	m := r.selectBestModel(TaskPlanning)
	require.NotNil(t, m)
	assert.Equal(t, "main", m.Name, "无 json/tools 时应回落用户选定模型")
}

// 核心硬约束：TaskPlanning 绝不能路由到和 TaskChat 相同的 main model
// 只要环境里有更便宜且带 json/tools 的模型。
func TestSelector_TaskPlanning_NeverStealsMainBudget(t *testing.T) {
	models := []ModelScore{
		{Name: "main", Model: "gpt-5", CostTier: TierMedium, Capabilities: []string{"tools", "json"}},
		{Name: "haiku", Model: "claude-haiku", CostTier: TierCheap, Capabilities: []string{"tools", "json"}},
	}
	r := newTestRouter(models, "main")

	planningModel := r.selectBestModel(TaskPlanning)
	chatModel := r.selectBestModel(TaskChat)

	require.NotNil(t, planningModel)
	require.NotNil(t, chatModel)
	assert.NotEqual(t, chatModel.Name, planningModel.Name,
		"TaskPlanning 绝不能和 TaskChat 用同一个模型（否则偷主对话预算）")
	assert.Equal(t, "haiku", planningModel.Name)
	assert.Equal(t, "main", chatModel.Name)
}

func TestSelector_TaskPlanning_EmptyModelsReturnsNil(t *testing.T) {
	r := newTestRouter([]ModelScore{}, "")
	m := r.selectBestModel(TaskPlanning)
	assert.Nil(t, m, "无任何模型时应返回 nil")
}
