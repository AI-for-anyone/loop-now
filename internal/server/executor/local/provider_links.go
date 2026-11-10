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
	"loop-now/internal/server/agentconfig"
	"loop-now/internal/server/executor"
)

// 插件提供方订阅账号的状态（plugins/SPEC.md §13）：启动 Pi 前，Agent 的 Pi 目录里的 auth.json 链接到账号状态目录里的
// auth.json，声明随账号走的 HOME 路径链接到状态目录里的同名路径；同项目多个 Agent 共用一份，由扩展自己刷新。
// 本次建的链接记在 Agent 目录里，下次启动、释放与清理前先删掉，所以换账号、换模型、解绑都收得干净，
// 删除 Agent 目录时也不会经链接动到状态目录。链接只建在平台自己的 Agent 目录里，从不建在用户真实的家目录里。

// providerLinksFile 记下上次建的链接（绝对路径）。
const providerLinksFile = "provider-links.json"

// clearProviderLinks 删除上次建的链接：只删 Agent HOME 与 Pi 目录里的链接本身（Windows 上的 auth.json 是硬链接，也删），
// 不跟随、不碰链接指向的状态目录。
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

func within(dir, path string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// linkProviderState 为插件提供方的订阅账号建链接。loginHome 不为空（开着“用本机的登录状态”）时 HOME 是用户真实的家目录：
// 不在那里建任何链接，扩展用用户本人目录下自己的状态；声明的路径在那里不存在时明确失败，不让 Pi 起来后才报未登录。
func (e *Executor) linkProviderState(snap agentconfig.Snapshot, d agentDirs, loginHome string) error {
	st := snap.ProviderState
	if !slices.Contains(snap.Plugins.IDs, st.PluginID) {
		return fail(executor.ErrCredentialUnavailable, "模型提供方 %s 来自插件 %s，这个 Agent 现在没有挂载它（未授权、项目未启用、全局停用或已卸载）", snap.Provider, st.PluginID)
	}
	if err := pluginmanifest.CheckProviderState("providerState", map[string]pluginmanifest.ProviderState{snap.Provider: {Paths: st.Paths}}); len(st.Paths) > 0 && err != nil {
		return fail(executor.ErrCredentialUnavailable, "插件 %s 声明的状态路径不合规：%v", st.PluginID, err)
	}
	for _, p := range st.Paths {
		for _, payload := range snap.CLIPayloads {
			for _, f := range payload.Files {
				managed := filepath.ToSlash(filepath.Clean(f.Path))
				if managed == p || strings.HasPrefix(managed, p+"/") || strings.HasPrefix(p, managed+"/") {
					return fail(executor.ErrCredentialUnavailable, "插件 %s 声明的状态路径 %s 与 CLI 的配置文件 %s 重叠", st.PluginID, p, managed)
				}
			}
		}
	}
	info, err := os.Lstat(st.Dir)
	auth := filepath.Join(st.Dir, "auth.json")
	if err == nil && info.IsDir() && info.Mode()&fs.ModeSymlink == 0 {
		info, err = os.Lstat(auth)
		if err == nil && !info.Mode().IsRegular() {
			err = errors.New("不是普通文件")
		}
	} else if err == nil {
		err = errors.New("不是目录")
	}
	if err != nil {
		return fail(executor.ErrCredentialUnavailable, "订阅账号的状态已不存在，请在项目里重新登录该账号")
	}
	if loginHome != "" {
		for _, p := range st.Paths {
			if _, err := os.Lstat(filepath.Join(loginHome, filepath.FromSlash(p))); err != nil {
				return fail(executor.ErrCredentialUnavailable, "已开启“用本机的登录状态”，此时用的是本机自己的登录而不是项目账号；本机没有该扩展的登录（%s），关闭开关或在本机登录后再试", p)
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
		return fail(executor.ErrCredentialUnavailable, "为订阅账号建立链接失败：%v", cause)
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
				// 登录时扩展还没写这条路径：在状态目录里建一个空目录，扩展之后写进去的东西都随账号走。
				err = os.MkdirAll(target, 0o700)
				if err == nil {
					info, err = os.Lstat(target)
				}
			}
			if err != nil {
				return undo(err)
			}
			if info.Mode()&fs.ModeSymlink != 0 || !(info.IsDir() || info.Mode().IsRegular()) {
				return undo(errors.New("状态目录里的 " + p + " 不是普通文件或目录"))
			}
			if err := os.MkdirAll(filepath.Dir(link), 0o700); err != nil {
				return undo(err)
			}
			// 原处是 Agent 自己 HOME 里的旧东西（平台目录）：删掉；是链接时只删链接本身，不进到链接里面。
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
