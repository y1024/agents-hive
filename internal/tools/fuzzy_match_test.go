package tools

import (
	"testing"

	"go.uber.org/zap"
)

// TestExactReplacer_Match 测试精确匹配器
func TestExactReplacer_Match(t *testing.T) {
	tests := []struct {
		name      string
		fileLines []string
		hunkLines []string
		startHint int
		wantStart int
		wantOK    bool
	}{
		{
			name:      "精确匹配_hint位置正确",
			fileLines: []string{"line1", "line2", "line3"},
			hunkLines: []string{"line1", "line2"},
			startHint: 0,
			wantStart: 0,
			wantOK:    true,
		},
		{
			name:      "精确匹配_hint位置不正确但能搜索到",
			fileLines: []string{"line1", "line2", "line3"},
			hunkLines: []string{"line2", "line3"},
			startHint: 0,
			wantStart: 1,
			wantOK:    true,
		},
		{
			name:      "精确匹配_空白差异导致失败",
			fileLines: []string{"  line1", "  line2"},
			hunkLines: []string{"line1", "line2"},
			startHint: 0,
			wantStart: -1,
			wantOK:    false,
		},
		{
			name:      "空hunk行_返回hint位置",
			fileLines: []string{"line1"},
			hunkLines: []string{},
			startHint: 0,
			wantStart: 0,
			wantOK:    true,
		},
		{
			name:      "无hint时从头搜索",
			fileLines: []string{"a", "b", "c"},
			hunkLines: []string{"b", "c"},
			startHint: -1,
			wantStart: 1,
			wantOK:    true,
		},
	}

	r := &ExactReplacer{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			start, ok := r.Match(tt.fileLines, tt.hunkLines, tt.startHint)
			if ok != tt.wantOK {
				t.Errorf("ok = %v, 期望 %v", ok, tt.wantOK)
			}
			if ok && start != tt.wantStart {
				t.Errorf("startLine = %d, 期望 %d", start, tt.wantStart)
			}
		})
	}
}

// TestLineTrimmedReplacer_Match 测试行首尾空白容错匹配器
func TestLineTrimmedReplacer_Match(t *testing.T) {
	tests := []struct {
		name      string
		fileLines []string
		hunkLines []string
		startHint int
		wantStart int
		wantOK    bool
	}{
		{
			name:      "trim后匹配_行尾有空格",
			fileLines: []string{"line1  ", "line2  ", "line3"},
			hunkLines: []string{"line1", "line2"},
			startHint: 0,
			wantStart: 0,
			wantOK:    true,
		},
		{
			name:      "trim后匹配_行首有空格",
			fileLines: []string{"  line1", "  line2"},
			hunkLines: []string{"line1", "line2"},
			startHint: 0,
			wantStart: 0,
			wantOK:    true,
		},
		{
			name:      "trim后匹配_两端都有空格",
			fileLines: []string{"\tline1\t", "  line2  "},
			hunkLines: []string{"line1", "line2"},
			startHint: 0,
			wantStart: 0,
			wantOK:    true,
		},
		{
			name:      "trim后仍不匹配",
			fileLines: []string{"lineA", "lineB"},
			hunkLines: []string{"line1", "line2"},
			startHint: 0,
			wantStart: -1,
			wantOK:    false,
		},
	}

	r := &LineTrimmedReplacer{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			start, ok := r.Match(tt.fileLines, tt.hunkLines, tt.startHint)
			if ok != tt.wantOK {
				t.Errorf("ok = %v, 期望 %v", ok, tt.wantOK)
			}
			if ok && start != tt.wantStart {
				t.Errorf("startLine = %d, 期望 %d", start, tt.wantStart)
			}
		})
	}
}

// TestWhitespaceNormalizedReplacer_Match 测试空白归一化匹配器
func TestWhitespaceNormalizedReplacer_Match(t *testing.T) {
	tests := []struct {
		name      string
		fileLines []string
		hunkLines []string
		startHint int
		wantStart int
		wantOK    bool
	}{
		{
			name:      "多空格归一化匹配",
			fileLines: []string{"a  b  c", "d   e"},
			hunkLines: []string{"a b c", "d e"},
			startHint: 0,
			wantStart: 0,
			wantOK:    true,
		},
		{
			name:      "tab和空格混合归一化",
			fileLines: []string{"a\t\tb", "c \t d"},
			hunkLines: []string{"a b", "c d"},
			startHint: 0,
			wantStart: 0,
			wantOK:    true,
		},
		{
			name:      "内容不同_归一化后仍不匹配",
			fileLines: []string{"hello world"},
			hunkLines: []string{"hello mars"},
			startHint: 0,
			wantStart: -1,
			wantOK:    false,
		},
	}

	r := &WhitespaceNormalizedReplacer{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			start, ok := r.Match(tt.fileLines, tt.hunkLines, tt.startHint)
			if ok != tt.wantOK {
				t.Errorf("ok = %v, 期望 %v", ok, tt.wantOK)
			}
			if ok && start != tt.wantStart {
				t.Errorf("startLine = %d, 期望 %d", start, tt.wantStart)
			}
		})
	}
}

