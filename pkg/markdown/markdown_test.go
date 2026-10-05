package markdown

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

func TestRenderBlocks(t *testing.T) {
	tests := []struct{ name, in, want string }{
		{"heading with id", "# Hello World", `<h1 id="hello-world">Hello World</h1>` + "\n"},
		{"heading closing hashes", "## Install ##", `<h2 id="install">Install</h2>` + "\n"},
		{"duplicate heading ids", "## A\n## A", `<h2 id="a">A</h2>` + "\n" + `<h2 id="a-1">A</h2>` + "\n"},
		{"paragraph joins lines", "one\ntwo", "<p>one\ntwo</p>\n"},
		{"hard break", "one  \ntwo", "<p>one<br>\ntwo</p>\n"},
		{"rule", "---", "<hr>\n"},
		{"fenced code keeps text", "```go\nif a < b && c {\n```", `<pre><code class="language-go">if a &lt; b &amp;&amp; c {</code></pre>` + "\n"},
		{"unclosed fence runs to end", "~~~\nx", "<pre><code>x</code></pre>\n"},
		{"blockquote", "> quoted\n> more", "<blockquote>\n<p>quoted\nmore</p>\n</blockquote>\n"},
		{"tight list", "- a\n- b", "<ul>\n<li>a\n</li>\n<li>b\n</li>\n</ul>\n"},
		{"loose list", "- a\n\n- b", "<ul>\n<li><p>a</p>\n</li>\n<li><p>b</p>\n</li>\n</ul>\n"},
		{"ordered start", "3. c\n4. d", "<ol start=\"3\">\n<li>c\n</li>\n<li>d\n</li>\n</ol>\n"},
		{"nested list", "- a\n  - b\n- c", "<ul>\n<li>a\n<ul>\n<li>b\n</li>\n</ul>\n</li>\n<li>c\n</li>\n</ul>\n"},
		{"task list", "- [x] done\n- [ ] todo", "<ul>\n<li class=\"task\"><input type=\"checkbox\" disabled checked> done\n</li>\n<li class=\"task\"><input type=\"checkbox\" disabled> todo\n</li>\n</ul>\n"},
		{"loose task list keeps box with text", "- [x] a\n\n- [ ] b", "<ul>\n<li class=\"task\"><p><input type=\"checkbox\" disabled checked> a</p>\n</li>\n<li class=\"task\"><p><input type=\"checkbox\" disabled> b</p>\n</li>\n</ul>\n"},
		{"code block in list item", "1. run:\n   ```\n   make\n   ```\n2. done", "<ol>\n<li>run:\n<pre><code>make</code></pre>\n</li>\n<li>done\n</li>\n</ol>\n"},
		{"table with alignment and code pipe", "| a | b |\n|:--|--:|\n| `x|y` | 2 |", "<table>\n<thead><tr><th style=\"text-align:left\">a</th><th style=\"text-align:right\">b</th></tr></thead>\n<tbody>\n<tr><td style=\"text-align:left\"><code>x|y</code></td><td style=\"text-align:right\">2</td></tr>\n</tbody>\n</table>\n"},
		{"paragraph then list", "text\n- item", "<p>text</p>\n<ul>\n<li>item\n</li>\n</ul>\n"},
		{"number in text is not a list", "the year\n2024. was good", "<p>the year\n2024. was good</p>\n"},
	}
	for _, tt := range tests {
		if got := Render([]byte(tt.in)); got != tt.want {
			t.Errorf("%s:\n got %q\nwant %q", tt.name, got, tt.want)
		}
	}
}

