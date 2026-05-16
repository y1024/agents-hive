package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"go.uber.org/zap"

	"github.com/chef-guo/agents-hive/internal/mcphost"
)

type filesystemInput struct {
	Action          string           `json:"action"`
	Path            string           `json:"path,omitempty"`
	Recursive       bool             `json:"recursive,omitempty"`
	MaxDepth        int              `json:"max_depth,omitempty"`
	Pattern         string           `json:"pattern,omitempty"`
	Glob            string           `json:"glob,omitempty"`
	TypeFilter      string           `json:"type,omitempty"`
	Context         int              `json:"context,omitempty"`
	Before          int              `json:"before,omitempty"`
	After           int              `json:"after,omitempty"`
	MaxResults      int              `json:"max_results,omitempty"`
	Multiline       bool             `json:"multiline,omitempty"`
	Offset          int              `json:"offset,omitempty"`
	Limit           int              `json:"limit,omitempty"`
	ShowLineNumbers bool             `json:"show_line_numbers,omitempty"`
	Content         *string          `json:"content,omitempty"`
	OldString       string           `json:"old_string,omitempty"`
	NewString       *string          `json:"new_string,omitempty"`
	ReplaceAll      bool             `json:"replace_all,omitempty"`
	Edits           []filesystemEdit `json:"edits,omitempty"`
}

type filesystemEdit struct {
	Path       string  `json:"path"`
	OldString  string  `json:"old_string"`
	NewString  *string `json:"new_string"`
	ReplaceAll bool    `json:"replace_all,omitempty"`
}

type filesystemErrorCode string

const (
	filesystemErrInvalidInput  filesystemErrorCode = "invalid_input"
	filesystemErrMissingField  filesystemErrorCode = "missing_field"
	filesystemErrUnknownAction filesystemErrorCode = "unknown_action"
	filesystemErrExecution     filesystemErrorCode = "execution_failed"
)

func registerFilesystem(host *mcphost.Host, logger *zap.Logger, tracker *ReadTracker) {
	schema, _ := json.Marshal(map[string]any{
		"type": "object",
		"properties": map[string]any{
			"action": map[string]any{
				"type":        "string",
				"description": "文件系统动作",
				"enum":        []string{"list", "glob", "grep", "read", "write", "edit", "multiedit"},
			},
			"path":              map[string]any{"type": "string"},
			"recursive":         map[string]any{"type": "boolean"},
			"max_depth":         map[string]any{"type": "integer"},
			"pattern":           map[string]any{"type": "string"},
			"glob":              map[string]any{"type": "string"},
			"type":              map[string]any{"type": "string"},
			"context":           map[string]any{"type": "integer"},
			"before":            map[string]any{"type": "integer"},
			"after":             map[string]any{"type": "integer"},
			"max_results":       map[string]any{"type": "integer"},
			"multiline":         map[string]any{"type": "boolean"},
			"offset":            map[string]any{"type": "integer"},
			"limit":             map[string]any{"type": "integer"},
			"show_line_numbers": map[string]any{"type": "boolean"},
			"content":           map[string]any{"type": "string"},
			"old_string":        map[string]any{"type": "string"},
			"new_string":        map[string]any{"type": "string"},
			"replace_all":       map[string]any{"type": "boolean"},
			"edits": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"path":        map[string]any{"type": "string"},
						"old_string":  map[string]any{"type": "string"},
						"new_string":  map[string]any{"type": "string"},
						"replace_all": map[string]any{"type": "boolean"},
					},
					"required": []string{"path", "old_string", "new_string"},
				},
			},
		},
		"required": []string{"action"},
	})

	host.RegisterTool(mcphost.ToolDefinition{
		Name:              "filesystem",
		Description:       "结构化文件系统工具。通过 action 执行目录列表、文件匹配、内容搜索、文件读取、文件写入、精确编辑和多文件原子编辑。用于常规文件系统读写；需要运行测试、构建、git 或任意 shell 命令时使用 bash。",
		InputSchema:       schema,
		Core:              true,
		IsConcurrencySafe: false,
	}, func(ctx context.Context, input json.RawMessage) (*mcphost.ToolResult, error) {
		var params filesystemInput
		if err := json.Unmarshal(input, &params); err != nil {
			return filesystemErrorResult("unknown", filesystemErrInvalidInput, "输入无效"), nil
		}
		return executeFilesystem(ctx, params, tracker, logger)
	})
}

