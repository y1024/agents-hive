package agentquality

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// Source kind constants for tracking where cases originate
type SourceKind string

const (
	SourceQualityEvent      SourceKind = "quality_event"
	SourceUserFeedback      SourceKind = "user_feedback"
	SourceApprovalRejection SourceKind = "approval_rejection"
	SourceRollbackIncident  SourceKind = "rollback_incident"
	SourceManual            SourceKind = "manual"
)

// Golden case states for lifecycle management
type GoldenCaseState string

const (
	GoldenCaseStateDraft       GoldenCaseState = "draft"
	GoldenCaseStateActive      GoldenCaseState = "active"
	GoldenCaseStateQuarantined GoldenCaseState = "quarantined"
	GoldenCaseStateDeprecated  GoldenCaseState = "deprecated"
)

// CaseAssertion defines structured assertions for case validation
type CaseAssertion struct {
	Type     string `json:"type"`
	Target   string `json:"target,omitempty"`
	Expected string `json:"expected,omitempty"`
	Message  string `json:"message,omitempty"`
}

// Assertion type constants
const (
	AssertionToolCalled            = "tool_called"
	AssertionToolNotCalled         = "tool_not_called"
	AssertionStatusIs              = "status_is"
	AssertionAnswerContains        = "answer_contains"
	AssertionAnswerNotContains     = "answer_not_contains"
	AssertionRequiresClarification = "requires_clarification"
	AssertionNoExternalWrite       = "no_external_write"
	AssertionTraceHasEvent         = "trace_has_event"
)

// CaseStateChange tracks state transitions with audit trail
type CaseStateChange struct {
	FromState GoldenCaseState `json:"from_state"`
	ToState   GoldenCaseState `json:"to_state"`
	Reason    string          `json:"reason"`
	Reviewer  string          `json:"reviewer"`
	Timestamp time.Time       `json:"timestamp"`
}

// PromoteCandidateToGoldenDraft converts a candidate to a golden case draft
// with redaction, deduplication, and required metadata
func PromoteCandidateToGoldenDraft(candidate CandidateRecord) (Case, error) {
	if candidate.ID == "" {
		return Case{}, fmt.Errorf("candidate id is required")
	}
	if candidate.Case.Input == "" {
		return Case{}, fmt.Errorf("candidate input is required")
	}

	// Redact secrets from input
	redactedInput := RedactSecrets(candidate.Case.Input)

	// Build golden case from candidate
	goldenCase := Case{
		ID:             generateGoldenCaseID(candidate),
		Name:           sanitizeCaseName(candidate.Case.Name),
		Route:          candidate.Route,
		Input:          redactedInput,
		ExpectedTools:  candidate.Case.ExpectedTools,
		AllowedTools:   candidate.Case.AllowedTools,
		ExpectedSkills: candidate.Case.ExpectedSkills,
		ExpectedAgents: candidate.Case.ExpectedAgents,
		Scenario:       candidate.Case.Scenario,
		ExpectedStatus: candidate.Case.ExpectedStatus,
		FailureType:    candidate.FailureType,
		Risk:           candidate.Risk,
		Required:       false, // Draft cases are not required by default
		Notes:          buildGoldenCaseNotes(candidate),

		// Extended fields for Phase 3
		DomainID:         extractDomainID(candidate),
		SourceKind:       string(inferSourceKind(candidate)),
		SourceName:       extractSourceName(candidate),
		CreatedFrom:      candidate.ID,
		State:            string(GoldenCaseStateDraft),
		EvidenceLevelMin: string(EvidenceRealRunner), // Golden cases require real execution
		Tags:             buildCaseTags(candidate),
		Assertions:       buildCaseAssertions(candidate),
	}

	// Validate the golden case
	if err := ValidateGoldenCaseDraft(goldenCase); err != nil {
		return Case{}, fmt.Errorf("invalid golden case draft: %w", err)
	}

	return goldenCase, nil
}

// MergeDuplicateCandidate merges a new candidate into an existing case draft
func MergeDuplicateCandidate(existing Case, newCandidate CandidateRecord) error {
	if existing.ID == "" {
		return fmt.Errorf("existing case id is required")
	}
	if newCandidate.ID == "" {
		return fmt.Errorf("new candidate id is required")
	}

	// Check if fingerprints match (deduplication logic)
	if existing.CreatedFrom != "" && existing.CreatedFrom == newCandidate.ID {
		return fmt.Errorf("candidate %s already merged into case %s", newCandidate.ID, existing.ID)
	}

	// Merge logic: update notes to track duplicate
	mergeNote := fmt.Sprintf("\n[Merged duplicate candidate %s at %s]",
		newCandidate.ID, time.Now().Format(time.RFC3339))

	// In a real implementation, this would update the case in the store
	// For now, we validate the merge is possible
	if existing.State != string(GoldenCaseStateDraft) {
		return fmt.Errorf("can only merge into draft cases, got state %s", existing.State)
	}

	_ = mergeNote // Would be appended to existing.Notes in real implementation
	return nil
}

