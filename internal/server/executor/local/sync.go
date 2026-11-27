package local

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"

	"loop-now/internal/loopctl/guide"
	"loop-now/internal/procgroup"
	"loop-now/internal/server/agentconfig"
	"loop-now/internal/server/executor"
	"loop-now/internal/server/modelcredential"
	"loop-now/internal/server/prompts"
	"loop-now/internal/server/store"
)

// failure is an expected run failure, matching RunErrorCode.
type failure struct {
	code string
	msg  string
}

func (f *failure) Error() string { return f.code + ": " + f.msg }

func fail(code, format string, args ...any) *failure {
	return &failure{code: code, msg: fmt.Sprintf(format, args...)}
}

// prepared is the result of the preparation before startup.
type prepared struct {
	env        []string
	skillDirs  []string
	cliBinDirs []string
	cliBins    []string // template CLI executables put on the run PATH (one for one with cliBinDirs)
}

// prepare syncs the config by the rules: it installs missing template CLIs, rebuilds Skills and the Prompt, deletes and
// then rewrites credential files, and syncs workspace sources. The CLI installs actually performed and the result of
// this sync are recorded in the run record of this run.
func (e *Executor) prepare(ctx context.Context, snap agentconfig.Snapshot, d agentDirs, runID uuid.UUID, larkShim string) (prepared, error) {
	var p prepared
	if snap.LarkBinding != nil && larkShim == "" {
		return p, fail(executor.ErrCredentialUnavailable, "The Lark user runtime channel is not wired up yet.")
	}
	if err := d.ensure(); err != nil {
		return p, err
	}
	// 1. Install the CLIs that are allowed and have a template.
	ghBin := ""
	for _, cli := range snap.CLIs {
		if cli == "lark-cli" && snap.LarkBinding != nil {
			continue // dynamic user scopes use only the registered private-exec shim
		}
		t, ok := e.tmpl.Get(cli)
		if !ok {
			continue
		}
		begin := time.Now()
		dir, attempted, err := e.inst.EnsureReport(ctx, t)
		if attempted {
			data := map[string]any{"cli": t.Name, "version": t.Version, "result": "succeeded", "durationMs": time.Since(begin).Milliseconds()}
			if err != nil {
				data["result"], data["error"] = "failed", err.Error()
			}
			e.event(snap.AgentID, &runID, store.EventCLIInstall, data)
		}
		if err != nil {
			return p, fail(executor.ErrCLIInstallFailed, "%s: %v", cli, err)
		}
		p.cliBinDirs = append(p.cliBinDirs, dir)
		p.cliBins = append(p.cliBins, filepath.Join(dir, t.Binary))
		if cli == "gh" {
			ghBin = filepath.Join(dir, t.Binary)
		}
	}

	begin := time.Now()
	var outcomes []sourceOutcome
	err := e.syncConfig(ctx, snap, d, runID, &p, ghBin, &outcomes)
	if err == nil && larkShim != "" {
		for i, value := range p.env {
			if strings.HasPrefix(value, "PATH=") {
				p.env[i] = "PATH=" + larkShim + string(os.PathListSeparator) + strings.TrimPrefix(value, "PATH=")
			}
		}
	}
	data := map[string]any{"result": "succeeded", "durationMs": time.Since(begin).Milliseconds(), "sources": outcomesData(outcomes)}
	if err != nil {
		data["result"], data["error"] = "failed", failureMessage(err)
	}
	e.event(snap.AgentID, &runID, store.EventWorkspaceSync, data)
	return p, err
}

