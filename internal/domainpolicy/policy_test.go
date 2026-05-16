package domainpolicy

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/chef-guo/agents-hive/internal/router"
)

func TestBusinessDomainCannotGrantToolByKeyword(t *testing.T) {
	decision := Admit(AdmissionInput{
		DomainID:       DomainSkillAuthoring,
		Intent:         router.IntentFrame{Kind: router.IntentCreateSkill},
		Capabilities:   []router.Capability{router.CapabilityMetaSkillCreate},
		HasEvalCorpus:  true,
		TriggerKeyword: "创建技能",
	})
	if decision.Allowed || decision.Reason != "trigger_keyword_cannot_grant_domain" {
		t.Fatalf("decision = %+v, want keyword denial", decision)
	}
}

func TestBusinessDomainCapabilityRequiresDomainPolicy(t *testing.T) {
	decision := Admit(AdmissionInput{
		DomainID:      DomainSkillAuthoring,
		Intent:        router.IntentFrame{Kind: router.IntentCreateSkill},
		HasEvalCorpus: true,
	})
	if decision.Allowed || decision.Reason != "capability_required_for_domain" {
		t.Fatalf("decision = %+v, want missing capability denial", decision)
	}
}

func TestBusinessDomainRouteEvalGateFailsWithoutCorpus(t *testing.T) {
	decision := Admit(AdmissionInput{
		DomainID:     DomainMCPServerBuilding,
		Intent:       router.IntentFrame{Kind: router.IntentManageTool},
		Capabilities: []router.Capability{router.CapabilityMetaToolRegister},
	})
	if decision.Allowed || decision.Reason != "domain_eval_corpus_required" {
		t.Fatalf("decision = %+v, want corpus denial", decision)
	}
}

func TestBusinessDomainAllowsValidPolicy(t *testing.T) {
	decision := Admit(AdmissionInput{
		DomainID:      DomainMCPServerBuilding,
		Intent:        router.IntentFrame{Kind: router.IntentManageTool},
		Capabilities:  []router.Capability{router.CapabilityMetaToolRegister},
		HasEvalCorpus: true,
	})
	if !decision.Allowed {
		t.Fatalf("decision = %+v, want allowed", decision)
	}
}

func TestBusinessDomainFromIntentUsesExplicitDomainOnly(t *testing.T) {
	intent := router.IntentFrame{
		Kind:     router.IntentExternalWrite,
		DomainID: string(DomainCustomerService),
		Subject:  "customer_service keyword in user text must not matter",
	}
	if got := DomainIDFromIntent(intent); got != DomainCustomerService {
		t.Fatalf("DomainIDFromIntent = %q, want %q", got, DomainCustomerService)
	}
	if got := DomainIDFromIntent(router.IntentFrame{Subject: string(DomainCustomerService)}); got != DomainGeneric {
		t.Fatalf("DomainIDFromIntent without explicit DomainID = %q, want generic", got)
	}
}

func TestCustomerServiceDomainDisabledEvenWithExternalSendCapability(t *testing.T) {
	decision := Admit(AdmissionInput{
		DomainID:      DomainCustomerService,
		Intent:        router.IntentFrame{Kind: router.IntentExternalWrite, DomainID: string(DomainCustomerService)},
		Capabilities:  []router.Capability{router.CapabilityExternalSend},
		HasEvalCorpus: true,
	})
	if decision.Allowed || decision.Reason != "domain_disabled" {
		t.Fatalf("decision = %+v, want disabled customer_service domain", decision)
	}
}

func TestAdmitConsumesDomainAdmissionMetrics(t *testing.T) {
	decision := Admit(AdmissionInput{
		DomainID:      DomainCustomerService,
		Intent:        router.IntentFrame{Kind: router.IntentExternalWrite, DomainID: string(DomainCustomerService)},
		Capabilities:  []router.Capability{router.CapabilityExternalSend},
		HasEvalCorpus: true,
		QualityMetrics: &QualityMetrics{
			ActiveGoldenCases: 10,
			SafetyCaseIDs: []string{
				"cs_safety_no_pii_leak",
				"cs_safety_permission_required",
				"cs_safety_external_write_guard",
			},
			LatestEvidenceLevel: "real_runner",
			SemanticScore:       0.85,
			JudgeCoverage:       0.8,
		},
	})
	if !decision.Allowed || decision.Reason != "domain_policy_allowed" {
		t.Fatalf("decision = %+v, want domain policy allowed after admission metrics pass", decision)
	}
}

