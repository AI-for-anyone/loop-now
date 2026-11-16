package local

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"loop-now/internal/pluginmanifest"
	"loop-now/internal/privatefs"
	"loop-now/internal/server/agentconfig"
	"loop-now/internal/server/executor"
)

// State of a plugin provider's subscription account (plugins/SPEC.md §13): before Pi starts, auth.json in the Agent's
// Pi directory is linked to auth.json in the account's state directory, and the HOME paths declared to follow the
// account are linked to the paths of the same name in the state directory; several Agents of the same project share one
// copy, which the extension refreshes itself. The links created this time are recorded in the Agent directory and
// removed before the next start, release and cleanup, so switching account, switching model and unbinding all clean up
// fully, and deleting the Agent directory cannot reach the state directory through a link. Links are only ever created
// in the platform's own Agent directory, never in the user's real home directory.

// providerLinksFile records the links created last time (absolute paths).
const providerLinksFile = "provider-links.json"

// clearProviderLinks deletes the links created last time: only the links themselves in the Agent HOME and the Pi
// directory (on Windows auth.json is a hard link, which is deleted too); it neither follows them nor touches the state
// directory they point to.
func clearProviderLinks(d agentDirs) error {
	record := filepath.Join(d.root, providerLinksFile)
	b, err := os.ReadFile(record)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var links []string
	if json.Unmarshal(b, &links) == nil {
		for _, l := range links {
			if !within(d.home, l) && l != filepath.Join(d.pi, "auth.json") {
				continue
			}
			info, err := os.Lstat(l)
			if err != nil {
				continue
			}
			if isLink(l, info) || (l == filepath.Join(d.pi, "auth.json") && info.Mode().IsRegular()) {
				if err := os.Remove(l); err != nil && !errors.Is(err, fs.ErrNotExist) {
					return err
				}
			}
		}
	}
	return os.Remove(record)
}

// mkdirPrivate creates dir and any missing parent directories inside the state directory root. The state directory
// holds subscription tokens: every newly created level is explicitly made accessible only to the current user (on
// Windows a protected DACL, not just the ACL inherited from the parent); every existing level must be a real directory,
// never a symlink or junction that leads outside the state directory.
func mkdirPrivate(root, dir string) error {
	if !within(root, dir) {
		return errors.New("the path is not inside the state directory")
	}
	rel, err := filepath.Rel(root, dir)
	if err != nil {
		return err
	}
	p := root
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		p = filepath.Join(p, part)
		err := os.Mkdir(p, 0o700)
		if errors.Is(err, fs.ErrExist) {
			info, err := os.Lstat(p)
			if err != nil {
				return err
			}
			if isLink(p, info) || !info.IsDir() {
				return errors.New("state directory entry " + p + " is not a directory")
			}
			continue
		}
		if err != nil {
			return err
		}
		if err := privatefs.MakePrivate(p); err != nil {
			return err
		}
	}
	return nil
}

