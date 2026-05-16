package agentquality

import (
	"strings"
	"testing"
	"time"
)

func TestCandidatePromotesToGoldenCaseDraftWithRedaction(t *testing.T) {
	candidate := CandidateRecord{
		ID:     "candidate_abc123",
		Status: CandidateApproved,
		Route:  "customer_service",
		Input:  "用户输入包含 token=sk-abc123456789012345678 和 api_key=secret_value",
		Case: Candidate{
			ID:             "candidate_abc123",
			Name:           "失败回归候选 abc123",
			Route:          "customer_service",
			Input:          "用户输入包含 token=sk-abc123456789012345678 和 api_key=secret_value",
			ExpectedTools:  []string{"search_knowledge_base"},
			ExpectedStatus: StatusFail,
			FailureType:    FailureTool,
			Risk:           "safe",
		},
		FailureType: FailureTool,
		Risk:        "safe",
		Fingerprint: "sha256:abc123",
		ReplayRef:   "replay_ref_001",
		SourceEvent: Event{
			Name:        EventToolDecision,
			DomainID:    "customer_service",
			FailureType: FailureTool,
			FinalStatus: StatusFail,
		},
		ReviewNote: "确认为工具调用失败",
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}

	goldenCase, err := PromoteCandidateToGoldenDraft(candidate)
	if err != nil {
		t.Fatalf("PromoteCandidateToGoldenDraft failed: %v", err)
	}

	// Verify state is draft
	if goldenCase.State != string(GoldenCaseStateDraft) {
		t.Errorf("expected state %s, got %s", GoldenCaseStateDraft, goldenCase.State)
	}

	// Verify secrets are redacted
	if strings.Contains(goldenCase.Input, "sk-abc123456789012345678") {
		t.Error("input should have redacted the API key")
	}
	if strings.Contains(goldenCase.Input, "secret_value") {
		t.Error("input should have redacted the api_key value")
	}

	// Verify domain is set
	if goldenCase.DomainID != "customer_service" {
		t.Errorf("expected domain_id %q, got %q", "customer_service", goldenCase.DomainID)
	}

	// Verify source tracking
	if goldenCase.SourceKind != string(SourceQualityEvent) {
		t.Errorf("expected source_kind %q, got %q", SourceQualityEvent, goldenCase.SourceKind)
	}

	// Verify created_from references candidate
	if goldenCase.CreatedFrom != candidate.ID {
		t.Errorf("expected created_from %q, got %q", candidate.ID, goldenCase.CreatedFrom)
	}

	// Verify evidence level minimum
	if goldenCase.EvidenceLevelMin != string(EvidenceRealRunner) {
		t.Errorf("expected evidence_level_min %q, got %q", EvidenceRealRunner, goldenCase.EvidenceLevelMin)
	}

	// Verify assertions are generated
	if len(goldenCase.Assertions) == 0 {
		t.Error("expected at least one assertion")
	}

	// Verify ID format
	if !strings.HasPrefix(goldenCase.ID, "golden_") {
		t.Errorf("expected golden case ID to start with 'golden_', got %q", goldenCase.ID)
	}

	// Verify name is sanitized
	if strings.Contains(goldenCase.Name, "失败回归候选") {
		t.Error("golden case name should not contain '失败回归候选'")
	}
}

func TestDuplicateCandidateMergesIntoExistingCaseDraft(t *testing.T) {
	existingCase := Case{
		ID:          "golden_abc123",
		Name:        "回归用例 abc123",
		Route:       "customer_service",
		Input:       "用户查询知识库",
		ExpectedStatus: StatusFail,
		FailureType: FailureTool,
		Risk:        "safe",
		State:       string(GoldenCaseStateDraft),
		DomainID:    "customer_service",
		CreatedFrom: "candidate_abc123",
	}

	newCandidate := CandidateRecord{
		ID:          "candidate_def456",
		Status:      CandidateNew,
		Route:       "customer_service",
		Input:       "类似的用户查询",
		Fingerprint: "sha256:def456",
		Case: Candidate{
			ID:             "candidate_def456",
			Name:           "失败回归候选 def456",
			Route:          "customer_service",
			Input:          "类似的用户查询",
			ExpectedStatus: StatusFail,
			FailureType:    FailureTool,
		},
		FailureType: FailureTool,
	}

	// Merge should succeed for draft cases
	err := MergeDuplicateCandidate(existingCase, newCandidate)
	if err != nil {
		t.Fatalf("MergeDuplicateCandidate failed: %v", err)
	}

	// Merge should fail for non-draft cases
	activeCase := existingCase
	activeCase.State = string(GoldenCaseStateActive)
	err = MergeDuplicateCandidate(activeCase, newCandidate)
	if err == nil {
		t.Error("expected error when merging into active case")
	}
	if !strings.Contains(err.Error(), "draft") {
		t.Errorf("error should mention draft state, got: %v", err)
	}

	// Merge should fail for same candidate
	sameCandidate := newCandidate
	sameCandidate.ID = "candidate_abc123"
	existingCase.CreatedFrom = "candidate_abc123"
	err = MergeDuplicateCandidate(existingCase, sameCandidate)
	if err == nil {
		t.Error("expected error when merging same candidate")
	}
	if !strings.Contains(err.Error(), "already merged") {
		t.Errorf("error should mention already merged, got: %v", err)
	}
}

