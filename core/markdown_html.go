package core

import (
	"regexp"
	"strings"
)

// maxPreTableWidth is the maximum display width (in monospace characters) for
// tables rendered inside <pre> blocks. Wider tables are truncated or fall back
// to inline-formatted text. 42 fits iPhone 14+ and most Android devices.
const maxPreTableWidth = 42

// minColWidthForPre is the minimum display width per column in a <pre> table.
// If shrinking columns to fit maxPreTableWidth would make any column narrower
// than this, the table is "flattened" into a bulleted record list instead.
const minColWidthForPre = 7

// MarkdownToSimpleHTML converts common Markdown to a simplified HTML subset.
// Supported tags: <b>, <i>, <s>, <code>, <pre>, <a href="">, <blockquote>.
// Useful for platforms that accept a limited set of HTML (e.g. Telegram).
func MarkdownToSimpleHTML(md string) string {
	var b strings.Builder
	b.Grow(len(md) + len(md)/4)

	lines := strings.Split(md, "\n")
	inCodeBlock := false
	codeLang := ""
	var codeLines []string
	inBlockquote := false
	var bqLines []string
	inTable := false
	var tblLines []string

	// flushBlockquote merges buffered blockquote lines into a single <blockquote>.
	flushBlockquote := func() {
		if len(bqLines) == 0 {
			return
		}
		b.WriteString("<blockquote>")
		for j, ql := range bqLines {
			if j > 0 {
				b.WriteByte('\n')
			}
			b.WriteString(convertInlineHTML(ql))
		}
		b.WriteString("</blockquote>")
		bqLines = bqLines[:0]
		inBlockquote = false
	}

	// flushTable renders buffered table rows.
	// Branch A: plain text cells → aligned <pre> block (monospace).
	// Branch C: cells with markdown formatting → bold header + inline HTML.
	flushTable := func() {
		if len(tblLines) == 0 {
			return
		}

		// Parse rows into cells, skip separator row.
		var rows [][]string
		for _, tl := range tblLines {
			tl = strings.TrimSpace(tl)
			if reTableSep.MatchString(tl) {
				continue
			}
			inner := tl[1 : len(tl)-1]
			parts := strings.Split(inner, "|")
			for k := range parts {
				parts[k] = strings.TrimSpace(parts[k])
			}
			rows = append(rows, parts)
		}
		if len(rows) == 0 {
			tblLines = tblLines[:0]
			inTable = false
			return
		}

		// Strip markdown formatting from header cells — headers are
		// visually distinguished by the separator line, not by bold.
		for c := range rows[0] {
			rows[0][c] = stripMarkdownFormatting(rows[0][c])
		}

		// Determine column count from first row.
		nCols := len(rows[0])

		// Check if any DATA cell (not header) has markdown formatting.
		hasFormatting := false
		for _, row := range rows[1:] {
			for _, cell := range row {
				if hasMarkdownFormatting(cell) {
					hasFormatting = true
					break
				}
			}
			if hasFormatting {
				break
			}
		}

		// Compute max display width per column.
		colWidths := make([]int, nCols)
		for _, row := range rows {
			for c := 0; c < nCols && c < len(row); c++ {
				w := stringDisplayWidth(row[c])
				if w > colWidths[c] {
					colWidths[c] = w
				}
			}
		}

		// Total width: sum(colWidths) + (nCols-1)*3 for " | " separators.
		totalWidth := 0
		for _, w := range colWidths {
			totalWidth += w
		}
		totalWidth += (nCols - 1) * 3

		if hasFormatting && totalWidth <= maxPreTableWidth {
			// Formatting in data cells + fits on screen → inline HTML.
			renderTableInline(&b, rows)
		} else if totalWidth > maxPreTableWidth {
			// Wide table — try to shrink for <pre>.
			origWidths := make([]int, nCols)
			copy(origWidths, colWidths)
			shrinkColumns(colWidths, nCols, maxPreTableWidth)
			// Flatten only if a column that was wide enough got shrunk below minimum.
			tooNarrow := false
			for c, w := range colWidths {
				if w < minColWidthForPre && origWidths[c] >= minColWidthForPre {
					tooNarrow = true
					break
				}
			}
			if tooNarrow && len(rows) > 1 {
				renderTableFlat(&b, rows, hasFormatting)
			} else {
				renderTablePre(&b, rows, colWidths)
			}
		} else {
			// Fits within budget → <pre> as-is.
			renderTablePre(&b, rows, colWidths)
		}
		tblLines = tblLines[:0]
		inTable = false
	}

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)

		if strings.HasPrefix(trimmed, "```") {
			if !inCodeBlock {
				if inBlockquote {
					flushBlockquote()
					b.WriteByte('\n')
				}
				if inTable {
					flushTable()
					b.WriteByte('\n')
				}
				inCodeBlock = true
				codeLang = strings.TrimPrefix(trimmed, "```")
				codeLines = nil
			} else {
				inCodeBlock = false
				if codeLang != "" {
					b.WriteString("<pre><code class=\"language-" + escapeHTML(codeLang) + "\">")
				} else {
					b.WriteString("<pre><code>")
				}
				b.WriteString(escapeHTML(strings.Join(codeLines, "\n")))
				b.WriteString("</code></pre>")
				if i < len(lines)-1 {
					b.WriteByte('\n')
				}
			}
			continue
		}

		if inCodeBlock {
			codeLines = append(codeLines, line)
			continue
		}

		// Determine line type for blockquote/table buffering
		isQuote := strings.HasPrefix(trimmed, "> ") || trimmed == ">"
		isTable := len(trimmed) > 2 && trimmed[0] == '|' && trimmed[len(trimmed)-1] == '|'

		// Flush blockquote when leaving
		if !isQuote && inBlockquote {
			flushBlockquote()
			b.WriteByte('\n')
		}
		// Flush table when leaving
		if !isTable && inTable {
			flushTable()
			b.WriteByte('\n')
		}

		// Buffer blockquote lines into a single block
		if isQuote {
			quoteContent := strings.TrimPrefix(trimmed, "> ")
			if trimmed == ">" {
				quoteContent = ""
			}
			bqLines = append(bqLines, quoteContent)
			inBlockquote = true
			continue
		}

		// Buffer table lines
		if isTable {
			tblLines = append(tblLines, trimmed)
			inTable = true
			continue
		}

		// Headings → bold
		if heading := reHeading.FindString(line); heading != "" {
			rest := strings.TrimPrefix(line, heading)
			b.WriteString("<b>")
			b.WriteString(convertInlineHTML(rest))
			b.WriteString("</b>")
		} else if reHorizontal.MatchString(trimmed) {
			b.WriteString("——————————")
		} else if m := reUnorderedList.FindStringSubmatch(line); m != nil {
			indent := strings.Repeat("  ", len(m[1])/2)
			b.WriteString(indent + "• " + convertInlineHTML(m[2]))
		} else if m := reOrderedList.FindStringSubmatch(line); m != nil {
			indent := strings.Repeat("  ", len(m[1])/2)
			numDot := strings.TrimSpace(line[:len(line)-len(m[2])])
			b.WriteString(indent + escapeHTML(numDot) + " " + convertInlineHTML(m[2]))
		} else {
			b.WriteString(convertInlineHTML(line))
		}

		if i < len(lines)-1 {
			b.WriteByte('\n')
		}
	}

	// Flush any remaining buffered state
	if inBlockquote {
		flushBlockquote()
	}
	if inTable {
		flushTable()
	}
	if inCodeBlock && len(codeLines) > 0 {
		b.WriteString("<pre><code>")
		b.WriteString(escapeHTML(strings.Join(codeLines, "\n")))
		b.WriteString("</code></pre>")
	}

	return b.String()
}

