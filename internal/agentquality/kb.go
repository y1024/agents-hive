package agentquality

import (
	"strings"
	"time"
)

const (
	EventKBRetrieval EventName = "quality.kb.retrieval"
	EventKBEvidence  EventName = "quality.kb.evidence"

	FailureKBRetrieval FailureType = "kb_retrieval"
	FailureKBEvidence  FailureType = "kb_evidence"
	FailureKBSummary   FailureType = "kb_summary"
)

const (
	KBFailureSectionText       = "kb_section_text"
	KBFailureEvidenceViolation = "kb_evidence_violation"
	KBFailureCitationMissing   = "kb_citation_missing"
	KBFailureSummaryGenerator  = "kb_summary_generator"
)

type KBPageIndexRetrievalCase struct {
	CaseID             string   `json:"case_id"`
	Query              string   `json:"query"`
	ExpectedNodeIDs    []string `json:"expected_node_ids,omitempty"`
	ExpectedPageRanges []string `json:"expected_page_ranges,omitempty"`
}

type KBPageIndexRetrievalActual struct {
	RetrievedNodeIDs    []string `json:"retrieved_node_ids,omitempty"`
	RetrievedPageRanges []string `json:"retrieved_page_ranges,omitempty"`
	CitationNodeIDs     []string `json:"citation_node_ids,omitempty"`
	CitationPageRanges  []string `json:"citation_page_ranges,omitempty"`
}

type KBPageIndexRetrievalScore struct {
	CaseID               string   `json:"case_id"`
	RetrievalHit         bool     `json:"retrieval_hit"`
	CitationHit          bool     `json:"citation_hit"`
	ExpectedCount        int      `json:"expected_count"`
	RetrievedCount       int      `json:"retrieved_count"`
	MissingNodeIDs       []string `json:"missing_node_ids,omitempty"`
	MissingPageRanges    []string `json:"missing_page_ranges,omitempty"`
	MissingCitationNodes []string `json:"missing_citation_nodes,omitempty"`
	MissingCitationPages []string `json:"missing_citation_pages,omitempty"`
	UnexpectedNodeIDs    []string `json:"unexpected_node_ids,omitempty"`
	UnexpectedPageRanges []string `json:"unexpected_page_ranges,omitempty"`
}

func ScoreKBPageIndexRetrieval(tc KBPageIndexRetrievalCase, actual KBPageIndexRetrievalActual) KBPageIndexRetrievalScore {
	expectedNodeIDs := normalizeIDSet(tc.ExpectedNodeIDs)
	actualNodeIDs := normalizeIDSet(actual.RetrievedNodeIDs)
	citationNodeIDs := normalizeIDSet(actual.CitationNodeIDs)
	expectedRanges := normalizeRangeSet(tc.ExpectedPageRanges)
	actualRanges := normalizeRangeSet(actual.RetrievedPageRanges)
	citationRanges := normalizeRangeSet(actual.CitationPageRanges)

	missingNodeIDs := missingStrings(expectedNodeIDs, actualNodeIDs)
	missingRanges := missingStrings(expectedRanges, actualRanges)
	unexpectedNodeIDs := missingStrings(actualNodeIDs, expectedNodeIDs)
	unexpectedRanges := missingStrings(actualRanges, expectedRanges)
	citationMissingNodes := missingStrings(expectedNodeIDs, citationNodeIDs)
	citationMissingRanges := missingStrings(expectedRanges, citationRanges)

	return KBPageIndexRetrievalScore{
		CaseID:               strings.TrimSpace(tc.CaseID),
		RetrievalHit:         len(missingNodeIDs) == 0 && len(missingRanges) == 0,
		CitationHit:          len(citationMissingNodes) == 0 && len(citationMissingRanges) == 0,
		ExpectedCount:        len(expectedNodeIDs) + len(expectedRanges),
		RetrievedCount:       len(actualNodeIDs) + len(actualRanges),
		MissingNodeIDs:       missingNodeIDs,
		MissingPageRanges:    missingRanges,
		MissingCitationNodes: citationMissingNodes,
		MissingCitationPages: citationMissingRanges,
		UnexpectedNodeIDs:    unexpectedNodeIDs,
		UnexpectedPageRanges: unexpectedRanges,
	}
}

