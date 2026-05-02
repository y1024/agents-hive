package qualityworkbench

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

type ReplayJobKind string

const (
	ReplayJobKindCandidate ReplayJobKind = "candidate"
	ReplayJobKindCluster   ReplayJobKind = "cluster"
)

type ReplayJobStatus string

const (
	ReplayJobQueued    ReplayJobStatus = "queued"
	ReplayJobRunning   ReplayJobStatus = "running"
	ReplayJobSucceeded ReplayJobStatus = "succeeded"
	ReplayJobFailed    ReplayJobStatus = "failed"
	ReplayJobCancelled ReplayJobStatus = "cancelled"
)

type ReplayJob struct {
	ID         string          `json:"id"`
	BatchID    string          `json:"batch_id"`
	Kind       ReplayJobKind   `json:"kind"`
	TargetIDs  []string        `json:"target_ids"`
	Status     ReplayJobStatus `json:"status"`
	MaxAttempt int             `json:"max_attempt"`
	Attempt    int             `json:"attempt"`
	Result     ReplayJobResult `json:"result,omitempty"`
	Error      string          `json:"error,omitempty"`
	CreatedAt  time.Time       `json:"created_at"`
	UpdatedAt  time.Time       `json:"updated_at"`
}

type ReplayJobResult struct {
	Total   int      `json:"total"`
	Passed  int      `json:"passed"`
	Failed  int      `json:"failed"`
	Unknown int      `json:"unknown"`
	CaseIDs []string `json:"case_ids,omitempty"`
	Reasons []string `json:"reasons,omitempty"`
}

type ReplayJobCreate struct {
	BatchID    string
	Kind       ReplayJobKind
	TargetIDs  []string
	MaxAttempt int
}

type ReplayJobListFilter struct {
	BatchID string
	Kind    ReplayJobKind
	Status  ReplayJobStatus
	Limit   int
	Offset  int
}

type ReplayJobStore interface {
	Create(input ReplayJobCreate) (ReplayJob, error)
	Get(id string) (ReplayJob, bool)
	List(filter ReplayJobListFilter) []ReplayJob
	MarkRunning(id string) (ReplayJob, error)
	Finish(id string, status ReplayJobStatus, result ReplayJobResult, errorMessage string) (ReplayJob, error)
	Cancel(id string) (ReplayJob, error)
}

type MemoryReplayJobStore struct {
	mu   sync.RWMutex
	now  func() time.Time
	seq  int
	jobs map[string]ReplayJob
}

func NewMemoryReplayJobStore(now func() time.Time) *MemoryReplayJobStore {
	if now == nil {
		now = time.Now
	}
	return &MemoryReplayJobStore{now: now, jobs: map[string]ReplayJob{}}
}

func (s *MemoryReplayJobStore) Create(input ReplayJobCreate) (ReplayJob, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if strings.TrimSpace(input.BatchID) == "" {
		return ReplayJob{}, errors.New("batch_id is required")
	}
	if input.Kind == "" {
		return ReplayJob{}, errors.New("kind is required")
	}
	if len(input.TargetIDs) == 0 {
		return ReplayJob{}, errors.New("target_ids is required")
	}
	if input.MaxAttempt <= 0 {
		input.MaxAttempt = 1
	}
	s.seq++
	now := s.now()
	job := ReplayJob{
		ID:         fmt.Sprintf("replay_%06d", s.seq),
		BatchID:    input.BatchID,
		Kind:       input.Kind,
		TargetIDs:  append([]string(nil), input.TargetIDs...),
		Status:     ReplayJobQueued,
		MaxAttempt: input.MaxAttempt,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	s.jobs[job.ID] = job
	return cloneReplayJob(job), nil
}

func (s *MemoryReplayJobStore) Get(id string) (ReplayJob, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	job, ok := s.jobs[id]
	if !ok {
		return ReplayJob{}, false
	}
	return cloneReplayJob(job), true
}

func (s *MemoryReplayJobStore) List(filter ReplayJobListFilter) []ReplayJob {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]ReplayJob, 0, len(s.jobs))
	for _, job := range s.jobs {
		if filter.BatchID != "" && job.BatchID != filter.BatchID {
			continue
		}
		if filter.Kind != "" && job.Kind != filter.Kind {
			continue
		}
		if filter.Status != "" && job.Status != filter.Status {
			continue
		}
		out = append(out, cloneReplayJob(job))
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ID > out[j].ID
		}
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	return pageReplayJobs(out, filter.Offset, filter.Limit)
}

