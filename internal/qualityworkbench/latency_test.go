package qualityworkbench

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestLatencySummaryComputesP95FromRawSamples(t *testing.T) {
	summary := SummarizeLatency([]time.Duration{
		10 * time.Millisecond,
		20 * time.Millisecond,
		30 * time.Millisecond,
		40 * time.Millisecond,
		50 * time.Millisecond,
		60 * time.Millisecond,
		70 * time.Millisecond,
		80 * time.Millisecond,
		90 * time.Millisecond,
		100 * time.Millisecond,
	})

	assert.Equal(t, 10, summary.Count)
	assert.Equal(t, 10*time.Millisecond, summary.Min)
	assert.Equal(t, 100*time.Millisecond, summary.Max)
	assert.Equal(t, 60*time.Millisecond, summary.P50)
	assert.Equal(t, 100*time.Millisecond, summary.P95)
}

func TestMergeLatencyBucketsComputesExactPercentilesAcrossBuckets(t *testing.T) {
	left := NewLatencyBucket([]time.Duration{10 * time.Millisecond, 30 * time.Millisecond, 50 * time.Millisecond})
	right := NewLatencyBucket([]time.Duration{20 * time.Millisecond, 40 * time.Millisecond, 60 * time.Millisecond, 80 * time.Millisecond})

	merged := MergeLatencyBuckets(left, right).Summary()

	assert.Equal(t, 7, merged.Count)
	assert.Equal(t, 40*time.Millisecond, merged.P50)
	assert.Equal(t, 80*time.Millisecond, merged.P95)
	assert.Equal(t, 80*time.Millisecond, merged.Max)
}