// syncConfig rebuilds Skills and the Prompt, writes credential files and syncs workspace sources, in that order.
func (e *Executor) syncConfig(ctx context.Context, snap agentconfig.Snapshot, d agentDirs, runID uuid.UUID, p *prepared, ghBin string, outcomes *[]sourceOutcome) error {
	// 2. Skills and the Prompt are overwritten as a whole from the config. Each runtime puts the Skills and the plugin
	// MCP servers where it reads them; what other runtimes would read is removed, so a switch leaves nothing behind.
	drv := e.driverFor(snap)
	for _, other := range e.allDrivers() {
		if other.runtime() != drv.runtime() {
			if err := other.clearAssets(d); err != nil {
				return err
			}
		}
	}
	var err error
	if p.skillDirs, err = drv.syncAssets(ctx, snap, d); err != nil {
		return err
	}
	if err := removeStrayEntries(d); err != nil {
		return err
	}
	// “用本机的登录状态” ("Use local logins"): the runtime's HOME is the user's real home directory; d.home does not change,
	// and credential files, cleanup and reclaiming only ever use d.home. A runtime with the realHome capability (Claude
	// Code, which can only use the user's own local login) always works this way, regardless of the global switch.
	loginHome := ""
	if snap.Local.Logins || capabilities(snap).RealHome {
		if loginHome, err = e.o.RealHome(); err != nil || !filepath.IsAbs(loginHome) {
			return fail(executor.ErrInternal, "Could not read the server user's home directory (\"Use local logins\" needs it).")
		}
	}
	prompt, err := e.appendedPrompt(snap, d, loginHome)
	if err != nil {
		return err
	}
	if prompt != "" {
		if err := os.WriteFile(d.prompt, []byte(prompt), 0o600); err != nil {
			return err
		}
	} else if err := os.Remove(d.prompt); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	// 3. Model credentials are written by the runtime (for Pi, a separate cache, or links to a plugin provider's
	// subscription account; the previous links are removed first); CLI credentials keep the template rules.
	if err := clearProviderLinks(d); err != nil {
		return fail(executor.ErrCredentialUnavailable, "Could not remove the old links of the subscription account: %v", err)
	}
	if err := drv.modelCredentials(snap, d, loginHome); err != nil {
		return err
	}
	// CLI credential files are deleted first, then written.
	if err := e.syncCredentialFiles(snap, d); err != nil {
		return err
	}
	var userPath []string
	if snap.Local.UserPath && e.o.UserPath != nil {
		userPath = e.o.UserPath(ctx)
	}
	log := e.snapshotLog(snap).With("runId", runID)
	// With the login setting on, managed CLIs run through shims that restore the separate HOME; with it off, old shims
	// are removed.
	shimDir, err := syncCLIShims(d, loginHome, p.cliBins, e.loginOnlyEnv())
	if err != nil {
		return err
	}
	pathDirs := p.cliBinDirs
	if shimDir != "" {
		pathDirs = []string{shimDir}
	}
	p.env = e.buildEnv(d, snap, pathDirs, userPath, loginHome, log)
	// 4. Workspace sources: the clones the platform makes for the Agent do not follow the login switch and use the
	// environment as it is with the switch off.
	sourceEnv := p.env
	if loginHome != "" {
		sourceEnv = e.buildEnv(d, snap, p.cliBinDirs, userPath, "", log)
	}
	return e.syncSources(ctx, snap, d, sourceEnv, ghBin, outcomes)
}

// appendedPrompt is the content of the --append-system-prompt file (every runtime reads it as its system or
// developer prompt): the Agent's Prompt, a task step's prompt, then the "Folders and context" section.
func (e *Executor) appendedPrompt(snap agentconfig.Snapshot, d agentDirs, loginHome string) (string, error) {
	section, err := e.contextPrompt(snap, d, loginHome)
	if err != nil {
		return "", err
	}
	return joinPrompt(joinPrompt(snap.Prompt, stepPromptSection(snap.StepPrompt)), section), nil
}

// stepPromptSection is the task step's prompt under its own heading; "" without one.
func stepPromptSection(prompt string) string {
	if strings.TrimSpace(prompt) == "" {
		return ""
	}
	return "## Your prompt for this task step\n\nThe Manager wrote this for the step of the task you are doing, and it holds on every visit of the step. The job of each visit comes in the node message.\n\n" + strings.TrimRight(prompt, "\n")
}

