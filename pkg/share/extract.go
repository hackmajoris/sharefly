package share

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

var ErrTooLarge = errors.New("archive too large")

func Extract(r io.Reader, dst string, maxBytes int64) (size int64, err error) {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return 0, err
	}
	defer gz.Close()
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return 0, err
	}
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return size, nil
		}
		if err != nil {
			return size, err
		}
		if !filepath.IsLocal(hdr.Name) {
			return size, fmt.Errorf("unsafe path %q", hdr.Name)
		}
		target := filepath.Join(dst, hdr.Name)
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return size, err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return size, err
			}
			n, err := writeFile(target, tr, maxBytes-size)
			size += n
			if err != nil {
				return size, err
			}
		default:
			return size, fmt.Errorf("unsupported entry %q: only regular files and directories allowed", hdr.Name)
		}
	}
}

func writeFile(path string, r io.Reader, remaining int64) (int64, error) {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return 0, err
	}
	n, err := io.Copy(f, io.LimitReader(r, remaining+1))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil && n > remaining {
		err = ErrTooLarge
	}
	return n, err
}

func DetectEntry(dir string) (string, error) {
	if fi, err := os.Stat(filepath.Join(dir, "index.html")); err == nil && fi.Mode().IsRegular() {
		return "", nil
	}
	var files []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type().IsRegular() {
			rel, err := filepath.Rel(dir, path)
			if err != nil {
				return err
			}
			files = append(files, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if len(files) != 1 {
		return "", errors.New("need index.html at the root or exactly one file")
	}
	return files[0], nil
}
