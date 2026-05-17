package bootstrap

import (
	"context"
	"errors"
	"strings"

	"go.uber.org/zap"

	"github.com/chef-guo/agents-hive/internal/airouter"
	"github.com/chef-guo/agents-hive/internal/llm"
)

var errKBSummaryNoModel = errors.New("kb summary generator: no summary llm model")

type airouterKBSummaryGenerator struct {
	router *airouter.Router
	logger *zap.Logger
}

func newAirouterKBSummaryGenerator(router *airouter.Router, logger *zap.Logger) *airouterKBSummaryGenerator {
	if router == nil {
		return nil
	}
	return &airouterKBSummaryGenerator{router: router, logger: logger}
}

func (g *airouterKBSummaryGenerator) Summarize(ctx context.Context, text string, model string) (string, error) {
	client := g.router.GetLLMClientForModel(airouter.TaskSummary, strings.TrimSpace(model))
	if client == nil {
		client = g.router.GetLLMClient(airouter.TaskSummary)
	}
	if client == nil {
		return "", errKBSummaryNoModel
	}
	resp, err := client.Chat(ctx, llm.ChatRequest{
		SystemPrompt: "你是知识库文档摘要器。请保留关键事实、约束、步骤和专有名词，输出简洁中文摘要，不添加原文没有的信息。",
		Messages: []llm.Message{
			{Role: "user", Content: llm.NewTextContent("请摘要以下知识库段落：\n\n" + text)},
		},
		Temperature: 0.2,
		MaxTokens:   512,
	})
	if err != nil {
		if g.logger != nil {
			g.logger.Warn("KB 摘要生成失败", zap.Error(err))
		}
		return "", err
	}
	return strings.TrimSpace(resp.Content), nil
}
