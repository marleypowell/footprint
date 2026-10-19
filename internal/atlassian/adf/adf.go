// Package adf renders Atlassian Document Format, the JSON rich text of Jira and Confluence, as
// Markdown for reading. It keeps the text and structure (headings, lists, tables, code, links,
// mentions) and drops presentation (colours, layouts, sizes). Macros, which a document only
// references, render as [name: parameters]. Text is not escaped: the output is for reading, not
// for converting back.
package adf

import (
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// Node is one ADF node: a block (paragraph, table, ...) or an inline (text, mention, ...).
type Node struct {
	Type    string         `json:"type"`
	Text    string         `json:"text"`
	Attrs   map[string]any `json:"attrs"`
	Marks   []Mark         `json:"marks"`
	Content []Node         `json:"content"`
}

// Mark is formatting on a text node.
type Mark struct {
	Type  string         `json:"type"`
	Attrs map[string]any `json:"attrs"`
}

// Markdown renders an ADF document given as JSON.
func Markdown(doc []byte) (string, error) {
	var n Node
	if err := json.Unmarshal(doc, &n); err != nil {
		return "", fmt.Errorf("adf: %w", err)
	}
	return Render(n), nil
}

// Render renders a document, or any node, as Markdown.
func Render(n Node) string {
	switch {
	case n.Type == "doc":
		return strings.TrimSpace(blocks(n.Content))
	case inlineTypes[n.Type]:
		return strings.TrimSpace(inline([]Node{n}))
	}
	return strings.TrimSpace(block(n))
}

// IsDoc reports whether v, a value decoded from JSON, is an ADF document.
func IsDoc(v any) bool {
	m, ok := v.(map[string]any)
	if !ok {
		return false
	}
	_, hasContent := m["content"].([]any)
	_, hasVersion := m["version"]
	return m["type"] == "doc" && hasContent && hasVersion
}

var inlineTypes = map[string]bool{
	"text": true, "hardBreak": true, "mention": true, "emoji": true, "date": true, "status": true,
	"inlineCard": true, "mediaInline": true, "placeholder": true, "inlineExtension": true,
}

// blocks renders block nodes separated by blank lines. Inline nodes found among them (malformed,
// but seen) render as a paragraph.
func blocks(nodes []Node) string { return joinBlocks(nodes, "\n\n") }

// tight renders blocks one per line: list items and table cells.
func tight(nodes []Node) string { return joinBlocks(nodes, "\n") }

func joinBlocks(nodes []Node, sep string) string {
	var parts []string
	var run []Node
	add := func(s string) {
		if s = strings.TrimRight(s, " \n"); strings.TrimSpace(s) != "" { // a trailing hard break, say
			parts = append(parts, s)
		}
	}
	flush := func() {
		if len(run) > 0 {
			add(strings.TrimSpace(inline(run)))
			run = nil
		}
	}
	for _, n := range nodes {
		if inlineTypes[n.Type] {
			run = append(run, n)
			continue
		}
		flush()
		add(block(n))
	}
	flush()
	return strings.Join(parts, sep)
}

func block(n Node) string {
	switch n.Type {
	case "paragraph", "caption":
		return inline(n.Content)
	case "heading":
		text := inline(n.Content)
		if strings.TrimSpace(text) == "" {
			return ""
		}
		return strings.Repeat("#", min(max(intAttr(n, "level", 1), 1), 6)) + " " + text
	case "bulletList":
		return list(n.Content, func(int) string { return "- " })
	case "orderedList":
		start := intAttr(n, "order", 1)
		return list(n.Content, func(i int) string { return strconv.Itoa(start+i) + ". " })
	case "taskList":
		return taskList(n)
	case "decisionList":
		var lines []string
		for _, item := range n.Content {
			lines = append(lines, hang(content(item.Content), "- Decision: "))
		}
		return strings.Join(lines, "\n")
	case "blockquote":
		return quote(blocks(n.Content))
	case "codeBlock":
		return codeBlock(n)
	case "rule":
		return "---"
	case "panel":
		return quote("[!" + alert(strAttr(n, "panelType")) + "]\n" + blocks(n.Content))
	case "expand", "nestedExpand":
		return "<details>\n<summary>" + strAttr(n, "title") + "</summary>\n\n" + blocks(n.Content) + "\n\n</details>"
	case "table":
		return table(n)
	case "media":
		return media(n)
	case "mediaSingle", "mediaGroup":
		return tight(n.Content)
	case "blockCard", "embedCard":
		return cardURL(n)
	case "extension":
		return extension(n)
	}
	// Containers whose presentation is dropped (layouts, bodied macros, synced blocks), and node
	// types newer than this renderer: keep whatever text they hold.
	if len(n.Content) > 0 {
		return blocks(n.Content)
	}
	if n.Text != "" {
		return n.Text
	}
	return strAttr(n, "text")
}

// list renders list items with their markers; continuation lines are indented under the marker.
func list(items []Node, marker func(i int) string) string {
	var lines []string
	for i, item := range items {
		lines = append(lines, hang(tight(item.Content), marker(i)))
	}
	return strings.Join(lines, "\n")
}

func taskList(n Node) string {
	var lines []string
	for _, item := range n.Content {
		switch item.Type {
		case "taskItem":
			box := "- [ ] "
			if strAttr(item, "state") == "DONE" {
				box = "- [x] "
			}
			lines = append(lines, hang(content(item.Content), box))
		case "taskList": // a nested list is the next sibling of its parent item
			lines = append(lines, indent(taskList(item), "  "))
		default:
			lines = append(lines, block(item))
		}
	}
	return strings.Join(lines, "\n")
}

// content renders nodes that may be inline (task and decision items) or blocks.
func content(nodes []Node) string {
	for _, n := range nodes {
		if !inlineTypes[n.Type] {
			return tight(nodes)
		}
	}
	return inline(nodes)
}

// hang prefixes the first line with marker and indents the rest to line up with it.
func hang(body, marker string) string {
	lines := strings.Split(body, "\n")
	pad := strings.Repeat(" ", len(marker))
	for i, l := range lines {
		switch {
		case i == 0:
			lines[i] = marker + l
		case l != "":
			lines[i] = pad + l
		}
	}
	return strings.Join(lines, "\n")
}

func indent(body, pad string) string {
	lines := strings.Split(body, "\n")
	for i, l := range lines {
		if l != "" {
			lines[i] = pad + l
		}
	}
	return strings.Join(lines, "\n")
}

func quote(body string) string {
	lines := strings.Split(body, "\n")
	for i, l := range lines {
		if l == "" {
			lines[i] = ">"
		} else {
			lines[i] = "> " + l
		}
	}
	return strings.Join(lines, "\n")
}

func codeBlock(n Node) string {
	var b strings.Builder
	for _, c := range n.Content {
		b.WriteString(c.Text)
	}
	text := strings.TrimSuffix(b.String(), "\n")
	fence := "```"
	for strings.Contains(text, fence) {
		fence += "`"
	}
	return fence + strAttr(n, "language") + "\n" + text + "\n" + fence
}

// alert maps a panel type to the GitHub alert it reads like.
func alert(panelType string) string {
	switch panelType {
	case "tip", "success":
		return "TIP"
	case "warning":
		return "WARNING"
	case "error":
		return "CAUTION"
	}
	return "NOTE"
}

// table renders a GitHub table, its first row as the header. Cells with several blocks join them
// with <br>.
func table(n Node) string {
	var rows [][]string
	width := 0
	for _, row := range n.Content {
		var cells []string
		for _, cell := range row.Content {
			text := strings.ReplaceAll(strings.TrimSpace(tight(cell.Content)), "|", `\|`)
			cells = append(cells, strings.ReplaceAll(text, "\n", "<br>"))
		}
		width = max(width, len(cells))
		rows = append(rows, cells)
	}
	if width == 0 {
		return ""
	}
	var b strings.Builder
	line := func(cells []string) {
		b.WriteString("|")
		for i := range width {
			cell := ""
			if i < len(cells) {
				cell = cells[i]
			}
			b.WriteString(" " + cell + " |")
		}
		b.WriteString("\n")
	}
	line(rows[0])
	line(slices.Repeat([]string{"---"}, width))
	for _, r := range rows[1:] {
		line(r)
	}
	return strings.TrimSuffix(b.String(), "\n")
}

func media(n Node) string {
	alt := strAttr(n, "alt")
	if alt == "" {
		alt = strAttr(n, "__fileName")
	}
	if alt == "" {
		alt = "attachment"
	}
	if url := strAttr(n, "url"); url != "" {
		return "![" + alt + "](" + url + ")"
	}
	return "![" + alt + "](media:" + strAttr(n, "id") + ")"
}

func cardURL(n Node) string {
	if url := strAttr(n, "url"); url != "" {
		return url
	}
	if data, ok := n.Attrs["data"].(map[string]any); ok {
		for _, k := range []string{"url", "name"} {
			if s, ok := data[k].(string); ok && s != "" {
				return s
			}
		}
	}
	return ""
}

// extension renders a macro reference as [key: name=value, ...], from its macro parameters.
func extension(n Node) string {
	key := strAttr(n, "extensionKey")
	if key == "" {
		key = n.Type
	}
	params, _ := n.Attrs["parameters"].(map[string]any)
	macro, _ := params["macroParams"].(map[string]any)
	names := make([]string, 0, len(macro))
	for k := range macro {
		names = append(names, k)
	}
	sort.Strings(names)
	var parts []string
	for _, k := range names {
		v := macro[k]
		if m, ok := v.(map[string]any); ok {
			v = m["value"]
		}
		switch v.(type) {
		case string, float64, bool:
			if s := oneLine(fmt.Sprint(v)); s != "" && !strings.HasPrefix(k, "_") {
				parts = append(parts, k+"="+truncate(s, 80))
			}
		}
	}
	if len(parts) == 0 {
		if t := strAttr(n, "text"); t != "" {
			return "[" + key + ": " + truncate(oneLine(t), 200) + "]"
		}
		return "[" + key + "]"
	}
	return "[" + key + ": " + truncate(strings.Join(parts, ", "), 200) + "]"
}

func inline(nodes []Node) string {
	var b strings.Builder
	for _, n := range mergeText(nodes) {
		switch n.Type {
		case "text":
			b.WriteString(marked(n.Text, n.Marks))
		case "hardBreak":
			b.WriteString("\n")
		case "mention":
			t := strAttr(n, "text")
			if t == "" {
				t = strAttr(n, "id")
			}
			if !strings.HasPrefix(t, "@") {
				t = "@" + t
			}
			b.WriteString(t)
		case "emoji":
			t := strAttr(n, "text")
			if t == "" {
				t = strAttr(n, "shortName")
			}
			b.WriteString(t)
		case "date":
			b.WriteString(date(strAttr(n, "timestamp")))
		case "status":
			b.WriteString("[" + strAttr(n, "text") + "]")
		case "inlineCard":
			b.WriteString(cardURL(n))
		case "mediaInline":
			b.WriteString(media(n))
		case "placeholder":
		case "inlineExtension":
			b.WriteString(extension(n))
		default:
			if n.Text != "" {
				b.WriteString(n.Text)
			} else {
				b.WriteString(inline(n.Content))
			}
		}
	}
	return b.String()
}

// shownMarks are the marks Markdown can show; the rest (colours, annotations, ...) are dropped.
var shownMarks = map[string]bool{"strong": true, "em": true, "code": true, "strike": true, "link": true, "subsup": true}

// markKey identifies the visible formatting of a text node.
func markKey(marks []Mark) string {
	var keys []string
	for _, m := range marks {
		if !shownMarks[m.Type] {
			continue
		}
		k := m.Type
		switch m.Type {
		case "link":
			k += ":" + fmt.Sprint(m.Attrs["href"])
		case "subsup":
			k += ":" + fmt.Sprint(m.Attrs["type"])
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return strings.Join(keys, "|")
}

// mergeText joins adjacent text nodes that look the same, so "**a** **b**" renders as "**a b**".
func mergeText(nodes []Node) []Node {
	var out []Node
	for _, n := range nodes {
		if last := len(out) - 1; n.Type == "text" && last >= 0 && out[last].Type == "text" &&
			markKey(out[last].Marks) == markKey(n.Marks) {
			out[last].Text += n.Text
			continue
		}
		out = append(out, n)
	}
	return out
}

func marked(text string, marks []Mark) string {
	key := markKey(marks)
	if key == "" || strings.TrimSpace(text) == "" {
		return text
	}
	// Markdown delimiters must hug the text, so surrounding spaces go outside them.
	rest := strings.TrimLeftFunc(text, unicode.IsSpace)
	core := strings.TrimRightFunc(rest, unicode.IsSpace)
	lead, trail := text[:len(text)-len(rest)], rest[len(core):]
	has := map[string]bool{}
	var href, subsup string
	for _, m := range marks {
		has[m.Type] = true
		switch m.Type {
		case "link":
			href, _ = m.Attrs["href"].(string)
		case "subsup":
			subsup, _ = m.Attrs["type"].(string)
		}
	}
	if has["code"] {
		core = codeSpan(core)
	}
	if has["strike"] {
		core = "~~" + core + "~~"
	}
	if has["em"] {
		core = "*" + core + "*"
	}
	if has["strong"] {
		core = "**" + core + "**"
	}
	if subsup == "sub" || subsup == "sup" {
		core = "<" + subsup + ">" + core + "</" + subsup + ">"
	}
	if href != "" {
		core = "[" + core + "](" + href + ")"
	}
	return lead + core + trail
}

func codeSpan(s string) string {
	fence := "`"
	for strings.Contains(s, fence) {
		fence += "`"
	}
	if strings.HasPrefix(s, "`") || strings.HasSuffix(s, "`") {
		return fence + " " + s + " " + fence
	}
	return fence + s + fence
}

// date renders a date node's timestamp (epoch milliseconds) as a UTC calendar date.
func date(ts string) string {
	ms, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return ts
	}
	return time.UnixMilli(ms).UTC().Format("2006-01-02")
}

func strAttr(n Node, key string) string {
	switch v := n.Attrs[key].(type) {
	case string:
		return v
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	}
	return ""
}

func intAttr(n Node, key string, fallback int) int {
	if v, ok := n.Attrs[key].(float64); ok {
		return int(v)
	}
	return fallback
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n] + "…"
}
