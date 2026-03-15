package core

import (
	"fmt"
	"strings"
	"testing"
)

func TestMarkdownToSimpleHTML_Bold(t *testing.T) {
	out := MarkdownToSimpleHTML("hello **world**")
	if !strings.Contains(out, "<b>world</b>") {
		t.Errorf("expected <b>world</b>, got %q", out)
	}
}

func TestMarkdownToSimpleHTML_Italic(t *testing.T) {
	out := MarkdownToSimpleHTML("hello *world*")
	if !strings.Contains(out, "<i>world</i>") {
		t.Errorf("expected <i>world</i>, got %q", out)
	}
}

func TestMarkdownToSimpleHTML_Strikethrough(t *testing.T) {
	out := MarkdownToSimpleHTML("hello ~~world~~")
	if !strings.Contains(out, "<s>world</s>") {
		t.Errorf("expected <s>world</s>, got %q", out)
	}
}

func TestMarkdownToSimpleHTML_InlineCode(t *testing.T) {
	out := MarkdownToSimpleHTML("run `echo hello`")
	if !strings.Contains(out, "<code>echo hello</code>") {
		t.Errorf("expected <code>echo hello</code>, got %q", out)
	}
}

func TestMarkdownToSimpleHTML_CodeBlock(t *testing.T) {
	md := "```go\nfmt.Println()\n```"
	out := MarkdownToSimpleHTML(md)
	if !strings.Contains(out, `<pre><code class="language-go">`) {
		t.Errorf("expected language-go code block, got %q", out)
	}
	if !strings.Contains(out, "fmt.Println()") {
		t.Errorf("expected code content, got %q", out)
	}
}

func TestMarkdownToSimpleHTML_Link(t *testing.T) {
	out := MarkdownToSimpleHTML("visit [Google](https://google.com)")
	if !strings.Contains(out, `<a href="https://google.com">Google</a>`) {
		t.Errorf("expected link HTML, got %q", out)
	}
}

func TestMarkdownToSimpleHTML_Heading(t *testing.T) {
	out := MarkdownToSimpleHTML("## Section Title")
	if !strings.Contains(out, "<b>Section Title</b>") {
		t.Errorf("expected heading as bold, got %q", out)
	}
}

func TestMarkdownToSimpleHTML_Blockquote(t *testing.T) {
	out := MarkdownToSimpleHTML("> quoted text")
	if !strings.Contains(out, "<blockquote>quoted text</blockquote>") {
		t.Errorf("expected blockquote, got %q", out)
	}
}

func TestMarkdownToSimpleHTML_EscapesHTML(t *testing.T) {
	out := MarkdownToSimpleHTML("x < y && y > z")
	if !strings.Contains(out, "&lt;") || !strings.Contains(out, "&gt;") || !strings.Contains(out, "&amp;") {
		t.Errorf("HTML special chars should be escaped, got %q", out)
	}
}

func TestMarkdownToSimpleHTML_EscapesInsideBold(t *testing.T) {
	out := MarkdownToSimpleHTML("**x < y**")
	if !strings.Contains(out, "<b>x &lt; y</b>") {
		t.Errorf("expected escaped content inside bold, got %q", out)
	}
}

func TestMarkdownToSimpleHTML_LinkWithAmpersand(t *testing.T) {
	out := MarkdownToSimpleHTML("click [here](https://example.com?a=1&b=2)")
	if !strings.Contains(out, "&amp;b=2") {
		t.Errorf("URL ampersand should be escaped, got %q", out)
	}
	if !strings.Contains(out, `<a href=`) {
		t.Errorf("expected link tag, got %q", out)
	}
}

func TestMarkdownToSimpleHTML_LinkWithQuotesInURL(t *testing.T) {
	out := MarkdownToSimpleHTML(`visit [book](https://example.com/q="test")`)
	if strings.Contains(out, `href="https://example.com/q="`) {
		t.Errorf("unescaped quote in href attribute, got %q", out)
	}
	if !strings.Contains(out, `&quot;`) {
		t.Errorf("expected escaped quote in URL, got %q", out)
	}
	if err := validateHTMLNesting(out); err != nil {
		t.Errorf("invalid HTML: %v, got %q", err, out)
	}
}

