package api

import (
	"github.com/chef-guo/agents-hive/internal/config"
	"github.com/chef-guo/agents-hive/internal/fileconv"
)

func markdownRegistryFromConfig(cfg config.FileConvConfig) *fileconv.MarkdownRegistry {
	cfg = config.NormalizeFileConvConfig(cfg)
	pdfCfg := cfg.Markdown.PDF
	pdfProvider := fileconv.NewPDFMarkdownProvider(fileconv.PDFMarkdownProviderConfig{
		Provider: pdfCfg.Provider,
		Timeout:  pdfCfg.Timeout,
		Command: fileconv.ExternalMarkdownCommandConfig{
			Name:         pdfCfg.Command.Name,
			Binary:       pdfCfg.Command.Binary,
			Args:         append([]string(nil), pdfCfg.Command.Args...),
			MarkdownPath: pdfCfg.Command.MarkdownPath,
			AssetDir:     pdfCfg.Command.AssetDir,
		},
	})
	return fileconv.NewPDFMarkdownRegistry(pdfProvider)
}