func (s *MemoryReplayJobStore) Cancel(id string) (ReplayJob, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	job, ok := s.jobs[id]
	if !ok {
		return ReplayJob{}, fmt.Errorf("replay job %s not found", id)
	}
	if err := job.Transition(ReplayJobCancelled, s.now()); err != nil {
		return ReplayJob{}, err
	}
	s.jobs[id] = job
	return cloneReplayJob(job), nil
}

func (s *MemoryReplayJobStore) MarkRunning(id string) (ReplayJob, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	job, ok := s.jobs[id]
	if !ok {
		return ReplayJob{}, fmt.Errorf("replay job %s not found", id)
	}
	if err := job.Transition(ReplayJobRunning, s.now()); err != nil {
		return ReplayJob{}, err
	}
	job.Error = ""
	s.jobs[id] = job
	return cloneReplayJob(job), nil
}

func (s *MemoryReplayJobStore) Finish(id string, status ReplayJobStatus, result ReplayJobResult, errorMessage string) (ReplayJob, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	job, ok := s.jobs[id]
	if !ok {
		return ReplayJob{}, fmt.Errorf("replay job %s not found", id)
	}
	if status != ReplayJobSucceeded && status != ReplayJobFailed {
		return ReplayJob{}, fmt.Errorf("invalid replay finish status %s", status)
	}
	if err := job.Transition(status, s.now()); err != nil {
		return ReplayJob{}, err
	}
	job.Result = cloneReplayResult(result)
	job.Error = strings.TrimSpace(errorMessage)
	s.jobs[id] = job
	return cloneReplayJob(job), nil
}

func (j *ReplayJob) Transition(next ReplayJobStatus, at time.Time) error {
	if j.Status == ReplayJobSucceeded || j.Status == ReplayJobFailed || j.Status == ReplayJobCancelled {
		return fmt.Errorf("replay job %s is terminal", j.ID)
	}
	valid := false
	switch j.Status {
	case ReplayJobQueued:
		valid = next == ReplayJobRunning || next == ReplayJobCancelled
	case ReplayJobRunning:
		valid = next == ReplayJobSucceeded || next == ReplayJobFailed || next == ReplayJobCancelled
	}
	if !valid {
		return fmt.Errorf("invalid replay transition %s -> %s", j.Status, next)
	}
	if next == ReplayJobRunning {
		j.Attempt++
		if j.MaxAttempt > 0 && j.Attempt > j.MaxAttempt {
			return fmt.Errorf("replay job %s exceeded max_attempt", j.ID)
		}
	}
	j.Status = next
	j.UpdatedAt = at
	return nil
}

func cloneReplayJob(job ReplayJob) ReplayJob {
	job.TargetIDs = append([]string(nil), job.TargetIDs...)
	job.Result = cloneReplayResult(job.Result)
	return job
}

func cloneReplayResult(result ReplayJobResult) ReplayJobResult {
	result.CaseIDs = append([]string(nil), result.CaseIDs...)
	result.Reasons = append([]string(nil), result.Reasons...)
	return result
}

func pageReplayJobs(jobs []ReplayJob, offset, limit int) []ReplayJob {
	if offset < 0 {
		offset = 0
	}
	if offset >= len(jobs) {
		return []ReplayJob{}
	}
	jobs = jobs[offset:]
	if limit <= 0 || limit >= len(jobs) {
		return jobs
	}
	return jobs[:limit]
}
