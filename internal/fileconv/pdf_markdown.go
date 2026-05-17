package fileconv

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"mime"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/chef-guo/agents-hive/internal/errs"
)

const (
	defaultMinerUBinary = "mineru"
	pdfHeaderProbeSize  = 1024
)

type PDFMarkdownProviderConfig struct {
	Provider string
	Command  ExternalMarkdownCommandConfig
	Timeout  time.Duration
}

type ExternalMarkdownCommandConfig struct {
	Name         string
	Binary       string
	Args         []string
	MarkdownPath string
	AssetDir     string
}

type ExternalMarkdownCommandProvider struct {
	name         string
	binary       string
	args         []string
	markdownPath string
	assetDir     string
	timeout      time.Duration
}

func NewPDFMarkdownProvider(cfg PDFMarkdownProviderConfig) MarkdownProvider {
	provider := strings.ToLower(strings.TrimSpace(cfg.Provider))
	if provider == "" {
		provider = "mineru"
	}
	if provider == "none" || provider == "disabled" || provider == "off" {
		return nil
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = defaultPDFMarkdownTimeout
	}
	switch provider {
	case "mineru":
		cmdCfg := cfg.Command
		if strings.TrimSpace(cmdCfg.Name) == "" {
			cmdCfg.Name = "mineru"
		}
		if strings.TrimSpace(cmdCfg.Binary) == "" {
			cmdCfg.Binary = defaultMinerUBinary
		}
		if len(cmdCfg.Args) == 0 {
			cmdCfg.Args = []string{"-p", "{input}", "-o", "{output}"}
		}
		return NewExternalMarkdownCommandProvider(cmdCfg, timeout)
	case "external":
		return NewExternalMarkdownCommandProvider(cfg.Command, timeout)
	default:
		return &unavailablePDFProvider{provider: provider, reason: "unsupported PDF markdown provider"}
	}
}

func NewPDFMarkdownProviderFromEnv() MarkdownProvider {
	provider := strings.TrimSpace(os.Getenv("FILECONV_PDF_PROVIDER"))
	if provider == "" {
		provider = strings.TrimSpace(os.Getenv("KB_PDF_PROVIDER"))
	}
	if provider == "" {
		provider = "mineru"
	}
	timeout := defaultPDFMarkdownTimeout
	if raw := strings.TrimSpace(os.Getenv("FILECONV_PDF_TIMEOUT_SECONDS")); raw != "" {
		if seconds, err := strconv.Atoi(raw); err == nil && seconds > 0 {
			timeout = time.Duration(seconds) * time.Second
		}
	}
	cfg := ExternalMarkdownCommandConfig{
		Binary:       firstNonEmpty(os.Getenv("MINERU_BIN"), os.Getenv("FILECONV_PDF_BIN")),
		Args:         parseCommandArgs(os.Getenv("MINERU_ARGS"), os.Getenv("FILECONV_PDF_ARGS")),
		MarkdownPath: firstNonEmpty(os.Getenv("MINERU_MARKDOWN_PATH"), os.Getenv("FILECONV_PDF_MARKDOWN_PATH")),
		AssetDir:     firstNonEmpty(os.Getenv("MINERU_ASSET_DIR"), os.Getenv("FILECONV_PDF_ASSET_DIR")),
	}
	return NewPDFMarkdownProvider(PDFMarkdownProviderConfig{
		Provider: provider,
		Command:  cfg,
		Timeout:  timeout,
	})
}

func NewExternalMarkdownCommandProvider(cfg ExternalMarkdownCommandConfig, timeout time.Duration) MarkdownProvider {
	name := strings.TrimSpace(cfg.Name)
	if name == "" {
		name = "external-pdf"
	}
	binary := strings.TrimSpace(cfg.Binary)
	if binary == "" {
		return &unavailablePDFProvider{provider: name, reason: "missing PDF markdown command binary"}
	}
	if len(cfg.Args) == 0 {
		return &unavailablePDFProvider{provider: name, reason: "missing PDF markdown command args"}
	}
	if timeout <= 0 {
		timeout = defaultPDFMarkdownTimeout
	}
	return &ExternalMarkdownCommandProvider{
		name:         name,
		binary:       binary,
		args:         cfg.Args,
		markdownPath: strings.TrimSpace(cfg.MarkdownPath),
		assetDir:     strings.TrimSpace(cfg.AssetDir),
		timeout:      timeout,
	}
}

func (p *ExternalMarkdownCommandProvider) Name() string {
	if p == nil || p.name == "" {
		return "external-pdf"
	}
	return p.name
}

