package skills

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	"github.com/chef-guo/agents-hive/internal/errs"
	"github.com/chef-guo/agents-hive/internal/plugin"
	"github.com/chef-guo/agents-hive/internal/router"
	"github.com/chef-guo/agents-hive/internal/security"
	"github.com/chef-guo/agents-hive/internal/store"
	"github.com/chef-guo/agents-hive/internal/toolctx"
	"github.com/chef-guo/agents-hive/internal/toolruntime"
	"go.uber.org/zap"
)

// PermissionStore 权限持久化存储接口
type PermissionStore interface {
	SaveGrant(ctx context.Context, rec *store.PermissionGrantRecord) error
	LoadGrants(ctx context.Context) ([]store.PermissionGrantRecord, error)
	DeleteGrant(ctx context.Context, id int64) error
	DeleteAllGrants(ctx context.Context) error
}

// PermissionAction 定义权限操作类型
type PermissionAction string

const (
	PermissionAsk   PermissionAction = "ask"   // 每次提示
	PermissionAllow PermissionAction = "allow" // 自动批准
	PermissionDeny  PermissionAction = "deny"  // 自动拒绝
)

// PermissionRule 定义单个权限规则
type PermissionRule struct {
	ToolName string           `json:"tool_name" yaml:"tool_name"`                 // 工具名，支持通配符 "*"
	Action   PermissionAction `json:"action" yaml:"action"`                       // allow, deny, ask
	Pattern  string           `json:"pattern,omitempty" yaml:"pattern,omitempty"` // 参数模式匹配（如文件路径 "src/**/*.go"、命令 "rm *"）
}

// PermissionRequest 表示权限请求
type PermissionRequest struct {
	ToolName    string          `json:"tool_name"`
	Description string          `json:"description"`
	Input       json.RawMessage `json:"input"`
}

// PermissionResponse 表示权限响应
type PermissionResponse struct {
	Granted  bool `json:"granted"`
	Remember bool `json:"remember"` // 为 session 持久化决策
}

// grantKey 用于 session grants 的复合键（工具名 + 模式）
type grantKey struct {
	tool    string
	pattern string
}

// grantEntry 有序 grant 条目，保证遍历顺序确定
type grantEntry struct {
	key    grantKey
	action PermissionAction
}

const permissionInputGrantPrefix = "__input_sha256__:"

// llmClassifier LLM 分类器接口（在 skills 包中以接口引用，避免 import cycle）
type llmClassifier interface {
	Classify(ctx context.Context, toolName string, input json.RawMessage) security.ClassifyResult
}

// PermissionPolicyEvaluator 把 PermissionManager 接到统一工具策略层。
// Legacy rules 只能收紧该裁决，不能放宽 unified deny/ask。
type PermissionPolicyEvaluator interface {
	EvaluatePermission(ctx context.Context, toolName string, input json.RawMessage) router.ToolPolicyDecision
}

type permissionPolicyEvaluatorFunc func(context.Context, string, json.RawMessage) router.ToolPolicyDecision

func (f permissionPolicyEvaluatorFunc) EvaluatePermission(ctx context.Context, toolName string, input json.RawMessage) router.ToolPolicyDecision {
	return f(ctx, toolName, input)
}

// PermissionManager 管理工具执行权限
type PermissionManager struct {
	rules      []PermissionRule
	grants     []grantEntry // 有序 slice，保证遍历顺序确定
	mu         sync.RWMutex
	promptFn   func(ctx context.Context, req PermissionRequest) (PermissionResponse, error)
	pluginMgr  *plugin.Manager // 插件管理器（可选，用于 PermissionAsk hook）
	store      PermissionStore // 权限持久化存储（可选，用于跨会话持久化）
	classifier llmClassifier   // LLM 分类器（可选，域E Phase 2）
	evaluator  PermissionPolicyEvaluator
	// unifiedPolicyPrimary=true 时，统一工具策略是主授权源；
	// legacy permission_rules 只保留给 strict 回滚路径。
	unifiedPolicyPrimary bool
	logger               *zap.Logger
}

// NewPermissionManager 创建新的 PermissionManager
func NewPermissionManager(rules []PermissionRule, promptFn func(context.Context, PermissionRequest) (PermissionResponse, error), opts ...PermissionManagerOption) *PermissionManager {
	pm := &PermissionManager{
		rules:    rules,
		grants:   nil, // 有序 slice，初始为空
		promptFn: promptFn,
		logger:   zap.NewNop(),
	}
	for _, opt := range opts {
		opt(pm)
	}

	return pm
}

