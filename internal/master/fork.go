package master

import (
	"context"
	"encoding/json"
	"fmt"

	"go.uber.org/zap"

	"github.com/chef-guo/agents-hive/internal/auth"
	"github.com/chef-guo/agents-hive/internal/errs"
	"github.com/chef-guo/agents-hive/internal/skills"
	"github.com/chef-guo/agents-hive/internal/subagent"
	"github.com/chef-guo/agents-hive/internal/toolctx"
	"github.com/chef-guo/agents-hive/internal/tools"
)

// ForkExecutor 在隔离的 sub-agent 上下文中运行 skill，
// 使用 AgentFactory 创建具有真正 LLM 推理能力的临时 agent
type ForkExecutor struct {
	factory *subagent.AgentFactory
	logger  *zap.Logger
}

// NewForkExecutor 创建一个新的 ForkExecutor
func NewForkExecutor(factory *subagent.AgentFactory, logger *zap.Logger) *ForkExecutor {
	return &ForkExecutor{
		factory: factory,
		logger:  logger,
	}
}

// ExecuteForked 渲染 skill 并在隔离的 sub-agent 中运行它。
// 流程: 渲染 skill → 通过 AgentFactory 创建临时 agent → 发送任务 → LLM 推理 → 收集结果 → 清理
func (f *ForkExecutor) ExecuteForked(ctx context.Context, skill *skills.Skill, rctx skills.RenderContext, executor skills.ShellExecutor) (string, error) {
	if f.factory == nil {
		return "", errs.New(errs.CodeSkillForkFailed, "AgentFactory 未初始化，无法执行 fork")
	}

	if err := skill.LoadContent(); err != nil {
		return "", errs.Wrap(errs.CodeSkillForkFailed, "load skill content for fork", err)
	}

	rendered := skill.Render(rctx)

	// Process dynamic context if executor is available
	if executor != nil {
		var err error
		rendered, err = skills.ExecuteDynamicContext(rendered, executor)
		if err != nil {
			f.logger.Warn("fork 中的动态上下文失败", zap.Error(err))
		}
	}

	// 构建 agent 规格
	spec := subagent.AgentSpec{
		Name:         fmt.Sprintf("fork-%s", skill.Metadata.Name),
		Description:  fmt.Sprintf("Forked execution of skill: %s", skill.Metadata.Name),
		SystemPrompt: rendered,
		MaxTurns:     50,
	}

	// 如果 skill 定义了工具白名单，透传到 agent
	if len(skill.Metadata.AllowedTools) > 0 {
		spec.Tools = skill.Metadata.AllowedTools
	}

	f.logger.Info("创建 forked agent（LLM 推理模式）",
		zap.String("skill", skill.Metadata.Name),
	)

	// 通过 AgentFactory 创建真正有 LLM 推理能力的 agent
	agent, err := f.factory.CreateAgent(ctx, spec)
	if err != nil {
		return "", errs.Wrap(errs.CodeSkillForkFailed, "创建 forked agent 失败", err)
	}

	// 确保退出时清理
	defer func() {
		if destroyErr := f.factory.DestroyAgent(agent.ID()); destroyErr != nil {
			f.logger.Warn("清理 forked agent 失败",
				zap.String("agent_id", agent.ID()), zap.Error(destroyErr))
		}
		f.logger.Debug("已清理 forked agent", zap.String("agent_id", agent.ID()))
	}()

	// 构建并发送任务
	taskPayload, _ := json.Marshal(map[string]string{
		"instruction": "请根据系统提示词中的指令执行任务，并返回执行结果。",
	})

	tc := toolctx.GetToolContext(ctx)
	taskReq := subagent.TaskRequest{
		ID:            fmt.Sprintf("fork-%s", skill.Metadata.Name),
		Type:          "execute",
		SessionID:     toolctx.GetSessionID(ctx),
		UserID:        auth.UserIDFrom(ctx),
		TraceID:       tools.DeriveChildTraceID(tc.TraceID, agent.ID()),
		ParentSpanID:  tc.SpanID,
		ParentTraceID: tc.TraceID,
		TurnID:        tc.TurnIDOrTraceID(),
		ToolCallID:    tc.ToolCallID,
		Payload:       taskPayload,
	}

	resp, err := agent.SendTask(ctx, taskReq)
	if err != nil {
		return "", errs.Wrap(errs.CodeSkillForkFailed, "发送任务到 forked agent 失败", err)
	}

	if resp.Status == "failed" {
		errMsg := resp.Error
		if errMsg == "" {
			errMsg = "forked agent 执行失败"
		}
		return "", errs.New(errs.CodeSkillForkFailed, errMsg)
	}

	// 提取结果
	if resp.Result != nil {
		var resultMap map[string]string
		if json.Unmarshal(resp.Result, &resultMap) == nil {
			if summary, ok := resultMap["summary"]; ok && summary != "" {
				return summary, nil
			}
		}
		return string(resp.Result), nil
	}

	return rendered, nil
}
