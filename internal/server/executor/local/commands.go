package local

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"loop-now/internal/execenv"
	"loop-now/internal/procgroup"
	"loop-now/internal/server/agentconfig"
	"loop-now/internal/server/executor"
	"loop-now/internal/server/executor/local/pirpc"
	"loop-now/internal/server/preflight"
)

// maintenanceErr 把维护操作里可以预期的失败标上 executor 的哨兵错误（原错误仍在链上）。
func maintenanceErr(err error) error {
	if errors.Is(err, ErrRuntimeBusy) {
		return fmt.Errorf("%w：%w", executor.ErrMaintenanceBusy, err)
	}
	return err
}

// compact 在当前真实运行里执行手动压缩，复用中断、事件、用量和会话记录链。
func (e *Executor) compact(ctx context.Context, p *proc, r *run, req executor.Request, snap agentconfig.Snapshot) executor.Result {
	runID := req.RunID
	result, err := p.client.Compact(ctx, req.Message)
	if errors.Is(err, pirpc.ErrExited) {
		e.discard(p, &runID)
	}
	sessionErr := e.turnFinished(p)
	res := executor.Result{ConfigVersion: snap.ConfigVersion}
	if r.interrupted.Load() {
		res.Outcome = executor.OutcomeInterrupted
		return res
	}
	if err != nil {
		res.Outcome = executor.OutcomeFailed
		res.ErrorCode, res.ErrorMessage = executor.ErrPiError, err.Error()
		if errors.Is(err, pirpc.ErrExited) {
			res.ErrorCode = executor.ErrPiExited
			select {
			case <-p.rt.Done():
			case <-time.After(e.o.StopGrace):
			}
			res.ErrorMessage = strings.TrimSpace("Pi 进程异常退出 " + tail(p.client.Stderr(), e.o.StderrTailBytes))
		}
		return res
	}
	if sessionErr != nil {
		return failed(executor.ErrInternal, "会话记录保存失败", snap.ConfigVersion)
	}
	res.Outcome = executor.OutcomeCompleted
	res.Compaction = &executor.CompactionResult{TokensBefore: result.TokensBefore, EstimatedTokensAfter: result.EstimatedTokensAfter, Skipped: result.Skipped}
	return res
}

// StopIdle 停止没有执行的空闲运行时；没有进程时成功返回 false。reason 是平台命令的停止原因（idleStopReasons）。
func (e *Executor) StopIdle(ctx context.Context, agentID uuid.UUID, reason string) (bool, error) {
	if !slices.Contains(idleStopReasons, reason) {
		return false, fmt.Errorf("未知的停止原因 %q", reason)
	}
	free, err := e.reserveMaintenance(ctx, agentID)
	if err != nil {
		return false, maintenanceErr(err)
	}
	defer free()
	e.mu.Lock()
	p := e.procs[agentID]
	if e.runs[agentID] != nil || p != nil && p.busy {
		e.mu.Unlock()
		return false, maintenanceErr(ErrRuntimeBusy)
	}
	delete(e.procs, agentID)
	e.mu.Unlock()
	if p == nil {
		return false, nil
	}
	e.stop(p, stopCause{reason: reason})
	select {
	case <-p.rt.Done():
		return true, nil
	case <-ctx.Done():
		return true, ctx.Err()
	}
}

// ExportSession 导出 Agent 的受管 Pi 会话。sessionFile 是数据库记下的当前会话文件，只接受 sessions 目录里的文件。
// JSONL 直接读会话文件；HTML 用 `pi --export <会话文件> <输出>` 生成（exportHTML）。两者都不需要执行快照与运行准备，
// 不启动运行时，也不碰存活的空闲进程。
func (e *Executor) ExportSession(ctx context.Context, agentID uuid.UUID, sessionFile, format string) (executor.SessionExport, error) {
	if format != "jsonl" && format != "html" {
		return executor.SessionExport{}, fmt.Errorf("不支持的导出格式 %q", format)
	}
	free, err := e.reserveMaintenance(ctx, agentID)
	if err != nil {
		return executor.SessionExport{}, maintenanceErr(err)
	}
	defer free()
	e.mu.Lock()
	p := e.procs[agentID]
	busy := e.runs[agentID] != nil || p != nil && p.busy
	e.mu.Unlock()
	if busy {
		return executor.SessionExport{}, maintenanceErr(ErrRuntimeBusy)
	}
	if sessionFile == "" {
		return executor.SessionExport{}, executor.ErrNoSession
	}
	d := e.dirs(agentID)
	file, err := managedSessionFile(d, sessionFile)
	if errors.Is(err, fs.ErrNotExist) {
		return executor.SessionExport{}, fmt.Errorf("%w：会话文件不存在", executor.ErrNoSession)
	}
	if err != nil {
		return executor.SessionExport{}, err
	}
	if format == "jsonl" {
		data, err := os.ReadFile(file)
		if err != nil {
			return executor.SessionExport{}, err
		}
		return executor.SessionExport{Filename: filepath.Base(file), ContentType: "application/x-ndjson", Data: data}, nil
	}
	data, err := e.exportHTML(ctx, d, file)
	if err != nil {
		return executor.SessionExport{}, err
	}
	name := strings.TrimSuffix(filepath.Base(file), ".jsonl") + ".html"
	return executor.SessionExport{Filename: name, ContentType: "text/html; charset=utf-8", Data: data}, nil
}