var (
	reInlineCodeHTML = regexp.MustCompile("`([^`]+)`")
	reBoldAstHTML    = regexp.MustCompile(`\*\*(.+?)\*\*`)
	reBoldUndHTML    = regexp.MustCompile(`__(.+?)__`)
	reItalicAstHTML  = regexp.MustCompile(`(?:^|[^*])\*([^*]+?)\*(?:[^*]|$)`)
	reStrikeHTML     = regexp.MustCompile(`~~(.+?)~~`)
	reLinkHTML       = regexp.MustCompile(`\[([^\]]+)\]\(([^)]+)\)`)
	reUnorderedList  = regexp.MustCompile(`^(\s*)[-*]\s+(.*)$`)
	reOrderedList    = regexp.MustCompile(`^(\s*)\d+\.\s+(.*)$`)
	reTableSep       = regexp.MustCompile(`^\|[\s:|-]+\|$`)
)

// convertInlineHTML converts inline Markdown formatting to Telegram-compatible HTML.
//
// Each formatting pass (bold, strikethrough) protects its output as placeholders
// so that subsequent passes (italic) cannot match across HTML tag boundaries.
func convertInlineHTML(s string) string {
	type placeholder struct {
		key  string
		html string
	}
	var phs []placeholder
	phIdx := 0

	nextPH := func(html string) string {
		key := "\x00PH" + string(rune('0'+phIdx)) + "\x00"
		phs = append(phs, placeholder{key: key, html: html})
		phIdx++
		return key
	}

	// 1. Extract inline code → placeholder (content escaped)
	s = reInlineCodeHTML.ReplaceAllStringFunc(s, func(m string) string {
		inner := m[1 : len(m)-1]
		return nextPH("<code>" + escapeHTML(inner) + "</code>")
	})

	// 2. Extract links → placeholder (text & URL escaped)
	s = reLinkHTML.ReplaceAllStringFunc(s, func(m string) string {
		sm := reLinkHTML.FindStringSubmatch(m)
		if len(sm) < 3 {
			return m
		}
		return nextPH(`<a href="` + escapeHTML(sm[2]) + `">` + escapeHTML(sm[1]) + `</a>`)
	})

	// 3. HTML-escape the entire remaining text.
	s = escapeHTML(s)

	// 4. Bold → placeholder (so italic regex can't cross bold boundaries)
	s = reBoldAstHTML.ReplaceAllStringFunc(s, func(m string) string {
		inner := m[2 : len(m)-2]
		return nextPH("<b>" + inner + "</b>")
	})
	s = reBoldUndHTML.ReplaceAllStringFunc(s, func(m string) string {
		inner := m[2 : len(m)-2]
		return nextPH("<b>" + inner + "</b>")
	})

	// 5. Strikethrough → placeholder
	s = reStrikeHTML.ReplaceAllStringFunc(s, func(m string) string {
		inner := m[2 : len(m)-2]
		return nextPH("<s>" + inner + "</s>")
	})

	// 6. Italic (applied last, on text with bold/strike already protected)
	s = reItalicAstHTML.ReplaceAllStringFunc(s, func(m string) string {
		idx := strings.Index(m, "*")
		if idx < 0 {
			return m
		}
		lastIdx := strings.LastIndex(m, "*")
		if lastIdx <= idx {
			return m
		}
		return m[:idx] + "<i>" + m[idx+1:lastIdx] + "</i>" + m[lastIdx+1:]
	})

	// 7. Restore all placeholders (may be nested, so iterate until stable).
	for i := 0; i <= len(phs); i++ {
		changed := false
		for _, ph := range phs {
			if strings.Contains(s, ph.key) {
				s = strings.Replace(s, ph.key, ph.html, 1)
				changed = true
			}
		}
		if !changed {
			break
		}
	}

	return s
}

