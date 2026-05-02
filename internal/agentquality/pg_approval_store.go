package agentquality

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PGApprovalStore struct {
	pool *pgxpool.Pool
}

func NewPGApprovalStore(pool *pgxpool.Pool) *PGApprovalStore {
	return &PGApprovalStore{pool: pool}
}

func (s *PGApprovalStore) RecordApproval(ctx context.Context, rec ApprovalRecord) (*ApprovalRecord, error) {
	if s == nil || s.pool == nil {
		return nil, fmt.Errorf("approval store not configured")
	}
	if err := normalizeApprovalRecord(&rec); err != nil {
		return nil, err
	}
	if err := AuthorizeApprovalRole(rec.ReviewerRole); err != nil {
		return nil, err
	}
	var out ApprovalRecord
	row := s.pool.QueryRow(ctx, `
INSERT INTO optimization_approvals
	(id, subject_id, subject_type, action, reviewer, reviewer_role, note, created_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
ON CONFLICT (id) DO UPDATE SET id=optimization_approvals.id
RETURNING id, subject_id, subject_type, action, reviewer, reviewer_role, note, created_at`,
		rec.ID,
		rec.SubjectID,
		rec.SubjectType,
		rec.Action,
		rec.Reviewer,
		rec.ReviewerRole,
		rec.Note,
		rec.CreatedAt,
	)
	if err := scanApprovalRecord(row, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *PGApprovalStore) ListApprovals(ctx context.Context, subjectID string) ([]ApprovalRecord, error) {
	if s == nil || s.pool == nil {
		return nil, fmt.Errorf("approval store not configured")
	}
	var rows pgx.Rows
	var err error
	subjectID = strings.TrimSpace(subjectID)
	if subjectID == "" {
		rows, err = s.pool.Query(ctx, approvalSelectSQL+` ORDER BY created_at ASC, id ASC`)
	} else {
		rows, err = s.pool.Query(ctx, approvalSelectSQL+` WHERE subject_id=$1 ORDER BY created_at ASC, id ASC`, subjectID)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]ApprovalRecord, 0)
	for rows.Next() {
		var rec ApprovalRecord
		if err := scanApprovalRecord(rows, &rec); err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

const approvalSelectSQL = `SELECT id, subject_id, subject_type, action, reviewer, reviewer_role, note, created_at FROM optimization_approvals`

type approvalScanner interface {
	Scan(dest ...any) error
}

func scanApprovalRecord(row approvalScanner, rec *ApprovalRecord) error {
	return row.Scan(
		&rec.ID,
		&rec.SubjectID,
		&rec.SubjectType,
		&rec.Action,
		&rec.Reviewer,
		&rec.ReviewerRole,
		&rec.Note,
		&rec.CreatedAt,
	)
}
