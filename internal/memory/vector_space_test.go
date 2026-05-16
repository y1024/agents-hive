package memory

import (
	"encoding/json"
	"testing"
	"time"
)

func TestPlanVectorSpaceMigrationDryRunBatchesAndResume(t *testing.T) {
	now := time.Date(2026, 4, 30, 11, 0, 0, 0, time.UTC)
	records := []MemoryRecord{
		{ID: 1, UserID: "u1", Content: "already migrated", Metadata: encodeVectorSpaceMeta(t, "memory:v2", "ready")},
		{ID: 2, UserID: "u1", Content: "pending two"},
		{ID: 3, UserID: "u2", Content: "pending three", Metadata: encodeVectorSpaceMeta(t, "legacy", "ready")},
		{ID: 4, UserID: "u2", Content: "pending four"},
	}

	first := PlanVectorSpaceMigration(records, VectorSpaceMigrationOptions{
		TargetSpace: "memory:v2",
		BatchSize:   2,
		DryRun:      true,
		Now:         now,
	})
	if !first.DryRun {
		t.Fatal("DryRun = false, want true")
	}
	if first.ResumeToken == "" {
		t.Fatal("ResumeToken is empty, want continuation token")
	}
	if len(first.Updates) != 2 {
		t.Fatalf("first updates len = %d, want 2", len(first.Updates))
	}
	if first.Updates[0].MemoryID != 2 || first.Updates[1].MemoryID != 3 {
		t.Fatalf("first update IDs = %+v, want 2 then 3", first.Updates)
	}
	if first.Updates[0].Record.Metadata != nil {
		t.Fatalf("dry-run update mutated metadata: %s", string(first.Updates[0].Record.Metadata))
	}

	second := PlanVectorSpaceMigration(records, VectorSpaceMigrationOptions{
		TargetSpace: "memory:v2",
		BatchSize:   2,
		ResumeToken: first.ResumeToken,
		Now:         now,
	})
	if len(second.Updates) != 1 {
		t.Fatalf("second updates len = %d, want 1", len(second.Updates))
	}
	if second.Updates[0].MemoryID != 4 {
		t.Fatalf("second update ID = %d, want 4", second.Updates[0].MemoryID)
	}
	if second.ResumeToken != "" {
		t.Fatalf("second ResumeToken = %q, want empty", second.ResumeToken)
	}
	got := DecodeVectorSpace(second.Updates[0].Record.Metadata)
	if got.Name != "memory:v2" || got.EmbeddingState != EmbeddingStatePending {
		t.Fatalf("vector space = %+v, want target with pending embedding", got)
	}
	if got.MigratedAt.IsZero() || !got.MigratedAt.Equal(now) {
		t.Fatalf("MigratedAt = %v, want %v", got.MigratedAt, now)
	}
}

func TestPlanVectorSpaceMigrationOffsetResumeAfterInterruption(t *testing.T) {
	records := []MemoryRecord{
		{ID: 10, UserID: "u1", Content: "a"},
		{ID: 11, UserID: "u1", Content: "b"},
		{ID: 12, UserID: "u1", Content: "c"},
	}

	first := PlanVectorSpaceMigration(records, VectorSpaceMigrationOptions{TargetSpace: "space-a", BatchSize: 1})
	if first.ResumeToken == "" {
		t.Fatal("ResumeToken is empty, want continuation token")
	}
	resumed := PlanVectorSpaceMigration(records, VectorSpaceMigrationOptions{TargetSpace: "space-a", BatchSize: 10, ResumeToken: first.ResumeToken})

	if len(resumed.Updates) != 2 {
		t.Fatalf("resumed updates len = %d, want 2", len(resumed.Updates))
	}
	if resumed.Updates[0].MemoryID != 11 || resumed.Updates[1].MemoryID != 12 {
		t.Fatalf("resumed IDs = %+v, want 11 then 12", resumed.Updates)
	}
}

func TestPlanVectorSpaceMigrationUsesEmptyUpdatesSliceWhenNoChanges(t *testing.T) {
	plan := PlanVectorSpaceMigration(nil, VectorSpaceMigrationOptions{TargetSpace: "space-a"})
	if plan.Updates == nil {
		t.Fatal("Updates is nil, want empty slice")
	}
	if len(plan.Updates) != 0 {
		t.Fatalf("Updates len = %d, want 0", len(plan.Updates))
	}
}

func encodeVectorSpaceMeta(t *testing.T, name string, state EmbeddingState) json.RawMessage {
	t.Helper()
	return EncodeVectorSpace(nil, VectorSpaceMetadata{Name: name, EmbeddingState: state})
}