func TestAdmitFailsWhenDomainAdmissionMetricsFail(t *testing.T) {
	decision := Admit(AdmissionInput{
		DomainID:      DomainCustomerService,
		Intent:        router.IntentFrame{Kind: router.IntentExternalWrite, DomainID: string(DomainCustomerService)},
		Capabilities:  []router.Capability{router.CapabilityExternalSend},
		HasEvalCorpus: true,
		QualityMetrics: &QualityMetrics{
			ActiveGoldenCases:   5,
			LatestEvidenceLevel: "real_runner",
			SemanticScore:       0.9,
			JudgeCoverage:       0.8,
		},
	})
	if decision.Allowed || decision.Reason != "domain_admission_failed" {
		t.Fatalf("decision = %+v, want domain admission failure", decision)
	}
	if !strings.Contains(decision.Detail, "active golden cases") {
		t.Fatalf("decision detail = %q, want active golden cases failure", decision.Detail)
	}
}

// Phase 4: Domain admission tests

func TestDomainAdmissionFailsWithoutRegressionSuite(t *testing.T) {
	policy := Policy{
		DomainID:             "test_domain",
		RegressionSuiteID:    "",
		MinActiveGoldenCases: 5,
	}
	metrics := QualityMetrics{
		ActiveGoldenCases:   10,
		LatestEvidenceLevel: "real_runner",
		SemanticScore:       0.9,
	}
	err := CheckDomainAdmission(policy, metrics)
	if err == nil || err.Error() != "domain test_domain: regression_suite_id not declared" {
		t.Fatalf("CheckDomainAdmission = %v, want regression_suite_id error", err)
	}
}

func TestDomainAdmissionFailsWithOnlyStaticEvidence(t *testing.T) {
	policy := Policy{
		DomainID:             "test_domain",
		RegressionSuiteID:    "test_suite",
		MinActiveGoldenCases: 5,
		MinRunnerEvidence:    "real_runner",
	}
	metrics := QualityMetrics{
		ActiveGoldenCases:   10,
		LatestEvidenceLevel: "static_schema",
		SemanticScore:       0.9,
	}
	err := CheckDomainAdmission(policy, metrics)
	if err == nil {
		t.Fatalf("CheckDomainAdmission = nil, want static_schema rejection")
	}
	if err.Error() != `domain test_domain: latest evidence level "static_schema" does not meet minimum "real_runner"` {
		t.Fatalf("CheckDomainAdmission error = %v, want static_schema rejection", err)
	}
}

func TestDomainAdmissionFailsWhenEvidenceBelowMinimum(t *testing.T) {
	policy := Policy{
		DomainID:             "test_domain",
		RegressionSuiteID:    "test_suite",
		MinActiveGoldenCases: 5,
		MinRunnerEvidence:    "real_runner",
	}
	metrics := QualityMetrics{
		ActiveGoldenCases:   10,
		LatestEvidenceLevel: "simulated_runner",
		SemanticScore:       0.9,
	}
	err := CheckDomainAdmission(policy, metrics)
	if err == nil {
		t.Fatalf("CheckDomainAdmission = nil, want low evidence rejection")
	}
	if err.Error() != `domain test_domain: latest evidence level "simulated_runner" does not meet minimum "real_runner"` {
		t.Fatalf("CheckDomainAdmission error = %v, want low evidence rejection", err)
	}
}

func TestDomainAdmissionPassesWithRealRunnerEvidence(t *testing.T) {
	policy := Policy{
		DomainID:             "test_domain",
		RegressionSuiteID:    "test_suite",
		MinActiveGoldenCases: 5,
		MinRunnerEvidence:    "real_runner",
	}
	metrics := QualityMetrics{
		ActiveGoldenCases:   10,
		LatestEvidenceLevel: "real_runner",
		SemanticScore:       0.9,
	}
	err := CheckDomainAdmission(policy, metrics)
	if err != nil {
		t.Fatalf("CheckDomainAdmission = %v, want nil", err)
	}
}

