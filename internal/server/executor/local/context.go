package local

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"

	"loop-now/internal/server/agentconfig"
	"loop-now/internal/server/agentruntime"
	"loop-now/internal/server/folders"
)

// entryCandidates are the context entry file names Pi looks for in PI_CODING_AGENT_DIR, in the working directory and in
// each of its parent directories (the first one that exists wins, in this order).
var entryCandidates = []string{"AGENTS.override.md", "AGENTS.md", "AGENTS.MD", "CLAUDE.md", "CLAUDE.MD"}

// contextPrompt returns the "Folders and context" section appended to the system prompt when Pi starts: the absolute
// paths of the Agent's own folder and of the context folders it can see, the local directories in effect, and the
// verbatim text (not truncated) of the project's (for a platform Agent, the global) context entry AGENTS.md. The
// Agent's own entry is read by Pi from the working directory and is not repeated here. It is read only at startup, so
// changes to an entry take effect only the next time Pi restarts; it also plays no part in deciding whether to restart
// (changes to the list of local directories trigger a restart through snap.Local.Identity).
//
// A runtime that does not read AGENTS.md by itself (capability readsEntryFile; Claude Code, which the platform also
// stops from reading CLAUDE.md in the working directory) gets the verbatim text of the Agent's own entry in this section
// too, using the same candidate names as Pi and taking the first one that exists.
func (e *Executor) contextPrompt(snap agentconfig.Snapshot, d agentDirs, loginHome string) (string, error) {
	local := localDirectoriesPrompt(snap) + loginsPrompt(loginHome) + providerLoginPrompt(snap, loginHome)
	runtime := agentruntime.ForProvider(snap.Provider)
	cwd := workDir(snap, d)
	if cwd != d.workspace {
		local = fmt.Sprintf("- Your working directory: %s, a folder on this computer the person picked. You work there as they do in their terminal; its own CLAUDE.md or AGENTS.md belongs to that folder.\n", cwd) + local
	}
	// A runtime that reads AGENTS.md by itself reads it in its working directory: with a working folder, the Agent's
	// own entry is included here instead.
	readsOwnEntry := runtime.Capabilities.ReadsEntryFile && cwd == d.workspace
	ownEntry, ownContent := "", ""
	if !readsOwnEntry {
		var err error
		if ownEntry, ownContent, err = readOwnEntry(d.workspace); err != nil {
			return "", err
		}
	}
	if e.o.ContextDir == "" {
		if local == "" && strings.TrimSpace(ownContent) == "" {
			return "", nil
		}
		var b strings.Builder
		b.WriteString("## Folders and context\n\n" + local)
		writeEntry(&b, ownEntry, ownContent)
		return b.String(), nil
	}
	layer := folders.ContextFolder(e.o.ContextDir, snap.ContextProjectID)
	if err := os.MkdirAll(layer, 0o700); err != nil {
		return "", err
	}
	name, shared := "Project context folder", "read and written by this project's Agents (including their instances in tasks) and by people"
	if snap.ContextProjectID == nil {
		name, shared = "Global context folder", "read and written by platform Agents and by people"
	}
	own := "Your own folder (also your working directory and your own context)"
	if cwd != d.workspace {
		own = "Your own folder (your own context; the person sees it as your folder)"
	}
	if snap.TaskID != uuid.Nil {
		own = "Your folder in this task (your working directory; copied from your regular folder when the task started and reclaimed when the task ends)"
	}
	entry := filepath.Join(layer, "AGENTS.md")
	runtimeName, loaded := runtime.Name, "already loaded by "+runtime.Name+" if it exists"
	if !readsOwnEntry {
		loaded = "its content is included below if it exists"
	}
	var b strings.Builder
	b.WriteString("## Folders and context\n\n")
	fmt.Fprintf(&b, "- %s: %s. Its entry file is AGENTS.md at the root (%s).\n", own, d.workspace, loaded)
	fmt.Fprintf(&b, "- %s: %s, %s. Its entry file is %s.\n", name, layer, shared, entry)
	fmt.Fprintf(&b, "- Both entry files are loaded only when %s starts; after you change them, the change appears in the system prompt only after the next restart. For when to write, which layer to write to, and how to organize it, see the \"Context folders\" section of Skill loopctl.\n", runtimeName)
	b.WriteString(local)
	writeEntry(&b, ownEntry, ownContent)
	content, err := readEntry(layer)
	if err != nil {
		return "", err
	}
	writeEntry(&b, entry, content)
	return b.String(), nil
}

// writeEntry puts the verbatim text of one context entry into this section; nothing is written when the content is
// empty.
func writeEntry(b *strings.Builder, path, content string) {
	if strings.TrimSpace(content) != "" {
		fmt.Fprintf(b, "\n<context_entry path=%q>\n%s\n</context_entry>\n", path, content)
	}
}

