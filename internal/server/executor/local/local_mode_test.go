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

// Local mode: local directories that are in effect and exist are written into the "Folders and context" section, and
// the user's PATH is appended after the run PATH; when the list or the switches change, the next dispatch restarts with
// the new config (keeping the session), while a directory coming into or out of existence does not trigger a restart.
func TestLocalModePromptPathAndRestart(t *testing.T) {
	// Use a run PATH written the way this platform writes it (separated by semicolons on Windows); the user's PATH
	// repeats one of its directories (in different case on Windows), which must appear only once.
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
			{Path: code, Note: "Frontend repository", Inherited: true}, {Path: later},
		}}
	})
	if res, _ := h.exec(id, "first"); res.Outcome != executor.OutcomeCompleted {
		t.Fatal(res)
	}
	d := readDump(t, dumpPath)
	if !strings.Contains(d.SystemPrompt, "## Folders and context") || !strings.Contains(d.SystemPrompt, "Local directories") ||
		!strings.Contains(d.SystemPrompt, "  - "+code+": Frontend repository\n") || strings.Contains(d.SystemPrompt, later) {
		t.Fatalf("wrong local directories in the system prompt:\n%s", d.SystemPrompt)
	}
	if !strings.Contains(d.SystemPrompt, "full read and write access") || !strings.Contains(d.SystemPrompt, "never reclaimed") {
		t.Fatalf("should state the read and write access truthfully:\n%s", d.SystemPrompt)
	}
	if path := d.Env["PATH"]; !strings.HasSuffix(path, basePath+sep+userBin) || countDir(path, baseA) != 1 {
		t.Fatalf("the user's PATH should come last without duplicates: %s", path)
	}
	pid := h.pid(id)

	// The directory is created later: the fingerprint does not include whether it exists, so there is no restart; it is
	// listed only at the next natural restart.
	if err := os.MkdirAll(later, 0o700); err != nil {
		t.Fatal(err)
	}
	if res, _ := h.exec(id, "second"); res.Outcome != executor.OutcomeCompleted || h.pid(id) != pid {
		t.Fatal("a directory coming into or out of existence should not restart Pi")
	}

	// The list changes: the next dispatch restarts with the new list, and the session stays the same.
	h.src.mu.Lock()
	h.src.snaps[id].Local.Directories = append(h.src.snaps[id].Local.Directories, agentconfig.LocalDirectory{Path: root, Note: "Whole directory"})
	session := h.src.recorded[id]
	h.src.mu.Unlock()
	if res, _ := h.exec(id, "third"); res.Outcome != executor.OutcomeCompleted || h.pid(id) == pid {
		t.Fatal("a change to the list should restart Pi at the next dispatch")
	}
	d = readDump(t, dumpPath)
	if !strings.Contains(d.SystemPrompt, "  - "+later+"\n") || !strings.Contains(d.SystemPrompt, "  - "+root+": Whole directory\n") || argValue(d.Args, "--session") != session {
		t.Fatalf("wrong system prompt or session after the restart: %v\n%s", d.Args, d.SystemPrompt)
	}
	pid = h.pid(id)

	// Turn off “用本机的 CLI” ("Use local CLIs"): restart, and PATH no longer includes the user's directories.
	h.src.mu.Lock()
	h.src.snaps[id].Local.UserPath = false
	h.src.mu.Unlock()
	if res, _ := h.exec(id, "fourth"); res.Outcome != executor.OutcomeCompleted || h.pid(id) == pid {
		t.Fatal("a change to the PATH switch should restart Pi")
	}
	if d = readDump(t, dumpPath); strings.Contains(d.Env["PATH"], userBin) || !strings.HasSuffix(d.Env["PATH"], basePath) {
		t.Fatalf("PATH should not include the user's directories once turned off: %s", d.Env["PATH"])
	}
}

func TestLocalDirectoriesPromptWording(t *testing.T) {
	dir := t.TempDir()
	snap := agentconfig.Snapshot{Local: agentconfig.LocalAccess{Directories: []agentconfig.LocalDirectory{{Path: dir}, {Path: filepath.Join(dir, "missing")}}}}
	normal := localDirectoriesPrompt(snap)
	if !strings.Contains(normal, "  - "+dir+"\n") || strings.Contains(normal, "missing") || strings.Contains(normal, "task copy") {
		t.Fatalf("regular Agent: %s", normal)
	}
	snap.TaskID = uuid.New()
	if task := localDirectoriesPrompt(snap); !strings.Contains(task, "not part of the task copy") || !strings.Contains(task, "other tasks may be using them") {
		t.Fatalf("an instance in a task should say the directories are not copies: %s", task)
	}
	if got := localDirectoriesPrompt(agentconfig.Snapshot{Local: agentconfig.LocalAccess{Directories: []agentconfig.LocalDirectory{{Path: filepath.Join(dir, "missing")}}}}); got != "" {
		t.Fatalf("this entry should not be written when no directory exists: %q", got)
	}
	// Without context folders (test setup), the local directories section is still written.
	e := &Executor{}
	section, err := e.contextPrompt(agentconfig.Snapshot{Local: agentconfig.LocalAccess{Directories: []agentconfig.LocalDirectory{{Path: dir}}}}, agentDirs{}, "")
	if err != nil || !strings.HasPrefix(section, "## Folders and context\n\n- Local directories") {
		t.Fatalf("without context folders: %q %v", section, err)
	}
}

func TestAppendUserPath(t *testing.T) {
	sep := string(os.PathListSeparator)
	got := appendUserPath([]string{"/loopctl", "/cli/gh", "/node" + sep + "/usr/bin"}, []string{"/usr/bin", "", "/home/me/bin", "/cli/gh", "/home/me/bin"})
	want := []string{"/loopctl", "/cli/gh", "/node" + sep + "/usr/bin", "/home/me/bin"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("appendUserPath = %v", got)
	}
	// Windows directories are case-insensitive: the System32 built from SYSTEMROOT and the system32 on the user's PATH
	// are the same directory.
	got = appendUserPath([]string{`C:\WINDOWS\System32` + sep + `C:\WINDOWS`}, []string{`C:\WINDOWS\system32`, `c:\windows`, `C:\Tools\bin`})
	want = []string{`C:\WINDOWS\System32` + sep + `C:\WINDOWS`, `C:\Tools\bin`}
	if runtime.GOOS == "windows" && strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("on Windows duplicates should be removed case-insensitively: %v", got)
	}
}

// countDir counts how many directories on PATH are the same as dir (compared with execenv.PathKey).
func countDir(path, dir string) int {
	n := 0
	for _, d := range filepath.SplitList(path) {
		if execenv.PathKey(d) == execenv.PathKey(dir) {
			n++
		}
	}
	return n
}
