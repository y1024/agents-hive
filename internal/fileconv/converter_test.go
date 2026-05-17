package fileconv

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/xml"
	"fmt"
	"os/exec"
	"strings"
	"testing"
)

// --- 辅助函数：构建最小有效 ZIP 文件 ---

// buildZip 构建包含指定文件的 ZIP 并返回 base64 编码
func buildZip(t *testing.T, files map[string][]byte) string {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for name, data := range files {
		f, err := w.Create(name)
		if err != nil {
			t.Fatalf("创建 ZIP 文件条目失败: %v", err)
		}
		if _, err := f.Write(data); err != nil {
			t.Fatalf("写入 ZIP 文件条目失败: %v", err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("关闭 ZIP 写入器失败: %v", err)
	}
	return base64.StdEncoding.EncodeToString(buf.Bytes())
}

// buildMinimalDOCX 构建最小有效 DOCX
func buildMinimalDOCX(t *testing.T, text string) string {
	t.Helper()
	type wtText struct {
		XMLName xml.Name `xml:"w:t"`
		Value   string   `xml:",chardata"`
	}
	type wRun struct {
		XMLName xml.Name `xml:"w:r"`
		Text    wtText
	}
	type wParagraph struct {
		XMLName xml.Name `xml:"w:p"`
		Runs    []wRun
	}
	type wBody struct {
		XMLName    xml.Name `xml:"w:body"`
		Paragraphs []wParagraph
	}
	type wDocument struct {
		XMLName xml.Name `xml:"w:document"`
		WNS     string   `xml:"xmlns:w,attr"`
		Body    wBody
	}

	doc := wDocument{
		WNS: "http://schemas.openxmlformats.org/wordprocessingml/2006/main",
		Body: wBody{
			Paragraphs: []wParagraph{
				{Runs: []wRun{{Text: wtText{Value: text}}}},
			},
		},
	}

	xmlData, err := xml.Marshal(doc)
	if err != nil {
		t.Fatalf("序列化 DOCX XML 失败: %v", err)
	}

	return buildZip(t, map[string][]byte{
		"word/document.xml": xmlData,
	})
}

// buildMinimalPPTX 构建最小有效 PPTX
func buildMinimalPPTX(t *testing.T, slides []string) string {
	t.Helper()
	files := make(map[string][]byte)

	for i, text := range slides {
		// 构建幻灯片 XML（手动拼接，因为命名空间处理复杂）
		slideXML := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<p:sld xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main"
       xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main">
  <p:cSld>
    <p:spTree>
      <p:sp>
        <p:txBody>
          <a:p><a:r><a:t>%s</a:t></a:r></a:p>
        </p:txBody>
      </p:sp>
    </p:spTree>
  </p:cSld>
</p:sld>`, text)
		files[fmt.Sprintf("ppt/slides/slide%d.xml", i+1)] = []byte(slideXML)
	}

	return buildZip(t, files)
}

// buildMinimalXLSX 构建最小有效 XLSX
func buildMinimalXLSX(t *testing.T, rows [][]string) string {
	t.Helper()

	// 构建共享字符串表
	var allStrings []string
	stringIndex := make(map[string]int)
	for _, row := range rows {
		for _, cell := range row {
			if _, ok := stringIndex[cell]; !ok {
				stringIndex[cell] = len(allStrings)
				allStrings = append(allStrings, cell)
			}
		}
	}

	var sstBuf bytes.Buffer
	sstBuf.WriteString(`<?xml version="1.0" encoding="UTF-8"?>`)
	sstBuf.WriteString(fmt.Sprintf(`<sst xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" count="%d" uniqueCount="%d">`, len(allStrings), len(allStrings)))
	for _, s := range allStrings {
		sstBuf.WriteString(fmt.Sprintf("<si><t>%s</t></si>", xmlEscape(s)))
	}
	sstBuf.WriteString("</sst>")

	// 构建工作表
	var sheetBuf bytes.Buffer
	sheetBuf.WriteString(`<?xml version="1.0" encoding="UTF-8"?>`)
	sheetBuf.WriteString(`<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData>`)
	for _, row := range rows {
		sheetBuf.WriteString("<row>")
		for _, cell := range row {
			idx := stringIndex[cell]
			sheetBuf.WriteString(fmt.Sprintf(`<c t="s"><v>%d</v></c>`, idx))
		}
		sheetBuf.WriteString("</row>")
	}
	sheetBuf.WriteString("</sheetData></worksheet>")

	return buildZip(t, map[string][]byte{
		"xl/sharedStrings.xml":     sstBuf.Bytes(),
		"xl/worksheets/sheet1.xml": sheetBuf.Bytes(),
	})
}

// xmlEscape 简单的 XML 字符转义
func xmlEscape(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	return s
}

// mockWhisperFunc 模拟 Whisper 转录函数
func mockWhisperFunc(_ context.Context, _ []byte, filename string) (string, error) {
	return "这是转录的文本内容 from " + filename, nil
}

// failWhisperFunc 模拟 Whisper 转录失败
func failWhisperFunc(_ context.Context, _ []byte, _ string) (string, error) {
	return "", fmt.Errorf("whisper 服务不可用")
}

func TestConvert(t *testing.T) {
	ctx := context.Background()
	textBase64 := base64.StdEncoding.EncodeToString([]byte("Hello, World!"))

	tests := []struct {
		name       string
		filename   string
		mimeType   string
		base64Data string
		whisperFn  WhisperFunc
		wantType   string
		wantText   string // 部分匹配
		wantErr    bool
	}{
		// --- 图片透传 ---
		{
			name:       "图片 PNG 透传",
			filename:   "photo.png",
			mimeType:   "image/png",
			base64Data: "iVBORw0KGgo=",
			wantType:   "image",
		},
		{
			name:       "图片 JPEG 透传",
			filename:   "photo.jpg",
			mimeType:   "image/jpeg",
			base64Data: "/9j/4AAQ",
			wantType:   "image",
		},

		// --- PDF 透传 ---
		{
			name:       "PDF 透传",
			filename:   "doc.pdf",
			mimeType:   "application/pdf",
			base64Data: "JVBERi0=",
			wantType:   "file",
		},

		// --- 文本文件 ---
		{
			name:       "纯文本文件",
			filename:   "hello.txt",
			mimeType:   "text/plain",
			base64Data: textBase64,
			wantType:   "text",
			wantText:   "--- hello.txt ---\nHello, World!",
		},
		{
			name:       "HTML 文件",
			filename:   "page.html",
			mimeType:   "text/html",
			base64Data: base64.StdEncoding.EncodeToString([]byte("<h1>Title</h1>")),
			wantType:   "text",
			wantText:   "--- page.html ---\n<h1>Title</h1>",
		},

		// --- 代码文件（octet-stream）---
		{
			name:       "Go 代码文件",
			filename:   "main.go",
			mimeType:   "application/octet-stream",
			base64Data: base64.StdEncoding.EncodeToString([]byte("package main")),
			wantType:   "text",
			wantText:   "--- main.go ---\npackage main",
		},
		{
			name:       "Python 代码文件",
			filename:   "app.py",
			mimeType:   "application/octet-stream",
			base64Data: base64.StdEncoding.EncodeToString([]byte("print('hello')")),
			wantType:   "text",
			wantText:   "--- app.py ---\nprint('hello')",
		},
		{
			name:       "TypeScript 代码文件",
			filename:   "index.ts",
			mimeType:   "application/octet-stream",
			base64Data: base64.StdEncoding.EncodeToString([]byte("const x: number = 1")),
			wantType:   "text",
			wantText:   "--- index.ts ---\nconst x: number = 1",
		},
		{
			name:       "Rust 代码文件",
			filename:   "main.rs",
			mimeType:   "application/octet-stream",
			base64Data: base64.StdEncoding.EncodeToString([]byte("fn main() {}")),
			wantType:   "text",
			wantText:   "--- main.rs ---\nfn main() {}",
		},

		// --- 音频转录 ---
		{
			name:       "音频文件转录",
			filename:   "voice.mp3",
			mimeType:   "audio/mpeg",
			base64Data: base64.StdEncoding.EncodeToString([]byte("fake-audio-data")),
			whisperFn:  mockWhisperFunc,
			wantType:   "text",
			wantText:   "--- voice.mp3 [转录] ---\n这是转录的文本内容 from voice.mp3",
		},

		// --- 错误情况 ---
		{
			name:       "空文件名",
			filename:   "",
			mimeType:   "text/plain",
			base64Data: textBase64,
			wantErr:    true,
		},
		{
			name:       "空 MIME 类型",
			filename:   "file.txt",
			mimeType:   "",
			base64Data: textBase64,
			wantErr:    true,
		},
		{
			name:       "空 base64 数据",
			filename:   "file.txt",
			mimeType:   "text/plain",
			base64Data: "",
			wantErr:    true,
		},
		{
			name:       "不支持的 MIME 类型",
			filename:   "file.bin",
			mimeType:   "application/octet-stream",
			base64Data: textBase64,
			wantErr:    true,
		},
		{
			name:       "音频无回调函数",
			filename:   "voice.mp3",
			mimeType:   "audio/mpeg",
			base64Data: base64.StdEncoding.EncodeToString([]byte("fake")),
			whisperFn:  nil,
			wantErr:    true,
		},
		{
			name:       "音频转录失败",
			filename:   "voice.mp3",
			mimeType:   "audio/mpeg",
			base64Data: base64.StdEncoding.EncodeToString([]byte("fake")),
			whisperFn:  failWhisperFunc,
			wantErr:    true,
		},
		{
			name:       "视频无回调函数",
			filename:   "clip.mp4",
			mimeType:   "video/mp4",
			base64Data: base64.StdEncoding.EncodeToString([]byte("fake")),
			whisperFn:  nil,
			wantErr:    true,
		},
		{
			name:       "超长文件名",
			filename:   strings.Repeat("a", 1000) + ".txt",
			mimeType:   "text/plain",
			base64Data: textBase64,
			wantType:   "text",
			wantText:   "Hello, World!",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := Convert(ctx, tt.filename, tt.mimeType, tt.base64Data, tt.whisperFn)
			if tt.wantErr {
				if err == nil {
					t.Fatal("期望返回错误，但没有")
				}
				return
			}
			if err != nil {
				t.Fatalf("意外错误: %v", err)
			}
			if result.Type != tt.wantType {
				t.Errorf("Type = %q, 期望 %q", result.Type, tt.wantType)
			}
			if tt.wantText != "" && !strings.Contains(result.Text, tt.wantText) {
				t.Errorf("Text 不包含期望内容:\n  得到: %q\n  期望包含: %q", result.Text, tt.wantText)
			}
		})
	}
}

func TestConvertDOCX(t *testing.T) {
	ctx := context.Background()
	base64Data := buildMinimalDOCX(t, "这是测试文档内容")

	result, err := Convert(ctx, "test.docx",
		"application/vnd.openxmlformats-officedocument.wordprocessingml.document",
		base64Data, nil)
	if err != nil {
		t.Fatalf("DOCX 转换失败: %v", err)
	}
	if result.Type != "text" {
		t.Errorf("Type = %q, 期望 \"text\"", result.Type)
	}
	if !strings.Contains(result.Text, "这是测试文档内容") {
		t.Errorf("DOCX 文本未包含期望内容: %q", result.Text)
	}
	if !strings.Contains(result.Text, "--- test.docx ---") {
		t.Errorf("DOCX 输出缺少文件名标题: %q", result.Text)
	}
}

func TestConvertPPTX(t *testing.T) {
	ctx := context.Background()
	base64Data := buildMinimalPPTX(t, []string{"第一页内容", "第二页内容"})

	result, err := Convert(ctx, "slides.pptx",
		"application/vnd.openxmlformats-officedocument.presentationml.presentation",
		base64Data, nil)
	if err != nil {
		t.Fatalf("PPTX 转换失败: %v", err)
	}
	if result.Type != "text" {
		t.Errorf("Type = %q, 期望 \"text\"", result.Type)
	}
	if !strings.Contains(result.Text, "[Slide 1]") {
		t.Error("PPTX 输出缺少 [Slide 1]")
	}
	if !strings.Contains(result.Text, "[Slide 2]") {
		t.Error("PPTX 输出缺少 [Slide 2]")
	}
	if !strings.Contains(result.Text, "第一页内容") {
		t.Errorf("PPTX 未包含第一页内容: %q", result.Text)
	}
	if !strings.Contains(result.Text, "第二页内容") {
		t.Errorf("PPTX 未包含第二页内容: %q", result.Text)
	}
}

func TestConvertXLSX(t *testing.T) {
	ctx := context.Background()
	base64Data := buildMinimalXLSX(t, [][]string{
		{"姓名", "年龄", "城市"},
		{"张三", "25", "北京"},
		{"李四", "30", "上海"},
	})

	result, err := Convert(ctx, "data.xlsx",
		"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
		base64Data, nil)
	if err != nil {
		t.Fatalf("XLSX 转换失败: %v", err)
	}
	if result.Type != "text" {
		t.Errorf("Type = %q, 期望 \"text\"", result.Type)
	}
	if !strings.Contains(result.Text, "--- data.xlsx ---") {
		t.Error("XLSX 输出缺少文件名标题")
	}
	if !strings.Contains(result.Text, "姓名") {
		t.Errorf("XLSX 未包含表头: %q", result.Text)
	}
	if !strings.Contains(result.Text, "张三") {
		t.Errorf("XLSX 未包含数据行: %q", result.Text)
	}
}

func TestConvertDOCXInvalidZip(t *testing.T) {
	ctx := context.Background()
	// 无效的 base64 数据（不是有效 ZIP）
	base64Data := base64.StdEncoding.EncodeToString([]byte("not a zip file"))

	_, err := Convert(ctx, "bad.docx",
		"application/vnd.openxmlformats-officedocument.wordprocessingml.document",
		base64Data, nil)
	if err == nil {
		t.Fatal("期望无效 DOCX 返回错误")
	}
}

func TestConvertVideoSkipIfNoFFmpeg(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("跳过视频测试：ffmpeg 未安装")
	}

	// 即使有 ffmpeg，伪数据也会导致错误（非有效视频）
	ctx := context.Background()
	base64Data := base64.StdEncoding.EncodeToString([]byte("not-a-video"))
	_, err := Convert(ctx, "clip.mp4", "video/mp4", base64Data, mockWhisperFunc)
	if err == nil {
		t.Log("注意：ffmpeg 处理伪视频数据可能不会报错")
	}
}

func TestConvertEmptyDOCX(t *testing.T) {
	// DOCX 中没有文本内容
	ctx := context.Background()
	base64Data := buildMinimalDOCX(t, "")

	result, err := Convert(ctx, "empty.docx",
		"application/vnd.openxmlformats-officedocument.wordprocessingml.document",
		base64Data, nil)
	if err != nil {
		t.Fatalf("空 DOCX 转换失败: %v", err)
	}
	if result.Type != "text" {
		t.Errorf("Type = %q, 期望 \"text\"", result.Type)
	}
	if !strings.Contains(result.Text, "--- empty.docx ---") {
		t.Error("空 DOCX 输出缺少文件名标题")
	}
}

func TestConvertInvalidBase64Text(t *testing.T) {
	ctx := context.Background()
	_, err := Convert(ctx, "file.txt", "text/plain", "!!!invalid-base64!!!", nil)
	if err == nil {
		t.Fatal("期望无效 base64 返回错误")
	}
}

func TestConvertInvalidBase64Audio(t *testing.T) {
	ctx := context.Background()
	_, err := Convert(ctx, "voice.mp3", "audio/mpeg", "!!!invalid!!!", mockWhisperFunc)
	if err == nil {
		t.Fatal("期望无效 base64 音频返回错误")
	}
}

func TestConvertInvalidBase64Video(t *testing.T) {
	ctx := context.Background()
	_, err := Convert(ctx, "clip.mp4", "video/mp4", "!!!invalid!!!", mockWhisperFunc)
	if err == nil {
		t.Fatal("期望无效 base64 视频返回错误")
	}
}