func TestMarkdownToSimpleHTML_EscapesQuotesInText(t *testing.T) {
	out := MarkdownToSimpleHTML(`He said "hello" world`)
	if strings.Contains(out, `"hello"`) {
		t.Errorf("quotes in text should be escaped, got %q", out)
	}
	if !strings.Contains(out, `&quot;hello&quot;`) {
		t.Errorf("expected &quot; in output, got %q", out)
	}
}

func TestMarkdownToSimpleHTML_CodeBlockEscapesHTML(t *testing.T) {
	md := "```\nif a < b && c > d {\n}\n```"
	out := MarkdownToSimpleHTML(md)
	if !strings.Contains(out, "&lt;") || !strings.Contains(out, "&gt;") {
		t.Errorf("code block content should be HTML-escaped, got %q", out)
	}
}

func TestMarkdownToSimpleHTML_InlineCodeEscapesHTML(t *testing.T) {
	out := MarkdownToSimpleHTML("run `x<y>z`")
	if !strings.Contains(out, "<code>x&lt;y&gt;z</code>") {
		t.Errorf("inline code should escape HTML, got %q", out)
	}
}

func TestMarkdownToSimpleHTML_MixedFormattingWithSpecialChars(t *testing.T) {
	out := MarkdownToSimpleHTML("**bold** & *italic* < normal")
	if !strings.Contains(out, "<b>bold</b>") {
		t.Errorf("expected bold tag, got %q", out)
	}
	if !strings.Contains(out, "&amp;") {
		t.Errorf("expected escaped &, got %q", out)
	}
	if !strings.Contains(out, "&lt;") {
		t.Errorf("expected escaped <, got %q", out)
	}
}

func TestMarkdownToSimpleHTML_NoCrossedTags(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"bold then italic", "**bold *text***"},
		{"italic around bold", "*italic **bold** more*"},
		{"heading with bold", "## **important** heading"},
		{"heading with italic", "## *weather* report"},
		{"mixed line", "**北京** *晴天* 25°C"},
		{"triple star", "***bold italic***"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := MarkdownToSimpleHTML(tt.input)
			if err := validateHTMLNesting(out); err != nil {
				t.Errorf("crossed tags in output %q: %v", out, err)
			}
		})
	}
}

func validateHTMLNesting(html string) error {
	var stack []string
	i := 0
	for i < len(html) {
		if html[i] != '<' {
			i++
			continue
		}
		end := strings.Index(html[i:], ">")
		if end < 0 {
			break
		}
		tag := html[i+1 : i+end]
		i += end + 1
		if strings.HasPrefix(tag, "/") {
			closing := tag[1:]
			if sp := strings.IndexByte(closing, ' '); sp > 0 {
				closing = closing[:sp]
			}
			if len(stack) == 0 {
				return fmt.Errorf("unexpected closing tag </%s>", closing)
			}
			top := stack[len(stack)-1]
			if top != closing {
				return fmt.Errorf("expected </%s>, found </%s>", top, closing)
			}
			stack = stack[:len(stack)-1]
		} else {
			name := tag
			if sp := strings.IndexByte(name, ' '); sp > 0 {
				name = name[:sp]
			}
			stack = append(stack, name)
		}
	}
	return nil
}

func TestMarkdownToSimpleHTML_UnorderedList(t *testing.T) {
	md := "Items:\n- first item\n- second item\n- third item"
	out := MarkdownToSimpleHTML(md)
	if !strings.Contains(out, "• first item") {
		t.Errorf("expected bullet for unordered list, got %q", out)
	}
	if !strings.Contains(out, "• second item") {
		t.Errorf("expected bullet for second item, got %q", out)
	}
}

func TestMarkdownToSimpleHTML_UnorderedListAsterisk(t *testing.T) {
	md := "* one\n* two"
	out := MarkdownToSimpleHTML(md)
	if !strings.Contains(out, "• one") {
		t.Errorf("expected bullet for asterisk list, got %q", out)
	}
}

