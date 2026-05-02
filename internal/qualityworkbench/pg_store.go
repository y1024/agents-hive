package qualityworkbench

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PGReplayJobStore struct {
	pool *pgxpool.Pool
}

func NewPGReplayJobStore(pool *pgxpool.Pool) *PGReplayJobStore {
	return &PGReplayJobStore{pool: pool}
}

type PGGroupingRuleStore struct {
	pool *pgxpool.Pool
}

func NewPGGroupingRuleStore(pool *pgxpool.Pool) *PGGroupingRuleStore {
	return &PGGroupingRuleStore{pool: pool}
}

func (s *PGGroupingRuleStore) List() []GroupingRule {
	rows, err := s.pool.Query(context.Background(), groupingRuleSelectSQL+" ORDER BY priority ASC, id ASC")
	if err != nil {
		return []GroupingRule{}
	}
	defer rows.Close()
	out, err := scanGroupingRuleRows(rows)
	if err != nil {
		return []GroupingRule{}
	}
	return out
}

func (s *PGGroupingRuleStore) Get(id string) (GroupingRule, bool) {
	var rule GroupingRule
	row := s.pool.QueryRow(context.Background(), groupingRuleSelectSQL+" WHERE id=$1", strings.TrimSpace(id))
	if err := scanGroupingRule(row, &rule); err != nil {
		return GroupingRule{}, false
	}
	return cloneGroupingRule(rule), true
}

func (s *PGGroupingRuleStore) Upsert(rule GroupingRule) (GroupingRule, error) {
	if err := ValidateGroupingRule(rule); err != nil {
		return GroupingRule{}, err
	}
	ruleJSON, err := json.Marshal(rule)
	if err != nil {
		return GroupingRule{}, err
	}
	var saved GroupingRule
	row := s.pool.QueryRow(context.Background(), `
INSERT INTO agentquality_grouping_rules (id, name, priority, enabled, rule_json, created_by)
VALUES ($1,$2,$3,$4,$5,$6)
ON CONFLICT (id)
DO UPDATE SET name=$2, priority=$3, enabled=$4, rule_json=$5, created_by=$6, updated_at=NOW()
RETURNING id, name, priority, enabled, rule_json, created_by, created_at, updated_at`,
		rule.ID, rule.Name, rule.Priority, rule.Enabled, ruleJSON, rule.CreatedBy,
	)
	if err := scanGroupingRule(row, &saved); err != nil {
		return GroupingRule{}, err
	}
	return cloneGroupingRule(saved), nil
}

func (s *PGGroupingRuleStore) Delete(id string) error {
	tag, err := s.pool.Exec(context.Background(), `DELETE FROM agentquality_grouping_rules WHERE id=$1`, strings.TrimSpace(id))
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("grouping rule %s not found", id)
	}
	return nil
}

func (s *PGReplayJobStore) Create(input ReplayJobCreate) (ReplayJob, error) {
	if err := validateReplayJobCreate(input); err != nil {
		return ReplayJob{}, err
	}
	if input.MaxAttempt <= 0 {
		input.MaxAttempt = 1
	}
	targetIDs, err := json.Marshal(input.TargetIDs)
	if err != nil {
		return ReplayJob{}, err
	}
	var job ReplayJob
	row := s.pool.QueryRow(context.Background(), `
INSERT INTO qualityworkbench_replay_jobs (batch_id, kind, target_ids, status, max_attempt, attempt)
VALUES ($1,$2,$3,$4,$5,0)
RETURNING id, batch_id, kind, target_ids, status, max_attempt, attempt, result, error, created_at, updated_at`,
		input.BatchID, input.Kind, targetIDs, ReplayJobQueued, input.MaxAttempt,
	)
	if err := scanReplayJob(row, &job); err != nil {
		return ReplayJob{}, err
	}
	return cloneReplayJob(job), nil
}

func (s *PGReplayJobStore) Get(id string) (ReplayJob, bool) {
	var job ReplayJob
	row := s.pool.QueryRow(context.Background(), replayJobSelectSQL+" WHERE id=$1", strings.TrimSpace(id))
	if err := scanReplayJob(row, &job); err != nil {
		return ReplayJob{}, false
	}
	return cloneReplayJob(job), true
}