func TestRollbackIncidentCreatesHighPriorityCandidate(t *testing.T) {
	incident := RollbackRecord{
		ID:           "rollback_suggestion_001_manual_admin",
		SuggestionID: "suggestion_001",
		AlertID:      "alert_001",
		Trigger:      RollbackTriggerManual,
		TriggeredBy:  "admin_user",
		CreatedAt:    time.Now(),
		Rollout: OptimizationRollout{
			ID:           "rollout_001",
			SuggestionID: "suggestion_001",
		},
	}

	rec := CreateCandidateFromRollbackIncident(incident)

	// Verify high priority
	if !rec.Case.Required {
		t.Error("rollback incident candidate should be required (high priority)")
	}

	// Verify risk is dangerous
	if rec.Risk != "dangerous" {
		t.Errorf("expected risk 'dangerous', got %q", rec.Risk)
	}

	// Verify failure type
	if rec.FailureType != FailureRuntime {
		t.Errorf("expected failure type %q, got %q", FailureRuntime, rec.FailureType)
	}

	// Verify ID contains rollback reference
	if !strings.Contains(rec.ID, "rollback") {
		t.Errorf("expected ID to contain 'rollback', got %q", rec.ID)
	}

	// Verify status is new
	if rec.Status != CandidateNew {
		t.Errorf("expected status %q, got %q", CandidateNew, rec.Status)
	}

	// Verify created_by is set
	if rec.CreatedBy != "admin_user" {
		t.Errorf("expected created_by %q, got %q", "admin_user", rec.CreatedBy)
	}

	// Verify notes mention rollback
	if !strings.Contains(rec.Case.Notes, "回滚事故") {
		t.Error("notes should mention rollback incident")
	}
}

func TestApprovalRejectionCanCreateRegressionCandidate(t *testing.T) {
	approval := ApprovalRecord{
		ID:           "approval_001_reject",
		SubjectID:    "suggestion_002",
		SubjectType:  ApprovalSubjectSuggestion,
		Action:       ApprovalActionReject,
		Reviewer:     "lead_reviewer",
		ReviewerRole: ApprovalRoleLead,
		Note:         "该优化建议可能导致工具权限泄漏",
		CreatedAt:    time.Now(),
	}

	rec := CreateCandidateFromApprovalRejection(approval)

	// Verify candidate is created
	if rec.ID == "" {
		t.Fatal("expected non-empty candidate ID")
	}

	// Verify ID contains approval reference
	if !strings.Contains(rec.ID, "approval") {
		t.Errorf("expected ID to contain 'approval', got %q", rec.ID)
	}

	// Verify failure type is permission
	if rec.FailureType != FailurePermission {
		t.Errorf("expected failure type %q, got %q", FailurePermission, rec.FailureType)
	}

	// Verify risk is dangerous
	if rec.Risk != "dangerous" {
		t.Errorf("expected risk 'dangerous', got %q", rec.Risk)
	}

	// Verify created_by is the reviewer
	if rec.CreatedBy != "lead_reviewer" {
		t.Errorf("expected created_by %q, got %q", "lead_reviewer", rec.CreatedBy)
	}

	// Verify notes contain reviewer info
	if !strings.Contains(rec.Case.Notes, "lead_reviewer") {
		t.Error("notes should contain reviewer name")
	}
	if !strings.Contains(rec.Case.Notes, "工具权限泄漏") {
		t.Error("notes should contain rejection reason")
	}

	// Verify non-rejection does not create candidate
	approveAction := approval
	approveAction.Action = ApprovalActionApprove
	emptyRec := CreateCandidateFromApprovalRejection(approveAction)
	if emptyRec.ID != "" {
		t.Error("approve action should not create a candidate")
	}
}

