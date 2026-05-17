package fileconv

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
)

func TestConvertToMarkdownPreservesMarkdown(t *testing.T) {
	doc, err := ConvertToMarkdown(context.Background(), "faq.md", "text/markdown", base64.StdEncoding.EncodeToString([]byte("# FAQ\n\nhello")))
	if err != nil {
		t.Fatalf("ConvertToMarkdown() error = %v", err)
	}
	if doc.Quality != ConversionQualityExact {
		t.Fatalf("quality = %q, want exact", doc.Quality)
	}
	if doc.Content != "# FAQ\n\nhello" {
		t.Fatalf("content = %q", doc.Content)
	}
}

func TestConvertToMarkdownDOCXUsesExistingExtractor(t *testing.T) {
	doc, err := ConvertToMarkdown(context.Background(), "policy.docx", "application/vnd.openxmlformats-officedocument.wordprocessingml.document", buildMinimalDOCX(t, "退货政策"))
	if err != nil {
		t.Fatalf("ConvertToMarkdown() DOCX error = %v", err)
	}
	if doc.Title != "policy" {
		t.Fatalf("title = %q, want policy", doc.Title)
	}
	if !strings.Contains(doc.Content, "# policy") || !strings.Contains(doc.Content, "退货政策") {
		t.Fatalf("content = %q", doc.Content)
	}
}

func TestConvertToMarkdownDOCXExtractsImagesAsAssets(t *testing.T) {
	docx := buildZip(t, map[string][]byte{
		"word/document.xml":     []byte(`<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:r><w:t>图文政策</w:t></w:r></w:p></w:body></w:document>`),
		"word/media/image1.png": []byte("png-data"),
	})
	doc, err := ConvertToMarkdown(context.Background(), "policy.docx", "application/vnd.openxmlformats-officedocument.wordprocessingml.document", docx)
	if err != nil {
		t.Fatalf("ConvertToMarkdown() DOCX error = %v", err)
	}
	if !strings.Contains(doc.Content, "![image1](word/media/image1.png)") {
		t.Fatalf("content = %q, want markdown image reference", doc.Content)
	}
	if len(doc.Assets) != 1 {
		t.Fatalf("assets = %#v, want 1", doc.Assets)
	}
	if doc.Assets[0].Path != "word/media/image1.png" || doc.Assets[0].MimeType != "image/png" || string(doc.Assets[0].Data) != "png-data" {
		t.Fatalf("asset = %#v", doc.Assets[0])
	}
}

func TestConvertToMarkdownPDFRequiresProvider(t *testing.T) {
	_, err := ConvertToMarkdown(
		context.Background(),
		"manual.pdf",
		"application/pdf",
		base64.StdEncoding.EncodeToString([]byte("%PDF-1.7 mock")),
		WithMarkdownRegistry(NewMarkdownRegistryWithoutPDF()),
	)
	if !errors.Is(err, ErrMarkdownProviderUnavailable) {
		t.Fatalf("err = %v, want ErrMarkdownProviderUnavailable", err)
	}
}

func TestConvertToMarkdownPDFUsesConfiguredProvider(t *testing.T) {
	provider := fakePDFMarkdownProvider{
		doc: &MarkdownDocument{
			Title:   "Manual",
			Content: "# Manual\n\n![diagram](images/a.png)\n",
			Quality: ConversionQualityExact,
			Assets: []ExtractedAsset{{
				Path:     "images/a.png",
				Filename: "a.png",
				MimeType: "image/png",
				Data:     []byte("png"),
			}},
		},
	}
	doc, err := ConvertToMarkdown(
		context.Background(),
		"manual.pdf",
		"application/pdf",
		base64.StdEncoding.EncodeToString([]byte("%PDF-1.7 mock")),
		WithMarkdownProviders(provider),
	)
	if err != nil {
		t.Fatalf("ConvertToMarkdown() PDF error = %v", err)
	}
	if doc.Provider != "fake-pdf" {
		t.Fatalf("provider = %q, want fake-pdf", doc.Provider)
	}
	if doc.Quality != ConversionQualityExact {
		t.Fatalf("quality = %q, want exact", doc.Quality)
	}
	if len(doc.Assets) != 1 || doc.Assets[0].Path != "images/a.png" {
		t.Fatalf("assets = %#v", doc.Assets)
	}
}

func TestPDFProviderRejectsInvalidPDFHeader(t *testing.T) {
	provider := NewExternalMarkdownCommandProvider(ExternalMarkdownCommandConfig{
		Name:   "fake-external",
		Binary: "sh",
		Args:   []string{"-c", "printf '# ok' > {output}/out.md"},
	}, 0)
	_, err := provider.ConvertToMarkdown(context.Background(), MarkdownInput{
		Filename: "bad.pdf",
		MimeType: "application/pdf",
		Data:     []byte("not-pdf"),
	})
	if err == nil {
		t.Fatal("expected invalid PDF error")
	}
}

type fakePDFMarkdownProvider struct {
	doc *MarkdownDocument
}

func (f fakePDFMarkdownProvider) Name() string { return "fake-pdf" }

func (f fakePDFMarkdownProvider) Supports(filename, mimeType string) bool {
	return isPDFInput(filename, mimeType)
}

func (f fakePDFMarkdownProvider) ConvertToMarkdown(context.Context, MarkdownInput) (*MarkdownDocument, error) {
	return f.doc, nil
}