// exportTimeout 是 `pi --export` 的最长用时。
const exportTimeout = time.Minute

// exportHTML 运行 `pi --offline --export <会话文件> <输出>` 生成 HTML：Pi 只读会话文件、写出页面就退出，不调用模型。
// 它用 Agent 自己的 HOME、临时目录与 Pi 目录（与运行时相同），只带基础环境，不带凭证、插件与 CLI 的环境变量。
// 与 RPC 的 export_html 不同，页面里没有当前进程的系统提示词与工具定义（Pi 的 --export 本来如此）。
func (e *Executor) exportHTML(ctx context.Context, d agentDirs, sessionFile string) ([]byte, error) {
	if err := os.MkdirAll(d.tmp, 0o700); err != nil {
		return nil, err
	}
	output := filepath.Join(d.tmp, "loop-now-export-"+uuid.Must(uuid.NewV7()).String()+".html")
	defer os.Remove(output)
	ctx, cancel := context.WithTimeout(ctx, exportTimeout)
	defer cancel()
	cmd := preflight.PiCommand(e.o.NodeBin, e.o.PiBin, "--offline", "--export", sessionFile, output)
	cmd.Env = e.exportEnv(d)
	cmd.Dir = d.tmp
	var stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = io.Discard, &limitedBuffer{b: &stderr, max: 4 << 10}
	procgroup.Configure(cmd)
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("启动 pi --export 失败：%w", err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			return nil, fmt.Errorf("pi --export 失败：%v %s", err, strings.TrimSpace(stderr.String()))
		}
	case <-ctx.Done():
		_ = procgroup.Kill(cmd.Process.Pid)
		<-done
		return nil, fmt.Errorf("pi --export 没有完成：%w", ctx.Err())
	}
	return os.ReadFile(output)
}

// exportEnv 是 `pi --export` 的环境：透传变量、基础环境与 Agent 的目录。
func (e *Executor) exportEnv(d agentDirs) []string {
	vars := map[string]string{}
	for _, name := range e.o.Passthrough {
		if v := e.o.Getenv(name); v != "" && !executor.IsReservedEnv(name) && !e.modelSecretEnv(name) {
			vars[name] = v
		}
	}
	for _, kv := range execenv.Base(d.home, d.tmp) {
		if k, v, ok := strings.Cut(kv, "="); ok {
			vars[k] = v
		}
	}
	for k, v := range map[string]string{"HOME": d.home, "TMPDIR": d.tmp, "PI_CODING_AGENT_DIR": d.pi, "PI_OFFLINE": "1",
		"USER": e.o.Username, "LOGNAME": e.o.Username, "PATH": e.o.ExecBasePath} {
		vars[k] = v
	}
	out := make([]string, 0, len(vars))
	for _, k := range slices.Sorted(maps.Keys(vars)) {
		out = append(out, k+"="+vars[k])
	}
	return out
}

// limitedBuffer 只留前 max 个字节。
type limitedBuffer struct {
	b   *bytes.Buffer
	max int
}

func (l *limitedBuffer) Write(p []byte) (int, error) {
	if room := l.max - l.b.Len(); room > 0 {
		l.b.Write(p[:min(len(p), room)])
	}
	return len(p), nil
}

