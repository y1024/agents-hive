package router

import (
	"encoding/json"
	"sort"
	"strings"
)

// IntentCapabilityRule declares the host-side capability requirements for an intent.
type IntentCapabilityRule struct {
	Required []Capability
}

// SkillDomainRule declares what a trusted local skill domain can do.
type SkillDomainRule struct {
	Capabilities       []Capability
	AllowedIntentKinds []IntentKind
	CallableTool       string
}

// BuiltinToolRule declares host-owned metadata for built-in tools.
type BuiltinToolRule struct {
	Domain       string
	Invocation   InvocationMode
	Risk         RiskLevel
	ReadOnly     bool
	Destructive  bool
	Idempotent   bool
	OpenWorld    bool
	SideEffect   bool
	Capabilities []Capability
}

// ToolActionRiskRule 声明单个 action/operation 中需要审批的危险动作。
type ToolActionRiskRule struct {
	ToolName string
	Actions  []string
}

const (
	// IntentSignalExternalBusinessWrite 标记本轮是外部业务对象写入，
	// 具体写入类型由 action_capability:* signal 进一步收窄。
	IntentSignalExternalBusinessWrite = "external_business_write"

	IntentActionCapabilitySignalPrefix = "action_capability:"

	ActionCapabilityExternalTaskCreate     = "external.task.create"
	ActionCapabilityExternalTaskComplete   = "external.task.complete"
	ActionCapabilityExternalApprovalSubmit = "external.approval.submit"
	ActionCapabilityExternalTableWrite     = "external.table.write"
	ActionCapabilityExternalRecordCreate   = "external.record.create"
	ActionCapabilityExternalRecordUpdate   = "external.record.update"
)

// ActionCapabilityRule 是动作级能力的单一事实源。
// 自然语言意图恢复、RouteDecision 最小 action 授权、tool_search 提示和契约验收
// 都应从这里读取，不再在各层重复维护业务写入语义。
type ActionCapabilityRule struct {
	ToolName           string                          `json:"tool_name"`
	Action             string                          `json:"action"`
	CapabilityID       string                          `json:"capability_id"`
	Resource           string                          `json:"resource"`
	Operation          string                          `json:"operation"`
	RiskClass          ToolRiskClass                   `json:"risk_class"`
	RequiredFields     []string                        `json:"required_fields,omitempty"`
	ParameterHints     []ActionCapabilityParameterHint `json:"parameter_hints,omitempty"`
	IntentAliases      []string                        `json:"intent_aliases,omitempty"`
	Platforms          []string                        `json:"platforms,omitempty"`
	PreparatoryActions []string                        `json:"preparatory_actions,omitempty"`
	ExampleArgs        map[string]any                  `json:"example_args,omitempty"`
	RepairHint         string                          `json:"repair_hint,omitempty"`
}

type ActionCapabilityParameterHint struct {
	Name        string   `json:"name"`
	Type        string   `json:"type,omitempty"`
	Required    bool     `json:"required,omitempty"`
	Source      string   `json:"source,omitempty"`
	Format      string   `json:"format,omitempty"`
	Description string   `json:"description,omitempty"`
	Enum        []string `json:"enum,omitempty"`
}

type actionCapabilityScoredRule struct {
	rule  ActionCapabilityRule
	score int
}

type HostToolSet string

const (
	HostToolSetDefaultVisible HostToolSet = "default_visible"
	HostToolSetPlanControl    HostToolSet = "plan_control"
	HostToolSetPlanAllowed    HostToolSet = "plan_allowed"
)

var intentCapabilityRules = map[IntentKind]IntentCapabilityRule{
	IntentCreateSkill:   {Required: []Capability{CapabilityMetaSkillCreate}},
	IntentModifySkill:   {Required: []Capability{CapabilityMetaSkillModify}},
	IntentManageTool:    {Required: []Capability{CapabilityMetaToolRegister}},
	IntentExternalWrite: {Required: []Capability{CapabilityExternalSend}},
}

var skillDomainRules = map[string]SkillDomainRule{
	"skill_authoring": {
		Capabilities:       []Capability{CapabilityMetaSkillCreate, CapabilityMetaSkillModify},
		AllowedIntentKinds: []IntentKind{IntentCreateSkill, IntentModifySkill},
		CallableTool:       "skill",
	},
	"mcp_server_building": {
		Capabilities:       []Capability{CapabilityMetaToolRegister},
		AllowedIntentKinds: []IntentKind{IntentManageTool},
		CallableTool:       "skill",
	},
}

var knownSkillWorkflowDomains = map[string]string{
	"skill-creator": "skill_authoring",
	"mcp-builder":   "mcp_server_building",
}

