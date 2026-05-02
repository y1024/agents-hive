package acpserver

import "testing"

func TestValidateACPMessageFixtures(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		wantErr bool
	}{
		{
			name: "initialize",
			raw:  `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":1}}`,
		},
		{
			name: "new session",
			raw:  `{"jsonrpc":"2.0","id":2,"method":"session/new","params":{"cwd":"/tmp"}}`,
		},
		{
			name: "prompt",
			raw:  `{"jsonrpc":"2.0","id":3,"method":"session/prompt","params":{"sessionId":"s1","prompt":[{"type":"text","text":"hello"}]}}`,
		},
		{
			name: "cancel",
			raw:  `{"jsonrpc":"2.0","method":"session/cancel","params":{"sessionId":"s1"}}`,
		},
		{
			name: "request permission",
			raw:  `{"jsonrpc":"2.0","id":4,"method":"session/request_permission","params":{"sessionId":"s1","toolCall":{"toolCallId":"tc1","title":"write","kind":"edit","status":"pending"},"options":[{"optionId":"allow_once","name":"Allow","kind":"allow_once"}]}}`,
		},
		{
			name:    "prompt missing session",
			raw:     `{"jsonrpc":"2.0","id":5,"method":"session/prompt","params":{"prompt":[{"type":"text","text":"hello"}]}}`,
			wantErr: true,
		},
		{
			name:    "unknown method",
			raw:     `{"jsonrpc":"2.0","id":6,"method":"unknown","params":{}}`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateACPMessage([]byte(tt.raw))
			if tt.wantErr && err == nil {
				t.Fatal("ValidateACPMessage succeeded, want error")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("ValidateACPMessage failed: %v", err)
			}
		})
	}
}
