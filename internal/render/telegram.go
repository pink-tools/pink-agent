package render

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	east "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"

	goldmarkext "github.com/yuin/goldmark/extension"
)

// Telegram converts markdown to Telegram HTML.
func Telegram(markdown string) string {
	md := goldmark.New(
		goldmark.WithExtensions(goldmarkext.GFM),
	)

	reader := text.NewReader([]byte(markdown))
	doc := md.Parser().Parse(reader, parser.WithContext(parser.NewContext()))

	var buf bytes.Buffer
	renderNode(&buf, doc, []byte(markdown), 0)

	return strings.TrimRight(buf.String(), "\n")
}

func renderNode(buf *bytes.Buffer, node ast.Node, source []byte, depth int) {
	switch n := node.(type) {
	case *ast.Document:
		renderChildren(buf, n, source, depth)

	case *ast.Heading:
		buf.WriteString("<b>")
		renderChildren(buf, n, source, depth)
		buf.WriteString("</b>\n")

	case *ast.Paragraph:
		renderChildren(buf, n, source, depth)
		// Add newline unless parent is a list item (handled by list)
		if _, ok := n.Parent().(*ast.ListItem); !ok {
			buf.WriteString("\n")
		}

	case *ast.TextBlock:
		renderChildren(buf, n, source, depth)

	case *ast.ThematicBreak:
		buf.WriteString("---\n")

	case *ast.CodeBlock:
		buf.WriteString("<pre>")
		writeEscapedLines(buf, n, source)
		buf.WriteString("</pre>\n")

	case *ast.FencedCodeBlock:
		lang := string(n.Language(source))
		if lang != "" {
			buf.WriteString(fmt.Sprintf("<pre><code class=\"language-%s\">", escapeHTML(lang)))
		} else {
			buf.WriteString("<pre><code>")
		}
		writeEscapedLines(buf, n, source)
		buf.WriteString("</code></pre>\n")

	case *ast.Blockquote:
		buf.WriteString("<blockquote>")
		// Render children but trim trailing newlines
		var inner bytes.Buffer
		renderChildren(&inner, n, source, depth)
		buf.WriteString(strings.TrimRight(inner.String(), "\n"))
		buf.WriteString("</blockquote>\n")

	case *ast.List:
		counter := n.Start
		for child := n.FirstChild(); child != nil; child = child.NextSibling() {
			if n.IsOrdered() {
				buf.WriteString(fmt.Sprintf("  %d. ", counter))
				counter++
			} else {
				buf.WriteString("  \u2022 ")
			}
			renderChildren(buf, child, source, depth+1)
			buf.WriteString("\n")
		}

	case *ast.ListItem:
		renderChildren(buf, n, source, depth)

	case *ast.Text:
		buf.WriteString(escapeHTML(string(n.Text(source))))
		if n.SoftLineBreak() {
			buf.WriteString("\n")
		}
		if n.HardLineBreak() {
			buf.WriteString("\n")
		}

	case *ast.String:
		buf.WriteString(escapeHTML(string(n.Value)))

	case *ast.CodeSpan:
		buf.WriteString("<code>")
		renderCodeSpanChildren(buf, n, source)
		buf.WriteString("</code>")

	case *ast.Emphasis:
		if n.Level == 2 {
			buf.WriteString("<b>")
			renderChildren(buf, n, source, depth)
			buf.WriteString("</b>")
		} else {
			buf.WriteString("<i>")
			renderChildren(buf, n, source, depth)
			buf.WriteString("</i>")
		}

	case *ast.Link:
		buf.WriteString(fmt.Sprintf("<a href=\"%s\">", escapeHTML(string(n.Destination))))
		renderChildren(buf, n, source, depth)
		buf.WriteString("</a>")

	case *ast.AutoLink:
		url := string(n.URL(source))
		buf.WriteString(fmt.Sprintf("<a href=\"%s\">%s</a>", escapeHTML(url), escapeHTML(url)))

	case *ast.Image:
		// Telegram doesn't support images in HTML — render as link
		buf.WriteString(fmt.Sprintf("<a href=\"%s\">", escapeHTML(string(n.Destination))))
		renderChildren(buf, n, source, depth)
		buf.WriteString("</a>")

	case *ast.RawHTML:
		// Pass through raw HTML (Telegram will ignore unsupported tags)
		for i := 0; i < n.Segments.Len(); i++ {
			seg := n.Segments.At(i)
			buf.Write(seg.Value(source))
		}

	case *ast.HTMLBlock:
		for i := 0; i < n.Lines().Len(); i++ {
			seg := n.Lines().At(i)
			buf.Write(seg.Value(source))
		}

	case *east.Strikethrough:
		buf.WriteString("<s>")
		renderChildren(buf, n, source, depth)
		buf.WriteString("</s>")

	case *east.Table:
		renderTable(buf, n, source)

	case *east.TableHeader, *east.TableRow, *east.TableCell:
		// Handled by renderTable
		return

	default:
		// Unknown node type — render children
		renderChildren(buf, n, source, depth)
	}
}