var builtinToolRules = map[string]BuiltinToolRule{
	"apply_patch":                {Domain: "filesystem", Invocation: InvocationDirectTool, Risk: RiskLocalWrite, SideEffect: true},
	"bash":                       {Domain: "filesystem", Invocation: InvocationDirectTool, Risk: RiskRuntimeExec, SideEffect: true, Capabilities: []Capability{CapabilityRuntimeExec}},
	"batch":                      {Domain: "agent", Invocation: InvocationDirectTool, Risk: RiskLocalWrite, SideEffect: true},
	"browser_interact":           {Domain: "web", Invocation: InvocationDirectTool, Risk: RiskLocalWrite, SideEffect: true},
	"cancel_escalation":          {Domain: "customer_service", Invocation: InvocationDirectTool, Risk: RiskLocalWrite, SideEffect: true, Capabilities: []Capability{CapabilityCustomerServiceCancelEscalation}},
	"create_handoff_summary":     {Domain: "agent", Invocation: InvocationDirectTool, Risk: RiskReadOnly, ReadOnly: true},
	"create_tool":                {Domain: "tools", Invocation: InvocationDirectTool, Risk: RiskLocalWrite, SideEffect: true, Capabilities: []Capability{CapabilityMetaToolRegister}},
	"edit":                       {Domain: "filesystem", Invocation: InvocationDirectTool, Risk: RiskLocalWrite, SideEffect: true},
	"enter_plan_mode":            {Domain: "planning", Invocation: InvocationDirectTool, Risk: RiskReadOnly, ReadOnly: true},
	"escalate_to_human":          {Domain: "customer_service", Invocation: InvocationDirectTool, Risk: RiskExternalWrite, SideEffect: true, Capabilities: []Capability{CapabilityExternalSend, CapabilityCustomerServiceEscalate}},
	"exit_plan_mode":             {Domain: "planning", Invocation: InvocationDirectTool, Risk: RiskReadOnly, ReadOnly: true},
	"feishu_api":                 {Domain: "messaging", Invocation: InvocationDirectTool, Risk: RiskExternalWrite, SideEffect: true, Capabilities: []Capability{CapabilityExternalSend, CapabilityExternalSendFeishu}},
	"filesystem":                 {Domain: "filesystem", Invocation: InvocationDirectTool, Risk: RiskLocalWrite, SideEffect: true},
	"finish_plan":                {Domain: "planning", Invocation: InvocationDirectTool, Risk: RiskReadOnly, ReadOnly: true},
	"generate_image":             {Domain: "media", Invocation: InvocationDirectTool, Risk: RiskLocalWrite, SideEffect: true},
	"generate_video":             {Domain: "media", Invocation: InvocationDirectTool, Risk: RiskLocalWrite, SideEffect: true},
	"glob":                       {Domain: "filesystem", Invocation: InvocationDirectTool, Risk: RiskReadOnly, ReadOnly: true},
	"grep":                       {Domain: "filesystem", Invocation: InvocationDirectTool, Risk: RiskReadOnly, ReadOnly: true},
	"kb.doc.meta":                {Domain: "kb", Invocation: InvocationDirectTool, Risk: RiskReadOnly, ReadOnly: true, Capabilities: []Capability{CapabilityKBRead}},
	"kb.doc.structure":           {Domain: "kb", Invocation: InvocationDirectTool, Risk: RiskReadOnly, ReadOnly: true, Capabilities: []Capability{CapabilityKBRead}},
	"kb.section.text":            {Domain: "kb", Invocation: InvocationDirectTool, Risk: RiskReadOnly, ReadOnly: true, Capabilities: []Capability{CapabilityKBRead}},
	"im_api":                     {Domain: "messaging", Invocation: InvocationDirectTool, Risk: RiskExternalWrite, SideEffect: true, Capabilities: allExternalSendCapabilities()},
	"kb_search":                  {Domain: "customer_service", Invocation: InvocationDirectTool, Risk: RiskReadOnly, ReadOnly: true, Capabilities: []Capability{CapabilityCustomerServiceKBRead}},
	"ls":                         {Domain: "filesystem", Invocation: InvocationDirectTool, Risk: RiskReadOnly, ReadOnly: true},
	"memory":                     {Domain: "agent", Invocation: InvocationDirectTool, Risk: RiskLocalWrite, SideEffect: true},
	"multiedit":                  {Domain: "filesystem", Invocation: InvocationDirectTool, Risk: RiskLocalWrite, SideEffect: true},
	"parallel_dispatch":          {Domain: "agent", Invocation: InvocationDirectTool, Risk: RiskLocalWrite, SideEffect: true},
	"promote_todos_to_taskboard": {Domain: "taskboard", Invocation: InvocationDirectTool, Risk: RiskLocalWrite, SideEffect: true},
	"question":                   {Domain: "agent", Invocation: InvocationDirectTool, Risk: RiskReadOnly, ReadOnly: true},
	"read_file":                  {Domain: "filesystem", Invocation: InvocationDirectTool, Risk: RiskReadOnly, ReadOnly: true},
	"remove_tool":                {Domain: "tools", Invocation: InvocationDirectTool, Risk: RiskLocalWrite, SideEffect: true, Capabilities: []Capability{CapabilityMetaToolRegister}},
	"send_im_message":            {Domain: "messaging", Invocation: InvocationDirectTool, Risk: RiskExternalWrite, SideEffect: true, Capabilities: allExternalSendCapabilities()},
	"skill":                      {Domain: "skills", Invocation: InvocationDirectTool, Risk: RiskLocalWrite, SideEffect: true},
	"skill_install":              {Domain: "skills", Invocation: InvocationDirectTool, Risk: RiskLocalWrite, SideEffect: true, Capabilities: []Capability{CapabilityMetaSkillCreate}},
	"skill_search":               {Domain: "skills", Invocation: InvocationDirectTool, Risk: RiskReadOnly, ReadOnly: true},
	"spawn_agent":                {Domain: "agent", Invocation: InvocationDirectTool, Risk: RiskLocalWrite, SideEffect: true},
	"task":                       {Domain: "agent", Invocation: InvocationDirectTool, Risk: RiskLocalWrite, SideEffect: true},
	"taskboard":                  {Domain: "taskboard", Invocation: InvocationDirectTool, Risk: RiskLocalWrite, SideEffect: true},
	"text_to_speech":             {Domain: "media", Invocation: InvocationDirectTool, Risk: RiskReadOnly, ReadOnly: true},
	"tool_search":                {Domain: "discovery", Invocation: InvocationDiscoveryOnly, Risk: RiskReadOnly, ReadOnly: true},
	"todo_write":                 {Domain: "planning", Invocation: InvocationDirectTool, Risk: RiskLocalWrite, SideEffect: true},
	"web_fetch":                  {Domain: "web", Invocation: InvocationDirectTool, Risk: RiskReadOnly, ReadOnly: true},
	"web_search":                 {Domain: "web", Invocation: InvocationDirectTool, Risk: RiskReadOnly, ReadOnly: true},
	"webfetch":                   {Domain: "web", Invocation: InvocationDirectTool, Risk: RiskReadOnly, ReadOnly: true},
	"websearch":                  {Domain: "web", Invocation: InvocationDirectTool, Risk: RiskReadOnly, ReadOnly: true},
	"write_file":                 {Domain: "filesystem", Invocation: InvocationDirectTool, Risk: RiskLocalWrite, SideEffect: true},
}

func allExternalSendCapabilities() []Capability {
	return []Capability{
		CapabilityExternalSend,
		CapabilityExternalSendFeishu,
		CapabilityExternalSendWechatBot,
		CapabilityExternalSendWeCom,
		CapabilityExternalSendDingTalk,
	}
}

var shellCommandTools = map[string]bool{
	"bash":        true,
	"shell":       true,
	"exec":        true,
	"run_command": true,
}

var structuredDangerousActions = map[string]map[string]bool{
	"feishu_api": {
		"create_approval":       true,
		"create_bitable_record": true,
		"update_bitable_record": true,
		"create_task":           true,
		"complete_task":         true,
		"write_sheet":           true,
	},
	"memory":    {"delete": true},
	"taskboard": {"delete": true},
}

var structuredDangerousTools = map[string]bool{
	"create_tool": true,
	"remove_tool": true,
}

type mixedActionRule struct {
	Field                    string
	ReadOnlyActions          []string
	RoutineSideEffectActions []string
	PrivilegedActions        []string
	LocalWriteActions        []string
	ExternalSendActions      []string
}