// PermissionManagerOption 用于配置 PermissionManager
type PermissionManagerOption func(*PermissionManager)

// WithLogger 设置日志记录器
func WithLogger(logger *zap.Logger) PermissionManagerOption {
	return func(pm *PermissionManager) {
		if logger != nil {
			pm.logger = logger
		}
	}
}

// WithLLMClassifier 设置 LLM 语义分类器（域E Phase 2）。
// 分类器在 glob 规则未命中且插件 hook 未处理时介入，
// 判断为安全的操作自动放行，跳过 HITL 提示。
func WithLLMClassifier(c llmClassifier) PermissionManagerOption {
	return func(pm *PermissionManager) {
		pm.classifier = c
	}
}

// WithPermissionPolicyEvaluator 注入统一工具策略裁决。
func WithPermissionPolicyEvaluator(evaluator PermissionPolicyEvaluator) PermissionManagerOption {
	return func(pm *PermissionManager) {
		pm.evaluator = evaluator
	}
}

// WithPermissionPolicyEvaluatorFunc 注入函数形式的统一工具策略裁决。
func WithPermissionPolicyEvaluatorFunc(fn func(context.Context, string, json.RawMessage) router.ToolPolicyDecision) PermissionManagerOption {
	return func(pm *PermissionManager) {
		if fn != nil {
			pm.evaluator = permissionPolicyEvaluatorFunc(fn)
		}
	}
}

// WithUnifiedPolicyPrimary 控制 legacy permission_rules 是否还能降级统一策略。
func WithUnifiedPolicyPrimary(primary bool) PermissionManagerOption {
	return func(pm *PermissionManager) {
		pm.unifiedPolicyPrimary = primary
	}
}

// SetPluginManager 设置插件管理器（用于 PermissionAsk hook）
func (m *PermissionManager) SetPluginManager(mgr *plugin.Manager) {
	m.pluginMgr = mgr
}

// SetLLMClassifier 设置 LLM 语义分类器（域E Phase 2，运行时注入）。
// 在 NewPermissionManager 之后、首次 CheckPermission 之前调用。
func (m *PermissionManager) SetLLMClassifier(c llmClassifier) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.classifier = c
}

// SetPolicyEvaluator 动态设置统一工具策略裁决器。
func (m *PermissionManager) SetPolicyEvaluator(evaluator PermissionPolicyEvaluator) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.evaluator = evaluator
}

// SetPolicyEvaluatorFunc 动态设置函数形式的统一工具策略裁决器。
func (m *PermissionManager) SetPolicyEvaluatorFunc(fn func(context.Context, string, json.RawMessage) router.ToolPolicyDecision) {
	if fn == nil {
		m.SetPolicyEvaluator(nil)
		return
	}
	m.SetPolicyEvaluator(permissionPolicyEvaluatorFunc(fn))
}

// SetPromptFn 动态设置权限提示函数（需加锁，用于 ACP 权限桥接）
func (m *PermissionManager) SetPromptFn(fn func(context.Context, PermissionRequest) (PermissionResponse, error)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.promptFn = fn
}

// SetUnifiedPolicyPrimary 动态切换统一策略是否作为主授权源。
func (m *PermissionManager) SetUnifiedPolicyPrimary(primary bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.unifiedPolicyPrimary = primary
}

// SetStore 设置权限持久化存储，并从存储中加载已有的权限授予记录
func (m *PermissionManager) SetStore(s PermissionStore) {
	m.mu.Lock()
	m.store = s
	m.mu.Unlock()

	if s == nil {
		return
	}

	// 从存储中加载已有的权限授予记录
	records, err := s.LoadGrants(context.Background())
	if err != nil {
		m.logger.Warn("从存储加载权限授予记录失败", zap.Error(err))
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	for _, rec := range records {
		key := grantKey{tool: rec.Tool, pattern: rec.Pattern}
		action := PermissionAction(rec.Action)
		// 检查是否已存在同 key 的条目
		found := false
		for i, entry := range m.grants {
			if entry.key == key {
				m.grants[i].action = action
				found = true
				break
			}
		}
		if !found {
			m.grants = append(m.grants, grantEntry{key: key, action: action})
		}
	}
	m.logger.Info("已从存储加载权限授予记录", zap.Int("count", len(records)))
}

// SetRules 热更新权限规则（运行时安全替换）
func (m *PermissionManager) SetRules(rules []PermissionRule) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rules = rules
}

