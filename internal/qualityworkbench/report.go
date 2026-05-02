package qualityworkbench

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/chef-guo/agents-hive/internal/agentquality"
)

type WeeklyReportInput struct {
	Since      time.Time
	Until      time.Time
	Clusters   []Cluster
	Candidates []agentquality.CandidateRecord
	EvalRuns   []BatchEvalRun
}

type WeeklyReport struct {
	Summary  WeeklyReportSummary `json:"summary"`
	Markdown string              `json:"markdown"`
}

type WeeklyReportRecord struct {
	ID        string              `json:"id"`
	WeekStart time.Time           `json:"week_start"`
	Title     string              `json:"title"`
	Summary   WeeklyReportSummary `json:"summary"`
	Markdown  string              `json:"markdown"`
	CreatedAt time.Time           `json:"created_at"`
	UpdatedAt time.Time           `json:"updated_at"`
}

type WeeklyReportSave struct {
	ID        string
	WeekStart time.Time
	Title     string
	Report    WeeklyReport
}

type WeeklyReportListFilter struct {
	Limit  int
	Offset int
}

type WeeklyReportStore interface {
	Save(input WeeklyReportSave) (WeeklyReportRecord, error)
	Get(id string) (WeeklyReportRecord, bool)
	List(filter WeeklyReportListFilter) []WeeklyReportRecord
}

type WeeklyReportSummary struct {
	OpenClusters     int `json:"open_clusters"`
	CandidateTotal   int `json:"candidate_total"`
	FailedEvalRuns   int `json:"failed_eval_runs"`
	RegressedRecords int `json:"regressed_records"`
}

func GenerateWeeklyReport(input WeeklyReportInput) WeeklyReport {
	summary := WeeklyReportSummary{CandidateTotal: len(input.Candidates)}
	for _, c := range input.Clusters {
		if c.OpenCount > 0 {
			summary.OpenClusters++
		}
	}
	for _, c := range input.Candidates {
		if c.Status == agentquality.CandidatePromotedRegressed || normalizeVerifyResult(c.VerifyResult) == "failed" {
			summary.RegressedRecords++
		}
	}
	for _, r := range input.EvalRuns {
		if r.Status == BatchEvalFailed {
			summary.FailedEvalRuns++
		}
	}

	var b strings.Builder
	fmt.Fprintf(&b, "# Quality Workbench Weekly Report\n\n")
	fmt.Fprintf(&b, "Window: %s to %s\n\n", input.Since.Format("2006-01-02"), input.Until.Format("2006-01-02"))
	fmt.Fprintf(&b, "## Summary\n\n")
	fmt.Fprintf(&b, "- Open clusters: %d\n", summary.OpenClusters)
	fmt.Fprintf(&b, "- Candidates: %d\n", summary.CandidateTotal)
	fmt.Fprintf(&b, "- Failed eval runs: %d\n", summary.FailedEvalRuns)
	fmt.Fprintf(&b, "- Regressed records: %d\n\n", summary.RegressedRecords)

	fmt.Fprintf(&b, "## Open Clusters\n\n")
	clusters := append([]Cluster(nil), input.Clusters...)
	sort.Slice(clusters, func(i, j int) bool {
		if clusters[i].OpenCount != clusters[j].OpenCount {
			return clusters[i].OpenCount > clusters[j].OpenCount
		}
		return clusters[i].LastSeen.After(clusters[j].LastSeen)
	})
	for _, c := range clusters {
		if c.OpenCount == 0 {
			continue
		}
		fmt.Fprintf(&b, "- %s: %d open, %d total, failure=%s\n", c.ID, c.OpenCount, c.Size, c.FailureType)
	}

	fmt.Fprintf(&b, "\n## Candidate Changes\n\n")
	for _, c := range input.Candidates {
		fmt.Fprintf(&b, "- %s: status=%s, failure=%s, verify=%s\n", c.ID, c.Status, c.FailureType, dashboardVerifyResult(c.VerifyResult))
	}

	fmt.Fprintf(&b, "\n## Eval Runs\n\n")
	for _, r := range input.EvalRuns {
		fmt.Fprintf(&b, "- %s: batch=%s, status=%s, pass=%d failed=%d unknown=%d\n", r.ID, r.BatchID, r.Status, r.Summary.Passed, r.Summary.Failed, r.Summary.Unknown)
	}

	fmt.Fprintf(&b, "\n## Regressions\n\n")
	for _, c := range input.Candidates {
		if c.Status == agentquality.CandidatePromotedRegressed || normalizeVerifyResult(c.VerifyResult) == "failed" {
			fmt.Fprintf(&b, "- %s: status=%s, failure=%s, verify=%s\n", c.ID, c.Status, c.FailureType, dashboardVerifyResult(c.VerifyResult))
		}
	}

	fmt.Fprintf(&b, "\n## Next Actions\n\n")
	if summary.OpenClusters == 0 && summary.RegressedRecords == 0 && summary.FailedEvalRuns == 0 {
		fmt.Fprintf(&b, "- No immediate action.\n")
	} else {
		fmt.Fprintf(&b, "- Review open clusters and regressed records before promotion.\n")
	}

	return WeeklyReport{Summary: summary, Markdown: b.String()}
}