var mixedActionRules = map[string]mixedActionRule{
	"feishu_api": {
		Field: "action",
		ReadOnlyActions: []string{
			"search_docs", "get_doc_content", "wiki_get_node", "wiki_list_nodes",
			"search_contacts", "get_user_info",
			"get_calendar_events",
			"get_chat_info", "get_chat_admins", "list_chat_members",
			"list_approvals", "get_approval",
			"list_bitable_tables", "list_bitable_records",
			"list_tasks",
			"read_sheet",
			"download_message_resource",
		},
		RoutineSideEffectActions: []string{"send_message"},
		PrivilegedActions: []string{
			"upload_image", "upload_file",
			"send_image", "send_file",
			"create_approval", "create_bitable_record", "update_bitable_record",
			"create_task", "complete_task", "write_sheet",
		},
		ExternalSendActions: []string{
			"search_contacts", "get_user_info",
			"get_chat_info", "get_chat_admins", "list_chat_members",
			"upload_image", "upload_file",
			"send_message", "send_image", "send_file",
		},
	},
	"im_api": {
		Field: "action",
		ReadOnlyActions: []string{
			"search_recipients", "list_recent_conversations", "resolve_recipient",
		},
		RoutineSideEffectActions: []string{"send_message"},
		ExternalSendActions: []string{
			"search_recipients", "list_recent_conversations", "resolve_recipient", "send_message",
		},
	},
	"memory": {
		Field:             "operation",
		ReadOnlyActions:   []string{"search", "list"},
		LocalWriteActions: []string{"save", "update"},
	},
	"taskboard": {
		Field:             "operation",
		ReadOnlyActions:   []string{"get", "list"},
		LocalWriteActions: []string{"create", "update"},
	},
	"browser_interact": {
		Field:             "commands[].action",
		ReadOnlyActions:   []string{"navigate", "snapshot", "wait", "screenshot"},
		LocalWriteActions: []string{"click", "fill", "eval", "close"},
	},
	"filesystem": {
		Field:             "action",
		ReadOnlyActions:   []string{"list", "glob", "grep", "read"},
		LocalWriteActions: []string{"write", "edit", "multiedit"},
	},
}

var actionCapabilityRules = []ActionCapabilityRule{
	{
		ToolName:       "feishu_api",
		Action:         "create_task",
		CapabilityID:   ActionCapabilityExternalTaskCreate,
		Resource:       "task",
		Operation:      "create",
		RiskClass:      ToolRiskPrivilegedSideEffect,
		RequiredFields: []string{"summary"},
		ParameterHints: []ActionCapabilityParameterHint{
			{Name: "action", Type: "string", Required: true, Source: "fixed", Description: "固定为 create_task", Enum: []string{"create_task"}},
			{Name: "summary", Type: "string", Required: true, Source: "user_text", Description: "任务标题/摘要，可从用户原文提取"},
			{Name: "due_time", Type: "string", Source: "user_text", Format: "Unix timestamp string or provider accepted due-time string", Description: "可选截止时间；用户未给出时不要伪造"},
		},
		IntentAliases:      []string{"创建|任务", "新建|任务", "新增|任务", "建|任务", "创建|事项", "新建|事项", "create|task", "new|task", "add|task"},
		Platforms:          []string{"feishu", "lark"},
		PreparatoryActions: []string{"search_contacts", "get_user_info", "list_tasks"},
		ExampleArgs:        map[string]any{"action": "create_task", "summary": "跟进合同"},
		RepairHint:         "create_task 至少需要 summary；缺少标题/摘要时应从用户原文提取，无法提取再追问。",
	},
	{
		ToolName:       "feishu_api",
		Action:         "complete_task",
		CapabilityID:   ActionCapabilityExternalTaskComplete,
		Resource:       "task",
		Operation:      "complete",
		RiskClass:      ToolRiskPrivilegedSideEffect,
		RequiredFields: []string{"task_id"},
		ParameterHints: []ActionCapabilityParameterHint{
			{Name: "action", Type: "string", Required: true, Source: "fixed", Description: "固定为 complete_task", Enum: []string{"complete_task"}},
			{Name: "task_id", Type: "string", Required: true, Source: "list_tasks.result.task_id or explicit_user_input", Description: "飞书任务 ID；用户只给标题时先 list_tasks 查询候选或追问确认"},
		},
		IntentAliases:      []string{"完成|任务", "关闭|任务", "办结|任务", "complete|task", "close|task"},
		Platforms:          []string{"feishu", "lark"},
		PreparatoryActions: []string{"list_tasks"},
		ExampleArgs:        map[string]any{"action": "complete_task", "task_id": "task_id"},
		RepairHint:         "complete_task 需要 task_id；如果用户只给任务标题，应先 list_tasks 查询候选或追问确认。",
	},
	{
		ToolName:       "feishu_api",
		Action:         "create_approval",
		CapabilityID:   ActionCapabilityExternalApprovalSubmit,
		Resource:       "approval",
		Operation:      "submit",
		RiskClass:      ToolRiskPrivilegedSideEffect,
		RequiredFields: []string{"approval_code", "open_id", "form"},
		ParameterHints: []ActionCapabilityParameterHint{
			{Name: "action", Type: "string", Required: true, Source: "fixed", Description: "固定为 create_approval", Enum: []string{"create_approval"}},
			{Name: "approval_code", Type: "string", Required: true, Source: "list_approvals.result.approval_code or explicit_user_input", Description: "审批定义 code；缺失时先 list_approvals/get_approval"},
			{Name: "open_id", Type: "string", Required: true, Source: "search_contacts/get_user_info result", Description: "发起人 open_id；不要伪造"},
			{Name: "form", Type: "string", Required: true, Source: "user_text + approval schema", Format: "JSON string", Description: "审批表单 JSON；字段缺失时追问"},
		},
		IntentAliases:      []string{"创建|审批", "新建|审批", "发起|审批", "提交|审批", "create|approval", "submit|approval"},
		Platforms:          []string{"feishu", "lark"},
		PreparatoryActions: []string{"list_approvals", "get_approval", "search_contacts", "get_user_info"},
		ExampleArgs:        map[string]any{"action": "create_approval", "approval_code": "approval_code", "open_id": "ou_xxx", "form": "{}"},
		RepairHint:         "create_approval 需要 approval_code、open_id 和 form；缺审批定义或发起人时先查询/澄清，不能伪造。",
	},
	{
		ToolName:       "feishu_api",
		Action:         "write_sheet",
		CapabilityID:   ActionCapabilityExternalTableWrite,
		Resource:       "sheet",
		Operation:      "write",
		RiskClass:      ToolRiskPrivilegedSideEffect,
		RequiredFields: []string{"spreadsheet_token", "range", "values"},
		ParameterHints: []ActionCapabilityParameterHint{
			{Name: "action", Type: "string", Required: true, Source: "fixed", Description: "固定为 write_sheet", Enum: []string{"write_sheet"}},
			{Name: "spreadsheet_token", Type: "string", Required: true, Source: "user_link_or_prior_read_sheet", Description: "电子表格 token；缺失时追问"},
			{Name: "range", Type: "string", Required: true, Source: "user_text", Format: "Sheet1!A1:C10", Description: "写入范围；缺失时追问"},
			{Name: "values", Type: "array", Required: true, Source: "user_text", Format: "二维数组", Description: "待写入数据，必须是二维数组"},
		},
		IntentAliases:      []string{"写入|表格", "更新|表格", "写|sheet", "write|sheet", "update|sheet", "write|spreadsheet"},
		Platforms:          []string{"feishu", "lark"},
		PreparatoryActions: []string{"read_sheet"},
		ExampleArgs:        map[string]any{"action": "write_sheet", "spreadsheet_token": "spreadsheet_token", "range": "Sheet1!A1:C1", "values": [][]any{{"值"}}},
		RepairHint:         "write_sheet 需要 spreadsheet_token、range 和二维数组 values；缺少表格位置或数据时必须追问。",
	},
	{
		ToolName:       "feishu_api",
		Action:         "create_bitable_record",
		CapabilityID:   ActionCapabilityExternalRecordCreate,
		Resource:       "bitable_record",
		Operation:      "create",
		RiskClass:      ToolRiskPrivilegedSideEffect,
		RequiredFields: []string{"app_token", "table_id", "fields"},
		ParameterHints: []ActionCapabilityParameterHint{
			{Name: "action", Type: "string", Required: true, Source: "fixed", Description: "固定为 create_bitable_record", Enum: []string{"create_bitable_record"}},
			{Name: "app_token", Type: "string", Required: true, Source: "user_link_or_context", Description: "多维表格 app_token；缺失时追问"},
			{Name: "table_id", Type: "string", Required: true, Source: "list_bitable_tables.result.table_id or explicit_user_input", Description: "数据表 ID；缺失时先查询表列表或追问"},
			{Name: "fields", Type: "object", Required: true, Source: "user_text + table_schema", Description: "字段键值对；字段映射不明确时追问"},
		},
		IntentAliases:      []string{"创建|多维表格|记录", "新增|多维表格|记录", "写入|多维表格", "create|bitable|record", "add|bitable|record"},
		Platforms:          []string{"feishu", "lark"},
		PreparatoryActions: []string{"list_bitable_tables", "list_bitable_records"},
		ExampleArgs:        map[string]any{"action": "create_bitable_record", "app_token": "app_token", "table_id": "table_id", "fields": map[string]any{"字段": "值"}},
		RepairHint:         "create_bitable_record 需要 app_token、table_id 和 fields；缺少表或字段映射时先查询/澄清。",
	},
	{
		ToolName:       "feishu_api",
		Action:         "update_bitable_record",
		CapabilityID:   ActionCapabilityExternalRecordUpdate,
		Resource:       "bitable_record",
		Operation:      "update",
		RiskClass:      ToolRiskPrivilegedSideEffect,
		RequiredFields: []string{"app_token", "table_id", "record_id", "fields"},
		ParameterHints: []ActionCapabilityParameterHint{
			{Name: "action", Type: "string", Required: true, Source: "fixed", Description: "固定为 update_bitable_record", Enum: []string{"update_bitable_record"}},
			{Name: "app_token", Type: "string", Required: true, Source: "user_link_or_context", Description: "多维表格 app_token；缺失时追问"},
			{Name: "table_id", Type: "string", Required: true, Source: "list_bitable_tables.result.table_id or explicit_user_input", Description: "数据表 ID；缺失时先查询表列表或追问"},
			{Name: "record_id", Type: "string", Required: true, Source: "list_bitable_records.result.record_id or explicit_user_input", Description: "记录 ID；缺失时先查候选记录或追问"},
			{Name: "fields", Type: "object", Required: true, Source: "user_text + table_schema", Description: "要更新的字段键值对；字段映射不明确时追问"},
		},
		IntentAliases:      []string{"更新|多维表格|记录", "修改|多维表格|记录", "update|bitable|record"},
		Platforms:          []string{"feishu", "lark"},
		PreparatoryActions: []string{"list_bitable_tables", "list_bitable_records"},
		ExampleArgs:        map[string]any{"action": "update_bitable_record", "app_token": "app_token", "table_id": "table_id", "record_id": "record_id", "fields": map[string]any{"字段": "新值"}},
		RepairHint:         "update_bitable_record 需要 app_token、table_id、record_id 和 fields；缺记录 ID 时先查询候选或追问。",
	},
}

