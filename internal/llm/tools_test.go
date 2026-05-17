package llm

import (
	"strings"
	"testing"

	"github.com/chef-guo/agents-hive/internal/mcphost"
)

func TestConvertToolsForChatCompletionsSortsByName(t *testing.T) {
	tools, err := convertToolsForChatCompletions([]mcphost.ToolDefinition{
		{Name: "zeta", Description: "z", InputSchema: []byte(`{"type":"object"}`)},
		{Name: "alpha", Description: "a", InputSchema: []byte(`{"type":"object"}`)},
		{Name: "middle", Description: "m", InputSchema: []byte(`{"type":"object"}`)},
	})
	if err != nil {
		t.Fatalf("convertToolsForChatCompletions returned error: %v", err)
	}
	got := []string{tools[0].Function.Name, tools[1].Function.Name, tools[2].Function.Name}
	want := []string{"alpha", "middle", "zeta"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("tool order = %v, want %v", got, want)
		}
	}
}

func TestConvertToolsForResponsesSortsByName(t *testing.T) {
	tools, err := convertToolsForResponses([]mcphost.ToolDefinition{
		{Name: "zeta", Description: "z", InputSchema: []byte(`{"type":"object"}`)},
		{Name: "alpha", Description: "a", InputSchema: []byte(`{"type":"object"}`)},
		{Name: "middle", Description: "m", InputSchema: []byte(`{"type":"object"}`)},
	})
	if err != nil {
		t.Fatalf("convertToolsForResponses returned error: %v", err)
	}
	got := []string{
		tools[0].OfFunction.Name,
		tools[1].OfFunction.Name,
		tools[2].OfFunction.Name,
	}
	want := []string{"alpha", "middle", "zeta"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("tool order = %v, want %v", got, want)
		}
	}
}

func TestConvertToolsForResponsesRejectsInvalidSchema(t *testing.T) {
	_, err := convertToolsForResponses([]mcphost.ToolDefinition{
		{Name: "bad_schema", InputSchema: []byte(`{"type":`)},
	})
	if err == nil {
		t.Fatal("convertToolsForResponses should reject invalid input schema")
	}
	if !strings.Contains(err.Error(), "bad_schema") {
		t.Fatalf("error should include tool name, got %v", err)
	}
}

func TestConvertToolsAliasesDottedNamesForProviderCompatibility(t *testing.T) {
	defs := []mcphost.ToolDefinition{
		{Name: "kb.section.text", Description: "kb", InputSchema: []byte(`{"type":"object"}`)},
		{Name: "read_file", Description: "read", InputSchema: []byte(`{"type":"object"}`)},
	}

	chatTools, err := convertToolsForChatCompletions(defs)
	if err != nil {
		t.Fatalf("convertToolsForChatCompletions returned error: %v", err)
	}
	gotChatNames := []string{chatTools[0].Function.Name, chatTools[1].Function.Name}
	wantChatNames := []string{"kb_section_text", "read_file"}
	for i := range wantChatNames {
		if gotChatNames[i] != wantChatNames[i] {
			t.Fatalf("chat tool names = %v, want %v", gotChatNames, wantChatNames)
		}
		if !isValidLLMToolName(gotChatNames[i]) {
			t.Fatalf("chat tool name %q is not provider-compatible", gotChatNames[i])
		}
	}

	responseTools, err := convertToolsForResponses(defs)
	if err != nil {
		t.Fatalf("convertToolsForResponses returned error: %v", err)
	}
	gotResponseNames := []string{responseTools[0].OfFunction.Name, responseTools[1].OfFunction.Name}
	wantResponseNames := []string{"kb_section_text", "read_file"}
	for i := range wantResponseNames {
		if gotResponseNames[i] != wantResponseNames[i] {
			t.Fatalf("response tool names = %v, want %v", gotResponseNames, wantResponseNames)
		}
		if !isValidLLMToolName(gotResponseNames[i]) {
			t.Fatalf("response tool name %q is not provider-compatible", gotResponseNames[i])
		}
	}

	aliases := toolNameAliasesForTools(defs)
	if got := aliases.APIName("kb.section.text"); got != "kb_section_text" {
		t.Fatalf("APIName(kb.section.text) = %q, want kb_section_text", got)
	}
	if got := aliases.InternalName("kb_section_text"); got != "kb.section.text" {
		t.Fatalf("InternalName(kb_section_text) = %q, want kb.section.text", got)
	}
}

func TestToolNameAliasesDisambiguateCollisions(t *testing.T) {
	aliases := toolNameAliasesForTools([]mcphost.ToolDefinition{
		{Name: "a.b", InputSchema: []byte(`{"type":"object"}`)},
		{Name: "a/b", InputSchema: []byte(`{"type":"object"}`)},
	})

	first := aliases.APIName("a.b")
	second := aliases.APIName("a/b")
	if first == second {
		t.Fatalf("aliases collided: %q", first)
	}
	for _, name := range []string{first, second} {
		if !isValidLLMToolName(name) {
			t.Fatalf("alias %q is not provider-compatible", name)
		}
	}
	if got := aliases.InternalName(first); got != "a.b" {
		t.Fatalf("InternalName(%q) = %q, want a.b", first, got)
	}
	if got := aliases.InternalName(second); got != "a/b" {
		t.Fatalf("InternalName(%q) = %q, want a/b", second, got)
	}
}