// CreateCandidateFromRollbackIncident creates a high-priority candidate from rollback
func CreateCandidateFromRollbackIncident(incident RollbackRecord) CandidateRecord {
	now := time.Now()

	// Extract information from rollback incident
	fingerprint := fmt.Sprintf("sha256:rollback_%s", safeLifecycleIDSegment(incident.ID))

	candidate := Candidate{
		ID:             "candidate_rollback_" + incident.ID,
		Name:           fmt.Sprintf("回滚事故回归候选 %s", incident.SuggestionID),
		Route:          "unknown", // Would be extracted from suggestion context
		Input:          fmt.Sprintf("Rollback incident for suggestion %s", incident.SuggestionID),
		ExpectedStatus: StatusFail, // Rollback indicates failure
		FailureType:    FailureRuntime,
		Risk:           "dangerous",
		Required:       true, // Rollback incidents are high priority
		Notes:          fmt.Sprintf("由回滚事故生成，触发原因: %s，必须立即 review 并加入 regression suite", incident.Trigger),
	}

	rec := CandidateRecord{
		ID:          candidate.ID,
		Status:      CandidateNew,
		Route:       candidate.Route,
		SessionID:   "", // Not available from rollback
		ReplayRef:   "", // Not available from rollback
		Input:       candidate.Input,
		Case:        candidate,
		FailureType: FailureRuntime,
		Risk:        "dangerous",
		Fingerprint: fingerprint,
		SourceEvent: Event{
			Name:        EventBudgetExit, // Placeholder for rollback event
			FailureType: FailureRuntime,
			FinalStatus: StatusFail,
		},
		CreatedBy: incident.TriggeredBy,
		CreatedAt: now,
		UpdatedAt: now,
	}

	rec.Suggestions = BuildOptimizationSuggestions(rec)
	return rec
}

// CreateCandidateFromApprovalRejection creates a candidate from rejected approval
func CreateCandidateFromApprovalRejection(approval ApprovalRecord) CandidateRecord {
	now := time.Now()

	if approval.Action != ApprovalActionReject {
		// Only rejections create regression candidates
		return CandidateRecord{}
	}

	fingerprint := fmt.Sprintf("sha256:approval_reject_%s", safeLifecycleIDSegment(approval.ID))

	candidate := Candidate{
		ID:             "candidate_approval_" + approval.ID,
		Name:           fmt.Sprintf("审批拒绝回归候选 %s", approval.SubjectID),
		Route:          "unknown",
		Input:          fmt.Sprintf("Approval rejection for %s: %s", approval.SubjectType, approval.Note),
		ExpectedStatus: StatusFail,
		FailureType:    FailurePermission,
		Risk:           "dangerous",
		Required:       false,
		Notes:          fmt.Sprintf("由审批拒绝生成，审批人: %s (%s)，原因: %s", approval.Reviewer, approval.ReviewerRole, approval.Note),
	}

	rec := CandidateRecord{
		ID:          candidate.ID,
		Status:      CandidateNew,
		Route:       candidate.Route,
		SessionID:   "",
		ReplayRef:   "",
		Input:       candidate.Input,
		Case:        candidate,
		FailureType: FailurePermission,
		Risk:        "dangerous",
		Fingerprint: fingerprint,
		SourceEvent: Event{
			Name:        EventPermissionDecision,
			FailureType: FailurePermission,
			FinalStatus: StatusBlocked,
		},
		CreatedBy:  approval.Reviewer,
		ReviewedBy: approval.Reviewer,
		CreatedAt:  now,
		UpdatedAt:  now,
	}

	rec.Suggestions = BuildOptimizationSuggestions(rec)
	return rec
}

func safeLifecycleIDSegment(id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return "unknown"
	}
	if len(id) > 16 {
		return id[:16]
	}
	return id
}

// ValidateGoldenCaseDraft validates a golden case draft
func ValidateGoldenCaseDraft(c Case) error {
	// Basic case validation
	if err := ValidateCase(c); err != nil {
		return err
	}

	// Draft-specific validation (relaxed)
	if c.State != string(GoldenCaseStateDraft) {
		return fmt.Errorf("expected draft state, got %s", c.State)
	}

	return nil
}

// ValidateActiveGoldenCase validates an active golden case
func ValidateActiveGoldenCase(c Case) error {
	// Basic case validation
	if err := ValidateCase(c); err != nil {
		return err
	}

	// Active case requirements
	if c.State != string(GoldenCaseStateActive) {
		return fmt.Errorf("expected active state, got %s", c.State)
	}

	if c.DomainID == "" {
		return fmt.Errorf("active golden case must have domain_id")
	}

	if c.EvidenceLevelMin == "" {
		return fmt.Errorf("active golden case must have evidence_level_min")
	}

	// Validate evidence level
	switch RunnerEvidenceLevel(c.EvidenceLevelMin) {
	case EvidenceStaticSchema, EvidenceReplayTrace:
		return fmt.Errorf("active golden case requires at least simulated_runner evidence, got %s", c.EvidenceLevelMin)
	case EvidenceSimulatedRunner, EvidenceRealRunner, EvidenceProductionShadow:
		// Valid
	default:
		return fmt.Errorf("invalid evidence_level_min: %s", c.EvidenceLevelMin)
	}

	if len(c.Assertions) == 0 {
		return fmt.Errorf("active golden case must have at least one assertion")
	}

	return nil
}

