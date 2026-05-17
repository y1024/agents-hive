package master

import (
	"encoding/json"
	"time"
)

// InputRequestType 描述所需的人工输入类型
type InputRequestType string

const (
	InputApproval      InputRequestType = "approval"      // approve/reject/modify
	InputClarification InputRequestType = "clarification" // free-text answer
	InputConfirmation  InputRequestType = "confirmation"  // proceed/skip/cancel
	InputChoice        InputRequestType = "choice"        // select from options
	InputPermission    InputRequestType = "permission"    // tool permission request
)

// InputRequest 是系统向用户请求输入
type InputRequest struct {
	ID          string           `json:"id"`
	TaskID      string           `json:"task_id"`
	StepID      string           `json:"step_id,omitempty"`
	Type        InputRequestType `json:"type"`
	Prompt      string           `json:"prompt"`
	Options     []string         `json:"options,omitempty"`
	Default     string           `json:"default,omitempty"`
	Timeout     time.Duration    `json:"timeout,omitempty"`
	ToolName    string           `json:"tool_name,omitempty"`   // 工具名称（权限请求时使用）
	ChoiceType  string           `json:"choice_type,omitempty"` // 业务决策子语义；由 choice_type_registry 管控白名单
	Data        json.RawMessage  `json:"data,omitempty"`        // for permission: tool input
	SessionID   string           `json:"session_id,omitempty"`  // 关联会话 ID，用于前端过滤
	Fingerprint string           `json:"fingerprint,omitempty"` // 去重指纹（tool+args hash），相同指纹的请求只广播一次
	CreatedAt   time.Time        `json:"created_at"`
}

// InputResponse 是用户对 InputRequest 的回复
type InputResponse struct {
	RequestID string `json:"request_id"`
	TaskID    string `json:"task_id"`
	Value     string `json:"value"`
	Action    string `json:"action"`             // "approve","reject","modify","proceed","skip","cancel"
	Remember  bool   `json:"remember,omitempty"` // for permission requests
}

// UserCommandType 表示用户控制命令
type UserCommandType string

const (
	CmdPause  UserCommandType = "pause"
	CmdResume UserCommandType = "resume"
	CmdCancel UserCommandType = "cancel"
)

// UserCommand 是用户在执行期间发送的控制命令
type UserCommand struct {
	Type    UserCommandType `json:"type"`
	TaskID  string          `json:"task_id"`
	Payload json.RawMessage `json:"payload,omitempty"`
}