var systemDelegationAgents = map[string]bool{
	"codereview":  true,
	"compaction":  true,
	"title-agent": true,
	"summary":     true,
}

var hostToolSets = map[HostToolSet]map[string]bool{
	HostToolSetDefaultVisible: {
		"filesystem":  true,
		"ls":          true,
		"memory":      true,
		"question":    true,
		"skill":       true,
		"tool_search": true,
	},
	HostToolSetPlanControl: {
		"todo_write":                 true,
		"finish_plan":                true,
		"enter_plan_mode":            true,
		"exit_plan_mode":             true,
		"create_handoff_summary":     true,
		"promote_todos_to_taskboard": true,
	},
	HostToolSetPlanAllowed: {
		"exit_plan_mode":             true,
		"filesystem":                 true,
		"glob":                       true,
		"grep":                       true,
		"ls":                         true,
		"memory":                     true,
		"question":                   true,
		"read_file":                  true,
		"skill":                      true,
		"todo_write":                 true,
		"create_handoff_summary":     true,
		"promote_todos_to_taskboard": true,
		"tool_search":                true,
		"webfetch":                   true,
		"websearch":                  true,
		"web_fetch":                  true,
		"web_search":                 true,
	},
}

var hostToolGroups = map[string][]string{
	"agent":            {"spawn_agent", "parallel_dispatch", "task"},
	"customer_service": {"kb_search", "escalate_to_human", "cancel_escalation"},
	"discovery":        {"tool_search"},
	"fs":               {"filesystem", "read_file", "write_file", "edit", "glob", "grep", "ls", "multiedit", "apply_patch"},
	"kb":               {"kb.doc.meta", "kb.doc.structure", "kb.section.text"},
	"lsp":              {"lsp_definition", "lsp_references", "lsp_hover", "lsp_symbols", "lsp_diagnostics", "lsp_rename", "lsp_code_action", "lsp_format", "lsp_completion"},
	"runtime":          {"bash"},
	"web":              {"websearch", "webfetch", "web_search", "web_fetch", "browser_interact"},
}

var hostToolPolicyProfiles = map[string][]string{
	"coding": {
		"group:fs", "group:runtime", "group:web", "group:lsp", "group:discovery",
		"skill", "memory", "batch", "question",
	},
	"full":      {"*"},
	"messaging": {"send_im_message", "feishu_api", "skill"},
	"readonly":  {"filesystem", "read_file", "glob", "grep", "ls", "websearch", "webfetch", "web_search", "web_fetch"},
	"master":    {"skill", "memory", "question", "taskboard", "task", "spawn_agent", "parallel_dispatch"},
	"master_direct": {
		"group:fs", "group:runtime", "group:web", "group:lsp", "group:agent", "group:discovery", "group:kb",
		"create_tool", "remove_tool",
		"skill", "memory", "question", "taskboard", "batch",
		"send_im_message", "feishu_api",
	},
}

var subagentDeniedHostTools = []string{"spawn_agent", "create_tool", "remove_tool"}
var subagentLeafDeniedHostTools = []string{"parallel_dispatch", "task"}

func intentCapabilityRule(kind IntentKind) (IntentCapabilityRule, bool) {
	rule, ok := intentCapabilityRules[kind]
	return IntentCapabilityRule{Required: cloneCapabilities(rule.Required)}, ok
}

