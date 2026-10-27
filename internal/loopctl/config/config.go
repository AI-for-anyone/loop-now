// Package config 读写外部身份的本地连接配置 $HOME/.config/loopctl/config.json。
// 目录权限 0700、文件权限 0600；读取时文件权限宽于 0600 的拒绝使用；写入时先写 0600 的临时文件再改名。
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
)

// SchemaVersion 是配置文件格式的版本。
const SchemaVersion = 1

// Profile 是一组连接参数。
type Profile struct {
	Server string `json:"server,omitempty"`
	CAFile string `json:"caFile,omitempty"`
	Token  string `json:"token,omitempty"`
}

// File 是配置文件的内容。
type File struct {
	SchemaVersion int                `json:"schemaVersion"`
	Profiles      map[string]Profile `json:"profiles"`
}

// DefaultProfile 是没有指定 profile 时使用的名称。
const DefaultProfile = "default"

var profileName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// ValidProfileName 判断 profile 名称是否合法。
func ValidProfileName(name string) bool { return profileName.MatchString(name) }

// Path 返回配置文件路径。
func Path(home string) string { return filepath.Join(home, ".config", "loopctl", "config.json") }

// Names 返回按名称排序的 profile 名称。
func (f *File) Names() []string {
	out := make([]string, 0, len(f.Profiles))
	for k := range f.Profiles {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Load 读取配置文件。文件不存在时返回空配置与 exists=false。
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
	if info.Mode().Perm()&0o077 != 0 {
		return nil, true, output.Errorf(output.CodeConfigInsecurePermissions,
			"配置文件 %s 的权限为 %04o，组或其他用户可以访问；请执行 chmod 600 %s 后重试", path, info.Mode().Perm(), path)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, true, output.Errorf(output.CodeConfigInvalid, "无法读取配置文件 %s：%v", path, pathErr(err))
	}
	var out File
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	// 解析错误可能带出文件中的片段（其中有令牌），所以只报告位置无关的说明。
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

// Save 原子地写入配置文件：确保目录为 0700，在同一目录写入 0600 的临时文件，同步后改名覆盖。
func Save(path string, f *File) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".config-*.json") // CreateTemp 以 0600 创建
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

// rename 可在测试中替换，用于注入写入失败。
var rename = os.Rename

// pathErr 去掉 *fs.PathError 中重复的路径，只保留原因。
func pathErr(err error) error {
	var pe *fs.PathError
	if errors.As(err, &pe) {
		return pe.Err
	}
	return err
}
