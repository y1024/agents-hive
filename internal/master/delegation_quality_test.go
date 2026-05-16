package master

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/chef-guo/agents-hive/internal/toolctx"
	"github.com/chef-guo/agents-hive/internal/tools"
)

func TestRecordDelegation_EmitsQualityEvent(t *testing.T) {
	m := &Master{obsCh: make(chan observabilityEntry, 2)}
	ctx := toolctx.WithSessionID(context.Background(), "session-1")

	m.RecordDelegation(ctx, tools.DelegationEvent{
		AgentID:     "agent-1",
		AgentType:   "subagent",
		Status:      "failed",
		Error:       "timeout",
		FailureType: "runtime",
	})

	first := <-m.obsCh
	second := <-m.obsCh
	require.NotNil(t, first.metric)
	require.NotNil(t, second.log)
	assert.Equal(t, "quality.delegation", first.metric.Name)
	assert.Equal(t, "runtime", first.metric.Labels["failure_type"])
	assert.Equal(t, "fail", first.metric.Labels["status"])
	assert.NotContains(t, first.metric.Labels, "session_id")
	assert.Equal(t, "session-1", second.log.SessionID)
}

func TestRecordDelegation_NormalizesStopReasonMetricLabel(t *testing.T) {
	m := &Master{obsCh: make(chan observabilityEntry, 2)}
	ctx := toolctx.WithSessionID(context.Background(), "session-1")

	m.RecordDelegation(ctx, tools.DelegationEvent{
		AgentType:  "subagent_group",
		Status:     "failed",
		StopReason: "completed=12 failed=3",
	})

	first := <-m.obsCh
	second := <-m.obsCh
	require.NotNil(t, first.metric)
	require.NotNil(t, second.log)
	assert.Equal(t, "summary", first.metric.Labels["stop_reason"])
	assert.NotEqual(t, "completed=12 failed=3", first.metric.Labels["stop_reason"])
	assert.Contains(t, second.log.Attributes, "quality_event")
}
