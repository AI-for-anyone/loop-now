package local

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"loop-now/internal/server/agentconfig"
	"loop-now/internal/server/executor"
	"loop-now/internal/server/executor/local/pirpc"
)

var errSessionUnavailable = errors.New("当前没有可操作的 Pi 会话")

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
			case <-p.client.Done():
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
	res.Compaction = &executor.CompactionResult{TokensBefore: result.TokensBefore, EstimatedTokensAfter: result.EstimatedTokensAfter}
	return res
}

// StopIdle 停止没有执行的空闲 Pi；没有进程时成功返回 false。
func (e *Executor) StopIdle(ctx context.Context, agentID uuid.UUID, reason string) (bool, error) {
	free, err := e.reserveMaintenance(ctx, agentID)
	if err != nil {
		return false, err
	}
	defer free()
	e.mu.Lock()
	p := e.procs[agentID]
	if e.runs[agentID] != nil || p != nil && p.busy {
		e.mu.Unlock()
		return false, ErrRuntimeBusy
	}
	delete(e.procs, agentID)
	e.mu.Unlock()
	if p == nil {
		return false, nil
	}
	if reason == "" {
		reason = "console_command"
	}
	e.stop(p, stopCause{reason: reason})
	select {
	case <-p.client.Done():
		return true, nil
	case <-ctx.Done():
		return true, ctx.Err()
	}
}

// ExportSession 导出当前 Agent 的受管 Pi 会话。JSONL 直接读取受管文件；HTML 由 Pi 生成到平台指定的临时路径。
func (e *Executor) ExportSession(ctx context.Context, agentID uuid.UUID, format string) (executor.SessionExport, error) {
	if format != "jsonl" && format != "html" {
		return executor.SessionExport{}, fmt.Errorf("不支持的导出格式 %q", format)
	}
	free, err := e.reserveMaintenance(ctx, agentID)
	if err != nil {
		return executor.SessionExport{}, err
	}
	defer free()
	e.mu.Lock()
	p := e.procs[agentID]
	busy := e.runs[agentID] != nil || p != nil && p.busy
	e.mu.Unlock()
	if busy {
		return executor.SessionExport{}, ErrRuntimeBusy
	}

	snap, err := e.src.ExecutionSnapshot(ctx, agentID)
	if err != nil {
		return executor.SessionExport{}, err
	}
	if snap.Session.State != "active" || snap.Session.SessionFile == "" {
		return executor.SessionExport{}, errSessionUnavailable
	}
	d := e.dirs(agentID)
	sessionFile, err := managedSessionFile(d, snap.Session.SessionFile)
	if err != nil {
		return executor.SessionExport{}, err
	}
	if format == "jsonl" {
		data, err := os.ReadFile(sessionFile)
		if err != nil {
			return executor.SessionExport{}, err
		}
		return executor.SessionExport{Filename: filepath.Base(sessionFile), ContentType: "application/x-ndjson", Data: data}, nil
	}

	launched := false
	if p == nil {
		p, err = e.launch(ctx, snap, quietSink{}, uuid.Nil, "console_export")
		if err != nil {
			return executor.SessionExport{}, err
		}
		launched = true
		go e.watch(p)
	}
	if launched {
		defer e.stop(p, stopCause{reason: "console_export"})
	}
	output := filepath.Join(d.sessions, ".loop-now-export-"+uuid.Must(uuid.NewV7()).String()+".html")
	defer os.Remove(output)
	returned, err := p.client.ExportHTML(ctx, output)
	if err != nil {
		return executor.SessionExport{}, err
	}
	output, err = managedSessionFile(d, output)
	if err != nil {
		return executor.SessionExport{}, err
	}
	returned, err = managedSessionFile(d, returned)
	if err != nil || returned != output {
		return executor.SessionExport{}, errors.New("Pi 返回了不受管的导出路径")
	}
	data, err := os.ReadFile(output)
	if err != nil {
		return executor.SessionExport{}, err
	}
	name := strings.TrimSuffix(filepath.Base(sessionFile), ".jsonl") + ".html"
	return executor.SessionExport{Filename: name, ContentType: "text/html; charset=utf-8", Data: data}, nil
}

// ListSessions 流式读取受管 JSONL 的会话头和用户文字，不获取执行快照或启动 Pi。
func (e *Executor) ListSessions(ctx context.Context, agentID uuid.UUID, currentFile string) ([]executor.ManagedSession, error) {
	free, err := e.reserveMaintenance(ctx, agentID)
	if err != nil {
		return nil, err
	}
	defer free()
	e.mu.Lock()
	busy := e.runs[agentID] != nil || e.procs[agentID] != nil && e.procs[agentID].busy
	e.mu.Unlock()
	if busy {
		return nil, ErrRuntimeBusy
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
	if strings.HasPrefix(text, "本轮运行ID：") {
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

type quietSink struct{}

func (quietSink) Phase(string)                {}
func (quietSink) Running(int64)               {}
func (quietSink) TextDelta(string)            {}
func (quietSink) Tool(string, string, string) {}