func skillDomainRule(domain string) (SkillDomainRule, bool) {
	rule, ok := skillDomainRules[strings.TrimSpace(domain)]
	return SkillDomainRule{
		Capabilities:       cloneCapabilities(rule.Capabilities),
		AllowedIntentKinds: cloneIntentKinds(rule.AllowedIntentKinds),
		CallableTool:       rule.CallableTool,
	}, ok
}

func knownSkillWorkflowDomain(name string) (string, bool) {
	domain, ok := knownSkillWorkflowDomains[strings.TrimSpace(strings.ToLower(name))]
	return domain, ok
}

func builtinToolRule(nameLower string) (BuiltinToolRule, bool) {
	nameLower = strings.TrimSpace(strings.ToLower(nameLower))
	if shellCommandTools[nameLower] {
		return BuiltinToolRule{Domain: "filesystem", Invocation: InvocationDirectTool, Risk: RiskRuntimeExec, SideEffect: true, Capabilities: []Capability{CapabilityRuntimeExec}}, true
	}
	if strings.HasPrefix(nameLower, "lsp_") {
		return BuiltinToolRule{Domain: "lsp", Invocation: InvocationDirectTool, Risk: RiskReadOnly, ReadOnly: true}, true
	}
	rule, ok := builtinToolRules[nameLower]
	return BuiltinToolRule{
		Domain:       rule.Domain,
		Invocation:   rule.Invocation,
		Risk:         rule.Risk,
		ReadOnly:     rule.ReadOnly,
		Destructive:  rule.Destructive,
		Idempotent:   rule.Idempotent,
		OpenWorld:    rule.OpenWorld,
		SideEffect:   rule.SideEffect,
		Capabilities: cloneCapabilities(rule.Capabilities),
	}, ok
}

// BuiltinToolProfile returns the canonical trusted profile for a host-owned tool.
func BuiltinToolProfile(name string) (ToolProfile, bool) {
	normalized := ToolNamePolicy{}.Normalize(name)
	rule, ok := builtinToolRule(normalized)
	if !ok {
		return ToolProfile{}, false
	}
	return ToolProfile{
		Name:          normalized,
		Kind:          CapabilityKindBuiltinTool,
		Domain:        rule.Domain,
		Source:        CapabilitySourceBuiltin,
		Invocation:    rule.Invocation,
		Risk:          rule.Risk,
		Trust:         TrustBuiltIn,
		ReadOnly:      rule.ReadOnly,
		Destructive:   rule.Destructive,
		Idempotent:    rule.Idempotent,
		OpenWorld:     rule.OpenWorld,
		SideEffect:    rule.SideEffect,
		Capabilities:  cloneCapabilities(rule.Capabilities),
		Version:       "v1",
		Visibility:    "system",
		PolicyProfile: "default",
	}, true
}

// IsKnownHostTool reports whether the host owns this tool name family.
func IsKnownHostTool(name string) bool {
	_, ok := builtinToolRule(name)
	return ok
}

// IsShellCommandTool reports whether a tool input is a shell command payload.
func IsShellCommandTool(name string) bool {
	return shellCommandTools[strings.TrimSpace(strings.ToLower(name))]
}

// IsMixedReadWriteTool reports whether a tool exposes both read operations and
// write operations behind an action/operation field.
func IsMixedReadWriteTool(name string) bool {
	_, ok := mixedActionRuleForTool(name)
	return ok
}

// MixedActionField 返回混合读写工具的动作字段路径。
func MixedActionField(name string) string {
	rule, ok := mixedActionRuleForTool(name)
	if !ok {
		return ""
	}
	return rule.Field
}

// MixedReadOnlyActions 返回混合工具在只读意图下可用的动作。
func MixedReadOnlyActions(name string) []string {
	rule, ok := mixedActionRuleForTool(name)
	if !ok {
		return nil
	}
	return cloneStrings(rule.ReadOnlyActions)
}

// MixedLocalWriteActions 返回混合工具的非危险本地写动作。
func MixedLocalWriteActions(name string) []string {
	rule, ok := mixedActionRuleForTool(name)
	if !ok {
		return nil
	}
	return cloneStrings(rule.LocalWriteActions)
}

// RoutineSideEffectActions 返回用户明确要求后可直接执行的常规副作用动作。
func RoutineSideEffectActions(name string) []string {
	rule, ok := mixedActionRuleForTool(name)
	if !ok {
		return nil
	}
	return cloneStrings(rule.RoutineSideEffectActions)
}

// PrivilegedActions 返回需要执行前确认的高影响副作用动作。
func PrivilegedActions(name string) []string {
	rule, ok := mixedActionRuleForTool(name)
	if !ok {
		return nil
	}
	return cloneStrings(rule.PrivilegedActions)
}

// ExternalSendActions 返回外部发送意图下可用的检索和发送动作。
func ExternalSendActions(name string) []string {
	rule, ok := mixedActionRuleForTool(name)
	if !ok {
		return nil
	}
	return cloneStrings(rule.ExternalSendActions)
}

// ExternalWriteActions 返回外部业务写入意图下可用的读、发送和高影响写动作。
// 高影响写动作仍只进入路由候选，具体执行必须经过 ActionGuard/HITL。
func ExternalWriteActions(name string) []string {
	rule, ok := mixedActionRuleForTool(name)
	if !ok {
		return nil
	}
	actions := append(cloneStrings(rule.ReadOnlyActions), rule.ExternalSendActions...)
	actions = append(actions, rule.PrivilegedActions...)
	return uniqueSortedStrings(actions)
}

// ActionCapabilitySignal 返回 intent signals 中使用的动作能力标签。
func ActionCapabilitySignal(capabilityID string) string {
	capabilityID = strings.TrimSpace(capabilityID)
	if capabilityID == "" {
		return ""
	}
	return IntentActionCapabilitySignalPrefix + capabilityID
}

// IntentActionCapabilityIDs 返回本轮 intent 显式携带的动作能力 subtype。
func IntentActionCapabilityIDs(intent IntentFrame) []string {
	seen := map[string]bool{}
	var out []string
	for _, signal := range intent.Signals {
		signal = strings.TrimSpace(signal)
		if !strings.HasPrefix(signal, IntentActionCapabilitySignalPrefix) {
			continue
		}
		capabilityID := strings.TrimSpace(strings.TrimPrefix(signal, IntentActionCapabilitySignalPrefix))
		if capabilityID == "" || seen[capabilityID] {
			continue
		}
		seen[capabilityID] = true
		out = append(out, capabilityID)
	}
	sort.Strings(out)
	return out
}

// ActionCapabilityRules 返回动作级能力规则的拷贝，供 tool_search 和测试使用。
func ActionCapabilityRules() []ActionCapabilityRule {
	out := make([]ActionCapabilityRule, 0, len(actionCapabilityRules))
	for _, rule := range actionCapabilityRules {
		out = append(out, cloneActionCapabilityRule(rule))
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].ToolName == out[j].ToolName {
			return out[i].Action < out[j].Action
		}
		return out[i].ToolName < out[j].ToolName
	})
	return out
}