func joinPrompt(a, b string) string {
	switch {
	case b == "":
		return a
	case a == "":
		return b
	}
	return strings.TrimRight(a, "\n") + "\n\n" + b
}

// promptIdentity fingerprints what the system prompt is made of; a change restarts the runtime. A task instance keeps
// its frozen Prompt but takes the prompt of each step it moves to.
func promptIdentity(snap agentconfig.Snapshot) string {
	id := prompts.Identity(snap.PromptLayers)
	if snap.StepPrompt != "" {
		sum := sha256.Sum256([]byte(snap.StepPrompt))
		id += "/step:" + hex.EncodeToString(sum[:8])
	}
	return id
}

// sourceOutcome is how one folder source was handled in this sync.
type sourceOutcome struct {
	path         string
	kind         string
	outcome      string // cloned / kept / files_synced / failed
	filesWritten int
	filesDeleted int
	err          string
}

func outcomesData(list []sourceOutcome) []map[string]any {
	out := make([]map[string]any, 0, len(list))
	for _, o := range list {
		m := map[string]any{"path": o.path, "kind": o.kind, "outcome": o.outcome}
		if o.kind == "files" {
			m["filesWritten"], m["filesDeleted"] = o.filesWritten, o.filesDeleted
		}
		if o.err != "" {
			m["error"] = o.err
		}
		out = append(out, m)
	}
	return out
}

func failureMessage(err error) string {
	var f *failure
	if errors.As(err, &f) {
		return f.msg
	}
	return err.Error()
}

// syncSkills rebuilds the Agent's Skill copies and the built-in Skill loopctl under dir as a whole, and returns each
// Skill's directory.
func (e *Executor) syncSkills(ctx context.Context, snap agentconfig.Snapshot, dir string) ([]string, error) {
	if err := os.RemoveAll(dir); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	var dirs []string
	for _, k := range snap.Skills {
		if k.Name == guide.SkillName {
			e.snapshotLog(snap).Warn("a Skill copy uses the name of a built-in Skill; skipped", "skill", k.Name)
			continue
		}
		for _, f := range k.Files {
			data, err := e.src.ReadBlob(ctx, f.SHA256)
			if err != nil {
				return nil, err
			}
			p := path.Join(k.Name, f.Path)
			if err := root.MkdirAll(path.Dir(p), 0o700); err != nil {
				return nil, err
			}
			if err := root.WriteFile(p, data, fileMode(f.Path)); err != nil {
				return nil, err
			}
		}
		dirs = append(dirs, filepath.Join(dir, k.Name))
	}
	// The built-in Skill loopctl: its content is generated by the loopctl of the same version, not stored in the
	// database, and written on every sync.
	for name, data := range guide.Files() {
		p := path.Join(guide.SkillName, name)
		if err := root.MkdirAll(path.Dir(p), 0o700); err != nil {
			return nil, err
		}
		if err := root.WriteFile(p, data, fileMode(name)); err != nil {
			return nil, err
		}
	}
	dirs = append(dirs, filepath.Join(dir, guide.SkillName))
	return dirs, nil
}

// fileMode makes scripts in a Skill executable.
func fileMode(p string) fs.FileMode {
	switch path.Ext(p) {
	case ".sh", ".py", ".js", ".mjs", ".rb", ".pl":
		return 0o700
	}
	return 0o600
}

func (e *Executor) syncCredentialFiles(snap agentconfig.Snapshot, d agentDirs) error {
	root, err := os.OpenRoot(d.home)
	if err != nil {
		return err
	}
	defer root.Close()
	// Mutable SDK caches must not survive a changed captured authorization.
	if err := root.RemoveAll(".loop-now/gcloud"); err != nil {
		return err
	}
	// Dynamic DB account directories must not survive a removed grant/CLI.
	if err := root.RemoveAll(".loop-now/db"); err != nil {
		return err
	}
	for _, p := range e.tmpl.AllCredentialFilePaths() {
		if err := root.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("delete the old credential file %s: %w", p, err)
		}
	}
	for _, cli := range snap.CLIs {
		if cli == "lark-cli" && snap.LarkBinding != nil {
			continue
		}
		for _, f := range snap.CLIPayloads[cli].Files {
			if err := root.MkdirAll(path.Dir(f.Path), 0o700); err != nil {
				return err
			}
			if err := root.WriteFile(f.Path, f.Content, 0o600); err != nil {
				return fmt.Errorf("write the credential file %s: %w", f.Path, err)
			}
		}
	}
	return nil
}

