package memory

import (
	"testing"
	"time"
)

func TestResolveMemoryConflictNewest(t *testing.T) {
	older := conflictMemory(1, "u1", "editor", "vim", 0.8, 1, time.Date(2026, 4, 30, 9, 0, 0, 0, time.UTC))
	newer := conflictMemory(2, "u1", "editor", "emacs", 0.4, 2, time.Date(2026, 4, 30, 10, 0, 0, 0, time.UTC))

	result := ResolveMemoryConflict([]MemoryRecord{older, newer}, ConflictResolutionOptions{Strategy: ConflictStrategyNewest})

	if result.Resolved.Content != "emacs" {
		t.Fatalf("content = %q, want newest content", result.Resolved.Content)
	}
	if result.Resolved.UserID != "u1" || result.Resolved.Type != MemoryTypeUser {
		t.Fatalf("resolved identity = %+v, want same user/type", result.Resolved)
	}
	if result.Governance.Confidence != 0.4 {
		t.Fatalf("confidence = %v, want newest confidence 0.4", result.Governance.Confidence)
	}
	if result.Governance.Evidence == "" {
		t.Fatal("governance evidence is empty")
	}
	if result.ConflictKey != "u1/user/editor" {
		t.Fatalf("ConflictKey = %q, want u1/user/editor", result.ConflictKey)
	}
}

func TestResolveMemoryConflictVersioned(t *testing.T) {
	first := conflictMemory(1, "u1", "editor", "vim", 0.8, 1, time.Date(2026, 4, 30, 9, 0, 0, 0, time.UTC))
	second := conflictMemory(2, "u1", "editor", "emacs", 0.9, 2, time.Date(2026, 4, 30, 10, 0, 0, 0, time.UTC))

	result := ResolveMemoryConflict([]MemoryRecord{first, second}, ConflictResolutionOptions{Strategy: ConflictStrategyVersioned})

	if result.Resolved.Content != "emacs" {
		t.Fatalf("content = %q, want latest version content", result.Resolved.Content)
	}
	if result.Version != 3 {
		t.Fatalf("version = %d, want 3", result.Version)
	}
	if result.Governance.Confidence != 0.9 {
		t.Fatalf("confidence = %v, want 0.9", result.Governance.Confidence)
	}
	if result.Governance.Evidence == "" {
		t.Fatal("governance evidence is empty")
	}
}

func TestResolveMemoryConflictWeighted(t *testing.T) {
	newerLowConfidence := conflictMemory(1, "u1", "timezone", "UTC", 0.3, 1, time.Date(2026, 4, 30, 10, 0, 0, 0, time.UTC))
	olderHighConfidence := conflictMemory(2, "u1", "timezone", "Asia/Shanghai", 0.95, 1, time.Date(2026, 4, 30, 9, 0, 0, 0, time.UTC))

	result := ResolveMemoryConflict([]MemoryRecord{newerLowConfidence, olderHighConfidence}, ConflictResolutionOptions{Strategy: ConflictStrategyWeighted})

	if result.Resolved.Content != "Asia/Shanghai" {
		t.Fatalf("content = %q, want weighted high-confidence content", result.Resolved.Content)
	}
	if result.Governance.Confidence != 0.95 {
		t.Fatalf("confidence = %v, want 0.95", result.Governance.Confidence)
	}
	if result.Governance.Evidence == "" {
		t.Fatal("governance evidence is empty")
	}
}

func TestResolveMemoryConflictManual(t *testing.T) {
	first := conflictMemory(1, "u1", "shell", "bash", 0.7, 1, time.Date(2026, 4, 30, 9, 0, 0, 0, time.UTC))
	second := conflictMemory(2, "u1", "shell", "zsh", 0.8, 2, time.Date(2026, 4, 30, 10, 0, 0, 0, time.UTC))

	result := ResolveMemoryConflict([]MemoryRecord{first, second}, ConflictResolutionOptions{Strategy: ConflictStrategyManual})

	if result.RequiresManualReview != true {
		t.Fatal("RequiresManualReview = false, want true")
	}
	if result.Resolved.Content != "" {
		t.Fatalf("manual content = %q, want empty until reviewer chooses", result.Resolved.Content)
	}
	if result.Governance.Confidence != 0 {
		t.Fatalf("manual confidence = %v, want 0", result.Governance.Confidence)
	}
	if result.Version != 3 {
		t.Fatalf("version = %d, want next version 3", result.Version)
	}
	if result.Governance.Evidence == "" {
		t.Fatal("governance evidence is empty")
	}
}

func TestDetectMemoryConflictsRequiresSameUserTypeKey(t *testing.T) {
	records := []MemoryRecord{
		conflictMemory(1, "u1", "editor", "vim", 0.8, 1, time.Now()),
		conflictMemory(2, "u1", "editor", "emacs", 0.9, 2, time.Now()),
		conflictMemory(3, "u2", "editor", "nano", 0.9, 1, time.Now()),
		conflictMemory(4, "u1", "shell", "zsh", 0.9, 1, time.Now()),
	}

	conflicts := DetectMemoryConflicts(records)

	if len(conflicts) != 1 {
		t.Fatalf("conflicts len = %d, want 1", len(conflicts))
	}
	if conflicts[0].Key != "u1/user/editor" {
		t.Fatalf("conflict key = %q, want u1/user/editor", conflicts[0].Key)
	}
	if len(conflicts[0].Records) != 2 {
		t.Fatalf("conflict records len = %d, want 2", len(conflicts[0].Records))
	}
}

func conflictMemory(id int64, userID, key, content string, confidence float64, version int, updatedAt time.Time) MemoryRecord {
	return MemoryRecord{
		ID:        id,
		UserID:    userID,
		Type:      MemoryTypeUser,
		Content:   content,
		UpdatedAt: updatedAt,
		Metadata: EncodeConflictGovernance(nil, ConflictGovernance{
			Key:        key,
			Version:    version,
			Confidence: confidence,
			Evidence:   "source evidence",
		}),
	}
}