func TestMarkdownToSimpleHTML_OrderedList(t *testing.T) {
	md := "Steps:\n1. first\n2. second\n3. third"
	out := MarkdownToSimpleHTML(md)
	if !strings.Contains(out, "1.") || !strings.Contains(out, "first") {
		t.Errorf("expected ordered list items, got %q", out)
	}
	if !strings.Contains(out, "2.") || !strings.Contains(out, "second") {
		t.Errorf("expected ordered list items, got %q", out)
	}
}

func TestMarkdownToSimpleHTML_ListWithInlineFormatting(t *testing.T) {
	md := "- **bold item**\n- `code item`\n- *italic item*"
	out := MarkdownToSimpleHTML(md)
	if !strings.Contains(out, "• <b>bold item</b>") {
		t.Errorf("expected bold in list item, got %q", out)
	}
	if !strings.Contains(out, "• <code>code item</code>") {
		t.Errorf("expected code in list item, got %q", out)
	}
	if err := validateHTMLNesting(out); err != nil {
		t.Errorf("invalid HTML nesting: %v, got %q", err, out)
	}
}

func TestMarkdownToSimpleHTML_NestedList(t *testing.T) {
	md := "- top\n  - nested\n    - deep"
	out := MarkdownToSimpleHTML(md)
	if !strings.Contains(out, "• top") {
		t.Errorf("expected top-level bullet, got %q", out)
	}
	if !strings.Contains(out, "  • nested") {
		t.Errorf("expected indented nested bullet, got %q", out)
	}
	if !strings.Contains(out, "    • deep") {
		t.Errorf("expected double-indented deep bullet, got %q", out)
	}
}

func TestMarkdownToSimpleHTML_GeminiTypicalOutput(t *testing.T) {
	md := `## Analysis Results

Here are the findings:

- **File structure**: The project has 3 main directories
- **Dependencies**: All up to date
- **Tests**: 15 passing, 0 failing

### Recommendations

1. Update the ` + "`README.md`" + ` file
2. Add **error handling** to the main function
3. Consider using ~~deprecated~~ updated API

> Note: This is an automated analysis

For more info, visit [docs](https://example.com).`

	out := MarkdownToSimpleHTML(md)

	if !strings.Contains(out, "<b>Analysis Results</b>") {
		t.Error("heading should be bold")
	}
	if !strings.Contains(out, "• <b>File structure</b>") {
		t.Errorf("list item with bold not converted properly, got %q", out)
	}
	if !strings.Contains(out, "<blockquote>") {
		t.Error("blockquote should be present")
	}
	if !strings.Contains(out, `<a href=`) {
		t.Error("link should be present")
	}
	if err := validateHTMLNesting(out); err != nil {
		t.Errorf("invalid HTML nesting: %v\nfull output: %q", err, out)
	}
}

func TestMarkdownToSimpleHTML_CodeBlockWithHTMLTags(t *testing.T) {
	md := "```html\n<div class=\"test\">\n  <p>Hello</p>\n</div>\n```"
	out := MarkdownToSimpleHTML(md)
	if !strings.Contains(out, "&lt;div") {
		t.Errorf("HTML tags in code block should be escaped, got %q", out)
	}
	if err := validateHTMLNesting(out); err != nil {
		t.Errorf("invalid HTML: %v, got %q", err, out)
	}
}

func TestMarkdownToSimpleHTML_HorizontalRule(t *testing.T) {
	out := MarkdownToSimpleHTML("before\n---\nafter")
	if !strings.Contains(out, "——————————") {
		t.Errorf("expected wide horizontal rule, got %q", out)
	}
}

func TestMarkdownToSimpleHTML_UnclosedCodeBlock(t *testing.T) {
	md := "```python\nprint('hello')\nprint('world')"
	out := MarkdownToSimpleHTML(md)
	if !strings.Contains(out, "print") {
		t.Errorf("unclosed code block content should still appear, got %q", out)
	}
	if !strings.Contains(out, "<pre><code>") {
		t.Errorf("unclosed code block should still get code tags, got %q", out)
	}
}

func TestMarkdownToSimpleHTML_MultiLineBlockquote(t *testing.T) {
	md := "> line 1\n> line 2\n> line 3"
	out := MarkdownToSimpleHTML(md)
	if strings.Count(out, "<blockquote>") != 1 {
		t.Errorf("expected single blockquote, got %q", out)
	}
	if !strings.Contains(out, "line 1\nline 2\nline 3") {
		t.Errorf("expected all lines joined in blockquote, got %q", out)
	}
}