func (p *ExternalMarkdownCommandProvider) Supports(filename, mimeType string) bool {
	return isPDFInput(filename, mimeType)
}

func (p *ExternalMarkdownCommandProvider) ConvertToMarkdown(ctx context.Context, input MarkdownInput) (*MarkdownDocument, error) {
	if p == nil {
		return nil, ErrMarkdownProviderUnavailable
	}
	if !looksLikePDF(input.Data) {
		return nil, errs.New(errs.CodeInvalidInput, "PDF 文件头无效")
	}
	binaryPath, err := exec.LookPath(p.binary)
	if err != nil {
		return nil, fmt.Errorf("%w: %s binary not found: %s", ErrMarkdownProviderUnavailable, p.Name(), p.binary)
	}
	workDir, err := os.MkdirTemp("", "fileconv-pdf-*")
	if err != nil {
		return nil, errs.Wrap(errs.CodeInternal, "创建 PDF 转换临时目录失败", err)
	}
	defer os.RemoveAll(workDir)

	inputPath := filepath.Join(workDir, sanitizeBaseFilename(input.Filename, ".pdf"))
	outputDir := filepath.Join(workDir, "out")
	if err := os.MkdirAll(outputDir, 0o700); err != nil {
		return nil, errs.Wrap(errs.CodeInternal, "创建 PDF 转换输出目录失败", err)
	}
	if err := os.WriteFile(inputPath, input.Data, 0o600); err != nil {
		return nil, errs.Wrap(errs.CodeInternal, "写入 PDF 临时文件失败", err)
	}

	args := expandCommandArgs(p.args, inputPath, outputDir)
	cmdCtx := ctx
	cancel := func() {}
	if p.timeout > 0 {
		cmdCtx, cancel = context.WithTimeout(ctx, p.timeout)
	}
	defer cancel()
	cmd := exec.CommandContext(cmdCtx, binaryPath, args...)
	cmd.Dir = workDir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if errors.Is(cmdCtx.Err(), context.DeadlineExceeded) {
			return nil, errs.Wrap(errs.CodeTimeout, "PDF Markdown provider 执行超时", cmdCtx.Err())
		}
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = err.Error()
		}
		return nil, errs.New(errs.CodeInternal, "PDF Markdown provider 执行失败: "+detail)
	}

	mdPath, err := p.resolveMarkdownPath(outputDir)
	if err != nil {
		return nil, err
	}
	contentBytes, err := os.ReadFile(mdPath)
	if err != nil {
		return nil, errs.Wrap(errs.CodeInternal, "读取 PDF 转换 Markdown 失败", err)
	}
	content := strings.TrimSpace(string(contentBytes))
	if content == "" {
		return nil, errs.New(errs.CodeInvalidInput, "PDF Markdown provider 未产出有效 Markdown")
	}
	assets, err := p.collectAssets(outputDir, mdPath)
	if err != nil {
		return nil, err
	}
	title := markdownTitle(input.Filename)
	return &MarkdownDocument{
		Title:    title,
		Content:  ensureMarkdownHeading(title, content),
		Assets:   assets,
		Quality:  ConversionQualityExact,
		Provider: p.Name(),
	}, nil
}

func (p *ExternalMarkdownCommandProvider) resolveMarkdownPath(outputDir string) (string, error) {
	if p.markdownPath != "" {
		path := expandPathTemplate(p.markdownPath, "", outputDir)
		if !filepath.IsAbs(path) {
			path = filepath.Join(outputDir, path)
		}
		if _, err := os.Stat(path); err == nil {
			return path, nil
		}
		return "", errs.New(errs.CodeInternal, "PDF Markdown provider 未产出配置的 Markdown 文件: "+p.markdownPath)
	}
	var mdFiles []string
	if err := filepath.WalkDir(outputDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(d.Name()))
		if ext == ".md" || ext == ".markdown" {
			mdFiles = append(mdFiles, path)
		}
		return nil
	}); err != nil {
		return "", errs.Wrap(errs.CodeInternal, "扫描 PDF 转换输出目录失败", err)
	}
	if len(mdFiles) == 0 {
		return "", errs.New(errs.CodeInternal, "PDF Markdown provider 未产出 Markdown 文件")
	}
	sort.Slice(mdFiles, func(i, j int) bool {
		di, _ := os.Stat(mdFiles[i])
		dj, _ := os.Stat(mdFiles[j])
		if di != nil && dj != nil && di.Size() != dj.Size() {
			return di.Size() > dj.Size()
		}
		return mdFiles[i] < mdFiles[j]
	})
	return mdFiles[0], nil
}

