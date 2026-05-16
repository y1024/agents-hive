package accounting

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestBuildWhere_NoFilters(t *testing.T) {
	where, args := buildWhere(CostFilter{})
	assert.Equal(t, " WHERE 1=1", where)
	assert.Empty(t, args)
}

func TestBuildWhere_SessionIDOnly(t *testing.T) {
	where, args := buildWhere(CostFilter{SessionID: "sess-1"})
	assert.Equal(t, " WHERE 1=1 AND session_id = $1", where)
	assert.Equal(t, []any{"sess-1"}, args)
}

func TestBuildWhere_ModelOnly(t *testing.T) {
	where, args := buildWhere(CostFilter{Model: "gpt-5"})
	assert.Equal(t, " WHERE 1=1 AND model = $1", where)
	assert.Equal(t, []any{"gpt-5"}, args)
}

func TestBuildWhere_AllFilters(t *testing.T) {
	since := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	until := time.Date(2026, 12, 31, 23, 59, 59, 0, time.UTC)
	where, args := buildWhere(CostFilter{
		SessionID: "sess-1",
		UserID:    "user-1",
		Model:     "gpt-5",
		Since:     &since,
		Until:     &until,
	})
	assert.Equal(t, " WHERE 1=1 AND session_id = $1 AND user_id = $2 AND model = $3 AND created_at >= $4 AND created_at <= $5", where)
	assert.Len(t, args, 5)
	assert.Equal(t, "sess-1", args[0])
	assert.Equal(t, "user-1", args[1])
	assert.Equal(t, "gpt-5", args[2])
	assert.Equal(t, since, args[3])
	assert.Equal(t, until, args[4])
}

func TestBuildWhere_UserIDOnly(t *testing.T) {
	where, args := buildWhere(CostFilter{UserID: "user-1"})
	assert.Equal(t, " WHERE 1=1 AND user_id = $1", where)
	assert.Equal(t, []any{"user-1"}, args)
}

func TestBuildWhere_UserIDAndModel(t *testing.T) {
	// Model + UserID → Model=$1, UserID=$2 (UserID branch skipped, so Model gets $1)
	// buildWhere order: SessionID→UserID→Model, so UserID present means UserID=$1, Model=$2
	where, args := buildWhere(CostFilter{UserID: "user-1", Model: "gpt-5"})
	assert.Contains(t, where, "user_id = $1")
	assert.Contains(t, where, "model = $2")
	assert.Len(t, args, 2)
	assert.Equal(t, "user-1", args[0])
	assert.Equal(t, "gpt-5", args[1])
}

func TestBuildWhere_TimeRangeOnly(t *testing.T) {
	since := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	where, args := buildWhere(CostFilter{Since: &since})
	assert.Equal(t, " WHERE 1=1 AND created_at >= $1", where)
	assert.Equal(t, []any{since}, args)
}

func TestBuildWhere_PlaceholderIndexCorrectness(t *testing.T) {
	// SessionID skipped, Model + Since → $1, $2 (not $2, $3)
	since := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	where, args := buildWhere(CostFilter{Model: "claude-3-5-sonnet", Since: &since})
	assert.Contains(t, where, "model = $1")
	assert.Contains(t, where, "created_at >= $2")
	assert.Len(t, args, 2)
}
