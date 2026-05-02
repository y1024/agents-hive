package qualityworkbench

import (
	"sort"
	"time"
)

type LatencySummary struct {
	Count int           `json:"count"`
	Min   time.Duration `json:"min"`
	Max   time.Duration `json:"max"`
	P50   time.Duration `json:"p50"`
	P95   time.Duration `json:"p95"`
	P99   time.Duration `json:"p99"`
}

type LatencyBucket struct {
	samples []time.Duration
}

func NewLatencyBucket(samples []time.Duration) LatencyBucket {
	b := LatencyBucket{samples: append([]time.Duration(nil), samples...)}
	sort.Slice(b.samples, func(i, j int) bool { return b.samples[i] < b.samples[j] })
	return b
}

func MergeLatencyBuckets(buckets ...LatencyBucket) LatencyBucket {
	var samples []time.Duration
	for _, bucket := range buckets {
		samples = append(samples, bucket.samples...)
	}
	return NewLatencyBucket(samples)
}

func SummarizeLatency(samples []time.Duration) LatencySummary {
	return NewLatencyBucket(samples).Summary()
}

func (b LatencyBucket) Summary() LatencySummary {
	if len(b.samples) == 0 {
		return LatencySummary{}
	}
	samples := append([]time.Duration(nil), b.samples...)
	sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
	return LatencySummary{
		Count: len(samples),
		Min:   samples[0],
		Max:   samples[len(samples)-1],
		P50:   percentileNearestRank(samples, 0.50),
		P95:   percentileNearestRank(samples, 0.95),
		P99:   percentileNearestRank(samples, 0.99),
	}
}

func percentileNearestRank(samples []time.Duration, p float64) time.Duration {
	if len(samples) == 0 {
		return 0
	}
	if p <= 0 {
		return samples[0]
	}
	if p >= 1 {
		return samples[len(samples)-1]
	}
	rankFloat := p * float64(len(samples)+1)
	rank := int(rankFloat)
	if float64(rank) < rankFloat {
		rank++
	}
	if rank < 1 {
		rank = 1
	}
	if rank > len(samples) {
		rank = len(samples)
	}
	return samples[rank-1]
}
