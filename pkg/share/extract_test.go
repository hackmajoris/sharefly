package share

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type entry struct {
	hdr  tar.Header
	body string
}

func file(name, body string) entry {
	return entry{tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(body))}, body}
}

func makeTarGz(t *testing.T, entries ...entry) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		if err := tw.WriteHeader(&e.hdr); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(e.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return &buf
}

// Upload can never write outside its share dir: every rejected archive must leave the parent untouched.
func TestExtractRejectsEscapes(t *testing.T) {
	cases := map[string]func(parent string) []entry{
		"dotdot": func(string) []entry { return []entry{file("../x", "evil")} },
		"absolute": func(parent string) []entry {
			return []entry{file(filepath.Join(parent, "abs"), "evil")}
		},
		"symlink": func(parent string) []entry {
			return []entry{
				{hdr: tar.Header{Name: "link", Typeflag: tar.TypeSymlink, Linkname: parent, Mode: 0o777}},
				file("link/evil", "evil"),
			}
		},
		"hardlink": func(string) []entry {
			return []entry{{hdr: tar.Header{Name: "hl", Typeflag: tar.TypeLink, Linkname: "../outside", Mode: 0o644}}}
		},
	}
	for name, build := range cases {
		t.Run(name, func(t *testing.T) {
			parent := t.TempDir()
			dst := filepath.Join(parent, "dst")
			_, err := Extract(makeTarGz(t, build(parent)...), dst, 1<<20)
			if err == nil {
				t.Fatal("expected error")
			}
			des, err := os.ReadDir(parent)
			if err != nil {
				t.Fatal(err)
			}
			if len(des) != 1 || des[0].Name() != "dst" {
				t.Fatalf("parent contains %v, want only dst", des)
			}
			inner, err := os.ReadDir(dst)
			if err != nil {
				t.Fatal(err)
			}
			for _, d := range inner {
				if d.Type()&os.ModeSymlink != 0 {
					t.Fatalf("symlink %q created in dst", d.Name())
				}
			}
		})
	}
}

// Uncompressed size is capped so a small gzip bomb cannot fill the mini's disk.
func TestExtractTooLarge(t *testing.T) {
	big := strings.Repeat("a", 2000)
	_, err := Extract(makeTarGz(t, file("a.txt", big[:600]), file("b.txt", big[:600])), t.TempDir(), 1000)
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("err = %v, want ErrTooLarge", err)
	}
}

// Many empty entries fit under the byte cap, so the entry count is capped too (inode exhaustion).
func TestExtractTooManyEntries(t *testing.T) {
	entries := make([]entry, maxEntries+1)
	for i := range entries {
		entries[i] = file(fmt.Sprintf("f%d", i), "")
	}
	_, err := Extract(makeTarGz(t, entries...), t.TempDir(), 1<<20)
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("err = %v, want ErrTooLarge", err)
	}
}

func TestExtractAtCapSucceeds(t *testing.T) {
	size, err := Extract(makeTarGz(t, file("a.txt", strings.Repeat("a", 1000))), t.TempDir(), 1000)
	if err != nil || size != 1000 {
		t.Fatalf("size=%d err=%v, want 1000 nil", size, err)
	}
}

func TestExtractNestedFolder(t *testing.T) {
	dst := filepath.Join(t.TempDir(), "dst")
	size, err := Extract(makeTarGz(t,
		entry{hdr: tar.Header{Name: "css/", Typeflag: tar.TypeDir, Mode: 0o755}},
		file("index.html", "<h1>hi</h1>"),
		file("css/site.css", "body{}"),
		file("img/deep/a.txt", "abc"),
	), dst, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if want := int64(len("<h1>hi</h1>") + len("body{}") + len("abc")); size != want {
		t.Fatalf("size = %d, want %d", size, want)
	}
	for path, want := range map[string]string{
		"index.html":     "<h1>hi</h1>",
		"css/site.css":   "body{}",
		"img/deep/a.txt": "abc",
	} {
		got, err := os.ReadFile(filepath.Join(dst, path))
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != want {
			t.Fatalf("%s = %q, want %q", path, got, want)
		}
	}
}

func TestExtractNotGzip(t *testing.T) {
	if _, err := Extract(strings.NewReader("plain"), t.TempDir(), 1<<20); err == nil {
		t.Fatal("expected error")
	}
}

func writeTree(t *testing.T, files ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, f := range files {
		p := filepath.Join(dir, f)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestDetectEntry(t *testing.T) {
	cases := []struct {
		name    string
		files   []string
		want    string
		wantErr bool
	}{
		{"index at root", []string{"index.html", "a.css"}, "", false},
		{"single file", []string{"report.html"}, "report.html", false},
		{"single nested file", []string{"sub/report.html"}, "sub/report.html", false},
		{"index.html is a directory", []string{"index.html/a.txt", "b.txt"}, "", true},
		{"multiple files without index", []string{"a.html", "b.html"}, "", true},
		{"empty", nil, "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := DetectEntry(writeTree(t, c.files...))
			if (err != nil) != c.wantErr || got != c.want {
				t.Fatalf("got %q err=%v, want %q wantErr=%v", got, err, c.want, c.wantErr)
			}
		})
	}
}
