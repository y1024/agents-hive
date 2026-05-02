package qualityworkbench

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/chef-guo/agents-hive/internal/agentquality"
)

func TestGenerateWeeklyReportIncludesSummaryAndKeyMarkdownContent(t *testing.T) {
	since := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	until := since.AddDate(0, 0, 7)
	candidates := []agentquality.CandidateRecord{
		qualityWorkbenchCandidate("candidate-open", agentquality.CandidateNew, agentquality.FailureTool, "", since.Add(2*time.Hour)),
		qualityWorkbenchCandidate("candidate-regressed", agentquality.CandidatePromotedRegressed, agentquality.FailureRuntime, "failed", since.Add(3*time.Hour)),
	}
	clusters := []Cluster{
		{ID: "cl_open", FailureType: agentquality.FailureTool, Size: 3, OpenCount: 2, CandidateIDs: []string{"candidate-open"}, LastSeen: since.Add(2 * time.Hour)},
	}
	evalRuns := []BatchEvalRun{
		{ID: "eval-1", BatchID: "batch-1", Status: BatchEvalFailed, Summary: BatchEvalSummary{Total: 2, Passed: 1, Failed: 1}},
	}

	report := GenerateWeeklyReport(WeeklyReportInput{
		Since:      since,
		Until:      until,
		Clusters:   clusters,
		Candidates: candidates,
		EvalRuns:   evalRuns,
	})

	assert.Equal(t, 1, report.Summary.OpenClusters)
	assert.Equal(t, 2, report.Summary.CandidateTotal)
	assert.Equal(t, 1, report.Summary.FailedEvalRuns)
	assert.Contains(t, report.Markdown, "# Quality Workbench Weekly Report")
	assert.Contains(t, report.Markdown, "2026-04-01")
	assert.Contains(t, report.Markdown, "cl_open")
	assert.Contains(t, report.Markdown, "candidate-regressed")
	assert.Contains(t, report.Markdown, "eval-1")
}

func TestWeeklyReportStore_SaveListAndGet(t *testing.T) {
	now := time.Date(2026, 4, 8, 12, 0, 0, 0, time.UTC)
	store := NewMemoryWeeklyReportStore(func() time.Time { return now })
	report := WeeklyReport{
		Summary:  WeeklyReportSummary{OpenClusters: 1, CandidateTotal: 2},
		Markdown: "# report",
	}

	saved, err := store.Save(WeeklyReportSave{
		ID:        "report_20260401",
		WeekStart: time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC),
		Report:    report,
	})

	require.NoError(t, err)
	assert.Equal(t, "report_20260401", saved.ID)
	assert.Equal(t, now, saved.CreatedAt)
	assert.Equal(t, report.Summary, saved.Summary)

	got, ok := store.Get(saved.ID)
	require.True(t, ok)
	assert.Equal(t, saved.ID, got.ID)

	list := store.List(WeeklyReportListFilter{Limit: 10})
	require.Len(t, list, 1)
	assert.Equal(t, saved.ID, list[0].ID)
}

func TestBuildDashboardSnapshotAggregatesWindowedCounts(t *testing.T) {
	now := time.Date(2026, 4, 8, 12, 0, 0, 0, time.UTC)
	candidates := []agentquality.CandidateRecord{
		qualityWorkbenchCandidate("candidate-new", agentquality.CandidateNew, agentquality.FailureTool, "", now.Add(-time.Hour)),
		qualityWorkbenchCandidate("candidate-verified", agentquality.CandidatePromotedVerified, agentquality.FailureRuntime, "passed", now.Add(-2*time.Hour)),
		qualityWorkbenchCandidate("candidate-old", agentquality.CandidateRejected, agentquality.FailurePermission, "failed", now.Add(-10*24*time.Hour)),
	}
	clusters := []Cluster{
		{ID: "cl_recent", OpenCount: 2, LastSeen: now.Add(-time.Hour)},
		{ID: "cl_closed", OpenCount: 0, LastSeen: now.Add(-time.Hour)},
		{ID: "cl_old", OpenCount: 5, LastSeen: now.Add(-10 * 24 * time.Hour)},
	}

	snapshot := BuildDashboardSnapshot(DashboardInput{
		Now:        now,
		Window:     7 * 24 * time.Hour,
		Clusters:   clusters,
		Candidates: candidates,
	})

	assert.Equal(t, 1, snapshot.OpenClusters)
	assert.Equal(t, 1, snapshot.CandidateStatusCounts[agentquality.CandidateNew])
	assert.Equal(t, 1, snapshot.CandidateStatusCounts[agentquality.CandidatePromotedVerified])
	assert.Equal(t, 1, snapshot.FailureTypeCounts[agentquality.FailureTool])
	assert.Equal(t, 1, snapshot.FailureTypeCounts[agentquality.FailureRuntime])
	assert.Equal(t, 1, snapshot.VerifyResultCounts["passed"])
	assert.Equal(t, 1, snapshot.VerifyResultCounts["unknown"])
}

func TestBuildDashboardSeriesBucketsByDay(t *testing.T) {
	now := time.Date(2026, 4, 8, 12, 0, 0, 0, time.UTC)
	candidates := []agentquality.CandidateRecord{
		qualityWorkbenchCandidate("candidate-1", agentquality.CandidateNew, agentquality.FailureTool, "", now.Add(-time.Hour)),
		qualityWorkbenchCandidate("candidate-2", agentquality.CandidatePromotedRegressed, agentquality.FailureRuntime, "failed", now.Add(-25*time.Hour)),
	}

	series := BuildDashboardSeries(DashboardInput{
		Now:        now,
		Window:     48 * time.Hour,
		Candidates: candidates,
	}, 24*time.Hour)

	require.Len(t, series, 2)
	assert.Equal(t, 1, series[0].CandidateStatusCounts[agentquality.CandidatePromotedRegressed])
	assert.Equal(t, 1, series[0].VerifyResultCounts["failed"])
	assert.Equal(t, 1, series[1].CandidateStatusCounts[agentquality.CandidateNew])
	assert.Equal(t, 1, series[1].VerifyResultCounts["unknown"])
}

func qualityWorkbenchCandidate(id string, status agentquality.CandidateStatus, failureType agentquality.FailureType, verifyResult string, ts time.Time) agentquality.CandidateRecord {
	return agentquality.CandidateRecord{
		ID:           id,
		Status:       status,
		FailureType:  failureType,
		VerifyResult: verifyResult,
		CreatedAt:    ts,
		UpdatedAt:    ts,
		SourceEvent: agentquality.Event{
			FailureType: failureType,
			FinalStatus: agentquality.StatusFail,
		},
	}
}