// TestIndentationFlexibleReplacer_Match 测试缩进容错匹配器
func TestIndentationFlexibleReplacer_Match(t *testing.T) {
	tests := []struct {
		name      string
		fileLines []string
		hunkLines []string
		startHint int
		wantStart int
		wantOK    bool
	}{
		{
			name:      "忽略缩进差异_tab vs 空格",
			fileLines: []string{"\tline1", "\t\tline2"},
			hunkLines: []string{"    line1", "        line2"},
			startHint: 0,
			wantStart: 0,
			wantOK:    true,
		},
		{
			name:      "忽略缩进差异_不同缩进级别",
			fileLines: []string{"    func main() {", "        fmt.Println()"},
			hunkLines: []string{"  func main() {", "    fmt.Println()"},
			startHint: 0,
			wantStart: 0,
			wantOK:    true,
		},
		{
			name:      "无缩进差异_精确匹配",
			fileLines: []string{"line1", "line2"},
			hunkLines: []string{"line1", "line2"},
			startHint: 0,
			wantStart: 0,
			wantOK:    true,
		},
		{
			name:      "内容不同_即使忽略缩进也不匹配",
			fileLines: []string{"\thello"},
			hunkLines: []string{"\tworld"},
			startHint: 0,
			wantStart: -1,
			wantOK:    false,
		},
	}

	r := &IndentationFlexibleReplacer{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			start, ok := r.Match(tt.fileLines, tt.hunkLines, tt.startHint)
			if ok != tt.wantOK {
				t.Errorf("ok = %v, 期望 %v", ok, tt.wantOK)
			}
			if ok && start != tt.wantStart {
				t.Errorf("startLine = %d, 期望 %d", start, tt.wantStart)
			}
		})
	}
}

