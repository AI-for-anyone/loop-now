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
func (e *Executor) contextPrompt(snap agentconfig.Snapshot, d agentDirs, loginHome string) (string, error) {
	local := localDirectoriesPrompt(snap) + loginsPrompt(loginHome) + providerLoginPrompt(snap, loginHome)
	if e.o.ContextDir == "" {
		if local == "" {
			return "", nil
		}
		return "## 文件夹与上下文\n\n" + local, nil
	}
	layer := folders.ContextFolder(e.o.ContextDir, snap.ContextProjectID)
	if err := os.MkdirAll(layer, 0o700); err != nil {
		return "", err
	}
	name, shared := "项目上下文文件夹", "本项目的 Agent（含任务内的实例）与人共同读写"
	if snap.ContextProjectID == nil {
		name, shared = "全局上下文文件夹", "平台 Agent 与人共同读写"
	}
	own := "你自己的文件夹（也是你的工作目录与你自己的上下文）"
	if snap.TaskID != uuid.Nil {
		own = "你在这个任务中的文件夹（工作目录，任务开始时复制自你的常态文件夹，任务结束后回收）"
	}
	entry := filepath.Join(layer, "AGENTS.md")
	var b strings.Builder
	b.WriteString("## 文件夹与上下文\n\n")
	fmt.Fprintf(&b, "- %s：%s，入口是根目录的 AGENTS.md（存在时已由 Pi 读入）。\n", own, d.workspace)
	fmt.Fprintf(&b, "- %s：%s，%s，入口是 %s。\n", name, layer, shared, entry)
	b.WriteString("- 两个入口只在 Pi 启动时读入，修改后要等下次重启才会出现在系统提示中。什么时候写、写到哪一层、怎样组织，见 Skill loopctl 的“上下文文件夹”一节。\n")
	b.WriteString(local)
	content, err := readEntry(layer)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(content) != "" {
		fmt.Fprintf(&b, "\n<context_entry path=%q>\n%s\n</context_entry>\n", entry, content)
	}
	return b.String(), nil
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
			fmt.Fprintf(&items, "  - %s：%s\n", dir.Path, dir.Note)
		} else {
			fmt.Fprintf(&items, "  - %s\n", dir.Path)
		}
	}
	if items.Len() == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("- 本机目录：用户登记给你直接使用的本机文件夹，不是副本。你对它们有完整的读写权限，改动直接落在用户的真实文件上，")
	if snap.TaskID != uuid.Nil {
		b.WriteString("不在任务副本里、不随任务回收，常态 Agent 和其他任务可能同时在用；")
	} else {
		b.WriteString("不会被回收；")
	}
	b.WriteString("平台没有它们的回收站，删改前确认是该动的地方，拿不准先问人。这些目录里的 AGENTS.md 等入口不会自动读入，需要时自己读。\n")
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
	return "- 模型订阅：你的模型提供方 " + snap.Provider + " 用的是用户本机主目录里该扩展自己的登录，不是项目里配置的订阅账号。\n"
}

func loginsPrompt(home string) string {
	if home == "" {
		return ""
	}
	return "- 本机登录：你的 HOME 是用户本人的主目录 " + home + "，本机工具已有的登录可以直接使用，等于以用户本人的身份操作，" +
		"改动会影响用户自己的配置与状态；Loop Now 里给你配置了账号的 CLI 仍用配置的账号。遇到需要交互授权的本机工具" +
		"（弹窗、浏览器登录、输入口令或确认）不要反复重试，告诉用户需要先在本机完成一次授权。\n"
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
