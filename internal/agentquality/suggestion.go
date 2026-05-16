package agentquality

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

type SuggestionStatus string

const (
	SuggestionPending  SuggestionStatus = "pending"
	SuggestionApproved SuggestionStatus = "approved"
	SuggestionRejected SuggestionStatus = "rejected"
	SuggestionExpired  SuggestionStatus = "expired"
)

type SuggestionApplyStatus string

const (
	SuggestionApplyUnapplied     SuggestionApplyStatus = "unapplied"
	SuggestionApplyApplied       SuggestionApplyStatus = "applied"
	SuggestionApplyError         SuggestionApplyStatus = "apply_error"
	SuggestionApplyNotApplicable SuggestionApplyStatus = "not_applicable"
)

type SuggestionTarget string

const (
	TargetPrompt           SuggestionTarget = "prompt"
	TargetToolDescription  SuggestionTarget = "tool_description"
	TargetSkillContent     SuggestionTarget = "skill_content"
	TargetMemoryGovernance SuggestionTarget = "memory_governance"
)

type OptimizationReviewSuggestion struct {
	ID                string                `json:"id"`
	Status            SuggestionStatus      `json:"status"`
	Target            SuggestionTarget      `json:"target"`
	Kind              SuggestionKind        `json:"kind"`
	Title             string                `json:"title"`
	Rationale         string                `json:"rationale"`
	CurrentValue      string                `json:"current_value,omitempty"`
	ProposedValue     string                `json:"proposed_value"`
	DiffFormat        string                `json:"diff_format"`
	SourceCandidateID string                `json:"source_candidate_id"`
	SourceEvalDiffID  string                `json:"source_eval_diff_id,omitempty"`
	SourceEvent       Event                 `json:"source_event"`
	RunnerInfo        RunnerInfo            `json:"runner_info,omitempty"`
	ReviewRequired    bool                  `json:"review_required"`
	CreatedBy         string                `json:"created_by"`
	ApprovedBy        string                `json:"approved_by,omitempty"`
	ApprovalNote      string                `json:"approval_note,omitempty"`
	ApplyStatus       SuggestionApplyStatus `json:"apply_status"`
	AppliedBy         string                `json:"applied_by,omitempty"`
	ApplyError        string                `json:"apply_error,omitempty"`
	CreatedAt         time.Time             `json:"created_at"`
	UpdatedAt         time.Time             `json:"updated_at"`
	ApprovedAt        *time.Time            `json:"approved_at,omitempty"`
	AppliedAt         *time.Time            `json:"applied_at,omitempty"`
	ExpiresAt         time.Time             `json:"expires_at"`
}

type SuggestionGenerator struct {
	now           func() time.Time
	defaultExpiry time.Duration
}

type SuggestionGeneratorOption func(*SuggestionGenerator)

func NewSuggestionGenerator(opts ...SuggestionGeneratorOption) *SuggestionGenerator {
	g := &SuggestionGenerator{
		now:           time.Now,
		defaultExpiry: 30 * 24 * time.Hour,
	}
	for _, opt := range opts {
		opt(g)
	}
	return g
}

func WithSuggestionNow(now func() time.Time) SuggestionGeneratorOption {
	return func(g *SuggestionGenerator) {
		if now != nil {
			g.now = now
		}
	}
}

func (g *SuggestionGenerator) GenerateFromCandidate(rec CandidateRecord, createdBy string) []OptimizationReviewSuggestion {
	drafts := BuildOptimizationSuggestions(rec)
	out := make([]OptimizationReviewSuggestion, 0, len(drafts))
	now := g.now()
	for idx, draft := range drafts {
		target := suggestionTargetForKind(draft.Kind)
		out = append(out, OptimizationReviewSuggestion{
			ID:                suggestionID(rec.ID, draft.Kind, draft.Target, idx),
			Status:            SuggestionPending,
			Target:            target,
			Kind:              draft.Kind,
			Title:             draft.Title,
			Rationale:         draft.Rationale,
			CurrentValue:      draft.Target,
			ProposedValue:     draft.Proposed,
			DiffFormat:        "text",
			SourceCandidateID: rec.ID,
			SourceEvent:       rec.SourceEvent,
			ReviewRequired:    true,
			CreatedBy:         createdBy,
			CreatedAt:         now,
			UpdatedAt:         now,
			ExpiresAt:         now.Add(g.defaultExpiry),
		})
	}
	return out
}

