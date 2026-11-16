package app

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"loop-now/internal/server/config"
	"loop-now/internal/server/webui"
)

// consoleFiles picks the console's static files: when LOOP_NOW_WEB_DIR is configured, that directory (which must
// contain index.html); otherwise the files embedded in the binary; nil when neither exists (paths outside the API
// answer 404 with an explanation).
func consoleFiles(dir string) (fs.FS, string, error) {
	if dir != "" {
		if _, err := os.Stat(filepath.Join(dir, "index.html")); err != nil {
			return nil, "", fmt.Errorf("控制台目录 %s 里没有 index.html（%s）：%w", dir, config.EnvWebDir, err)
		}
		return os.DirFS(dir), dir, nil
	}
	if embedded := webui.Embedded(); embedded != nil {
		return embedded, "内嵌", nil
	}
	return nil, "", nil
}

// Ready is the content of the ready file: the desktop shell uses it to find the console address (the only way to learn
// it when the listen port is 0) and, when the external port is on, its https address.
type Ready struct {
	URL         string `json:"url"`
	PID         int    `json:"pid"`
	Version     string `json:"version"`
	ExternalURL string `json:"externalUrl,omitempty"`
}

// writeReadyFile writes the ready file atomically: it writes a temporary file in the same directory and then renames
// it, so a reader never sees half a file.
func writeReadyFile(path string, r Ready) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".ready-*")
	if err != nil {
		return err
	}
	if _, err = tmp.Write(append(b, '\n')); err == nil {
		err = tmp.Close()
	} else {
		_ = tmp.Close()
	}
	if err == nil {
		err = os.Rename(tmp.Name(), path)
	}
	if err != nil {
		_ = os.Remove(tmp.Name())
	}
	return err
}
