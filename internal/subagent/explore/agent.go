package explore

import (
	"context"
	"encoding/json"
	"fmt"

	"go.uber.org/zap"

	"github.com/chef-guo/agents-hive/internal/errs"
	"github.com/chef-guo/agents-hive/internal/llm"
	"github.com/chef-guo/agents-hive/internal/skills"
	"github.com/chef-guo/agents-hive/internal/subagent"
)

// New 创建一个新的探索 sub-agent
// callbacks 可选：传入 AgentCallbacks 以启用进度回调和流式内容回调
func New(skillReg *skills.Registry, llmClient *llm.Client, toolBridge *skills.ToolBridge, permMgr *skills.PermissionManager, logger *zap.Logger, callbacks ...subagent.AgentCallbacks) *subagent.BaseAgent {
	return newAgent(skillReg, nil, llmClient, toolBridge, permMgr, nil, logger, callbacks...)
}

// NewWithResolver 创建使用动态 LLM 路由的探索 sub-agent。
func NewWithResolver(skillReg *skills.Registry, resolver subagent.LLMClientResolver, toolBridge *skills.ToolBridge, permMgr *skills.PermissionManager, logger *zap.Logger, callbacks ...subagent.AgentCallbacks) *subagent.BaseAgent {
	return newAgent(skillReg, resolver, nil, toolBridge, permMgr, nil, logger, callbacks...)
}

// NewWithPromptLoader 创建带 PromptLoader 的探索 sub-agent。
func NewWithPromptLoader(skillReg *skills.Registry, resolver subagent.LLMClientResolver, llmClient *llm.Client, toolBridge *skills.ToolBridge, permMgr *skills.PermissionManager, promptLoader any, logger *zap.Logger, callbacks ...subagent.AgentCallbacks) *subagent.BaseAgent {
	return newAgent(skillReg, resolver, llmClient, toolBridge, permMgr, promptLoader, logger, callbacks...)
}

// newAgent 内部构造函数，支持静态 client 和 resolver 两种模式
func newAgent(skillReg *skills.Registry, resolver subagent.LLMClientResolver, llmClient *llm.Client, toolBridge *skills.ToolBridge, permMgr *skills.PermissionManager, promptLoader any, logger *zap.Logger, callbacks ...subagent.AgentCallbacks) *subagent.BaseAgent {
	card := subagent.AgentCard{
		ID:          "explore",
		Name:        "Explore Agent",
		Description: "快速探索代码库结构、关键文件和代码模式",
		Skills:      []string{},
	}

	// 创建 AgentLoop 用于工具调用
	var loop *subagent.AgentLoop
	if toolBridge != nil {
		if resolver != nil {
			loop = subagent.NewAgentLoopWithResolver("explore", resolver, toolBridge, permMgr, logger)
		} else if llmClient != nil {
			loop = subagent.NewAgentLoop("explore", llmClient, toolBridge, permMgr, logger)
		}
		if loop != nil {
			// 设置较短的迭代轮次限制（快速探索）
			loop.SetMaxTurns(50)
			if len(callbacks) > 0 {
				if callbacks[0].ProgressFn != nil {
					loop.SetProgressCallback(callbacks[0].ProgressFn)
				}
				if callbacks[0].StreamFn != nil {
					loop.SetStreamCallback(callbacks[0].StreamFn)
				}
				if callbacks[0].LLMCompleteFn != nil {
					loop.SetLLMCompleteCallback(callbacks[0].LLMCompleteFn)
				}
			}
		}
	}

	handler := makeExploreHandler(loop, skillReg, promptLoader, logger)
	return subagent.NewBaseAgent(card, handler, skillReg, logger)
}

// loadPrompt 从 PromptLoader 加载 prompt，nil 时返回 defaultVal
func loadPrompt(promptLoader any, key, defaultVal string) string {
	if promptLoader == nil {
		return defaultVal
	}
	type loader interface {
		LoadOrDefault(string, string) string
	}
	if l, ok := promptLoader.(loader); ok {
		return l.LoadOrDefault(key, defaultVal)
	}
	return defaultVal
}

