package memory

import (
	"encoding/json"
	"strconv"
	"time"
)

type EmbeddingState string

const (
	EmbeddingStatePending EmbeddingState = "pending"
	EmbeddingStateReady   EmbeddingState = "ready"
	EmbeddingStateFailed  EmbeddingState = "failed"

	DefaultVectorSpaceName = "memory:default"
)

type VectorSpaceMetadata struct {
	Name           string         `json:"name,omitempty"`
	EmbeddingState EmbeddingState `json:"embedding_state,omitempty"`
	MigratedAt     time.Time      `json:"migrated_at,omitempty"`
}

type VectorSpaceMigrationOptions struct {
	TargetSpace string
	BatchSize   int
	ResumeToken string
	Offset      int
	DryRun      bool
	Now         time.Time
}

type VectorSpaceMigrationUpdate struct {
	MemoryID int64        `json:"memory_id"`
	Record   MemoryRecord `json:"record"`
}

type VectorSpaceMigrationPlan struct {
	DryRun      bool                         `json:"dry_run"`
	Scanned     int                          `json:"scanned"`
	Updates     []VectorSpaceMigrationUpdate `json:"updates"`
	ResumeToken string                       `json:"resume_token,omitempty"`
	NextOffset  int                          `json:"next_offset,omitempty"`
}

func DecodeVectorSpace(raw json.RawMessage) VectorSpaceMetadata {
	if len(raw) == 0 {
		return VectorSpaceMetadata{}
	}
	var wrapper struct {
		VectorSpace VectorSpaceMetadata `json:"vector_space"`
	}
	_ = json.Unmarshal(raw, &wrapper)
	return wrapper.VectorSpace
}

func EncodeVectorSpace(raw json.RawMessage, meta VectorSpaceMetadata) json.RawMessage {
	var m map[string]any
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &m)
	}
	if m == nil {
		m = map[string]any{}
	}
	var encoded map[string]any
	b, _ := json.Marshal(meta)
	_ = json.Unmarshal(b, &encoded)
	if meta.MigratedAt.IsZero() {
		delete(encoded, "migrated_at")
	}
	m["vector_space"] = encoded
	out, _ := json.Marshal(m)
	return out
}

func PlanVectorSpaceMigration(records []MemoryRecord, opts VectorSpaceMigrationOptions) VectorSpaceMigrationPlan {
	if opts.TargetSpace == "" {
		opts.TargetSpace = DefaultVectorSpaceName
	}
	if opts.BatchSize <= 0 {
		opts.BatchSize = len(records)
		if opts.BatchSize == 0 {
			opts.BatchSize = 100
		}
	}
	if opts.Now.IsZero() {
		opts.Now = time.Now()
	}
	offset := opts.Offset
	if opts.ResumeToken != "" {
		if parsed, err := strconv.Atoi(opts.ResumeToken); err == nil && parsed > offset {
			offset = parsed
		}
	}
	if offset < 0 {
		offset = 0
	}
	if offset > len(records) {
		offset = len(records)
	}

	plan := VectorSpaceMigrationPlan{DryRun: opts.DryRun}
	nextOffset := offset
	for i := offset; i < len(records); i++ {
		plan.Scanned++
		nextOffset = i + 1
		record := records[i]
		current := DecodeVectorSpace(record.Metadata)
		if current.Name == opts.TargetSpace {
			continue
		}
		update := VectorSpaceMigrationUpdate{MemoryID: record.ID, Record: record}
		if !opts.DryRun {
			nextMeta := VectorSpaceMetadata{
				Name:           opts.TargetSpace,
				EmbeddingState: EmbeddingStatePending,
				MigratedAt:     opts.Now,
			}
			update.Record.Metadata = EncodeVectorSpace(record.Metadata, nextMeta)
		}
		plan.Updates = append(plan.Updates, update)
		if len(plan.Updates) >= opts.BatchSize {
			break
		}
	}
	if nextOffset < len(records) {
		plan.NextOffset = nextOffset
		plan.ResumeToken = strconv.Itoa(nextOffset)
	}
	return plan
}
