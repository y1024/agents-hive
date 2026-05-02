package memory

import (
	"encoding/json"
	"time"
)

// Governance 承载 memory 注入治理元数据，存放在 MemoryRecord.Metadata.governance。
type Governance struct {
	Source         string    `json:"source,omitempty"`
	Evidence       string    `json:"evidence,omitempty"`
	Confidence     float64   `json:"confidence,omitempty"`
	ExpiresAt      time.Time `json:"expires_at,omitempty"`
	ExtractedBy    string    `json:"extracted_by,omitempty"`
	SourceMessage  string    `json:"source_message,omitempty"`
	SourceUserID   string    `json:"source_user_id,omitempty"`
	SourceTenantID string    `json:"source_tenant_id,omitempty"`
	RunID          string    `json:"run_id,omitempty"`
}

// DecodeGovernance 从 MemoryRecord.Metadata 读取 governance 字段。
func DecodeGovernance(raw json.RawMessage) Governance {
	if len(raw) == 0 {
		return Governance{}
	}
	var wrapper struct {
		Governance Governance `json:"governance"`
	}
	_ = json.Unmarshal(raw, &wrapper)
	return wrapper.Governance
}

// EncodeGovernance 在保留既有 metadata 字段的同时写入 governance 字段。
func EncodeGovernance(raw json.RawMessage, g Governance) json.RawMessage {
	var m map[string]any
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &m)
	}
	if m == nil {
		m = map[string]any{}
	}
	var governance map[string]any
	b, _ := json.Marshal(g)
	_ = json.Unmarshal(b, &governance)
	if g.ExpiresAt.IsZero() {
		delete(governance, "expires_at")
	}
	m["governance"] = governance
	encoded, _ := json.Marshal(m)
	return encoded
}

// Injectable 判断该记忆是否允许注入上下文。
func (g Governance) Injectable(now time.Time, minConfidence float64) bool {
	if g.Confidence > 0 && g.Confidence < minConfidence {
		return false
	}
	if !g.ExpiresAt.IsZero() && now.After(g.ExpiresAt) {
		return false
	}
	return true
}
