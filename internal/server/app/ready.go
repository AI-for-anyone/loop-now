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

// consoleFiles 选出控制台的静态文件：配置了 LOOP_NOW_WEB_DIR 时用那个目录（必须有 index.html），
// 否则用二进制里内嵌的；都没有时为 nil（API 之外的路径回答 404 并说明）。
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

// Ready 是就绪文件的内容：桌面壳据此找到控制台地址（监听端口为 0 时只能从这里知道），以及开了对外端口时它的 https 地址。
type Ready struct {
	URL         string `json:"url"`
	PID         int    `json:"pid"`
	Version     string `json:"version"`
	ExternalURL string `json:"externalUrl,omitempty"`
}

// writeReadyFile 原子地写出就绪文件：先写同目录下的临时文件再改名，读的一方不会读到半个文件。
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