// makeExploreHandler 构建探索任务处理函数（New 和 NewWithResolver 共用）
func makeExploreHandler(loop *subagent.AgentLoop, _ *skills.Registry, promptLoader any, logger *zap.Logger) subagent.TaskHandler {
	return func(ctx context.Context, req subagent.TaskRequest) subagent.TaskResponse {
		ctx = subagent.ContextFromTaskRequest(ctx, req)
		if loop != nil {
			if req.SessionID != "" {
				loop.SetSessionID(req.SessionID)
			}
			if req.UserID != "" {
				loop.SetUserID(req.UserID)
			}
		}
		payload, skillContext := subagent.ExtractPayload(req)

		var exploreReq ExploreRequest
		if err := json.Unmarshal(payload, &exploreReq); err != nil {
			return subagent.TaskResponse{
				Status: "failed",
				Error:  "invalid explore request: " + err.Error(),
			}
		}

		// 使用 AgentLoop 执行探索或回退
		var result ExploreOutput
		if loop != nil {
			var err error
			result, err = exploreWithAgentLoop(ctx, loop, exploreReq, skillContext, promptLoader)
			if err != nil {
				logger.Warn("Agent loop 探索失败，回退到存根", zap.Error(err))
				result = exploreStub(exploreReq)
			}
		} else {
			result = exploreStub(exploreReq)
		}

		resultJSON, err := json.Marshal(result)
		if err != nil {
			return subagent.TaskResponse{
				Status: "failed",
				Error:  fmt.Sprintf("序列化探索结果失败: %v", err),
			}
		}
		return subagent.TaskResponse{
			Status: "completed",
			Result: resultJSON,
		}
	}
}

const exploreSystemPrompt = `你是一个高效的代码探索专家。你的任务是快速了解代码库结构并生成清晰的探索报告。

## 工作原则
- 优先使用 glob 和 grep 快速定位关键信息
- 只读取必要的关键文件（如 README、主要配置文件、入口文件）
- 避免读取过多文件，保持高效
- 15 轮内完成探索任务
- 输出简洁清晰的结构化结果

## 可用工具
- **glob**: 快速查找文件模式（如 *.go, **/*.ts）
- **grep**: 搜索代码关键字和模式
- **read_file**: 读取文件内容（谨慎使用，只读关键文件）
- **bash**: 执行只读命令（如 ls -la, find, git log, tree 等）

## 探索策略
1. **项目结构分析**
   - 使用 bash (ls, tree) 了解目录结构
   - 使用 glob 查找特定类型文件分布

2. **关键文件识别**
   - README、LICENSE、配置文件（package.json, go.mod, Makefile 等）
   - 入口文件（main.go, index.ts, app.py 等）
   - 核心模块和组件

3. **代码模式发现**
   - 使用 grep 查找常见模式（如函数定义、类声明、接口等）
   - 识别架构模式（MVC、分层架构等）
   - 发现技术栈和依赖

4. **洞察总结**
   - 代码组织方式
   - 技术选型和依赖
   - 架构特点
   - 潜在改进点

## 响应格式
在探索完成后，以 JSON 格式返回结果：

{
  "summary": "项目整体概览（1-2 句话）",
  "structure": {
    "root_path": "/path/to/project",
    "directories": {
      "/cmd/": "命令行入口程序",
      "/internal/": "内部包（不对外暴露）",
      "/pkg/": "公共包（可被外部引用）"
    },
    "file_types": {
      ".go": 150,
      ".md": 10,
      ".yaml": 5
    }
  },
  "key_files": [
    {
      "path": "/go.mod",
      "purpose": "Go 模块定义和依赖管理",
      "importance": "high"
    },
    {
      "path": "/cmd/main.go",
      "purpose": "程序入口",
      "importance": "high"
    }
  ],
  "patterns": [
    {
      "pattern": "分层架构",
      "description": "使用 internal/ 组织内部包，遵循标准 Go 项目布局",
      "examples": ["internal/api/", "internal/service/", "internal/store/"]
    }
  ],
  "insights": [
    "项目使用标准 Go 项目布局",
    "采用依赖注入模式（通过构造函数）",
    "包含完善的测试覆盖（_test.go 文件）"
  ]
}

## 重要约束
- **禁止**使用 write_file、edit、multiedit 或 filesystem.write/edit/multiedit 等修改文件的工具
- **禁止**执行破坏性的 bash 命令（rm, mv, git reset 等）
- **必须**在 15 轮内完成探索
- **必须**返回有效的 JSON 格式结果`

