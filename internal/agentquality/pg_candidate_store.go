package agentquality

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
)

type PGCandidateStore struct {
	pool   *pgxpool.Pool
	logger *zap.Logger
}

func NewPGCandidateStore(pool *pgxpool.Pool, logger *zap.Logger) *PGCandidateStore {
	return &PGCandidateStore{pool: pool, logger: logger}
}

func (s *PGCandidateStore) UpsertCandidate(ctx context.Context, rec CandidateRecord) (*CandidateRecord, error) {
	if s == nil || s.pool == nil {
		return nil, fmt.Errorf("candidate store not configured")
	}
	if err := normalizeCandidateRecord(&rec); err != nil {
		return nil, err
	}
	return s.insertCandidate(ctx, rec)
}

func (s *PGCandidateStore) ListCandidates(ctx context.Context, filter CandidateFilter) ([]CandidateRecord, int, error) {
	if s == nil || s.pool == nil {
		return nil, 0, fmt.Errorf("candidate store not configured")
	}
	limit := filter.Limit
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	offset := filter.Offset
	if offset < 0 {
		offset = 0
	}

	where, args := candidateWhere(filter)
	countSQL := "SELECT COUNT(*) FROM agentquality_candidates" + where
	var total int
	if err := s.pool.QueryRow(ctx, countSQL, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	args = append(args, limit, offset)
	querySQL := candidateSelectSQL + where + fmt.Sprintf(" ORDER BY created_at DESC LIMIT $%d OFFSET $%d", len(args)-1, len(args))
	rows, err := s.pool.Query(ctx, querySQL, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	records, err := scanCandidateRows(rows)
	if err != nil {
		return nil, 0, err
	}
	return records, total, nil
}

func (s *PGCandidateStore) GetCandidate(ctx context.Context, id string) (*CandidateRecord, bool, error) {
	if s == nil || s.pool == nil {
		return nil, false, fmt.Errorf("candidate store not configured")
	}
	var rec CandidateRecord
	row := s.pool.QueryRow(ctx, candidateSelectSQL+" WHERE id=$1", id)
	if err := scanCandidate(row, &rec); err != nil {
		if err == pgx.ErrNoRows {
			return nil, false, nil
		}
		return nil, false, err
	}
	return &rec, true, nil
}

func (s *PGCandidateStore) UpdateCandidateStatus(ctx context.Context, id string, status CandidateStatus, reviewer, note, promotedCaseID string) error {
	if s == nil || s.pool == nil {
		return fmt.Errorf("candidate store not configured")
	}
	if err := ValidateCandidateStatus(status); err != nil {
		return err
	}
	if strings.TrimSpace(id) == "" {
		return fmt.Errorf("candidate id required")
	}
	if status == CandidatePromoted && strings.TrimSpace(promotedCaseID) == "" {
		return fmt.Errorf("promoted candidate requires promoted_case_id")
	}
	current, ok, err := s.GetCandidate(ctx, id)
	if err != nil {
		return err
	}
	if !ok {
		return pgx.ErrNoRows
	}
	if err := ValidateCandidateTransition(current.Status, status); err != nil {
		return err
	}

	tag, err := s.pool.Exec(ctx, `
UPDATE agentquality_candidates
SET status=$2,
	reviewed_by=$3,
	review_note=$4,
	promoted_case_id=$5,
	reviewed_at=NOW(),
	updated_at=NOW()
WHERE id=$1 AND status=$6`,
		id, status, reviewer, note, promotedCaseID, current.Status,
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

func (s *PGCandidateStore) insertCandidate(ctx context.Context, rec CandidateRecord) (*CandidateRecord, error) {
	current := rec
	for attempt := 0; attempt < 3; attempt++ {
		var out CandidateRecord
		row := s.pool.QueryRow(ctx, `
INSERT INTO agentquality_candidates
	(id, status, route, session_id, replay_ref, input, case_json, failure_type, risk, fingerprint, source_event, suggestions_json, created_by, cluster_id, verify_result, created_at, updated_at, last_verified_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18)
ON CONFLICT (fingerprint) WHERE status IN ('new', 'reviewing', 'approved')
DO UPDATE SET updated_at = agentquality_candidates.updated_at
RETURNING id, status, route, session_id, replay_ref, input, case_json, failure_type, risk, fingerprint, source_event,
	suggestions_json, review_note, created_by, reviewed_by, promoted_case_id, cluster_id, verify_result, created_at, updated_at, reviewed_at, last_verified_at`,
			current.ID, current.Status, current.Route, current.SessionID, current.ReplayRef, current.Input,
			mustJSON(current.Case), current.FailureType, current.Risk, current.Fingerprint, mustJSON(current.SourceEvent),
			mustJSON(current.Suggestions), current.CreatedBy, current.ClusterID, mustJSONRaw(current.VerifyResult), current.CreatedAt, current.UpdatedAt, current.LastVerifiedAt,
		)
		err := scanCandidate(row, &out)
		if err == nil {
			return &out, nil
		}
		if !isDuplicatePrimaryKey(err) {
			return nil, err
		}
		current.ID = rec.ID + "-" + randomSuffix()
		current.Case.ID = current.ID
	}
	return nil, fmt.Errorf("candidate id conflict after retries")
}

func normalizeCandidateRecord(rec *CandidateRecord) error {
	if strings.TrimSpace(rec.Input) == "" {
		return fmt.Errorf("candidate input required")
	}
	if rec.Status == "" {
		rec.Status = CandidateNew
	}
	if err := ValidateCandidateStatus(rec.Status); err != nil {
		return err
	}
	if rec.Fingerprint == "" {
		rec.Fingerprint = CandidateFingerprint(rec.Input, rec.SourceEvent)
	}
	if rec.ID == "" {
		rec.ID = "candidate_" + strings.TrimPrefix(rec.Fingerprint, "sha256:")
	}
	if rec.Case.ID == "" {
		rec.Case.ID = rec.ID
	}
	if rec.Case.Input == "" {
		rec.Case.Input = rec.Input
	}
	if rec.Case.Route == "" {
		rec.Case.Route = rec.Route
	}
	if rec.Route == "" {
		rec.Route = rec.Case.Route
	}
	if rec.Risk == "" {
		rec.Risk = "safe"
	}
	if rec.CreatedAt.IsZero() {
		rec.CreatedAt = time.Now()
	}
	if rec.UpdatedAt.IsZero() {
		rec.UpdatedAt = rec.CreatedAt
	}
	if len(rec.Suggestions) == 0 {
		rec.Suggestions = BuildOptimizationSuggestions(*rec)
	}
	return nil
}

func candidateWhere(filter CandidateFilter) (string, []any) {
	var clauses []string
	var args []any
	if filter.Status != "" {
		clauses = append(clauses, fmt.Sprintf("status=$%d", len(args)+1))
		args = append(args, filter.Status)
	}
	if filter.Route != "" {
		clauses = append(clauses, fmt.Sprintf("route=$%d", len(args)+1))
		args = append(args, filter.Route)
	}
	if filter.DomainID != "" {
		clauses = append(clauses, fmt.Sprintf("source_event->>'domain_id'=$%d", len(args)+1))
		args = append(args, filter.DomainID)
	}
	if filter.SourceKind != "" {
		clauses = append(clauses, fmt.Sprintf("source_event->>'source_kind'=$%d", len(args)+1))
		args = append(args, filter.SourceKind)
	}
	if filter.SourceName != "" {
		clauses = append(clauses, fmt.Sprintf("source_event->>'source_name'=$%d", len(args)+1))
		args = append(args, filter.SourceName)
	}
	if filter.OwnerScope != "" {
		clauses = append(clauses, fmt.Sprintf("source_event->>'owner_scope'=$%d", len(args)+1))
		args = append(args, string(filter.OwnerScope))
	}
	if filter.OwnerID != "" {
		clauses = append(clauses, fmt.Sprintf("source_event->>'owner_id'=$%d", len(args)+1))
		args = append(args, filter.OwnerID)
	}
	if filter.UserID != "" {
		clauses = append(clauses, fmt.Sprintf("source_event->>'user_id'=$%d", len(args)+1))
		args = append(args, filter.UserID)
	}
	if filter.FailureType != "" {
		clauses = append(clauses, fmt.Sprintf("COALESCE(NULLIF(source_event->>'failure_type',''), failure_type)=$%d", len(args)+1))
		args = append(args, string(filter.FailureType))
	}
	if len(clauses) == 0 {
		return "", args
	}
	return " WHERE " + strings.Join(clauses, " AND "), args
}

const candidateSelectSQL = `SELECT id, status, route, session_id, replay_ref, input, case_json, failure_type, risk, fingerprint, source_event,
	suggestions_json, review_note, created_by, reviewed_by, promoted_case_id, cluster_id, verify_result, created_at, updated_at, reviewed_at, last_verified_at
FROM agentquality_candidates`

type candidateScanner interface {
	Scan(dest ...any) error
}

func scanCandidate(row candidateScanner, rec *CandidateRecord) error {
	var caseJSON, eventJSON, suggestionsJSON []byte
	if err := row.Scan(
		&rec.ID,
		&rec.Status,
		&rec.Route,
		&rec.SessionID,
		&rec.ReplayRef,
		&rec.Input,
		&caseJSON,
		&rec.FailureType,
		&rec.Risk,
		&rec.Fingerprint,
		&eventJSON,
		&suggestionsJSON,
		&rec.ReviewNote,
		&rec.CreatedBy,
		&rec.ReviewedBy,
		&rec.PromotedCaseID,
		&rec.ClusterID,
		&rec.VerifyResult,
		&rec.CreatedAt,
		&rec.UpdatedAt,
		&rec.ReviewedAt,
		&rec.LastVerifiedAt,
	); err != nil {
		return err
	}
	if len(caseJSON) > 0 {
		if err := json.Unmarshal(caseJSON, &rec.Case); err != nil {
			return err
		}
	}
	if len(eventJSON) > 0 {
		if err := json.Unmarshal(eventJSON, &rec.SourceEvent); err != nil {
			return err
		}
	}
	if len(suggestionsJSON) > 0 {
		if err := json.Unmarshal(suggestionsJSON, &rec.Suggestions); err != nil {
			return err
		}
	}
	if len(rec.Suggestions) == 0 {
		rec.Suggestions = BuildOptimizationSuggestions(*rec)
	}
	return nil
}

func scanCandidateRows(rows pgx.Rows) ([]CandidateRecord, error) {
	var records []CandidateRecord
	for rows.Next() {
		var rec CandidateRecord
		if err := scanCandidate(rows, &rec); err != nil {
			return nil, err
		}
		records = append(records, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return records, nil
}

func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}

func mustJSONRaw(v string) []byte {
	if strings.TrimSpace(v) == "" {
		return []byte("{}")
	}
	return []byte(v)
}

func isDuplicatePrimaryKey(err error) bool {
	return err != nil && strings.Contains(err.Error(), "duplicate key") && strings.Contains(err.Error(), "agentquality_candidates_pkey")
}

func randomSuffix() string {
	var b [3]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano()%1_000_000)
	}
	return hex.EncodeToString(b[:])
}
