package folders

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// 真实 APFS 克隆的耗时：构造接近真实工作区形状的目录（≥20 万个小文件，嵌套的 node_modules，总计数 GB），
// 测 CopyTree 的耗时（任务准备时它在基础 Agent 暂停派发的窗口内执行）。写入数 GB 数据，只在设置
// LOOP_NOW_CLONE_BENCH=1 时运行；目录放在 LOOP_NOW_CLONE_BENCH_DIR（默认系统临时目录）。
func TestCopyTreeLargeTreeTiming(t *testing.T) {
	if os.Getenv("LOOP_NOW_CLONE_BENCH") != "1" {
		t.Skip("设置 LOOP_NOW_CLONE_BENCH=1 才运行（会写入数 GB）")
	}
	parent := os.Getenv("LOOP_NOW_CLONE_BENCH_DIR")
	if parent == "" {
		parent = os.TempDir()
	}
	base, err := os.MkdirTemp(parent, "clone-bench-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(base)
	src := filepath.Join(base, "workspace")
	small := bytes.Repeat([]byte("module.exports = function () { return 42 }\n"), 300) // ≈13KB
	large := bytes.Repeat([]byte("x"), 64<<20)
	begin := time.Now()
	files, total := 0, int64(0)
	// 3 个仓库 × 每个 node_modules 下 400 个包，每包 3 层嵌套目录、每层约 56 个文件；外加少量大文件（构建产物、二进制）。
	for repo := range 3 {
		for pkg := range 400 {
			dir := filepath.Join(src, fmt.Sprintf("repo%d", repo), "node_modules", fmt.Sprintf("pkg%03d", pkg))
			for depth := range 3 {
				dir = filepath.Join(dir, fmt.Sprintf("lib%d", depth))
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Fatal(err)
				}
				for i := range 56 {
					if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("f%02d.js", i)), small, 0o644); err != nil {
						t.Fatal(err)
					}
					files++
					total += int64(len(small))
				}
			}
		}
		dist := filepath.Join(src, fmt.Sprintf("repo%d", repo), "dist")
		if err := os.MkdirAll(dist, 0o755); err != nil {
			t.Fatal(err)
		}
		for i := range 4 {
			if err := os.WriteFile(filepath.Join(dist, fmt.Sprintf("bundle%d.bin", i)), large, 0o644); err != nil {
				t.Fatal(err)
			}
			files++
			total += int64(len(large))
		}
	}
	t.Logf("构造：%d 个文件，%.2f GB，用时 %v", files, float64(total)/(1<<30), time.Since(begin))
	free := func() string {
		out, _ := exec.Command("df", "-k", base).Output()
		lines := strings.Split(strings.TrimSpace(string(out)), "\n")
		return lines[len(lines)-1]
	}
	before := free()
	res, err := CopyTree(src, filepath.Join(base, "task", "workspace"))
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("CopyTree：方式 %s，耗时 %v", res.Method, res.Duration)
	t.Logf("复制前 df：%s", before)
	t.Logf("复制后 df：%s", free())
}
