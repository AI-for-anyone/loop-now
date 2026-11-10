// Package providerstate 管理插件提供方订阅账号的状态目录（plugins/SPEC.md §13）。
//
// 一次成功登录产生一个状态版本，目录 <Root>/<版本 ID>/（0700，文件 0600），里面是扩展自己写下的东西（它的 HOME 就是这里）
// 与平台写的 auth.json（login 的返回值，Pi 认证条目）。令牌不进数据库；数据库里的版本只记下插件。
// 登录进行中的目录叫 .pending-<版本 ID>，登录成功并与数据库提交在同一把锁下改名为正式目录；清理与提交互斥。
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

// AuthFile 是状态目录里 Pi 认证条目的文件名；启动 Pi 时 Agent 的 auth.json 链接到它。
const AuthFile = "auth.json"

const pendingPrefix = ".pending-"

// ErrNotSaved 表示登录结果没有保存（操作已结束、账号已变）：状态目录随之删除。
var ErrNotSaved = errors.New("登录结果没有保存")

// Manager 管理状态目录。零值不可用，须给出 Root。
type Manager struct {
	Root string
	mu   sync.Mutex
}

// Dir 返回状态版本的目录。
func (m *Manager) Dir(version uuid.UUID) string { return filepath.Join(m.Root, version.String()) }

func (m *Manager) pending(version uuid.UUID) string {
	return filepath.Join(m.Root, pendingPrefix+version.String())
}

// Begin 为一次登录建好暂存的状态目录（0700）并返回它；登录辅助进程以它为 HOME。
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

// Discard 删除登录没有成功的暂存目录。
func (m *Manager) Discard(version uuid.UUID) { _ = os.RemoveAll(m.pending(version)) }

// Publish 把登录成功的暂存目录变成正式的状态版本：写入 auth.json（{provider: entry}），把目录收紧为 0700、文件 0600，
// 改名为正式目录，再调用 commit 写数据库；commit 失败（包括 ErrNotSaved）时删除目录。entry 是 Pi 认证条目（JSON 对象）。
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
		return fail(errors.New("登录结果不是有效的认证条目"))
	}
	data, err := json.Marshal(map[string]json.RawMessage{provider: entry})
	if err != nil {
		return fail(err)
	}
	defer clear(data)
	path := filepath.Join(staging, AuthFile)
	if info, err := os.Lstat(path); err == nil && !info.Mode().IsRegular() {
		return fail(errors.New("状态目录里的 auth.json 不是普通文件"))
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

// tighten 把目录树收紧为目录 0700、普通文件 0600；软链接与特殊文件原样不动（不跟随）。
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
