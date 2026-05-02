package agentquality

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestPGApprovalStore_RecordAndListApprovals(t *testing.T) {
	ctx := context.Background()
	stores, cleanup := setupPGOptimizationLifecycleStores(t)
	defer cleanup()
	now := time.Date(2026, 4, 30, 10, 0, 0, 0, time.UTC)

	recorded, err := stores.approvals.RecordApproval(ctx, ApprovalRecord{
		ID:           "approval-pg-1",
		SubjectID:    "evaldiff-pg-1",
		SubjectType:  ApprovalSubjectEvalDiff,
		Action:       ApprovalActionApprove,
		Reviewer:     "lead-1",
		ReviewerRole: ApprovalRoleLead,
		Note:         "ok",
		CreatedAt:    now,
	})
	require.NoError(t, err)
	require.Equal(t, "approval-pg-1", recorded.ID)

	listed, err := stores.approvals.ListApprovals(ctx, "evaldiff-pg-1")
	require.NoError(t, err)
	require.Len(t, listed, 1)
	require.Equal(t, ApprovalActionApprove, listed[0].Action)

	_, err = stores.approvals.RecordApproval(ctx, ApprovalRecord{
		ID:           "approval-pg-engineer",
		SubjectID:    "evaldiff-pg-1",
		SubjectType:  ApprovalSubjectEvalDiff,
		Action:       ApprovalActionApprove,
		Reviewer:     "engineer-1",
		ReviewerRole: ApprovalRoleEngineer,
		CreatedAt:    now,
	})
	require.Error(t, err)
}
