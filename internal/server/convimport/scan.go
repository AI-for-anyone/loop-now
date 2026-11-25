// Package convimport finds the conversations Claude Code and Codex keep on this computer and turns picked ones into
// Agents that continue them. The scan only reads: conversation records under the two CLIs' history folders, Codex's
// session index, and the git metadata (.git, .git/config) of the folders the conversations worked in; never a login,
// token or credential file, and never anything it changes.
package convimport

import (
	"bufio"
	"bytes"
	"cmp"
	"context"
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// Skip reasons, matching ImportConversation.skipReason.
const (
	SkipTooLarge       = "too_large"
	SkipTooManyRecords = "too_many_records"
	SkipEmpty          = "empty"
	SkipUnreadable     = "unreadable"
)

// Limits (T3's).
const (
	// MaxFileBytes and MaxFileRecords: a conversation larger than this is not imported.
	MaxFileBytes   = 4 << 30
	MaxFileRecords = 100_000
	// MaxFolderConversations is how many conversations of a folder are listed (the newest).
	MaxFolderConversations = 500
	// RecentWindow and PreCheckConversations: a git repository with at least that many conversations active in that
	// window is offered checked.
	RecentWindow          = 30 * 24 * time.Hour
	PreCheckConversations = 3
	// titleLimit is how many characters of a prompt make a title; promptLimit of the first prompt shown.
	titleLimit  = 100
	promptLimit = 500
)

// CLIs.
const (
	CLIClaude = "claude"
	CLICodex  = "codex"
)

// ScanOptions say where to look and what to keep.
type ScanOptions struct {
	// ClaudeProjects is Claude Code's ~/.claude/projects; CodexSessions Codex's ~/.codex/sessions, CodexIndex its
	// ~/.codex/session_index.jsonl (thread names). Empty skips that CLI (or the names).
	ClaudeProjects, CodexSessions, CodexIndex string
	// Since keeps only conversations active since then (zero: all); Path only those whose folder is Path or inside it.
	Since time.Time
	Path  string
	// Now is the clock; nil is time.Now.
	Now func() time.Time
	// Progress is told how many files there are to read and how many are read; may be nil.
	Progress func(done, total int)
	// Open opens a file; nil is os.Open (tests watch what is opened).
	Open func(string) (*os.File, error)
	// MaxBytes and MaxRecords override MaxFileBytes and MaxFileRecords (tests); zero keeps them.
	MaxBytes   int64
	MaxRecords int
}

// Folder is a group of conversations: a git repository (its main checkout, its worktrees and checkouts with the same
// remote folded in) or, outside git, one folder.
type Folder struct {
	Path         string
	Git          bool
	Remote       string // fetch URL of origin (else of the first remote), without credentials; empty when none
	MainCheckout string
	LastActiveAt time.Time
	PreChecked   bool
	// Conversations, newest first; Truncated when more than MaxFolderConversations were found.
	Conversations []Conversation
	Truncated     bool
}

// Conversation is one conversation a CLI keeps.
type Conversation struct {
	CLI          string
	SessionID    string // Claude Code's session id, Codex's thread id
	Path         string // its record
	Cwd          string
	Title        string
	FirstPrompt  string
	LastActiveAt time.Time
	MessageCount int
	SizeBytes    int64
	Model        string
	SkipReason   string
}

// Scan finds the conversations and groups them by folder: git repositories first, then newest first.
func Scan(ctx context.Context, o ScanOptions) ([]Folder, error) {
	o = o.withDefaults()
	var files []candidate
	if o.ClaudeProjects != "" {
		files = append(files, claudeFiles(o.ClaudeProjects)...)
	}
	if o.CodexSessions != "" {
		files = append(files, codexFiles(o.CodexSessions)...)
	}
	if !o.Since.IsZero() {
		// A file not written since then holds no newer record.
		files = slices.DeleteFunc(files, func(c candidate) bool { return c.mtime.Before(o.Since) })
	}
	names := map[string]string{}
	if o.CodexIndex != "" {
		names = codexNames(o, o.CodexIndex)
	}
	if o.Progress != nil {
		o.Progress(0, len(files))
	}
	var convs []Conversation
	for i, f := range files {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		c, ok := readConversation(o, f, names)
		if o.Progress != nil {
			o.Progress(i+1, len(files))
		}
		if !ok || c.Cwd == "" {
			continue
		}
		if !o.Since.IsZero() && c.LastActiveAt.Before(o.Since) {
			continue
		}
		if o.Path != "" && !within(c.Cwd, o.Path) {
			continue
		}
		convs = append(convs, c)
	}
	return group(o, convs), nil
}

func (o ScanOptions) withDefaults() ScanOptions {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Open == nil {
		o.Open = os.Open
	}
	if o.MaxBytes <= 0 {
		o.MaxBytes = MaxFileBytes
	}
	if o.MaxRecords <= 0 {
		o.MaxRecords = MaxFileRecords
	}
	if o.Path != "" {
		o.Path = filepath.Clean(o.Path)
	}
	return o
}

// candidate is a conversation record to read.
type candidate struct {
	cli, path, id string
	size          int64
	mtime         time.Time
}

func within(path, root string) bool {
	path, root = filepath.Clean(path), filepath.Clean(root)
	return path == root || strings.HasPrefix(path, root+string(filepath.Separator))
}

// readConversation reads one record file; false when it is not a conversation (a sub-agent's, say).
func readConversation(o ScanOptions, f candidate, names map[string]string) (Conversation, bool) {
	c := Conversation{CLI: f.cli, SessionID: f.id, Path: f.path, SizeBytes: f.size, LastActiveAt: f.mtime}
	if f.size > o.MaxBytes {
		// Its folder is still wanted, to list it with why it is not imported: read the first records only.
		c.SkipReason = SkipTooLarge
	}
	var r reader
	if f.cli == CLIClaude {
		r = &claudeReader{}
	} else {
		r = &codexReader{}
	}
	records, err := eachRecord(o, f.path, c.SkipReason != "", func(line []byte) bool { return r.record(line) })
	if err != nil {
		c.SkipReason = SkipUnreadable
	}
	m := r.result()
	if m.skip {
		return c, false
	}
	if m.sessionID != "" {
		c.SessionID = m.sessionID
	}
	if f.cli == CLICodex {
		if name := names[c.SessionID]; name != "" {
			m.title = name
		}
	}
	c.Cwd, c.Title, c.FirstPrompt, c.Model, c.MessageCount = m.cwd, m.title, m.firstPrompt, m.model, m.messages
	if !m.lastAt.IsZero() {
		c.LastActiveAt = m.lastAt
	}
	if c.Title == "" {
		c.Title = titleOf(m.titlePrompt)
	}
	switch {
	case c.SkipReason != "":
	case records > o.MaxRecords:
		c.SkipReason = SkipTooManyRecords
	case m.firstPrompt == "":
		c.SkipReason = SkipEmpty
	}
	return c, true
}

// reader reads one CLI's records.
type reader interface {
	// record takes one record; false stops reading (enough is known).
	record(line []byte) bool
	result() meta
}

// meta is what a conversation's records say.
type meta struct {
	skip                  bool // not a conversation
	sessionID, cwd, model string
	title, titlePrompt    string
	firstPrompt           string
	messages              int
	lastAt                time.Time
}

// eachRecord calls fn with each complete record (line) of the file, and returns how many there were. A last line
// without its newline is still being written and is left out. With firstOnly it stops after the first 64 records.
func eachRecord(o ScanOptions, path string, firstOnly bool, fn func([]byte) bool) (int, error) {
	f, err := o.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	r := bufio.NewReaderSize(f, 256<<10)
	n := 0
	for {
		line, err := r.ReadBytes('\n')
		if errors.Is(err, io.EOF) {
			return n, nil // a last line without its newline is incomplete
		}
		if err != nil {
			return n, err
		}
		n++
		line = bytes.TrimSpace(line)
		if len(line) > 0 && !fn(line) {
			return n, nil
		}
		if firstOnly && n >= 64 {
			return n, nil
		}
	}
}

// titleOf is a title from a prompt: its first line, at most titleLimit characters.
func titleOf(prompt string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(prompt), "\n")
	return strings.TrimSpace(clip(line, titleLimit))
}