func (g *SuggestionGenerator) GenerateFromEvalDiff(diff EvalDiff, createdBy string) []OptimizationReviewSuggestion {
	now := g.now()
	out := make([]OptimizationReviewSuggestion, 0, len(diff.CaseDiffs))
	for idx, caseDiff := range diff.CaseDiffs {
		if !caseDiff.BaselinePassed || caseDiff.TreatmentPassed {
			continue
		}
		draft, ok := buildSuggestionFromEvalCaseDiff(caseDiff)
		if !ok {
			continue
		}
		out = append(out, OptimizationReviewSuggestion{
			ID:               evalDiffSuggestionID(diff.ID, caseDiff.CaseID, draft.Kind, idx),
			Status:           SuggestionPending,
			Target:           suggestionTargetForKind(draft.Kind),
			Kind:             draft.Kind,
			Title:            draft.Title,
			Rationale:        draft.Rationale,
			CurrentValue:     draft.Target,
			ProposedValue:    draft.Proposed,
			DiffFormat:       "text",
			SourceEvalDiffID: diff.ID,
			RunnerInfo:       diff.TreatmentRunnerInfo,
			ReviewRequired:   true,
			CreatedBy:        createdBy,
			CreatedAt:        now,
			UpdatedAt:        now,
			ExpiresAt:        now.Add(g.defaultExpiry),
		})
	}
	return out
}

func (s OptimizationReviewSuggestion) IsExpired(now time.Time) bool {
	return !s.ExpiresAt.IsZero() && now.After(s.ExpiresAt)
}

func (s OptimizationReviewSuggestion) CanApprove() bool {
	return CanApproveOptimization(s.RunnerInfo.EvidenceLevel)
}

func (s OptimizationReviewSuggestion) ValidateApprovalEvidence() error {
	if s.CanApprove() {
		return nil
	}
	return fmt.Errorf("suggestion %s requires real_runner, production_shadow, or human_verified evidence before approval/apply, got %q", s.ID, s.RunnerInfo.EvidenceLevel)
}

func (s OptimizationReviewSuggestion) Approve(reviewer, note string, now time.Time) (OptimizationReviewSuggestion, error) {
	if s.Status != SuggestionPending {
		return s, fmt.Errorf("suggestion %s is not pending", s.ID)
	}
	if s.IsExpired(now) {
		return s, fmt.Errorf("suggestion %s expired", s.ID)
	}
	if err := s.ValidateApprovalEvidence(); err != nil {
		return s, err
	}
	s.Status = SuggestionApproved
	s.ApprovedBy = strings.TrimSpace(reviewer)
	s.ApprovalNote = strings.TrimSpace(note)
	s.ApprovedAt = &now
	s.UpdatedAt = now
	return s, nil
}

func (s OptimizationReviewSuggestion) Reject(reviewer, note string, now time.Time) (OptimizationReviewSuggestion, error) {
	if s.Status != SuggestionPending {
		return s, fmt.Errorf("suggestion %s is not pending", s.ID)
	}
	s.Status = SuggestionRejected
	s.ApprovedBy = strings.TrimSpace(reviewer)
	s.ApprovalNote = strings.TrimSpace(note)
	s.UpdatedAt = now
	return s, nil
}

func (s OptimizationReviewSuggestion) MarkApplied(appliedBy string, now time.Time) OptimizationReviewSuggestion {
	s.ApplyStatus = SuggestionApplyApplied
	s.AppliedBy = strings.TrimSpace(appliedBy)
	s.ApplyError = ""
	s.AppliedAt = &now
	s.UpdatedAt = now
	return s
}

func (s OptimizationReviewSuggestion) MarkApplyError(appliedBy, message string, now time.Time) OptimizationReviewSuggestion {
	s.ApplyStatus = SuggestionApplyError
	s.AppliedBy = strings.TrimSpace(appliedBy)
	s.ApplyError = strings.TrimSpace(message)
	s.AppliedAt = &now
	s.UpdatedAt = now
	return s
}