func (e *Executor) syncSources(ctx context.Context, snap agentconfig.Snapshot, d agentDirs, env []string, ghBin string, outcomes *[]sourceOutcome) error {
	root, err := os.OpenRoot(d.workspace)
	if err != nil {
		return err
	}
	defer root.Close()
	for _, src := range snap.Sources {
		exists := false
		if fi, err := root.Lstat(src.Path); err == nil && fi.IsDir() {
			exists = true
		}
		o := sourceOutcome{path: src.Path, kind: src.Kind}
		switch src.Kind {
		case "git":
			if exists {
				o.outcome = "kept" // an existing repository is not pulled, overwritten or reset
				*outcomes = append(*outcomes, o)
				continue
			}
			if err := e.cloneRepo(ctx, root, d, src, env, ghBin); err != nil {
				o.outcome, o.err = "failed", failureMessage(err)
				*outcomes = append(*outcomes, o)
				return err
			}
			o.outcome = "cloned"
		case "files":
			if err := e.syncFilesSource(ctx, root, src, exists, &o); err != nil {
				o.outcome, o.err = "failed", err.Error()
				*outcomes = append(*outcomes, o)
				return fail(executor.ErrWorkspaceSyncFailed, "Source %s: %v", src.Path, err)
			}
			o.outcome = "files_synced"
		}
		*outcomes = append(*outcomes, o)
	}
	return nil
}

func (e *Executor) syncFilesSource(ctx context.Context, root *os.Root, src store.FolderSource, exists bool, o *sourceOutcome) error {
	files, err := e.src.SourceFilesForSync(ctx, src.ID)
	if err != nil {
		return err
	}
	full := !exists || src.SyncedAt == nil
	if err := root.MkdirAll(src.Path, 0o755); err != nil {
		return err
	}
	var processed []store.SyncFile
	for _, f := range files {
		p := path.Join(src.Path, f.Path)
		switch {
		case f.Pending == "delete":
			if !full {
				if err := root.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
					return err
				}
				o.filesDeleted++
			}
			processed = append(processed, f)
			continue
		case full || f.Pending == "write":
			// The first sync writes all files; files that were edited by hand overwrite what is on disk.
		default:
			if _, err := root.Lstat(p); err == nil {
				continue // existing files nobody edited are not overwritten
			} else if !errors.Is(err, fs.ErrNotExist) {
				return err
			}
		}
		data, err := e.src.ReadBlob(ctx, f.SHA256)
		if err != nil {
			return err
		}
		if err := root.MkdirAll(path.Dir(p), 0o755); err != nil {
			return err
		}
		if err := writeFileAtomic(root, p, data); err != nil {
			return err
		}
		o.filesWritten++
		if f.Pending != "none" {
			processed = append(processed, f)
		}
	}
	return e.src.MarkSourceSynced(ctx, src.ID, processed)
}

// writeFileAtomic writes a temp file in the same directory first and then renames it over the target, so the Agent
// never reads a half-written file.
func writeFileAtomic(root *os.Root, p string, data []byte) error {
	tmp := path.Join(path.Dir(p), ".loop-now-tmp-"+randHex())
	if err := root.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	if err := root.Rename(tmp, p); err != nil {
		_ = root.Remove(tmp)
		return err
	}
	return nil
}

