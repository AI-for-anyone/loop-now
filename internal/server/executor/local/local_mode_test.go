package local

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/google/uuid"

	"loop-now/internal/execenv"
	"loop-now/internal/server/agentconfig"
	"loop-now/internal/server/executor"
)

// 本机模式：生效且存在的本机目录写进“文件夹与上下文”一节，用户 PATH 接在执行 PATH 之后；
// 清单或开关变化时下一次派发按新配置重启（保留会话），目录是否存在的变化不触发重启。
func TestLocalModePromptPathAndRestart(t *testing.T) {
	// 用本平台写法的执行 PATH（Windows 上以分号分隔）；用户 PATH 里重复了其中一个目录（Windows 上换了大小写），只应出现一次。
	baseA, baseB, userBin := filepath.Join(t.TempDir(), "base-a"), filepath.Join(t.TempDir(), "base-b"), filepath.Join(t.TempDir(), "user-bin")
	sep := string(os.PathListSeparator)
	basePath := baseA + sep + baseB
	dupA := baseA
	if runtime.GOOS == "windows" {
		dupA = strings.ToUpper(baseA)
	}
	h := newHarness(t, func(o *Options) {
		o.ContextDir = t.TempDir()
		o.ExecBasePath = basePath
		o.UserPath = func(context.Context) []string { return []string{dupA, userBin} }
	}, "")
	dumpPath := filepath.Join(t.TempDir(), "dump.json")
	h.env["FAKEPI_DUMP"] = dumpPath
	root := t.TempDir()
	code := filepath.Join(root, "code")
	if err := os.MkdirAll(code, 0o700); err != nil {
		t.Fatal(err)
	}
	later := filepath.Join(root, "later")
	id := h.agent(func(s *agentconfig.Snapshot) {
		s.Local = agentconfig.LocalAccess{UserPath: true, Directories: []agentconfig.LocalDirectory{
			{Path: code, Note: "前端仓库", Inherited: true}, {Path: later},
		}}
	})
	if res, _ := h.exec(id, "first"); res.Outcome != executor.OutcomeCompleted {
		t.Fatal(res)
	}
	d := readDump(t, dumpPath)
	if !strings.Contains(d.SystemPrompt, "## 文件夹与上下文") || !strings.Contains(d.SystemPrompt, "本机目录") ||
		!strings.Contains(d.SystemPrompt, "  - "+code+"：前端仓库\n") || strings.Contains(d.SystemPrompt, later) {
		t.Fatalf("系统提示里的本机目录不对：\n%s", d.SystemPrompt)
	}
	if !strings.Contains(d.SystemPrompt, "完整的读写权限") || !strings.Contains(d.SystemPrompt, "不会被回收") {
		t.Fatalf("应如实说明读写权限：\n%s", d.SystemPrompt)
	}
	if path := d.Env["PATH"]; !strings.HasSuffix(path, basePath+sep+userBin) || countDir(path, baseA) != 1 {
		t.Fatalf("用户 PATH 应接在最后且不重复：%s", path)
	}
	pid := h.pid(id)

	// 目录后来被建出来：指纹不含存在与否，不重启；下次自然重启时才会列出。
	if err := os.MkdirAll(later, 0o700); err != nil {
		t.Fatal(err)
	}
	if res, _ := h.exec(id, "second"); res.Outcome != executor.OutcomeCompleted || h.pid(id) != pid {
		t.Fatal("目录存在与否的变化不应重启 Pi")
	}

	// 清单变化：下一次派发重启，带上新的清单，会话不变。
	h.src.mu.Lock()
	h.src.snaps[id].Local.Directories = append(h.src.snaps[id].Local.Directories, agentconfig.LocalDirectory{Path: root, Note: "整个目录"})
	session := h.src.recorded[id]
	h.src.mu.Unlock()
	if res, _ := h.exec(id, "third"); res.Outcome != executor.OutcomeCompleted || h.pid(id) == pid {
		t.Fatal("清单变化应在下一次派发重启 Pi")
	}
	d = readDump(t, dumpPath)
	if !strings.Contains(d.SystemPrompt, "  - "+later+"\n") || !strings.Contains(d.SystemPrompt, "  - "+root+"：整个目录\n") || argValue(d.Args, "--session") != session {
		t.Fatalf("重启后的系统提示或会话不对：%v\n%s", d.Args, d.SystemPrompt)
	}
	pid = h.pid(id)

	// 关掉“用本机的 CLI”：重启，PATH 不再带用户目录。
	h.src.mu.Lock()
	h.src.snaps[id].Local.UserPath = false
	h.src.mu.Unlock()
	if res, _ := h.exec(id, "fourth"); res.Outcome != executor.OutcomeCompleted || h.pid(id) == pid {
		t.Fatal("PATH 开关变化应重启 Pi")
	}
	if d = readDump(t, dumpPath); strings.Contains(d.Env["PATH"], userBin) || !strings.HasSuffix(d.Env["PATH"], basePath) {
		t.Fatalf("关掉后 PATH 不应带用户目录：%s", d.Env["PATH"])
	}
}