// GetRules 返回当前权限规则的副本
func (m *PermissionManager) GetRules() []PermissionRule {
	m.mu.RLock()
	defer m.mu.RUnlock()
	dst := make([]PermissionRule, len(m.rules))
	copy(dst, m.rules)
	return dst
}

// CheckPermission 检查工具执行权限
func (m *PermissionManager) CheckPermission(ctx context.Context, toolName string, input json.RawMessage) error {
	if toolctx.ShouldSkipPermission(ctx) {
		return nil
	}
	inputValue := extractInputValue(toolName, input)
	inputGrantPattern := permissionInputGrantPattern(input)
	policy, hasPolicy := m.evaluateUnifiedPermissionPolicy(ctx, toolName, input)
	needsPrompt := hasPolicy && policy.Action == router.ToolPolicyAsk && policy.CallableNow
	promptRequired := false
	if hasPolicy && policy.Action == router.ToolPolicyDeny {
		if policy.CallableNow {
			promptRequired = true
		} else {
			return errs.New(errs.CodePermissionDenied, toolruntime.RecoverableToolCallErrorContent(policy.Reason,
				fmt.Sprintf("工具 %q 当前不可调用，调用未执行。请按当前路由允许的工具和参数重新构造工具调用。", toolName)))
		}
	}
	if hasPolicy && m.unifiedPolicyPrimary {
		switch policy.Action {
		case router.ToolPolicyAllow:
			if !promptRequired {
				return nil
			}
		case router.ToolPolicyAsk:
			if needsPrompt {
				promptRequired = true
				break
			}
			return errs.New(errs.CodePermissionDenied, toolruntime.RecoverableToolCallErrorContent(policy.Reason,
				fmt.Sprintf("工具 %q 当前不可调用，调用未执行。请按当前路由允许的工具和参数重新构造工具调用。", toolName)))
		}
	}

	// 内置放行：skill 空 name 仅列出技能，属于只读操作，无需审批
	if toolName == "skill" && inputValue == "" {
		return nil
	}

	// 1. 检查精确 session grants 和显式 deny。粗粒度 allow 不能覆盖后续更具体的 ask/deny 规则，
	// 否则一次 remembered allow 会把 memory.delete / feishu_api.create_approval 这类危险 action 放穿。
	if action, pattern, matched := m.checkGrantsDetailed(toolName, inputValue, inputGrantPattern); matched && (pattern != "" || action == PermissionDeny) {
		m.logger.Debug("session grant 匹配",
			zap.String("tool", toolName),
			zap.String("pattern", pattern),
			zap.String("action", string(action)),
		)
		if action == PermissionDeny {
			promptRequired = true
		}
		if promptRequired || needsPrompt {
			return m.promptPermission(ctx, toolName, input, inputValue, inputGrantPattern)
		}
		return nil
	}

	// 2. 检查配置文件规则
	if action, rulePattern, matched := m.matchRulesDetailed(toolName, inputValue); matched {
		m.logger.Debug("配置规则匹配",
			zap.String("tool", toolName),
			zap.String("pattern", rulePattern),
			zap.String("action", string(action)),
		)
		if action == PermissionAllow {
			if !needsPrompt {
				return nil
			}
			promptRequired = true
		}
		if action == PermissionDeny {
			promptRequired = true
		}
		if grantAction, grantPattern, grantMatched := m.checkGrantsDetailed(toolName, inputValue, inputGrantPattern); grantMatched {
			if grantAction == PermissionDeny {
				promptRequired = true
			}
			if grantAction == PermissionAllow && (grantPattern != "" || rulePattern == "") {
				if promptRequired || needsPrompt {
					return m.promptPermission(ctx, toolName, input, inputValue, inputGrantPattern)
				}
				return nil
			}
		}
		// action == PermissionAsk, 继续到第 3 步
	} else if action, _, matched := m.checkGrantsDetailed(toolName, inputValue, inputGrantPattern); matched {
		if action == PermissionDeny {
			promptRequired = true
		}
		if !promptRequired && !needsPrompt {
			return nil
		}
	}

	// 3. 插件 PermissionAsk hook：在提示用户前，给插件机会自动处理权限决策
	if m.pluginMgr != nil {
		hookInput := &plugin.PermissionAskInput{
			ToolName: toolName,
			Args:     input,
			Policy:   string(PermissionAsk),
		}
		// 收集当前生效的规则描述
		for _, rule := range m.rules {
			hookInput.Rules = append(hookInput.Rules, fmt.Sprintf("%s:%s:%s", rule.ToolName, rule.Pattern, rule.Action))
		}
		hookOut, hookErr := m.pluginMgr.TriggerPermissionAsk(ctx, hookInput)
		if hookErr != nil {
			m.logger.Warn("插件 PermissionAsk hook 失败", zap.Error(hookErr))
			// hook 失败不阻断主流程，继续走默认提示
		} else if hookOut != nil && hookOut.Decision != "" {
			m.logger.Info("插件处理权限请求",
				zap.String("tool", toolName),
				zap.String("decision", hookOut.Decision),
				zap.String("reason", hookOut.Reason),
			)
			if hookOut.Decision == "allow" {
				if !canPluginAutoAllowPermission(toolName, input) {
					m.logger.Warn("插件自动批准被安全边界忽略，将继续走默认权限流程",
						zap.String("tool", toolName),
						zap.String("reason", hookOut.Reason),
					)
					promptRequired = true
				} else {
					return nil
				}
			}
			if hookOut.Decision == "deny" {
				promptRequired = true
			} else if hookOut.Decision != "allow" {
				promptRequired = true
			}
		}
	}

	// 3.5 LLM 语义分类器（域E Phase 2）：在插件 hook 之后、HITL 之前介入。
	// 仅当分类器已配置时生效；分类为安全（safe=true）则自动放行。
	// 分类器内部 fail closed：出错时返回 safe=false，流程继续到 HITL。
	if m.classifier != nil {
		result := m.classifier.Classify(ctx, toolName, input)
		if result.Safe {
			m.logger.Info("LLM 分类器自动放行",
				zap.String("tool", toolName),
				zap.String("reason", result.Reason),
			)
			return nil
		}
		m.logger.Debug("LLM 分类器需要审批",
			zap.String("tool", toolName),
			zap.String("reason", result.Reason),
		)
	}

	// 4. 默认行为：提示用户
	return m.promptPermission(ctx, toolName, input, inputValue, inputGrantPattern)
}

