package accounting

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/chef-guo/agents-hive/internal/llm"
)

func TestCalcCost(t *testing.T) {
	t.Run("positive tokens with known pricing", func(t *testing.T) {
		// gpt-5: input $2.5e-6, output $10e-6
		cost := CalcCost(1000, 500, 2.5e-6, 10e-6)
		assert.InDelta(t, 0.0075, cost, 1e-10) // 1000*2.5e-6 + 500*10e-6 = 0.0025 + 0.005
	})

	t.Run("zero tokens", func(t *testing.T) {
		cost := CalcCost(0, 0, 2.5e-6, 10e-6)
		assert.Equal(t, float64(0), cost)
	})

	t.Run("zero pricing (unknown model)", func(t *testing.T) {
		cost := CalcCost(1000, 500, 0, 0)
		assert.Equal(t, float64(0), cost)
	})

	t.Run("only prompt tokens", func(t *testing.T) {
		cost := CalcCost(1000, 0, 2.5e-6, 10e-6)
		assert.InDelta(t, 0.0025, cost, 1e-10)
	})

	t.Run("only completion tokens", func(t *testing.T) {
		cost := CalcCost(0, 500, 2.5e-6, 10e-6)
		assert.InDelta(t, 0.005, cost, 1e-10)
	})

	t.Run("large token counts", func(t *testing.T) {
		// 1M prompt + 100K completion with claude pricing
		cost := CalcCost(1_000_000, 100_000, 3e-6, 15e-6)
		assert.InDelta(t, 4.5, cost, 1e-10) // 3.0 + 1.5
	})

	t.Run("very small per-token cost", func(t *testing.T) {
		cost := CalcCost(100, 50, 1e-7, 1e-7)
		assert.InDelta(t, 1.5e-5, cost, 1e-15)
	})
}

func TestCostSummaryZeroValue(t *testing.T) {
	s := &CostSummary{ByModel: make(map[string]ModelCost)}
	assert.Equal(t, float64(0), s.TotalCostUSD)
	assert.Equal(t, int64(0), s.TotalPromptTokens)
	assert.Equal(t, int64(0), s.RequestCount)
	assert.Empty(t, s.ByModel)
}

func TestQualityCostSummaryZeroValue(t *testing.T) {
	s := &QualityCostSummary{}
	assert.Empty(t, s.ByTaskType)
	assert.Empty(t, s.ByQualityCase)
	assert.Empty(t, s.ByPromptVersion)
	assert.Empty(t, s.ByFailureType)
	assert.Empty(t, s.ByFinalStatus)
	assert.Empty(t, s.TopQualityCases)
}

// 验证 PgTracker 实现了 CostTracker 接口
var _ CostTracker = (*PgTracker)(nil)

// --- mock CostTracker ---

type mockCostTracker struct {
	mu      sync.Mutex
	entries []UsageEntry
}

func (m *mockCostTracker) Record(_ context.Context, entry UsageEntry) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.entries = append(m.entries, entry)
	return nil
}

func (m *mockCostTracker) GetSessionCost(_ context.Context, _ string) (*CostSummary, error) {
	return &CostSummary{}, nil
}

func (m *mockCostTracker) GetTotalCost(_ context.Context, _ CostFilter) (*CostSummary, error) {
	return &CostSummary{}, nil
}

func (m *mockCostTracker) Cleanup(_ context.Context, _ int) (int64, error) {
	return 0, nil
}
func (m *mockCostTracker) GetCostByUser(_ context.Context) ([]UserCost, error) {
	return nil, nil
}

func (m *mockCostTracker) GetQualityCost(_ context.Context) (*QualityCostSummary, error) {
	return &QualityCostSummary{}, nil
}

func (m *mockCostTracker) recorded() []UsageEntry {
	m.mu.Lock()
	defer m.mu.Unlock()
	dst := make([]UsageEntry, len(m.entries))
	copy(dst, m.entries)
	return dst
}

// --- AsyncRecorder edge-case tests ---

func TestAsyncRecorder_SubmitAfterStop(t *testing.T) {
	mock := &mockCostTracker{}
	logger := zap.NewNop()
	rec := NewAsyncRecorder(mock, logger)

	// Submit one entry before stop to make sure the recorder works.
	rec.Submit(UsageEntry{SessionID: "s1", Model: "gpt-5"})
	rec.Stop()

	// Submit after Stop must not panic (guarded by atomic.Bool).
	assert.NotPanics(t, func() {
		rec.Submit(UsageEntry{SessionID: "s2", Model: "gpt-5"})
	})

	// Only the first entry should have been recorded.
	assert.Len(t, mock.recorded(), 1)
	assert.Equal(t, "s1", mock.recorded()[0].SessionID)
}

func TestAsyncRecorder_ChannelFullDrops(t *testing.T) {
	// Use a blocking mock so the worker never drains the channel.
	block := make(chan struct{})
	blocking := &blockingCostTracker{block: block}

	core, logs := observer.New(zapcore.WarnLevel)
	logger := zap.New(core)

	rec := NewAsyncRecorder(blocking, logger)

	// Fill the channel (buffer = 256). The first entry will be picked up by
	// the worker and block on Record, so we need 256 + 1 entries to saturate.
	for i := 0; i < asyncRecorderBufSize+1; i++ {
		rec.Submit(UsageEntry{SessionID: "fill", Model: "gpt-5"})
	}

	// Give the worker a moment to pick up the first entry and block.
	time.Sleep(50 * time.Millisecond)

	// This submit should be dropped because the channel is full.
	rec.Submit(UsageEntry{SessionID: "overflow", Model: "gpt-5"})

	// Verify the warning was logged.
	assert.GreaterOrEqual(t, logs.Len(), 1, "expected at least one warn log for dropped entry")
	assert.Contains(t, logs.All()[0].Message, "丢弃")

	// Unblock the worker and stop cleanly.
	close(block)
	rec.Stop()
}