// ActionCapabilityRulesForTool 返回某个工具拥有的动作能力规则。
func ActionCapabilityRulesForTool(toolName string) []ActionCapabilityRule {
	toolName = strings.TrimSpace(strings.ToLower(toolName))
	var out []ActionCapabilityRule
	for _, rule := range actionCapabilityRules {
		if strings.TrimSpace(strings.ToLower(rule.ToolName)) == toolName {
			out = append(out, cloneActionCapabilityRule(rule))
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].Action < out[j].Action
	})
	return out
}

// ActionCapabilityRuleForAction 返回工具 action 对应的动作能力规则。
func ActionCapabilityRuleForAction(toolName, action string) (ActionCapabilityRule, bool) {
	toolName = strings.TrimSpace(strings.ToLower(toolName))
	action = strings.TrimSpace(strings.ToLower(action))
	if toolName == "" || action == "" {
		return ActionCapabilityRule{}, false
	}
	for _, rule := range actionCapabilityRules {
		if strings.TrimSpace(strings.ToLower(rule.ToolName)) == toolName &&
			strings.TrimSpace(strings.ToLower(rule.Action)) == action {
			return cloneActionCapabilityRule(rule), true
		}
	}
	return ActionCapabilityRule{}, false
}

// ActionCapabilityRulesForIntent 返回当前 intent subtype 匹配的动作能力规则。
func ActionCapabilityRulesForIntent(intent IntentFrame) []ActionCapabilityRule {
	ids := IntentActionCapabilityIDs(intent)
	if len(ids) == 0 {
		return nil
	}
	want := make(map[string]bool, len(ids))
	for _, id := range ids {
		want[id] = true
	}
	var out []ActionCapabilityRule
	for _, rule := range actionCapabilityRules {
		if want[rule.CapabilityID] {
			out = append(out, cloneActionCapabilityRule(rule))
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].ToolName == out[j].ToolName {
			return out[i].Action < out[j].Action
		}
		return out[i].ToolName < out[j].ToolName
	})
	return out
}

// MatchActionCapabilityRulesForText 从用户自然语言中召回动作能力规则。
// 这里的 alias 只负责把自然语言映射到 host-owned capability，不直接授权执行。
func MatchActionCapabilityRulesForText(text string) []ActionCapabilityRule {
	q := strings.ToLower(strings.TrimSpace(text))
	if q == "" {
		return nil
	}
	var scored []actionCapabilityScoredRule
	for _, rule := range actionCapabilityRules {
		score := actionCapabilityRuleMatchScore(rule, q)
		if score <= 0 {
			continue
		}
		scored = append(scored, actionCapabilityScoredRule{rule: rule, score: score})
	}
	var out []ActionCapabilityRule
	seen := map[string]bool{}
	for _, candidate := range scored {
		if hasMoreSpecificActionCapabilityMatch(candidate, scored) {
			continue
		}
		key := strings.TrimSpace(strings.ToLower(candidate.rule.ToolName)) + ":" + strings.TrimSpace(strings.ToLower(candidate.rule.Action))
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, cloneActionCapabilityRule(candidate.rule))
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].CapabilityID == out[j].CapabilityID {
			return out[i].Action < out[j].Action
		}
		return out[i].CapabilityID < out[j].CapabilityID
	})
	return out
}

func hasMoreSpecificActionCapabilityMatch(candidate actionCapabilityScoredRule, matches []actionCapabilityScoredRule) bool {
	for _, other := range matches {
		if other.score <= candidate.score {
			continue
		}
		if strings.TrimSpace(strings.ToLower(other.rule.ToolName)) != strings.TrimSpace(strings.ToLower(candidate.rule.ToolName)) {
			continue
		}
		if actionCapabilityRulesOverlap(candidate.rule, other.rule) {
			return true
		}
	}
	return false
}

func actionCapabilityRulesOverlap(left, right ActionCapabilityRule) bool {
	if strings.TrimSpace(left.CapabilityID) == strings.TrimSpace(right.CapabilityID) {
		return true
	}
	if strings.TrimSpace(left.Resource) != "" && strings.TrimSpace(left.Resource) == strings.TrimSpace(right.Resource) {
		return true
	}
	if actionCapabilityAliasPartSubset(left.IntentAliases, right.IntentAliases) || actionCapabilityAliasPartSubset(right.IntentAliases, left.IntentAliases) {
		return true
	}
	return false
}

func actionCapabilityAliasPartSubset(leftAliases, rightAliases []string) bool {
	for _, left := range leftAliases {
		leftParts := actionCapabilityAliasParts(left)
		if len(leftParts) == 0 {
			continue
		}
		for _, right := range rightAliases {
			rightParts := actionCapabilityAliasParts(right)
			if aliasPartsCoveredBy(rightParts, leftParts) {
				return true
			}
		}
	}
	return false
}

func actionCapabilityAliasParts(alias string) []string {
	alias = strings.ToLower(strings.TrimSpace(alias))
	if alias == "" {
		return nil
	}
	var out []string
	for _, part := range strings.Split(alias, "|") {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	sort.Strings(out)
	return out
}

func aliasPartsCoveredBy(haystack, needles []string) bool {
	if len(needles) == 0 || len(haystack) < len(needles) {
		return false
	}
	for _, needle := range needles {
		covered := false
		for _, candidate := range haystack {
			if candidate == needle || strings.Contains(candidate, needle) {
				covered = true
				break
			}
		}
		if !covered {
			return false
		}
	}
	return true
}

func actionCapabilityRuleMatchesText(rule ActionCapabilityRule, q string) bool {
	return actionCapabilityRuleMatchScore(rule, q) > 0
}

func actionCapabilityRuleMatchScore(rule ActionCapabilityRule, q string) int {
	best := 0
	for _, alias := range rule.IntentAliases {
		if score := actionCapabilityAliasMatchScore(q, alias); score > best {
			best = score
		}
	}
	return best
}

func actionCapabilityAliasMatches(q, alias string) bool {
	return actionCapabilityAliasMatchScore(q, alias) > 0
}

func actionCapabilityAliasMatchScore(q, alias string) int {
	alias = strings.ToLower(strings.TrimSpace(alias))
	if alias == "" {
		return 0
	}
	score := 0
	for _, part := range strings.Split(alias, "|") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if !strings.Contains(q, part) {
			return 0
		}
		score += 100 + len([]rune(part))
	}
	return score
}

// ExternalWriteActionsForIntent 返回外部业务写入下的最小 action 集。
// 有 action_capability subtype 时，只开放目标 action 和声明的预备 action；
// 无 subtype 时保留旧 generic business-write 兼容路径。
func ExternalWriteActionsForIntent(name string, intent IntentFrame) []string {
	capabilityIDs := IntentActionCapabilityIDs(intent)
	if len(capabilityIDs) == 0 {
		return ExternalWriteActions(name)
	}
	toolName := strings.TrimSpace(strings.ToLower(name))
	var actions []string
	for _, rule := range ActionCapabilityRulesForIntent(intent) {
		if strings.TrimSpace(strings.ToLower(rule.ToolName)) != toolName {
			continue
		}
		actions = append(actions, rule.PreparatoryActions...)
		actions = append(actions, rule.Action)
	}
	return uniqueSortedStrings(actions)
}

