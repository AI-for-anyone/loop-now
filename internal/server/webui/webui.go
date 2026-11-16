// Package webui serves the console's static files. Release builds embed the web build output in the binary with the
// embedweb tag; a directory given by LOOP_NOW_WEB_DIR can be used instead. On the console port the server hands every
// path outside the API to this package.
package webui

import (
	"bytes"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"path"
	"strings"
	"time"
)

// Embedded returns the embedded console files; nil when built without the embedweb tag.
func Embedded() fs.FS { return embedded() }

// Handler serves the files in fsys following single-page app rules:
//   - files under assets/ have a content hash in their name and are cached long-term; a missing one gets a 404 with
//     no fallback, so HTML is never returned as a script or stylesheet;
//   - other existing files and index.html are not cached, so a reload picks up an update;
//   - other missing paths fall back to index.html and are handled by the front-end router.
//
// Only GET and HEAD are accepted. When fsys is nil every request gets a 404 explaining how to provide the console.
func Handler(fsys fs.FS) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			h.Set("Allow", "GET, HEAD")
			http.Error(w, "Only GET and HEAD are supported.", http.StatusMethodNotAllowed)
			return
		}
		if fsys == nil {
			http.Error(w, "This server has no embedded console. Build it with the embedweb tag, or set LOOP_NOW_WEB_DIR to the console directory.", http.StatusNotFound)
			return
		}
		name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		if name == "" {
			name = "index.html"
		}
		data, err := readFile(fsys, name)
		switch {
		case err == nil:
		case strings.HasPrefix(name, "assets/"):
			http.NotFound(w, r)
			return
		default:
			name = "index.html"
			if data, err = readFile(fsys, name); err != nil {
				http.Error(w, "The console directory has no index.html.", http.StatusNotFound)
				return
			}
		}
		if strings.HasPrefix(name, "assets/") {
			h.Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			h.Set("Cache-Control", "no-cache")
		}
		// No modification time: embedded files do not have one, and index.html is not cached anyway.
		http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(data))
	})
}

// readFile reads a regular file from fsys; directories and invalid paths count as nonexistent.
func readFile(fsys fs.FS, name string) ([]byte, error) {
	if !fs.ValidPath(name) {
		return nil, fs.ErrNotExist
	}
	f, err := fsys.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if st, err := f.Stat(); err != nil {
		return nil, err
	} else if st.IsDir() {
		return nil, errors.New("is a directory")
	}
	return io.ReadAll(f)
}
