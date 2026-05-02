package memory

import (
	"encoding/json"
	"testing"
	"time"
)

func TestExportMemoriesJSONIncludesOnlyRequestedUser(t *testing.T) {
	records := []MemoryRecord{
		{ID: 1, UserID: "u1", Type: MemoryTypeUser, Content: "u1 memory", UpdatedAt: time.Date(2026, 4, 30, 9, 0, 0, 0, time.UTC)},
		{ID: 2, UserID: "u2", Type: MemoryTypeUser, Content: "u2 memory", UpdatedAt: time.Date(2026, 4, 30, 10, 0, 0, 0, time.UTC)},
	}

	data, err := ExportMemoriesJSON(records, MemoryExportOptions{UserID: "u1"})
	if err != nil {
		t.Fatalf("ExportMemoriesJSON() error = %v", err)
	}

	var doc MemoryExportDocument
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("export is invalid JSON: %v", err)
	}
	if doc.UserID != "u1" {
		t.Fatalf("doc.UserID = %q, want u1", doc.UserID)
	}
	if len(doc.Memories) != 1 || doc.Memories[0].UserID != "u1" {
		t.Fatalf("export memories = %+v, want only u1", doc.Memories)
	}
}

func TestExportMemoriesJSONRejectsCrossUserInput(t *testing.T) {
	_, err := ExportMemoriesJSON([]MemoryRecord{
		{ID: 1, UserID: "u1", Type: MemoryTypeUser, Content: "ok"},
		{ID: 2, UserID: "u2", Type: MemoryTypeUser, Content: "leak"},
	}, MemoryExportOptions{UserID: "u1", StrictUserIsolation: true})

	if err == nil {
		t.Fatal("ExportMemoriesJSON() error = nil, want cross-user isolation error")
	}
}

func TestImportMemoriesJSONValidatesUserIsolation(t *testing.T) {
	doc := MemoryExportDocument{
		Version: 1,
		UserID:  "u1",
		Memories: []MemoryRecord{
			{ID: 1, UserID: "u1", Type: MemoryTypeUser, Content: "safe"},
			{ID: 2, UserID: "u2", Type: MemoryTypeUser, Content: "leak"},
		},
	}
	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}

	_, err = ImportMemoriesJSON(data, MemoryImportOptions{UserID: "u1", StrictUserIsolation: true})
	if err == nil {
		t.Fatal("ImportMemoriesJSON() error = nil, want cross-user isolation error")
	}
}

func TestImportMemoriesJSONNormalizesIDsAndUser(t *testing.T) {
	doc := MemoryExportDocument{
		Version: 1,
		UserID:  "u1",
		Memories: []MemoryRecord{
			{ID: 99, UserID: "u1", Type: MemoryTypeUser, Content: "import me"},
		},
	}
	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}

	records, err := ImportMemoriesJSON(data, MemoryImportOptions{UserID: "u1", ResetIDs: true})
	if err != nil {
		t.Fatalf("ImportMemoriesJSON() error = %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("records len = %d, want 1", len(records))
	}
	if records[0].ID != 0 {
		t.Fatalf("ID = %d, want reset 0", records[0].ID)
	}
	if records[0].UserID != "u1" {
		t.Fatalf("UserID = %q, want u1", records[0].UserID)
	}
}
