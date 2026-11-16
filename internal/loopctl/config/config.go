// Package config reads and writes the external identity's local connection config,
// $HOME/.config/loopctl/config.json.
// The directory has mode 0700 and the file 0600. On read, a file with permissions wider than 0600 is refused; on
// write, a 0600 temporary file is written first and then renamed.
package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"

	"loop-now/internal/loopctl/output"
	"loop-now/internal/privatefs"
)

// SchemaVersion is the version of the config file format.
const SchemaVersion = 1

// Profile is a set of connection settings.
type Profile struct {
	Server string `json:"server,omitempty"`
	CAFile string `json:"caFile,omitempty"`
	Token  string `json:"token,omitempty"`
}

// File is the content of the config file.
type File struct {
	SchemaVersion int                `json:"schemaVersion"`
	Profiles      map[string]Profile `json:"profiles"`
}

// DefaultProfile is the name used when no profile is given.
const DefaultProfile = "default"

var profileName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// ValidProfileName reports whether a profile name is valid.
func ValidProfileName(name string) bool { return profileName.MatchString(name) }

// Path returns the path of the config file.
func Path(home string) string { return filepath.Join(home, ".config", "loopctl", "config.json") }

// Names returns the profile names, sorted.
func (f *File) Names() []string {
	out := make([]string, 0, len(f.Profiles))
	for k := range f.Profiles {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Load reads the config file. When the file does not exist, it returns an empty config and exists=false.
func Load(path string) (f *File, exists bool, e *output.Error) {
	empty := &File{SchemaVersion: SchemaVersion, Profiles: map[string]Profile{}}
	info, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return empty, false, nil
	}
	if err != nil {
		return nil, false, output.Errorf(output.CodeConfigInvalid, "无法读取配置文件 %s：%v", path, pathErr(err))
	}
	if !info.Mode().IsRegular() {
		return nil, true, output.Errorf(output.CodeConfigInvalid, "配置文件 %s 不是普通文件", path)
	}
	if !privatefs.IsPrivate(path, info) {
		return nil, true, output.Errorf(output.CodeConfigInsecurePermissions,
			"配置文件 %s 的权限为 %04o，组或其他用户可以访问；请把它改为只有当前用户可访问（Unix 上 chmod 600 %s）后重试", path, info.Mode().Perm(), path)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, true, output.Errorf(output.CodeConfigInvalid, "无法读取配置文件 %s：%v", path, pathErr(err))
	}
	var out File
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	// A parse error could quote fragments of the file (which contains tokens), so only report a message that does
	// not depend on the position.
	if err := dec.Decode(&out); err != nil || dec.More() {
		return nil, true, output.Errorf(output.CodeConfigInvalid, "配置文件 %s 不是合法的 JSON，或含有未知字段", path)
	}
	if out.SchemaVersion != SchemaVersion {
		return nil, true, output.Errorf(output.CodeConfigInvalid, "配置文件 %s 的 schemaVersion 为 %d，本版本只支持 %d", path, out.SchemaVersion, SchemaVersion)
	}
	if out.Profiles == nil {
		out.Profiles = map[string]Profile{}
	}
	for name := range out.Profiles {
		if !ValidProfileName(name) {
			return nil, true, output.Errorf(output.CodeConfigInvalid, "配置文件 %s 中的 profile 名称 %q 不合法", path, name)
		}
	}
	return &out, true, nil
}

// Save writes the config file atomically: it makes sure the directory is 0700, writes a 0600 temporary file in the
// same directory, syncs it and renames it over the target.
func Save(path string, f *File) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := privatefs.MakePrivate(dir); err != nil {
		return err
	}
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".config-*.json") // CreateTemp creates it with 0600
	if err != nil {
		return err
	}
	name := tmp.Name()
	ok := false
	defer func() {
		if !ok {
			tmp.Close()
			os.Remove(name)
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		return err
	}
	if _, err := tmp.Write(append(b, '\n')); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := rename(name, path); err != nil {
		return err
	}
	ok = true
	if d, err := os.Open(dir); err == nil {
		d.Sync()
		d.Close()
	}
	return nil
}

// rename can be replaced in tests to inject write failures.
var rename = os.Rename

// pathErr strips the repeated path from a *fs.PathError and keeps only the cause.
func pathErr(err error) error {
	var pe *fs.PathError
	if errors.As(err, &pe) {
		return pe.Err
	}
	return err
}