func SummarizeKBPageIndexRetrieval(scores []KBPageIndexRetrievalScore) map[string]any {
	total := len(scores)
	retrievalHits := 0
	citationHits := 0
	for _, score := range scores {
		if score.RetrievalHit {
			retrievalHits++
		}
		if score.CitationHit {
			citationHits++
		}
	}
	return map[string]any{
		"total":                  total,
		"retrieval_hits":         retrievalHits,
		"citation_hits":          citationHits,
		"retrieval_hit_rate":     kbRatio(retrievalHits, total),
		"citation_hit_rate":      kbRatio(citationHits, total),
		"pageindex_style_eval":   true,
		"retrieval_contract":     "kb.doc.meta -> kb.doc.structure -> kb.section.text(node_ids|page_ranges)",
		"requires_structured_kb": true,
	}
}

type KBEventInput struct {
	Name       EventName
	SessionID  string
	TurnID     string
	TraceID    string
	SpanID     string
	ToolCallID string
	DomainID   string
	OwnerScope OwnerScope
	OwnerID    string
	UserID     string
	Route      string
	ToolName   string
	Status     FinalStatus
	Failure    FailureType
	Reason     string
	Error      string
	Attributes map[string]any
	Ts         time.Time
}

func NewKBQualityEvent(input KBEventInput) Event {
	name := input.Name
	if name == "" {
		name = EventKBRetrieval
	}
	status := input.Status
	if status == "" {
		status = StatusPass
	}
	failure := input.Failure
	if failure == "" {
		failure = FailureNone
	}
	attrs := map[string]any{
		"kb_failure_type": kbFailureTypeFor(name, failure, input.Reason),
	}
	for k, v := range input.Attributes {
		if strings.TrimSpace(k) != "" && v != nil {
			attrs[k] = v
		}
	}
	if input.ToolCallID != "" {
		attrs["tool_call_id"] = input.ToolCallID
	}
	if input.Reason != "" {
		attrs["reason"] = input.Reason
	}
	if input.Error != "" {
		attrs["error"] = input.Error
	}
	return Event{
		Name:        name,
		TraceID:     input.TraceID,
		SpanID:      input.SpanID,
		TurnID:      input.TurnID,
		DomainID:    input.DomainID,
		SourceKind:  "kb",
		SourceName:  "quality",
		OwnerScope:  input.OwnerScope,
		OwnerID:     input.OwnerID,
		UserID:      input.UserID,
		Route:       input.Route,
		FailureType: failure,
		RetryReason: input.Reason,
		FinalStatus: status,
		ToolDecision: ToolDecision{
			Actual: input.ToolName,
		},
		Attributes: attrs,
		Ts:         input.Ts,
	}
}

func kbFailureTypeFor(name EventName, failure FailureType, reason string) string {
	if reason != "" {
		return reason
	}
	switch failure {
	case FailureKBEvidence:
		return KBFailureEvidenceViolation
	case FailureKBSummary:
		return KBFailureSummaryGenerator
	case FailureKBRetrieval:
		return KBFailureSectionText
	}
	if name == EventKBEvidence {
		return KBFailureEvidenceViolation
	}
	return ""
}

func normalizeIDSet(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func normalizeRangeSet(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.Join(strings.Fields(strings.TrimSpace(value)), "")
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func missingStrings(expected, actual []string) []string {
	if len(expected) == 0 {
		return nil
	}
	actualSet := make(map[string]struct{}, len(actual))
	for _, value := range actual {
		actualSet[value] = struct{}{}
	}
	missing := make([]string, 0)
	for _, value := range expected {
		if _, ok := actualSet[value]; !ok {
			missing = append(missing, value)
		}
	}
	return missing
}

func kbRatio(numerator, denominator int) float64 {
	if denominator == 0 {
		return 0
	}
	return float64(numerator) / float64(denominator)
}
