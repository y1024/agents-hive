package security

// BuiltinDangerousRules 是内置高风险命令规则。
// 这些规则在用户配置规则之前匹配，优先级最高；高风险动作走人工确认，
// 不在策略层直接终止业务请求。
var BuiltinDangerousRules = []ExecRule{
	// === PolicyAsk: 需要人工审批 ===
	{Pattern: `^rm\s+-rf\s+/$`, Policy: PolicyAsk, Description: "删除根目录需审批"},
	{Pattern: `^rm\s+-rf\s+/\*`, Policy: PolicyAsk, Description: "删除根目录下所有内容需审批"},
	{Pattern: `^mkfs`, Policy: PolicyAsk, Description: "格式化磁盘需审批"},
	{Pattern: `^dd\s+if=.*\s+of=/dev/`, Policy: PolicyAsk, Description: "直接写磁盘设备需审批"},
	{Pattern: `>\s*/dev/sd`, Policy: PolicyAsk, Description: "重定向到磁盘设备需审批"},
	{Pattern: `>\s*/dev/nvme`, Policy: PolicyAsk, Description: "重定向到 NVMe 设备需审批"},
	{Pattern: `:\(\)\s*\{`, Policy: PolicyAsk, Description: "fork bomb 形态命令需审批"},
	{Pattern: `^rm\s+-rf\s+`, Policy: PolicyAsk, Description: "递归强制删除需审批"},
	{Pattern: `^rm\s+-r\s+`, Policy: PolicyAsk, Description: "递归删除需审批"},
	{Pattern: `(?i)^DROP\s+TABLE\s+`, Policy: PolicyAsk, Description: "删表需审批"},
	{Pattern: `(?i)^DROP\s+DATABASE\s+`, Policy: PolicyAsk, Description: "删库需审批"},
	{Pattern: `(?i)^TRUNCATE\s+`, Policy: PolicyAsk, Description: "清空表需审批"},
	{Pattern: `^git\s+push\s+--force`, Policy: PolicyAsk, Description: "强制推送需审批"},
	{Pattern: `^git\s+push\s+-f\s+`, Policy: PolicyAsk, Description: "强制推送需审批"},
	{Pattern: `^git\s+reset\s+--hard`, Policy: PolicyAsk, Description: "硬重置需审批"},
	{Pattern: `^kubectl\s+delete\s+`, Policy: PolicyAsk, Description: "K8s 删除需审批"},
	{Pattern: `^docker\s+rm\s+-f\s+`, Policy: PolicyAsk, Description: "强制删除容器需审批"},
	{Pattern: `^docker\s+rmi\s+-f\s+`, Policy: PolicyAsk, Description: "强制删除镜像需审批"},
	{Pattern: `^chmod\s+777\s+`, Policy: PolicyAsk, Description: "全开权限需审批"},
}