// MixedAllowedToolInputsForIntent 返回混合工具在当前意图下的输入约束。
func MixedAllowedToolInputsForIntent(intent IntentFrame, toolName string) map[string]string {
	rule, ok := mixedActionRuleForTool(toolName)
	if !ok || strings.TrimSpace(rule.Field) == "" {
		return nil
	}
	var actions []string
	switch intent.Kind {
	case IntentRead, IntentAnswer, IntentPlan:
		if len(rule.ExternalSendActions) > 0 {
			return nil
		}
		actions = rule.ReadOnlyActions
	case IntentExternalRead:
		actions = rule.ReadOnlyActions
	case IntentWriteLocal:
		if !intent.AllowsSideEffects {
			actions = rule.ReadOnlyActions
			break
		}
		actions = append(cloneStrings(rule.ReadOnlyActions), rule.LocalWriteActions...)
	case IntentExternalWrite:
		if !intent.AllowsSideEffects {
			return nil
		}
		if intentHasSignal(intent, IntentSignalExternalBusinessWrite) {
			actions = ExternalWriteActionsForIntent(toolName, intent)
		} else {
			actions = rule.ExternalSendActions
		}
	}
	if len(actions) == 0 {
		return nil
	}
	return map[string]string{rule.Field: strings.Join(uniqueSortedStrings(actions), "|")}
}

func intentHasSignal(intent IntentFrame, signal string) bool {
	signal = strings.TrimSpace(signal)
	if signal == "" {
		return false
	}
	for _, item := range intent.Signals {
		if strings.TrimSpace(item) == signal {
			return true
		}
	}
	return false
}

// MixedReadOnlyToolInputs returns the safe read/list constraints for default
// visible mixed tools before a side-effect route decision grants wider access.
func MixedReadOnlyToolInputs(toolName string) map[string]string {
	return MixedAllowedToolInputsForIntent(IntentFrame{Kind: IntentRead}, toolName)
}

// StructuredDangerousOperation reports whether a structured tool input requires
// HITL in minimal mode because the specific action/operation has side effects.
func StructuredDangerousOperation(toolName string, input json.RawMessage) bool {
	toolName = strings.TrimSpace(strings.ToLower(toolName))
	if structuredDangerousTools[toolName] {
		return true
	}
	action := structuredAction(input)
	if action == "" {
		return false
	}
	actions := structuredDangerousActions[toolName]
	return actions["*"] || actions[action]
}

// StructuredDangerousAction reports whether a concrete action/operation value
// is dangerous for a mixed structured tool.
func StructuredDangerousAction(toolName, action string) bool {
	toolName = strings.TrimSpace(strings.ToLower(toolName))
	action = strings.TrimSpace(strings.ToLower(action))
	if toolName == "" || action == "" {
		return false
	}
	actions := structuredDangerousActions[toolName]
	return actions["*"] || actions[action]
}

// StructuredPrivilegedAction reports whether an action has high-impact side effects.
func StructuredPrivilegedAction(toolName, action string) bool {
	return containsActionString(PrivilegedActions(toolName), action) || StructuredDangerousAction(toolName, action)
}

// StructuredRoutineSideEffectAction reports whether an action is routine once inputs are validated.
func StructuredRoutineSideEffectAction(toolName, action string) bool {
	return containsActionString(RoutineSideEffectActions(toolName), action)
}

// StructuredDangerousActions returns the dangerous action names for a tool.
func StructuredDangerousActions(toolName string) []string {
	toolName = strings.TrimSpace(strings.ToLower(toolName))
	actions := structuredDangerousActions[toolName]
	if len(actions) == 0 || actions["*"] {
		return nil
	}
	names := make([]string, 0, len(actions))
	for action := range actions {
		names = append(names, action)
	}
	sort.Strings(names)
	return names
}

// ProfileRequiresApproval reports whether the unified policy needs human
// attention for this profile before action-level runtime narrowing.
func ProfileRequiresApproval(profile ToolProfile) bool {
	if structuredDangerousTools[strings.TrimSpace(strings.ToLower(profile.Name))] {
		return true
	}
	decision := EvaluateToolPolicy(profile, ToolPolicyContext{ForRoute: true})
	return decision.RequiresApproval
}

// ProfileMayRequireApproval reports catalog-level approval potential. It must
// not be used as the concrete execution approval decision.
func ProfileMayRequireApproval(profile ToolProfile) bool {
	name := strings.TrimSpace(strings.ToLower(profile.Name))
	if structuredDangerousTools[name] {
		return true
	}
	if profile.OpenWorld || profile.Destructive {
		return true
	}
	switch profile.Risk {
	case RiskRuntimeExec, RiskDestructive, RiskUnknown:
		return true
	}
	if IsMixedReadWriteTool(name) {
		return len(PrivilegedActions(name)) > 0 || len(StructuredDangerousActions(name)) > 0
	}
	return false
}

// ToolActionProfile specializes a mixed read/write tool profile using a
// structured action/operation value when available.
// Invariant: dangerous mixed operations return the original profile so
// EvaluateToolPolicy can ask through the mixed policy instead of outer deny.
func ToolActionProfile(profile ToolProfile, input json.RawMessage) ToolProfile {
	if profile.Name == "" {
		return profile
	}
	if !IsMixedReadWriteTool(profile.Name) {
		if profile.Risk == RiskExternalWrite && len(input) > 0 && !IsRoutinePlainTextExternalSend(profile.Name, input) {
			profile.ReadOnly = false
			profile.SideEffect = true
		}
		return profile
	}
	if StructuredDangerousOperation(profile.Name, input) {
		return profile
	}
	action := structuredAction(input)
	if action == "" {
		return profile
	}
	if containsActionString(MixedReadOnlyActions(profile.Name), action) {
		profile.Risk = RiskReadOnly
		profile.ReadOnly = true
		profile.SideEffect = false
		return profile
	}
	if containsActionString(RoutineSideEffectActions(profile.Name), action) {
		profile.Risk = RiskExternalWrite
		profile.ReadOnly = false
		profile.SideEffect = true
		return profile
	}
	if containsActionString(PrivilegedActions(profile.Name), action) {
		profile.Risk = RiskExternalWrite
		profile.ReadOnly = false
		profile.SideEffect = true
		return profile
	}
	if containsActionString(MixedLocalWriteActions(profile.Name), action) {
		profile.Risk = RiskLocalWrite
		profile.ReadOnly = false
		profile.SideEffect = true
		return profile
	}
	if containsActionString(ExternalSendActions(profile.Name), action) {
		profile.Risk = RiskExternalWrite
		profile.ReadOnly = false
		profile.SideEffect = true
		return profile
	}
	return profile
}

