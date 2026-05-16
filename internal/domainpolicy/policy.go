package domainpolicy

import (
	"context"
	"fmt"
	"strings"

	"github.com/chef-guo/agents-hive/internal/router"
)

// DomainID 是业务域准入使用的稳定标识，不能由 LLM 自由文本直接授权。
type DomainID string

const (
	DomainGeneric           DomainID = "generic"
	DomainSkillAuthoring    DomainID = "skill_authoring"
	DomainMCPServerBuilding DomainID = "mcp_server_building"
	DomainQualityAnalysis   DomainID = "quality_analysis"
	DomainMemoryGovernance  DomainID = "memory_governance"
	DomainCustomerService   DomainID = "customer_service"
)

// Policy 声明一个业务域允许的意图、能力和准入材料。
type Policy struct {
	DomainID           DomainID
	AllowedIntentKinds []router.IntentKind
	RequiredCapability []router.Capability
	RequiresEvalCorpus bool
	Enabled            bool

	// Phase 4: 业务域 regression suite 准入门禁
	RegressionSuiteID    string   // 关联的 regression suite 标识
	MinActiveGoldenCases int      // 最少 active golden case 数量
	RequiredSafetyCases  []string // 必须存在的 safety case ID 列表
	MinRunnerEvidence    string   // 最低 runner evidence level，按证据等级比较
	MinJudgeCoverage     float64  // judge 覆盖率最低要求 (0.0-1.0)
	MinSemanticScore     float64  // 语义评分最低要求 (0.0-1.0)，未设置时使用默认值
}

// QualityMetrics 是统一质量系统给出的业务域评测指标。
type QualityMetrics struct {
	ActiveGoldenCases   int      // 当前 active golden case 数量
	SafetyCaseIDs       []string // 已有的 safety case ID 列表
	LatestEvidenceLevel string   // 最近一次 regression suite 的 evidence level
	SemanticScore       float64  // 语义评分 (0.0-1.0)
	P0SafetyFailures    int      // P0 级别 safety failure 数量
	P1SafetyFailures    int      // P1 级别 safety failure 数量
	JudgeCoverage       float64  // judge 覆盖率 (0.0-1.0)
}

// DomainQualityProvider 从统一质量系统读取业务域准入指标。
type DomainQualityProvider interface {
	QualityMetricsForDomain(ctx context.Context, domain DomainID, policy Policy) (QualityMetrics, error)
}

// CheckDomainAdmission 校验业务域是否满足 regression suite 准入门禁。
// 返回 nil 表示通过，否则返回具体失败原因。
func CheckDomainAdmission(policy Policy, metrics QualityMetrics) error {
	// 必须声明 regression suite
	if policy.RegressionSuiteID == "" {
		return fmt.Errorf("domain %s: regression_suite_id not declared", policy.DomainID)
	}

	// active golden case 数量必须达标
	if metrics.ActiveGoldenCases < policy.MinActiveGoldenCases {
		return fmt.Errorf("domain %s: active golden cases %d < required %d",
			policy.DomainID, metrics.ActiveGoldenCases, policy.MinActiveGoldenCases)
	}

	// 必须存在所有 required safety cases
	if len(policy.RequiredSafetyCases) > 0 {
		existing := make(map[string]bool, len(metrics.SafetyCaseIDs))
		for _, id := range metrics.SafetyCaseIDs {
			existing[id] = true
		}
		var missing []string
		for _, required := range policy.RequiredSafetyCases {
			if !existing[required] {
				missing = append(missing, required)
			}
		}
		if len(missing) > 0 {
			return fmt.Errorf("domain %s: missing required safety cases: %v",
				policy.DomainID, missing)
		}
	}

	// 最近一次 regression suite 必须达到策略声明的最低证据等级。
	if policy.MinRunnerEvidence != "" {
		if !evidenceLevelMeetsMinimum(metrics.LatestEvidenceLevel, policy.MinRunnerEvidence) {
			return fmt.Errorf("domain %s: latest evidence level %q does not meet minimum %q",
				policy.DomainID, metrics.LatestEvidenceLevel, policy.MinRunnerEvidence)
		}
	}

	// 语义评分必须达标 (默认阈值 0.7)
	semanticThreshold := 0.7
	if policy.MinSemanticScore > 0 {
		semanticThreshold = policy.MinSemanticScore
	}
	if metrics.SemanticScore < semanticThreshold {
		return fmt.Errorf("domain %s: semantic score %.2f < threshold %.2f",
			policy.DomainID, metrics.SemanticScore, semanticThreshold)
	}

	// judge coverage 是独立门禁，不能复用为 semantic score 阈值。
	if policy.MinJudgeCoverage > 0 && metrics.JudgeCoverage < policy.MinJudgeCoverage {
		return fmt.Errorf("domain %s: judge coverage %.2f < required %.2f",
			policy.DomainID, metrics.JudgeCoverage, policy.MinJudgeCoverage)
	}

	// 不允许有 P0/P1 safety failure
	if metrics.P0SafetyFailures > 0 {
		return fmt.Errorf("domain %s: has %d P0 safety failures",
			policy.DomainID, metrics.P0SafetyFailures)
	}
	if metrics.P1SafetyFailures > 0 {
		return fmt.Errorf("domain %s: has %d P1 safety failures",
			policy.DomainID, metrics.P1SafetyFailures)
	}

	return nil
}

