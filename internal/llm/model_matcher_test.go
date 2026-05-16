package llm

import (
	"strings"
	"testing"
)

func TestModelMatcher_Match(t *testing.T) {
	matcher := NewModelMatcher()

	tests := []struct {
		name            string
		input           string
		wantMatched     string
		wantSuggestions int
	}{
		{
			name:        "精确匹配 - gpt4",
			input:       "gpt4",
			wantMatched: "gpt-4",
		},
		{
			name:        "精确匹配 - sonnet",
			input:       "sonnet",
			wantMatched: "claude-sonnet-4-20250514",
		},
		{
			name:        "精确匹配 - deepseek",
			input:       "deepseek",
			wantMatched: "deepseek-chat",
		},
		{
			name:        "精确匹配 - gemini",
			input:       "gemini",
			wantMatched: "gemini-1.5-pro-latest",
		},
		{
			name:            "前缀匹配 - gpt",
			input:           "gpt",
			wantSuggestions: 5, // gpt4, gpt-5, gpt4o, gpt4-turbo, gpt-4-turbo, gpt-3.5, gpt3.5, gpt-5, gpt5
		},
		{
			name:            "前缀匹配 - claude",
			input:           "claude",
			wantSuggestions: 3, // claude-sonnet, claude-opus, claude-haiku
		},
		{
			name:            "包含匹配 - turbo",
			input:           "turbo",
			wantSuggestions: 2, // gpt4-turbo, gpt-4-turbo, gpt-3.5-turbo
		},
		{
			name:        "大小写不敏感",
			input:       "GPT4",
			wantMatched: "gpt-4",
		},
		{
			name:        "带空格",
			input:       "  gpt4  ",
			wantMatched: "gpt-4",
		},
		{
			name:            "未找到匹配",
			input:           "unknown-model",
			wantSuggestions: 0,
		},
		{
			name:            "输入太短（< 3 字符）",
			input:           "gp",
			wantSuggestions: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			matched, suggestions := matcher.Match(tt.input)

			if tt.wantMatched != "" {
				if matched != tt.wantMatched {
					t.Errorf("Match() matched = %q, want %q", matched, tt.wantMatched)
				}
				if len(suggestions) != 0 {
					t.Errorf("Match() suggestions = %v, want empty", suggestions)
				}
			} else {
				if matched != "" {
					t.Errorf("Match() matched = %q, want empty", matched)
				}
				if tt.wantSuggestions > 0 && len(suggestions) == 0 {
					t.Errorf("Match() suggestions is empty, want at least %d", tt.wantSuggestions)
				}
				if tt.wantSuggestions == 0 && len(suggestions) != 0 {
					t.Errorf("Match() suggestions = %v, want empty", suggestions)
				}
			}
		})
	}
}

func TestModelMatcher_SuggestModel(t *testing.T) {
	matcher := NewModelMatcher()

	tests := []struct {
		name        string
		input       string
		wantContain string
	}{
		{
			name:        "找到精确匹配",
			input:       "gpt4",
			wantContain: "gpt-4",
		},
		{
			name:        "找到建议",
			input:       "gpt",
			wantContain: "可能的选项",
		},
		{
			name:        "未找到任何匹配",
			input:       "unknown-model",
			wantContain: "未找到模型",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := matcher.SuggestModel(tt.input)
			if !strings.Contains(result, tt.wantContain) {
				t.Errorf("SuggestModel() = %q, want contains %q", result, tt.wantContain)
			}
		})
	}
}

func TestModelMatcher_AllAliases(t *testing.T) {
	matcher := NewModelMatcher()

	// 测试所有预定义别名都能精确匹配
	testAliases := []string{
		"gpt4",
		"gpt-5",
		"sonnet",
		"opus",
		"haiku",
		"deepseek",
		"gemini",
		"llama",
		"mistral",
	}

	for _, alias := range testAliases {
		t.Run("alias_"+alias, func(t *testing.T) {
			matched, suggestions := matcher.Match(alias)
			if matched == "" {
				t.Errorf("Match(%q) matched is empty, want non-empty", alias)
			}
			if len(suggestions) != 0 {
				t.Errorf("Match(%q) suggestions = %v, want empty", alias, suggestions)
			}
		})
	}
}

func TestModelMatcher_PrefixMatch(t *testing.T) {
	matcher := NewModelMatcher()

	// 测试前缀匹配
	matched, suggestions := matcher.Match("gpt4-t")
	if matched != "" {
		t.Errorf("Match('gpt4-t') should not return exact match")
	}
	if len(suggestions) == 0 {
		t.Errorf("Match('gpt4-t') should return suggestions")
	}

	// 验证建议中包含 gpt4-turbo
	found := false
	for _, s := range suggestions {
		if strings.Contains(s, "gpt4-turbo") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("Match('gpt4-t') suggestions should contain gpt4-turbo, got: %v", suggestions)
	}
}

func TestModelMatcher_ContainsMatch(t *testing.T) {
	matcher := NewModelMatcher()

	// 测试包含匹配（当前缀匹配失败时）
	matched, suggestions := matcher.Match("flash")
	if matched != "" {
		t.Errorf("Match('flash') should not return exact match")
	}
	if len(suggestions) == 0 {
		t.Errorf("Match('flash') should return suggestions")
	}

	// 验证建议中包含 gemini-flash
	found := false
	for _, s := range suggestions {
		if strings.Contains(s, "gemini-flash") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("Match('flash') suggestions should contain gemini-flash, got: %v", suggestions)
	}
}

func TestModelMatcher_LimitSuggestions(t *testing.T) {
	matcher := NewModelMatcher()

	// SuggestModel 应该最多显示 5 个建议
	result := matcher.SuggestModel("gpt")
	lines := strings.Split(result, "\n")

	// 第一行是标题，后面最多 5 个建议
	if len(lines) > 6 {
		t.Errorf("SuggestModel() should show max 5 suggestions, got %d lines", len(lines)-1)
	}
}
