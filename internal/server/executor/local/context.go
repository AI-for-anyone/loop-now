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
	"loop-now/internal/server/folders"
)

// entryCandidates 是 Pi 在 PI_CODING_AGENT_DIR、工作目录及其每一级上级目录中查找的上下文入口文件名（按顺序取第一个存在的）。
var entryCandidates = []string{"AGENTS.override.md", "AGENTS.md", "AGENTS.MD", "CLAUDE.md", "CLAUDE.MD"}

// contextPrompt 返回在 Pi 启动时追加到系统提示的“文件夹与上下文”一节：自己的文件夹与可见的上下文文件夹的绝对路径，
// 生效的本机目录，以及项目（平台 Agent 为全局）上下文入口 AGENTS.md 的原文（不截断）。自己的入口由 Pi 从工作目录读取，不在这里重复。
// 只在启动时读取，入口的修改要等 Pi 下次重启才生效；也不参与是否重启的判断（本机目录清单的变化由 snap.Local.Identity 决定重启）。
//
// Claude Code 不读 AGENTS.md（平台也让它不读工作目录里的 CLAUDE.md），所以 Claude Code 运行时把自己的入口原文也放进这一节，
// 候选名与 Pi 相同，取第一个存在的。
func (e *Executor) contextPrompt(snap agentconfig.Snapshot, d agentDirs, loginHome string) (string, error) {
	local := localDirectoriesPrompt(snap) + loginsPrompt(loginHome) + providerLoginPrompt(snap, loginHome)
	ownEntry, ownContent := "", ""
	if isClaude(snap) {
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
	if snap.TaskID != uuid.Nil {
		own = "Your folder in this task (your working directory; copied from your regular folder when the task started and reclaimed when the task ends)"
	}
	entry := filepath.Join(layer, "AGENTS.md")
	runtimeName, loaded := "Pi", "already loaded by Pi if it exists"
	if isClaude(snap) {
		runtimeName, loaded = "Claude Code", "its content is included below if it exists"
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

// writeEntry 把一个上下文入口的原文放进这一节；内容为空时不写。
func writeEntry(b *strings.Builder, path, content string) {
	if strings.TrimSpace(content) != "" {
		fmt.Fprintf(b, "\n<context_entry path=%q>\n%s\n</context_entry>\n", path, content)
	}
}

// readOwnEntry 读取自己文件夹根目录的上下文入口：按 entryCandidates 的顺序取第一个存在的普通文件（经 os.Root，不跟随越界的软链接），
// 返回它的路径与原文；没有时都为空。
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

// localDirectoriesPrompt 返回“本机目录”一条（含末尾换行）：只列此刻存在且是目录的条目，没有时为空。
// Pi 不在沙箱里，这是告知而不是授权：如实说明 Agent 对这些目录有完整读写权限、改动直接落在真实文件上。
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

// loginsPrompt 返回“本机登录”一条（含末尾换行）：开着“用本机的登录状态”时告诉 Agent 它以用户本人的身份使用本机工具，
// 以及遇到需要交互授权的工具时不要反复重试（Pi 没有终端，弹窗或口令提示只会一直等到超时）；关着时为空。
// providerLoginPrompt 在开着“用本机的登录状态”、模型提供方来自插件且状态随账号走时说明：用的是本机该扩展自己的登录。
func providerLoginPrompt(snap agentconfig.Snapshot, home string) string {
	if home == "" || snap.ProviderState == nil || len(snap.ProviderState.Paths) == 0 {
		return ""
	}
	return "- Model subscription: your model provider " + snap.Provider + " uses the extension's own login in the user's home directory on this machine, not the subscription account configured in the project.\n"
}

func loginsPrompt(home string) string {
	if home == "" {
		return ""
	}
	return "- Local logins: your HOME is the user's own home directory " + home + ". Logins that tools on this machine already have can be used directly, " +
		"which means you act as the user, and your changes affect the user's own configuration and state; CLIs that have accounts configured for you in Loop Now still use those accounts. " +
		"When a local tool needs interactive authorization (a popup, a browser login, a password or a confirmation), do not keep retrying; tell the user to complete the authorization once on this machine first.\n"
}

// readEntry 读取上下文文件夹根目录的 AGENTS.md（经 os.Root，不跟随指向文件夹之外的软链接）；不存在或不是普通文件时为空。
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
		return "", nil // 越界的软链接等：当作没有入口
	}
	defer f.Close()
	if info, err := f.Stat(); err != nil || !info.Mode().IsRegular() {
		return "", nil
	}
	data, err := io.ReadAll(f)
	return strings.TrimPrefix(string(data), "\ufeff"), err
}

// removeStrayEntries 删除平台自有目录（Agent 根目录、PI_CODING_AGENT_DIR）中的上下文入口文件：
// Pi 会把它们读进系统提示，而这些目录不是任何一层上下文。
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

// AncestorEntryFiles 返回工作根目录的 agents 目录及其每一级上级目录中的上下文入口文件。
// Pi 会把它们读进每个 Agent 的系统提示；这些目录不归平台管理，只在启动时检查并提示。
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