func (s OptimizationReviewSuggestion) MarkNotApplicable(appliedBy, message string, now time.Time) OptimizationReviewSuggestion {
	s.ApplyStatus = SuggestionApplyNotApplicable
	s.AppliedBy = strings.TrimSpace(appliedBy)
	s.ApplyError = strings.TrimSpace(message)
	s.AppliedAt = &now
	s.UpdatedAt = now
	return s
}

func suggestionTargetForKind(kind SuggestionKind) SuggestionTarget {
	switch kind {
	case SuggestionToolDescription:
		return TargetToolDescription
	case SuggestionSkillDraft:
		return TargetSkillContent
	default:
		return TargetPrompt
	}
}

func suggestionID(candidateID string, kind SuggestionKind, target string, idx int) string {
	payload := fmt.Sprintf("%s|%s|%s|%d", candidateID, kind, target, idx)
	sum := sha256.Sum256([]byte(payload))
	return "sug_" + hex.EncodeToString(sum[:8])
}

func evalDiffSuggestionID(evalDiffID, caseID string, kind SuggestionKind, idx int) string {
	payload := fmt.Sprintf("evaldiff|%s|%s|%s|%d", evalDiffID, caseID, kind, idx)
	sum := sha256.Sum256([]byte(payload))
	return "sug_" + hex.EncodeToString(sum[:8])
}

func buildSuggestionFromEvalCaseDiff(caseDiff EvalCaseDiff) (OptimizationSuggestion, bool) {
	switch caseDiff.FailureType {
	case FailurePrompt:
		return OptimizationSuggestion{
			Kind:           SuggestionPromptDiff,
			Title:          "Eval diff prompt 回归建议",
			Target:         promptRefTarget(caseDiff.Prompt),
			Rationale:      "offline eval diff 显示 treatment 在 golden case 上出现 prompt 归因回归，应以人工 review 的 prompt diff 修复。",
			Proposed:       fmt.Sprintf("review 草稿：针对 golden case %s 补充约束，要求回答前先确认当前证据、工具结果和任务边界，不得凭记忆跳过验证。", caseDiff.CaseID),
			ReviewRequired: true,
		}, true
	case FailureTool:
		tool := ""
		if len(caseDiff.ExpectedTools) > 0 {
			tool = caseDiff.ExpectedTools[0]
		}
		if tool == "" {
			tool = caseDiff.ActualTool
		}
		return OptimizationSuggestion{
			Kind:           SuggestionToolDescription,
			Title:          "Eval diff 工具选择回归建议",
			Target:         tool,
			Rationale:      "offline eval diff 显示 treatment 在工具选择 golden case 上回归，应强化工具描述触发条件。",
			Proposed:       fmt.Sprintf("将 %s 的描述补强为：当任务与 golden case %s 的检索、定位或验证模式相似时优先调用该工具，并基于工具证据回答。", firstNonEmpty(tool, "目标工具"), caseDiff.CaseID),
			ReviewRequired: true,
		}, true
	case FailureSkill:
		skill := ""
		if len(caseDiff.ExpectedSkills) > 0 {
			skill = caseDiff.ExpectedSkills[0]
		}
		if skill == "" {
			skill = "candidate-skill"
		}
		return OptimizationSuggestion{
			Kind:           SuggestionSkillDraft,
			Title:          "Eval diff skill 路由回归建议",
			Target:         skill,
			Rationale:      "offline eval diff 显示 treatment 在 skill 路由 golden case 上回归，应生成可评审 skill 草稿或路由修正。",
			Proposed:       fmt.Sprintf("---\nname: %s\ndescription: 修复 eval diff golden case %s 暴露的 skill 路由回归，必须人工 review 后使用。\n---\n\n# 执行要求\n1. 先匹配当前任务是否符合该 golden case。\n2. 匹配时优先加载并执行该 skill。\n3. 输出前说明使用的证据来源。\n", skill, caseDiff.CaseID),
			ReviewRequired: true,
		}, true
	default:
		return OptimizationSuggestion{}, false
	}
}

func promptRefTarget(ref PromptRef) string {
	if ref.Key == "" {
		return "system/base"
	}
	if ref.Version != "" {
		return ref.Key + "@" + ref.Version
	}
	return ref.Key
}
