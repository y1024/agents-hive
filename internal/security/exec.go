package security

import (
	"bytes"
	"context"
	"os/exec"
	"time"

	"go.uber.org/zap"

	"github.com/chef-guo/agents-hive/internal/errs"
)

// ExecPolicy 命令执行策略
type ExecPolicy string

const (
	PolicyAllow ExecPolicy = "allow"
	PolicyAsk   ExecPolicy = "ask"
	PolicyDeny  ExecPolicy = "deny"
)

// ExecRule 命令执行规则
type ExecRule struct {
	Pattern     string     `json:"pattern"`
	Policy      ExecPolicy `json:"policy"`
	Description string     `json:"description"`
}

// ExecRequest 安全执行请求
type ExecRequest struct {
	Command string            `json:"command"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	WorkDir string            `json:"work_dir,omitempty"`
	Timeout time.Duration     `json:"timeout,omitempty"`
}

// ExecResult 执行结果
type ExecResult struct {
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	ExitCode int    `json:"exit_code"`
}

// SafeExecutor 安全命令执行器
type SafeExecutor struct {
	rules         []ExecRule
	defaultPolicy ExecPolicy // 未匹配规则时的默认策略，默认 PolicyAllow
	envValidator  *EnvValidator
	astAnalyzer   *ASTAnalyzer // AST 分析器（可选）
	logger        *zap.Logger
}

// NewSafeExecutor 创建安全执行器。
// 内置危险规则（BuiltinDangerousRules）始终在用户规则之前匹配，不可关闭。
func NewSafeExecutor(rules []ExecRule, logger *zap.Logger) *SafeExecutor {
	return NewSafeExecutorWithDefault(rules, PolicyAllow, logger)
}

// NewSafeExecutorWithDefault 创建安全执行器，支持自定义默认策略。
func NewSafeExecutorWithDefault(rules []ExecRule, defaultPolicy ExecPolicy, logger *zap.Logger) *SafeExecutor {
	// 内置规则优先级最高，拼接在用户规则之前
	allRules := make([]ExecRule, 0, len(BuiltinDangerousRules)+len(rules))
	allRules = append(allRules, BuiltinDangerousRules...)
	allRules = append(allRules, rules...)
	if defaultPolicy == "" {
		defaultPolicy = PolicyAllow
	}
	return &SafeExecutor{
		rules:         allRules,
		defaultPolicy: defaultPolicy,
		envValidator:  NewEnvValidator(),
		logger:        logger,
	}
}

// NewSafeExecutorWithAST 创建带 AST 分析能力的安全执行器。
// 内置危险规则（BuiltinDangerousRules）始终在用户规则之前匹配，不可关闭。
func NewSafeExecutorWithAST(rules []ExecRule, projectRoot string, logger *zap.Logger) *SafeExecutor {
	allRules := make([]ExecRule, 0, len(BuiltinDangerousRules)+len(rules))
	allRules = append(allRules, BuiltinDangerousRules...)
	allRules = append(allRules, rules...)
	return &SafeExecutor{
		rules:         allRules,
		defaultPolicy: PolicyAllow,
		envValidator:  NewEnvValidator(),
		astAnalyzer:   NewASTAnalyzer(projectRoot, logger),
		logger:        logger,
	}
}

// MatchPolicy 匹配命令的执行策略（导出方法，供 tools 包使用）
// 如果配置了 AST 分析器，会先进行 AST 分析做更精细的判断；
// AST 解析失败时降级到原有白名单逻辑
func (e *SafeExecutor) MatchPolicy(command string) ExecPolicy {
	policy, _ := e.MatchPolicyWithRule(command)
	return policy
}

// MatchPolicyWithRule 同 MatchPolicy，但同时返回命中的规则 pattern（未命中规则时返回空）。
// 供需要在 metric / audit log 中标注"命中哪条规则"的调用方使用。
// AST 拦截路径返回伪 pattern：ast:dangerous / ast:external-path，便于与正则规则区分。
func (e *SafeExecutor) MatchPolicyWithRule(command string) (ExecPolicy, string) {
	if e.astAnalyzer != nil {
		if policy, pattern, ok := e.matchPolicyByASTWithTag(command); ok {
			return policy, pattern
		}
	}
	return e.matchPolicyByRulesWithPattern(command)
}

// matchPolicyByASTWithTag 在 AST 决策时附带一个固定 tag，便于 metric 聚合。
func (e *SafeExecutor) matchPolicyByASTWithTag(command string) (ExecPolicy, string, bool) {
	info, err := e.astAnalyzer.Analyze(command)
	if err != nil {
		e.logger.Debug("AST 解析失败，降级到规则匹配", zap.Error(err))
		return "", "", false
	}
	if IsDangerous(info, command) {
		e.logger.Warn("AST 检测到危险命令",
			zap.String("command", command),
			zap.Strings("commands", info.Commands),
		)
		return PolicyAsk, "ast:dangerous", true
	}
	if info.IsExternal {
		e.logger.Info("AST 检测到项目外路径访问，需要审批",
			zap.String("command", command),
			zap.Strings("paths", info.FilePaths),
		)
		return PolicyAsk, "ast:external-path", true
	}
	return "", "", false
}

// matchPolicyByRulesWithPattern 规则匹配并返回命中 pattern。
func (e *SafeExecutor) matchPolicyByRulesWithPattern(command string) (ExecPolicy, string) {
	for _, rule := range e.rules {
		if MatchPattern(rule.Pattern, command) {
			return rule.Policy, rule.Pattern
		}
	}
	return e.defaultPolicy, ""
}

// Execute 安全执行命令
func (e *SafeExecutor) Execute(ctx context.Context, req ExecRequest) (*ExecResult, error) {
	// 1. 匹配规则
	policy := e.MatchPolicy(req.Command)

	// 2. 检查策略
	switch policy {
	case PolicyDeny:
		return nil, errs.New(errs.CodeExecApprovalTimeout, "命令需要审批: "+req.Command)
	case PolicyAsk:
		// ask 策略由调用方（tools 包）处理 HITL 审批
		// 返回结构化错误，由调用方走 HITL 审批后以 PolicyAllow 重新调用
		return nil, errs.New(errs.CodeExecApprovalTimeout, "命令需要审批: "+req.Command)
	case PolicyAllow:
		// 直接执行
	}

	// 3. 解析命令
	command, args := req.Command, req.Args
	if len(args) == 0 {
		parsed := ParseCommand(req.Command)
		if len(parsed) > 0 {
			command = parsed[0]
			args = parsed[1:]
		}
	}

	// 4. 执行
	timeout := req.Timeout
	if timeout == 0 {
		timeout = 120 * time.Second
	}
	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(execCtx, command, args...)
	if req.WorkDir != "" {
		cmd.Dir = req.WorkDir
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			return nil, errs.Wrap(errs.CodeExecDenied, "命令执行失败", err)
		}
	}

	return &ExecResult{
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		ExitCode: exitCode,
	}, nil
}