// TestFuzzyMatchHunk_渐进策略 测试渐进匹配调度逻辑
func TestFuzzyMatchHunk_渐进策略(t *testing.T) {
	tests := []struct {
		name      string
		fileLines []string
		hunk      *Hunk
		reverse   bool
		wantLevel MatchLevel
		wantStart int
		wantErr   bool
	}{
		{
			name:      "精确匹配成功",
			fileLines: []string{"line1", "line2", "line3"},
			hunk: &Hunk{
				OldStart: 1,
				OldLines: 2,
				NewStart: 1,
				NewLines: 2,
				Lines: []HunkLine{
					{Type: LineContext, Content: "line1"},
					{Type: LineRemoved, Content: "line2"},
					{Type: LineAdded, Content: "line2_modified"},
				},
			},
			wantLevel: MatchExact,
			wantStart: 0,
		},
		{
			name:      "精确失败_trim匹配成功",
			fileLines: []string{"line1  ", "line2  ", "line3"},
			hunk: &Hunk{
				OldStart: 1,
				OldLines: 2,
				NewStart: 1,
				NewLines: 2,
				Lines: []HunkLine{
					{Type: LineContext, Content: "line1"},
					{Type: LineRemoved, Content: "line2"},
					{Type: LineAdded, Content: "line2_modified"},
				},
			},
			wantLevel: MatchLineTrimmed,
			wantStart: 0,
		},
		{
			name:      "精确和trim都失败_空白归一化成功",
			fileLines: []string{"a  b  c", "d   e", "f"},
			hunk: &Hunk{
				OldStart: 1,
				OldLines: 2,
				NewStart: 1,
				NewLines: 2,
				Lines: []HunkLine{
					{Type: LineContext, Content: "a b c"},
					{Type: LineRemoved, Content: "d e"},
					{Type: LineAdded, Content: "new_line"},
				},
			},
			wantLevel: MatchWhitespaceNormalized,
			wantStart: 0,
		},
		{
			name:      "缩进差异_LineTrimmed优先匹配",
			fileLines: []string{"\tfunc main() {", "\t\tfmt.Println()", "\t}"},
			hunk: &Hunk{
				OldStart: 1,
				OldLines: 2,
				NewStart: 1,
				NewLines: 2,
				Lines: []HunkLine{
					{Type: LineContext, Content: "    func main() {"},
					{Type: LineRemoved, Content: "        fmt.Println()"},
					{Type: LineAdded, Content: "        fmt.Println(\"hello\")"},
				},
			},
			wantLevel: MatchLineTrimmed,
			wantStart: 0,
		},
		{
			name:      "所有策略都失败",
			fileLines: []string{"completely", "different", "content"},
			hunk: &Hunk{
				OldStart: 1,
				OldLines: 2,
				NewStart: 1,
				NewLines: 2,
				Lines: []HunkLine{
					{Type: LineContext, Content: "expected_line1"},
					{Type: LineRemoved, Content: "expected_line2"},
					{Type: LineAdded, Content: "new_line"},
				},
			},
			wantErr: true,
		},
		{
			name:      "反向模式_精确匹配",
			fileLines: []string{"line1", "line2_modified", "line3"},
			hunk: &Hunk{
				OldStart: 1,
				OldLines: 2,
				NewStart: 1,
				NewLines: 2,
				Lines: []HunkLine{
					{Type: LineContext, Content: "line1"},
					{Type: LineRemoved, Content: "line2"},
					{Type: LineAdded, Content: "line2_modified"},
				},
			},
			reverse:   true,
			wantLevel: MatchExact,
			wantStart: 0,
		},
		{
			name:      "纯添加hunk_无需匹配行",
			fileLines: []string{"line1", "line2", "line3"},
			hunk: &Hunk{
				OldStart: 2,
				OldLines: 0,
				NewStart: 2,
				NewLines: 2,
				Lines: []HunkLine{
					{Type: LineAdded, Content: "new1"},
					{Type: LineAdded, Content: "new2"},
				},
			},
			wantLevel: MatchExact,
			wantStart: 1,
		},
	}

	logger := zap.NewNop()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := fuzzyMatchHunk(tt.fileLines, tt.hunk, tt.reverse, logger)
			if tt.wantErr {
				if err == nil {
					t.Error("期望返回错误，但成功了")
				}
				return
			}
			if err != nil {
				t.Fatalf("意外错误: %v", err)
			}
			if result.MatchLevel != tt.wantLevel {
				t.Errorf("匹配级别 = %v (%s), 期望 %v (%s)",
					result.MatchLevel, matchLevelNames[result.MatchLevel],
					tt.wantLevel, matchLevelNames[tt.wantLevel])
			}
			if result.StartLine != tt.wantStart {
				t.Errorf("起始行 = %d, 期望 %d", result.StartLine, tt.wantStart)
			}
		})
	}
}

// TestNormalizeWhitespace 测试空白归一化函数
func TestNormalizeWhitespace(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"a  b  c", "a b c"},
		{"\t a \t b \t", "a b"},
		{"  hello  world  ", "hello world"},
		{"no_extra_spaces", "no_extra_spaces"},
		{"", ""},
		{"\t\t\t", ""},
	}

	for _, tt := range tests {
		got := normalizeWhitespace(tt.input)
		if got != tt.want {
			t.Errorf("normalizeWhitespace(%q) = %q, 期望 %q", tt.input, got, tt.want)
		}
	}
}

// TestReplacer_Level 测试各匹配器的级别标识
func TestReplacer_Level(t *testing.T) {
	tests := []struct {
		name     string
		replacer Replacer
		want     MatchLevel
	}{
		{"ExactReplacer", &ExactReplacer{}, MatchExact},
		{"LineTrimmedReplacer", &LineTrimmedReplacer{}, MatchLineTrimmed},
		{"WhitespaceNormalizedReplacer", &WhitespaceNormalizedReplacer{}, MatchWhitespaceNormalized},
		{"IndentationFlexibleReplacer", &IndentationFlexibleReplacer{}, MatchIndentationFlexible},
		{"BlockAnchorReplacer", &BlockAnchorReplacer{}, MatchBlockAnchor},
		{"EscapeNormalizedReplacer", &EscapeNormalizedReplacer{}, MatchEscapeNormalized},
		{"TrimmedBoundaryReplacer", &TrimmedBoundaryReplacer{}, MatchTrimmedBoundary},
		{"ContextAwareReplacer", &ContextAwareReplacer{}, MatchContextAware},
		{"MultiOccurrenceReplacer", &MultiOccurrenceReplacer{}, MatchMultiOccurrence},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.replacer.Level(); got != tt.want {
				t.Errorf("Level() = %v, 期望 %v", got, tt.want)
			}
		})
	}
}

