package app_test

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/auth"
	"loop-now/internal/server/taskmodel"
	"loop-now/internal/server/workflow"
)

type piDump struct {
	Args         []string `json:"args"`
	Cwd          string   `json:"cwd"`
	SystemPrompt string   `json:"systemPrompt"`
}

// real resolves symlinks (on macOS the temporary directory /var points to /private/var, and the working directory Pi
// reports is the resolved path).
func real(t *testing.T, p string) string {
	t.Helper()
	r, err := filepath.EvalSymlinks(p)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func readDump(t *testing.T, path string) piDump {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var d piDump
	if err := json.Unmarshal(b, &d); err != nil {
		t.Fatal(err)
	}
	return d
}

// When Pi starts, the appended system prompt carries the context entry of the project (global for a platform Agent) and
// the absolute paths of the two folders; Pi's working directory is the Agent folder; changes to the entry take effect
// only after Pi restarts; entry files in the platform's own directory are deleted at startup.
func TestFolderContextInjectionActualAppFakePi(t *testing.T) {
	w := promoteWorld(t)
	dump := filepath.Join(t.TempDir(), "fake-env.json")
	t.Setenv("FAKEPI_DUMP", dump)
	w.cfg.ExecEnvPassthrough = []string{"FAKEPI_DUMP"}
	w.cfg.ContextDir = filepath.Join(t.TempDir(), "loop_now")
	s := w.start(nil)
	defer s.stop()
	project := decode[apitypes.Context](t, s.must(200, "GET", "/context", nil)).Project.ID
	s.must(201, "POST", "/projects/"+project.String()+"/credential-accounts", map[string]any{"purpose": "model", "provider": "anthropic", "name": "synthetic", "credential": map[string]string{"type": "api_key", "key": "synthetic-key"}})
	path, agent := s.createAgent("Ctx")
	s.must(200, "PUT", "/projects/"+project.String()+"/context-folder/content?path=AGENTS.md", []byte("Project conventions v1"))
	s.must(200, "PUT", path+"/folder/content?path=AGENTS.md", []byte("My own notes"))
	root := filepath.Join(w.cfg.WorkRoot, "agents", agent.ID.String())
	for _, p := range []string{filepath.Join(root, "AGENTS.md"), filepath.Join(root, "pi", "CLAUDE.md")} {
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("must not be read"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	s.must(202, "POST", path+"/messages", map[string]any{"text": "!text=ok"})
	s.waitIdle(path)
	workspace := filepath.Join(root, "workspace")
	projectDir := filepath.Join(w.cfg.ContextDir, "projects", project.String())
	d := readDump(t, dump)
	if d.Cwd != real(t, workspace) || !slices.Contains(d.Args, "--append-system-prompt") {
		t.Fatalf("Pi's working directory should be the Agent folder, with an appended system prompt: %s %v", d.Cwd, d.Args)
	}
	for _, want := range []string{"Project conventions v1", projectDir, workspace, filepath.Join(projectDir, "AGENTS.md"), "You are a test assistant"} {
		if !strings.Contains(d.SystemPrompt, want) {
			t.Fatalf("the appended system prompt should contain %q: %s", want, d.SystemPrompt)
		}
	}
	if strings.Contains(d.SystemPrompt, "My own notes") {
		t.Fatal("Pi reads the Agent's own entry from its working directory; it should not be repeated in the appended prompt")
	}
	for _, p := range []string{filepath.Join(root, "AGENTS.md"), filepath.Join(root, "pi", "CLAUDE.md")} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Fatalf("entry files in the platform directory should be removed at startup: %s", p)
		}
	}
	// Changing the entry does not trigger a restart: the Pi being reused still has the old prompt.
	s.must(200, "PUT", "/projects/"+project.String()+"/context-folder/content?path=AGENTS.md", []byte("Project conventions v2"))
	if err := os.Remove(dump); err != nil {
		t.Fatal(err)
	}
	s.must(202, "POST", path+"/messages", map[string]any{"text": "!text=again"})
	s.waitIdle(path)
	if _, err := os.Stat(dump); !os.IsNotExist(err) {
		t.Fatal("changing the entry should not restart Pi")
	}
	// After a config change causes a restart, the new entry is read.
	s.must(200, "PATCH", path, map[string]any{"prompt": "New prompt", "expectedPromptVersion": agent.PromptVersion})
	s.must(202, "POST", path+"/messages", map[string]any{"text": "!text=third"})
	s.waitIdle(path)
	if d = readDump(t, dump); !strings.Contains(d.SystemPrompt, "Project conventions v2") || !strings.Contains(d.SystemPrompt, "New prompt") {
		t.Fatalf("the new entry should be read after the restart: %s", d.SystemPrompt)
	}

	// Platform Agent: the global context folder; it cannot see the project's.
	ppath, _ := s.createPlatformAgent("PlatformCtx")
	s.must(200, "PUT", ppath+"/model-credential", map[string]any{"provider": "anthropic", "key": "sk-test"})
	s.must(200, "PUT", "/context-folder/content?path=AGENTS.md", []byte("Global conventions"))
	s.must(202, "POST", ppath+"/messages", map[string]any{"text": "!text=ok"})
	s.waitIdle(ppath)
	d = readDump(t, dump)
	if !strings.Contains(d.SystemPrompt, "Global conventions") || !strings.Contains(d.SystemPrompt, filepath.Join(w.cfg.ContextDir, "global")) || strings.Contains(d.SystemPrompt, "Project conventions") {
		t.Fatalf("the platform Agent should see only the global context: %s", d.SystemPrompt)
	}
}

// A new task (snapshot v9) copies the base Agent's whole folder when it starts (APFS clone): the instance works in the
// copy and its changes do not affect the original folder; folder sources are no longer captured.
func TestTaskCopiesAgentFolderActualAppFakePi(t *testing.T) {
	w := promoteWorld(t)
	dump := filepath.Join(t.TempDir(), "fake-env.json")
	t.Setenv("FAKEPI_DUMP", dump)
	w.cfg.ExecEnvPassthrough = []string{"FAKEPI_DUMP"}
	logs := &syncBuffer{}
	w.logTo = logs
	s := w.start(nil)
	defer s.stop()
	ctx := context.Background()
	project := decode[apitypes.Context](t, s.must(200, "GET", "/context", nil)).Project.ID
	s.must(201, "POST", "/projects/"+project.String()+"/credential-accounts", map[string]any{"purpose": "model", "provider": "anthropic", "name": "synthetic", "credential": map[string]string{"type": "api_key", "key": "synthetic-key"}})
	_, manager := s.createAgent("Manager")
	wpath, worker := s.createAgent("Worker")
	s.must(200, "PUT", "/projects/"+project.String()+"/manager", map[string]any{"agentId": manager.ID})
	s.must(200, "PUT", wpath+"/folder/content?path=AGENTS.md", []byte("Worker's notes"))
	s.must(200, "PUT", wpath+"/folder/content?path=node_modules/pkg/index.js", []byte("module.exports = 1"))
	base := filepath.Join(w.cfg.WorkRoot, "agents", worker.ID.String(), "workspace")
	if err := os.Symlink("node_modules/pkg", filepath.Join(base, "pkg")); err != nil {
		t.Fatal(err)
	}
	raw, err := s.a.Tasks.Create(ctx, auth.Principal{Kind: auth.KindAgent, ID: manager.ID}, taskmodel.CreateInput{ActionID: uuid.New(), Title: "copy", Request: map[string]any{}, Workflow: workflow.Definition{StartStepID: "work", Steps: []workflow.Step{{ID: "work", Title: "work", Description: " !sleep=30s !noabort !text=working", AgentID: worker.ID, InputFields: []workflow.Field{}, OutputFields: []workflow.Field{}, Inputs: map[string]workflow.Source{}}}, Edges: []workflow.Edge{}}})
	if err != nil {
		t.Fatal(err)
	}
	task := decode[taskmodel.Detail](t, raw)
	runtime := task.Nodes[0].RuntimeID
	waitFor(t, "node running", func() bool {
		var n int
		return s.a.Store.Pool.QueryRow(ctx, `SELECT count(*) FROM task_turns WHERE runtime_id=$1 AND status='running'`, runtime).Scan(&n) == nil && n == 1
	})
	copyDir := filepath.Join(w.cfg.WorkRoot, "agents", runtime.String(), "workspace")
	if b, err := os.ReadFile(filepath.Join(copyDir, "node_modules/pkg/index.js")); err != nil || string(b) != "module.exports = 1" {
		t.Fatalf("the instance workspace should be a copy of the base folder: %q %v", b, err)
	}
	if l, _ := os.Readlink(filepath.Join(copyDir, "pkg")); l != "node_modules/pkg" {
		t.Fatalf("symlinks should be copied as is: %q", l)
	}
	d := readDump(t, dump)
	if d.Cwd != real(t, copyDir) || !strings.Contains(d.SystemPrompt, "reclaimed when the task ends") || !strings.Contains(d.SystemPrompt, filepath.Join("projects", project.String())) {
		t.Fatalf("the task instance should work in the copy and see the project context: %s %s", d.Cwd, d.SystemPrompt)
	}
	if err := os.WriteFile(filepath.Join(copyDir, "AGENTS.md"), []byte("changed in the task"), 0o644); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(base, "AGENTS.md")); string(b) != "Worker's notes" {
		t.Fatal("changing the copy should not affect the base Agent's folder")
	}

	// The console reads the instance's folder (not the base Agent's) through its node, read only.
	nodeFolder := "/tasks/" + task.Task.ID.String() + "/nodes/" + task.Nodes[0].ID.String() + "/folder"
	if loc := decode[apitypes.FolderLocation](t, s.must(200, "GET", nodeFolder, nil)); loc.Path != copyDir || loc.RealPath != real(t, copyDir) {
		t.Fatalf("node folder location: %+v, want %s (%s)", loc, copyDir, real(t, copyDir))
	}
	if b := s.must(200, "GET", nodeFolder+"/content?path=AGENTS.md", nil); string(b) != "changed in the task" {
		t.Fatalf("node folder content should come from the instance's copy: %q", b)
	}
	s.must(404, "GET", nodeFolder+"/content?path=missing.png", nil)
	s.must(422, "GET", nodeFolder+"/content?path=../"+worker.ID.String()+"/workspace/AGENTS.md", nil)
	s.must(404, "GET", "/tasks/"+task.Task.ID.String()+"/nodes/"+uuid.NewString()+"/folder", nil)
	s.must(404, "PUT", nodeFolder+"/content?path=AGENTS.md", []byte("x")) // read only: there is no write endpoint
	var snapshot []byte
	var sources string
	if err := s.a.Store.Pool.QueryRow(ctx, `SELECT snapshot, sources FROM task_instances WHERE id=$1`, runtime).Scan(&snapshot, &sources); err != nil {
		t.Fatal(err)
	}
	var env struct {
		SchemaVersion int `json:"schemaVersion"`
	}
	if err := json.Unmarshal(snapshot, &env); err != nil || env.SchemaVersion != 11 || sources != "[]" {
		t.Fatalf("a new task should be snapshot v11 with no frozen sources: %d %s %v", env.SchemaVersion, sources, err)
	}
	if !strings.Contains(logs.String(), "copied the task working directory") || !strings.Contains(logs.String(), "clonefile") {
		t.Fatalf("the copy method should be logged (APFS clone): %s", logs.String())
	}

	// Once the task ended its folder is reclaimed: the content answers 404 and reading does not make the folder again.
	s.must(200, "POST", "/tasks/"+task.Task.ID.String()+"/cancel", map[string]any{"actionId": uuid.New()})
	waitFor(t, "instance folder reclaimed", func() bool {
		_, err := os.Stat(filepath.Join(w.cfg.WorkRoot, "agents", runtime.String()))
		return errors.Is(err, fs.ErrNotExist)
	})
	s.must(404, "GET", nodeFolder+"/content?path=AGENTS.md", nil)
	if _, err := os.Stat(copyDir); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("reading a reclaimed folder should not create it: %v", err)
	}
	if loc := decode[apitypes.FolderLocation](t, s.must(200, "GET", nodeFolder, nil)); loc.Path != copyDir {
		t.Fatalf("the location is still given after the folder is gone: %+v", loc)
	}
}