func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

// group puts conversations in folders.
func group(o ScanOptions, convs []Conversation) []Folder {
	repos := newRepoFinder(o)
	byKey := map[string]*Folder{}
	var order []string
	for _, c := range convs {
		repo := repos.find(c.Cwd)
		key, f := "dir:"+c.Cwd, Folder{Path: c.Cwd}
		if repo != nil {
			key = "repo:" + cmp.Or(repo.remote, repo.main)
			f = Folder{Path: repo.main, Git: true, Remote: repo.remote, MainCheckout: repo.main}
		}
		g := byKey[key]
		if g == nil {
			g = &f
			byKey[key] = g
			order = append(order, key)
		}
		g.Conversations = append(g.Conversations, c)
		if c.LastActiveAt.After(g.LastActiveAt) {
			g.LastActiveAt = c.LastActiveAt
			if repo != nil {
				// Checkouts with the same remote: the group is the most recently active one.
				g.Path, g.MainCheckout = repo.main, repo.main
			}
		}
	}
	now := o.Now()
	out := make([]Folder, 0, len(order))
	for _, key := range order {
		g := *byKey[key]
		slices.SortStableFunc(g.Conversations, func(a, b Conversation) int { return b.LastActiveAt.Compare(a.LastActiveAt) })
		recent := 0
		for _, c := range g.Conversations {
			if now.Sub(c.LastActiveAt) <= RecentWindow {
				recent++
			}
		}
		g.PreChecked = g.Git && recent >= PreCheckConversations
		if len(g.Conversations) > MaxFolderConversations {
			g.Conversations, g.Truncated = g.Conversations[:MaxFolderConversations], true
		}
		out = append(out, g)
	}
	slices.SortStableFunc(out, func(a, b Folder) int {
		if a.Git != b.Git {
			if a.Git {
				return -1
			}
			return 1
		}
		return b.LastActiveAt.Compare(a.LastActiveAt)
	})
	return out
}