// TestDefaultReplacers 测试默认匹配器列表
func TestDefaultReplacers(t *testing.T) {
	replacers := defaultReplacers()
	if len(replacers) != 9 {
		t.Fatalf("期望 9 个匹配器，实际 %d 个", len(replacers))
	}

	expectedLevels := []MatchLevel{
		MatchExact,
		MatchLineTrimmed,
		MatchWhitespaceNormalized,
		MatchIndentationFlexible,
		MatchBlockAnchor,
		MatchEscapeNormalized,
		MatchTrimmedBoundary,
		MatchContextAware,
		MatchMultiOccurrence,
	}

	for i, r := range replacers {
		if r.Level() != expectedLevels[i] {
			t.Errorf("匹配器 %d 级别 = %v, 期望 %v", i, r.Level(), expectedLevels[i])
		}
	}
}

// TestFuzzyFindString 测试字符串级别的模糊查找
func TestFuzzyFindString(t *testing.T) {
	logger := zap.NewNop()

	tests := []struct {
		name      string
		content   string
		oldString string
		wantLevel MatchLevel
		wantOK    bool
		wantFound string // 期望返回的原始文件中的字符串
	}{
		{
			name:      "精确匹配成功",
			content:   "line1\nline2\nline3\n",
			oldString: "line2\nline3",
			wantLevel: MatchExact,
			wantOK:    true,
			wantFound: "line2\nline3",
		},
		{
			name:      "行首尾空白容错匹配",
			content:   "  line1  \n  line2  \nline3\n",
			oldString: "line1\nline2",
			wantLevel: MatchLineTrimmed,
			wantOK:    true,
			wantFound: "  line1  \n  line2  ",
		},
		{
			name:      "空白归一化匹配",
			content:   "a  b  c\nd   e\nf\n",
			oldString: "a b c\nd e",
			wantLevel: MatchWhitespaceNormalized,
			wantOK:    true,
			wantFound: "a  b  c\nd   e",
		},
		{
			name:      "缩进弹性匹配",
			content:   "\tfunc main() {\n\t\tfmt.Println()\n\t}\n",
			oldString: "    func main() {\n        fmt.Println()",
			wantLevel: MatchLineTrimmed,
			wantOK:    true,
			wantFound: "\tfunc main() {\n\t\tfmt.Println()",
		},
		{
			name:      "所有匹配都失败",
			content:   "completely\ndifferent\ncontent\n",
			oldString: "nothing\nmatches",
			wantLevel: 0,
			wantOK:    false,
		},
		{
			name:      "单行模糊匹配_行尾空白",
			content:   "hello world   \n",
			oldString: "hello world",
			wantLevel: MatchExact, // 单行精确匹配（strings.Contains 直接成功）
			wantOK:    true,
			wantFound: "hello world",
		},
		{
			name:      "多行_缩进不同",
			content:   "    if true {\n        doSomething()\n    }\n",
			oldString: "  if true {\n    doSomething()",
			wantLevel: MatchLineTrimmed,
			wantOK:    true,
			wantFound: "    if true {\n        doSomething()",
		},
		{
			name:      "tab与空格混合_归一化匹配",
			content:   "key\t\tvalue\nnext\t line\n",
			oldString: "key value\nnext line",
			wantLevel: MatchWhitespaceNormalized,
			wantOK:    true,
			wantFound: "key\t\tvalue\nnext\t line",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			found, level, ok := FuzzyFindString(tt.content, tt.oldString, logger)
			if ok != tt.wantOK {
				t.Errorf("ok = %v, 期望 %v", ok, tt.wantOK)
				return
			}
			if !ok {
				return
			}
			if level != tt.wantLevel {
				t.Errorf("匹配级别 = %v (%s), 期望 %v (%s)",
					level, matchLevelNames[level],
					tt.wantLevel, matchLevelNames[tt.wantLevel])
			}
			if found != tt.wantFound {
				t.Errorf("找到的字符串 = %q, 期望 %q", found, tt.wantFound)
			}
		})
	}
}

