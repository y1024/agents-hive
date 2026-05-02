package memory

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

type MemoryExportDocument struct {
	Version    int            `json:"version"`
	UserID     string         `json:"user_id,omitempty"`
	ExportedAt time.Time      `json:"exported_at,omitempty"`
	Memories   []MemoryRecord `json:"memories"`
}

type MemoryExportOptions struct {
	UserID              string
	StrictUserIsolation bool
	Now                 time.Time
}

type MemoryImportOptions struct {
	UserID              string
	StrictUserIsolation bool
	ResetIDs            bool
}

func ExportMemoriesJSON(records []MemoryRecord, opts MemoryExportOptions) ([]byte, error) {
	if opts.Now.IsZero() {
		opts.Now = time.Now()
	}
	exported := make([]MemoryRecord, 0, len(records))
	for _, record := range records {
		if opts.UserID != "" && record.UserID != opts.UserID {
			if opts.StrictUserIsolation {
				return nil, fmt.Errorf("memory %d belongs to user %q, want %q", record.ID, record.UserID, opts.UserID)
			}
			continue
		}
		exported = append(exported, cloneMemoryRecord(record))
	}
	doc := MemoryExportDocument{
		Version:    1,
		UserID:     opts.UserID,
		ExportedAt: opts.Now,
		Memories:   exported,
	}
	return json.MarshalIndent(doc, "", "  ")
}

func ImportMemoriesJSON(data []byte, opts MemoryImportOptions) ([]MemoryRecord, error) {
	if len(data) == 0 {
		return nil, errors.New("memory import document is empty")
	}
	var doc MemoryExportDocument
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	if doc.Version != 1 {
		return nil, fmt.Errorf("unsupported memory export version %d", doc.Version)
	}
	if opts.UserID != "" && doc.UserID != "" && doc.UserID != opts.UserID {
		return nil, fmt.Errorf("memory export belongs to user %q, want %q", doc.UserID, opts.UserID)
	}

	imported := make([]MemoryRecord, 0, len(doc.Memories))
	for _, record := range doc.Memories {
		if opts.UserID != "" && record.UserID != "" && record.UserID != opts.UserID {
			if opts.StrictUserIsolation {
				return nil, fmt.Errorf("memory %d belongs to user %q, want %q", record.ID, record.UserID, opts.UserID)
			}
			continue
		}
		next := cloneMemoryRecord(record)
		if opts.ResetIDs {
			next.ID = 0
		}
		if opts.UserID != "" {
			next.UserID = opts.UserID
		}
		if next.Type == "" {
			next.Type = MemoryTypeUser
		}
		imported = append(imported, next)
	}
	return imported, nil
}

func cloneMemoryRecord(record MemoryRecord) MemoryRecord {
	next := record
	next.Tags = append([]string(nil), record.Tags...)
	next.Metadata = cloneRawMessage(record.Metadata)
	return next
}