func renderChildren(buf *bytes.Buffer, node ast.Node, source []byte, depth int) {
	for child := node.FirstChild(); child != nil; child = child.NextSibling() {
		renderNode(buf, child, source, depth)
	}
}

func renderCodeSpanChildren(buf *bytes.Buffer, node ast.Node, source []byte) {
	for child := node.FirstChild(); child != nil; child = child.NextSibling() {
		if t, ok := child.(*ast.Text); ok {
			buf.WriteString(escapeHTML(string(t.Text(source))))
			if t.SoftLineBreak() || t.HardLineBreak() {
				buf.WriteString(" ")
			}
		}
	}
}

func writeEscapedLines(buf *bytes.Buffer, node ast.Node, source []byte) {
	lines := node.Lines()
	for i := 0; i < lines.Len(); i++ {
		seg := lines.At(i)
		buf.WriteString(escapeHTML(string(seg.Value(source))))
	}
}

func renderTable(buf *bytes.Buffer, table *east.Table, source []byte) {
	// Render tables as monospace pre block — Telegram has no table support
	var rows [][]string
	var maxCols int

	// Table children: TableHeader (contains cells directly), then TableRow nodes
	for child := table.FirstChild(); child != nil; child = child.NextSibling() {
		cells := extractRowCells(child, source)
		if len(cells) > maxCols {
			maxCols = len(cells)
		}
		rows = append(rows, cells)
	}

	if len(rows) == 0 {
		return
	}

	// Calculate column widths
	widths := make([]int, maxCols)
	for _, row := range rows {
		for i, cell := range row {
			if len(cell) > widths[i] {
				widths[i] = len(cell)
			}
		}
	}

	buf.WriteString("<pre>")
	for i, row := range rows {
		for j := 0; j < maxCols; j++ {
			cell := ""
			if j < len(row) {
				cell = row[j]
			}
			if j > 0 {
				buf.WriteString(" | ")
			}
			buf.WriteString(escapeHTML(cell))
			// Pad
			for k := len(cell); k < widths[j]; k++ {
				buf.WriteByte(' ')
			}
		}
		buf.WriteString("\n")
		// Separator after header
		if i == 0 && len(rows) > 1 {
			for j := 0; j < maxCols; j++ {
				if j > 0 {
					buf.WriteString("-+-")
				}
				for k := 0; k < widths[j]; k++ {
					buf.WriteByte('-')
				}
			}
			buf.WriteString("\n")
		}
	}
	buf.WriteString("</pre>\n")
}

func extractRowCells(row ast.Node, source []byte) []string {
	var cells []string
	for cell := row.FirstChild(); cell != nil; cell = cell.NextSibling() {
		var cellBuf bytes.Buffer
		for child := cell.FirstChild(); child != nil; child = child.NextSibling() {
			if t, ok := child.(*ast.Text); ok {
				cellBuf.WriteString(string(t.Text(source)))
			}
		}
		cells = append(cells, cellBuf.String())
	}
	return cells
}

func escapeHTML(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	return s
}
