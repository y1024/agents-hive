package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/chef-guo/agents-hive/internal/config"
)

func ensureFileConvDependencies(ctx context.Context, cfg *config.Config, logger *zap.Logger) error {
	if cfg == nil {
		return nil
	}
	cfg.FileConv = config.NormalizeFileConvConfig(cfg.FileConv)
	pdfCfg := &cfg.FileConv.Markdown.PDF
	if strings.ToLower(strings.TrimSpace(pdfCfg.Provider)) != "mineru" {
		return nil
	}
	binary := strings.TrimSpace(pdfCfg.Command.Binary)
	if binary == "" {
		binary = "mineru"
	}
	if path, err := lookupMinerUBinary(binary, pdfCfg.Install.InstallDir); err == nil {
		pdfCfg.Command.Binary = path
		logger.Info("MinerU PDF provider 已就绪", zap.String("binary", path))
		return nil
	}
	if pdfCfg.Install.Enabled == nil || !*pdfCfg.Install.Enabled {
		return fmt.Errorf("fileconv: PDF provider 配置为 MinerU，但未找到 %q，且自动安装已关闭", binary)
	}
	if err := installMinerU(ctx, pdfCfg, logger); err != nil {
		return err
	}
	path, err := lookupMinerUBinary(binary, pdfCfg.Install.InstallDir)
	if err != nil {
		return fmt.Errorf("fileconv: MinerU 安装后仍未找到可执行文件 %q: %w", binary, err)
	}
	pdfCfg.Command.Binary = path
	logger.Info("MinerU PDF provider 安装完成", zap.String("binary", path))
	return nil
}

func lookupMinerUBinary(binary, installDir string) (string, error) {
	if strings.TrimSpace(binary) == "" {
		binary = "mineru"
	}
	if filepath.IsAbs(binary) {
		if isExecutableFile(binary) {
			return binary, nil
		}
		return "", os.ErrNotExist
	}
	candidates := minerUBinaryCandidates(binary, installDir)
	for _, candidate := range candidates {
		if isExecutableFile(candidate) {
			return candidate, nil
		}
	}
	return exec.LookPath(binary)
}

func minerUBinaryCandidates(binary, installDir string) []string {
	if strings.TrimSpace(installDir) == "" {
		return nil
	}
	binDir := filepath.Join(installDir, "bin")
	return []string{
		filepath.Join(binDir, binary),
		filepath.Join(installDir, binary),
	}
}

func isExecutableFile(path string) bool {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return false
	}
	return info.Mode()&0o111 != 0
}

func installMinerU(ctx context.Context, pdfCfg *config.PDFMarkdownConfig, logger *zap.Logger) error {
	installDir := strings.TrimSpace(pdfCfg.Install.InstallDir)
	if installDir == "" {
		return errors.New("fileconv: MinerU 自动安装目录为空")
	}
	if err := os.MkdirAll(installDir, 0o755); err != nil {
		return fmt.Errorf("fileconv: 创建 MinerU 安装目录失败: %w", err)
	}
	binary := strings.TrimSpace(pdfCfg.Install.Command.Binary)
	if binary == "" {
		return errors.New("fileconv: MinerU 自动安装命令为空")
	}
	args := expandInstallArgs(pdfCfg.Install.Command.Args, installDir)
	if len(args) == 0 {
		return errors.New("fileconv: MinerU 自动安装参数为空")
	}
	timeout := pdfCfg.Install.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Minute
	}
	cmdCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if binary == "builtin:python-venv-pip" {
		return installMinerUWithPythonVenv(cmdCtx, installDir, args, logger)
	}
	logger.Warn("MinerU 未安装，开始按配置执行自动安装",
		zap.String("binary", binary),
		zap.Strings("args", args),
		zap.String("install_dir", installDir),
	)
	cmd := exec.CommandContext(cmdCtx, binary, args...)
	cmd.Env = append(os.Environ(), "PATH="+minerUInstallPath(installDir, os.Getenv("PATH")))
	output, err := cmd.CombinedOutput()
	if err != nil {
		if errors.Is(cmdCtx.Err(), context.DeadlineExceeded) {
			return fmt.Errorf("fileconv: MinerU 自动安装超时: %w", cmdCtx.Err())
		}
		return fmt.Errorf("fileconv: MinerU 自动安装失败: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func installMinerUWithPythonVenv(ctx context.Context, installDir string, packages []string, logger *zap.Logger) error {
	python, err := lookupPythonBinary()
	if err != nil {
		return fmt.Errorf("fileconv: MinerU 自动安装需要 python3/python: %w", err)
	}
	logger.Warn("MinerU 未安装，开始创建隔离 Python venv 并安装",
		zap.String("python", python),
		zap.String("install_dir", installDir),
		zap.Strings("packages", packages),
	)
	if err := createPythonVenv(ctx, installDir, python); err != nil {
		return fmt.Errorf("fileconv: 创建 MinerU Python venv 失败: %w", err)
	}
	pip := filepath.Join(installDir, "bin", "pip")
	if !isExecutableFile(pip) {
		return fmt.Errorf("fileconv: MinerU Python venv 缺少 pip: %s", pip)
	}
	args := append([]string{"install", "-U"}, packages...)
	if err := runInstallCommand(ctx, installDir, pip, args); err != nil {
		return fmt.Errorf("fileconv: 安装 MinerU Python 包失败: %w", err)
	}
	return nil
}

func lookupPythonBinary() (string, error) {
	if path, err := exec.LookPath("python3"); err == nil {
		return path, nil
	}
	return exec.LookPath("python")
}

func createPythonVenv(ctx context.Context, installDir, python string) error {
	if err := runInstallCommand(ctx, installDir, python, []string{"-m", "venv", installDir}); err == nil {
		return nil
	} else {
		venvErr := err
		if err := runInstallCommand(ctx, installDir, python, []string{"-m", "virtualenv", installDir}); err == nil {
			return nil
		} else {
			return fmt.Errorf("python -m venv failed: %v; python -m virtualenv failed: %w", venvErr, err)
		}
	}
}

func runInstallCommand(ctx context.Context, installDir, binary string, args []string) error {
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Env = append(os.Environ(), "PATH="+minerUInstallPath(installDir, os.Getenv("PATH")))
	output, err := cmd.CombinedOutput()
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return fmt.Errorf("超时: %w", ctx.Err())
		}
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func expandInstallArgs(args []string, installDir string) []string {
	out := make([]string, 0, len(args))
	for _, arg := range args {
		arg = strings.ReplaceAll(arg, "{install_dir}", installDir)
		out = append(out, arg)
	}
	return out
}

func minerUInstallPath(installDir, existing string) string {
	values := []string{
		filepath.Join(installDir, "bin"),
		installDir,
	}
	if existing != "" {
		values = append(values, existing)
	}
	return strings.Join(values, string(os.PathListSeparator))
}
