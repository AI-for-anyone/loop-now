package local

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"

	"loop-now/internal/server/executor"
)

// commandsFile 是 Agent 目录里上次启动 Pi 时读到的命令列表（Pi RPC get_commands），供对话页的 "/" 菜单用；
// 平台不会为了列命令专门拉起 Pi。
const commandsFile = "pi-commands.json"

// cacheCommands 读出刚启动的 Pi 的命令并写进 Agent 目录；失败只记日志，不影响执行。
func (e *Executor) cacheCommands(p *proc) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	data, err := p.client.Call(ctx, "get_commands", nil)
	if err != nil {
		p.log.Warn("读取 Pi 命令列表失败", "err", err)
		return
	}
	var raw struct {
		Commands []struct {
			Name        string  `json:"name"`
			Description *string `json:"description"`
			Source      string  `json:"source"`
			SourceInfo  struct {
				Path string `json:"path"`
			} `json:"sourceInfo"`
		} `json:"commands"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		p.log.Warn("解析 Pi 命令列表失败", "err", err)
		return
	}
	out := executor.PiCommands{CapturedAt: time.Now().UTC(), Commands: []executor.PiCommand{}}
	for _, c := range raw.Commands {
		if c.Name == "" || len(c.Name) > 200 {
			continue
		}
		if c.Description != nil && len(*c.Description) > 1000 {
			d := (*c.Description)[:1000]
			c.Description = &d
		}
		out.Commands = append(out.Commands, executor.PiCommand{Name: c.Name, Description: c.Description, Source: c.Source, Path: c.SourceInfo.Path})
	}
	b, err := json.Marshal(out)
	if err != nil {
		return
	}
	path := filepath.Join(p.dirs.root, commandsFile)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		p.log.Warn("保存 Pi 命令列表失败", "err", err)
		return
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		p.log.Warn("保存 Pi 命令列表失败", "err", err)
	}
}

// Commands 返回 Agent 上次启动 Pi 时读到的命令；从没启动过（或还没读到）时为 nil。
func (e *Executor) Commands(agentID uuid.UUID) (*executor.PiCommands, error) {
	b, err := os.ReadFile(filepath.Join(e.dirs(agentID).root, commandsFile))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out executor.PiCommands
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, nil // 写到一半或旧格式：当作没有
	}
	return &out, nil
}
