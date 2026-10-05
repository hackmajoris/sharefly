package markdown

import (
	"html"
	"strings"
)

// inline renders inline Markdown; every byte of s ends up escaped or inside markup this package writes.
func (r *renderer) inline(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		c := s[i]
		switch c {
		case '\\':
			if i+1 < len(s) && strings.IndexByte("!\"#$%&'()*+,-./:;<=>?@[\\]^_`{|}~", s[i+1]) >= 0 {
				b.WriteString(html.EscapeString(s[i+1 : i+2]))
				i += 2
				continue
			}
			b.WriteByte('\\')
			i++
		case '`':
			n := run(s, i, '`')
			if end := findRun(s, i+n, '`', n); end >= 0 {
				code := strings.ReplaceAll(s[i+n:end], "\n", " ")
				if len(code) > 2 && code[0] == ' ' && code[len(code)-1] == ' ' && strings.TrimSpace(code) != "" {
					code = code[1 : len(code)-1]
				}
				b.WriteString("<code>" + html.EscapeString(code) + "</code>")
				i = end + n
				continue
			}
			b.WriteString(s[i : i+n])
			i += n
		case '!':
			if i+1 < len(s) && s[i+1] == '[' {
				if text, dest, title, end, ok := parseLink(s, i+1); ok {
					if u, safe := safeURL(dest); safe {
						b.WriteString(`<img src="` + html.EscapeString(u) + `" alt="` + html.EscapeString(text) + `"` + titleAttr(title) + `>`)
					} else {
						b.WriteString(html.EscapeString(text))
					}
					i = end
					continue
				}
			}
			b.WriteByte('!')
			i++
		case '[':
			if text, dest, title, end, ok := parseLink(s, i); ok {
				if u, safe := safeURL(dest); safe {
					b.WriteString(`<a href="` + html.EscapeString(u) + `"` + titleAttr(title) + `>` + r.inline(text) + `</a>`)
				} else {
					b.WriteString(r.inline(text))
				}
				i = end
				continue
			}
			b.WriteByte('[')
			i++
		case '<':
			if end := strings.IndexByte(s[i:], '>'); end > 1 {
				u := s[i+1 : i+end]
				if !strings.ContainsAny(u, " \n<") && hasLinkScheme(u) {
					b.WriteString(`<a href="` + html.EscapeString(u) + `">` + html.EscapeString(u) + `</a>`)
					i += end + 1
					continue
				}
			}
			b.WriteString("&lt;")
			i++
		case '*', '_', '~':
			if out, end, ok := r.emphasis(s, i); ok {
				b.WriteString(out)
				i = end
				continue
			}
			n := run(s, i, c)
			b.WriteString(s[i : i+n])
			i += n
		default:
			if c == 'h' && (i == 0 || !isWordByte(s[i-1])) && (strings.HasPrefix(s[i:], "https://") || strings.HasPrefix(s[i:], "http://")) {
				u := bareURL(s[i:])
				b.WriteString(`<a href="` + html.EscapeString(u) + `">` + html.EscapeString(u) + `</a>`)
				i += len(u)
				continue
			}
			switch c {
			case '&':
				b.WriteString("&amp;")
			case '>':
				b.WriteString("&gt;")
			case '"':
				b.WriteString("&#34;")
			case '\'':
				b.WriteString("&#39;")
			default:
				b.WriteByte(c)
			}
			i++
		}
	}
	return b.String()
}

// emphasis renders *em*, **strong**, ***both***, the same with _ (not inside words), and ~~strikethrough~~.
func (r *renderer) emphasis(s string, i int) (string, int, bool) {
	c := s[i]
	n := run(s, i, c)
	if n > 3 || c == '~' && n != 2 || i+n >= len(s) || isSpace(s[i+n]) || c == '_' && i > 0 && isWordByte(s[i-1]) {
		return "", 0, false
	}
	for k := i + n; k < len(s); {
		switch {
		case s[k] == '\\':
			k += 2
		case s[k] == '`':
			m := run(s, k, '`')
			if end := findRun(s, k+m, '`', m); end >= 0 {
				k = end + m
			} else {
				k += m
			}
		case s[k] == c:
			m := run(s, k, c)
			if m == n && !isSpace(s[k-1]) && (c != '_' || k+m >= len(s) || !isWordByte(s[k+m])) {
				inner := r.inline(s[i+n : k])
				switch {
				case c == '~':
					return "<del>" + inner + "</del>", k + m, true
				case n == 1:
					return "<em>" + inner + "</em>", k + m, true
				case n == 2:
					return "<strong>" + inner + "</strong>", k + m, true
				default:
					return "<em><strong>" + inner + "</strong></em>", k + m, true
				}
			}
			k += m
		default:
			k++
		}
	}
	return "", 0, false
}

