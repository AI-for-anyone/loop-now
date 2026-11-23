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
	"loop-now/internal/server/executor"
	"loop-now/internal/server/preflight"
)

// exportSession exports the Agent's managed Pi session. Only files in the sessions directory are accepted. JSONL reads
// the session file directly; HTML is generated with `pi --export <session file> <output>` (exportHTML).
func (pd piDriver) exportSession(ctx context.Context, agentID uuid.UUID, sessionFile, format string) (executor.SessionExport, error) {
	e := pd.e
	if sessionFile == "" {
		return executor.SessionExport{}, executor.ErrNoSession
	}
	d := e.dirs(agentID)
	file, err := managedSessionFile(d, sessionFile)
	if errors.Is(err, fs.ErrNotExist) {
		return executor.SessionExport{}, fmt.Errorf("%w: the session file does not exist", executor.ErrNoSession)
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

// exportTimeout is the longest `pi --export` may take.
const exportTimeout = time.Minute

// exportHTML runs `pi --offline --export <session file> <output>` to generate HTML: Pi only reads the session file,
// writes the page and exits, without calling a model. It uses the Agent's own HOME, temp directory and Pi directory
// (the same as the runtime), with only the base environment and no credential, plugin or CLI environment variables.
// Unlike RPC's export_html, the page does not contain the current process's system prompt and tool definitions (that is
// how Pi's --export works).
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
		return nil, fmt.Errorf("start pi --export: %w", err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			return nil, fmt.Errorf("pi --export failed: %v %s", err, strings.TrimSpace(stderr.String()))
		}
	case <-ctx.Done():
		_ = procgroup.Kill(cmd.Process.Pid)
		<-done
		return nil, fmt.Errorf("pi --export did not finish: %w", ctx.Err())
	}
	return os.ReadFile(output)
}

// exportEnv is the environment of `pi --export`: passed-through variables, the base environment and the Agent's
// directories.
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

// limitedBuffer keeps only the first max bytes.
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

// listSessions lists the valid Pi sessions in the sessions directory (whose first line is {"type":"session","id":...}),
// with the first and the last plain user text. It only reads files: it does not take a run snapshot, does not start Pi,
// and does not need the Agent to be idle. The title is read only forwards and the preview only backwards from the end,
// one window each (sessionTextWindow), cached by file size and modification time, so a long session is not decoded in
// full every time. Sessions whose ID is duplicated are all left out (there is no telling which one to resume).
func (pd piDriver) listSessions(ctx context.Context, agentID uuid.UUID, currentFile string) ([]executor.ManagedSession, error) {
	out, err := pd.e.scanSessions(ctx, agentID, currentFile, true)
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

// resolveSession finds the session in the sessions directory by its full session ID, reading only the first line of
// each file; it returns nil when there is none or the ID is duplicated.
func (pd piDriver) resolveSession(ctx context.Context, agentID uuid.UUID, sessionID, currentFile string) (*executor.ManagedSession, error) {
	all, err := pd.e.scanSessions(ctx, agentID, currentFile, false)
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

// sessionMeta is a session file's ID and summary of user text; it is re-read when size or mod changes.
type sessionMeta struct {
	size           int64
	mod            time.Time
	id             string
	withText       bool
	title, preview string
}

// sessionTextWindow is the most bytes read each when looking for the title (forwards) and the preview (backwards from
// the end).
const sessionTextWindow = 1 << 20

// scanSessions reads the first line of every .jsonl in the sessions directory (plus the title and preview with
// withText), dropping invalid sessions and sessions with duplicated IDs.
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

// sessionMeta returns a session file's ID and (with withText) its title and preview, using the cache when the file has
// not changed; ok is false when it is not a valid session.
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
		e.sessionCache.Store(path, m) // invalid ones are cached too, so an unchanged file is not read again
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

// firstUserText reads records in order and returns the first plain user text (cut to 80 characters); it stops at the
// end of the window or at a corrupt line.
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

// lastUserText reads one window backwards from the end of the file and returns the last plain user text in it (cut to
// 160 characters). The partial line at the start of the window is dropped.
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

// sessionLineUserText returns the plain user text in one record line (empty when it is not a user message, is a command
// starting with "/", or cannot be parsed).
func sessionLineUserText(line []byte) string {
	if !bytes.Contains(line, []byte(`"user"`)) {
		return "" // most lines are assistant messages and tool results, no need to decode them
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
	if strings.HasPrefix(text, "本轮运行ID：") || strings.HasPrefix(text, "Run ID for this turn: ") { // check-english: zh (older sessions start with this line)
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

// managedSessionFile accepts only existing direct children of the sessions directory, and checks again after resolving
// symlinks.
func managedSessionFile(d agentDirs, path string) (string, error) {
	if path == "" || !filepath.IsAbs(path) {
		return "", errors.New("the session path is not managed by the platform")
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
		return "", errors.New("the session path is not inside the platform-managed sessions directory")
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.Mode().IsRegular() {
		return "", errors.New("the session path is not a regular file")
	}
	return resolved, nil
}