func TestMarkdownToSimpleHTML_BlockquoteBreaksOnBlankLine(t *testing.T) {
	md := "> quote 1\n\n> quote 2"
	out := MarkdownToSimpleHTML(md)
	if strings.Count(out, "<blockquote>") != 2 {
		t.Errorf("blank line should create separate blockquotes, got %q", out)
	}
}

func TestMarkdownToSimpleHTML_Table(t *testing.T) {
	md := "| Name | Age |\n|------|-----|\n| Alice | 30 |\n| Bob | 25 |"
	out := MarkdownToSimpleHTML(md)
	// Should use <pre> for plain-text tables.
	if !strings.Contains(out, "<pre>") {
		t.Errorf("expected <pre> block for plain table, got %q", out)
	}
	if !strings.Contains(out, "Name") || !strings.Contains(out, "Age") {
		t.Errorf("expected table header cells, got %q", out)
	}
	if !strings.Contains(out, "Alice") || !strings.Contains(out, "30") {
		t.Errorf("expected table data cells, got %q", out)
	}
	// Should have separator line with dashes.
	if !strings.Contains(out, "-----") {
		t.Errorf("expected separator with dashes, got %q", out)
	}
}

func TestMarkdownToSimpleHTML_TableAligned(t *testing.T) {
	md := "| Name | Age |\n|------|-----|\n| Alice | 30 |\n| Bob | 25 |"
	out := MarkdownToSimpleHTML(md)
	// Columns should be padded to equal width.
	lines := strings.Split(out, "\n")
	// Find data lines inside <pre> block.
	var dataLines []string
	inPre := false
	for _, l := range lines {
		if strings.Contains(l, "<pre>") {
			inPre = true
			l = strings.TrimPrefix(l, "<pre>")
		}
		if strings.Contains(l, "</pre>") {
			l = strings.TrimSuffix(l, "</pre>")
			if inPre && l != "" {
				dataLines = append(dataLines, l)
			}
			inPre = false
			continue
		}
		if inPre {
			dataLines = append(dataLines, l)
		}
	}
	if len(dataLines) < 3 {
		t.Fatalf("expected at least 3 lines (header+sep+data), got %d: %v", len(dataLines), dataLines)
	}
	// All non-separator lines should have the same display width.
	firstWidth := -1
	for _, dl := range dataLines {
		if strings.Contains(dl, "---") || strings.Contains(dl, "-+-") {
			continue
		}
		w := stringDisplayWidth(dl)
		if firstWidth < 0 {
			firstWidth = w
		} else if w != firstWidth {
			t.Errorf("lines have different widths: %d vs %d\nlines: %v", firstWidth, w, dataLines)
		}
	}
}

func TestMarkdownToSimpleHTML_TableWithFormatting(t *testing.T) {
	md := "| **Header** | `code` |\n|---|---|\n| *italic* | normal |"
	out := MarkdownToSimpleHTML(md)
	// Should NOT use <pre> when cells have formatting.
	if strings.Contains(out, "<pre>") {
		t.Errorf("expected no <pre> for table with formatting, got %q", out)
	}
	if !strings.Contains(out, "<b>Header</b>") {
		t.Errorf("expected bold in table cell, got %q", out)
	}
	if !strings.Contains(out, "<code>code</code>") {
		t.Errorf("expected code in table cell, got %q", out)
	}
	// Header row should be wrapped in bold.
	if !strings.Contains(out, "<b>") {
		t.Errorf("expected bold header row, got %q", out)
	}
	if !strings.Contains(out, "——————————") {
		t.Errorf("expected separator line, got %q", out)
	}
	if err := validateHTMLNesting(out); err != nil {
		t.Errorf("invalid HTML nesting: %v, got %q", err, out)
	}
}

func TestMarkdownToSimpleHTML_TableNoOuterPipes(t *testing.T) {
	md := "| A | B |\n|---|---|\n| 1 | 2 |"
	out := MarkdownToSimpleHTML(md)
	// The rendered table should not have leading/trailing pipes.
	if strings.Contains(out, "| A") && strings.Index(out, "| A") > 0 && out[strings.Index(out, "| A")-1] != ' ' {
		// This is fine — it's the separator between columns.
	}
	// Check no outer pipes: content should be "A | B" not "| A | B |".
	if strings.Contains(out, "| A |") {
		t.Errorf("should not have outer pipes, got %q", out)
	}
}

