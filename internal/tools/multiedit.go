package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"go.uber.org/zap"

	"github.com/chef-guo/agents-hive/internal/errs"
	"github.com/chef-guo/agents-hive/internal/mcphost"
)

// multieditInput 定义多文件编辑的输入参数
type multieditInput struct {
	Edits []struct {
		Path       string `json:"path"`
		OldString  string `json:"old_string"`
		NewString  string `json:"new_string"`
		ReplaceAll bool   `json:"replace_all,omitempty"`
	} `json:"edits"`
}

// editOperation 表示单个编辑操作
type editOperation struct {
	path       string
	oldString  string
	newString  string
	replaceAll bool
	backup     string      // 备份内容
	origPerm   os.FileMode // 原始文件权限
}

// registerMultiEdit 注册 multiedit 工具到 MCP host
func registerMultiEdit(host *mcphost.Host, logger *zap.Logger, tracker *ReadTracker) {
	schema, _ := json.Marshal(map[string]any{
		"type": "object",
		"properties": map[string]any{
			"edits": map[string]any{
				"type":        "array",
				"description": "要执行的编辑操作列表",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"path":        map[string]any{"type": "string", "description": "文件的绝对路径"},
						"old_string":  map[string]any{"type": "string", "description": "要查找替换的原始文本"},
						"new_string":  map[string]any{"type": "string", "description": "替换后的文本"},
						"replace_all": map[string]any{"type": "boolean", "description": "是否替换所有匹配（默认 false）"},
					},
					"required": []string{"path", "old_string", "new_string"},
				},
			},
		},
		"required": []string{"edits"},
	})

	host.RegisterTool(
		mcphost.ToolDefinition{
			Name:        "multiedit",
			Description: "对多个文件同时进行编辑，支持原子性操作（全部成功或全部回滚）。注意：同一文件的多个编辑操作各自独立于原始文件内容，不会串联应用。如果需要对同一文件串联编辑，请分多次调用。",
			InputSchema: schema,
			Core:        true,
		},
		func(ctx context.Context, input json.RawMessage) (*mcphost.ToolResult, error) {
			var params multieditInput
			if err := json.Unmarshal(input, &params); err != nil {
				return errorResult("输入无效: " + err.Error()), nil
			}

			return executeMultiEditInput(ctx, params, tracker, logger)
		},
	)
}

func executeMultiEditInput(ctx context.Context, params multieditInput, tracker *ReadTracker, logger *zap.Logger) (*mcphost.ToolResult, error) {
	_ = ctx

	// 验证输入
	if len(params.Edits) == 0 {
		return errorResult("编辑列表不能为空"), nil
	}

	if len(params.Edits) > 100 {
		return errorResult("编辑操作数量不能超过 100 个"), nil
	}

	// 路径安全校验
	resolvedPaths := make([]string, len(params.Edits))
	for i, e := range params.Edits {
		resolvedPath, err := resolveToolPath(e.Path)
		if err != nil {
			return errorResult(err.Error()), nil
		}
		resolvedPaths[i] = resolvedPath
	}

	// 转换为内部操作结构
	operations := make([]editOperation, 0, len(params.Edits))
	for i, e := range params.Edits {
		operations = append(operations, editOperation{
			path:       resolvedPaths[i],
			oldString:  e.OldString,
			newString:  e.NewString,
			replaceAll: e.ReplaceAll,
		})
	}

	// 执行多文件编辑
	result, err := executeMultiEdit(operations, tracker, logger)
	if err != nil {
		return errorResult(err.Error()), nil
	}

	return textResult(result), nil
}

