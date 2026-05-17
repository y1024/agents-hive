package fileconv

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/chef-guo/agents-hive/internal/errs"
)

var ErrMarkdownProviderUnavailable = errors.New("fileconv: markdown provider unavailable")

const defaultPDFMarkdownTimeout = 5 * time.Minute

type ConversionQuality string

const (
	ConversionQualityExact    ConversionQuality = "exact"
	ConversionQualityDegraded ConversionQuality = "degraded"
)

type MarkdownDocument struct {
	Title    string
	Content  string
	Assets   []ExtractedAsset
	Quality  ConversionQuality
	Provider string
	Warnings []string
}

type ExtractedAsset struct {
	Path     string
	Filename string
	MimeType string
	Data     []byte
	AltText  string
	Caption  string
}

type MarkdownInput struct {
	Filename string
	MimeType string
	Data     []byte
}

type MarkdownProvider interface {
	Name() string
	Supports(filename, mimeType string) bool
	ConvertToMarkdown(ctx context.Context, input MarkdownInput) (*MarkdownDocument, error)
}

type MarkdownRegistry struct {
	providers []MarkdownProvider
}

type MarkdownOption func(*markdownConvertOptions)

type markdownConvertOptions struct {
	registry *MarkdownRegistry
}

func NewMarkdownRegistry(providers ...MarkdownProvider) *MarkdownRegistry {
	return &MarkdownRegistry{providers: providers}
}

func DefaultMarkdownRegistry() *MarkdownRegistry {
	return DefaultMarkdownRegistryWithPDFProvider(NewPDFMarkdownProviderFromEnv())
}

func DefaultMarkdownRegistryWithPDFProvider(pdfProvider MarkdownProvider) *MarkdownRegistry {
	providers := []MarkdownProvider{
		plainMarkdownProvider{},
		textMarkdownProvider{},
		docxMarkdownProvider{},
	}
	if pdfProvider != nil {
		providers = append(providers, pdfProvider)
	}
	return NewMarkdownRegistry(providers...)
}

func BuiltinMarkdownProviders() []MarkdownProvider {
	return []MarkdownProvider{
		plainMarkdownProvider{},
		textMarkdownProvider{},
		docxMarkdownProvider{},
	}
}

func NewPDFMarkdownRegistry(pdfProvider MarkdownProvider) *MarkdownRegistry {
	providers := BuiltinMarkdownProviders()
	if pdfProvider != nil {
		providers = append(providers, pdfProvider)
	}
	return NewMarkdownRegistry(providers...)
}

func NewMarkdownRegistryWithoutPDF() *MarkdownRegistry {
	return NewMarkdownRegistry(
		plainMarkdownProvider{},
		textMarkdownProvider{},
		docxMarkdownProvider{},
	)
}

func WithMarkdownRegistry(registry *MarkdownRegistry) MarkdownOption {
	return func(opts *markdownConvertOptions) {
		opts.registry = registry
	}
}

func WithMarkdownProviders(providers ...MarkdownProvider) MarkdownOption {
	return WithMarkdownRegistry(NewMarkdownRegistry(providers...))
}

func ConvertToMarkdown(ctx context.Context, filename, mimeType, base64Data string, opts ...MarkdownOption) (*MarkdownDocument, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if filename == "" {
		return nil, errs.New(errs.CodeInvalidInput, "文件名不能为空")
	}
	if mimeType == "" {
		return nil, errs.New(errs.CodeInvalidInput, "MIME 类型不能为空")
	}
	if base64Data == "" {
		return nil, errs.New(errs.CodeInvalidInput, "文件数据不能为空")
	}
	if len(base64Data) > maxConvertSize {
		return nil, errs.New(errs.CodeInvalidInput,
			fmt.Sprintf("文件过大: %d 字节，超过 %d 字节限制", len(base64Data), maxConvertSize))
	}
	data, err := base64.StdEncoding.DecodeString(base64Data)
	if err != nil {
		return nil, errs.Wrap(errs.CodeInvalidInput, "文件数据 base64 解码失败", err)
	}

	options := markdownConvertOptions{registry: DefaultMarkdownRegistry()}
	for _, opt := range opts {
		opt(&options)
	}
	if options.registry == nil {
		options.registry = DefaultMarkdownRegistry()
	}
	return options.registry.Convert(ctx, MarkdownInput{
		Filename: filename,
		MimeType: mimeType,
		Data:     data,
	})
}

