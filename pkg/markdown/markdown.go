// Package markdown renders the common subset of GitHub-flavoured Markdown to HTML: ATX headings, paragraphs,
// emphasis, strikethrough, code spans and fenced code, links, images, autolinks, block quotes, nested and task
// lists, tables and rules. Everything else shows as text. Raw HTML is always escaped and link targets are limited
// to http, https, mailto and relative URLs, so a rendered document can't run script.
package markdown

import (
	"fmt"
	"html"
	"regexp"
	"strconv"
	"strings"
)

// Render converts a Markdown document to an HTML fragment.
func Render(src []byte) string {
	text := strings.ReplaceAll(strings.ReplaceAll(string(src), "\r\n", "\n"), "\t", "    ")
	r := &renderer{ids: map[string]int{}}
	r.blocks(strings.Split(text, "\n"), false)
	return r.b.String()
}

type renderer struct {
	b   strings.Builder
	ids map[string]int
}

var (
	headingRe  = regexp.MustCompile(`^ {0,3}(#{1,6})(?:[ ]+(.*?))?(?:[ ]+#+)?[ ]*$`)
	ruleRe     = regexp.MustCompile(`^ {0,3}(?:(?:-[ ]*){3,}|(?:\*[ ]*){3,}|(?:_[ ]*){3,})$`)
	fenceRe    = regexp.MustCompile("^( {0,3})(`{3,}|~{3,})[ ]*([^`]*)$")
	listRe     = regexp.MustCompile(`^( *)([-*+]|\d{1,9}[.)])( +|$)`)
	tableSepRe = regexp.MustCompile(`^ *\|? *:?-+:? *(\| *:?-+:? *)*\|? *$`)
	quoteRe    = regexp.MustCompile(`^ {0,3}> ?`)
	taskRe     = regexp.MustCompile(`^\[([ xX])\] `)
	tagRe      = regexp.MustCompile(`<[^>]*>`)
)

func indent(line string) int { return len(line) - len(strings.TrimLeft(line, " ")) }

func blank(line string) bool { return strings.TrimSpace(line) == "" }

func isTableStart(lines []string, i int) bool {
	return i+1 < len(lines) && strings.Contains(lines[i], "|") && tableSepRe.MatchString(lines[i+1]) &&
		len(splitRow(lines[i])) == len(splitRow(lines[i+1]))
}

// startsBlock reports whether line begins a block that ends a paragraph.
func startsBlock(lines []string, i int) bool {
	line := lines[i]
	if headingRe.MatchString(line) || ruleRe.MatchString(line) || fenceRe.MatchString(line) || quoteRe.MatchString(line) || isTableStart(lines, i) {
		return true
	}
	if m := listRe.FindStringSubmatch(line); m != nil && m[3] != "" && indent(line) < 4 {
		return !isOrdered(m[2]) || strings.TrimRight(m[2], ".)") == "1"
	}
	return false
}

func isOrdered(marker string) bool { return marker[0] >= '0' && marker[0] <= '9' }

func (r *renderer) blocks(lines []string, tight bool) {
	for i := 0; i < len(lines); {
		line := lines[i]
		switch {
		case blank(line):
			i++
		case fenceRe.MatchString(line):
			i = r.fence(lines, i)
		case headingRe.MatchString(line):
			m := headingRe.FindStringSubmatch(line)
			level := len(m[1])
			text := r.inline(m[2])
			fmt.Fprintf(&r.b, "<h%d id=\"%s\">%s</h%d>\n", level, r.slug(text), text, level)
			i++
		case ruleRe.MatchString(line):
			r.b.WriteString("<hr>\n")
			i++
		case quoteRe.MatchString(line):
			var inner []string
			for ; i < len(lines) && quoteRe.MatchString(lines[i]); i++ {
				inner = append(inner, quoteRe.ReplaceAllString(lines[i], ""))
			}
			r.b.WriteString("<blockquote>\n")
			r.blocks(inner, false)
			r.b.WriteString("</blockquote>\n")
		case isTableStart(lines, i):
			i = r.table(lines, i)
		case listRe.MatchString(line):
			i = r.list(lines, i)
		default:
			var para []string
			for ; i < len(lines) && !blank(lines[i]) && (len(para) == 0 || !startsBlock(lines, i)); i++ {
				para = append(para, lines[i])
			}
			r.paragraph(para, tight)
		}
	}
}