// executeMultiEdit 执行多文件编辑，确保原子性
func executeMultiEdit(operations []editOperation, tracker *ReadTracker, logger *zap.Logger) (string, error) {
	// 阶段 0: 获取所有涉及文件的锁（按路径排序避免死锁）
	paths := make([]string, 0, len(operations))
	for _, op := range operations {
		paths = append(paths, op.path)
	}
	defer globalFileLock.LockFiles(paths)()

	// 阶段 1: 验证所有文件的 ReadTracker
	logger.Debug("阶段 1: 验证 ReadTracker", zap.Int("count", len(operations)))
	for i := range operations {
		if tracker != nil {
			if err := tracker.CheckRead(operations[i].path); err != nil {
				return "", errs.New(errs.CodeInvalidInput,
					fmt.Sprintf("文件 %q %v", operations[i].path, err))
			}
		}
	}

	// 阶段 2: 读取所有文件并创建备份
	logger.Debug("阶段 2: 读取文件并创建备份", zap.Int("count", len(operations)))
	for i := range operations {
		fi, err := os.Stat(operations[i].path)
		if err != nil {
			return "", errs.New(errs.CodeStoreReadFailed,
				fmt.Sprintf("获取文件信息 %q 失败: %v", operations[i].path, err))
		}
		operations[i].origPerm = fi.Mode().Perm()

		data, err := os.ReadFile(operations[i].path)
		if err != nil {
			return "", errs.New(errs.CodeStoreReadFailed,
				fmt.Sprintf("读取文件 %q 失败: %v", operations[i].path, err))
		}
		operations[i].backup = string(data)
	}

	// 阶段 3: 验证所有编辑操作的可行性（支持模糊匹配降级）
	logger.Debug("阶段 3: 验证编辑操作可行性", zap.Int("count", len(operations)))
	newContents := make([]string, len(operations))
	fuzzyNotes := make([]string, len(operations))
	for i, op := range operations {
		content := op.backup

		// 确定实际用于替换的 oldString
		actualOldString := op.oldString
		if !strings.Contains(content, op.oldString) {
			// 精确匹配失败，尝试模糊查找
			found, level, ok := FuzzyFindString(content, op.oldString, logger)
			if !ok {
				return "", errs.New(errs.CodeInvalidInput,
					fmt.Sprintf("文件 %q 中未找到 old_string（精确匹配和模糊匹配均失败）", op.path))
			}
			actualOldString = found
			fuzzyNotes[i] = fmt.Sprintf("（通过「%s」匹配）", matchLevelNames[level])
		}

		// 计算替换后的内容
		var newContent string
		if op.replaceAll {
			newContent = strings.ReplaceAll(content, actualOldString, op.newString)
		} else {
			count := strings.Count(content, actualOldString)
			if count > 1 {
				return "", errs.New(errs.CodeInvalidInput,
					fmt.Sprintf("文件 %q 中 old_string 出现 %d 次——请使用 replace_all 或提供更多上下文", op.path, count))
			}
			newContent = strings.Replace(content, actualOldString, op.newString, 1)
		}

		newContents[i] = newContent
	}

	// 阶段 4: 应用所有编辑
	logger.Debug("阶段 4: 应用所有编辑", zap.Int("count", len(operations)))
	successCount := 0
	var firstError error

	for i, op := range operations {
		if err := os.WriteFile(op.path, []byte(newContents[i]), op.origPerm); err != nil {
			firstError = errs.New(errs.CodeStoreWriteFailed,
				fmt.Sprintf("写入文件 %q 失败: %v", op.path, err))
			break
		}
		successCount++
	}

	// 如果有失败，回滚所有已成功的编辑
	if firstError != nil {
		logger.Warn("编辑失败，开始回滚", zap.Error(firstError), zap.Int("successCount", successCount))
		rollbackCount := 0
		for i := 0; i < successCount; i++ {
			if err := os.WriteFile(operations[i].path, []byte(operations[i].backup), operations[i].origPerm); err != nil {
				logger.Error("回滚失败", zap.String("path", operations[i].path), zap.Error(err))
			} else {
				rollbackCount++
			}
		}
		return "", errs.New(errs.CodeStoreWriteFailed,
			fmt.Sprintf("编辑失败并已回滚 %d/%d 个文件: %v", rollbackCount, successCount, firstError))
	}

	// E4: 所有编辑成功后记录每个文件的 hash（用于后续外部修改检测）
	if globalFileTracker != nil {
		for _, op := range operations {
			_ = globalFileTracker.Track(op.path) // 忽略错误（非关键路径）
		}
	}

	// 成功
	logger.Info("多文件编辑成功", zap.Int("count", len(operations)))

	// 构建成功消息
	var msg strings.Builder
	msg.WriteString(fmt.Sprintf("成功编辑 %d 个文件:\n", len(operations)))
	for i, op := range operations {
		replaceMode := "单次替换"
		if op.replaceAll {
			replaceMode = "全部替换"
		}
		msg.WriteString(fmt.Sprintf("  - %s (%s)%s\n", op.path, replaceMode, fuzzyNotes[i]))
	}

	return msg.String(), nil
}
