package client

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/hackmajoris/go-share/pkg/share"
)

func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for name, body := range files {
		p := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func entries(t *testing.T, r io.Reader) map[string]string {
	t.Helper()
	gz, err := gzip.NewReader(r)
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	got := map[string]string{}
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return got
		}
		if err != nil {
			t.Fatal(err)
		}
		if hdr.Typeflag != tar.TypeReg {
			t.Fatalf("entry %q has type %c, server only accepts regular files", hdr.Name, hdr.Typeflag)
		}
		b, err := io.ReadAll(tr)
		if err != nil {
			t.Fatal(err)
		}
		got[hdr.Name] = string(b)
	}
}

func TestArchiveSingleFile(t *testing.T) {
	root := writeTree(t, map[string]string{"report.html": "<h1>r</h1>"})
	var buf bytes.Buffer
	if err := Archive(filepath.Join(root, "report.html"), &buf); err != nil {
		t.Fatal(err)
	}
	got := entries(t, &buf)
	if len(got) != 1 || got["report.html"] != "<h1>r</h1>" {
		t.Fatalf("entries = %v, want only report.html at archive root", got)
	}
}

func TestArchiveFolderExcludesRepoAndJunk(t *testing.T) {
	root := writeTree(t, map[string]string{
		"index.html":        "i",
		"css/site.css":      "c",
		".DS_Store":         "junk",
		"img/.DS_Store":     "junk",
		".git/HEAD":         "ref: refs/heads/main",
		".git/objects/ab/x": "secret history",
	})
	if err := os.Symlink("/etc/passwd", filepath.Join(root, "leak")); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := Archive(root, &buf); err != nil {
		t.Fatal(err)
	}
	got := entries(t, &buf)
	names := make([]string, 0, len(got))
	for n := range got {
		names = append(names, n)
	}
	slices.Sort(names)
	if want := []string{"css/site.css", "index.html"}; !slices.Equal(names, want) {
		t.Fatalf("entries = %v, want %v (.git, .DS_Store and symlinks must never be published)", names, want)
	}
}

func TestArchiveFolderWithoutIndexWritesNothing(t *testing.T) {
	root := writeTree(t, map[string]string{"a.html": "a", "b.html": "b"})
	var buf bytes.Buffer
	if err := Archive(root, &buf); err == nil {
		t.Fatal("want error for folder without index.html")
	}
	if buf.Len() != 0 {
		t.Fatalf("wrote %d bytes; must fail before any upload starts", buf.Len())
	}
}

func TestArchiveMissingPath(t *testing.T) {
	var buf bytes.Buffer
	if err := Archive(filepath.Join(t.TempDir(), "nope"), &buf); err == nil {
		t.Fatal("want error for nonexistent path")
	}
	if buf.Len() != 0 {
		t.Fatalf("wrote %d bytes for missing path", buf.Len())
	}
}

// `serve <symlink-to-folder>` must upload the folder's contents, not an empty archive.
func TestArchiveSymlinkedFolder(t *testing.T) {
	root := writeTree(t, map[string]string{"index.html": "i", "css/a.css": "c"})
	link := filepath.Join(t.TempDir(), "site")
	if err := os.Symlink(root, link); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := Archive(link, &buf); err != nil {
		t.Fatal(err)
	}
	if got := entries(t, &buf); len(got) != 2 || got["index.html"] != "i" || got["css/a.css"] != "c" {
		t.Fatalf("entries = %v, want index.html and css/a.css", got)
	}
}

// A symlinked index.html would be skipped by the walk, so the server would get a folder without
// one; reject it up front instead.
func TestArchiveRejectsSymlinkedIndex(t *testing.T) {
	root := writeTree(t, map[string]string{"real.html": "r"})
	if err := os.Symlink(filepath.Join(root, "real.html"), filepath.Join(root, "index.html")); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := Archive(root, &buf); err == nil {
		t.Fatal("want error for symlinked index.html")
	}
	if buf.Len() != 0 {
		t.Fatalf("wrote %d bytes before rejecting", buf.Len())
	}
}

func TestArchiveRejectsNonRegularPath(t *testing.T) {
	var buf bytes.Buffer
	if err := Archive(os.DevNull, &buf); err == nil {
		t.Fatal("want error for a device path")
	}
	if buf.Len() != 0 {
		t.Fatalf("wrote %d bytes for a device path", buf.Len())
	}
}

func TestArchiveRoundTripsThroughExtract(t *testing.T) {
	files := map[string]string{"index.html": "index", "a/b/deep.js": "js", "img/x.png": "png"}
	root := writeTree(t, files)
	var buf bytes.Buffer
	if err := Archive(root, &buf); err != nil {
		t.Fatal(err)
	}
	dst := t.TempDir()
	size, err := share.Extract(&buf, dst, 1<<20)
	if err != nil {
		t.Fatalf("server rejected client archive: %v", err)
	}
	var want int64
	for name, body := range files {
		want += int64(len(body))
		b, err := os.ReadFile(filepath.Join(dst, name))
		if err != nil || string(b) != body {
			t.Fatalf("%s = %q, %v; want %q", name, b, err, body)
		}
	}
	if size != want {
		t.Fatalf("size = %d, want %d", size, want)
	}
	entry, err := share.DetectEntry(dst)
	if err != nil || entry != "" {
		t.Fatalf("DetectEntry = %q, %v; want root index", entry, err)
	}
}
