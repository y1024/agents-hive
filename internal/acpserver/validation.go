package acpserver

import (
	"encoding/json"
	"fmt"
)

type acpParamValidator func(json.RawMessage) error

var acpValidatedMethodOrder = []string{
	"initialize",
	"session/new",
	"session/prompt",
	"session/cancel",
	"session/request_permission",
}

var acpMethodValidators = map[string]acpParamValidator{
	"initialize":                 validateInitialize,
	"session/new":                validateObjectParams,
	"session/prompt":             validatePrompt,
	"session/cancel":             validateSessionID,
	"session/request_permission": validateRequestPermission,
}

type acpJSONRPCMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

func SupportedACPMethods() []string {
	out := make([]string, 0, len(acpValidatedMethodOrder))
	out = append(out, acpValidatedMethodOrder...)
	return out
}

func ValidateACPMessage(raw []byte) error {
	var msg acpJSONRPCMessage
	if err := json.Unmarshal(raw, &msg); err != nil {
		return fmt.Errorf("invalid acp json: %w", err)
	}
	if msg.JSONRPC != "2.0" {
		return fmt.Errorf("invalid acp jsonrpc version")
	}
	if msg.Method == "" {
		return fmt.Errorf("missing acp method")
	}

	validator, ok := acpMethodValidators[msg.Method]
	if !ok {
		return fmt.Errorf("unsupported acp method %q", msg.Method)
	}
	return validator(msg.Params)
}

func validateInitialize(raw json.RawMessage) error {
	if len(raw) == 0 {
		return nil
	}
	return validateObjectParams(raw)
}

func validateObjectParams(raw json.RawMessage) error {
	if len(raw) == 0 {
		return nil
	}
	var params map[string]json.RawMessage
	if err := json.Unmarshal(raw, &params); err != nil {
		return fmt.Errorf("params must be an object: %w", err)
	}
	return nil
}

func validateSessionID(raw json.RawMessage) error {
	var params struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.Unmarshal(raw, &params); err != nil {
		return fmt.Errorf("invalid session params: %w", err)
	}
	if params.SessionID == "" {
		return fmt.Errorf("missing sessionId")
	}
	return nil
}

func validatePrompt(raw json.RawMessage) error {
	var params struct {
		SessionID string            `json:"sessionId"`
		Prompt    []json.RawMessage `json:"prompt"`
	}
	if err := json.Unmarshal(raw, &params); err != nil {
		return fmt.Errorf("invalid prompt params: %w", err)
	}
	if params.SessionID == "" {
		return fmt.Errorf("missing sessionId")
	}
	if len(params.Prompt) == 0 {
		return fmt.Errorf("missing prompt")
	}
	return nil
}

func validateRequestPermission(raw json.RawMessage) error {
	var params struct {
		SessionID string `json:"sessionId"`
		ToolCall  struct {
			ToolCallID string `json:"toolCallId"`
			Title      string `json:"title"`
			Kind       string `json:"kind"`
			Status     string `json:"status"`
		} `json:"toolCall"`
		Options []struct {
			OptionID string `json:"optionId"`
			Name     string `json:"name"`
			Kind     string `json:"kind"`
		} `json:"options"`
	}
	if err := json.Unmarshal(raw, &params); err != nil {
		return fmt.Errorf("invalid request_permission params: %w", err)
	}
	if params.SessionID == "" {
		return fmt.Errorf("missing sessionId")
	}
	if params.ToolCall.ToolCallID == "" {
		return fmt.Errorf("missing toolCall.toolCallId")
	}
	if len(params.Options) == 0 {
		return fmt.Errorf("missing permission options")
	}
	for i, opt := range params.Options {
		if opt.OptionID == "" || opt.Name == "" || opt.Kind == "" {
			return fmt.Errorf("invalid permission option %d", i)
		}
	}
	return nil
}