// repo is a git repository a folder belongs to.
type repo struct {
	main, remote string
}

// repoFinder finds the git repository of a folder from its .git (a folder, or a worktree's file) and the main
// checkout's .git/config, remembering what it found.
type repoFinder struct {
	o     ScanOptions
	known map[string]*repo
}

func newRepoFinder(o ScanOptions) *repoFinder { return &repoFinder{o: o, known: map[string]*repo{}} }

func (r *repoFinder) find(dir string) *repo {
	dir = filepath.Clean(dir)
	if v, ok := r.known[dir]; ok {
		return v
	}
	var found *repo
	for d := dir; ; d = filepath.Dir(d) {
		if v, ok := r.known[d]; ok {
			found = v
			break
		}
		if info, err := os.Lstat(filepath.Join(d, ".git")); err == nil {
			main := d
			if info.Mode().IsRegular() {
				main = r.worktreeMain(filepath.Join(d, ".git"), d)
			}
			found = &repo{main: main, remote: r.remote(main)}
			r.known[d] = found
			break
		}
		if parent := filepath.Dir(d); parent == d {
			break
		}
	}
	r.known[dir] = found
	return found
}

// worktreeMain is the main checkout of a worktree, whose .git file says "gitdir: <main>/.git/worktrees/<name>".
func (r *repoFinder) worktreeMain(gitFile, dir string) string {
	b, err := readSmall(r.o, gitFile)
	if err != nil {
		return dir
	}
	gitdir, ok := strings.CutPrefix(strings.TrimSpace(string(b)), "gitdir:")
	if !ok {
		return dir
	}
	gitdir = strings.TrimSpace(gitdir)
	if !filepath.IsAbs(gitdir) {
		gitdir = filepath.Join(dir, gitdir)
	}
	if main, _, ok := strings.Cut(filepath.ToSlash(gitdir), "/.git/worktrees/"); ok {
		return filepath.FromSlash(main)
	}
	return dir
}

// remote is the fetch URL of origin (else of the first remote) in the repository's .git/config, without credentials.
func (r *repoFinder) remote(main string) string {
	b, err := readSmall(r.o, filepath.Join(main, ".git", "config"))
	if err != nil {
		return ""
	}
	var first, origin, section string
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[") {
			section = line
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok || strings.TrimSpace(key) != "url" || !strings.HasPrefix(section, `[remote "`) {
			continue
		}
		value = withoutCredentials(strings.TrimSpace(value))
		if first == "" {
			first = value
		}
		if section == `[remote "origin"]` && origin == "" {
			origin = value
		}
	}
	return cmp.Or(origin, first)
}

// withoutCredentials drops a user and password from a URL (an https remote can carry a token).
func withoutCredentials(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.User == nil || u.Scheme == "" {
		return raw
	}
	u.User = nil
	return u.String()
}

// readSmall reads a small metadata file (at most 1 MiB).
func readSmall(o ScanOptions, path string) ([]byte, error) {
	f, err := o.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, 1<<20))
}