// readOwnEntry reads the context entry at the root of the Agent's own folder: the first regular file that exists, in
// the order of entryCandidates (via os.Root, not following symlinks that escape it), and returns its path and text;
// both are empty when there is none.
func readOwnEntry(workspace string) (string, string, error) {
	root, err := os.OpenRoot(workspace)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", "", nil
		}
		return "", "", err
	}
	defer root.Close()
	for _, name := range entryCandidates {
		f, err := root.Open(name)
		if err != nil {
			continue
		}
		info, err := f.Stat()
		if err != nil || !info.Mode().IsRegular() {
			f.Close()
			continue
		}
		data, err := io.ReadAll(f)
		f.Close()
		if err != nil {
			return "", "", err
		}
		return filepath.Join(workspace, name), strings.TrimPrefix(string(data), "\ufeff"), nil
	}
	return "", "", nil
}

// localDirectoriesPrompt returns the "Local directories" item (with a trailing newline): it lists only entries that
// exist right now and are directories, and is empty when there are none. Pi is not sandboxed, so this informs rather
// than authorizes: it states truthfully that the Agent has full read and write access to these directories and that
// changes land directly on the real files.
func localDirectoriesPrompt(snap agentconfig.Snapshot) string {
	var items strings.Builder
	for _, dir := range snap.Local.Directories {
		if info, err := os.Stat(dir.Path); err != nil || !info.IsDir() {
			continue
		}
		if dir.Note != "" {
			fmt.Fprintf(&items, "  - %s: %s\n", dir.Path, dir.Note)
		} else {
			fmt.Fprintf(&items, "  - %s\n", dir.Path)
		}
	}
	if items.Len() == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("- Local directories: folders on this machine that the user registered for you to use directly. They are not copies. You have full read and write access, and your changes land directly on the user's real files; ")
	if snap.TaskID != uuid.Nil {
		b.WriteString("they are not part of the task copy and are not reclaimed with the task, and the regular Agent and other tasks may be using them at the same time. ")
	} else {
		b.WriteString("they are never reclaimed. ")
	}
	b.WriteString("The platform keeps no trash for them, so before deleting or changing anything, make sure it is the right place, and ask a person when unsure. Entry files such as AGENTS.md in these directories are not loaded automatically; read them yourself when needed.\n")
	b.WriteString(items.String())
	return b.String()
}

// providerLoginPrompt explains, when “用本机的登录状态” ("Use local logins") is on and the model provider comes from a
// plugin whose state follows the account, that the extension's own local login is what is used.
func providerLoginPrompt(snap agentconfig.Snapshot, home string) string {
	if home == "" || snap.ProviderState == nil || len(snap.ProviderState.Paths) == 0 {
		return ""
	}
	// A platform Agent logs in to the provider in its own model credential; a project Agent uses a project account.
	account := "the subscription account configured in the project"
	if snap.ContextProjectID == nil {
		account = "the subscription account logged in for this Agent"
	}
	return "- Model subscription: your model provider " + snap.Provider + " uses the extension's own login in the user's home directory on this machine, not " + account + ".\n"
}

// loginsPrompt returns the "Local logins" item (with a trailing newline): when
// “用本机的登录状态” ("Use local logins") is on, it tells the Agent that it uses local tools as the user, and that it
// should not keep retrying tools that need interactive authorization (Pi has no terminal, so a popup or password prompt
// just waits until it times out); empty when the setting is off.
func loginsPrompt(home string) string {
	if home == "" {
		return ""
	}
	return "- Local logins: your HOME is the user's own home directory " + home + ". Logins that tools on this machine already have can be used directly, " +
		"which means you act as the user, and your changes affect the user's own configuration and state; CLIs that have accounts configured for you in Loop Now still use those accounts. " +
		"When a local tool needs interactive authorization (a popup, a browser login, a password or a confirmation), do not keep retrying; tell the user to complete the authorization once on this machine first.\n"
}

// readEntry reads AGENTS.md at the root of a context folder (via os.Root, not following symlinks that point outside the
// folder); empty when it does not exist or is not a regular file.
func readEntry(dir string) (string, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return "", err
	}
	defer root.Close()
	f, err := root.Open("AGENTS.md")
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", nil // a symlink escaping the folder or the like: treat as no entry
	}
	defer f.Close()
	if info, err := f.Stat(); err != nil || !info.Mode().IsRegular() {
		return "", nil
	}
	data, err := io.ReadAll(f)
	return strings.TrimPrefix(string(data), "\ufeff"), err
}

// removeStrayEntries deletes context entry files from the platform's own directories (the Agent root directory and
// PI_CODING_AGENT_DIR): Pi would read them into the system prompt, yet these directories are not any layer of context.
func removeStrayEntries(d agentDirs) error {
	for _, dir := range []string{d.root, d.pi} {
		for _, name := range entryCandidates {
			if err := os.Remove(filepath.Join(dir, name)); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return err
			}
		}
	}
	return nil
}

// AncestorEntryFiles returns the context entry files in the agents directory of the work root and in each of its parent
// directories. Pi reads them into every Agent's system prompt; these directories are not managed by the platform, so
// they are only checked and reported at startup.
func AncestorEntryFiles(workRoot string) []string {
	var found []string
	dir := filepath.Join(workRoot, "agents")
	for {
		for _, name := range entryCandidates {
			p := filepath.Join(dir, name)
			if info, err := os.Stat(p); err == nil && info.Mode().IsRegular() {
				found = append(found, p)
				break
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return found
		}
		dir = parent
	}
}