func exploreWithAgentLoop(ctx context.Context, loop *subagent.AgentLoop, req ExploreRequest, skillContext string, promptLoader any) (ExploreOutput, error) {
	var userMsg string
	userMsg = fmt.Sprintf("目标: %s\n", req.effectiveTarget())
	if req.Focus != "" {
		userMsg += fmt.Sprintf("聚焦领域: %s\n", req.Focus)
	}
	if req.Depth != "" {
		userMsg += fmt.Sprintf("探索深度: %s\n", req.Depth)
	} else {
		userMsg += "探索深度: normal\n"
	}
	if skillContext != "" {
		userMsg += fmt.Sprintf("\n附加上下文:\n%s\n", skillContext)
	}
	userMsg += "\n\n请对此目标进行代码探索，使用可用工具快速了解项目结构、关键文件和代码模式。完成后以 JSON 格式返回探索结果。"

	systemPrompt := loadPrompt(promptLoader, "subagents/explore", exploreSystemPrompt)
	initialMessages := []llm.MessageWithTools{
		{Role: "user", Content: llm.NewTextContent(userMsg)},
	}

	// 创建工具白名单（只读工具）
	toolFilter := skills.NewToolFilter([]string{
		"glob",
		"grep",
		"read_file",
		"bash", // bash 命令的安全性由 SafeExec 检查器处理
	})

	// 运行 agent loop
	resultText, err := loop.Run(ctx, systemPrompt, initialMessages, toolFilter)
	if err != nil {
		return ExploreOutput{}, errs.Wrap(errs.CodeAgentUnavailable, "agent loop failed", err)
	}

	// 解析 JSON 结果
	var result ExploreOutput
	if err := json.Unmarshal([]byte(resultText), &result); err != nil {
		// 如果不是有效的 JSON，返回摘要结果
		return ExploreOutput{
			Summary: resultText,
			Insights: []string{
				"探索完成（输出格式非标准 JSON，已转换为摘要）",
			},
		}, nil
	}

	return result, nil
}

// exploreStub 在没有 LLM 可用时提供回退方案
func exploreStub(req ExploreRequest) ExploreOutput {
	if req.effectiveTarget() == "" {
		return ExploreOutput{
			Summary: "未提供探索目标",
			Structure: ProjectStructure{
				RootPath:    "",
				Directories: make(map[string]string),
				FileTypes:   make(map[string]int),
			},
			KeyFiles: []KeyFile{},
			Patterns: []CodePattern{},
			Insights: []string{"警告: 未提供探索目标"},
		}
	}

	return ExploreOutput{
		Summary: "探索完成: " + req.effectiveTarget() + " (no LLM configured — stub result)",
		Structure: ProjectStructure{
			RootPath:    req.effectiveTarget(),
			Directories: make(map[string]string),
			FileTypes:   make(map[string]int),
		},
		KeyFiles: []KeyFile{
			{
				Path:       req.effectiveTarget(),
				Purpose:    "探索目标",
				Importance: "high",
			},
		},
		Patterns: []CodePattern{},
		Insights: []string{
			"初步分析: " + req.effectiveTarget(),
			"注意: LLM 未配置，返回存根结果",
		},
	}
}
