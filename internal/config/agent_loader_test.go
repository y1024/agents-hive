package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestSplitFrontmatter(t *testing.T) {
	tests := []struct {
		name        string
		content     string
		wantFM      string
		wantBody    string
		wantErr     bool
	}{
		{
			name: "正常解析",
			content: `---
name: test-agent
mode: subagent
---
你是一个测试 Agent。`,
			wantFM:   "name: test-agent\nmode: subagent",
			wantBody: "你是一个测试 Agent。",
		},
		{
			name: "多行 body",
			content: `---
name: foo
---
第一行
第二行
第三行`,
			wantFM:   "name: foo",
			wantBody: "第一行\n第二行\n第三行",
		},
		{
			name: "空 body",
			content: `---
name: bar
---`,
			wantFM:   "name: bar",
			wantBody: "",
		},
		{
			name:    "缺少开始标记",
			content: "name: test\n---\nbody",
			wantErr: true,
		},
		{
			name:    "缺少结束标记",
			content: "---\nname: test\nbody",
			wantErr: true,
		},
		{
			name:    "空文件",
			content: "",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fm, body, err := splitFrontmatter(tt.content)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantFM, fm)
			assert.Equal(t, tt.wantBody, body)
		})
	}
}

func TestAgentDefinitionValidate(t *testing.T) {
	tests := []struct {
		name    string
		def     AgentDefinition
		wantErr bool
	}{
		{
			name:    "合法定义",
			def:     AgentDefinition{Name: "test", Mode: "subagent"},
			wantErr: false,
		},
		{
			name:    "mode 为 primary",
			def:     AgentDefinition{Name: "test", Mode: "primary"},
			wantErr: false,
		},
		{
			name:    "mode 为 all",
			def:     AgentDefinition{Name: "test", Mode: "all"},
			wantErr: false,
		},
		{
			name:    "空 mode 视为合法（默认值）",
			def:     AgentDefinition{Name: "test"},
			wantErr: false,
		},
		{
			name:    "缺少 name",
			def:     AgentDefinition{Mode: "subagent"},
			wantErr: true,
		},
		{
			name:    "无效 mode",
			def:     AgentDefinition{Name: "test", Mode: "invalid"},
			wantErr: true,
		},
		{
			name:    "temperature 超出范围",
			def:     AgentDefinition{Name: "test", Temperature: 3.0},
			wantErr: true,
		},
		{
			name:    "负数 max_steps",
			def:     AgentDefinition{Name: "test", MaxSteps: -1},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.def.Validate()
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestLoadAgentDefinitions(t *testing.T) {
	logger := zap.NewNop()

	t.Run("目录不存在返回空列表", func(t *testing.T) {
		defs, err := LoadAgentDefinitions("/nonexistent/path", logger)
		require.NoError(t, err)
		assert.Empty(t, defs)
	})

	t.Run("正常加载多个文件", func(t *testing.T) {
		dir := t.TempDir()

		// 创建第一个 agent 文件
		writeFile(t, filepath.Join(dir, "research.md"), `---
name: research
description: 研究 Agent
mode: subagent
model: gpt-5
temperature: 0.3
max_steps: 10
tools:
  - read_file
  - grep
---
你是一个专注于代码研究的 Agent。
请仔细分析代码结构和依赖关系。`)

		// 创建第二个 agent 文件
		writeFile(t, filepath.Join(dir, "writer.md"), `---
name: writer
description: 写作 Agent
mode: primary
---
你是一个技术文档写作 Agent。`)

		// 创建一个非 .md 文件（应被忽略）
		writeFile(t, filepath.Join(dir, "notes.txt"), "这不是 agent 定义")

		defs, err := LoadAgentDefinitions(dir, logger)
		require.NoError(t, err)
		assert.Len(t, defs, 2)

		// 查找 research agent
		var research *AgentDefinition
		for i := range defs {
			if defs[i].Name == "research" {
				research = &defs[i]
				break
			}
		}
		require.NotNil(t, research)
		assert.Equal(t, "研究 Agent", research.Description)
		assert.Equal(t, "subagent", research.Mode)
		assert.Equal(t, "gpt-5", research.Model)
		assert.InDelta(t, 0.3, research.Temperature, 0.001)
		assert.Equal(t, 10, research.MaxSteps)
		assert.Equal(t, []string{"read_file", "grep"}, research.Tools)
		assert.Contains(t, research.Prompt, "代码研究")
	})

	t.Run("跳过无效文件", func(t *testing.T) {
		dir := t.TempDir()

		// 无效文件（缺少 frontmatter）
		writeFile(t, filepath.Join(dir, "invalid.md"), "没有 frontmatter")

		// 有效文件
		writeFile(t, filepath.Join(dir, "valid.md"), `---
name: valid-agent
description: 有效 Agent
---
有效的 Agent 定义`)

		defs, err := LoadAgentDefinitions(dir, logger)
		require.NoError(t, err)
		assert.Len(t, defs, 1)
		assert.Equal(t, "valid-agent", defs[0].Name)
	})

	t.Run("默认 mode 为 subagent", func(t *testing.T) {
		dir := t.TempDir()

		writeFile(t, filepath.Join(dir, "agent.md"), `---
name: no-mode
description: 未指定 mode
---
测试`)

		defs, err := LoadAgentDefinitions(dir, logger)
		require.NoError(t, err)
		require.Len(t, defs, 1)
		assert.Equal(t, "subagent", defs[0].Mode)
	})
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	err := os.WriteFile(path, []byte(content), 0644)
	require.NoError(t, err)
}