func (s *PGReplayJobStore) List(filter ReplayJobListFilter) []ReplayJob {
	limit, offset := normalizeWorkbenchPaging(filter.Limit, filter.Offset)
	where, args := replayJobWhere(filter)
	args = append(args, limit, offset)
	rows, err := s.pool.Query(context.Background(), replayJobSelectSQL+where+fmt.Sprintf(" ORDER BY created_at DESC, id DESC LIMIT $%d OFFSET $%d", len(args)-1, len(args)), args...)
	if err != nil {
		return []ReplayJob{}
	}
	defer rows.Close()
	out, err := scanReplayJobRows(rows)
	if err != nil {
		return []ReplayJob{}
	}
	return out
}

func (s *PGReplayJobStore) Cancel(id string) (ReplayJob, error) {
	current, ok := s.Get(id)
	if !ok {
		return ReplayJob{}, fmt.Errorf("replay job %s not found", id)
	}
	if err := current.Transition(ReplayJobCancelled, current.UpdatedAt); err != nil {
		return ReplayJob{}, err
	}
	var job ReplayJob
	row := s.pool.QueryRow(context.Background(), `
UPDATE qualityworkbench_replay_jobs
SET status=$2, updated_at=NOW()
WHERE id=$1 AND status=$3
RETURNING id, batch_id, kind, target_ids, status, max_attempt, attempt, result, error, created_at, updated_at`,
		id, ReplayJobCancelled, current.Status,
	)
	if err := scanReplayJob(row, &job); err != nil {
		return ReplayJob{}, err
	}
	return cloneReplayJob(job), nil
}

func (s *PGReplayJobStore) MarkRunning(id string) (ReplayJob, error) {
	current, ok := s.Get(id)
	if !ok {
		return ReplayJob{}, fmt.Errorf("replay job %s not found", id)
	}
	if err := current.Transition(ReplayJobRunning, current.UpdatedAt); err != nil {
		return ReplayJob{}, err
	}
	var job ReplayJob
	row := s.pool.QueryRow(context.Background(), `
UPDATE qualityworkbench_replay_jobs
SET status=$2, attempt=attempt+1, error='', updated_at=NOW()
WHERE id=$1 AND status=$3
RETURNING id, batch_id, kind, target_ids, status, max_attempt, attempt, result, error, created_at, updated_at`,
		id, ReplayJobRunning, current.Status,
	)
	if err := scanReplayJob(row, &job); err != nil {
		return ReplayJob{}, err
	}
	return cloneReplayJob(job), nil
}

func (s *PGReplayJobStore) Finish(id string, status ReplayJobStatus, result ReplayJobResult, errorMessage string) (ReplayJob, error) {
	current, ok := s.Get(id)
	if !ok {
		return ReplayJob{}, fmt.Errorf("replay job %s not found", id)
	}
	if status != ReplayJobSucceeded && status != ReplayJobFailed {
		return ReplayJob{}, fmt.Errorf("invalid replay finish status %s", status)
	}
	if err := current.Transition(status, current.UpdatedAt); err != nil {
		return ReplayJob{}, err
	}
	resultJSON, err := json.Marshal(result)
	if err != nil {
		return ReplayJob{}, err
	}
	var job ReplayJob
	row := s.pool.QueryRow(context.Background(), `
UPDATE qualityworkbench_replay_jobs
SET status=$2, result=$3, error=$4, updated_at=NOW()
WHERE id=$1 AND status=$5
RETURNING id, batch_id, kind, target_ids, status, max_attempt, attempt, result, error, created_at, updated_at`,
		id, status, resultJSON, strings.TrimSpace(errorMessage), current.Status,
	)
	if err := scanReplayJob(row, &job); err != nil {
		return ReplayJob{}, err
	}
	return cloneReplayJob(job), nil
}

type PGBatchEvalRunStore struct {
	pool *pgxpool.Pool
}