func (p *ExternalMarkdownCommandProvider) collectAssets(outputDir, mdPath string) ([]ExtractedAsset, error) {
	assetRoot := outputDir
	if p.assetDir != "" {
		assetRoot = expandPathTemplate(p.assetDir, "", outputDir)
		if !filepath.IsAbs(assetRoot) {
			assetRoot = filepath.Join(outputDir, assetRoot)
		}
	}
	if _, err := os.Stat(assetRoot); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, errs.Wrap(errs.CodeInternal, "读取 PDF 转换资产目录失败", err)
	}
	mdAbs, _ := filepath.Abs(mdPath)
	var assets []ExtractedAsset
	err := filepath.WalkDir(assetRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		abs, _ := filepath.Abs(path)
		if abs == mdAbs {
			return nil
		}
		mimeType := mime.TypeByExtension(filepath.Ext(path))
		if !isMarkdownAssetMime(mimeType, path) {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		relToOutput, err := filepath.Rel(outputDir, path)
		if err != nil {
			relToOutput = filepath.Base(path)
		}
		relToMarkdown, err := filepath.Rel(filepath.Dir(mdPath), path)
		if err != nil {
			relToMarkdown = relToOutput
		}
		if mimeType == "" {
			mimeType = "application/octet-stream"
		}
		assets = append(assets, ExtractedAsset{
			Path:     filepath.ToSlash(relToMarkdown),
			Filename: filepath.Base(path),
			MimeType: mimeType,
			Data:     data,
		})
		return nil
	})
	if err != nil {
		return nil, errs.Wrap(errs.CodeInternal, "读取 PDF 转换资产失败", err)
	}
	sort.Slice(assets, func(i, j int) bool {
		return assets[i].Path < assets[j].Path
	})
	return assets, nil
}

type unavailablePDFProvider struct {
	provider string
	reason   string
}

func (p *unavailablePDFProvider) Name() string {
	if p == nil || strings.TrimSpace(p.provider) == "" {
		return "pdf-unavailable"
	}
	return strings.TrimSpace(p.provider)
}

func (p *unavailablePDFProvider) Supports(filename, mimeType string) bool {
	return isPDFInput(filename, mimeType)
}

func (p *unavailablePDFProvider) ConvertToMarkdown(context.Context, MarkdownInput) (*MarkdownDocument, error) {
	reason := "PDF markdown provider unavailable"
	if p != nil && strings.TrimSpace(p.reason) != "" {
		reason = strings.TrimSpace(p.reason)
	}
	return nil, fmt.Errorf("%w: %s", ErrMarkdownProviderUnavailable, reason)
}

func isPDFInput(filename, mimeType string) bool {
	return strings.EqualFold(strings.TrimSpace(mimeType), "application/pdf") || strings.EqualFold(filepath.Ext(filename), ".pdf")
}

func looksLikePDF(data []byte) bool {
	probe := len(data)
	if probe > pdfHeaderProbeSize {
		probe = pdfHeaderProbeSize
	}
	return bytes.Contains(data[:probe], []byte("%PDF-"))
}

func expandCommandArgs(args []string, inputPath, outputDir string) []string {
	out := make([]string, 0, len(args))
	for _, arg := range args {
		out = append(out, expandPathTemplate(arg, inputPath, outputDir))
	}
	return out
}

func expandPathTemplate(value, inputPath, outputDir string) string {
	replacements := map[string]string{
		"{input}":  inputPath,
		"{output}": outputDir,
		"{out}":    outputDir,
	}
	for key, replacement := range replacements {
		value = strings.ReplaceAll(value, key, replacement)
	}
	return value
}

func sanitizeBaseFilename(filename, fallbackExt string) string {
	base := filepath.Base(strings.TrimSpace(filename))
	if base == "." || base == "/" || base == "" {
		base = "input" + fallbackExt
	}
	base = strings.ReplaceAll(base, string(filepath.Separator), "_")
	if filepath.Ext(base) == "" && fallbackExt != "" {
		base += fallbackExt
	}
	return base
}

func parseCommandArgs(values ...string) []string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		return strings.Fields(value)
	}
	return nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func isMarkdownAssetMime(mimeType, path string) bool {
	if strings.HasPrefix(mimeType, "image/") {
		return true
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".bmp", ".svg":
		return true
	default:
		return false
	}
}
