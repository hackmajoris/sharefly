package client

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

func Archive(path string, w io.Writer) (name string, err error) {
	fi, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	name = filepath.Base(filepath.Clean(path))
	if fi.IsDir() {
		idx, err := os.Lstat(filepath.Join(path, "index.html"))
		if err != nil || !idx.Mode().IsRegular() {
			return "", fmt.Errorf("%s: folder has no index.html", path)
		}
	} else if !fi.Mode().IsRegular() {
		return "", fmt.Errorf("%s: not a regular file or folder", path)
	}

	gz := gzip.NewWriter(w)
	tw := tar.NewWriter(gz)
	if fi.IsDir() {
		err = addDir(tw, path)
	} else {
		err = addFile(tw, path, name, fi)
	}
	if err != nil {
		return "", err
	}
	if err := tw.Close(); err != nil {
		return "", err
	}
	if err := gz.Close(); err != nil {
		return "", err
	}
	return name, nil
}

func addDir(tw *tar.Writer, root string) error {
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Name() == ".DS_Store" {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if !d.Type().IsRegular() {
			fmt.Fprintf(os.Stderr, "warning: skipping %s (not a regular file)\n", rel)
			return nil
		}
		fi, err := d.Info()
		if err != nil {
			return err
		}
		return addFile(tw, path, filepath.ToSlash(rel), fi)
	})
}

func addFile(tw *tar.Writer, path, name string, fi fs.FileInfo) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
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