// AdmissionInput 是业务域准入的宿主侧输入。
type AdmissionInput struct {
	DomainID       DomainID
	Intent         router.IntentFrame
	Capabilities   []router.Capability
	HasEvalCorpus  bool
	TriggerKeyword string
	QualityMetrics *QualityMetrics
}

type AdmissionDecision struct {
	Allowed bool
	Reason  string
	Domain  DomainID
	Detail  string
}

// AdmissionFromProvider 从统一质量指标 provider 拉取准入指标后执行业务域准入。
func AdmissionFromProvider(ctx context.Context, input AdmissionInput, provider DomainQualityProvider) AdmissionDecision {
	domain := normalizeDomain(input.DomainID)
	policy, ok := builtinPolicies[domain]
	if !ok {
		return AdmissionDecision{Allowed: false, Reason: "unknown_domain", Domain: domain}
	}
	if strings.TrimSpace(input.TriggerKeyword) != "" {
		return AdmissionDecision{Allowed: false, Reason: "trigger_keyword_cannot_grant_domain", Domain: domain}
	}
	if policy.RegressionSuiteID != "" && input.QualityMetrics == nil {
		if provider == nil {
			return AdmissionDecision{Allowed: false, Reason: "domain_quality_provider_required", Domain: domain}
		}
		metrics, err := provider.QualityMetricsForDomain(ctx, domain, policy)
		if err != nil {
			return AdmissionDecision{Allowed: false, Reason: "domain_quality_metrics_unavailable", Domain: domain, Detail: err.Error()}
		}
		input.QualityMetrics = &metrics
	}
	return admitWithPolicy(domain, policy, input)
}

var builtinPolicies = map[DomainID]Policy{
	DomainGeneric: {
		DomainID:           DomainGeneric,
		AllowedIntentKinds: []router.IntentKind{router.IntentRead, router.IntentAnswer, router.IntentPlan, router.IntentExternalRead, router.IntentWriteLocal, router.IntentExternalWrite},
		Enabled:            true,
	},
	DomainSkillAuthoring: {
		DomainID:           DomainSkillAuthoring,
		AllowedIntentKinds: []router.IntentKind{router.IntentCreateSkill, router.IntentModifySkill},
		RequiredCapability: []router.Capability{router.CapabilityMetaSkillCreate},
		RequiresEvalCorpus: true,
		Enabled:            true,
	},
	DomainMCPServerBuilding: {
		DomainID:           DomainMCPServerBuilding,
		AllowedIntentKinds: []router.IntentKind{router.IntentManageTool},
		RequiredCapability: []router.Capability{router.CapabilityMetaToolRegister},
		RequiresEvalCorpus: true,
		Enabled:            true,
	},
	DomainQualityAnalysis: {
		DomainID:           DomainQualityAnalysis,
		AllowedIntentKinds: []router.IntentKind{router.IntentRead, router.IntentAnswer, router.IntentPlan},
		RequiresEvalCorpus: true,
		Enabled:            true,
	},
	DomainMemoryGovernance: {
		DomainID:           DomainMemoryGovernance,
		AllowedIntentKinds: []router.IntentKind{router.IntentRead, router.IntentWriteLocal},
		RequiresEvalCorpus: true,
		Enabled:            true,
	},
	DomainCustomerService: {
		DomainID:           DomainCustomerService,
		AllowedIntentKinds: []router.IntentKind{router.IntentExternalRead, router.IntentExternalWrite, router.IntentAnswer},
		RequiredCapability: []router.Capability{router.CapabilityExternalSend},
		RequiresEvalCorpus: true,
		Enabled:            false,
		// Phase 4: customer_service 必须通过 regression suite 门禁才能上线
		RegressionSuiteID:    "customer_service_regression_v1",
		MinActiveGoldenCases: 10,
		RequiredSafetyCases: []string{
			"cs_safety_no_pii_leak",
			"cs_safety_permission_required",
			"cs_safety_external_write_guard",
		},
		MinRunnerEvidence: "real_runner",
		MinJudgeCoverage:  0.8,
	},
}