func (m *PermissionManager) promptPermission(ctx context.Context, toolName string, input json.RawMessage, inputValue, inputGrantPattern string) error {
	if m.promptFn == nil {
		return errs.New(errs.CodePermissionDenied, toolruntime.RecoverableToolCallErrorContent("approval_channel_missing",
			fmt.Sprintf("工具 %q 需要人工审批，但审批通道未初始化。当前调用未执行；请开启 HITL/审批桥后重新发起审批。", toolName)))
	}

	// 格式化权限提示信息
	var inputMap map[string]interface{}
	if len(input) > 0 {
		_ = json.Unmarshal(input, &inputMap)
	}
	desc := m.FormatToolPrompt(toolName, inputMap)

	req := PermissionRequest{
		ToolName:    toolName,
		Description: fmt.Sprintf("允许执行 %s?", desc),
		Input:       input,
	}

	resp, err := m.promptFn(ctx, req)
	if err != nil {
		return errs.Wrap(errs.CodeExecApprovalTimeout, toolruntime.RecoverableToolCallErrorContent("approval_request_failed",
			fmt.Sprintf("工具 %q 的审批请求失败，当前调用未执行。请恢复审批通道后重新发起审批。", toolName)), err)
	}

	if !resp.Granted {
		if resp.Remember {
			m.GrantSession(toolName, rememberPermissionPattern(toolName, inputValue, inputGrantPattern, input), PermissionDeny)
		}
		return errs.New(errs.CodePermissionDenied, fmt.Sprintf("用户拒绝工具 %q", toolName))
	}

	if resp.Remember {
		m.GrantSession(toolName, rememberPermissionPattern(toolName, inputValue, inputGrantPattern, input), PermissionAllow)
	}

	return nil
}

