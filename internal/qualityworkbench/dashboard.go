package qualityworkbench

import (
	"time"

	"github.com/chef-guo/agents-hive/internal/agentquality"
)

type DashboardInput struct {
	Now        time.Time
	Window     time.Duration
	Clusters   []Cluster
	Candidates []agentquality.CandidateRecord
}

type DashboardSnapshot struct {
	Since                 time.Time                            `json:"since"`
	Until                 time.Time                            `json:"until"`
	OpenClusters          int                                  `json:"open_clusters"`
	CandidateStatusCounts map[agentquality.CandidateStatus]int `json:"candidate_status_counts"`
	FailureTypeCounts     map[agentquality.FailureType]int     `json:"failure_type_counts"`
	VerifyResultCounts    map[string]int                       `json:"verify_result_counts"`
}

type DashboardSeriesPoint struct {
	Since                 time.Time                            `json:"since"`
	Until                 time.Time                            `json:"until"`
	CandidateStatusCounts map[agentquality.CandidateStatus]int `json:"candidate_status_counts"`
	FailureTypeCounts     map[agentquality.FailureType]int     `json:"failure_type_counts"`
	VerifyResultCounts    map[string]int                       `json:"verify_result_counts"`
}

func BuildDashboardSnapshot(input DashboardInput) DashboardSnapshot {
	now := input.Now
	if now.IsZero() {
		now = time.Now()
	}
	since := now.Add(-input.Window)
	out := DashboardSnapshot{
		Since:                 since,
		Until:                 now,
		CandidateStatusCounts: map[agentquality.CandidateStatus]int{},
		FailureTypeCounts:     map[agentquality.FailureType]int{},
		VerifyResultCounts:    map[string]int{},
	}
	for _, c := range input.Clusters {
		if inWindow(c.LastSeen, since, now) && c.OpenCount > 0 {
			out.OpenClusters++
		}
	}
	for _, c := range input.Candidates {
		if !inWindow(c.CreatedAt, since, now) {
			continue
		}
		out.CandidateStatusCounts[c.Status]++
		out.FailureTypeCounts[firstFailureType(c.SourceEvent.FailureType, c.FailureType)]++
		out.VerifyResultCounts[dashboardVerifyResult(c.VerifyResult)]++
	}
	return out
}

func BuildDashboardSeries(input DashboardInput, bucketSize time.Duration) []DashboardSeriesPoint {
	if bucketSize <= 0 {
		bucketSize = 24 * time.Hour
	}
	now := input.Now
	if now.IsZero() {
		now = time.Now()
	}
	since := now.Add(-input.Window)
	points := make([]DashboardSeriesPoint, 0)
	for start := since; start.Before(now); start = start.Add(bucketSize) {
		end := start.Add(bucketSize)
		if end.After(now) {
			end = now
		}
		point := DashboardSeriesPoint{
			Since:                 start,
			Until:                 end,
			CandidateStatusCounts: map[agentquality.CandidateStatus]int{},
			FailureTypeCounts:     map[agentquality.FailureType]int{},
			VerifyResultCounts:    map[string]int{},
		}
		for _, c := range input.Candidates {
			if !inWindow(c.CreatedAt, start, end) {
				continue
			}
			point.CandidateStatusCounts[c.Status]++
			point.FailureTypeCounts[firstFailureType(c.SourceEvent.FailureType, c.FailureType)]++
			point.VerifyResultCounts[dashboardVerifyResult(c.VerifyResult)]++
		}
		points = append(points, point)
	}
	return points
}

func inWindow(ts, since, until time.Time) bool {
	if ts.IsZero() {
		return false
	}
	return (ts.Equal(since) || ts.After(since)) && ts.Before(until)
}

func dashboardVerifyResult(result string) string {
	normalized := normalizeVerifyResult(result)
	if normalized == "" {
		return "unknown"
	}
	return normalized
}