func TestDomainAdmissionRequiresSafetyCases(t *testing.T) {
	policy := Policy{
		DomainID:             "test_domain",
		RegressionSuiteID:    "test_suite",
		MinActiveGoldenCases: 5,
		RequiredSafetyCases:  []string{"safety_case_1", "safety_case_2"},
		MinRunnerEvidence:    "real_runner",
	}
	metrics := QualityMetrics{
		ActiveGoldenCases:   10,
		SafetyCaseIDs:       []string{"safety_case_1"},
		LatestEvidenceLevel: "real_runner",
		SemanticScore:       0.9,
	}
	err := CheckDomainAdmission(policy, metrics)
	if err == nil {
		t.Fatalf("CheckDomainAdmission = nil, want missing safety cases error")
	}
	if err.Error() != "domain test_domain: missing required safety cases: [safety_case_2]" {
		t.Fatalf("CheckDomainAdmission error = %v, want missing safety cases", err)
	}
}

func TestCustomerServiceRemainsDisabledUntilGatePasses(t *testing.T) {
	policy, ok := GetBuiltinPolicy(DomainCustomerService)
	if !ok {
		t.Fatal("customer_service policy not found")
	}
	if policy.Enabled {
		t.Fatal("customer_service should be disabled by default")
	}

	// 不满足 regression suite 要求
	metrics := QualityMetrics{
		ActiveGoldenCases:   5, // 少于 MinActiveGoldenCases (10)
		SafetyCaseIDs:       []string{"cs_safety_no_pii_leak"},
		LatestEvidenceLevel: "real_runner",
		SemanticScore:       0.9,
	}
	err := CheckDomainAdmission(policy, metrics)
	if err == nil {
		t.Fatal("CheckDomainAdmission should fail with insufficient golden cases")
	}

	// 满足所有要求
	metrics = QualityMetrics{
		ActiveGoldenCases: 10,
		SafetyCaseIDs: []string{
			"cs_safety_no_pii_leak",
			"cs_safety_permission_required",
			"cs_safety_external_write_guard",
		},
		LatestEvidenceLevel: "real_runner",
		SemanticScore:       0.85,
		P0SafetyFailures:    0,
		P1SafetyFailures:    0,
		JudgeCoverage:       0.8,
	}
	err = CheckDomainAdmission(policy, metrics)
	if err != nil {
		t.Fatalf("CheckDomainAdmission = %v, want nil (pass)", err)
	}
}

func TestAdmissionFromProviderCallsProviderAndDrivesAdmit(t *testing.T) {
	provider := &recordingQualityProvider{
		metrics: QualityMetrics{
			ActiveGoldenCases: 10,
			SafetyCaseIDs: []string{
				"cs_safety_no_pii_leak",
				"cs_safety_permission_required",
				"cs_safety_external_write_guard",
			},
			LatestEvidenceLevel: "real_runner",
			SemanticScore:       0.9,
			JudgeCoverage:       0.85,
		},
	}

	decision := AdmissionFromProvider(context.Background(), AdmissionInput{
		DomainID:      DomainCustomerService,
		Intent:        router.IntentFrame{Kind: router.IntentExternalWrite, DomainID: string(DomainCustomerService)},
		Capabilities:  []router.Capability{router.CapabilityExternalSend},
		HasEvalCorpus: true,
	}, provider)
	if !decision.Allowed || decision.Reason != "domain_policy_allowed" {
		t.Fatalf("decision = %+v, want allowed from provider metrics", decision)
	}
	if provider.calls != 1 {
		t.Fatalf("provider calls = %d, want 1", provider.calls)
	}
	if provider.domain != DomainCustomerService {
		t.Fatalf("provider domain = %q, want %q", provider.domain, DomainCustomerService)
	}
	if provider.policy.RegressionSuiteID != "customer_service_regression_v1" {
		t.Fatalf("provider policy = %+v, want customer_service regression policy", provider.policy)
	}
}

func TestAdmissionFromProviderUsesProviderMetricsForRejection(t *testing.T) {
	provider := &recordingQualityProvider{
		metrics: QualityMetrics{
			ActiveGoldenCases: 10,
			SafetyCaseIDs: []string{
				"cs_safety_no_pii_leak",
				"cs_safety_permission_required",
				"cs_safety_external_write_guard",
			},
			LatestEvidenceLevel: "replay_trace",
			SemanticScore:       0.9,
			JudgeCoverage:       0.85,
		},
	}

	decision := AdmissionFromProvider(context.Background(), AdmissionInput{
		DomainID:      DomainCustomerService,
		Intent:        router.IntentFrame{Kind: router.IntentExternalWrite, DomainID: string(DomainCustomerService)},
		Capabilities:  []router.Capability{router.CapabilityExternalSend},
		HasEvalCorpus: true,
	}, provider)
	if decision.Allowed || decision.Reason != "domain_admission_failed" {
		t.Fatalf("decision = %+v, want admission failure from provider metrics", decision)
	}
	if !strings.Contains(decision.Detail, "does not meet minimum") {
		t.Fatalf("decision detail = %q, want evidence failure", decision.Detail)
	}
	if provider.calls != 1 {
		t.Fatalf("provider calls = %d, want 1", provider.calls)
	}
}

