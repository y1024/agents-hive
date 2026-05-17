package fileconv

import (
	"context"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestExternalMarkdownCommandProviderCollectsMarkdownAndAssets(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell command syntax is unix-specific")
	}
	provider := NewExternalMarkdownCommandProvider(ExternalMarkdownCommandConfig{
		Name:   "fake-external",
		Binary: "sh",
		Args: []string{
			"-c",
			"mkdir -p {output}/images && printf '# From PDF\n\n![图](images/a.png)\n' > {output}/doc.md && printf png > {output}/images/a.png",
		},
	}, 0)

	doc, err := provider.ConvertToMarkdown(context.Background(), MarkdownInput{
		Filename: "manual.pdf",
		MimeType: "application/pdf",
		Data:     []byte("%PDF-1.7 fake"),
	})
	if err != nil {
		t.Fatalf("ConvertToMarkdown() error = %v", err)
	}
	if doc.Provider != "fake-external" {
		t.Fatalf("provider = %q, want fake-external", doc.Provider)
	}
	if !strings.Contains(doc.Content, "# From PDF") {
		t.Fatalf("content = %q", doc.Content)
	}
	if len(doc.Assets) != 1 {
		t.Fatalf("assets = %#v, want 1", doc.Assets)
	}
	if doc.Assets[0].Path != "images/a.png" || doc.Assets[0].Filename != "a.png" {
		t.Fatalf("asset = %#v", doc.Assets[0])
	}
}

func TestExternalMarkdownCommandProviderHonorsConfiguredMarkdownAndAssetDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell command syntax is unix-specific")
	}
	provider := NewExternalMarkdownCommandProvider(ExternalMarkdownCommandConfig{
		Name:         "fake-external",
		Binary:       "sh",
		Args:         []string{"-c", "mkdir -p {output}/media && printf '# Custom\n' > {output}/result.md && printf jpg > {output}/media/a.jpg"},
		MarkdownPath: "result.md",
		AssetDir:     "media",
	}, 0)

	doc, err := provider.ConvertToMarkdown(context.Background(), MarkdownInput{
		Filename: "manual.pdf",
		MimeType: "application/pdf",
		Data:     []byte("%PDF-1.7 fake"),
	})
	if err != nil {
		t.Fatalf("ConvertToMarkdown() error = %v", err)
	}
	if strings.TrimSpace(doc.Content) != "# Custom" {
		t.Fatalf("content = %q", doc.Content)
	}
	if len(doc.Assets) != 1 || doc.Assets[0].Path != "media/a.jpg" {
		t.Fatalf("assets = %#v", doc.Assets)
	}
}

func TestExternalMarkdownCommandProviderReportsUnavailableBinary(t *testing.T) {
	provider := NewExternalMarkdownCommandProvider(ExternalMarkdownCommandConfig{
		Name:   "missing",
		Binary: filepath.Join(t.TempDir(), "missing-binary"),
		Args:   []string{"-p", "{input}", "-o", "{output}"},
	}, 0)
	_, err := provider.ConvertToMarkdown(context.Background(), MarkdownInput{
		Filename: "manual.pdf",
		MimeType: "application/pdf",
		Data:     []byte("%PDF-1.7 fake"),
	})
	if err == nil || !strings.Contains(err.Error(), "markdown provider unavailable") {
		t.Fatalf("err = %v, want unavailable", err)
	}
}

func TestNewPDFMarkdownProviderFromEnvCanDisablePDF(t *testing.T) {
	t.Setenv("FILECONV_PDF_PROVIDER", "none")
	if provider := NewPDFMarkdownProviderFromEnv(); provider != nil {
		t.Fatalf("provider = %#v, want nil", provider)
	}
}