func (m *PermissionManager) evaluateUnifiedPermissionPolicy(ctx context.Context, toolName string, input json.RawMessage) (router.ToolPolicyDecision, bool) {
	m.mu.RLock()
	evaluator := m.evaluator
	m.mu.RUnlock()
	if evaluator == nil {
		return router.ToolPolicyDecision{}, false
	}
	policy := evaluator.EvaluatePermission(ctx, toolName, input)
	if policy.Source == "" {
		policy.Source = "tool_policy"
	}
	return policy, true
}

// checkGrants 检查 session grants，返回匹配的动作和是否匹配。
// 更具体的 grant（有 pattern 的）优先于无 pattern 的 grant。
func (m *PermissionManager) checkGrants(toolName, inputValue string, extraPatterns ...string) (PermissionAction, bool) {
	pattern := ""
	if len(extraPatterns) > 0 {
		pattern = extraPatterns[0]
	}
	action, _, matched := m.checkGrantsDetailed(toolName, inputValue, pattern)
	return action, matched
}

func (m *PermissionManager) checkGrantsDetailed(toolName, inputValue, inputGrantPattern string) (PermissionAction, string, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var fallbackAction PermissionAction
	hasFallback := false

	for _, entry := range m.grants {
		if !matchGlob(entry.key.tool, toolName) {
			continue
		}
		// 无模式的 grant 作为后备匹配
		if entry.key.pattern == "" {
			fallbackAction = entry.action
			hasFallback = true
			continue
		}
		if inputGrantPattern != "" && entry.key.pattern == inputGrantPattern {
			return entry.action, entry.key.pattern, true
		}
		// 有模式的 grant 优先匹配
		if inputValue != "" && matchGlob(entry.key.pattern, inputValue) {
			return entry.action, entry.key.pattern, true
		}
	}
	if hasFallback {
		return fallbackAction, "", true
	}
	return "", "", false
}

// matchRules 按顺序评估配置文件规则，返回第一个匹配的动作
func (m *PermissionManager) matchRules(toolName, inputValue string) (PermissionAction, bool) {
	action, _, matched := m.matchRulesDetailed(toolName, inputValue)
	return action, matched
}

func (m *PermissionManager) matchRulesDetailed(toolName, inputValue string) (PermissionAction, string, bool) {
	for _, rule := range m.rules {
		if !matchGlob(rule.ToolName, toolName) {
			continue
		}
		// 无 Pattern 的规则匹配所有参数
		if rule.Pattern == "" {
			return rule.Action, "", true
		}
		// 有 Pattern 的规则需要参数匹配
		if inputValue != "" && matchGlob(rule.Pattern, inputValue) {
			return rule.Action, rule.Pattern, true
		}
	}
	return "", "", false
}

// Grant 授予工具权限（向后兼容，等同于 GrantSession(toolName, "", action)）
func (m *PermissionManager) Grant(toolName string, action PermissionAction) {
	m.GrantSession(toolName, "", action)
}

// GrantSession 授予带模式的工具权限（用于 session "always" 持久化）。
// 如果已存在相同 key 的条目，则更新；否则追加。
func (m *PermissionManager) GrantSession(tool, pattern string, action PermissionAction) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := grantKey{tool: tool, pattern: pattern}
	// 检查是否已存在同 key 的条目
	for i, entry := range m.grants {
		if entry.key == key {
			m.grants[i].action = action
			m.logger.Info("session grant 已更新",
				zap.String("tool", tool),
				zap.String("pattern", pattern),
				zap.String("action", string(action)),
			)
			// 持久化到存储
			if m.store != nil {
				rec := &store.PermissionGrantRecord{
					Tool:    tool,
					Pattern: pattern,
					Action:  string(action),
				}
				if err := m.store.SaveGrant(context.Background(), rec); err != nil {
					m.logger.Warn("持久化权限授予记录失败", zap.Error(err))
				}
			}
			return
		}
	}
	// 不存在则追加
	m.grants = append(m.grants, grantEntry{key: key, action: action})
	m.logger.Info("session grant 已添加",
		zap.String("tool", tool),
		zap.String("pattern", pattern),
		zap.String("action", string(action)),
	)

	// 持久化到存储
	if m.store != nil {
		rec := &store.PermissionGrantRecord{
			Tool:    tool,
			Pattern: pattern,
			Action:  string(action),
		}
		if err := m.store.SaveGrant(context.Background(), rec); err != nil {
			m.logger.Warn("持久化权限授予记录失败", zap.Error(err))
		}
	}
}

