package convimport

import (
	"context"
	"path/filepath"
	"sync"
	"time"

	"github.com/google/uuid"

	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/apperr"
	"loop-now/internal/server/store"
)

// keepOperations is how long a scan or import is kept after it ends.
const keepOperations = time.Hour

// Service runs scans and imports as operations the console follows.
type Service struct {
	Store *store.Store
	// ClaudeProjects is where Claude Code keeps its sessions (~/.claude/projects); CodexHome is Codex's home
	// (~/.codex). Empty skips that CLI.
	ClaudeProjects func(context.Context) string
	CodexHome      func() string
	// Now is the clock; nil is time.Now.
	Now func() time.Time

	mu    sync.Mutex
	scans map[string]*scanOp
}

// scanOp is a scan in progress or done.
type scanOp struct {
	mu      sync.Mutex
	view    apitypes.ConversationScan
	folders []Folder // what was found, kept for the import
	ended   time.Time
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// StartScan starts a scan and returns it running.
func (s *Service) StartScan(ctx context.Context, in apitypes.ConversationScanInput) (apitypes.ConversationScan, error) {
	o := ScanOptions{Now: s.now}
	if in.Since != nil {
		o.Since = *in.Since
	}
	if in.Path != nil && *in.Path != "" {
		if !filepath.IsAbs(*in.Path) {
			return apitypes.ConversationScan{}, apperr.Invalid("path", "Must be an absolute path.")
		}
		o.Path = *in.Path
	}
	if s.ClaudeProjects != nil {
		o.ClaudeProjects = s.ClaudeProjects(ctx)
	}
	if s.CodexHome != nil {
		if home := s.CodexHome(); home != "" {
			o.CodexSessions, o.CodexIndex = filepath.Join(home, "sessions"), filepath.Join(home, "session_index.jsonl")
		}
	}
	op := &scanOp{view: apitypes.ConversationScan{OperationID: uuid.Must(uuid.NewV7()).String(), State: "running", Folders: []apitypes.ImportFolder{}}}
	o.Progress = func(done, total int) {
		op.mu.Lock()
		op.view.FilesDone, op.view.FilesTotal = done, total
		op.mu.Unlock()
	}
	s.mu.Lock()
	s.forgetLocked()
	if s.scans == nil {
		s.scans = map[string]*scanOp{}
	}
	s.scans[op.view.OperationID] = op
	view := op.view
	s.mu.Unlock()
	go s.runScan(op, o)
	return view, nil
}

func (s *Service) runScan(op *scanOp, o ScanOptions) {
	ctx := context.Background()
	folders, err := Scan(ctx, o)
	var views []apitypes.ImportFolder
	if err == nil {
		views, err = s.folderViews(ctx, folders)
	}
	op.mu.Lock()
	defer op.mu.Unlock()
	op.ended = s.now()
	if err != nil {
		msg := err.Error()
		op.view.State, op.view.Error = "failed", &msg
		return
	}
	op.view.State, op.view.Folders, op.folders = "done", views, folders
}

// Scan returns a scan as it is now.
func (s *Service) Scan(id string) (apitypes.ConversationScan, error) {
	op := s.scan(id)
	if op == nil {
		return apitypes.ConversationScan{}, apperr.NotFound("Conversation scan")
	}
	op.mu.Lock()
	defer op.mu.Unlock()
	return op.view, nil
}

func (s *Service) scan(id string) *scanOp {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.forgetLocked()
	return s.scans[id]
}

// forgetLocked drops operations that ended more than keepOperations ago.
func (s *Service) forgetLocked() {
	now := s.now()
	for id, op := range s.scans {
		op.mu.Lock()
		old := !op.ended.IsZero() && now.Sub(op.ended) > keepOperations
		op.mu.Unlock()
		if old {
			delete(s.scans, id)
		}
	}
}

// folderViews is what the console is shown of the folders: each with the project that already has it as a local
// directory, and each conversation with the Agent it was imported as.
func (s *Service) folderViews(ctx context.Context, folders []Folder) ([]apitypes.ImportFolder, error) {
	out := make([]apitypes.ImportFolder, 0, len(folders))
	for _, f := range folders {
		v := apitypes.ImportFolder{Path: f.Path, Git: f.Git, LastActiveAt: f.LastActiveAt, PreChecked: f.PreChecked, Truncated: f.Truncated,
			Conversations: make([]apitypes.ImportConversation, 0, len(f.Conversations))}
		if f.Git {
			v.Repository = &apitypes.ImportRepository{MainCheckout: f.MainCheckout}
			if f.Remote != "" {
				remote := f.Remote
				v.Repository.Remote = &remote
			}
		}
		project, err := s.Store.ProjectWithLocalDirectory(ctx, f.Path)
		if err != nil {
			return nil, err
		}
		v.ProjectID = project
		for _, c := range f.Conversations {
			cv := apitypes.ImportConversation{CLI: c.CLI, SessionID: c.SessionID, Cwd: c.Cwd, Title: c.Title, FirstPrompt: c.FirstPrompt,
				LastActiveAt: c.LastActiveAt, MessageCount: c.MessageCount, SizeBytes: c.SizeBytes}
			if c.Model != "" {
				model := c.Model
				cv.Model = &model
			}
			if c.SkipReason != "" {
				reason := c.SkipReason
				cv.SkipReason = &reason
			}
			v.Conversations = append(v.Conversations, cv)
		}
		out = append(out, v)
	}
	return out, nil
}
