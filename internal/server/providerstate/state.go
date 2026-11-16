// Package providerstate manages the state directories of plugin provider subscription accounts
// (plugins/SPEC.md §13).
//
// Each successful login produces one state version, the directory <Root>/<version ID>/ (0700, files 0600). It holds
// whatever the extension itself wrote (this is its HOME) and the auth.json the platform writes (the return value of
// login, a Pi auth entry). Tokens never go into the database; the version in the database only records the plugin.
// While a login is in progress its directory is called .pending-<version ID>; once the login succeeds it is renamed to
// the final directory under the same lock as the database commit. Sweeping and committing are mutually exclusive.
package providerstate

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/google/uuid"

	"loop-now/internal/privatefs"
)

// AuthFile is the file name of the Pi auth entry in a state directory; when Pi starts, the Agent's auth.json is
// linked to it.
const AuthFile = "auth.json"

const pendingPrefix = ".pending-"

// ErrNotSaved means the login result was not saved (the operation has ended, or the account has changed); the state
// directory is deleted along with it.
var ErrNotSaved = errors.New("the login result was not saved")

// Manager manages state directories. The zero value is not usable; Root must be set.
type Manager struct {
	Root string
	mu   sync.Mutex
}

// Dir returns the directory of a state version.
func (m *Manager) Dir(version uuid.UUID) string { return filepath.Join(m.Root, version.String()) }

func (m *Manager) pending(version uuid.UUID) string {
	return filepath.Join(m.Root, pendingPrefix+version.String())
}

// Begin creates the staging state directory (0700) for one login and returns it; the login helper process uses it
// as HOME.
func (m *Manager) Begin(version uuid.UUID) (string, error) {
	if err := os.MkdirAll(m.Root, 0o700); err != nil {
		return "", err
	}
	if err := privatefs.MakePrivate(m.Root); err != nil {
		return "", err
	}
	dir := m.pending(version)
	if err := os.Mkdir(dir, 0o700); err != nil {
		return "", err
	}
	return dir, privatefs.MakePrivate(dir)
}

// Discard deletes the staging directory of a login that did not succeed.
func (m *Manager) Discard(version uuid.UUID) { _ = os.RemoveAll(m.pending(version)) }

// Publish turns the staging directory of a successful login into a final state version: it writes auth.json
// ({provider: entry}), tightens directories to 0700 and files to 0600, renames it to the final directory, and then
// calls commit to write the database; when commit fails (including with ErrNotSaved) the directory is deleted. entry
// is a Pi auth entry (a JSON object).
func (m *Manager) Publish(version uuid.UUID, provider string, entry []byte, commit func() error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	staging, final := m.pending(version), m.Dir(version)
	fail := func(err error) error {
		_ = os.RemoveAll(staging)
		return err
	}
	var obj map[string]json.RawMessage
	if json.Unmarshal(entry, &obj) != nil || len(obj) == 0 || provider == "" || strings.ContainsAny(provider, "/\\\x00") {
		return fail(errors.New("the login result is not a valid auth entry"))
	}
	data, err := json.Marshal(map[string]json.RawMessage{provider: entry})
	if err != nil {
		return fail(err)
	}
	defer clear(data)
	path := filepath.Join(staging, AuthFile)
	if info, err := os.Lstat(path); err == nil && !info.Mode().IsRegular() {
		return fail(errors.New("auth.json in the state directory is not a regular file"))
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fail(err)
	}
	if err := tighten(staging); err != nil {
		return fail(err)
	}
	if err := os.Rename(staging, final); err != nil {
		return fail(err)
	}
	if err := commit(); err != nil {
		_ = os.RemoveAll(final)
		return err
	}
	return nil
}

// tighten tightens a directory tree to 0700 for directories and 0600 for regular files; symlinks and special files
// are left untouched (not followed).
func tighten(root string) error {
	return filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type()&fs.ModeSymlink != 0 || !(d.IsDir() || d.Type().IsRegular()) {
			return nil
		}
		return privatefs.MakePrivate(p)
	})
}

// Sweep deletes state directories that are no longer needed: keep returns, inside the lock, the versions to keep
// (mutually exclusive with Publish, so a version that was just committed is never deleted by mistake). When pending
// is true it also deletes login staging directories (only at startup, when no login is in progress). It returns the
// deleted versions.
func (m *Manager) Sweep(keep func() (map[uuid.UUID]bool, error), pending bool) ([]uuid.UUID, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	entries, err := os.ReadDir(m.Root)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	keepSet, err := keep()
	if err != nil {
		return nil, err
	}
	var removed []uuid.UUID
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, pendingPrefix) {
			if pending {
				_ = os.RemoveAll(filepath.Join(m.Root, name))
			}
			continue
		}
		id, err := uuid.Parse(name)
		if err != nil || id.String() != name || keepSet[id] {
			continue
		}
		if err := os.RemoveAll(filepath.Join(m.Root, name)); err != nil {
			return removed, err
		}
		removed = append(removed, id)
	}
	return removed, nil
}