// TestFuzzyMatchHunk_搜索回退 测试当 hint 位置不正确时能搜索到正确位置
func TestFuzzyMatchHunk_搜索回退(t *testing.T) {
	fileLines := []string{"header", "line1", "line2", "line3", "footer"}
	hunk := &Hunk{
		OldStart: 1, // hint 指向 "header"，但实际内容在第 2 行
		OldLines: 2,
		NewStart: 1,
		NewLines: 2,
		Lines: []HunkLine{
			{Type: LineContext, Content: "line1"},
			{Type: LineRemoved, Content: "line2"},
			{Type: LineAdded, Content: "line2_new"},
		},
	}

	logger := zap.NewNop()
	result, err := fuzzyMatchHunk(fileLines, hunk, false, logger)
	if err != nil {
		t.Fatalf("意外错误: %v", err)
	}

	if result.StartLine != 1 {
		t.Errorf("起始行 = %d, 期望 1（搜索回退应找到正确位置）", result.StartLine)
	}
	if result.MatchLevel != MatchExact {
		t.Errorf("匹配级别 = %v, 期望精确匹配", result.MatchLevel)
	}
}

// TestBlockAnchorReplacer_Match 测试首尾锚点相似度匹配器
func TestBlockAnchorReplacer_Match(t *testing.T) {
	tests := []struct {
		name      string
		fileLines []string
		hunkLines []string
		startHint int
		wantStart int
		wantOK    bool
	}{
		{
			name: "首尾锚点匹配_中间内容略有差异",
			fileLines: []string{
				"func main() {",
				"    fmt.Println(\"hello\")",
				"    fmt.Println(\"world\")",
				"    fmt.Println(\"done\")",
				"}",
			},
			hunkLines: []string{
				"func main() {",
				"    fmt.Println(\"hi\")",
				"    fmt.Println(\"world\")",
				"    fmt.Println(\"done\")",
				"}",
			},
			startHint: -1,
			wantStart: 0,
			wantOK:    true,
		},
		{
			name: "首行锚点不匹配_跳过",
			fileLines: []string{
				"func run() {",
				"    doA()",
				"    doB()",
				"}",
			},
			hunkLines: []string{
				"func main() {",
				"    doA()",
				"    doB()",
				"}",
			},
			startHint: -1,
			wantStart: -1,
			wantOK:    false,
		},
		{
			name: "中间内容差异太大_相似度不足",
			fileLines: []string{
				"func main() {",
				"    completelyDifferentCode1()",
				"    completelyDifferentCode2()",
				"    completelyDifferentCode3()",
				"}",
			},
			hunkLines: []string{
				"func main() {",
				"    fmt.Println(\"hello\")",
				"    fmt.Println(\"world\")",
				"    fmt.Println(\"done\")",
				"}",
			},
			startHint: -1,
			wantStart: -1,
			wantOK:    false,
		},
		{
			name:      "少于3行_跳过此策略",
			fileLines: []string{"line1", "line2"},
			hunkLines: []string{"line1", "line2"},
			startHint: -1,
			wantStart: -1,
			wantOK:    false,
		},
		{
			name: "尾行锚点不匹配_跳过",
			fileLines: []string{
				"func main() {",
				"    doA()",
				"    doB()",
				"} // end",
			},
			hunkLines: []string{
				"func main() {",
				"    doA()",
				"    doB()",
				"}",
			},
			startHint: -1,
			wantStart: -1,
			wantOK:    false,
		},
		{
			name: "中间位置匹配_偏移正确",
			fileLines: []string{
				"// header",
				"func main() {",
				"    fmt.Println(\"hello\")",
				"    fmt.Println(\"world\")",
				"}",
				"// footer",
			},
			hunkLines: []string{
				"func main() {",
				"    fmt.Println(\"hi\")",
				"    fmt.Println(\"world\")",
				"}",
			},
			startHint: -1,
			wantStart: 1,
			wantOK:    true,
		},
	}

	r := &BlockAnchorReplacer{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			start, ok := r.Match(tt.fileLines, tt.hunkLines, tt.startHint)
			if ok != tt.wantOK {
				t.Errorf("ok = %v, 期望 %v", ok, tt.wantOK)
			}
			if ok && start != tt.wantStart {
				t.Errorf("startLine = %d, 期望 %d", start, tt.wantStart)
			}
		})
	}
}