// ToolActionRiskRules returns the canonical action-level rules for permission defaults.
func ToolActionRiskRules() []ToolActionRiskRule {
	out := make([]ToolActionRiskRule, 0, len(structuredDangerousActions))
	for tool, actions := range structuredDangerousActions {
		if actions["*"] {
			continue
		}
		names := make([]string, 0, len(actions))
		for action := range actions {
			names = append(names, action)
		}
		sort.Strings(names)
		out = append(out, ToolActionRiskRule{ToolName: tool, Actions: names})
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].ToolName < out[j].ToolName
	})
	return out
}

// IsSystemDelegationAgent reports whether an agent id is reserved for internal system jobs.
func IsSystemDelegationAgent(agentID string) bool {
	return systemDelegationAgents[strings.TrimSpace(strings.ToLower(agentID))]
}

// IsHostToolInSet reports whether a tool belongs to a named host policy set.
func IsHostToolInSet(set HostToolSet, name string) bool {
	tools, ok := hostToolSets[set]
	if !ok {
		return false
	}
	return tools[strings.TrimSpace(name)]
}

// HostToolSetMembers returns a copy of a named host policy set.
func HostToolSetMembers(set HostToolSet) []string {
	tools, ok := hostToolSets[set]
	if !ok || len(tools) == 0 {
		return nil
	}
	out := make([]string, 0, len(tools))
	for name := range tools {
		out = append(out, name)
	}
	return out
}

// HostToolPolicyGroups returns canonical tool groups used to build default config.
func HostToolPolicyGroups() map[string][]string {
	return cloneStringSliceMap(hostToolGroups)
}

// HostToolPolicyProfiles returns canonical default tool profiles.
func HostToolPolicyProfiles() map[string][]string {
	return cloneStringSliceMap(hostToolPolicyProfiles)
}

func SubagentDeniedHostTools() []string {
	return append([]string(nil), subagentDeniedHostTools...)
}

func SubagentLeafDeniedHostTools() []string {
	return append([]string(nil), subagentLeafDeniedHostTools...)
}

func isDiscoveryOnlyProfile(profile ToolProfile) bool {
	return profile.Invocation == InvocationDiscoveryOnly || strings.TrimSpace(profile.Name) == "tool_search"
}

func isDiscoveryEntrypoint(profile ToolProfile) bool {
	return strings.TrimSpace(profile.Name) == "tool_search" &&
		profile.Kind == CapabilityKindBuiltinTool &&
		profile.Source == CapabilitySourceBuiltin &&
		profile.Risk == RiskReadOnly
}

func isQuestionEntrypoint(profile ToolProfile) bool {
	return strings.TrimSpace(profile.Name) == "question" &&
		profile.Kind == CapabilityKindBuiltinTool &&
		profile.Source == CapabilitySourceBuiltin &&
		profile.Risk == RiskReadOnly &&
		profile.ReadOnly &&
		!ProfileHasSideEffect(profile)
}

func isListOnlySkillInvocation(profile ToolProfile, input json.RawMessage) bool {
	if strings.TrimSpace(profile.Name) != "skill" {
		return false
	}
	if len(strings.TrimSpace(string(input))) == 0 {
		return true
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(input, &payload); err != nil {
		return false
	}
	raw, ok := payload["name"]
	if !ok {
		return true
	}
	var name string
	if err := json.Unmarshal(raw, &name); err != nil {
		return false
	}
	return strings.TrimSpace(name) == ""
}

func isNamedSkillInvocation(profile ToolProfile, input json.RawMessage) bool {
	if strings.TrimSpace(profile.Name) != "skill" || len(strings.TrimSpace(string(input))) == 0 {
		return false
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(input, &payload); err != nil {
		return false
	}
	raw, ok := payload["name"]
	if !ok {
		return false
	}
	var name string
	if err := json.Unmarshal(raw, &name); err != nil {
		return false
	}
	return strings.TrimSpace(name) != ""
}

func skillEntrypointCallableForIntent(intent IntentFrame) bool {
	switch intent.Kind {
	case IntentCreateSkill, IntentModifySkill, IntentManageTool:
		return true
	default:
		return false
	}
}

func isPlanControlProfile(profile ToolProfile) bool {
	name := strings.TrimSpace(profile.Name)
	return IsHostToolInSet(HostToolSetPlanControl, name) ||
		name == "todo_write"
}

func structuredAction(input json.RawMessage) string {
	if len(input) == 0 {
		return ""
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(input, &payload); err != nil {
		return ""
	}
	for _, name := range []string{"action", "operation"} {
		raw, ok := payload[name]
		if !ok {
			continue
		}
		var value string
		if err := json.Unmarshal(raw, &value); err == nil {
			return strings.ToLower(strings.TrimSpace(value))
		}
	}
	return ""
}

func mixedActionRuleForTool(name string) (mixedActionRule, bool) {
	rule, ok := mixedActionRules[strings.TrimSpace(strings.ToLower(name))]
	return mixedActionRule{
		Field:                    rule.Field,
		ReadOnlyActions:          cloneStrings(rule.ReadOnlyActions),
		RoutineSideEffectActions: cloneStrings(rule.RoutineSideEffectActions),
		PrivilegedActions:        cloneStrings(rule.PrivilegedActions),
		LocalWriteActions:        cloneStrings(rule.LocalWriteActions),
		ExternalSendActions:      cloneStrings(rule.ExternalSendActions),
	}, ok
}

func containsActionString(values []string, want string) bool {
	want = strings.TrimSpace(strings.ToLower(want))
	if want == "" {
		return false
	}
	for _, value := range values {
		if strings.TrimSpace(strings.ToLower(value)) == want {
			return true
		}
	}
	return false
}

func uniqueSortedStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func cloneCapabilities(in []Capability) []Capability {
	if len(in) == 0 {
		return nil
	}
	return append([]Capability(nil), in...)
}

func cloneIntentKinds(in []IntentKind) []IntentKind {
	if len(in) == 0 {
		return nil
	}
	return append([]IntentKind(nil), in...)
}

func cloneStrings(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	return append([]string(nil), in...)
}

func cloneStringSliceMap(in map[string][]string) map[string][]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string][]string, len(in))
	for key, values := range in {
		out[key] = append([]string(nil), values...)
	}
	return out
}

func cloneActionCapabilityRule(rule ActionCapabilityRule) ActionCapabilityRule {
	rule.RequiredFields = cloneStrings(rule.RequiredFields)
	if len(rule.ParameterHints) > 0 {
		hints := make([]ActionCapabilityParameterHint, len(rule.ParameterHints))
		for i, hint := range rule.ParameterHints {
			hint.Enum = cloneStrings(hint.Enum)
			hints[i] = hint
		}
		rule.ParameterHints = hints
	}
	rule.IntentAliases = cloneStrings(rule.IntentAliases)
	rule.Platforms = cloneStrings(rule.Platforms)
	rule.PreparatoryActions = cloneStrings(rule.PreparatoryActions)
	if len(rule.ExampleArgs) > 0 {
		example := make(map[string]any, len(rule.ExampleArgs))
		for key, value := range rule.ExampleArgs {
			example[key] = value
		}
		rule.ExampleArgs = example
	}
	return rule
}