func TestLocalDirectoriesPromptWording(t *testing.T) {
	dir := t.TempDir()
	snap := agentconfig.Snapshot{Local: agentconfig.LocalAccess{Directories: []agentconfig.LocalDirectory{{Path: dir}, {Path: filepath.Join(dir, "missing")}}}}
	normal := localDirectoriesPrompt(snap)
	if !strings.Contains(normal, "  - "+dir+"\n") || strings.Contains(normal, "missing") || strings.Contains(normal, "任务副本") {
		t.Fatalf("常态 Agent：%s", normal)
	}
	snap.TaskID = uuid.New()
	if task := localDirectoriesPrompt(snap); !strings.Contains(task, "不在任务副本里") || !strings.Contains(task, "其他任务可能同时在用") {
		t.Fatalf("任务内实例应说明不是副本：%s", task)
	}
	if got := localDirectoriesPrompt(agentconfig.Snapshot{Local: agentconfig.LocalAccess{Directories: []agentconfig.LocalDirectory{{Path: filepath.Join(dir, "missing")}}}}); got != "" {
		t.Fatalf("没有存在的目录时不应写这一条：%q", got)
	}
	// 没有上下文文件夹（测试装配）时仍写出本机目录这一节。
	e := &Executor{}
	section, err := e.contextPrompt(agentconfig.Snapshot{Local: agentconfig.LocalAccess{Directories: []agentconfig.LocalDirectory{{Path: dir}}}}, agentDirs{})
	if err != nil || !strings.HasPrefix(section, "## 文件夹与上下文\n\n- 本机目录") {
		t.Fatalf("没有上下文文件夹时：%q %v", section, err)
	}
}

func TestAppendUserPath(t *testing.T) {
	sep := string(os.PathListSeparator)
	got := appendUserPath([]string{"/loopctl", "/cli/gh", "/node" + sep + "/usr/bin"}, []string{"/usr/bin", "", "/home/me/bin", "/cli/gh", "/home/me/bin"})
	want := []string{"/loopctl", "/cli/gh", "/node" + sep + "/usr/bin", "/home/me/bin"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("appendUserPath = %v", got)
	}
	// Windows 的目录不分大小写：SYSTEMROOT 拼出的 System32 与用户 PATH 里的 system32 是同一个。
	got = appendUserPath([]string{`C:\WINDOWS\System32` + sep + `C:\WINDOWS`}, []string{`C:\WINDOWS\system32`, `c:\windows`, `C:\Tools\bin`})
	want = []string{`C:\WINDOWS\System32` + sep + `C:\WINDOWS`, `C:\Tools\bin`}
	if runtime.GOOS == "windows" && strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("Windows 上应不分大小写去重：%v", got)
	}
}

// countDir 数 PATH 里与 dir 相同的目录有几个（按 execenv.PathKey 比较）。
func countDir(path, dir string) int {
	n := 0
	for _, d := range filepath.SplitList(path) {
		if execenv.PathKey(d) == execenv.PathKey(dir) {
			n++
		}
	}
	return n
}