// TestEscapeNormalizedReplacer_Match 测试转义归一化匹配器
func TestEscapeNormalizedReplacer_Match(t *testing.T) {
	tests := []struct {
		name      string
		fileLines []string
		hunkLines []string
		startHint int
		wantStart int
		wantOK    bool
	}{
		{
			name:      "反斜杠n_vs_换行符",
			fileLines: []string{`line with \n in it`},
			hunkLines: []string{"line with \n in it"},
			startHint: 0,
			wantStart: 0,
			wantOK:    true,
		},
		{
			name:      "反斜杠t_vs_制表符",
			fileLines: []string{`key\tvalue`},
			hunkLines: []string{"key\tvalue"},
			startHint: 0,
			wantStart: 0,
			wantOK:    true,
		},
		{
			name:      "反斜杠双引号_vs_双引号",
			fileLines: []string{`say \"hello\"`},
			hunkLines: []string{`say "hello"`},
			startHint: 0,
			wantStart: 0,
			wantOK:    true,
		},
		{
			name:      "混合转义_双反斜杠和转义引号",
			fileLines: []string{`path\\dir`, `say \"hi\" and \'bye\'`},
			hunkLines: []string{`path\dir`, `say "hi" and 'bye'`},
			startHint: 0,
			wantStart: 0,
			wantOK:    true,
		},
		{
			name:      "无转义差异_不会错误匹配",
			fileLines: []string{"hello world"},
			hunkLines: []string{"goodbye world"},
			startHint: 0,
			wantStart: -1,
			wantOK:    false,
		},
		{
			name:      "搜索回退_找到正确位置",
			fileLines: []string{"other", `key\tvalue`, "more"},
			hunkLines: []string{"key\tvalue"},
			startHint: -1,
			wantStart: 1,
			wantOK:    true,
		},
		{
			name:      "空hunk行_返回hint位置",
			fileLines: []string{"line1"},
			hunkLines: []string{},
			startHint: 0,
			wantStart: 0,
			wantOK:    true,
		},
	}

	r := &EscapeNormalizedReplacer{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			start, ok := r.Match(tt.fileLines, tt.hunkLines, tt.startHint)
			if ok != tt.wantOK {
				t.Errorf("ok = %v, 期望 %v", ok, tt.wantOK)
			}
			if ok && start != tt.wantStart {
				t.Errorf("startLine = %d, 期望 %d", start, tt.wantStart)
			}
		})
	}
}

// TestLevenshteinDistance 测试编辑距离计算
func TestLevenshteinDistance(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"", "", 0},
		{"abc", "", 3},
		{"", "abc", 3},
		{"abc", "abc", 0},
		{"abc", "abd", 1},
		{"kitten", "sitting", 3},
		{"sunday", "saturday", 3},
	}

	for _, tt := range tests {
		got := levenshteinDistance(tt.a, tt.b, 0)
		if got != tt.want {
			t.Errorf("levenshteinDistance(%q, %q) = %d, 期望 %d", tt.a, tt.b, got, tt.want)
		}
	}
}

// TestLevenshteinSimilarity 测试相似度计算
func TestLevenshteinSimilarity(t *testing.T) {
	tests := []struct {
		a, b    string
		wantMin float64
		wantMax float64
	}{
		{"", "", 1.0, 1.0},
		{"abc", "abc", 1.0, 1.0},
		{"abc", "abd", 0.6, 0.7}, // 距离 1，长度 3 → 1 - 1/3 ≈ 0.667
		{"hello", "world", 0.0, 0.3},
	}

	for _, tt := range tests {
		got := levenshteinSimilarity(tt.a, tt.b)
		if got < tt.wantMin || got > tt.wantMax {
			t.Errorf("levenshteinSimilarity(%q, %q) = %f, 期望在 [%f, %f] 范围内", tt.a, tt.b, got, tt.wantMin, tt.wantMax)
		}
	}
}