func executeFilesystem(ctx context.Context, params filesystemInput, tracker *ReadTracker, logger *zap.Logger) (*mcphost.ToolResult, error) {
	action := strings.ToLower(strings.TrimSpace(params.Action))
	switch action {
	case "list":
		result, err := executeLS(ctx, lsInput{
			Path:      params.Path,
			Recursive: params.Recursive,
			MaxDepth:  params.MaxDepth,
		})
		return wrapFilesystemResult(action, result, err, logger)
	case "glob":
		if missing := missingFilesystemField(action, "pattern", params.Pattern); missing != nil {
			return missing, nil
		}
		result, err := executeGlob(ctx, globInput{
			Pattern: params.Pattern,
			Path:    params.Path,
		})
		return wrapFilesystemResult(action, result, err, logger)
	case "grep":
		if missing := missingFilesystemField(action, "pattern", params.Pattern); missing != nil {
			return missing, nil
		}
		result, err := executeGrep(ctx, grepInput{
			Pattern:    params.Pattern,
			Path:       params.Path,
			Glob:       params.Glob,
			TypeFilter: params.TypeFilter,
			Context:    params.Context,
			Before:     params.Before,
			After:      params.After,
			MaxResults: params.MaxResults,
			Multiline:  params.Multiline,
		})
		return wrapFilesystemResult(action, result, err, logger)
	case "read":
		if missing := missingFilesystemField(action, "path", params.Path); missing != nil {
			return missing, nil
		}
		result, err := executeReadFile(ctx, readFileInput{
			Path:            params.Path,
			Offset:          params.Offset,
			Limit:           params.Limit,
			ShowLineNumbers: params.ShowLineNumbers,
		}, tracker)
		return wrapFilesystemResult(action, result, err, logger)
	case "write":
		if missing := missingFilesystemField(action, "path", params.Path); missing != nil {
			return missing, nil
		}
		if missing := missingFilesystemStringPtr(action, "content", params.Content); missing != nil {
			return missing, nil
		}
		result, err := executeWriteFile(ctx, writeFileInput{
			Path:    params.Path,
			Content: *params.Content,
		}, tracker)
		return wrapFilesystemResult(action, result, err, logger)
	case "edit":
		if missing := missingFilesystemField(action, "path", params.Path); missing != nil {
			return missing, nil
		}
		if missing := missingFilesystemField(action, "old_string", params.OldString); missing != nil {
			return missing, nil
		}
		if missing := missingFilesystemStringPtr(action, "new_string", params.NewString); missing != nil {
			return missing, nil
		}
		result, err := executeEdit(ctx, editInput{
			Path:       params.Path,
			OldString:  params.OldString,
			NewString:  *params.NewString,
			ReplaceAll: params.ReplaceAll,
		}, tracker, logger)
		return wrapFilesystemResult(action, result, err, logger)
	case "multiedit":
		if len(params.Edits) == 0 {
			return filesystemErrorResult(action, filesystemErrMissingField, "缺少 edits"), nil
		}
		input := multieditInput{Edits: make([]struct {
			Path       string `json:"path"`
			OldString  string `json:"old_string"`
			NewString  string `json:"new_string"`
			ReplaceAll bool   `json:"replace_all,omitempty"`
		}, 0, len(params.Edits))}
		for i, edit := range params.Edits {
			if missing := missingFilesystemField(action, fmt.Sprintf("edits[%d].path", i), edit.Path); missing != nil {
				return missing, nil
			}
			if missing := missingFilesystemField(action, fmt.Sprintf("edits[%d].old_string", i), edit.OldString); missing != nil {
				return missing, nil
			}
			if missing := missingFilesystemStringPtr(action, fmt.Sprintf("edits[%d].new_string", i), edit.NewString); missing != nil {
				return missing, nil
			}
			input.Edits = append(input.Edits, struct {
				Path       string `json:"path"`
				OldString  string `json:"old_string"`
				NewString  string `json:"new_string"`
				ReplaceAll bool   `json:"replace_all,omitempty"`
			}{
				Path:       edit.Path,
				OldString:  edit.OldString,
				NewString:  *edit.NewString,
				ReplaceAll: edit.ReplaceAll,
			})
		}
		result, err := executeMultiEditInput(ctx, input, tracker, logger)
		return wrapFilesystemResult(action, result, err, logger)
	default:
		displayAction := action
		if displayAction == "" {
			displayAction = "unknown"
		}
		return filesystemErrorResult(displayAction, filesystemErrUnknownAction, "未知 action"), nil
	}
}

func missingFilesystemField(action, name, value string) *mcphost.ToolResult {
	if strings.TrimSpace(value) == "" {
		return filesystemErrorResult(action, filesystemErrMissingField, "缺少 "+name)
	}
	return nil
}

func missingFilesystemStringPtr(action, name string, value *string) *mcphost.ToolResult {
	if value == nil {
		return filesystemErrorResult(action, filesystemErrMissingField, "缺少 "+name)
	}
	return nil
}

func wrapFilesystemResult(action string, result *mcphost.ToolResult, err error, logger *zap.Logger) (*mcphost.ToolResult, error) {
	if err != nil {
		if logger != nil {
			logger.Debug("filesystem action executor failed", zap.String("action", action), zap.Error(err))
		}
		return filesystemErrorResult(action, filesystemErrExecution, "执行失败"), nil
	}
	if result != nil && result.IsError {
		if logger != nil {
			logger.Debug("filesystem action returned tool error", zap.String("action", action))
		}
		return filesystemErrorResult(action, filesystemErrExecution, sanitizeFilesystemToolError(result.DecodeContent())), nil
	}
	return result, nil
}

func filesystemErrorResult(action string, code filesystemErrorCode, message string) *mcphost.ToolResult {
	if action == "" {
		action = "unknown"
	}
	return errorResult(fmt.Sprintf("filesystem.%s: %s: %s", action, code, message))
}

func sanitizeFilesystemToolError(message string) string {
	message = strings.TrimSpace(message)
	switch {
	case message == "":
		return "执行失败"
	case strings.Contains(message, "has not been read"), strings.Contains(message, "未读取"), strings.Contains(message, "读取"):
		return "需要先读取目标文件"
	case strings.Contains(message, "外部修改"):
		return "文件已被外部修改"
	case strings.Contains(message, "路径不能为空"):
		return "路径不能为空"
	case strings.Contains(message, "路径安全校验失败"), strings.Contains(message, "超出允许的工作目录"):
		return "路径不在允许的工作目录内"
	case strings.Contains(message, "old_string"):
		return "编辑匹配失败"
	default:
		return "执行失败"
	}
}