type MemoryWeeklyReportStore struct {
	mu      sync.RWMutex
	now     func() time.Time
	reports map[string]WeeklyReportRecord
}

func NewMemoryWeeklyReportStore(now func() time.Time) *MemoryWeeklyReportStore {
	if now == nil {
		now = time.Now
	}
	return &MemoryWeeklyReportStore{now: now, reports: map[string]WeeklyReportRecord{}}
}

func (s *MemoryWeeklyReportStore) Save(input WeeklyReportSave) (WeeklyReportRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if strings.TrimSpace(input.ID) == "" {
		return WeeklyReportRecord{}, errors.New("id is required")
	}
	if input.WeekStart.IsZero() {
		return WeeklyReportRecord{}, errors.New("week_start is required")
	}
	title := strings.TrimSpace(input.Title)
	if title == "" {
		title = "Quality Workbench Weekly Report"
	}
	now := s.now()
	record := WeeklyReportRecord{
		ID:        input.ID,
		WeekStart: input.WeekStart,
		Title:     title,
		Summary:   input.Report.Summary,
		Markdown:  input.Report.Markdown,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if existing, ok := s.reports[input.ID]; ok {
		record.CreatedAt = existing.CreatedAt
	}
	s.reports[input.ID] = cloneWeeklyReportRecord(record)
	return cloneWeeklyReportRecord(record), nil
}

func (s *MemoryWeeklyReportStore) Get(id string) (WeeklyReportRecord, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	record, ok := s.reports[id]
	if !ok {
		return WeeklyReportRecord{}, false
	}
	return cloneWeeklyReportRecord(record), true
}

func (s *MemoryWeeklyReportStore) List(filter WeeklyReportListFilter) []WeeklyReportRecord {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]WeeklyReportRecord, 0, len(s.reports))
	for _, report := range s.reports {
		out = append(out, cloneWeeklyReportRecord(report))
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].WeekStart.Equal(out[j].WeekStart) {
			return out[i].ID > out[j].ID
		}
		return out[i].WeekStart.After(out[j].WeekStart)
	})
	return pageWeeklyReports(out, filter.Offset, filter.Limit)
}

func cloneWeeklyReportRecord(record WeeklyReportRecord) WeeklyReportRecord {
	return record
}

func pageWeeklyReports(reports []WeeklyReportRecord, offset, limit int) []WeeklyReportRecord {
	if offset < 0 {
		offset = 0
	}
	if offset >= len(reports) {
		return []WeeklyReportRecord{}
	}
	reports = reports[offset:]
	if limit <= 0 || limit >= len(reports) {
		return reports
	}
	return reports[:limit]
}
