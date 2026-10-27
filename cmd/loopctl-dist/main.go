// loopctl-dist 为 macOS 与 Linux 的 arm64、amd64 交叉编译 loopctl，生成 SHA256SUMS 与 manifest.json。
//
//	go run ./cmd/loopctl-dist -version <版本> -out <目录>
//
// 产物写到 <目录>/<版本>/。服务端用同一个版本号构建，并读取其中的 manifest.json。
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Platforms 是构建的平台。
var Platforms = []struct{ OS, Arch string }{
	{"darwin", "arm64"}, {"darwin", "amd64"}, {"linux", "amd64"}, {"linux", "arm64"},
}

// Manifest 是 manifest.json 的内容。
type Manifest struct {
	SchemaVersion int        `json:"schemaVersion"`
	Program       string     `json:"program"`
	Version       string     `json:"version"`
	GoVersion     string     `json:"goVersion"`
	Artifacts     []Artifact `json:"artifacts"`
}

// Artifact 是一个平台的二进制文件。
type Artifact struct {
	OS     string `json:"os"`
	Arch   string `json:"arch"`
	File   string `json:"file"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

var versionPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]{0,127}$`)

func main() {
	version := flag.String("version", "", "版本号；默认取 git describe --tags --always --dirty")
	out := flag.String("out", "", "输出目录，产物写到 <out>/<版本>/")
	flag.Parse()
	if *out == "" {
		fmt.Fprintln(os.Stderr, "loopctl-dist：需要 -out")
		os.Exit(2)
	}
	ctx := context.Background()
	root, err := moduleRoot(ctx)
	if err != nil {
		fail(err)
	}
	if *version == "" {
		b, err := exec.CommandContext(ctx, "git", "-C", root, "describe", "--tags", "--always", "--dirty").Output()
		if err != nil {
			fail(fmt.Errorf("无法用 git describe 确定版本号，请用 -version 指定：%w", err))
		}
		*version = strings.TrimSpace(string(b))
	}
	m, dir, err := Build(ctx, root, *version, *out)
	if err != nil {
		fail(err)
	}
	fmt.Printf("loopctl %s：%d 个平台的产物已写入 %s\n", m.Version, len(m.Artifacts), dir)
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "loopctl-dist：", err)
	os.Exit(1)
}

func moduleRoot(ctx context.Context) (string, error) {
	b, err := exec.CommandContext(ctx, "go", "env", "GOMOD").Output()
	if err != nil {
		return "", err
	}
	mod := strings.TrimSpace(string(b))
	if mod == "" || mod == os.DevNull {
		return "", errors.New("当前目录不在 Go module 中")
	}
	return filepath.Dir(mod), nil
}

// Build 构建全部平台，写出二进制、SHA256SUMS 与 manifest.json，返回清单与产物目录。
func Build(ctx context.Context, root, version, out string) (*Manifest, string, error) {
	if !versionPattern.MatchString(version) {
		return nil, "", fmt.Errorf("版本号 %q 只能含字母、数字与 . _ + -", version)
	}
	dir := filepath.Join(out, version)
	if entries, err := os.ReadDir(dir); err == nil && len(entries) > 0 {
		return nil, "", fmt.Errorf("%s 已存在且不为空，请换一个输出目录或先清理", dir)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, "", err
	}
	goVersion, err := exec.CommandContext(ctx, "go", "env", "GOVERSION").Output()
	if err != nil {
		return nil, "", err
	}
	m := &Manifest{SchemaVersion: 1, Program: "loopctl", Version: version, GoVersion: strings.TrimSpace(string(goVersion))}
	for _, p := range Platforms {
		file := "loopctl-" + p.OS + "-" + p.Arch
		cmd := exec.CommandContext(ctx, "go", "build", "-trimpath", "-buildvcs=false",
			"-ldflags", "-s -w -X loop-now/internal/buildinfo.version="+version,
			"-o", filepath.Join(dir, file), "./cmd/loopctl")
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS="+p.OS, "GOARCH="+p.Arch)
		if b, err := cmd.CombinedOutput(); err != nil {
			return nil, "", fmt.Errorf("构建 %s/%s 失败：%v\n%s", p.OS, p.Arch, err, b)
		}
		size, sum, err := digest(filepath.Join(dir, file))
		if err != nil {
			return nil, "", err
		}
		m.Artifacts = append(m.Artifacts, Artifact{OS: p.OS, Arch: p.Arch, File: file, Size: size, SHA256: sum})
	}
	sort.Slice(m.Artifacts, func(i, j int) bool { return m.Artifacts[i].File < m.Artifacts[j].File })
	var sums bytes.Buffer
	for _, a := range m.Artifacts {
		fmt.Fprintf(&sums, "%s  %s\n", a.SHA256, a.File) // 与 shasum -a 256 的输出格式相同
	}
	if err := os.WriteFile(filepath.Join(dir, "SHA256SUMS"), sums.Bytes(), 0o644); err != nil {
		return nil, "", err
	}
	b, _ := json.MarshalIndent(m, "", "  ")
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), append(b, '\n'), 0o644); err != nil {
		return nil, "", err
	}
	return m, dir, nil
}

func digest(path string) (int64, string, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, "", err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return 0, "", err
	}
	return n, hex.EncodeToString(h.Sum(nil)), nil
}
