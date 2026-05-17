package fileconv

import (
	"archive/zip"
	"bytes"
	"encoding/base64"
	"encoding/xml"
	"fmt"
	"io"
	"mime"
	"path/filepath"
	"sort"
	"strings"

	"github.com/chef-guo/agents-hive/internal/errs"
)

// --- DOCX XML 结构 ---

// docxDocument 表示 word/document.xml 的根元素
type docxDocument struct {
	Body docxBody `xml:"body"`
}

// docxBody 文档正文
type docxBody struct {
	Paragraphs []docxParagraph `xml:"p"`
}

// docxParagraph 段落元素
type docxParagraph struct {
	Runs []docxRun `xml:"r"`
}

// docxRun 文本运行
type docxRun struct {
	Text []docxText `xml:"t"`
}

// docxText 文本节点
type docxText struct {
	Value string `xml:",chardata"`
}

// --- PPTX XML 结构 ---

// pptxSlide 表示 ppt/slides/slideN.xml
type pptxSlide struct {
	CSld pptxCSld `xml:"cSld"`
}

// pptxCSld 幻灯片内容
type pptxCSld struct {
	SpTree pptxSpTree `xml:"spTree"`
}

// pptxSpTree 形状树
type pptxSpTree struct {
	Shapes []pptxShape `xml:"sp"`
}

// pptxShape 形状
type pptxShape struct {
	TxBody *pptxTxBody `xml:"txBody"`
}

// pptxTxBody 文本框
type pptxTxBody struct {
	Paragraphs []pptxParagraph `xml:"p"`
}

// pptxParagraph 段落
type pptxParagraph struct {
	Runs []pptxRun `xml:"r"`
}

// pptxRun 文本运行
type pptxRun struct {
	Text string `xml:"t"`
}

// --- XLSX XML 结构 ---

// xlsxSST 共享字符串表
type xlsxSST struct {
	SI []xlsxSI `xml:"si"`
}

// xlsxSI 共享字符串项
type xlsxSI struct {
	T string  `xml:"t"`
	R []xlsxR `xml:"r"` // 富文本运行
}

// xlsxR 富文本运行
type xlsxR struct {
	T string `xml:"t"`
}

// xlsxWorksheet 工作表
type xlsxWorksheet struct {
	SheetData xlsxSheetData `xml:"sheetData"`
}

// xlsxSheetData 数据区域
type xlsxSheetData struct {
	Rows []xlsxRow `xml:"row"`
}

// xlsxRow 行
type xlsxRow struct {
	Cells []xlsxCell `xml:"c"`
}

// xlsxCell 单元格
type xlsxCell struct {
	Type  string `xml:"t,attr"` // "s" 表示共享字符串
	Value string `xml:"v"`
}

// extractDOCX 从 DOCX 文件中提取文本
func extractDOCX(filename, base64Data string) (string, error) {
	paragraphs, err := extractDOCXParagraphText(filename, base64Data)
	if err != nil {
		return "", err
	}
	content := strings.Join(paragraphs, "\n")
	return fmt.Sprintf("--- %s ---\n%s", filename, content), nil
}

func extractDOCXParagraphText(filename, base64Data string) ([]string, error) {
	zr, err := openZipFromBase64(base64Data)
	if err != nil {
		return nil, errs.Wrap(errs.CodeInvalidInput, "DOCX 文件解析失败", err)
	}

	// 查找 word/document.xml
	data, err := readZipFile(zr, "word/document.xml")
	if err != nil {
		return nil, errs.Wrap(errs.CodeInvalidInput, "DOCX 缺少 word/document.xml", err)
	}

	var doc docxDocument
	if err := xml.Unmarshal(data, &doc); err != nil {
		return nil, errs.Wrap(errs.CodeInvalidInput, "DOCX XML 解析失败", err)
	}

	// 提取所有段落文本
	var paragraphs []string
	for _, p := range doc.Body.Paragraphs {
		var texts []string
		for _, r := range p.Runs {
			for _, t := range r.Text {
				if t.Value != "" {
					texts = append(texts, t.Value)
				}
			}
		}
		if len(texts) > 0 {
			paragraphs = append(paragraphs, strings.Join(texts, ""))
		}
	}
	return paragraphs, nil
}