func TestMarkdownToSimpleHTML_TableWide(t *testing.T) {
	// Table wider than maxPreTableWidth should truncate cells.
	md := "| Name | Very Long Description Column | Status |\n|---|---|---|\n| Alice | This is a really long text value | Active |"
	out := MarkdownToSimpleHTML(md)
	if !strings.Contains(out, "<pre>") {
		t.Errorf("expected <pre> even for wide table (should truncate), got %q", out)
	}
	// Should contain truncation marker.
	if !strings.Contains(out, "…") {
		t.Errorf("expected truncation marker '…' for wide table, got %q", out)
	}
}

func TestMarkdownToSimpleHTML_TableFlatten(t *testing.T) {
	// Very wide table (>60 natural width) should be flattened to bulleted list.
	md := "| | РИИЛ | RED | МТС Супер | Дилер 2.0 |\n|---|---|---|---|---|\n| Цена (промо) | 390₽/мес (3 мес) | 500₽/мес (3 мес) | 650₽/мес (1 год) | 207–367₽/мес |\n| Интернет | Безлимит | Безлимит | Безлимит | Безлимит |"
	out := MarkdownToSimpleHTML(md)
	// Should NOT use <pre> — too wide.
	if strings.Contains(out, "<pre>") {
		t.Errorf("very wide table should be flattened, not <pre>, got %q", out)
	}
	// Should have bullet points.
	if !strings.Contains(out, "•") {
		t.Errorf("expected bullet points in flattened table, got %q", out)
	}
	// Each data row becomes a bold bullet label.
	if !strings.Contains(out, "• <b>Цена (промо)</b>") {
		t.Errorf("expected bold bullet label for data row, got %q", out)
	}
	// Blank line between records.
	if !strings.Contains(out, "\n\n•") {
		t.Errorf("expected blank line between records, got %q", out)
	}
	// Header values used as labels.
	if !strings.Contains(out, "РИИЛ: 390") {
		t.Errorf("expected 'Header: value' format, got %q", out)
	}
	// Full content preserved (not truncated).
	if !strings.Contains(out, "207–367₽/мес") {
		t.Errorf("expected full cell content (no truncation), got %q", out)
	}
}

func TestMarkdownToSimpleHTML_TableFlattenSkipsEmpty(t *testing.T) {
	// Flattened table should skip empty cells. Use long cell values to exceed 60 chars.
	md := "| Label | Column Alpha | Column Beta | Column Gamma | Column Delta | Column Epsilon |\n|---|---|---|---|---|---|\n| Data Row One | some value here |  | another value |  | final value here |"
	out := MarkdownToSimpleHTML(md)
	if !strings.Contains(out, "•") {
		t.Errorf("expected flattened format, got %q", out)
	}
	// Empty cells (Column Beta, Column Delta) should be omitted.
	if strings.Contains(out, "Column Beta:") {
		t.Errorf("empty cells should be skipped, got %q", out)
	}
	if strings.Contains(out, "Column Delta:") {
		t.Errorf("empty cells should be skipped, got %q", out)
	}
}

func TestMarkdownToSimpleHTML_TableModerateWidth(t *testing.T) {
	// Table between 42-60 chars should use <pre> with truncation, not flatten.
	md := "| Name | Description | Status |\n|---|---|---|\n| Task A | Implementing feature | Active |\n| Task B | Bug fix for login | Done |"
	out := MarkdownToSimpleHTML(md)
	if !strings.Contains(out, "<pre>") {
		t.Errorf("moderate-width table should use <pre>, got %q", out)
	}
}

func TestMarkdownToSimpleHTML_TableEmptyCells(t *testing.T) {
	md := "| A | B |\n|---|---|\n| x |  |\n|  | y |"
	out := MarkdownToSimpleHTML(md)
	if !strings.Contains(out, "<pre>") {
		t.Errorf("expected <pre> for plain table, got %q", out)
	}
	if !strings.Contains(out, "x") && !strings.Contains(out, "y") {
		t.Errorf("expected cell content, got %q", out)
	}
}