// Helper functions

func generateGoldenCaseID(candidate CandidateRecord) string {
	// Generate a stable ID from candidate
	baseID := strings.TrimPrefix(candidate.ID, "candidate_")
	return "golden_" + baseID
}

func sanitizeCaseName(name string) string {
	// Remove "候选" prefix and clean up
	name = strings.TrimSpace(name)
	name = strings.ReplaceAll(name, "失败回归候选", "回归用例")
	name = strings.ReplaceAll(name, "候选", "")
	return strings.TrimSpace(name)
}

func buildGoldenCaseNotes(candidate CandidateRecord) string {
	notes := []string{
		fmt.Sprintf("从候选 %s 晋升为 golden case", candidate.ID),
		fmt.Sprintf("原始失败类型: %s", candidate.FailureType),
	}

	if candidate.ReplayRef != "" {
		notes = append(notes, fmt.Sprintf("Replay ref: %s", candidate.ReplayRef))
	}

	if candidate.ReviewNote != "" {
		notes = append(notes, fmt.Sprintf("Review note: %s", candidate.ReviewNote))
	}

	return strings.Join(notes, "\n")
}

func extractDomainID(candidate CandidateRecord) string {
	// Extract domain from source event
	if candidate.SourceEvent.DomainID != "" {
		return candidate.SourceEvent.DomainID
	}

	// Infer from route
	if candidate.SourceEvent.RouteDecision.Domain != "" {
		return candidate.SourceEvent.RouteDecision.Domain
	}

	return "generic"
}

func inferSourceKind(candidate CandidateRecord) SourceKind {
	// Check if created from rollback
	if strings.Contains(candidate.ID, "rollback") {
		return SourceRollbackIncident
	}

	// Check if created from approval rejection
	if strings.Contains(candidate.ID, "approval") {
		return SourceApprovalRejection
	}

	// Check source event type
	switch candidate.SourceEvent.Name {
	case EventReflection:
		return SourceQualityEvent
	default:
		return SourceQualityEvent
	}
}

func extractSourceName(candidate CandidateRecord) string {
	if candidate.SourceEvent.Name != "" {
		return string(candidate.SourceEvent.Name)
	}
	return "unknown"
}

func buildCaseTags(candidate CandidateRecord) []string {
	tags := []string{}

	// Add failure type tag
	if candidate.FailureType != FailureNone {
		tags = append(tags, string(candidate.FailureType))
	}

	// Add risk tag
	if candidate.Risk != "" {
		tags = append(tags, candidate.Risk)
	}

	// Add source tag
	tags = append(tags, string(inferSourceKind(candidate)))

	return tags
}

func buildCaseAssertions(candidate CandidateRecord) []CaseAssertion {
	assertions := []CaseAssertion{}

	// Add status assertion
	if candidate.Case.ExpectedStatus != "" {
		assertions = append(assertions, CaseAssertion{
			Type:     AssertionStatusIs,
			Expected: string(candidate.Case.ExpectedStatus),
			Message:  fmt.Sprintf("Final status must be %s", candidate.Case.ExpectedStatus),
		})
	}

	// Add tool assertions
	for _, tool := range candidate.Case.ExpectedTools {
		assertions = append(assertions, CaseAssertion{
			Type:    AssertionToolCalled,
			Target:  tool,
			Message: fmt.Sprintf("Must call tool %s", tool),
		})
	}

	// Add failure type assertion if applicable
	if candidate.FailureType != FailureNone {
		assertions = append(assertions, CaseAssertion{
			Type:     AssertionTraceHasEvent,
			Target:   string(candidate.SourceEvent.Name),
			Expected: string(candidate.FailureType),
			Message:  fmt.Sprintf("Must detect %s failure", candidate.FailureType),
		})
	}

	return assertions
}

// RedactSecrets removes sensitive information from input
func RedactSecrets(input string) string {
	// Redact common secret patterns
	patterns := []struct {
		pattern     *regexp.Regexp
		replacement string
	}{
		{regexp.MustCompile(`(?i)(token|key|password|secret|credential)\s*[:=]\s*['"]?([^'"\s]+)['"]?`), "${1}=REDACTED"},
		{regexp.MustCompile(`(?i)bearer\s+[a-zA-Z0-9_\-\.]+`), "bearer REDACTED"},
		{regexp.MustCompile(`(?i)api[_-]?key\s*[:=]\s*['"]?([^'"\s]+)['"]?`), "api_key=REDACTED"},
		{regexp.MustCompile(`(?i)(sk|pk)[-_][a-zA-Z0-9]{20,}`), "REDACTED_KEY"},
		{regexp.MustCompile(`[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}`), "user@REDACTED.com"},
	}

	redacted := input
	for _, p := range patterns {
		redacted = p.pattern.ReplaceAllString(redacted, p.replacement)
	}

	return redacted
}
