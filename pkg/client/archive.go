package client

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/hackmajoris/sharefly/pkg/markdown"
)

func validate(path string) (fs.FileInfo, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if fi.IsDir() {
		idx, err := os.Lstat(filepath.Join(path, "index.html"))
		if err != nil || !idx.Mode().IsRegular() {
			return nil, fmt.Errorf("%s: folder has no index.html", path)
		}
	} else if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("%s: not a regular file or folder", path)
	}
	return fi, nil
}

func Archive(path string, w io.Writer) (skipped []string, err error) {
	fi, err := validate(path)
	if err != nil {
		return nil, err
	}
	return archive(path, fi, w)
}

func archive(path string, fi fs.FileInfo, w io.Writer) (skipped []string, err error) {
	root, err := filepath.EvalSymlinks(path)
	if err != nil {
		return nil, err
	}

	gz := gzip.NewWriter(w)
	tw := tar.NewWriter(gz)
	switch {
	case fi.IsDir():
		skipped, err = addDir(tw, root)
	case isMarkdown(path):
		err = addMarkdown(tw, root, filepath.Base(path), fi)
	default:
		err = addFile(tw, root, filepath.Base(path), fi)
	}
	if err != nil {
		return skipped, err
	}
	if err := tw.Close(); err != nil {
		return skipped, err
	}
	return skipped, gz.Close()
}

func addDir(tw *tar.Writer, root string) (skipped []string, err error) {
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Name() == ".git" {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() || d.Name() == ".DS_Store" {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if !d.Type().IsRegular() {
			skipped = append(skipped, filepath.ToSlash(rel))
			return nil
		}
		fi, err := d.Info()
		if err != nil {
			return err
		}
		return addFile(tw, path, filepath.ToSlash(rel), fi)
	})
	return skipped, err
}

func addFile(tw *tar.Writer, path, name string, fi fs.FileInfo) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	hdr := &tar.Header{
		Name:     name,
		Typeflag: tar.TypeReg,
		Mode:     0o644,
		Size:     fi.Size(),
		ModTime:  fi.ModTime(),
	}
	if err := tw.WriteHeader(hdr); err != nil {
		return err
	}
	_, err = io.Copy(tw, f)
	return err
}

func isMarkdown(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	return ext == ".md" || ext == ".markdown"
}

// addMarkdown shares a Markdown file as the rendered page notes.md -> notes.html, so any server serves it as HTML.
func addMarkdown(tw *tar.Writer, path, name string, fi fs.FileInfo) error {
	src, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	base := strings.TrimSuffix(name, filepath.Ext(name))
	page := markdown.Page(src, base, name)
	hdr := &tar.Header{
		Name:     base + ".html",
		Typeflag: tar.TypeReg,
		Mode:     0o644,
		Size:     int64(len(page)),
		ModTime:  fi.ModTime(),
	}
	if err := tw.WriteHeader(hdr); err != nil {
		return err
	}
	_, err = tw.Write(page)
	return err
}