func escapeHTML(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, "\"", "&quot;")
	return s
}

// stripMarkdownFormatting removes markdown inline formatting markers from s.
func stripMarkdownFormatting(s string) string {
	s = strings.ReplaceAll(s, "**", "")
	s = strings.ReplaceAll(s, "~~", "")
	s = strings.ReplaceAll(s, "`", "")
	return s
}

// hasMarkdownFormatting reports whether s contains markdown inline formatting markers.
func hasMarkdownFormatting(s string) bool {
	return strings.Contains(s, "**") ||
		strings.Contains(s, "~~") ||
		strings.Contains(s, "`")
}

// runeDisplayWidth returns the display width of a rune in a monospace font.
// CJK and fullwidth characters occupy 2 columns; everything else occupies 1.
func runeDisplayWidth(r rune) int {
	if r >= 0x1100 &&
		((r <= 0x115F) || // Hangul Jamo
			(r >= 0x2E80 && r <= 0x9FFF) || // CJK
			(r >= 0xAC00 && r <= 0xD7AF) || // Hangul Syllables
			(r >= 0xF900 && r <= 0xFAFF) || // CJK Compatibility Ideographs
			(r >= 0xFE10 && r <= 0xFE6F) || // CJK Compatibility Forms
			(r >= 0xFF01 && r <= 0xFF60) || // Fullwidth Forms
			(r >= 0x20000 && r <= 0x2FA1F)) { // CJK Supplementary
		return 2
	}
	return 1
}