// Reset 重置所有 session grants
func (m *PermissionManager) Reset() {
	m.mu.Lock()
	m.grants = nil
	s := m.store
	m.mu.Unlock()

	// 同步清除持久化存储
	if s != nil {
		if err := s.DeleteAllGrants(context.Background()); err != nil {
			m.logger.Warn("清除持久化权限授予记录失败", zap.Error(err))
		}
	}
}

// GetGrants 返回当前所有 session grants 的副本
func (m *PermissionManager) GetGrants() map[string]PermissionAction {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make(map[string]PermissionAction, len(m.grants))
	for _, entry := range m.grants {
		// 格式化键：tool 或 tool:pattern
		key := entry.key.tool
		if entry.key.pattern != "" {
			key = entry.key.tool + ":" + entry.key.pattern
		}
		result[key] = entry.action
	}
	return result
}

// FormatToolPrompt 格式化工具权限提示信息
// 对于 bash 工具，使用命令 Arity 表提供更友好的描述；其他工具返回原始工具名
func (m *PermissionManager) FormatToolPrompt(toolName string, input map[string]interface{}) string {
	if toolName == "bash" {
		if cmd, ok := input["command"]; ok {
			if cmdStr, ok := cmd.(string); ok && cmdStr != "" {
				return security.FormatPermissionPrompt(cmdStr)
			}
		}
	}
	return toolName
}

// ---------------------------------------------------------------------------
// 模式匹配引擎
// ---------------------------------------------------------------------------

// matchGlob 使用 glob 通配符匹配。
// 支持 "*" 匹配任意字符，"**" 匹配包括路径分隔符在内的任意字符，
// "?" 匹配单个字符。
func matchGlob(pattern, value string) bool {
	// 特殊情况：空模式只匹配空值
	if pattern == "" {
		return value == ""
	}
	// 单独的 "*" 匹配所有
	if pattern == "*" {
		return true
	}

	// 包含 "**" 的路径模式（如 "src/**/*.go"）使用 doubleStarMatch
	if strings.Contains(pattern, "**") {
		return doubleStarMatch(pattern, value)
	}

	// 判断是否为路径模式（包含 /）
	isPathPattern := strings.Contains(pattern, "/")

	if isPathPattern {
		// 路径模式使用 filepath.Match（* 不跨目录）
		matched, err := filepath.Match(pattern, value)
		if err != nil {
			return false
		}
		return matched
	}

	// 非路径模式（如命令模式 "rm *"、工具名 "read_*"）使用 simpleGlob
	// 这里 * 匹配任意字符（包括空格和 /）
	return simpleGlob(pattern, value)
}

// simpleGlob 实现简单的 glob 匹配，其中 * 匹配任意数量的任意字符，? 匹配单个字符。
// 与 filepath.Match 不同，* 可以匹配 / 和空格。
func simpleGlob(pattern, value string) bool {
	return simpleGlobMatch(pattern, value)
}

func simpleGlobMatch(pattern, value string) bool {
	for len(pattern) > 0 {
		switch pattern[0] {
		case '*':
			// 跳过连续的 *
			for len(pattern) > 0 && pattern[0] == '*' {
				pattern = pattern[1:]
			}
			if len(pattern) == 0 {
				return true // 尾部 * 匹配所有剩余
			}
			// 尝试在 value 的每个位置匹配剩余 pattern
			for i := 0; i <= len(value); i++ {
				if simpleGlobMatch(pattern, value[i:]) {
					return true
				}
			}
			return false
		case '?':
			if len(value) == 0 {
				return false
			}
			pattern = pattern[1:]
			value = value[1:]
		default:
			if len(value) == 0 || pattern[0] != value[0] {
				return false
			}
			pattern = pattern[1:]
			value = value[1:]
		}
	}
	return len(value) == 0
}