func TestRenderInline(t *testing.T) {
	tests := []struct{ in, want string }{
		{"*em* **strong** ***both*** ~~gone~~", "<em>em</em> <strong>strong</strong> <em><strong>both</strong></em> <del>gone</del>"},
		{"**bold *nested* bold**", "<strong>bold <em>nested</em> bold</strong>"},
		{"snake_case_name and 2 * 3 * 4", "snake_case_name and 2 * 3 * 4"},
		{"`a < b` and ``x ` y``", "<code>a &lt; b</code> and <code>x ` y</code>"},
		{`\*not em\*`, "*not em*"},
		{"[docs](https://x.dev/a_(b) \"T\")", `<a href="https://x.dev/a_(b)" title="T">docs</a>`},
		{"[**bold** link](#install)", `<a href="#install"><strong>bold</strong> link</a>`},
		{"![logo](img/a.png)", `<img src="img/a.png" alt="logo">`},
		{"see https://example.com/x.", `see <a href="https://example.com/x">https://example.com/x</a>.`},
		{"<https://example.com>", `<a href="https://example.com">https://example.com</a>`},
		{"a <id> path & \"q\"", "a &lt;id&gt; path &amp; &#34;q&#34;"},
	}
	for _, tt := range tests {
		got := strings.TrimSuffix(strings.TrimPrefix(Render([]byte(tt.in)), "<p>"), "</p>\n")
		if got != tt.want {
			t.Errorf("%q:\n got %q\nwant %q", tt.in, got, tt.want)
		}
	}
}

// A shared Markdown file is published as a web page on the user's domain: nothing in it may become script,
// an event handler or a javascript: link, whatever the author (or a pasted snippet) wrote.
func TestRenderNeverEmitsActiveContent(t *testing.T) {
	for _, in := range []string{
		"<script>alert(1)</script>",
		"<img src=x onerror=alert(1)>",
		"[click](javascript:alert(1))",
		"[click](JaVaScRiPt:alert(1))",
		"[click](java script:alert(1))",
		"![x](javascript:alert(1))",
		"[x](data:text/html,<script>alert(1)</script>)",
		"[x](https://ok.dev \"a\" onmouseover=\"alert(1))",
		"<javascript:alert(1)>",
		"```\n</code></pre><script>alert(1)</script>\n```",
		"| <b onclick=x> | b |\n|---|---|\n| c | d |",
		"# <svg onload=alert(1)>",
		"- [x] <iframe src=x>",
	} {
		out := Render([]byte(in))
		for _, tag := range tagRe.FindAllString(out, -1) {
			name := strings.ToLower(strings.FieldsFunc(tag, func(r rune) bool { return r == '<' || r == '>' || r == '/' || r == ' ' })[0])
			if !allowedTags[name] || eventAttrRe.MatchString(tag) || badURLRe.MatchString(tag) {
				t.Errorf("input %q produced active markup %q:\n%s", in, tag, out)
			}
		}
	}
}

var (
	allowedTags = map[string]bool{"h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true, "p": true, "br": true,
		"hr": true, "pre": true, "code": true, "blockquote": true, "ul": true, "ol": true, "li": true, "input": true, "table": true,
		"thead": true, "tbody": true, "tr": true, "th": true, "td": true, "a": true, "img": true, "em": true, "strong": true, "del": true}
	eventAttrRe = regexp.MustCompile(`(?i)\son[a-z]+\s*=`)
	badURLRe    = regexp.MustCompile(`(?i)(href|src)="\s*(javascript|data|vbscript):`)
)

// The renderer must handle the project's own documents, which are what people will share.
func TestRenderProjectDocs(t *testing.T) {
	src, err := os.ReadFile("../../README.md")
	if err != nil {
		t.Skip(err)
	}
	out := Render(src)
	for _, want := range []string{`<h1 id="sharefly">sharefly</h1>`, `<h2 id="install">Install</h2>`, `<a href="#install">Install</a>`, "<table>", "<pre><code>", "<ol>"} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered README misses %q", want)
		}
	}
}

// "Download notes.md" must give back exactly the file that was shared, and the file name can't break out of
// the download attribute.
func TestPageEmbedsOriginalForDownload(t *testing.T) {
	src := []byte("# Notes\n\nnon-ASCII: café ✓\n<script>x</script>\n")
	page := Page(src, "notes", `my "notes".md`)
	got, ok := Source(page)
	if !ok || string(got) != string(src) {
		t.Fatalf("Source = %q, %v; want the original bytes", got, ok)
	}
	if !strings.Contains(string(page), `download="my &#34;notes&#34;.md"`) || !strings.Contains(string(page), "Download my &#34;notes&#34;.md</a>") {
		t.Errorf("file name not escaped in the download link:\n%s", page)
	}
	if _, ok := Source([]byte("<html>plain page</html>")); ok {
		t.Error("a page without embedded Markdown must report none")
	}
}