func (r *renderer) paragraph(lines []string, tight bool) {
	var sb strings.Builder
	for k, l := range lines {
		l = strings.TrimLeft(l, " ")
		if k < len(lines)-1 {
			if strings.HasSuffix(l, "  ") || strings.HasSuffix(l, "\\") {
				l = strings.TrimRight(strings.TrimSuffix(l, "\\"), " ") + "\x01"
			}
			l += "\n"
		} else {
			l = strings.TrimRight(l, " ")
		}
		sb.WriteString(l)
	}
	text := strings.ReplaceAll(r.inline(sb.String()), "\x01", "<br>")
	if tight {
		r.b.WriteString(text + "\n")
		return
	}
	r.b.WriteString("<p>" + text + "</p>\n")
}

func (r *renderer) fence(lines []string, i int) int {
	m := fenceRe.FindStringSubmatch(lines[i])
	pad, marker := len(m[1]), m[2]
	lang := strings.Fields(m[3])
	var code []string
	j := i + 1
	for ; j < len(lines); j++ {
		t := strings.TrimSpace(lines[j])
		if indent(lines[j]) < 4 && strings.HasPrefix(t, marker[:3]) && strings.Trim(t, marker[:1]) == "" && len(t) >= len(marker) {
			j++
			break
		}
		l := lines[j]
		l = l[min(pad, indent(l)):]
		code = append(code, l)
	}
	class := ""
	if len(lang) > 0 {
		class = ` class="language-` + html.EscapeString(lang[0]) + `"`
	}
	fmt.Fprintf(&r.b, "<pre><code%s>%s</code></pre>\n", class, html.EscapeString(strings.Join(code, "\n")))
	return j
}

func splitRow(line string) []string {
	line = strings.TrimSpace(line)
	line = strings.TrimPrefix(line, "|")
	if strings.HasSuffix(line, "|") && !strings.HasSuffix(line, "\\|") {
		line = strings.TrimSuffix(line, "|")
	}
	var cells []string
	var cur strings.Builder
	inCode := false
	for k := 0; k < len(line); k++ {
		c := line[k]
		switch {
		case c == '\\' && k+1 < len(line) && line[k+1] == '|':
			cur.WriteByte('|')
			k++
		case c == '`':
			inCode = !inCode
			cur.WriteByte(c)
		case c == '|' && !inCode:
			cells = append(cells, strings.TrimSpace(cur.String()))
			cur.Reset()
		default:
			cur.WriteByte(c)
		}
	}
	return append(cells, strings.TrimSpace(cur.String()))
}

func (r *renderer) table(lines []string, i int) int {
	head := splitRow(lines[i])
	var aligns []string
	for _, c := range splitRow(lines[i+1]) {
		switch {
		case strings.HasPrefix(c, ":") && strings.HasSuffix(c, ":"):
			aligns = append(aligns, ` style="text-align:center"`)
		case strings.HasSuffix(c, ":"):
			aligns = append(aligns, ` style="text-align:right"`)
		case strings.HasPrefix(c, ":"):
			aligns = append(aligns, ` style="text-align:left"`)
		default:
			aligns = append(aligns, "")
		}
	}
	r.b.WriteString("<table>\n<thead><tr>")
	for k, c := range head {
		fmt.Fprintf(&r.b, "<th%s>%s</th>", aligns[k], r.inline(c))
	}
	r.b.WriteString("</tr></thead>\n<tbody>\n")
	j := i + 2
	for ; j < len(lines) && !blank(lines[j]) && strings.Contains(lines[j], "|") && !startsBlockOtherThanTable(lines, j); j++ {
		cells := splitRow(lines[j])
		r.b.WriteString("<tr>")
		for k := range head {
			c := ""
			if k < len(cells) {
				c = cells[k]
			}
			fmt.Fprintf(&r.b, "<td%s>%s</td>", aligns[k], r.inline(c))
		}
		r.b.WriteString("</tr>\n")
	}
	r.b.WriteString("</tbody>\n</table>\n")
	return j
}

func startsBlockOtherThanTable(lines []string, i int) bool {
	l := lines[i]
	return headingRe.MatchString(l) || fenceRe.MatchString(l) || quoteRe.MatchString(l)
}