func (e *Executor) cloneRepo(ctx context.Context, root *os.Root, d agentDirs, src store.FolderSource, env []string, ghBin string) error {
	tmpName := ".loop-now-tmp-" + randHex()
	tmpAbs := filepath.Join(d.workspace, tmpName)
	defer root.RemoveAll(tmpName)

	ctx, cancel := context.WithTimeout(ctx, e.o.CloneTimeout)
	defer cancel()
	baseArgs := []string{"-c", "credential.helper="}
	if ghBin != "" {
		baseArgs = append(baseArgs, "-c", "credential.helper=!"+ghBin+" auth git-credential")
	}
	run := func(args ...string) ([]byte, error) {
		cmd := exec.CommandContext(ctx, e.o.GitBin, append(append([]string{}, baseArgs...), args...)...)
		cmd.Env = env
		cmd.Dir = d.workspace
		cmd.Cancel = procgroup.Cancel(cmd)
		cmd.WaitDelay = 5 * time.Second
		return procgroup.CombinedOutput(cmd)
	}
	failed := func(reason string) error {
		if ctx.Err() != nil {
			return fail(executor.ErrWorkspaceSyncFailed, "Source %s: the clone timed out after %s.", src.Path, e.o.CloneTimeout)
		}
		return fail(executor.ErrWorkspaceSyncFailed, "Source %s: %s", src.Path, reason)
	}
	if src.GitCommit != "" {
		// Only full object IDs are accepted: a branch/ref expression must never
		// turn a captured task commit into a later branch head.
		if _, err := hex.DecodeString(src.GitCommit); err != nil || (len(src.GitCommit) != 40 && len(src.GitCommit) != 64) {
			return failed("the pinned commit is invalid")
		}
		initArgs := []string{"init", "--quiet"}
		if len(src.GitCommit) == 64 {
			initArgs = append(initArgs, "--object-format=sha256")
		}
		initArgs = append(initArgs, tmpAbs)
		for _, args := range [][]string{
			initArgs,
			{"-C", tmpAbs, "remote", "add", "--", "origin", src.GitURL},
			{"-C", tmpAbs, "fetch", "--depth=1", "--no-tags", "origin", src.GitCommit},
			{"-C", tmpAbs, "checkout", "--detach", "FETCH_HEAD"},
		} {
			if _, err := run(args...); err != nil {
				return failed("the pinned commit is not available (the exact shallow fetch failed)")
			}
		}
		head, err := run("-C", tmpAbs, "rev-parse", "HEAD")
		if err != nil || !strings.EqualFold(strings.TrimSpace(string(head)), src.GitCommit) {
			return failed("the pinned commit failed verification")
		}
	} else {
		out, err := run("clone", "--depth=1", "--single-branch", "--no-local", "--branch", src.GitBranch, "--", src.GitURL, tmpAbs)
		if err != nil {
			return failed("clone failed: " + tail(string(out), 2000))
		}
	}
	if err := root.MkdirAll(path.Dir(src.Path), 0o755); err != nil {
		return err
	}
	if err := root.Rename(tmpName, src.Path); err != nil {
		return fail(executor.ErrWorkspaceSyncFailed, "Source %s: %v", src.Path, err)
	}
	return nil
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "…" + s[len(s)-n:]
}

func randHex() string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func seedModelAuth(snap agentconfig.Snapshot, d agentDirs) error {
	data := []byte("{}")
	if snap.ModelCredential.Kind() != "" {
		var e error
		data, e = snap.ModelCredential.AuthFile(snap.Provider)
		if e != nil {
			return e
		}
	}
	defer clear(data)
	return modelcredential.WriteAuthFile(d.pi, data)
}

// syncModelsJSON writes only the Provider fragment captured for the current run; an empty config must delete the old
// file.
func syncModelsJSON(snap agentconfig.Snapshot, d agentDirs) error {
	root, err := os.OpenRoot(d.pi)
	if err != nil {
		return err
	}
	defer root.Close()
	if len(snap.ModelsJSON) == 0 {
		if err := root.Remove("models.json"); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		return nil
	}
	name := ".models-" + randHex() + ".tmp"
	defer root.Remove(name)
	if err := root.WriteFile(name, snap.ModelsJSON, 0o600); err != nil {
		return err
	}
	return root.Rename(name, "models.json")
}
