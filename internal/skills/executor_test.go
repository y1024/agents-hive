package skills

import (
	"fmt"
	"testing"
)

type testExecutor struct {
	outputs map[string]string
}

func (e *testExecutor) Execute(command string) (string, string, error) {
	out, ok := e.outputs[command]
	if !ok {
		return "", "", fmt.Errorf("unknown command: %s", command)
	}
	return out, "", nil
}

func TestExecuteDynamicContext(t *testing.T) {
	tests := []struct {
		name     string
		content  string
		outputs  map[string]string
		expected string
		wantErr  bool
	}{
		{
			name:     "single command",
			content:  "Version: !`echo hello`",
			outputs:  map[string]string{"echo hello": "hello"},
			expected: "Version: hello",
		},
		{
			name:     "command at line start",
			content:  "!`echo hello`",
			outputs:  map[string]string{"echo hello": "hello"},
			expected: "hello",
		},
		{
			name:     "multiple commands",
			content:  "A: !`cmd1`, B: !`cmd2`",
			outputs:  map[string]string{"cmd1": "val1", "cmd2": "val2"},
			expected: "A: val1, B: val2",
		},
		{
			name:     "multiline markdown after exclamation is not command",
			content:  "Template: `Done!`\n- keep markdown\n`later code`",
			outputs:  map[string]string{},
			expected: "Template: `Done!`\n- keep markdown\n`later code`",
		},
		{
			name:     "dynamic command cannot span lines",
			content:  "Broken: !`echo hello\nmore markdown`",
			outputs:  map[string]string{},
			expected: "Broken: !`echo hello\nmore markdown`",
		},
		{
			name:     "no commands",
			content:  "Plain text without dynamic context.",
			outputs:  map[string]string{},
			expected: "Plain text without dynamic context.",
		},
		{
			name:     "exclamation before inline code is plain markdown",
			content:  "Template: `Lead the way!`\n\n6. Only output greeting:\n- no markdown\n- no extra text\n\nExample: `skill greet`",
			outputs:  map[string]string{},
			expected: "Template: `Lead the way!`\n\n6. Only output greeting:\n- no markdown\n- no extra text\n\nExample: `skill greet`",
		},
		{
			name:     "command fails",
			content:  "Result: !`failing-cmd`",
			outputs:  map[string]string{},
			expected: "Result: !`failing-cmd`",
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exec := &testExecutor{outputs: tt.outputs}
			result, err := ExecuteDynamicContext(tt.content, exec)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
			} else {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
			}
			if result != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, result)
			}
		})
	}
}