func NewPGBatchEvalRunStore(pool *pgxpool.Pool) *PGBatchEvalRunStore {
	return &PGBatchEvalRunStore{pool: pool}
}

func (s *PGBatchEvalRunStore) Start(input BatchEvalStart) (BatchEvalRun, error) {
	if strings.TrimSpace(input.BatchID) == "" {
		return BatchEvalRun{}, errors.New("batch_id is required")
	}
	if input.Kind == "" {
		return BatchEvalRun{}, errors.New("kind is required")
	}
	if err := ValidateBatchEvalKind(input.Kind); err != nil {
		return BatchEvalRun{}, err
	}
	summary, diff, caseResults := summarizeBatchEvalWithGolden(input)
	status := BatchEvalSucceeded
	if summary.Total == 0 || summary.Failed > 0 || summary.Unknown > 0 {
		status = BatchEvalFailed
	}
	summaryJSON, err := json.Marshal(summary)
	if err != nil {
		return BatchEvalRun{}, err
	}
	diffJSON, err := json.Marshal(diff)
	if err != nil {
		return BatchEvalRun{}, err
	}
	caseResultsJSON, err := json.Marshal(caseResults)
	if err != nil {
		return BatchEvalRun{}, err
	}
	var run BatchEvalRun
	row := s.pool.QueryRow(context.Background(), `
INSERT INTO qualityworkbench_batch_eval_runs (batch_id, kind, status, summary, diff, case_results)
VALUES ($1,$2,$3,$4,$5,$6)
RETURNING id, batch_id, kind, status, summary, diff, case_results, created_at, updated_at`,
		input.BatchID, input.Kind, status, summaryJSON, diffJSON, caseResultsJSON,
	)
	if err := scanBatchEvalRun(row, &run); err != nil {
		return BatchEvalRun{}, err
	}
	return cloneBatchEvalRun(run), nil
}

func (s *PGBatchEvalRunStore) Get(id string) (BatchEvalRun, bool) {
	var run BatchEvalRun
	row := s.pool.QueryRow(context.Background(), batchEvalRunSelectSQL+" WHERE id=$1", strings.TrimSpace(id))
	if err := scanBatchEvalRun(row, &run); err != nil {
		return BatchEvalRun{}, false
	}
	return cloneBatchEvalRun(run), true
}

func (s *PGBatchEvalRunStore) List(filter BatchEvalRunListFilter) []BatchEvalRun {
	limit, offset := normalizeWorkbenchPaging(filter.Limit, filter.Offset)
	where, args := batchEvalRunWhere(filter)
	args = append(args, limit, offset)
	rows, err := s.pool.Query(context.Background(), batchEvalRunSelectSQL+where+fmt.Sprintf(" ORDER BY created_at DESC, id DESC LIMIT $%d OFFSET $%d", len(args)-1, len(args)), args...)
	if err != nil {
		return []BatchEvalRun{}
	}
	defer rows.Close()
	out, err := scanBatchEvalRunRows(rows)
	if err != nil {
		return []BatchEvalRun{}
	}
	return out
}

type PGWeeklyReportStore struct {
	pool *pgxpool.Pool
}

func NewPGWeeklyReportStore(pool *pgxpool.Pool) *PGWeeklyReportStore {
	return &PGWeeklyReportStore{pool: pool}
}

func (s *PGWeeklyReportStore) Save(input WeeklyReportSave) (WeeklyReportRecord, error) {
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
	summaryJSON, err := json.Marshal(input.Report.Summary)
	if err != nil {
		return WeeklyReportRecord{}, err
	}
	var record WeeklyReportRecord
	row := s.pool.QueryRow(context.Background(), `
INSERT INTO qualityworkbench_weekly_reports (id, week_start, title, summary, markdown)
VALUES ($1,$2,$3,$4,$5)
ON CONFLICT (id)
DO UPDATE SET week_start=$2, title=$3, summary=$4, markdown=$5, updated_at=NOW()
RETURNING id, week_start, title, summary, markdown, created_at, updated_at`,
		input.ID, input.WeekStart, title, summaryJSON, input.Report.Markdown,
	)
	if err := scanWeeklyReportRecord(row, &record); err != nil {
		return WeeklyReportRecord{}, err
	}
	return cloneWeeklyReportRecord(record), nil
}