// ListSessions 流式读取受管 JSONL 的会话头和用户文字，不获取执行快照或启动 Pi。
func (e *Executor) ListSessions(ctx context.Context, agentID uuid.UUID, currentFile string) ([]executor.ManagedSession, error) {
	free, err := e.reserveMaintenance(ctx, agentID)
	if err != nil {
		return nil, maintenanceErr(err)
	}
	defer free()
	e.mu.Lock()
	busy := e.runs[agentID] != nil || e.procs[agentID] != nil && e.procs[agentID].busy
	e.mu.Unlock()
	if busy {
		return nil, maintenanceErr(ErrRuntimeBusy)
	}
	d := e.dirs(agentID)
	entries, err := os.ReadDir(d.sessions)
	if errors.Is(err, os.ErrNotExist) {
		return []executor.ManagedSession{}, nil
	}
	if err != nil {
		return nil, err
	}
	current := ""
	if currentFile != "" {
		current, _ = managedSessionFile(d, currentFile)
	}
	out := make([]executor.ManagedSession, 0, len(entries))
	seen := make(map[string]int)
	duplicates := make(map[string]bool)
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if filepath.Ext(entry.Name()) != ".jsonl" {
			continue
		}
		path, err := managedSessionFile(d, filepath.Join(d.sessions, entry.Name()))
		if err != nil {
			continue
		}
		file, err := os.Open(path)
		if err != nil {
			continue
		}
		var header struct {
			Type string `json:"type"`
			ID   string `json:"id"`
		}
		decoder := json.NewDecoder(file)
		err = decoder.Decode(&header)
		title, preview := "", ""
		if err == nil && header.Type == "session" && header.ID != "" {
			title, preview = readSessionUserText(decoder)
		}
		_ = file.Close()
		if err != nil || header.Type != "session" || header.ID == "" {
			continue
		}
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		if _, exists := seen[header.ID]; exists {
			duplicates[header.ID] = true
		}
		seen[header.ID] = len(out)
		out = append(out, executor.ManagedSession{ID: header.ID, Path: path, Title: title, Preview: preview, UpdatedAt: info.ModTime(), Current: path == current})
	}
	if len(duplicates) > 0 {
		valid := out[:0]
		for _, sess := range out {
			if !duplicates[sess.ID] {
				valid = append(valid, sess)
			}
		}
		out = valid
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].UpdatedAt.Equal(out[j].UpdatedAt) {
			return out[i].UpdatedAt.After(out[j].UpdatedAt)
		}
		if out[i].ID != out[j].ID {
			return out[i].ID < out[j].ID
		}
		return out[i].Path < out[j].Path
	})
	return out, nil
}

// readSessionUserText 保留第一条和最后一条普通用户文字；尾部损坏不丢弃已读到的元数据。
func readSessionUserText(decoder *json.Decoder) (title, preview string) {
	for {
		var record struct {
			Type    string `json:"type"`
			Message struct {
				Role    string          `json:"role"`
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if err := decoder.Decode(&record); err != nil {
			return title, preview
		}
		if record.Type != "message" || record.Message.Role != "user" {
			continue
		}
		text := sessionMessageText(record.Message.Content)
		if text == "" || strings.HasPrefix(text, "/") {
			continue
		}
		if title == "" {
			title = truncateSessionText(text, 80)
		}
		preview = truncateSessionText(text, 160)
	}
}

func sessionMessageText(content json.RawMessage) string {
	var plain string
	if err := json.Unmarshal(content, &plain); err == nil {
		return normalizeSessionUserText(plain)
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(content, &parts); err != nil {
		return ""
	}
	texts := make([]string, 0, len(parts))
	for _, part := range parts {
		if part.Type == "text" && part.Text != "" {
			texts = append(texts, part.Text)
		}
	}
	return normalizeSessionUserText(strings.Join(texts, " "))
}

func normalizeSessionUserText(text string) string {
	text = strings.TrimSpace(text)
	if strings.HasPrefix(text, "本轮运行ID：") || strings.HasPrefix(text, "Run ID for this turn: ") {
		newline := strings.IndexByte(text, '\n')
		if newline < 0 {
			return ""
		}
		text = text[newline+1:]
	}
	return strings.Join(strings.Fields(text), " ")
}

func truncateSessionText(text string, limit int) string {
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:limit]) + "…"
}

// managedSessionFile 只接受 sessions 目录内已存在的直接子文件，并解析软链接后再次验证。
func managedSessionFile(d agentDirs, path string) (string, error) {
	if path == "" || !filepath.IsAbs(path) {
		return "", errors.New("会话路径不受平台管理")
	}
	dir, err := filepath.EvalSymlinks(d.sessions)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	if filepath.Dir(resolved) != filepath.Clean(dir) {
		return "", errors.New("会话路径不在平台管理的 sessions 目录内")
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.Mode().IsRegular() {
		return "", errors.New("会话路径不是普通文件")
	}
	return resolved, nil
}

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