func TestActiveGoldenCaseRequiresDomainAndAssertions(t *testing.T) {
	// Valid active golden case
	validCase := Case{
		ID:             "golden_test_001",
		Name:           "Active golden case",
		Route:          "customer_service",
		Input:          "用户查询",
		ExpectedStatus: StatusFail,
		FailureType:    FailureTool,
		Risk:           "safe",
		State:          string(GoldenCaseStateActive),
		DomainID:       "customer_service",
		EvidenceLevelMin: string(EvidenceRealRunner),
		Assertions: []CaseAssertion{
			{Type: AssertionToolCalled, Target: "search_kb", Message: "Must call search_kb"},
		},
	}

	err := ValidateActiveGoldenCase(validCase)
	if err != nil {
		t.Fatalf("valid active golden case should pass validation: %v", err)
	}

	// Missing domain
	noDomain := validCase
	noDomain.DomainID = ""
	err = ValidateActiveGoldenCase(noDomain)
	if err == nil {
		t.Error("expected error for missing domain_id")
	}
	if !strings.Contains(err.Error(), "domain_id") {
		t.Errorf("error should mention domain_id, got: %v", err)
	}

	// Missing assertions
	noAssertions := validCase
	noAssertions.Assertions = nil
	err = ValidateActiveGoldenCase(noAssertions)
	if err == nil {
		t.Error("expected error for missing assertions")
	}
	if !strings.Contains(err.Error(), "assertion") {
		t.Errorf("error should mention assertion, got: %v", err)
	}

	// Missing evidence level
	noEvidence := validCase
	noEvidence.EvidenceLevelMin = ""
	err = ValidateActiveGoldenCase(noEvidence)
	if err == nil {
		t.Error("expected error for missing evidence_level_min")
	}

	// Static schema evidence is insufficient
	staticEvidence := validCase
	staticEvidence.EvidenceLevelMin = string(EvidenceStaticSchema)
	err = ValidateActiveGoldenCase(staticEvidence)
	if err == nil {
		t.Error("expected error for static_schema evidence level")
	}
	if !strings.Contains(err.Error(), "simulated_runner") {
		t.Errorf("error should mention minimum required level, got: %v", err)
	}

	// Wrong state
	wrongState := validCase
	wrongState.State = string(GoldenCaseStateDraft)
	err = ValidateActiveGoldenCase(wrongState)
	if err == nil {
		t.Error("expected error for non-active state")
	}
}

func TestRedactSecretsRemovesSensitiveData(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		contains []string
		absent   []string
	}{
		{
			name:   "bearer token",
			input:  "Authorization: bearer eyJhbGciOiJIUzI1NiJ9.test",
			absent: []string{"eyJhbGciOiJIUzI1NiJ9.test"},
		},
		{
			name:   "api key",
			input:  "api_key=sk_live_abc123def456ghi789",
			absent: []string{"sk_live_abc123def456ghi789"},
		},
		{
			name:   "password field",
			input:  "password: my_secret_pass123",
			absent: []string{"my_secret_pass123"},
		},
		{
			name:     "normal text preserved",
			input:    "用户想查询订单状态",
			contains: []string{"用户想查询订单状态"},
		},
		{
			name:   "sk- prefixed key",
			input:  "使用 sk-abcdefghijklmnopqrstuvwxyz 调用 API",
			absent: []string{"sk-abcdefghijklmnopqrstuvwxyz"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := RedactSecrets(tt.input)
			for _, s := range tt.contains {
				if !strings.Contains(result, s) {
					t.Errorf("expected result to contain %q, got %q", s, result)
				}
			}
			for _, s := range tt.absent {
				if strings.Contains(result, s) {
					t.Errorf("expected result to NOT contain %q, got %q", s, result)
				}
			}
		})
	}
}

func TestCaseLifecycleStateTransitions(t *testing.T) {
	// Verify golden case state constants are valid
	states := []GoldenCaseState{
		GoldenCaseStateDraft,
		GoldenCaseStateActive,
		GoldenCaseStateQuarantined,
		GoldenCaseStateDeprecated,
	}

	for _, state := range states {
		if state == "" {
			t.Error("golden case state should not be empty")
		}
	}

	// Verify source kind constants
	sources := []SourceKind{
		SourceQualityEvent,
		SourceUserFeedback,
		SourceApprovalRejection,
		SourceRollbackIncident,
		SourceManual,
	}

	for _, source := range sources {
		if source == "" {
			t.Error("source kind should not be empty")
		}
	}
}

func TestCaseAssertionTypes(t *testing.T) {
	assertionTypes := []string{
		AssertionToolCalled,
		AssertionToolNotCalled,
		AssertionStatusIs,
		AssertionAnswerContains,
		AssertionAnswerNotContains,
		AssertionRequiresClarification,
		AssertionNoExternalWrite,
		AssertionTraceHasEvent,
	}

	seen := map[string]bool{}
	for _, at := range assertionTypes {
		if at == "" {
			t.Error("assertion type should not be empty")
		}
		if seen[at] {
			t.Errorf("duplicate assertion type: %s", at)
		}
		seen[at] = true
	}
}
