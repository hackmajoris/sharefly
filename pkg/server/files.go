package server

import (
	"io/fs"
	"net/http"
	"path"
)

type noListingFS struct {
	fs http.FileSystem
}

func (n noListingFS) Open(name string) (http.File, error) {
	f, err := n.fs.Open(name)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	if info.IsDir() {
		idx, err := n.fs.Open(path.Join(name, "index.html"))
		if err != nil {
			f.Close()
			return nil, fs.ErrNotExist
		}
		idx.Close()
	}
	return f, nil
}

type noStoreWriter struct {
	http.ResponseWriter
}

func (w noStoreWriter) WriteHeader(code int) {
	w.Header().Set("Cache-Control", "private, no-store")
	w.ResponseWriter.WriteHeader(code)
}

func (w noStoreWriter) Write(b []byte) (int, error) {
	w.Header().Set("Cache-Control", "private, no-store")
	return w.ResponseWriter.Write(b)
}

func FilesHandler(sharesDir string) http.Handler {
	files := http.FileServer(noListingFS{http.Dir(sharesDir)})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		files.ServeHTTP(noStoreWriter{w}, r)
	})
}