func TestAdmissionFromProviderReportsUnavailableMetrics(t *testing.T) {
	provider := &recordingQualityProvider{err: errors.New("quality store unavailable")}
	decision := AdmissionFromProvider(context.Background(), AdmissionInput{
		DomainID:      DomainCustomerService,
		Intent:        router.IntentFrame{Kind: router.IntentExternalWrite, DomainID: string(DomainCustomerService)},
		Capabilities:  []router.Capability{router.CapabilityExternalSend},
		HasEvalCorpus: true,
	}, provider)
	if decision.Allowed || decision.Reason != "domain_quality_metrics_unavailable" {
		t.Fatalf("decision = %+v, want provider unavailable failure", decision)
	}
	if !strings.Contains(decision.Detail, "quality store unavailable") {
		t.Fatalf("decision detail = %q, want provider error", decision.Detail)
	}
}

func TestDomainAdmissionFailsWithP0SafetyFailures(t *testing.T) {
	policy := Policy{
		DomainID:             "test_domain",
		RegressionSuiteID:    "test_suite",
		MinActiveGoldenCases: 5,
		MinRunnerEvidence:    "real_runner",
	}
	metrics := QualityMetrics{
		ActiveGoldenCases:   10,
		LatestEvidenceLevel: "real_runner",
		SemanticScore:       0.9,
		P0SafetyFailures:    1,
	}
	err := CheckDomainAdmission(policy, metrics)
	if err == nil {
		t.Fatal("CheckDomainAdmission should fail with P0 safety failures")
	}
	if err.Error() != "domain test_domain: has 1 P0 safety failures" {
		t.Fatalf("CheckDomainAdmission error = %v, want P0 safety failure error", err)
	}
}

func TestDomainAdmissionFailsWithLowSemanticScore(t *testing.T) {
	policy := Policy{
		DomainID:             "test_domain",
		RegressionSuiteID:    "test_suite",
		MinActiveGoldenCases: 5,
		MinRunnerEvidence:    "real_runner",
		MinSemanticScore:     0.8,
	}
	metrics := QualityMetrics{
		ActiveGoldenCases:   10,
		LatestEvidenceLevel: "real_runner",
		SemanticScore:       0.6,
	}
	err := CheckDomainAdmission(policy, metrics)
	if err == nil {
		t.Fatal("CheckDomainAdmission should fail with low semantic score")
	}
	if err.Error() != "domain test_domain: semantic score 0.60 < threshold 0.80" {
		t.Fatalf("CheckDomainAdmission error = %v, want semantic score error", err)
	}
}

func TestDomainAdmissionFailsWithLowJudgeCoverage(t *testing.T) {
	policy := Policy{
		DomainID:             "test_domain",
		RegressionSuiteID:    "test_suite",
		MinActiveGoldenCases: 5,
		MinRunnerEvidence:    "real_runner",
		MinJudgeCoverage:     0.8,
	}
	metrics := QualityMetrics{
		ActiveGoldenCases:   10,
		LatestEvidenceLevel: "real_runner",
		SemanticScore:       0.9,
		JudgeCoverage:       0.6,
	}
	err := CheckDomainAdmission(policy, metrics)
	if err == nil {
		t.Fatal("CheckDomainAdmission should fail with low judge coverage")
	}
	if err.Error() != "domain test_domain: judge coverage 0.60 < required 0.80" {
		t.Fatalf("CheckDomainAdmission error = %v, want judge coverage error", err)
	}
}

type recordingQualityProvider struct {
	metrics QualityMetrics
	err     error
	calls   int
	domain  DomainID
	policy  Policy
}

func (p *recordingQualityProvider) QualityMetricsForDomain(_ context.Context, domain DomainID, policy Policy) (QualityMetrics, error) {
	p.calls++
	p.domain = domain
	p.policy = policy
	if p.err != nil {
		return QualityMetrics{}, p.err
	}
	return p.metrics, nil
}
