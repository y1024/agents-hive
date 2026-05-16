package agentquality

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"strings"
	"sync"
	"time"

	"github.com/chef-guo/agents-hive/internal/security"
)

// ShadowEvalConfig 配置生产环境影子评测的采样和并发策略。
type ShadowEvalConfig struct {
	SamplingRate       float64       `json:"sampling_rate"`                  // 采样率 (0.0-1.0)
	DomainFilters      []string      `json:"domain_filters,omitempty"`       // 只采样这些 domain
	FailureTypeFilters []FailureType `json:"failure_type_filters,omitempty"` // 只采样这些失败类型
	MaxConcurrent      int           `json:"max_concurrent"`                 // 最大并发影子评测数
	Enabled            bool          `json:"enabled"`                        // 是否启用影子评测
}

// ShadowEvalSampler 决定是否对某个质量事件进行影子评测。
type ShadowEvalSampler interface {
	ShouldSample(event Event) bool
}

// ConfigurableSampler 实现基于配置的采样策略。
type ConfigurableSampler struct {
	Config ShadowEvalConfig
	rng    *rand.Rand
	mu     sync.Mutex
}

// NewConfigurableSampler 创建一个可配置的采样器。
func NewConfigurableSampler(config ShadowEvalConfig) *ConfigurableSampler {
	return &ConfigurableSampler{
		Config: config,
		rng:    rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

// ShouldSample 判断是否应该对事件进行影子评测。
func (s *ConfigurableSampler) ShouldSample(event Event) bool {
	if !s.Config.Enabled {
		return false
	}

	// 检查 domain 过滤
	if len(s.Config.DomainFilters) > 0 {
		matched := false
		for _, domain := range s.Config.DomainFilters {
			if event.DomainID == domain {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}

	// 检查 failure type 过滤
	if len(s.Config.FailureTypeFilters) > 0 {
		matched := false
		for _, ft := range s.Config.FailureTypeFilters {
			if event.FailureType == ft {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}

	// 采样率检查
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.rng.Float64() < s.Config.SamplingRate
}

// ShadowEvalResult 是单次影子评测的结果。
type ShadowEvalResult struct {
	CaseID         string            `json:"case_id"`
	DomainID       string            `json:"domain_id"`
	SourceKind     string            `json:"source_kind"`
	Passed         bool              `json:"passed"`
	JudgeVerdict   EvaluationVerdict `json:"judge_verdict"`
	RunnerInfo     RunnerInfo        `json:"runner_info"`
	TraceRef       string            `json:"trace_ref,omitempty"`
	ReplayRef      string            `json:"replay_ref,omitempty"`
	Timestamp      time.Time         `json:"timestamp"`
	EvalDurationMS int64             `json:"eval_duration_ms"`
}

// ShadowEvalRunner 执行异步、只读的影子评测。
type ShadowEvalRunner struct {
	Sampler       ShadowEvalSampler
	Runner        DescribedEvalRunner
	Evaluator     ShadowEvaluator
	ResultStore   ShadowEvalResultStore
	MaxConcurrent int
	semaphore     chan struct{}
	mu            sync.Mutex
}

// ShadowEvaluator 是影子评测的判断器接口。
type ShadowEvaluator interface {
	Evaluate(ctx context.Context, input EvaluationInput) (EvaluationVerdict, error)
}

// ShadowEvalResultStore 存储影子评测结果。
type ShadowEvalResultStore interface {
	Store(ctx context.Context, result ShadowEvalResult) error
	ListRecent(ctx context.Context, domainID string, limit int) ([]ShadowEvalResult, error)
}

// NewShadowEvalRunner 创建影子评测运行器。
func NewShadowEvalRunner(sampler ShadowEvalSampler, runner DescribedEvalRunner, evaluator ShadowEvaluator, store ShadowEvalResultStore, maxConcurrent int) *ShadowEvalRunner {
	if maxConcurrent <= 0 {
		maxConcurrent = 5 // 默认最多 5 个并发影子评测
	}
	return &ShadowEvalRunner{
		Sampler:       sampler,
		Runner:        runner,
		Evaluator:     evaluator,
		ResultStore:   store,
		MaxConcurrent: maxConcurrent,
		semaphore:     make(chan struct{}, maxConcurrent),
	}
}

// RunShadowEval 异步执行影子评测，永不阻塞用户响应。
// 返回 nil 表示评测已启动（或被跳过），不等待完成。
func (r *ShadowEvalRunner) RunShadowEval(ctx context.Context, event Event) error {
	if r.Sampler == nil || !r.Sampler.ShouldSample(event) {
		return nil // 不采样，直接返回
	}

	// 尝试获取信号量，非阻塞
	select {
	case r.semaphore <- struct{}{}:
		// 获取成功，启动异步评测
		go r.runShadowEvalAsync(event)
		return nil
	default:
		// 信号量已满，跳过本次评测
		return nil
	}
}

// runShadowEvalAsync 在后台执行影子评测。
func (r *ShadowEvalRunner) runShadowEvalAsync(event Event) {
	defer func() {
		<-r.semaphore // 释放信号量
		if rec := recover(); rec != nil {
			// 捕获 panic，防止影响主流程
			fmt.Printf("shadow eval panic: %v\n", rec)
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	startTime := time.Now()

	// 从事件构造 case
	testCase := eventToCase(event)
	if testCase.ID == "" {
		return // 无法构造有效 case
	}

	// 执行 runner
	gateInput, err := r.Runner.Run([]LoadedCase{{Case: testCase}})
	if err != nil {
		return // 执行失败，静默跳过
	}

	// 提取结果
	var runResult Result
	if len(gateInput.Results) > 0 {
		runResult = gateInput.Results[0]
	}

	// 构造评测输入
	userInput, assistantOutput := shadowEvalIO(event)
	evalInput := EvaluationInput{
		SessionID:       event.SessionIDHash,
		TraceID:         event.TraceID,
		Trigger:         "shadow_eval",
		UserInput:       userInput,
		AssistantOutput: assistantOutput,
	}

	// 执行判断
	verdict, err := r.Evaluator.Evaluate(ctx, evalInput)
	if err != nil {
		return // 判断失败，静默跳过
	}

	// 构造结果
	result := ShadowEvalResult{
		CaseID:         testCase.ID,
		DomainID:       event.DomainID,
		SourceKind:     event.SourceKind,
		Passed:         runResult.Passed && verdict.Score >= 7,
		JudgeVerdict:   verdict,
		RunnerInfo:     r.Runner.Info(),
		TraceRef:       event.TraceID,
		ReplayRef:      event.ReplayRef,
		Timestamp:      time.Now(),
		EvalDurationMS: time.Since(startTime).Milliseconds(),
	}

	// 存储结果
	if r.ResultStore != nil {
		_ = r.ResultStore.Store(ctx, result)
	}
}

// eventToCase 从质量事件构造测试 case。
func eventToCase(event Event) Case {
	caseID := fmt.Sprintf("shadow_%s_%d", event.TraceID, event.Ts.Unix())
	userInput, _ := shadowEvalIO(event)

	return Case{
		ID:             caseID,
		Name:           fmt.Sprintf("影子评测 %s", event.Name),
		Route:          event.Route,
		Input:          userInput,
		ExpectedStatus: event.FinalStatus,
		FailureType:    event.FailureType,
		DomainID:       event.DomainID,
		SourceKind:     event.SourceKind,
		SourceName:     event.SourceName,
		CreatedFrom:    shadowEvalCreatedFrom(event),
	}
}

func shadowEvalIO(event Event) (string, string) {
	userInput := firstShadowEvalAttribute(event.Attributes, []string{
		"user_input",
		"input",
		"query",
		"message",
		"prompt",
	})
	if userInput == "" {
		userInput = shadowEvalTraceFallback(event)
	}

	assistantOutput := firstShadowEvalAttribute(event.Attributes, []string{
		"assistant_output",
		"final_output",
		"response",
		"output",
	})

	return userInput, assistantOutput
}

func shadowEvalCreatedFrom(event Event) string {
	if firstShadowEvalAttribute(event.Attributes, []string{"user_input", "input", "query", "message", "prompt"}) != "" {
		return "shadow_eval_real_io"
	}
	return "shadow_eval_trace_fallback"
}

func shadowEvalTraceFallback(event Event) string {
	traceRef := strings.TrimSpace(event.TraceID)
	if traceRef == "" {
		traceRef = strings.TrimSpace(event.CaseID)
	}
	if traceRef == "" {
		traceRef = strings.TrimSpace(string(event.Name))
	}
	if traceRef == "" {
		traceRef = "unknown"
	}
	return fmt.Sprintf("Shadow eval trace fallback: %s", traceRef)
}

func firstShadowEvalAttribute(attributes map[string]any, keys []string) string {
	if len(attributes) == 0 {
		return ""
	}
	for _, key := range keys {
		if value, ok := attributes[key]; ok {
			if text := shadowEvalAttributeText(value); text != "" {
				return text
			}
		}
	}

	for _, key := range keys {
		normalizedWant := normalizeShadowEvalAttributeKey(key)
		for gotKey, value := range attributes {
			if normalizeShadowEvalAttributeKey(gotKey) != normalizedWant {
				continue
			}
			if text := shadowEvalAttributeText(value); text != "" {
				return text
			}
		}
	}
	return ""
}

func shadowEvalAttributeText(value any) string {
	redacted, err := security.RedactSecrets(value)
	if err != nil {
		return ""
	}

	var text string
	switch v := redacted.(type) {
	case nil:
		return ""
	case string:
		text = v
	case []byte:
		text = string(v)
	case json.RawMessage:
		text = string(v)
	default:
		raw, err := json.Marshal(v)
		if err != nil {
			return ""
		}
		text = string(raw)
	}

	text = strings.TrimSpace(text)
	if text == "" || text == security.RedactedValue || text == "****" {
		return ""
	}
	return text
}

func normalizeShadowEvalAttributeKey(key string) string {
	key = strings.ToLower(strings.TrimSpace(key))
	key = strings.ReplaceAll(key, "-", "_")
	key = strings.ReplaceAll(key, " ", "_")
	return key
}

// InMemoryShadowEvalResultStore 内存存储实现。
type InMemoryShadowEvalResultStore struct {
	mu      sync.RWMutex
	results []ShadowEvalResult
}

// NewInMemoryShadowEvalResultStore 创建内存存储。
func NewInMemoryShadowEvalResultStore() *InMemoryShadowEvalResultStore {
	return &InMemoryShadowEvalResultStore{
		results: []ShadowEvalResult{},
	}
}

// Store 存储影子评测结果。
func (s *InMemoryShadowEvalResultStore) Store(ctx context.Context, result ShadowEvalResult) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.results = append(s.results, result)
	return nil
}

// ListRecent 列出最近的影子评测结果。
func (s *InMemoryShadowEvalResultStore) ListRecent(ctx context.Context, domainID string, limit int) ([]ShadowEvalResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	// 过滤并排序
	filtered := []ShadowEvalResult{}
	for i := len(s.results) - 1; i >= 0 && len(filtered) < limit; i-- {
		if domainID == "" || s.results[i].DomainID == domainID {
			filtered = append(filtered, s.results[i])
		}
	}

	return filtered, nil
}

// ShadowEvalMetricsData 表示单个业务域的影子评测指标。
type ShadowEvalMetricsData struct {
	DomainID         string          `json:"domain_id"`
	SampleCount      int             `json:"sample_count"`
	PassRate         float64         `json:"pass_rate"`
	AvgSemanticScore float64         `json:"avg_semantic_score"`
	SafetyFailures   int             `json:"safety_failures"`
	ToolMisuses      int             `json:"tool_misuses"`
	RecentAlerts     []RollbackAlert `json:"recent_alerts"`
}

// BuildShadowEvalMetrics 从影子评测结果构建指标。
func BuildShadowEvalMetrics(results []ShadowEvalResult) ShadowEvalMetricsData {
	if len(results) == 0 {
		return ShadowEvalMetricsData{}
	}

	var domainID string
	passCount := 0
	totalScore := 0
	safetyFailures := 0
	toolMisuses := 0

	for _, result := range results {
		if domainID == "" {
			domainID = result.DomainID
		}

		if result.Passed {
			passCount++
		}

		totalScore += result.JudgeVerdict.Score

		if result.JudgeVerdict.FailureType == FailurePermission ||
			result.JudgeVerdict.FailureType == FailureRuntime {
			safetyFailures++
		}

		if result.JudgeVerdict.FailureType == FailureTool {
			toolMisuses++
		}
	}

	passRate := float64(passCount) / float64(len(results))
	avgScore := float64(totalScore) / float64(len(results))

	return ShadowEvalMetricsData{
		DomainID:         domainID,
		SampleCount:      len(results),
		PassRate:         passRate,
		AvgSemanticScore: avgScore,
		SafetyFailures:   safetyFailures,
		ToolMisuses:      toolMisuses,
		RecentAlerts:     []RollbackAlert{},
	}
}
