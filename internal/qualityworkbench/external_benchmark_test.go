package qualityworkbench

import (
	"testing"

	"github.com/chef-guo/agents-hive/internal/agentquality"
	"github.com/stretchr/testify/assert"
)

func TestExternalBenchmarkKind(t *testing.T) {
	// 验证 external_benchmark 是有效的 kind
	err := ValidateBatchEvalKind(BatchEvalKindExternalBenchmark)
	assert.NoError(t, err)
}

func TestIsExternalBenchmark(t *testing.T) {
	t.Run("by suite_type", func(t *testing.T) {
		run := BatchEvalRun{
			SuiteType: "external_benchmark",
			Kind:      BatchEvalKindManual,
		}
		assert.True(t, IsExternalBenchmark(run))
	})

	t.Run("by kind", func(t *testing.T) {
		run := BatchEvalRun{
			Kind: BatchEvalKindExternalBenchmark,
		}
		assert.True(t, IsExternalBenchmark(run))
	})

	t.Run("not external", func(t *testing.T) {
		run := BatchEvalRun{
			Kind: BatchEvalKindManual,
		}
		assert.False(t, IsExternalBenchmark(run))
	})
}

func TestCanAuthorizeRollout(t *testing.T) {
	t.Run("external benchmark cannot authorize", func(t *testing.T) {
		run := BatchEvalRun{
			SuiteType: "external_benchmark",
		}
		assert.False(t, CanAuthorizeRollout(run))
	})

	t.Run("normal run can authorize", func(t *testing.T) {
		run := BatchEvalRun{
			Kind: BatchEvalKindManual,
			RunnerInfo: agentquality.RunnerInfo{
				EvidenceLevel: agentquality.EvidenceRealRunner,
			},
		}
		assert.True(t, CanAuthorizeRollout(run))
	})

	t.Run("static schema cannot authorize", func(t *testing.T) {
		run := BatchEvalRun{
			Kind: BatchEvalKindManual,
			RunnerInfo: agentquality.RunnerInfo{
				EvidenceLevel: agentquality.EvidenceStaticSchema,
			},
		}
		assert.False(t, CanAuthorizeRollout(run))
	})

	t.Run("missing evidence cannot authorize", func(t *testing.T) {
		run := BatchEvalRun{Kind: BatchEvalKindManual}
		assert.False(t, CanAuthorizeRollout(run))
	})
}