// --- RecordUsage tests ---

func TestRecordUsage_ZeroTokensSkipped(t *testing.T) {
	mock := &mockCostTracker{}
	rec := NewAsyncRecorder(mock, zap.NewNop())
	defer rec.Stop()

	rec.RecordUsage("sess-1", "", "gpt-5", llm.Usage{PromptTokens: 0, CompletionTokens: 0})

	// Give worker a moment, then verify nothing was recorded.
	time.Sleep(50 * time.Millisecond)
	assert.Empty(t, mock.recorded(), "zero-token usage should be skipped")
}

func TestRecordUsage_UnknownModelZeroCost(t *testing.T) {
	mock := &mockCostTracker{}
	rec := NewAsyncRecorder(mock, zap.NewNop())

	rec.RecordUsage("sess-2", "", "unknown-model-xyz", llm.Usage{PromptTokens: 100, CompletionTokens: 50})
	rec.Stop() // flush

	entries := mock.recorded()
	assert.Len(t, entries, 1)
	assert.Equal(t, "sess-2", entries[0].SessionID)
	assert.Equal(t, "", entries[0].UserID)
	assert.Equal(t, "unknown-model-xyz", entries[0].Model)
	assert.Equal(t, int64(100), entries[0].PromptTokens)
	assert.Equal(t, int64(50), entries[0].CompletionTokens)
	assert.Equal(t, float64(0), entries[0].CostUSD, "unknown model should have zero cost")
}

func TestRecordUsage_UserIDPropagated(t *testing.T) {
	mock := &mockCostTracker{}
	rec := NewAsyncRecorder(mock, zap.NewNop())

	rec.RecordUsage("sess-uid", "user-42", "unknown-model-xyz", llm.Usage{PromptTokens: 10, CompletionTokens: 5})
	rec.Stop()

	entries := mock.recorded()
	assert.Len(t, entries, 1)
	assert.Equal(t, "user-42", entries[0].UserID, "userID must be propagated to UsageEntry")
}

func TestRecordUsageWithMeta_QualityFieldsPropagated(t *testing.T) {
	mock := &mockCostTracker{}
	rec := NewAsyncRecorder(mock, zap.NewNop())

	rec.RecordUsageWithMeta("sess-quality", "user-42", "unknown-model-xyz", llm.Usage{PromptTokens: 10, CompletionTokens: 5}, UsageMeta{
		TaskType:      "react",
		QualityCaseID: "aq01",
		PromptVersion: "system/base@db@sha256:abc",
		FailureType:   "tool",
		FinalStatus:   "fail",
	})
	rec.Stop()

	entries := mock.recorded()
	assert.Len(t, entries, 1)
	assert.Equal(t, "react", entries[0].TaskType)
	assert.Equal(t, "aq01", entries[0].QualityCaseID)
	assert.Equal(t, "system/base@db@sha256:abc", entries[0].PromptVersion)
	assert.Equal(t, "tool", entries[0].FailureType)
	assert.Equal(t, "fail", entries[0].FinalStatus)
}

func TestRecordUsage_KnownModelCalcsCost(t *testing.T) {
	mock := &mockCostTracker{}
	rec := NewAsyncRecorder(mock, zap.NewNop())

	// Use a model that has pricing in GetModelMeta.
	// Pick any model registered in model_meta.go; if none match, cost will be 0.
	model := "gpt-5"
	meta := llm.GetModelMeta(model)
	if meta == nil {
		t.Skip("gpt-5 not in model registry, skipping cost calculation test")
	}

	rec.RecordUsage("sess-3", "", model, llm.Usage{PromptTokens: 1000, CompletionTokens: 500})
	rec.Stop()

	entries := mock.recorded()
	assert.Len(t, entries, 1)
	assert.Equal(t, "sess-3", entries[0].SessionID)
	assert.Equal(t, model, entries[0].Model)
	expectedCost := CalcCost(1000, 500, meta.CostPerInputToken, meta.CostPerOutputToken)
	assert.InDelta(t, expectedCost, entries[0].CostUSD, 1e-10)
}

// blockingCostTracker blocks on the first Record call until unblocked.
type blockingCostTracker struct {
	block chan struct{}
	once  sync.Once
}

func (b *blockingCostTracker) Record(_ context.Context, _ UsageEntry) error {
	b.once.Do(func() { <-b.block })
	return nil
}

func (b *blockingCostTracker) GetSessionCost(_ context.Context, _ string) (*CostSummary, error) {
	return &CostSummary{}, nil
}

func (b *blockingCostTracker) GetTotalCost(_ context.Context, _ CostFilter) (*CostSummary, error) {
	return &CostSummary{}, nil
}

func (b *blockingCostTracker) Cleanup(_ context.Context, _ int) (int64, error) {
	return 0, nil
}
func (b *blockingCostTracker) GetCostByUser(_ context.Context) ([]UserCost, error) {
	return nil, nil
}

func (b *blockingCostTracker) GetQualityCost(_ context.Context) (*QualityCostSummary, error) {
	return &QualityCostSummary{}, nil
}