// stringDisplayWidth returns the total monospace display width of s.
func stringDisplayWidth(s string) int {
	w := 0
	for _, r := range s {
		w += runeDisplayWidth(r)
	}
	return w
}

// padRight pads s with spaces to reach the target display width.
func padRight(s string, targetWidth int) string {
	cur := stringDisplayWidth(s)
	if cur >= targetWidth {
		return s
	}
	return s + strings.Repeat(" ", targetWidth-cur)
}

// truncateToWidth truncates s to fit within maxWidth display columns,
// appending "…" if truncation occurs.
func truncateToWidth(s string, maxWidth int) string {
	if maxWidth < 2 {
		maxWidth = 2
	}
	if stringDisplayWidth(s) <= maxWidth {
		return s
	}
	// Reserve 1 column for "…".
	budget := maxWidth - 1
	var b strings.Builder
	w := 0
	for _, r := range s {
		rw := runeDisplayWidth(r)
		if w+rw > budget {
			break
		}
		b.WriteRune(r)
		w += rw
	}
	b.WriteRune('…')
	return b.String()
}

// shrinkColumns reduces column widths so the total table width fits within maxWidth.
// Total width = sum(colWidths) + (nCols-1)*3 for " | " separators.
func shrinkColumns(colWidths []int, nCols, maxWidth int) {
	separators := (nCols - 1) * 3
	budget := maxWidth - separators
	if budget < nCols {
		budget = nCols // at least 1 char per column
	}

	// Distribute budget: shrink widest columns first.
	for {
		total := 0
		for _, w := range colWidths {
			total += w
		}
		if total <= budget {
			break
		}
		// Find widest column.
		maxIdx := 0
		for i, w := range colWidths {
			if w > colWidths[maxIdx] {
				maxIdx = i
			}
		}
		if colWidths[maxIdx] <= 1 {
			break
		}
		colWidths[maxIdx]--
	}
}

// renderTablePre writes a <pre>-formatted aligned table.
func renderTablePre(b *strings.Builder, rows [][]string, colWidths []int) {
	nCols := len(colWidths)
	b.WriteString("<pre>")
	for i, row := range rows {
		if i > 0 {
			b.WriteByte('\n')
		}
		for c := 0; c < nCols; c++ {
			if c > 0 {
				b.WriteString(" | ")
			}
			cell := ""
			if c < len(row) {
				cell = row[c]
			}
			cell = truncateToWidth(cell, colWidths[c])
			b.WriteString(escapeHTML(padRight(cell, colWidths[c])))
		}
		// Insert separator after header (first row).
		if i == 0 && len(rows) > 1 {
			b.WriteByte('\n')
			for c := 0; c < nCols; c++ {
				if c > 0 {
					b.WriteString("-+-")
				}
				b.WriteString(strings.Repeat("-", colWidths[c]))
			}
		}
	}
	b.WriteString("</pre>")
}

