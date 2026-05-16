package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/chef-guo/agents-hive/internal/mcphost"
)

// lsInput ls 工具的输入参数
type lsInput struct {
	Path      string `json:"path"`
	Recursive bool   `json:"recursive,omitempty"` // 是否递归列出子目录
	MaxDepth  int    `json:"max_depth,omitempty"` // 最大递归深度（默认1，最大3）
}

// lsEntry 表示一个文件或目录条目
type lsEntry struct {
	Path    string      // 相对路径或绝对路径
	Mode    fs.FileMode // 文件权限
	Size    int64       // 文件大小（字节）
	ModTime time.Time   // 修改时间
	IsDir   bool        // 是否为目录
	IsLink  bool        // 是否为符号链接
}

const (
	defaultMaxDepth = 1 // 默认递归深度
	maxAllowedDepth = 3 // 允许的最大递归深度
)

// registerLS 注册 ls 工具
func registerLS(host *mcphost.Host, logger *zap.Logger) {
	schema, _ := json.Marshal(map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "要列出的目录路径（默认为当前目录）",
			},
			"recursive": map[string]any{
				"type":        "boolean",
				"description": "是否递归列出子目录（默认 false）",
			},
			"max_depth": map[string]any{
				"type":        "integer",
				"description": "最大递归深度（默认1，最大3）",
			},
		},
	})

	host.RegisterTool(
		mcphost.ToolDefinition{
			Name:              "ls",
			Description:       "列出目录内容，支持递归列出子目录（最多3层）",
			InputSchema:       schema,
			IsConcurrencySafe: true, // 只读无副作用，可并发执行
		},
		func(ctx context.Context, input json.RawMessage) (*mcphost.ToolResult, error) {
			var params lsInput
			if err := json.Unmarshal(input, &params); err != nil {
				return errorResult("输入无效: " + err.Error()), nil
			}

			return executeLS(ctx, params)
		},
	)
}

func executeLS(ctx context.Context, params lsInput) (*mcphost.ToolResult, error) {
	_ = ctx

	// 设置默认路径
	if params.Path == "" {
		params.Path = "."
	}

	resolvedPath, err := resolveToolPath(params.Path)
	if err != nil {
		return errorResult(err.Error()), nil
	}
	params.Path = resolvedPath

	// 限制递归深度
	maxDepth := params.MaxDepth
	if maxDepth <= 0 {
		maxDepth = defaultMaxDepth
	}
	if maxDepth > maxAllowedDepth {
		maxDepth = maxAllowedDepth
	}

	// 检查路径是否存在
	info, err := os.Stat(params.Path)
	if err != nil {
		return errorResult("路径不存在: " + err.Error()), nil
	}

	// 如果是文件，直接返回其信息
	if !info.IsDir() {
		return formatSingleFile(params.Path, info), nil
	}

	// 列出目录内容
	if params.Recursive {
		entries, err := listRecursive(params.Path, maxDepth)
		if err != nil {
			return errorResult("列出目录失败: " + err.Error()), nil
		}
		return formatDirectoryTree(params.Path, entries), nil
	}

	entries, err := listDirectory(params.Path)
	if err != nil {
		return errorResult("列出目录失败: " + err.Error()), nil
	}
	return formatDirectoryFlat(params.Path, entries), nil
}

// listDirectory 列出单个目录的内容（非递归）
func listDirectory(path string) ([]lsEntry, error) {
	files, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}

	entries := make([]lsEntry, 0, len(files))
	for _, file := range files {
		info, err := file.Info()
		if err != nil {
			// 跳过无法获取信息的文件（如权限问题）
			continue
		}

		entry := lsEntry{
			Path:    file.Name(),
			Mode:    info.Mode(),
			Size:    info.Size(),
			ModTime: info.ModTime(),
			IsDir:   file.IsDir(),
			IsLink:  info.Mode()&os.ModeSymlink != 0,
		}
		entries = append(entries, entry)
	}

	return entries, nil
}

// listRecursive 递归列出目录内容
func listRecursive(basePath string, maxDepth int) ([]lsEntry, error) {
	var entries []lsEntry

	err := filepath.WalkDir(basePath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			// 跳过权限错误等
			return nil
		}

		// 跳过根目录本身
		if path == basePath {
			return nil
		}

		// 计算当前深度
		relPath, _ := filepath.Rel(basePath, path)
		depth := strings.Count(relPath, string(filepath.Separator)) + 1

		// 超过最大深度则跳过
		if depth > maxDepth {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}

		info, err := d.Info()
		if err != nil {
			// 跳过无法获取信息的文件
			return nil
		}

		entry := lsEntry{
			Path:    relPath,
			Mode:    info.Mode(),
			Size:    info.Size(),
			ModTime: info.ModTime(),
			IsDir:   d.IsDir(),
			IsLink:  info.Mode()&os.ModeSymlink != 0,
		}
		entries = append(entries, entry)

		return nil
	})

	return entries, err
}

// formatSingleFile 格式化单个文件信息
func formatSingleFile(path string, info fs.FileInfo) *mcphost.ToolResult {
	entry := lsEntry{
		Path:    path,
		Mode:    info.Mode(),
		Size:    info.Size(),
		ModTime: info.ModTime(),
		IsDir:   info.IsDir(),
		IsLink:  info.Mode()&os.ModeSymlink != 0,
	}

	output := formatEntry(entry)
	return textResult(output)
}

// formatDirectoryFlat 格式化平铺目录列表（非递归）
func formatDirectoryFlat(path string, entries []lsEntry) *mcphost.ToolResult {
	var sb strings.Builder

	// 标题行
	sb.WriteString(fmt.Sprintf("%s (%d 项):\n\n", path, len(entries)))

	// 条目
	for _, entry := range entries {
		sb.WriteString(formatEntry(entry))
		sb.WriteString("\n")
	}

	return textResult(sb.String())
}

// formatDirectoryTree 格式化树形目录列表（递归）
func formatDirectoryTree(basePath string, entries []lsEntry) *mcphost.ToolResult {
	var sb strings.Builder

	// 标题行
	sb.WriteString(fmt.Sprintf("%s (%d 项):\n\n", basePath, len(entries)))

	// 条目
	for _, entry := range entries {
		// 计算缩进
		depth := strings.Count(entry.Path, string(filepath.Separator))
		indent := strings.Repeat("  ", depth)

		sb.WriteString(indent)
		sb.WriteString(formatEntry(entry))
		sb.WriteString("\n")
	}

	return textResult(sb.String())
}

// formatEntry 格式化单个文件或目录条目（类似 ls -lh）
func formatEntry(entry lsEntry) string {
	// 权限字符串
	mode := entry.Mode.String()

	// 文件大小（人类可读格式）
	size := formatSize(entry.Size)

	// 修改时间
	modTime := entry.ModTime.Format("2006-01-02 15:04")

	// 文件名（目录加 /，符号链接加 @）
	name := entry.Path
	if entry.IsDir {
		name += "/"
	} else if entry.IsLink {
		name += "@"
	}

	return fmt.Sprintf("%-12s %8s  %s  %s", mode, size, modTime, name)
}

// formatSize 将字节大小格式化为人类可读格式
func formatSize(size int64) string {
	const unit = 1024
	if size < unit {
		return fmt.Sprintf("%dB", size)
	}
	div, exp := int64(unit), 0
	for n := size / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f%cB", float64(size)/float64(div), "KMGTPE"[exp])
}