func within(dir, path string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// linkProviderState creates the links for a plugin provider's subscription account. When loginHome is not empty
// (“用本机的登录状态” ("Use local logins") is on), HOME is the user's real home directory: no link is created there and
// the extension uses its own state in the user's own directory; when a declared path does not exist there it fails
// explicitly, instead of letting Pi start and only then report that it is not logged in.
func (e *Executor) linkProviderState(snap agentconfig.Snapshot, d agentDirs, loginHome string) error {
	st := snap.ProviderState
	if !slices.Contains(snap.Plugins.IDs, st.PluginID) {
		return fail(executor.ErrCredentialUnavailable, "Model provider %s comes from plugin %s, which is not mounted for this Agent right now (not granted, not enabled in the project, disabled globally, or uninstalled).", snap.Provider, st.PluginID)
	}
	if err := pluginmanifest.CheckProviderState("providerState", map[string]pluginmanifest.ProviderState{snap.Provider: {Paths: st.Paths}}); len(st.Paths) > 0 && err != nil {
		return fail(executor.ErrCredentialUnavailable, "Plugin %s declares invalid state paths: %v", st.PluginID, err)
	}
	// On every start and reuse the platform deletes and rewrites the CLI templates' credential files in HOME (for all
	// templates, not just the ones this Agent configured), so the state paths must not overlap with them.
	managed := slices.Clone(e.tmpl.AllCredentialFilePaths())
	for _, payload := range snap.CLIPayloads {
		for _, f := range payload.Files {
			managed = append(managed, filepath.ToSlash(filepath.Clean(f.Path)))
		}
	}
	if p, m, ok := pluginmanifest.OverlappingStatePath(st.Paths, managed); ok {
		return fail(executor.ErrCredentialUnavailable, "Plugin %s declares the state path \"%s\", which overlaps the CLI configuration file \"%s\".", st.PluginID, p, m)
	}
	info, err := os.Lstat(st.Dir)
	auth := filepath.Join(st.Dir, "auth.json")
	if err == nil && info.IsDir() && info.Mode()&fs.ModeSymlink == 0 {
		info, err = os.Lstat(auth)
		if err == nil && !info.Mode().IsRegular() {
			err = errors.New("not a regular file")
		}
	} else if err == nil {
		err = errors.New("not a directory")
	}
	// A platform Agent's subscription is its own login (plugins/SPEC.md §13); tell the user where to log in again.
	account, relogin := "the project account", "Log in to this account again in the project."
	if snap.ContextProjectID == nil {
		account, relogin = "this Agent's own login", "Log in again in this Agent's model credentials."
	}
	if err != nil {
		return fail(executor.ErrCredentialUnavailable, "The subscription account's state no longer exists. %s", relogin)
	}
	if loginHome != "" {
		for _, p := range st.Paths {
			if _, err := os.Lstat(filepath.Join(loginHome, filepath.FromSlash(p))); err != nil {
				return fail(executor.ErrCredentialUnavailable, "\"Use local logins\" is on, so this computer's own login is used instead of %s, but this computer has no login for the extension (%s). Turn the switch off, or log in on this computer, and try again.", account, p)
			}
		}
	}
	var made []string
	record := func() error {
		b, _ := json.Marshal(made)
		return os.WriteFile(filepath.Join(d.root, providerLinksFile), b, 0o600)
	}
	undo := func(cause error) error {
		_ = record()
		_ = clearProviderLinks(d)
		return fail(executor.ErrCredentialUnavailable, "Could not create the links for the subscription account: %v", cause)
	}
	piAuth := filepath.Join(d.pi, "auth.json")
	if err := os.Remove(piAuth); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return undo(err)
	}
	if err := linkFile(auth, piAuth); err != nil {
		return undo(err)
	}
	made = append(made, piAuth)
	if loginHome == "" {
		for _, p := range st.Paths {
			target := filepath.Join(st.Dir, filepath.FromSlash(p))
			link := filepath.Join(d.home, filepath.FromSlash(p))
			info, err := os.Lstat(target)
			if errors.Is(err, fs.ErrNotExist) {
				// The extension had not written this path yet at login: create an empty directory in the state
				// directory, so whatever the extension writes there later follows the account.
				err = mkdirPrivate(st.Dir, target)
				if err == nil {
					info, err = os.Lstat(target)
				}
			}
			if err != nil {
				return undo(err)
			}
			if info.Mode()&fs.ModeSymlink != 0 || !(info.IsDir() || info.Mode().IsRegular()) {
				return undo(errors.New("state directory entry " + p + " is not a regular file or directory"))
			}
			if err := os.MkdirAll(filepath.Dir(link), 0o700); err != nil {
				return undo(err)
			}
			// What is in place is old content in the Agent's own HOME (a platform directory): delete it; if it is a
			// link, delete only the link itself without going into it.
			if old, err := os.Lstat(link); err == nil && isLink(link, old) {
				if err := os.Remove(link); err != nil {
					return undo(err)
				}
			} else if err := os.RemoveAll(link); err != nil {
				return undo(err)
			}
			if info.IsDir() {
				err = linkDir(target, link)
			} else {
				err = linkFile(target, link)
			}
			if err != nil {
				return undo(err)
			}
			made = append(made, link)
		}
	}
	if err := record(); err != nil {
		return undo(err)
	}
	return nil
}
