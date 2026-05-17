package llm

import (
	"testing"

	"github.com/chef-guo/agents-hive/internal/mcphost"
)

func TestBuildChatCompletionsToolChoice(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantSet   bool
		wantMode  string // OfAuto.Value 期望值（仅 wantNamed=false 时有效）
		wantNamed string // OfChatCompletionNamedToolChoice.Function.Name 期望值
	}{
		{"空字符串跳过", "", false, "", ""},
		{"auto 走模式分支", "auto", true, "auto", ""},
		{"required 走模式分支", "required", true, "required", ""},
		{"none 走模式分支", "none", true, "none", ""},
		{"具体工具名走 named 分支", "websearch", true, "", "websearch"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := buildChatCompletionsToolChoice(tc.input)
			if ok != tc.wantSet {
				t.Fatalf("want set=%v, got %v", tc.wantSet, ok)
			}
			if !ok {
				return
			}
			if tc.wantNamed != "" {
				if got.OfChatCompletionNamedToolChoice == nil {
					t.Fatalf("want named tool choice, got nil")
				}
				if got.OfChatCompletionNamedToolChoice.Function.Name != tc.wantNamed {
					t.Fatalf("want named=%q, got %q", tc.wantNamed, got.OfChatCompletionNamedToolChoice.Function.Name)
				}
			} else {
				if got.OfAuto.Value != tc.wantMode {
					t.Fatalf("want mode=%q, got %q", tc.wantMode, got.OfAuto.Value)
				}
			}
		})
	}
}

func TestBuildChatCompletionsToolChoiceUsesProviderAlias(t *testing.T) {
	aliases := toolNameAliasesForTools([]mcphost.ToolDefinition{
		{Name: "kb.section.text", InputSchema: []byte(`{"type":"object"}`)},
	})
	got, ok := buildChatCompletionsToolChoiceWithAliases("kb.section.text", aliases)
	if !ok {
		t.Fatal("expected named tool_choice")
	}
	if got.OfChatCompletionNamedToolChoice == nil {
		t.Fatal("expected named tool_choice branch")
	}
	if got.OfChatCompletionNamedToolChoice.Function.Name != "kb_section_text" {
		t.Fatalf("named tool_choice = %q, want kb_section_text", got.OfChatCompletionNamedToolChoice.Function.Name)
	}
}

func TestBuildResponsesToolChoice(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantSet   bool
		wantMode  string
		wantNamed string
	}{
		{"空字符串跳过", "", false, "", ""},
		{"auto 走模式", "auto", true, "auto", ""},
		{"required 走模式", "required", true, "required", ""},
		{"none 走模式", "none", true, "none", ""},
		{"具体工具名走 function 分支", "skill_search", true, "", "skill_search"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := buildResponsesToolChoice(tc.input)
			if ok != tc.wantSet {
				t.Fatalf("want set=%v, got %v", tc.wantSet, ok)
			}
			if !ok {
				return
			}
			if tc.wantNamed != "" {
				if got.OfFunctionTool == nil {
					t.Fatalf("want function tool choice, got nil")
				}
				if got.OfFunctionTool.Name != tc.wantNamed {
					t.Fatalf("want named=%q, got %q", tc.wantNamed, got.OfFunctionTool.Name)
				}
			} else {
				if string(got.OfToolChoiceMode.Value) != tc.wantMode {
					t.Fatalf("want mode=%q, got %q", tc.wantMode, got.OfToolChoiceMode.Value)
				}
			}
		})
	}
}

func TestBuildResponsesToolChoiceUsesProviderAlias(t *testing.T) {
	aliases := toolNameAliasesForTools([]mcphost.ToolDefinition{
		{Name: "kb.section.text", InputSchema: []byte(`{"type":"object"}`)},
	})
	got, ok := buildResponsesToolChoiceWithAliases("kb.section.text", aliases)
	if !ok {
		t.Fatal("expected named tool_choice")
	}
	if got.OfFunctionTool == nil {
		t.Fatal("expected function tool_choice branch")
	}
	if got.OfFunctionTool.Name != "kb_section_text" {
		t.Fatalf("named tool_choice = %q, want kb_section_text", got.OfFunctionTool.Name)
	}
}
