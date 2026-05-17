package qualityworkbench

import (
	"time"

	"github.com/chef-guo/agents-hive/internal/agentquality"
)

// DomainRegressionStatus 表示单个业务域的 regression suite 状态。
type DomainRegressionStatus struct {
	DomainID       string  `json:"domain_id"`
	Status         string  `json:"status"` // "pass", "fail", "unknown"
	SemanticScore  float64 `json:"semantic_score"`
	SafetyFailures int     `json:"safety_failures"`
	ActiveCases    int     `json:"active_cases"`
	EvidenceLevel  string  `json:"evidence_level"`
}

// DomainRegressionInput 是构建业务域 regression 状态的输入。
type DomainRegressionInput struct {
	DomainID       string
	SemanticScore  float64
	SafetyFailures int
	ActiveCases    int
	EvidenceLevel  string
	MinCases       int
	MinScore       float64
}

// BuildDomainRegressionStatus 根据输入构建单个业务域的 regression 状态。
func BuildDomainRegressionStatus(input DomainRegressionInput) DomainRegressionStatus {
	status := "unknown"
	if input.ActiveCases > 0 && input.EvidenceLevel != "" {
		if input.EvidenceLevel == "static_schema" {
			status = "fail"
		} else if input.SafetyFailures > 0 {
			status = "fail"
		} else if input.MinCases > 0 && input.ActiveCases < input.MinCases {
			status = "fail"
		} else if input.MinScore > 0 && input.SemanticScore < input.MinScore {
			status = "fail"
		} else {
			status = "pass"
		}
	}
	return DomainRegressionStatus{
		DomainID:       input.DomainID,
		Status:         status,
		SemanticScore:  input.SemanticScore,
		SafetyFailures: input.SafetyFailures,
		ActiveCases:    input.ActiveCases,
		EvidenceLevel:  input.EvidenceLevel,
	}
}

type DashboardInput struct {
	Since      time.Time
	Until      time.Time
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
	KBFailureTypeCounts   map[string]int                       `json:"kb_failure_type_counts"`
	VerifyResultCounts    map[string]int                       `json:"verify_result_counts"`
	DomainCounts          map[string]int                       `json:"domain_counts"`
	SourceKindCounts      map[string]int                       `json:"source_kind_counts"`
	SourceNameCounts      map[string]int                       `json:"source_name_counts"`
}

type DashboardSeriesPoint struct {
	Since                 time.Time                            `json:"since"`
	Until                 time.Time                            `json:"until"`
	CandidateStatusCounts map[agentquality.CandidateStatus]int `json:"candidate_status_counts"`
	FailureTypeCounts     map[agentquality.FailureType]int     `json:"failure_type_counts"`
	KBFailureTypeCounts   map[string]int                       `json:"kb_failure_type_counts"`
	VerifyResultCounts    map[string]int                       `json:"verify_result_counts"`
	DomainCounts          map[string]int                       `json:"domain_counts"`
	SourceKindCounts      map[string]int                       `json:"source_kind_counts"`
	SourceNameCounts      map[string]int                       `json:"source_name_counts"`
}

func BuildDashboardSnapshot(input DashboardInput) DashboardSnapshot {
	since, until := dashboardWindow(input)
	out := DashboardSnapshot{
		Since:                 since,
		Until:                 until,
		CandidateStatusCounts: map[agentquality.CandidateStatus]int{},
		FailureTypeCounts:     map[agentquality.FailureType]int{},
		KBFailureTypeCounts:   map[string]int{},
		VerifyResultCounts:    map[string]int{},
		DomainCounts:          map[string]int{},
		SourceKindCounts:      map[string]int{},
		SourceNameCounts:      map[string]int{},
	}
	for _, c := range input.Clusters {
		if inWindow(c.LastSeen, since, until) && c.OpenCount > 0 {
			out.OpenClusters++
		}
	}
	for _, c := range input.Candidates {
		if !inWindow(c.CreatedAt, since, until) {
			continue
		}
		out.CandidateStatusCounts[c.Status]++
		out.FailureTypeCounts[firstFailureType(c.SourceEvent.FailureType, c.FailureType)]++
		if failure := kbFailureType(c.SourceEvent); failure != "" {
			out.KBFailureTypeCounts[failure]++
		}
		out.VerifyResultCounts[dashboardVerifyResult(c.VerifyResult)]++
		out.DomainCounts[sourceBreakdownValue(c.SourceEvent.DomainID)]++
		out.SourceKindCounts[sourceBreakdownValue(c.SourceEvent.SourceKind)]++
		out.SourceNameCounts[sourceBreakdownValue(c.SourceEvent.SourceName)]++
	}
	return out
}