// parseLink parses [text](dest "title") starting at s[i] == '['; end is the index after the closing ')'.
func parseLink(s string, i int) (text, dest, title string, end int, ok bool) {
	depth := 0
	j := i
	for ; j < len(s); j++ {
		switch s[j] {
		case '\\':
			j++
			continue
		case '`':
			m := run(s, j, '`')
			if e := findRun(s, j+m, '`', m); e >= 0 {
				j = e + m - 1
			}
			continue
		case '[':
			depth++
		case ']':
			depth--
		}
		if depth == 0 {
			break
		}
	}
	if j >= len(s)-1 || s[j+1] != '(' {
		return "", "", "", 0, false
	}
	text = s[i+1 : j]
	k := j + 2
	for k < len(s) && s[k] == ' ' {
		k++
	}
	if k < len(s) && s[k] == '<' {
		e := strings.IndexByte(s[k:], '>')
		if e < 0 {
			return "", "", "", 0, false
		}
		dest, k = s[k+1:k+e], k+e+1
	} else {
		start, parens := k, 0
		for ; k < len(s) && s[k] != ' ' && s[k] != '\n'; k++ {
			if s[k] == '(' {
				parens++
			} else if s[k] == ')' {
				if parens == 0 {
					break
				}
				parens--
			}
		}
		dest = s[start:k]
	}
	for k < len(s) && (s[k] == ' ' || s[k] == '\n') {
		k++
	}
	if k < len(s) && (s[k] == '"' || s[k] == '\'') {
		q := s[k]
		e := strings.IndexByte(s[k+1:], q)
		if e < 0 {
			return "", "", "", 0, false
		}
		title, k = s[k+1:k+1+e], k+e+2
		for k < len(s) && s[k] == ' ' {
			k++
		}
	}
	if k >= len(s) || s[k] != ')' {
		return "", "", "", 0, false
	}
	return text, dest, title, k + 1, true
}

// safeURL allows http, https, mailto and scheme-less (relative) URLs; anything else, like javascript:, is dropped.
func safeURL(u string) (string, bool) {
	u = strings.TrimSpace(u)
	if k := strings.IndexAny(u, ":/?#"); k >= 0 && u[k] == ':' {
		return u, hasLinkScheme(u)
	}
	return u, true
}

func hasLinkScheme(u string) bool {
	l := strings.ToLower(u)
	return strings.HasPrefix(l, "http://") || strings.HasPrefix(l, "https://") || strings.HasPrefix(l, "mailto:")
}

func titleAttr(t string) string {
	if t == "" {
		return ""
	}
	return ` title="` + html.EscapeString(t) + `"`
}

// bareURL takes a URL up to whitespace or '<', without trailing punctuation or an unmatched ')'.
func bareURL(s string) string {
	end := strings.IndexAny(s, " \n<")
	if end < 0 {
		end = len(s)
	}
	u := s[:end]
	for len(u) > 0 {
		last := u[len(u)-1]
		if strings.IndexByte(".,:;!?'\"*_~", last) >= 0 || last == ')' && strings.Count(u, "(") < strings.Count(u, ")") {
			u = u[:len(u)-1]
			continue
		}
		break
	}
	return u
}

func run(s string, i int, c byte) int {
	n := 0
	for i+n < len(s) && s[i+n] == c {
		n++
	}
	return n
}

// findRun returns the index of the next run of exactly n bytes c at or after i, or -1.
func findRun(s string, i int, c byte, n int) int {
	for i < len(s) {
		if s[i] == c {
			m := run(s, i, c)
			if m == n {
				return i
			}
			i += m
			continue
		}
		i++
	}
	return -1
}

func isSpace(c byte) bool { return c == ' ' || c == '\n' }

func isWordByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c >= 0x80
}