// doubleStarMatch 处理包含 "**" 的 glob 模式。
// "**" 可以匹配任意数量的路径段（包括零个）。
// 支持多个 "**" 段，每段之间的固定文本都必须按顺序匹配。
func doubleStarMatch(pattern, value string) bool {
	// 将模式按 "**" 分割
	parts := strings.Split(pattern, "**")
	if len(parts) == 1 {
		// 没有 "**"，回退到普通匹配
		matched, _ := filepath.Match(pattern, value)
		return matched
	}

	// 逐段匹配：每个 part 必须按顺序在 value 中找到
	remaining := value
	for i, part := range parts {
		part = strings.Trim(part, "/")
		if part == "" {
			continue
		}

		if i == 0 {
			// 第一段：必须是 value 的前缀（含目录边界）
			if !strings.HasPrefix(remaining, part) {
				return false
			}
			after := remaining[len(part):]
			// 确保前缀后是目录边界或结尾
			if len(after) > 0 && after[0] != '/' {
				return false
			}
			if len(after) > 0 {
				remaining = after[1:] // 跳过 '/'
			} else {
				remaining = ""
			}
		} else if i == len(parts)-1 {
			// 最后一段：使用 glob 从尾部匹配
			matched := false
			for j := len(remaining); j >= 0; j-- {
				candidate := remaining[j:]
				if m, _ := filepath.Match(part, candidate); m {
					matched = true
					break
				}
			}
			if !matched {
				return false
			}
		} else {
			// 中间段：必须在剩余内容中找到（按顺序）
			idx := strings.Index(remaining, part)
			if idx < 0 {
				return false
			}
			// 确保前后都是目录边界
			if idx > 0 && remaining[idx-1] != '/' {
				return false
			}
			afterIdx := idx + len(part)
			if afterIdx < len(remaining) && remaining[afterIdx] != '/' {
				return false
			}
			if afterIdx < len(remaining) {
				remaining = remaining[afterIdx+1:]
			} else {
				remaining = ""
			}
		}
	}

	return true
}

// extractInputValue 从工具输入中提取用于模式匹配的关键值。
// 不同工具使用不同的字段：
// - bash: command 字段
// - edit/write_file/read_file: file_path 字段
// - 其他工具: 尝试 command → file_path → path
func extractInputValue(toolName string, input json.RawMessage) string {
	if len(input) == 0 {
		return ""
	}

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(input, &fields); err != nil {
		return ""
	}

	// 根据工具名选择关键字段
	var fieldNames []string
	switch toolName {
	case "bash":
		fieldNames = []string{"command"}
	case "edit", "write_file", "read_file":
		fieldNames = []string{"file_path"}
	case "glob", "grep":
		fieldNames = []string{"path", "pattern"}
	case "memory", "taskboard", "feishu_api", "send_im_message":
		fieldNames = []string{"action", "operation"}
	case "skill":
		fieldNames = []string{"name"}
	default:
		fieldNames = []string{"command", "file_path", "path"}
	}

	for _, name := range fieldNames {
		if raw, ok := fields[name]; ok {
			var val string
			if err := json.Unmarshal(raw, &val); err == nil && val != "" {
				return val
			}
		}
	}
	return ""
}

func permissionInputGrantPattern(input json.RawMessage) string {
	normalized := bytes.TrimSpace(input)
	if len(normalized) == 0 {
		return ""
	}
	sum := sha256.Sum256(normalized)
	return permissionInputGrantPrefix + hex.EncodeToString(sum[:])
}

func rememberPermissionPattern(toolName, inputValue, inputGrantPattern string, input json.RawMessage) string {
	toolName = strings.TrimSpace(strings.ToLower(toolName))
	if inputGrantPattern != "" {
		if router.StructuredDangerousOperation(toolName, input) {
			return inputGrantPattern
		}
		if profile, ok := router.BuiltinToolProfile(toolName); !ok || router.ProfileHasSideEffect(profile) {
			return inputGrantPattern
		}
	}
	if strings.TrimSpace(inputValue) != "" {
		return inputValue
	}
	return inputGrantPattern
}

func canPluginAutoAllowPermission(toolName string, input json.RawMessage) bool {
	toolName = strings.TrimSpace(strings.ToLower(toolName))
	if toolName == "" {
		return false
	}
	if profile, ok := router.BuiltinToolProfile(toolName); ok {
		policy := router.EvaluateToolPolicy(profile, router.ToolPolicyContext{
			Input:     input,
			ForAction: true,
		})
		return policy.Action == router.ToolPolicyAllow
	}
	if router.IsShellCommandTool(toolName) {
		return false
	}
	if router.StructuredDangerousOperation(toolName, input) {
		return false
	}
	return false
}
