package markdown

import (
	"encoding/base64"
	"html"
	"regexp"
	"strings"
)

var (
	firstH1Re = regexp.MustCompile(`<h1 id="[^"]*">(.*?)</h1>`)
	sourceRe  = regexp.MustCompile(`<a id="sharefly-markdown" download="[^"]*" href="data:text/markdown;charset=utf-8;base64,([A-Za-z0-9+/=]*)"`)
)

// Source returns the original Markdown embedded in a page made by Page, so it can be downloaded again.
func Source(page []byte) ([]byte, bool) {
	m := sourceRe.FindSubmatch(page)
	if m == nil {
		return nil, false
	}
	src, err := base64.StdEncoding.DecodeString(string(m[1]))
	return src, err == nil
}

// Page renders a Markdown document as a complete, self-contained HTML page with a link to download the original
// file (embedded as a data: URL, so the page stays a single file). The title is the first level-1 heading, else
// fallback; filename names the download.
func Page(src []byte, fallback, filename string) []byte {
	body := Render(src)
	title := fallback
	if m := firstH1Re.FindStringSubmatch(body); m != nil {
		title = html.UnescapeString(tagRe.ReplaceAllString(m[1], ""))
	}
	var b strings.Builder
	b.WriteString(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>`)
	b.WriteString(html.EscapeString(title))
	b.WriteString(`</title>
<style>
:root { --bg: #ffffff; --text: #1f2328; --muted: #59636e; --line: #d1d9e0; --panel: #f6f8fa; --link: #0969da; --mark: #1a7f37; }
@media (prefers-color-scheme: dark) {
  :root { --bg: #0f1828; --text: #e4e8f0; --muted: #8693aa; --line: #24324d; --panel: #162238; --link: #f2b544; --mark: #8fe3c0; }
}
* { box-sizing: border-box; }
html { background: var(--bg); color: var(--text); -webkit-text-size-adjust: 100%; }
body { margin: 0; font: 16px/1.65 -apple-system, BlinkMacSystemFont, "Segoe UI", "Helvetica Neue", Arial, sans-serif; }
main { max-width: 780px; margin: 0 auto; padding: 48px 20px 96px; overflow-wrap: break-word; }
h1, h2, h3, h4, h5, h6 { line-height: 1.25; margin: 1.8em 0 .6em; font-weight: 650; }
h1 { font-size: 2em; margin-top: 0; padding-bottom: .3em; border-bottom: 1px solid var(--line); }
h2 { font-size: 1.45em; padding-bottom: .25em; border-bottom: 1px solid var(--line); }
h3 { font-size: 1.2em; }
h4, h5, h6 { font-size: 1em; }
h6 { color: var(--muted); }
p, ul, ol, blockquote, pre, table { margin: 0 0 1em; }
a { color: var(--link); text-underline-offset: 3px; }
a:not(:hover) { text-decoration-thickness: 1px; }
hr { border: 0; border-top: 1px solid var(--line); margin: 2em 0; }
img { max-width: 100%; }
code { font: .875em/1.5 ui-monospace, "SF Mono", Menlo, Consolas, monospace; }
:not(pre) > code { padding: .15em .4em; border-radius: 5px; background: var(--panel); }
pre { padding: 14px 16px; border-radius: 8px; background: var(--panel); overflow-x: auto; }
pre code { font-size: 13.5px; }
blockquote { margin-left: 0; padding: 0 1em; border-left: 3px solid var(--line); color: var(--muted); }
ul, ol { padding-left: 1.6em; }
li + li { margin-top: .25em; }
li > ul, li > ol { margin: .25em 0 0; }
li.task { list-style: none; margin-left: -1.4em; }
li.task input { margin: 0 .5em 0 0; accent-color: var(--mark); vertical-align: -1px; }
table { border-collapse: collapse; display: block; max-width: 100%; overflow-x: auto; font-size: 15px; }
th, td { padding: 7px 12px; border: 1px solid var(--line); vertical-align: top; }
th { background: var(--panel); font-weight: 600; text-align: left; }
.download { display: flex; justify-content: flex-end; margin: 0 0 20px; font-size: 14px; }
.download a { color: var(--muted); text-decoration: none; border: 1px solid var(--line); border-radius: 7px; padding: 5px 11px; }
.download a:hover { color: var(--text); border-color: var(--muted); }
@media (max-width: 600px) { main { padding-top: 28px; } h1 { font-size: 1.7em; } }
@media print { .download { display: none; } }
</style>
</head>
<body>
<main>
`)
	b.WriteString(`<p class="download"><a id="sharefly-markdown" download="` + html.EscapeString(filename) +
		`" href="data:text/markdown;charset=utf-8;base64,` + base64.StdEncoding.EncodeToString(src) + `">Download ` +
		html.EscapeString(filename) + "</a></p>\n")
	b.WriteString(body)
	b.WriteString("</main>\n</body>\n</html>\n")
	return []byte(b.String())
}