// TestNormalizeEscapes 测试转义归一化函数
func TestNormalizeEscapes(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{`hello\nworld`, "hello\nworld"},
		{`key\tvalue`, "key\tvalue"},
		{`say \"hi\"`, `say "hi"`},
		{`it\'s`, "it's"},
		{`path\\file`, `path\file`},
		{`\u0041\u0042`, "AB"},
		{`no escapes here`, `no escapes here`},
		{`mixed\n\t\"`, "mixed\n\t\""},
		{`trailing backslash\`, `trailing backslash\`},
		{`\u004`, `\u004`}, // 不完整的 unicode 转义，保持原样
	}

	for _, tt := range tests {
		got := normalizeEscapes(tt.input)
		if got != tt.want {
			t.Errorf("normalizeEscapes(%q) = %q, 期望 %q", tt.input, got, tt.want)
		}
	}
}

// TestFuzzyFindString_BlockAnchor 测试字符串级别的首尾锚点匹配
func TestFuzzyFindString_BlockAnchor(t *testing.T) {
	logger := zap.NewNop()

	content := "func main() {\n    fmt.Println(\"hello\")\n    fmt.Println(\"world\")\n    fmt.Println(\"done\")\n}\n"
	// 中间行略有差异，但首尾行相同
	oldString := "func main() {\n    fmt.Println(\"hi\")\n    fmt.Println(\"world\")\n    fmt.Println(\"done\")\n}"

	found, level, ok := FuzzyFindString(content, oldString, logger)
	if !ok {
		t.Fatal("期望匹配成功")
	}
	if level != MatchBlockAnchor {
		t.Errorf("匹配级别 = %v (%s), 期望 BlockAnchor", level, matchLevelNames[level])
	}
	// 应该返回文件中的实际内容
	if found != "func main() {\n    fmt.Println(\"hello\")\n    fmt.Println(\"world\")\n    fmt.Println(\"done\")\n}" {
		t.Errorf("返回的字符串不正确: %q", found)
	}
}

// TestFuzzyFindString_EscapeNormalized 测试字符串级别的转义归一化匹配
func TestFuzzyFindString_EscapeNormalized(t *testing.T) {
	logger := zap.NewNop()

	// 文件中某行包含 \" （literal backslash + quote），oldString 中是普通双引号
	// 使用原始字符串确保 backslash 是 literal 的
	content := "line1\n" + `fmt.Println(\"hello\")` + "\nline3\n"
	oldString := `fmt.Println("hello")`

	found, level, ok := FuzzyFindString(content, oldString, logger)
	if !ok {
		t.Fatal("期望匹配成功")
	}
	if level != MatchEscapeNormalized {
		t.Errorf("匹配级别 = %v (%s), 期望 EscapeNormalized", level, matchLevelNames[level])
	}
	_ = found // 只验证匹配成功和级别
}

// TestTrimmedBoundaryReplacer_Match 测试边界Trim匹配器
func TestTrimmedBoundaryReplacer_Match(t *testing.T) {
	tests := []struct {
		name      string
		fileLines []string
		hunkLines []string
		startHint int
		wantStart int
		wantOK    bool
	}{
		{
			name:      "首尾行有空白差异_中间行精确匹配_成功",
			fileLines: []string{"  func main() {", "    doA()", "    doB()", "  }"},
			hunkLines: []string{"func main() {", "    doA()", "    doB()", "}"},
			startHint: -1,
			wantStart: 0,
			wantOK:    true,
		},
		{
			name:      "中间行有差异_失败",
			fileLines: []string{"  func main() {", "    doA()", "    doB()", "  }"},
			hunkLines: []string{"func main() {", "    doX()", "    doB()", "}"},
			startHint: -1,
			wantStart: -1,
			wantOK:    false,
		},
		{
			name:      "少于2行_跳过",
			fileLines: []string{"line1", "line2"},
			hunkLines: []string{"line1"},
			startHint: -1,
			wantStart: -1,
			wantOK:    false,
		},
		{
			name:      "恰好2行_首尾trim匹配_成功",
			fileLines: []string{"  hello  ", "  world  "},
			hunkLines: []string{"hello", "world"},
			startHint: -1,
			wantStart: 0,
			wantOK:    true,
		},
		{
			name:      "中间位置匹配_偏移正确",
			fileLines: []string{"header", "  func() {", "    body()", "  }", "footer"},
			hunkLines: []string{"func() {", "    body()", "}"},
			startHint: -1,
			wantStart: 1,
			wantOK:    true,
		},
	}

	r := &TrimmedBoundaryReplacer{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			start, ok := r.Match(tt.fileLines, tt.hunkLines, tt.startHint)
			if ok != tt.wantOK {
				t.Errorf("ok = %v, 期望 %v", ok, tt.wantOK)
			}
			if ok && start != tt.wantStart {
				t.Errorf("startLine = %d, 期望 %d", start, tt.wantStart)
			}
		})
	}
}

