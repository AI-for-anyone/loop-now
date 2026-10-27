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
// 以及项目（平台 Agent 为全局）上下文入口 AGENTS.md 的原文（不截断）。自己的入口由 Pi 从工作目录读取，不在这里重复。
// 只在启动时读取，入口的修改要等 Pi 下次重启才生效；也不参与是否重启的判断。
func (e *Executor) contextPrompt(snap agentconfig.Snapshot, d agentDirs) (string, error) {
	if e.o.ContextDir == "" {
		return "", nil
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
	content, err := readEntry(layer)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(content) != "" {
		fmt.Fprintf(&b, "\n<context_entry path=%q>\n%s\n</context_entry>\n", entry, content)
	}
	return b.String(), nil
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