func TestMarkdownToSimpleHTML_TableSingleColumn(t *testing.T) {
	md := "| Item |\n|------|\n| apple |\n| banana |"
	out := MarkdownToSimpleHTML(md)
	if !strings.Contains(out, "<pre>") {
		t.Errorf("expected <pre> for single-column table, got %q", out)
	}
	if !strings.Contains(out, "apple") || !strings.Contains(out, "banana") {
		t.Errorf("expected cell content, got %q", out)
	}
}

func TestMarkdownToSimpleHTML_TableCJK(t *testing.T) {
	md := "| 名前 | 年齢 |\n|------|------|\n| 太郎 | 30 |"
	out := MarkdownToSimpleHTML(md)
	if !strings.Contains(out, "<pre>") {
		t.Errorf("expected <pre> for CJK table, got %q", out)
	}
	if !strings.Contains(out, "太郎") || !strings.Contains(out, "30") {
		t.Errorf("expected CJK content, got %q", out)
	}
}

func TestStringDisplayWidth(t *testing.T) {
	tests := []struct {
		s    string
		want int
	}{
		{"hello", 5},
		{"太郎", 4},     // 2 CJK chars × 2 = 4
		{"A太郎B", 6},   // 1 + 2 + 2 + 1 = 6
		{"", 0},
		{"abc", 3},
	}
	for _, tt := range tests {
		got := stringDisplayWidth(tt.s)
		if got != tt.want {
			t.Errorf("stringDisplayWidth(%q) = %d, want %d", tt.s, got, tt.want)
		}
	}
}

func TestTruncateToWidth(t *testing.T) {
	tests := []struct {
		s        string
		maxWidth int
		wantLen  int // display width of result should be <= maxWidth
	}{
		{"hello world", 5, 5},
		{"short", 10, 5}, // no truncation needed
		{"太郎太郎", 5, 5},
	}
	for _, tt := range tests {
		got := truncateToWidth(tt.s, tt.maxWidth)
		w := stringDisplayWidth(got)
		if w > tt.maxWidth {
			t.Errorf("truncateToWidth(%q, %d) = %q (width %d), exceeds max", tt.s, tt.maxWidth, got, w)
		}
	}
}

func TestHasMarkdownFormatting(t *testing.T) {
	tests := []struct {
		s    string
		want bool
	}{
		{"plain text", false},
		{"**bold**", true},
		{"`code`", true},
		{"~~strike~~", true},
		{"no formatting here", false},
	}
	for _, tt := range tests {
		got := hasMarkdownFormatting(tt.s)
		if got != tt.want {
			t.Errorf("hasMarkdownFormatting(%q) = %v, want %v", tt.s, got, tt.want)
		}
	}
}

func TestSplitMessageCodeFenceAware_Short(t *testing.T) {
	chunks := SplitMessageCodeFenceAware("hello", 100)
	if len(chunks) != 1 || chunks[0] != "hello" {
		t.Errorf("unexpected: %v", chunks)
	}
}

func TestSplitMessageCodeFenceAware_PreservesCodeBlock(t *testing.T) {
	lines := []string{
		"before",
		"```python",
		"print('hello')",
		"print('world')",
		"```",
		"after",
	}
	text := strings.Join(lines, "\n")

	chunks := SplitMessageCodeFenceAware(text, 30)
	if len(chunks) < 2 {
		t.Fatalf("expected multiple chunks, got %d", len(chunks))
	}

	full := strings.Join(chunks, "")
	if !strings.Contains(full, "print('hello')") {
		t.Error("content should be preserved")
	}
}

func TestSplitMessageCodeFenceAware_NoCodeBlock(t *testing.T) {
	text := strings.Repeat("abcdefghij\n", 20)
	chunks := SplitMessageCodeFenceAware(text, 50)
	if len(chunks) < 2 {
		t.Fatalf("expected multiple chunks, got %d", len(chunks))
	}
	for _, chunk := range chunks {
		if len(chunk) > 50 {
			t.Errorf("chunk exceeds max len: %d", len(chunk))
		}
	}
}
