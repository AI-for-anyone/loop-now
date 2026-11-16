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

// Timing of a real APFS clone: build a directory shaped like a real workspace (>= 200,000 small files, nested
// node_modules, several GB in total) and measure how long CopyTree takes (during task preparation it runs inside the
// window where dispatch to the base Agent is paused). It writes several GB of data, so it runs only when
// LOOP_NOW_CLONE_BENCH=1 is set; the directory goes in LOOP_NOW_CLONE_BENCH_DIR (default: the system temporary
// directory).
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
	// 3 repositories x 400 packages under each node_modules, each package with 3 levels of nested directories and about
	// 56 files per level; plus a few large files (build output, binaries).
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