func BuildDashboardSeries(input DashboardInput, bucketSize time.Duration) []DashboardSeriesPoint {
	if bucketSize <= 0 {
		bucketSize = 24 * time.Hour
	}
	since, until := dashboardWindow(input)
	points := make([]DashboardSeriesPoint, 0)
	for start := since; start.Before(until); start = start.Add(bucketSize) {
		end := start.Add(bucketSize)
		if end.After(until) {
			end = until
		}
		point := DashboardSeriesPoint{
			Since:                 start,
			Until:                 end,
			CandidateStatusCounts: map[agentquality.CandidateStatus]int{},
			FailureTypeCounts:     map[agentquality.FailureType]int{},
			KBFailureTypeCounts:   map[string]int{},
			VerifyResultCounts:    map[string]int{},
			DomainCounts:          map[string]int{},
			SourceKindCounts:      map[string]int{},
			SourceNameCounts:      map[string]int{},
		}
		for _, c := range input.Candidates {
			if !inWindow(c.CreatedAt, start, end) {
				continue
			}
			point.CandidateStatusCounts[c.Status]++
			point.FailureTypeCounts[firstFailureType(c.SourceEvent.FailureType, c.FailureType)]++
			if failure := kbFailureType(c.SourceEvent); failure != "" {
				point.KBFailureTypeCounts[failure]++
			}
			point.VerifyResultCounts[dashboardVerifyResult(c.VerifyResult)]++
			point.DomainCounts[sourceBreakdownValue(c.SourceEvent.DomainID)]++
			point.SourceKindCounts[sourceBreakdownValue(c.SourceEvent.SourceKind)]++
			point.SourceNameCounts[sourceBreakdownValue(c.SourceEvent.SourceName)]++
		}
		points = append(points, point)
	}
	return points
}

func dashboardWindow(input DashboardInput) (time.Time, time.Time) {
	until := input.Until
	if until.IsZero() {
		until = input.Now
	}
	if until.IsZero() {
		until = time.Now()
	}
	window := input.Window
	if window <= 0 {
		window = 7 * 24 * time.Hour
	}
	since := input.Since
	if since.IsZero() {
		since = until.Add(-window)
	}
	if !since.Before(until) {
		since = until.Add(-window)
	}
	return since, until
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

// ShadowEvalMetrics 表示单个业务域的影子评测指标。
type ShadowEvalMetrics struct {
	DomainID         string                       `json:"domain_id"`
	SampleCount      int                          `json:"sample_count"`
	PassRate         float64                      `json:"pass_rate"`
	AvgSemanticScore float64                      `json:"avg_semantic_score"`
	SafetyFailures   int                          `json:"safety_failures"`
	ToolMisuses      int                          `json:"tool_misuses"`
	RecentAlerts     []agentquality.RollbackAlert `json:"recent_alerts"`
}

// BuildShadowEvalMetrics 从影子评测结果构建指标。
func BuildShadowEvalMetrics(results []agentquality.ShadowEvalResult) ShadowEvalMetrics {
	if len(results) == 0 {
		return ShadowEvalMetrics{}
	}

	var domainID string
	passCount := 0
	totalScore := 0
	safetyFailures := 0
	toolMisuses := 0

	for _, result := range results {
		if domainID == "" {
			domainID = result.DomainID
		}

		if result.Passed {
			passCount++
		}

		totalScore += result.JudgeVerdict.Score

		// 统计安全失败
		if result.JudgeVerdict.FailureType == agentquality.FailurePermission ||
			result.JudgeVerdict.FailureType == agentquality.FailureRuntime {
			safetyFailures++
		}

		// 统计工具误用
		if result.JudgeVerdict.FailureType == agentquality.FailureTool {
			toolMisuses++
		}
	}

	passRate := 0.0
	if len(results) > 0 {
		passRate = float64(passCount) / float64(len(results))
	}

	avgScore := 0.0
	if len(results) > 0 {
		avgScore = float64(totalScore) / float64(len(results))
	}

	return ShadowEvalMetrics{
		DomainID:         domainID,
		SampleCount:      len(results),
		PassRate:         passRate,
		AvgSemanticScore: avgScore,
		SafetyFailures:   safetyFailures,
		ToolMisuses:      toolMisuses,
		RecentAlerts:     []agentquality.RollbackAlert{}, // 由调用方填充
	}
}