func (s *PGWeeklyReportStore) Get(id string) (WeeklyReportRecord, bool) {
	var record WeeklyReportRecord
	row := s.pool.QueryRow(context.Background(), weeklyReportSelectSQL+" WHERE id=$1", strings.TrimSpace(id))
	if err := scanWeeklyReportRecord(row, &record); err != nil {
		return WeeklyReportRecord{}, false
	}
	return cloneWeeklyReportRecord(record), true
}

func (s *PGWeeklyReportStore) List(filter WeeklyReportListFilter) []WeeklyReportRecord {
	limit, offset := normalizeWorkbenchPaging(filter.Limit, filter.Offset)
	rows, err := s.pool.Query(context.Background(), weeklyReportSelectSQL+fmt.Sprintf(" ORDER BY week_start DESC, id DESC LIMIT $1 OFFSET $2"), limit, offset)
	if err != nil {
		return []WeeklyReportRecord{}
	}
	defer rows.Close()
	out, err := scanWeeklyReportRows(rows)
	if err != nil {
		return []WeeklyReportRecord{}
	}
	return out
}

type pgScanner interface {
	Scan(dest ...any) error
}

const replayJobSelectSQL = `SELECT id, batch_id, kind, target_ids, status, max_attempt, attempt, result, error, created_at, updated_at FROM qualityworkbench_replay_jobs`

func scanReplayJob(row pgScanner, job *ReplayJob) error {
	var targetIDs, resultJSON []byte
	if err := row.Scan(&job.ID, &job.BatchID, &job.Kind, &targetIDs, &job.Status, &job.MaxAttempt, &job.Attempt, &resultJSON, &job.Error, &job.CreatedAt, &job.UpdatedAt); err != nil {
		return err
	}
	if len(targetIDs) > 0 {
		if err := json.Unmarshal(targetIDs, &job.TargetIDs); err != nil {
			return err
		}
	}
	if len(resultJSON) > 0 {
		if err := json.Unmarshal(resultJSON, &job.Result); err != nil {
			return err
		}
	}
	return nil
}

func scanReplayJobRows(rows pgx.Rows) ([]ReplayJob, error) {
	var out []ReplayJob
	for rows.Next() {
		var job ReplayJob
		if err := scanReplayJob(rows, &job); err != nil {
			return nil, err
		}
		out = append(out, cloneReplayJob(job))
	}
	return out, rows.Err()
}

func replayJobWhere(filter ReplayJobListFilter) (string, []any) {
	var clauses []string
	var args []any
	if filter.BatchID != "" {
		clauses = append(clauses, fmt.Sprintf("batch_id=$%d", len(args)+1))
		args = append(args, filter.BatchID)
	}
	if filter.Kind != "" {
		clauses = append(clauses, fmt.Sprintf("kind=$%d", len(args)+1))
		args = append(args, filter.Kind)
	}
	if filter.Status != "" {
		clauses = append(clauses, fmt.Sprintf("status=$%d", len(args)+1))
		args = append(args, filter.Status)
	}
	if len(clauses) == 0 {
		return "", args
	}
	return " WHERE " + strings.Join(clauses, " AND "), args
}

const batchEvalRunSelectSQL = `SELECT id, batch_id, kind, status, summary, diff, case_results, created_at, updated_at FROM qualityworkbench_batch_eval_runs`

func scanBatchEvalRun(row pgScanner, run *BatchEvalRun) error {
	var summaryJSON, diffJSON, caseResultsJSON []byte
	if err := row.Scan(&run.ID, &run.BatchID, &run.Kind, &run.Status, &summaryJSON, &diffJSON, &caseResultsJSON, &run.CreatedAt, &run.UpdatedAt); err != nil {
		return err
	}
	if len(summaryJSON) > 0 {
		if err := json.Unmarshal(summaryJSON, &run.Summary); err != nil {
			return err
		}
	}
	if len(diffJSON) > 0 {
		if err := json.Unmarshal(diffJSON, &run.Diff); err != nil {
			return err
		}
	}
	if len(caseResultsJSON) > 0 {
		if err := json.Unmarshal(caseResultsJSON, &run.CaseResults); err != nil {
			return err
		}
	}
	return nil
}