// renderTableFlat writes a very wide table as a bulleted list of records.
// Each data row becomes a "• firstCol" entry with "  Header: value" lines.
// This avoids heavy truncation and preserves full cell content.
func renderTableFlat(b *strings.Builder, rows [][]string, hasFormatting bool) {
	if len(rows) < 2 {
		// Only header, no data rows — render inline.
		renderTableInline(b, rows)
		return
	}
	headers := rows[0]
	for i, row := range rows[1:] {
		if i > 0 {
			b.WriteString("\n\n")
		}
		// Use first cell as the bullet label.
		label := ""
		if len(row) > 0 {
			label = row[0]
		}
		if label == "" {
			label = headers[0]
		}
		b.WriteString("• <b>")
		if hasFormatting {
			b.WriteString(convertInlineHTML(label))
		} else {
			b.WriteString(escapeHTML(label))
		}
		b.WriteString("</b>")
		// Remaining cells as "  Header: value" lines.
		// Header names are always plain text (no bold even if markdown-formatted).
		for c := 1; c < len(headers) && c < len(row); c++ {
			val := row[c]
			if val == "" {
				continue
			}
			b.WriteByte('\n')
			b.WriteString("  ")
			b.WriteString(escapeHTML(stripMarkdownFormatting(headers[c])))
			b.WriteString(": ")
			if hasFormatting {
				b.WriteString(convertInlineHTML(val))
			} else {
				b.WriteString(escapeHTML(val))
			}
		}
	}
}

// renderTableInline writes a table with inline HTML formatting (Branch C).
// Header row is bold, separator is em-dashes.
func renderTableInline(b *strings.Builder, rows [][]string) {
	for i, row := range rows {
		if i > 0 {
			b.WriteByte('\n')
		}
		line := strings.Join(row, " | ")
		if i == 0 && len(rows) > 1 {
			// Bold header.
			b.WriteString("<b>")
			b.WriteString(convertInlineHTML(line))
			b.WriteString("</b>")
			b.WriteByte('\n')
			b.WriteString("——————————")
		} else {
			b.WriteString(convertInlineHTML(line))
		}
	}
}

// SplitMessageCodeFenceAware splits text into chunks respecting code fence boundaries.
// When a chunk boundary falls inside a code block, the fence is closed at the end of
// the chunk and re-opened at the start of the next chunk.
func SplitMessageCodeFenceAware(text string, maxLen int) []string {
	if len(text) <= maxLen {
		return []string{text}
	}

	lines := strings.Split(text, "\n")
	var chunks []string
	var current []string
	currentLen := 0
	openFence := "" // the ``` opening line, or "" if outside code block

	for _, line := range lines {
		lineLen := len(line) + 1 // +1 for newline

		if currentLen+lineLen > maxLen && len(current) > 0 {
			chunk := strings.Join(current, "\n")
			if openFence != "" {
				chunk += "\n```"
			}
			chunks = append(chunks, chunk)

			current = nil
			currentLen = 0
			if openFence != "" {
				current = append(current, openFence)
				currentLen = len(openFence) + 1
			}
		}

		current = append(current, line)
		currentLen += lineLen

		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			if openFence != "" {
				openFence = ""
			} else {
				openFence = trimmed
			}
		}
	}

	if len(current) > 0 {
		chunk := strings.Join(current, "\n")
		if openFence != "" {
			chunk += "\n```"
		}
		chunks = append(chunks, chunk)
	}

	return chunks
}