func extractDOCXMarkdown(filename string, data []byte) (*MarkdownDocument, error) {
	zr, err := openZipFromBytes(data)
	if err != nil {
		return nil, errs.Wrap(errs.CodeInvalidInput, "DOCX 文件解析失败", err)
	}
	paragraphs, err := extractDOCXParagraphTextFromZip(zr)
	if err != nil {
		return nil, err
	}
	assets, err := extractDOCXMediaAssets(zr)
	if err != nil {
		return nil, err
	}
	title := markdownTitle(filename)
	content := paragraphsToMarkdown(title, paragraphs)
	if len(assets) > 0 {
		var b strings.Builder
		b.WriteString(strings.TrimRight(content, "\n"))
		b.WriteString("\n\n")
		for _, asset := range assets {
			alt := strings.TrimSuffix(asset.Filename, filepath.Ext(asset.Filename))
			if alt == "" {
				alt = "image"
			}
			b.WriteString("![")
			b.WriteString(alt)
			b.WriteString("](")
			b.WriteString(asset.Path)
			b.WriteString(")\n\n")
		}
		content = strings.TrimRight(b.String(), "\n")
	}
	return &MarkdownDocument{Title: title, Content: content, Assets: assets, Quality: ConversionQualityExact}, nil
}

func extractDOCXParagraphTextFromZip(zr *zip.Reader) ([]string, error) {
	data, err := readZipFile(zr, "word/document.xml")
	if err != nil {
		return nil, errs.Wrap(errs.CodeInvalidInput, "DOCX 缺少 word/document.xml", err)
	}

	var doc docxDocument
	if err := xml.Unmarshal(data, &doc); err != nil {
		return nil, errs.Wrap(errs.CodeInvalidInput, "DOCX XML 解析失败", err)
	}

	var paragraphs []string
	for _, p := range doc.Body.Paragraphs {
		var texts []string
		for _, r := range p.Runs {
			for _, t := range r.Text {
				if t.Value != "" {
					texts = append(texts, t.Value)
				}
			}
		}
		if len(texts) > 0 {
			paragraphs = append(paragraphs, strings.Join(texts, ""))
		}
	}
	return paragraphs, nil
}

func extractDOCXMediaAssets(zr *zip.Reader) ([]ExtractedAsset, error) {
	var assets []ExtractedAsset
	for _, f := range zr.File {
		if !strings.HasPrefix(f.Name, "word/media/") {
			continue
		}
		mimeType := mime.TypeByExtension(filepath.Ext(f.Name))
		if !isMarkdownAssetMime(mimeType, f.Name) {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, errs.Wrap(errs.CodeInvalidInput, "DOCX 图片读取失败", err)
		}
		data, err := io.ReadAll(rc)
		closeErr := rc.Close()
		if err != nil {
			return nil, errs.Wrap(errs.CodeInvalidInput, "DOCX 图片读取失败", err)
		}
		if closeErr != nil {
			return nil, errs.Wrap(errs.CodeInvalidInput, "DOCX 图片关闭失败", closeErr)
		}
		if mimeType == "" {
			mimeType = "application/octet-stream"
		}
		assets = append(assets, ExtractedAsset{
			Path:     filepath.ToSlash(f.Name),
			Filename: filepath.Base(f.Name),
			MimeType: mimeType,
			Data:     data,
		})
	}
	sort.Slice(assets, func(i, j int) bool {
		return assets[i].Path < assets[j].Path
	})
	return assets, nil
}

