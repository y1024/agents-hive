package skills

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/chef-guo/agents-hive/internal/errs"
)

// ScriptRunner 执行 skill 的 scripts/ 目录中的脚本
type ScriptRunner struct {
	timeout  time.Duration
	logger   *zap.Logger
	Executor SandboxExecutor // 沙箱执行器（可选，由 bootstrap 注入）
}

// NewScriptRunner 创建具有给定超时的新 ScriptRunner
func NewScriptRunner(timeout time.Duration, logger *zap.Logger) *ScriptRunner {
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	return &ScriptRunner{
		timeout: timeout,
		logger:  logger,
	}
}

// extensionMap 将文件扩展名映射到其解释器
var extensionMap = map[string]string{
	".sh":  "/bin/sh",
	".py":  "python3",
	".js":  "node",
	".ts":  "npx ts-node",
	".rb":  "ruby",
	".pl":  "perl",
	".lua": "lua",
}

// detectInterpreter 确定脚本文件的解释器。
// 首先检查文件扩展名，然后回退到 shebang 行
func detectInterpreter(scriptPath string) (string, error) {
	ext := filepath.Ext(scriptPath)
	if interp, ok := extensionMap[ext]; ok {
		return interp, nil
	}

	// 回退到 shebang
	data, err := os.ReadFile(scriptPath)
	if err != nil {
		return "", errs.Wrap(errs.CodeSkillExecFailed, fmt.Sprintf("read script %s", scriptPath), err)
	}

	content := string(data)
	if strings.HasPrefix(content, "#!") {
		firstLine := strings.SplitN(content, "\n", 2)[0]
		shebang := strings.TrimPrefix(firstLine, "#!")
		shebang = strings.TrimSpace(shebang)
		shebang = strings.TrimRight(shebang, "\r") // 处理 Windows \r\n 行尾
		if shebang != "" {
			return shebang, nil
		}
	}

	return "", errs.New(errs.CodeSkillExecFailed,
		fmt.Sprintf("no interpreter found for %s (unknown extension %q and no shebang)",
			filepath.Base(scriptPath), ext))
}

// RunScript 从 skill 目录执行单个脚本并返回其 stdout
func (r *ScriptRunner) RunScript(ctx context.Context, skillDir, scriptName string, args ...string) (string, error) {
	scriptPath, err := resolveScriptPath(skillDir, scriptName)
	if err != nil {
		return "", err
	}

	info, err := os.Stat(scriptPath)
	if err != nil {
		return "", errs.Wrap(errs.CodeSkillScriptFailed, fmt.Sprintf("script %q not found", scriptName), err)
	}
	if info.IsDir() {
		return "", errs.New(errs.CodeSkillScriptFailed, fmt.Sprintf("script %q is a directory", scriptName))
	}
	if err := verifyResolvedScriptPath(skillDir, scriptPath, scriptName); err != nil {
		return "", err
	}

	interpreter, err := detectInterpreter(scriptPath)
	if err != nil {
		return "", errs.Wrap(errs.CodeSkillScriptFailed, "detect interpreter", err)
	}

	r.logger.Debug("运行脚本",
		zap.String("script", scriptName),
		zap.String("interpreter", interpreter),
		zap.String("skill_dir", skillDir),
	)

	fullCmd := buildScriptCommand(interpreter, scriptPath, args)

	// 优先委托给 Executor（WorkDir=skillDir）
	if r.Executor == nil {
		return "", errs.New(errs.CodeSkillScriptFailed,
			fmt.Sprintf("沙箱执行器未初始化，无法执行脚本 %q", scriptName))
	}

	result, execErr := r.Executor.Execute(ctx, SandboxExecRequest{
		Command:   fullCmd,
		SessionID: "script-runner",
		Timeout:   r.timeout,
		WorkDir:   skillDir,
	})
	if execErr != nil {
		return "", errs.Wrap(errs.CodeSkillScriptFailed,
			fmt.Sprintf("script %q failed", scriptName), execErr)
	}
	output := result.Stdout
	if result.Stderr != "" {
		output += result.Stderr
	}
	if result.ExitCode != 0 {
		return output, errs.New(errs.CodeSkillScriptFailed,
			fmt.Sprintf("script %q exited with code %d", scriptName, result.ExitCode))
	}
	return strings.TrimRight(output, "\n"), nil
}

func resolveScriptPath(skillDir, scriptName string) (string, error) {
	if filepath.IsAbs(scriptName) {
		return "", errs.New(errs.CodeSkillScriptFailed, fmt.Sprintf("script %q must be relative to scripts/", scriptName))
	}
	scriptsDir := filepath.Clean(filepath.Join(skillDir, "scripts"))
	scriptPath := filepath.Clean(filepath.Join(scriptsDir, scriptName))
	rel, err := filepath.Rel(scriptsDir, scriptPath)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) || filepath.IsAbs(rel) {
		return "", errs.New(errs.CodeSkillScriptFailed, fmt.Sprintf("script %q escapes scripts directory", scriptName))
	}
	return scriptPath, nil
}

func verifyResolvedScriptPath(skillDir, scriptPath, scriptName string) error {
	scriptsDir := filepath.Clean(filepath.Join(skillDir, "scripts"))
	realScriptsDir, err := filepath.EvalSymlinks(scriptsDir)
	if err != nil {
		return errs.Wrap(errs.CodeSkillScriptFailed, fmt.Sprintf("resolve scripts directory for %q", scriptName), err)
	}
	realScriptPath, err := filepath.EvalSymlinks(scriptPath)
	if err != nil {
		return errs.Wrap(errs.CodeSkillScriptFailed, fmt.Sprintf("resolve script %q", scriptName), err)
	}
	rel, err := filepath.Rel(realScriptsDir, realScriptPath)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) || filepath.IsAbs(rel) {
		return errs.New(errs.CodeSkillScriptFailed, fmt.Sprintf("script %q resolves outside scripts directory", scriptName))
	}
	return nil
}

func buildScriptCommand(interpreter, scriptPath string, args []string) string {
	interpParts := strings.Fields(interpreter)
	cmdParts := make([]string, 0, len(interpParts)+1+len(args))
	cmdParts = append(cmdParts, interpParts...)
	cmdParts = append(cmdParts, scriptPath)
	cmdParts = append(cmdParts, args...)
	for i, part := range cmdParts {
		cmdParts[i] = shellQuote(part)
	}
	return strings.Join(cmdParts, " ")
}

func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// RunAllScripts 按顺序执行所有列出的脚本并返回脚本名称 → 输出的映射
func (r *ScriptRunner) RunAllScripts(ctx context.Context, skillDir string, scripts []string) (map[string]string, error) {
	results := make(map[string]string, len(scripts))
	for _, script := range scripts {
		output, err := r.RunScript(ctx, skillDir, script)
		if err != nil {
			return results, errs.Wrap(errs.CodeSkillExecFailed, fmt.Sprintf("script %s", script), err)
		}
		results[script] = output
	}
	return results, nil
}