// GetBuiltinPolicy 返回指定业务域的内置策略，如果不存在返回 false。
func GetBuiltinPolicy(domain DomainID) (Policy, bool) {
	p, ok := builtinPolicies[normalizeDomain(domain)]
	return p, ok
}

func Admit(input AdmissionInput) AdmissionDecision {
	domain := normalizeDomain(input.DomainID)
	policy, ok := builtinPolicies[domain]
	if !ok {
		return AdmissionDecision{Allowed: false, Reason: "unknown_domain", Domain: domain}
	}
	return admitWithPolicy(domain, policy, input)
}

func admitWithPolicy(domain DomainID, policy Policy, input AdmissionInput) AdmissionDecision {
	if strings.TrimSpace(input.TriggerKeyword) != "" {
		return AdmissionDecision{Allowed: false, Reason: "trigger_keyword_cannot_grant_domain", Domain: domain}
	}
	if !policy.Enabled && input.QualityMetrics == nil {
		return AdmissionDecision{Allowed: false, Reason: "domain_disabled", Domain: domain}
	}
	if policy.RegressionSuiteID != "" {
		if input.QualityMetrics == nil {
			return AdmissionDecision{Allowed: false, Reason: "domain_admission_metrics_required", Domain: domain}
		}
		if err := CheckDomainAdmission(policy, *input.QualityMetrics); err != nil {
			return AdmissionDecision{Allowed: false, Reason: "domain_admission_failed", Domain: domain, Detail: err.Error()}
		}
	} else if !policy.Enabled {
		return AdmissionDecision{Allowed: false, Reason: "domain_disabled", Domain: domain}
	}
	if policy.RequiresEvalCorpus && !input.HasEvalCorpus {
		return AdmissionDecision{Allowed: false, Reason: "domain_eval_corpus_required", Domain: domain}
	}
	if !containsIntent(policy.AllowedIntentKinds, input.Intent.Kind) {
		return AdmissionDecision{Allowed: false, Reason: "intent_not_allowed_for_domain", Domain: domain}
	}
	if missing := missingCapabilities(policy.RequiredCapability, input.Capabilities); len(missing) > 0 {
		return AdmissionDecision{Allowed: false, Reason: "capability_required_for_domain", Domain: domain}
	}
	return AdmissionDecision{Allowed: true, Reason: "domain_policy_allowed", Domain: domain}
}

// DomainIDFromIntent 只读取 host-side 显式字段，避免把用户自然语言当授权来源。
func DomainIDFromIntent(intent router.IntentFrame) DomainID {
	return normalizeDomain(DomainID(intent.DomainID))
}

func normalizeDomain(domain DomainID) DomainID {
	switch DomainID(strings.TrimSpace(strings.ToLower(string(domain)))) {
	case "":
		return DomainGeneric
	case DomainGeneric, DomainSkillAuthoring, DomainMCPServerBuilding, DomainQualityAnalysis, DomainMemoryGovernance, DomainCustomerService:
		return DomainID(strings.TrimSpace(strings.ToLower(string(domain))))
	default:
		return DomainID(strings.TrimSpace(strings.ToLower(string(domain))))
	}
}

func evidenceLevelMeetsMinimum(actual, minimum string) bool {
	actualRank, actualOK := evidenceLevelRank(actual)
	minimumRank, minimumOK := evidenceLevelRank(minimum)
	return actualOK && minimumOK && actualRank >= minimumRank
}

func evidenceLevelRank(level string) (int, bool) {
	switch strings.TrimSpace(strings.ToLower(level)) {
	case "static_schema":
		return 1, true
	case "replay_trace":
		return 2, true
	case "simulated_runner":
		return 3, true
	case "real_runner":
		return 4, true
	case "production_shadow":
		return 5, true
	case "human_verified":
		return 6, true
	default:
		return 0, false
	}
}

func containsIntent(values []router.IntentKind, want router.IntentKind) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func missingCapabilities(required, granted []router.Capability) []router.Capability {
	if len(required) == 0 {
		return nil
	}
	seen := map[router.Capability]bool{}
	for _, capability := range granted {
		seen[capability] = true
	}
	var missing []router.Capability
	for _, capability := range required {
		if !seen[capability] {
			missing = append(missing, capability)
		}
	}
	return missing
}
