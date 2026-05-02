package memory

import (
	"encoding/json"
	"fmt"
	"sort"
)

type ConflictStrategy string

const (
	ConflictStrategyNewest    ConflictStrategy = "newest"
	ConflictStrategyVersioned ConflictStrategy = "versioned"
	ConflictStrategyWeighted  ConflictStrategy = "weighted"
	ConflictStrategyManual    ConflictStrategy = "manual"
)

type ConflictGovernance struct {
	Key        string  `json:"key,omitempty"`
	Version    int     `json:"version,omitempty"`
	Confidence float64 `json:"confidence,omitempty"`
	Evidence   string  `json:"evidence,omitempty"`
}

type ConflictResolutionOptions struct {
	Strategy ConflictStrategy
}

type MemoryConflict struct {
	Key     string         `json:"key"`
	Records []MemoryRecord `json:"records"`
}

type ConflictResolution struct {
	ConflictKey          string       `json:"conflict_key"`
	Resolved             MemoryRecord `json:"resolved"`
	Governance           Governance   `json:"governance"`
	Version              int          `json:"version"`
	RequiresManualReview bool         `json:"requires_manual_review"`
}

func EncodeConflictGovernance(raw json.RawMessage, cg ConflictGovernance) json.RawMessage {
	var m map[string]any
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &m)
	}
	if m == nil {
		m = map[string]any{}
	}
	var encoded map[string]any
	b, _ := json.Marshal(cg)
	_ = json.Unmarshal(b, &encoded)
	m["conflict_governance"] = encoded
	return mustMarshalRaw(m)
}

func DecodeConflictGovernance(raw json.RawMessage) ConflictGovernance {
	if len(raw) == 0 {
		return ConflictGovernance{}
	}
	var wrapper struct {
		ConflictGovernance ConflictGovernance `json:"conflict_governance"`
	}
	_ = json.Unmarshal(raw, &wrapper)
	return wrapper.ConflictGovernance
}

func DetectMemoryConflicts(records []MemoryRecord) []MemoryConflict {
	groups := map[string][]MemoryRecord{}
	for _, record := range records {
		cg := DecodeConflictGovernance(record.Metadata)
		if record.UserID == "" || record.Type == "" || cg.Key == "" {
			continue
		}
		key := memoryConflictKey(record.UserID, record.Type, cg.Key)
		groups[key] = append(groups[key], record)
	}
	keys := make([]string, 0, len(groups))
	for key, records := range groups {
		if len(records) > 1 {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	conflicts := make([]MemoryConflict, 0, len(keys))
	for _, key := range keys {
		conflicts = append(conflicts, MemoryConflict{Key: key, Records: append([]MemoryRecord(nil), groups[key]...)})
	}
	return conflicts
}

func ResolveMemoryConflict(records []MemoryRecord, opts ConflictResolutionOptions) ConflictResolution {
	if len(records) == 0 {
		return ConflictResolution{}
	}
	strategy := opts.Strategy
	if strategy == "" {
		strategy = ConflictStrategyNewest
	}
	maxVersion := 0
	for _, record := range records {
		if version := DecodeConflictGovernance(record.Metadata).Version; version > maxVersion {
			maxVersion = version
		}
	}
	firstGov := DecodeConflictGovernance(records[0].Metadata)
	key := memoryConflictKey(records[0].UserID, records[0].Type, firstGov.Key)
	if strategy == ConflictStrategyManual {
		return ConflictResolution{
			ConflictKey: key,
			Resolved: MemoryRecord{
				UserID: records[0].UserID,
				Type:   records[0].Type,
			},
			Governance: Governance{
				Evidence: fmt.Sprintf("manual review required for %d conflicting memories", len(records)),
			},
			Version:              maxVersion + 1,
			RequiresManualReview: true,
		}
	}

	chosen := records[0]
	switch strategy {
	case ConflictStrategyVersioned:
		chosen = newestByVersion(records)
	case ConflictStrategyWeighted:
		chosen = highestConfidence(records)
	default:
		chosen = newestByUpdatedAt(records)
	}
	chosenGov := DecodeConflictGovernance(chosen.Metadata)
	version := chosenGov.Version
	if strategy == ConflictStrategyVersioned {
		version = maxVersion + 1
	}
	if version == 0 {
		version = maxVersion
	}
	if version == 0 {
		version = 1
	}
	evidence := chosenGov.Evidence
	if evidence == "" {
		evidence = fmt.Sprintf("%s conflict resolution selected memory %d", strategy, chosen.ID)
	} else {
		evidence = fmt.Sprintf("%s conflict resolution selected memory %d: %s", strategy, chosen.ID, evidence)
	}
	chosen.Metadata = EncodeGovernance(chosen.Metadata, Governance{
		Confidence: chosenGov.Confidence,
		Evidence:   evidence,
	})
	return ConflictResolution{
		ConflictKey: key,
		Resolved:    chosen,
		Governance: Governance{
			Confidence: chosenGov.Confidence,
			Evidence:   evidence,
		},
		Version: version,
	}
}

func newestByUpdatedAt(records []MemoryRecord) MemoryRecord {
	chosen := records[0]
	for _, record := range records[1:] {
		if record.UpdatedAt.After(chosen.UpdatedAt) || record.UpdatedAt.Equal(chosen.UpdatedAt) && record.ID > chosen.ID {
			chosen = record
		}
	}
	return chosen
}

func newestByVersion(records []MemoryRecord) MemoryRecord {
	chosen := records[0]
	chosenVersion := DecodeConflictGovernance(chosen.Metadata).Version
	for _, record := range records[1:] {
		version := DecodeConflictGovernance(record.Metadata).Version
		if version > chosenVersion || version == chosenVersion && record.UpdatedAt.After(chosen.UpdatedAt) {
			chosen = record
			chosenVersion = version
		}
	}
	return chosen
}

func highestConfidence(records []MemoryRecord) MemoryRecord {
	chosen := records[0]
	chosenConfidence := DecodeConflictGovernance(chosen.Metadata).Confidence
	for _, record := range records[1:] {
		confidence := DecodeConflictGovernance(record.Metadata).Confidence
		if confidence > chosenConfidence || confidence == chosenConfidence && record.UpdatedAt.After(chosen.UpdatedAt) {
			chosen = record
			chosenConfidence = confidence
		}
	}
	return chosen
}

func memoryConflictKey(userID string, memoryType MemoryType, key string) string {
	return fmt.Sprintf("%s/%s/%s", userID, memoryType, key)
}
