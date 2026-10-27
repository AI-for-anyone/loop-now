// Package loopctldist 读取并校验 loopctl 的构建产物（cmd/loopctl-dist 生成的 <目录>/<版本>/），
// 把本机平台的二进制安装到工作根目录的工具目录，并在对外端口上提供下载。
package loopctldist

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
)

// Program 是清单中的程序名；SchemaVersion 是支持的清单格式版本。
const (
	Program       = "loopctl"
	SchemaVersion = 1
)

// Manifest 是 manifest.json 的内容（格式由 cmd/loopctl-dist 定义）。
type Manifest struct {
	SchemaVersion int        `json:"schemaVersion"`
	Program       string     `json:"program"`
	Version       string     `json:"version"`
	GoVersion     string     `json:"goVersion"`
	Artifacts     []Artifact `json:"artifacts"`
}

// Artifact 是一个平台的二进制。
type Artifact struct {
	OS     string `json:"os"`
	Arch   string `json:"arch"`
	File   string `json:"file"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// Dist 是校验过的构建产物。
type Dist struct {
	Dir      string // <目录>/<版本>
	Manifest Manifest
	sums     map[string]string // 下载文件名 → sha256（含 manifest.json 与 SHA256SUMS）
}

// Load 读取 <root>/<version>/manifest.json 并校验：程序名、版本与服务端相同，各文件的大小与 SHA-256 与清单一致，
// SHA256SUMS 与清单一致，且包含本机平台的二进制。任何一项不符都返回错误，服务端据此拒绝启动。
func Load(root, version string) (*Dist, error) {
	dir := filepath.Join(root, version)
	raw, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return nil, fmt.Errorf("读取 loopctl 构建清单失败（需要与服务端同版本 %s 的产物，用 go run ./cmd/loopctl-dist -version %s -out %s 生成）：%w",
			version, version, root, err)
	}
	var m Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("loopctl 构建清单不是合法的 JSON：%w", err)
	}
	switch {
	case m.SchemaVersion != SchemaVersion:
		return nil, fmt.Errorf("loopctl 构建清单的格式版本为 %d，服务端只支持 %d", m.SchemaVersion, SchemaVersion)
	case m.Program != Program:
		return nil, fmt.Errorf("构建清单的程序为 %q，应为 %q", m.Program, Program)
	case m.Version != version:
		return nil, fmt.Errorf("loopctl 构建清单的版本 %s 与服务端版本 %s 不一致", m.Version, version)
	}
	d := &Dist{Dir: dir, Manifest: m, sums: map[string]string{"manifest.json": sum(raw)}}
	for _, a := range m.Artifacts {
		if a.File == "" || filepath.Base(a.File) != a.File || strings.HasPrefix(a.File, ".") || a.File == "manifest.json" || a.File == "SHA256SUMS" {
			return nil, fmt.Errorf("构建清单中的文件名不合法：%q", a.File)
		}
		got, size, err := fileSum(filepath.Join(dir, a.File))
		if err != nil {
			return nil, fmt.Errorf("读取 %s 失败：%w", a.File, err)
		}
		if size != a.Size || got != a.SHA256 {
			return nil, fmt.Errorf("%s 与构建清单不符：大小 %d（清单 %d），SHA-256 %s（清单 %s）", a.File, size, a.Size, got, a.SHA256)
		}
		d.sums[a.File] = a.SHA256
	}
	sums, err := os.ReadFile(filepath.Join(dir, "SHA256SUMS"))
	if err != nil {
		return nil, fmt.Errorf("读取 SHA256SUMS 失败：%w", err)
	}
	if err := d.checkSums(sums); err != nil {
		return nil, err
	}
	d.sums["SHA256SUMS"] = sum(sums)
	if _, err := d.Local(); err != nil {
		return nil, err
	}
	return d, nil
}

// checkSums 确认 SHA256SUMS 与清单中的文件一一对应。
func (d *Dist) checkSums(b []byte) error {
	listed := map[string]string{}
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		h, f, ok := strings.Cut(line, "  ")
		if !ok {
			return fmt.Errorf("SHA256SUMS 格式不对：%q", line)
		}
		listed[f] = h
	}
	if len(listed) != len(d.Manifest.Artifacts) {
		return fmt.Errorf("SHA256SUMS 有 %d 个文件，构建清单有 %d 个", len(listed), len(d.Manifest.Artifacts))
	}
	for _, a := range d.Manifest.Artifacts {
		if listed[a.File] != a.SHA256 {
			return fmt.Errorf("SHA256SUMS 中 %s 的摘要与构建清单不一致", a.File)
		}
	}
	return nil
}

// Local 返回本机平台的二进制。
func (d *Dist) Local() (Artifact, error) {
	i := slices.IndexFunc(d.Manifest.Artifacts, func(a Artifact) bool { return a.OS == runtime.GOOS && a.Arch == runtime.GOARCH })
	if i < 0 {
		return Artifact{}, fmt.Errorf("loopctl 构建产物中没有本机平台 %s/%s 的二进制", runtime.GOOS, runtime.GOARCH)
	}
	return d.Manifest.Artifacts[i], nil
}

// Install 把本机平台的二进制安装到 <workRoot>/tools/loopctl/<版本>/bin/loopctl，复制后再校验一次摘要，返回 bin 目录。
// 已安装且摘要相符时不重复复制。
func (d *Dist) Install(workRoot string) (string, error) {
	a, err := d.Local()
	if err != nil {
		return "", err
	}
	binDir := filepath.Join(workRoot, "tools", Program, d.Manifest.Version, "bin")
	target := filepath.Join(binDir, Program)
	if got, _, err := fileSum(target); err == nil && got == a.SHA256 {
		return binDir, nil
	}
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return "", err
	}
	src, err := os.Open(filepath.Join(d.Dir, a.File))
	if err != nil {
		return "", err
	}
	defer src.Close()
	tmp, err := os.CreateTemp(binDir, ".loopctl-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name())
	if _, err := io.Copy(tmp, src); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Chmod(0o755); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if got, _, err := fileSum(tmp.Name()); err != nil || got != a.SHA256 {
		return "", fmt.Errorf("安装 loopctl 后摘要不符：%s（清单 %s）%v", got, a.SHA256, err)
	}
	if err := os.Rename(tmp.Name(), target); err != nil {
		return "", err
	}
	return binDir, nil
}

// Handler 在 /downloads/loopctl/ 下提供清单、SHA256SUMS 与清单中列出的二进制，响应带 Content-Length 与 X-Content-SHA256。
func (d *Dist) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/downloads/loopctl/")
		h, ok := d.sums[name]
		if !ok {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"error":{"code":"not_found","message":"文件不存在"}}`+"\n")
			return
		}
		f, err := os.Open(filepath.Join(d.Dir, name))
		if err != nil {
			http.Error(w, "读取文件失败", http.StatusInternalServerError)
			return
		}
		defer f.Close()
		st, err := f.Stat()
		if err != nil {
			http.Error(w, "读取文件失败", http.StatusInternalServerError)
			return
		}
		ct := "application/octet-stream"
		switch name {
		case "manifest.json":
			ct = "application/json"
		case "SHA256SUMS":
			ct = "text/plain; charset=utf-8"
		}
		w.Header().Set("Content-Type", ct)
		w.Header().Set("Content-Length", strconv.FormatInt(st.Size(), 10))
		w.Header().Set("X-Content-SHA256", h)
		w.WriteHeader(http.StatusOK)
		if r.Method != http.MethodHead {
			_, _ = io.Copy(w, f)
		}
	})
}

func sum(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func fileSum(p string) (string, int64, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}