func scanBatchEvalRunRows(rows pgx.Rows) ([]BatchEvalRun, error) {
	var out []BatchEvalRun
	for rows.Next() {
		var run BatchEvalRun
		if err := scanBatchEvalRun(rows, &run); err != nil {
			return nil, err
		}
		out = append(out, cloneBatchEvalRun(run))
	}
	return out, rows.Err()
}

func batchEvalRunWhere(filter BatchEvalRunListFilter) (string, []any) {
	var clauses []string
	var args []any
	if filter.BatchID != "" {
		clauses = append(clauses, fmt.Sprintf("batch_id=$%d", len(args)+1))
		args = append(args, filter.BatchID)
	}
	if filter.Kind != "" {
		clauses = append(clauses, fmt.Sprintf("kind=$%d", len(args)+1))
		args = append(args, filter.Kind)
	}
	if filter.Status != "" {
		clauses = append(clauses, fmt.Sprintf("status=$%d", len(args)+1))
		args = append(args, filter.Status)
	}
	if len(clauses) == 0 {
		return "", args
	}
	return " WHERE " + strings.Join(clauses, " AND "), args
}

const weeklyReportSelectSQL = `SELECT id, week_start, title, summary, markdown, created_at, updated_at FROM qualityworkbench_weekly_reports`

const groupingRuleSelectSQL = `SELECT id, name, priority, enabled, rule_json, created_by, created_at, updated_at FROM agentquality_grouping_rules`

func scanGroupingRule(row pgScanner, rule *GroupingRule) error {
	var ruleJSON []byte
	var createdBy string
	var createdAt, updatedAt time.Time
	if err := row.Scan(&rule.ID, &rule.Name, &rule.Priority, &rule.Enabled, &ruleJSON, &createdBy, &createdAt, &updatedAt); err != nil {
		return err
	}
	if len(ruleJSON) > 0 {
		if err := json.Unmarshal(ruleJSON, rule); err != nil {
			return err
		}
	}
	if rule.CreatedBy == "" {
		rule.CreatedBy = createdBy
	}
	rule.CreatedAt = createdAt
	rule.UpdatedAt = updatedAt
	return nil
}

func scanGroupingRuleRows(rows pgx.Rows) ([]GroupingRule, error) {
	var out []GroupingRule
	for rows.Next() {
		var rule GroupingRule
		if err := scanGroupingRule(rows, &rule); err != nil {
			return nil, err
		}
		out = append(out, cloneGroupingRule(rule))
	}
	return out, rows.Err()
}

func scanWeeklyReportRecord(row pgScanner, record *WeeklyReportRecord) error {
	var summaryJSON []byte
	if err := row.Scan(&record.ID, &record.WeekStart, &record.Title, &summaryJSON, &record.Markdown, &record.CreatedAt, &record.UpdatedAt); err != nil {
		return err
	}
	if len(summaryJSON) > 0 {
		if err := json.Unmarshal(summaryJSON, &record.Summary); err != nil {
			return err
		}
	}
	return nil
}

func scanWeeklyReportRows(rows pgx.Rows) ([]WeeklyReportRecord, error) {
	var out []WeeklyReportRecord
	for rows.Next() {
		var record WeeklyReportRecord
		if err := scanWeeklyReportRecord(rows, &record); err != nil {
			return nil, err
		}
		out = append(out, cloneWeeklyReportRecord(record))
	}
	return out, rows.Err()
}

func normalizeWorkbenchPaging(limit, offset int) (int, int) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}
	return limit, offset
}

func validateReplayJobCreate(input ReplayJobCreate) error {
	if strings.TrimSpace(input.BatchID) == "" {
		return errors.New("batch_id is required")
	}
	if input.Kind == "" {
		return errors.New("kind is required")
	}
	if len(input.TargetIDs) == 0 {
		return errors.New("target_ids is required")
	}
	return nil
}
