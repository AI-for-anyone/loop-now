package local

import (
	"bufio"
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
	return e.stopIdle(ctx, agentID, reason)
}

// stopIdle 在维护预留下停止 Agent 的空闲进程并等它退出（平台命令与任务分叉共用）。
func (e *Executor) stopIdle(ctx context.Context, agentID uuid.UUID, reason string) (bool, error) {
	free, err := e.reserveMaintenance(ctx, agentID)
	if err != nil {
		return false, maintenanceErr(err)
	}
	defer free()
	e.mu.Lock()
	p := e.procs[agentID]
	if e.runs[agentID] != nil || p != nil && p.busy {
		e.mu.Unlock()
		return false, fmt.Errorf("%w：常态轮次尚未完全结束", executor.ErrMaintenanceBusy)
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

// ListSessions 列出 sessions 目录里的有效 Pi 会话（头一行是 {"type":"session","id":…}），带第一条与最后一条普通用户文字。
// 只读文件，不获取执行快照、不启动 Pi，也不需要 Agent 空闲。标题只向前读、预览只从末尾往回读，各读一段（sessionTextWindow），
// 按文件的大小与修改时间缓存，长会话不会每次整份解码。ID 重复的会话都不列出（无法确定恢复哪一个）。
func (e *Executor) ListSessions(ctx context.Context, agentID uuid.UUID, currentFile string) ([]executor.ManagedSession, error) {
	out, err := e.scanSessions(ctx, agentID, currentFile, true)
	if err != nil {
		return nil, err
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

// ResolveSession 按完整会话 ID 找到 sessions 目录里的会话，只读各文件的头一行；找不到或 ID 重复时返回 nil。
func (e *Executor) ResolveSession(ctx context.Context, agentID uuid.UUID, sessionID, currentFile string) (*executor.ManagedSession, error) {
	all, err := e.scanSessions(ctx, agentID, currentFile, false)
	if err != nil {
		return nil, err
	}
	for i := range all {
		if all[i].ID == sessionID {
			return &all[i], nil
		}
	}
	return nil, nil
}

// sessionMeta 是一个会话文件的 ID 与用户文字摘要；size 与 mod 变了就重新读。
type sessionMeta struct {
	size           int64
	mod            time.Time
	id             string
	withText       bool
	title, preview string
}

// sessionTextWindow 是找标题（向前）与预览（从末尾往回）时各读的最多字节数。
const sessionTextWindow = 1 << 20

// scanSessions 读 sessions 目录里每个 .jsonl 的头一行（withText 时再读标题与预览），去掉无效与 ID 重复的会话。
func (e *Executor) scanSessions(ctx context.Context, agentID uuid.UUID, currentFile string, withText bool) ([]executor.ManagedSession, error) {
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
	seen := make(map[string]bool)
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
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		meta, ok := e.sessionMeta(path, info, withText)
		if !ok {
			continue
		}
		if seen[meta.id] {
			duplicates[meta.id] = true
		}
		seen[meta.id] = true
		out = append(out, executor.ManagedSession{ID: meta.id, Path: path, Title: meta.title, Preview: meta.preview, UpdatedAt: info.ModTime(), Current: path == current})
	}
	return slices.DeleteFunc(out, func(m executor.ManagedSession) bool { return duplicates[m.ID] }), nil
}

// sessionMeta 返回会话文件的 ID 与（withText 时）标题与预览，文件没变时用缓存；不是有效会话时 ok 为 false。
func (e *Executor) sessionMeta(path string, info os.FileInfo, withText bool) (sessionMeta, bool) {
	if v, ok := e.sessionCache.Load(path); ok {
		m := v.(sessionMeta)
		if m.size == info.Size() && m.mod.Equal(info.ModTime()) && (m.withText || !withText) {
			return m, m.id != ""
		}
	}
	m := sessionMeta{size: info.Size(), mod: info.ModTime(), withText: withText}
	file, err := os.Open(path)
	if err != nil {
		return m, false
	}
	defer file.Close()
	reader := bufio.NewReaderSize(io.LimitReader(file, sessionTextWindow), 64<<10)
	head, err := reader.ReadBytes('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return m, false
	}
	var header struct {
		Type string `json:"type"`
		ID   string `json:"id"`
	}
	if json.Unmarshal(head, &header) != nil || header.Type != "session" || header.ID == "" {
		e.sessionCache.Store(path, m) // 无效的也缓存，文件没变就不再读
		return m, false
	}
	m.id = header.ID
	if withText {
		m.title = firstUserText(reader)
		m.preview = lastUserText(file, info.Size())
	}
	e.sessionCache.Store(path, m)
	return m, true
}

// firstUserText 顺序读记录，返回第一条普通用户文字（截到 80 字）；读完窗口或遇到损坏的行就停。
func firstUserText(r *bufio.Reader) string {
	for {
		line, err := r.ReadBytes('\n')
		if text := sessionLineUserText(line); text != "" {
			return truncateSessionText(text, 80)
		}
		if err != nil {
			return ""
		}
	}
}

// lastUserText 从文件末尾往回读一段，返回其中最后一条普通用户文字（截到 160 字）。窗口开头的半行丢掉。
func lastUserText(f *os.File, size int64) string {
	start := max(0, size-sessionTextWindow)
	buf := make([]byte, size-start)
	if _, err := f.ReadAt(buf, start); err != nil && !errors.Is(err, io.EOF) {
		return ""
	}
	lines := bytes.Split(buf, []byte{'\n'})
	if start > 0 && len(lines) > 0 {
		lines = lines[1:]
	}
	for i := len(lines) - 1; i >= 0; i-- {
		if text := sessionLineUserText(lines[i]); text != "" {
			return truncateSessionText(text, 160)
		}
	}
	return ""
}

// sessionLineUserText 返回一行记录里的普通用户文字（不是用户消息、以 "/" 开头的命令或无法解析时为空）。
func sessionLineUserText(line []byte) string {
	if !bytes.Contains(line, []byte(`"user"`)) {
		return "" // 大多数行是助手消息与工具结果，不必解码
	}
	var record struct {
		Type    string `json:"type"`
		Message struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"message"`
	}
	if json.Unmarshal(line, &record) != nil || record.Type != "message" || record.Message.Role != "user" {
		return ""
	}
	text := sessionMessageText(record.Message.Content)
	if strings.HasPrefix(text, "/") {
		return ""
	}
	return text
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