type listItem struct {
	lines      []string
	blankAfter bool // a blank line separates this item from the next
}

func (r *renderer) list(lines []string, i int) int {
	first := listRe.FindStringSubmatch(lines[i])
	ordered := isOrdered(first[2])
	delim := first[2][len(first[2])-1]
	sibling := func(l string) bool {
		m := listRe.FindStringSubmatch(l)
		return m != nil && isOrdered(m[2]) == ordered && m[2][len(m[2])-1] == delim && !ruleRe.MatchString(l)
	}
	var items []listItem
	j := i
	for j < len(lines) && sibling(lines[j]) {
		m := listRe.FindStringSubmatch(lines[j])
		content := len(m[0])
		if m[3] == "" || len(m[3]) > 4 {
			content = len(m[1]) + len(m[2]) + 1
		}
		item := listItem{lines: []string{strings.TrimLeft(lines[j][min(content, len(lines[j])):], " ")}}
		j++
	collect:
		for j < len(lines) {
			l := lines[j]
			switch {
			case blank(l):
				k := j
				for k < len(lines) && blank(lines[k]) {
					k++
				}
				if k == len(lines) || indent(lines[k]) < content {
					break collect
				}
				for ; j < k; j++ {
					item.lines = append(item.lines, "")
				}
			case indent(l) >= content:
				item.lines = append(item.lines, l[content:])
				j++
			case listRe.MatchString(l) || startsBlock(lines, j):
				break collect
			default: // lazy continuation of the item's paragraph
				item.lines = append(item.lines, strings.TrimLeft(l, " "))
				j++
			}
		}
		k := j
		for k < len(lines) && blank(lines[k]) {
			k++
		}
		if k > j {
			if k == len(lines) || !sibling(lines[k]) {
				items = append(items, item)
				break
			}
			item.blankAfter = true
		}
		items = append(items, item)
		j = k
	}
	loose := false
	for k, it := range items {
		if it.blankAfter && k < len(items)-1 || hasInnerBlank(it.lines) {
			loose = true
		}
	}
	tag, start := "ul", ""
	if ordered {
		tag = "ol"
		if n, err := strconv.Atoi(strings.TrimRight(first[2], ".)")); err == nil && n != 1 {
			start = fmt.Sprintf(` start="%d"`, n)
		}
	}
	fmt.Fprintf(&r.b, "<%s%s>\n", tag, start)
	for _, it := range items {
		body := it.lines
		box := ""
		if m := taskRe.FindStringSubmatch(body[0]); m != nil {
			box = `<input type="checkbox" disabled> `
			if m[1] != " " {
				box = `<input type="checkbox" disabled checked> `
			}
			body = append([]string{body[0][len(m[0]):]}, body[1:]...)
		}
		item := &renderer{ids: r.ids}
		item.blocks(body, !loose)
		out := item.b.String()
		if box == "" {
			r.b.WriteString("<li>" + out + "</li>\n")
			continue
		}
		// inside the item's first paragraph, as GitHub does, so a loose item doesn't put its text on a new line
		if rest, ok := strings.CutPrefix(out, "<p>"); ok {
			out = "<p>" + box + rest
		} else {
			out = box + out
		}
		r.b.WriteString(`<li class="task">` + out + "</li>\n")
	}
	fmt.Fprintf(&r.b, "</%s>\n", tag)
	return j
}

func hasInnerBlank(lines []string) bool {
	for k := 1; k < len(lines)-1; k++ {
		if blank(lines[k]) {
			return true
		}
	}
	return false
}

// slug makes a GitHub-style heading id from its rendered text, unique within the document.
func (r *renderer) slug(rendered string) string {
	text := html.UnescapeString(tagRe.ReplaceAllString(rendered, ""))
	var sb strings.Builder
	for _, c := range strings.ToLower(text) {
		switch {
		case c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c > 127:
			sb.WriteRune(c)
		case c == ' ':
			sb.WriteByte('-')
		}
	}
	id := sb.String()
	if n := r.ids[id]; n > 0 {
		r.ids[id] = n + 1
		id = fmt.Sprintf("%s-%d", id, n)
	} else {
		r.ids[id] = 1
	}
	return html.EscapeString(id)
}