// TestContextAwareReplacer_Match 测试上下文感知匹配器
func TestContextAwareReplacer_Match(t *testing.T) {
	tests := []struct {
		name      string
		fileLines []string
		hunkLines []string
		startHint int
		wantStart int
		wantOK    bool
	}{
		{
			name: "首尾锚点匹配_中间60%行匹配_成功",
			fileLines: []string{
				"func main() {",
				"    lineA()",
				"    lineB()",
				"    lineC()",
				"    lineD()",
				"    lineE()",
				"}",
			},
			// 首尾锚点相同，中间 5 行中 3 行匹配 (60%)
			hunkLines: []string{
				"func main() {",
				"    lineA()",
				"    lineB()",
				"    lineX()", // 不匹配
				"    lineD()",
				"    lineY()", // 不匹配
				"}",
			},
			startHint: -1,
			wantStart: 0,
			wantOK:    true,
		},
		{
			name: "首尾锚点匹配_中间30%行匹配_失败",
			fileLines: []string{
				"func main() {",
				"    lineA()",
				"    lineB()",
				"    lineC()",
				"    lineD()",
				"    lineE()",
				"    lineF()",
				"    lineG()",
				"    lineH()",
				"    lineI()",
				"    lineJ()",
				"}",
			},
			// 首尾锚点相同，中间 10 行中 3 行匹配 (30%)
			hunkLines: []string{
				"func main() {",
				"    lineA()",
				"    lineB()",
				"    lineC()",
				"    xx1()",
				"    xx2()",
				"    xx3()",
				"    xx4()",
				"    xx5()",
				"    xx6()",
				"    xx7()",
				"}",
			},
			startHint: -1,
			wantStart: -1,
			wantOK:    false,
		},
		{
			name:      "少于3行_跳过",
			fileLines: []string{"line1", "line2"},
			hunkLines: []string{"line1", "line2"},
			startHint: -1,
			wantStart: -1,
			wantOK:    false,
		},
		{
			name: "首行锚点不匹配_失败",
			fileLines: []string{
				"func run() {",
				"    lineA()",
				"    lineB()",
				"}",
			},
			hunkLines: []string{
				"func main() {",
				"    lineA()",
				"    lineB()",
				"}",
			},
			startHint: -1,
			wantStart: -1,
			wantOK:    false,
		},
	}

	r := &ContextAwareReplacer{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			start, ok := r.Match(tt.fileLines, tt.hunkLines, tt.startHint)
			if ok != tt.wantOK {
				t.Errorf("ok = %v, 期望 %v", ok, tt.wantOK)
			}
			if ok && start != tt.wantStart {
				t.Errorf("startLine = %d, 期望 %d", start, tt.wantStart)
			}
		})
	}
}

// TestMultiOccurrenceReplacer_Match 测试多处出现匹配器
func TestMultiOccurrenceReplacer_Match(t *testing.T) {
	tests := []struct {
		name      string
		fileLines []string
		hunkLines []string
		startHint int
		wantStart int
		wantOK    bool
	}{
		{
			name:      "文件中有2处匹配_返回第一处",
			fileLines: []string{"fmt.Println()", "other", "fmt.Println()", "end"},
			hunkLines: []string{"fmt.Println()"},
			startHint: -1,
			wantStart: 0,
			wantOK:    true,
		},
		{
			name:      "文件中有1处匹配_返回该处",
			fileLines: []string{"header", "target_line", "footer"},
			hunkLines: []string{"target_line"},
			startHint: -1,
			wantStart: 1,
			wantOK:    true,
		},
		{
			name:      "文件中无匹配_失败",
			fileLines: []string{"line1", "line2", "line3"},
			hunkLines: []string{"not_found"},
			startHint: -1,
			wantStart: -1,
			wantOK:    false,
		},
		{
			name:      "多行块有2处匹配_返回第一处",
			fileLines: []string{"a", "b", "c", "a", "b", "d"},
			hunkLines: []string{"a", "b"},
			startHint: -1,
			wantStart: 0,
			wantOK:    true,
		},
		{
			name:      "空hunk行_返回hint位置",
			fileLines: []string{"line1"},
			hunkLines: []string{},
			startHint: 0,
			wantStart: 0,
			wantOK:    true,
		},
	}

	r := &MultiOccurrenceReplacer{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			start, ok := r.Match(tt.fileLines, tt.hunkLines, tt.startHint)
			if ok != tt.wantOK {
				t.Errorf("ok = %v, 期望 %v", ok, tt.wantOK)
			}
			if ok && start != tt.wantStart {
				t.Errorf("startLine = %d, 期望 %d", start, tt.wantStart)
			}
		})
	}
}