func (r *MarkdownRegistry) Convert(ctx context.Context, input MarkdownInput) (*MarkdownDocument, error) {
	if r == nil {
		return nil, ErrMarkdownProviderUnavailable
	}
	for _, provider := range r.providers {
		if provider == nil || !provider.Supports(input.Filename, input.MimeType) {
			continue
		}
		doc, err := provider.ConvertToMarkdown(ctx, input)
		if err != nil {
			return nil, err
		}
		if doc.Provider == "" {
			doc.Provider = provider.Name()
		}
		return doc, nil
	}
	if input.MimeType == "application/pdf" || strings.EqualFold(filepath.Ext(input.Filename), ".pdf") {
		return nil, fmt.Errorf("%w: no PDF markdown provider configured", ErrMarkdownProviderUnavailable)
	}
	return nil, errs.New(errs.CodeInvalidInput, "不支持的 Markdown 转换文件类型: "+input.MimeType)
}

type plainMarkdownProvider struct{}

func (plainMarkdownProvider) Name() string { return "builtin-markdown" }

func (plainMarkdownProvider) Supports(filename, mimeType string) bool {
	return mimeType == "text/markdown" || strings.EqualFold(filepath.Ext(filename), ".md") || strings.EqualFold(filepath.Ext(filename), ".markdown")
}

func (plainMarkdownProvider) ConvertToMarkdown(ctx context.Context, input MarkdownInput) (*MarkdownDocument, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	title := markdownTitle(input.Filename)
	return &MarkdownDocument{Title: title, Content: string(input.Data), Quality: ConversionQualityExact}, nil
}

type textMarkdownProvider struct{}

func (textMarkdownProvider) Name() string { return "builtin-text" }

func (textMarkdownProvider) Supports(_ string, mimeType string) bool {
	return strings.HasPrefix(mimeType, "text/")
}

func (textMarkdownProvider) ConvertToMarkdown(ctx context.Context, input MarkdownInput) (*MarkdownDocument, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	title := markdownTitle(input.Filename)
	return &MarkdownDocument{Title: title, Content: "# " + title + "\n\n" + string(input.Data), Quality: ConversionQualityExact}, nil
}

type docxMarkdownProvider struct{}

func (docxMarkdownProvider) Name() string { return "builtin-docx" }

func (docxMarkdownProvider) Supports(_ string, mimeType string) bool {
	return mimeType == "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
}

func (docxMarkdownProvider) ConvertToMarkdown(ctx context.Context, input MarkdownInput) (*MarkdownDocument, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return extractDOCXMarkdown(input.Filename, input.Data)
}

func paragraphsToMarkdown(title string, paragraphs []string) string {
	var b strings.Builder
	b.WriteString("# ")
	b.WriteString(strings.TrimSpace(title))
	b.WriteString("\n\n")
	for _, paragraph := range paragraphs {
		paragraph = strings.TrimSpace(paragraph)
		if paragraph == "" {
			continue
		}
		b.WriteString(paragraph)
		b.WriteString("\n\n")
	}
	return strings.TrimSpace(b.String()) + "\n"
}

func markdownTitle(filename string) string {
	return strings.TrimSuffix(filepath.Base(filename), filepath.Ext(filename))
}

func ensureMarkdownHeading(title, content string) string {
	if containsHeading(content) {
		return content
	}
	return "# " + strings.TrimSpace(title) + "\n\n" + strings.TrimSpace(content) + "\n"
}

func containsHeading(content string) bool {
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "# ") || strings.HasPrefix(line, "## ") || strings.HasPrefix(line, "### ") ||
			strings.HasPrefix(line, "#### ") || strings.HasPrefix(line, "##### ") || strings.HasPrefix(line, "###### ") {
			return true
		}
	}
	return false
}
