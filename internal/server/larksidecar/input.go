package larksidecar

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// Roots come from captured runtime metadata, never argv, env, profile or CWD.
// Every component is opened beneath an opened root without following symlinks
// (openat+NOFOLLOW on Unix, os.Root on Windows); JSON is read before sandbox
// exec, so a path check followed by a pathname open is insufficient.
func openScopeInput(path, base string, roots []string) (*os.File, error) {
	if !filepath.IsAbs(path) {
		path = filepath.Join(base, path)
	}
	path = filepath.Clean(path)
	for _, root := range roots {
		if !filepath.IsAbs(root) || root != filepath.Clean(root) {
			continue
		}
		rel, e := filepath.Rel(root, path)
		if e != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		r, e := openScopeRoot(root)
		if e != nil {
			return nil, e
		}
		f, e := openInputAt(r, rel)
		r.Close()
		return f, e
	}
	return nil, errors.New("input outside scope")
}
