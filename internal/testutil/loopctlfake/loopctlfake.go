// Package loopctlfake 为测试生成与当前版本号相同的 loopctl 构建产物（格式同 cmd/loopctl-dist），
// 二进制是打印版本号的小脚本，不需要交叉编译。
package loopctlfake

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"loop-now/internal/buildinfo"
)

// Dir 生成产物并返回根目录（服务端的 LOOP_NOW_LOOPCTL_DIST_DIR）。产物位于 <根目录>/<版本>/。
func Dir(t testing.TB) string {
	t.Helper()
	root := t.TempDir()
	version := buildinfo.Version()
	dir := filepath.Join(root, version)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	type artifact struct {
		OS     string `json:"os"`
		Arch   string `json:"arch"`
		File   string `json:"file"`
		Size   int64  `json:"size"`
		SHA256 string `json:"sha256"`
	}
	var arts []artifact
	var sums strings.Builder
	platforms := [][2]string{{"darwin", "arm64"}, {"darwin", "amd64"}, {"linux", "amd64"}, {"linux", "arm64"}}
	for _, p := range platforms {
		file := "loopctl-" + p[0] + "-" + p[1]
		body := []byte(fmt.Sprintf("#!/bin/sh\necho '{\"version\":\"%s\",\"platform\":\"%s/%s\"}'\n", version, p[0], p[1]))
		if err := os.WriteFile(filepath.Join(dir, file), body, 0o755); err != nil {
			t.Fatal(err)
		}
		h := sha256.Sum256(body)
		sum := hex.EncodeToString(h[:])
		arts = append(arts, artifact{OS: p[0], Arch: p[1], File: file, Size: int64(len(body)), SHA256: sum})
		fmt.Fprintf(&sums, "%s  %s\n", sum, file)
	}
	if err := os.WriteFile(filepath.Join(dir, "SHA256SUMS"), []byte(sums.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	m, _ := json.MarshalIndent(map[string]any{"schemaVersion": 1, "program": "loopctl", "version": version, "goVersion": runtime.Version(), "artifacts": arts}, "", "  ")
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), append(m, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}