// extractPPTX 从 PPTX 文件中提取文本
func extractPPTX(filename, base64Data string) (string, error) {
	zr, err := openZipFromBase64(base64Data)
	if err != nil {
		return "", errs.Wrap(errs.CodeInvalidInput, "PPTX 文件解析失败", err)
	}

	// 收集所有幻灯片文件并排序
	var slideFiles []string
	for _, f := range zr.File {
		if strings.HasPrefix(f.Name, "ppt/slides/slide") && strings.HasSuffix(f.Name, ".xml") {
			slideFiles = append(slideFiles, f.Name)
		}
	}
	sort.Strings(slideFiles)

	if len(slideFiles) == 0 {
		return fmt.Sprintf("--- %s ---\n（空演示文稿）", filename), nil
	}

	var slides []string
	for i, sf := range slideFiles {
		data, err := readZipFile(zr, sf)
		if err != nil {
			continue
		}

		var slide pptxSlide
		if err := xml.Unmarshal(data, &slide); err != nil {
			continue
		}

		var texts []string
		for _, sp := range slide.CSld.SpTree.Shapes {
			if sp.TxBody == nil {
				continue
			}
			for _, p := range sp.TxBody.Paragraphs {
				var parts []string
				for _, r := range p.Runs {
					if r.Text != "" {
						parts = append(parts, r.Text)
					}
				}
				if len(parts) > 0 {
					texts = append(texts, strings.Join(parts, ""))
				}
			}
		}

		slideText := strings.Join(texts, "\n")
		slides = append(slides, fmt.Sprintf("[Slide %d]\n%s", i+1, slideText))
	}

	content := strings.Join(slides, "\n\n")
	return fmt.Sprintf("--- %s ---\n%s", filename, content), nil
}

// extractXLSX 从 XLSX 文件中提取数据为 CSV 格式
func extractXLSX(filename, base64Data string) (string, error) {
	zr, err := openZipFromBase64(base64Data)
	if err != nil {
		return "", errs.Wrap(errs.CodeInvalidInput, "XLSX 文件解析失败", err)
	}

	// 读取共享字符串表（可选，某些 XLSX 可能没有）
	var sharedStrings []string
	if sstData, err := readZipFile(zr, "xl/sharedStrings.xml"); err == nil {
		var sst xlsxSST
		if err := xml.Unmarshal(sstData, &sst); err == nil {
			for _, si := range sst.SI {
				if si.T != "" {
					sharedStrings = append(sharedStrings, si.T)
				} else {
					// 富文本：拼接所有运行
					var parts []string
					for _, r := range si.R {
						parts = append(parts, r.T)
					}
					sharedStrings = append(sharedStrings, strings.Join(parts, ""))
				}
			}
		}
	}

	// 收集工作表文件并排序
	var sheetFiles []string
	for _, f := range zr.File {
		if strings.HasPrefix(f.Name, "xl/worksheets/sheet") && strings.HasSuffix(f.Name, ".xml") {
			sheetFiles = append(sheetFiles, f.Name)
		}
	}
	sort.Strings(sheetFiles)

	if len(sheetFiles) == 0 {
		return fmt.Sprintf("--- %s ---\n（空工作簿）", filename), nil
	}

	var csvLines []string
	for _, sf := range sheetFiles {
		data, err := readZipFile(zr, sf)
		if err != nil {
			continue
		}

		var ws xlsxWorksheet
		if err := xml.Unmarshal(data, &ws); err != nil {
			continue
		}

		for _, row := range ws.SheetData.Rows {
			var cells []string
			for _, cell := range row.Cells {
				val := cell.Value
				// 如果类型为 "s"，从共享字符串表查找
				if cell.Type == "s" {
					idx := 0
					fmt.Sscanf(val, "%d", &idx)
					if idx >= 0 && idx < len(sharedStrings) {
						val = sharedStrings[idx]
					}
				}
				cells = append(cells, val)
			}
			csvLines = append(csvLines, strings.Join(cells, ","))
		}
	}

	content := strings.Join(csvLines, "\n")
	return fmt.Sprintf("--- %s ---\n%s", filename, content), nil
}

// openZipFromBase64 从 base64 数据打开 ZIP 读取器
func openZipFromBase64(base64Data string) (*zip.Reader, error) {
	data, err := base64.StdEncoding.DecodeString(base64Data)
	if err != nil {
		return nil, fmt.Errorf("base64 解码失败: %w", err)
	}

	return openZipFromBytes(data)
}

func openZipFromBytes(data []byte) (*zip.Reader, error) {
	reader := bytes.NewReader(data)
	zr, err := zip.NewReader(reader, int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("ZIP 格式无效: %w", err)
	}

	return zr, nil
}

// readZipFile 从 ZIP 中读取指定文件的内容
func readZipFile(zr *zip.Reader, name string) ([]byte, error) {
	for _, f := range zr.File {
		if f.Name == name {
			rc, err := f.Open()
			if err != nil {
				return nil, err
			}
			defer rc.Close()
			return io.ReadAll(rc)
		}
	}
	return nil, fmt.Errorf("ZIP 中未找到文件: %s", name)
}
