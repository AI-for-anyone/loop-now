package convimport

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"

	"loop-now/internal/server/agentruntime"
	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/apperr"
	"loop-now/internal/server/folders"
	"loop-now/internal/server/modelcatalog"
	"loop-now/internal/server/naming"
	"loop-now/internal/server/store"
)

// Import limits (T3's): per folder and attempt, and the messages an Agent's chat shows of a conversation.
const (
	MaxImportConversations = 100
	MaxImportBytes         = 4 << 30
	MaxDisplayMessages     = 200
)

// Capture file names in the Agent's folder.
const (
	claudeCaptureFile = "imported-session.jsonl"
	codexCaptureFile  = "imported-thread.json"
)

// Models is what the import needs of the model catalog.
type Models interface {
	Provider(string) (*modelcatalog.Provider, bool)
}

// importOp is an import in progress or done.
type importOp struct {
	view  apitypes.ConversationImport
	ended time.Time
}

// StartImport checks the picks against the scan and starts importing them.
func (s *Service) StartImport(ctx context.Context, in apitypes.ConversationImportInput) (apitypes.ConversationImport, error) {
	scan := s.scan(in.ScanID)
	if scan == nil {
		return apitypes.ConversationImport{}, apperr.NotFound("Conversation scan")
	}
	scan.mu.Lock()
	state, found := scan.view.State, scan.folders
	scan.mu.Unlock()
	if state != "done" {
		return apitypes.ConversationImport{}, apperr.Invalid("scanId", "The scan has not finished.")
	}
	if len(in.Folders) == 0 {
		return apitypes.ConversationImport{}, apperr.Invalid("folders", "Pick at least one folder.")
	}
	maxBytes := s.MaxFolderBytes
	if maxBytes <= 0 {
		maxBytes = MaxImportBytes
	}
	jobs := make([]importJob, 0, len(in.Folders))
	total := 0
	for i, pick := range in.Folders {
		field := fmt.Sprintf("folders[%d]", i)
		idx := slices.IndexFunc(found, func(f Folder) bool { return f.Path == pick.Path })
		if idx < 0 {
			return apitypes.ConversationImport{}, apperr.Invalid(field+".path", "The scan did not find this folder.")
		}
		if len(pick.SessionIDs) == 0 || len(pick.SessionIDs) > MaxImportConversations {
			return apitypes.ConversationImport{}, apperr.Invalid(field+".sessionIds", fmt.Sprintf("Pick 1 to %d conversations per folder.", MaxImportConversations))
		}
		var size int64
		for _, id := range pick.SessionIDs {
			if c, ok := findConversation(found[idx], id); ok {
				size += c.SizeBytes
			}
		}
		if size > maxBytes {
			return apitypes.ConversationImport{}, apperr.Invalid(field+".sessionIds", "The conversations picked in this folder are larger than 4 GiB together; import them in parts.")
		}
		jobs = append(jobs, importJob{folder: found[idx], projectID: pick.ProjectID, sessionIDs: slices.Clone(pick.SessionIDs)})
		total += len(pick.SessionIDs)
	}
	op := &importOp{view: apitypes.ConversationImport{OperationID: uuid.Must(uuid.NewV7()).String(), State: "running", Total: total,
		Projects: []apitypes.ImportProject{}, Agents: []apitypes.ImportedAgent{}, Errors: []apitypes.ImportError{}}}
	s.mu.Lock()
	s.forgetLocked()
	if s.imports == nil {
		s.imports = map[string]*importOp{}
	}
	s.imports[op.view.OperationID] = op
	view := op.view
	s.mu.Unlock()
	go s.runImport(op, jobs)
	return view, nil
}

// Import returns an import as it is now.
func (s *Service) Import(id string) (apitypes.ConversationImport, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.forgetLocked()
	op := s.imports[id]
	if op == nil {
		return apitypes.ConversationImport{}, apperr.NotFound("Conversation import")
	}
	v := op.view
	v.Projects, v.Agents, v.Errors = slices.Clone(v.Projects), slices.Clone(v.Agents), slices.Clone(v.Errors)
	return v, nil
}

// update changes an import's view under the lock.
func (s *Service) update(op *importOp, fn func(v *apitypes.ConversationImport)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fn(&op.view)
}

type importJob struct {
	folder     Folder
	projectID  *uuid.UUID
	sessionIDs []string
}

func (s *Service) runImport(op *importOp, jobs []importJob) {
	ctx := context.Background()
	failed := func(err error) {
		msg := err.Error()
		s.update(op, func(v *apitypes.ConversationImport) { v.State, v.Error, op.ended = "failed", &msg, s.now() })
	}
	for _, job := range jobs {
		projectID, err := s.folderProject(ctx, job)
		if err != nil {
			failed(fmt.Errorf("folder %s: %w", job.folder.Path, err))
			return
		}
		s.update(op, func(v *apitypes.ConversationImport) {
			v.Projects = append(v.Projects, apitypes.ImportProject{Path: job.folder.Path, ProjectID: projectID})
		})
		for _, id := range job.sessionIDs {
			agentID, err := s.importConversation(ctx, projectID, job.folder, id)
			s.update(op, func(v *apitypes.ConversationImport) {
				v.Done++
				if err != nil {
					v.Errors = append(v.Errors, apitypes.ImportError{SessionID: id, Message: err.Error()})
				} else {
					v.Agents = append(v.Agents, apitypes.ImportedAgent{SessionID: id, AgentID: agentID})
				}
			})
		}
	}
	s.update(op, func(v *apitypes.ConversationImport) { v.State, op.ended = "done", s.now() })
}

// folderProject is the project a folder goes to (the one picked, else one that has the folder as a local directory,
// else a new one named after the folder), with the folder added to its local directories.
func (s *Service) folderProject(ctx context.Context, job importJob) (uuid.UUID, error) {
	path := job.folder.Path
	var id uuid.UUID
	switch {
	case job.projectID != nil:
		p, err := s.Store.GetProject(ctx, *job.projectID)
		if err != nil {
			return uuid.Nil, err
		}
		id = p.ID
	default:
		bound, err := s.Store.ProjectWithLocalDirectory(ctx, path)
		if err != nil {
			return uuid.Nil, err
		}
		if bound != nil {
			return *bound, nil
		}
		if id, err = s.newProject(ctx, filepath.Base(path)); err != nil {
			return uuid.Nil, err
		}
	}
	for attempt := 0; ; attempt++ {
		dirs, err := s.Store.ProjectLocalDirectories(ctx, id)
		if err != nil {
			return uuid.Nil, err
		}
		if slices.ContainsFunc(dirs.Directories, func(d store.LocalDirectoryEntry) bool { return d.Path == path }) {
			return id, nil
		}
		_, err = s.Store.PutProjectLocalDirectories(ctx, id, dirs.Revision, append(dirs.Directories, store.LocalDirectoryEntry{Path: path}))
		if err == nil || attempt == 2 || !apperr.Is(err, apperr.CodePreconditionFailed) {
			return id, err
		}
	}
}

// newProject creates a project named after a folder (with " (2)", " (3)" ... when the name is taken).
func (s *Service) newProject(ctx context.Context, folder string) (uuid.UUID, error) {
	base := cleanName(folder, "Imported")
	for n := 1; n < 1000; n++ {
		name := base
		if n > 1 {
			suffix := fmt.Sprintf(" (%d)", n)
			name = clip(base, naming.MaxLen-len([]rune(suffix))) + suffix
		}
		p, err := s.Store.CreateProject(ctx, s.OrgID, name)
		if apperr.Is(err, apperr.CodeNameConflict) {
			continue
		}
		return p.ID, err
	}
	return uuid.Nil, errors.New("no free project name")
}

func findConversation(f Folder, sessionID string) (Conversation, bool) {
	for _, c := range f.Conversations {
		if c.SessionID == sessionID {
			return c, true
		}
	}
	return Conversation{}, false
}

// skipMessages say why a conversation the scan listed cannot be imported.
var skipMessages = map[string]string{
	SkipTooLarge:       "The conversation is larger than 4 GiB.",
	SkipTooManyRecords: "The conversation has more than 100,000 records.",
	SkipEmpty:          "The conversation has no prompt.",
	SkipUnreadable:     "The conversation could not be read.",
}

// importConversation makes an Agent of one conversation (or gives the Agent it was imported as before).
func (s *Service) importConversation(ctx context.Context, projectID uuid.UUID, folder Folder, sessionID string) (uuid.UUID, error) {
	c, ok := findConversation(folder, sessionID)
	if !ok {
		return uuid.Nil, errors.New("The scan did not find this conversation in the folder.")
	}
	if c.SkipReason != "" {
		return uuid.Nil, errors.New(skipMessages[c.SkipReason])
	}
	if existing, err := s.Store.ImportedAgent(ctx, c.CLI, c.SessionID); err != nil || existing != nil {
		if err != nil {
			return uuid.Nil, err
		}
		return *existing, nil
	}
	provider, modelID, err := s.model(c)
	if err != nil {
		return uuid.Nil, err
	}
	id := uuid.Must(uuid.NewV7())
	root := folders.AgentRoot(s.WorkRoot, id)
	if err := os.MkdirAll(root, 0o700); err != nil {
		return uuid.Nil, err
	}
	capture, messages, err := s.capture(c, root)
	if err != nil {
		_ = os.RemoveAll(root)
		return uuid.Nil, err
	}
	mode := store.PermissionModeFull
	if s.DefaultMode != nil {
		if mode, err = s.DefaultMode(ctx); err != nil {
			_ = os.RemoveAll(root)
			return uuid.Nil, err
		}
	}
	a, err := s.Store.CreateImportedAgent(ctx, store.NewImportedAgent{ID: id, ProjectID: projectID, Name: cleanName(c.Title, "Imported conversation"),
		Provider: provider, ModelID: modelID, WorkingDirectory: c.Cwd, PermissionMode: mode,
		From:  store.ImportedFrom{CLI: c.CLI, SessionID: c.SessionID, Path: c.Path, ImportedAt: s.now(), Capture: capture},
		Turns: displayTurns(messages)})
	if err != nil {
		_ = os.RemoveAll(root)
		var already *store.AlreadyImportedError
		if errors.As(err, &already) {
			return already.AgentID, nil
		}
		return uuid.Nil, err
	}
	if s.AgentCreated != nil {
		s.AgentCreated(a.ID)
	}
	return a.ID, nil
}

// capture takes the copy the Agent continues from, in its folder, and reads the conversation's messages as of that copy.
// Claude Code: the session file is cloned and cut after its last complete record. Codex: its thread and last ended turn
// are recorded (the Agent forks the thread from there), and the messages stop at that turn.
func (s *Service) capture(c Conversation, root string) (string, []Message, error) {
	o := ScanOptions{}.withDefaults()
	var messages []Message
	keep := func(m Message) { messages = append(messages, m) }
	switch c.CLI {
	case CLIClaude:
		path := filepath.Join(root, claudeCaptureFile)
		if _, err := folders.CloneFile(c.Path, path); err != nil {
			return "", nil, fmt.Errorf("could not copy the conversation: %w", err)
		}
		if err := cutAfterLastRecord(path); err != nil {
			return "", nil, fmt.Errorf("could not copy the conversation: %w", err)
		}
		r := &claudeReader{onMessage: keep}
		if _, err := eachRecord(o, path, false, r.record); err != nil {
			return "", nil, fmt.Errorf("could not read the conversation: %w", err)
		}
		return path, messages, nil
	case CLICodex:
		r := &codexReader{onMessage: keep}
		if _, err := eachRecord(o, c.Path, false, r.record); err != nil {
			return "", nil, fmt.Errorf("could not read the conversation: %w", err)
		}
		r.result()
		if r.lastTurn != "" && r.messagesAtTurn < len(messages) {
			messages = messages[:r.messagesAtTurn]
		}
		b, err := json.Marshal(map[string]string{"threadId": c.SessionID, "lastTurnId": r.lastTurn})
		if err != nil {
			return "", nil, err
		}
		path := filepath.Join(root, codexCaptureFile)
		if err := os.WriteFile(path, b, 0o600); err != nil {
			return "", nil, err
		}
		return path, messages, nil
	}
	return "", nil, fmt.Errorf("unknown CLI %s", c.CLI)
}

// cutAfterLastRecord drops a last line still being written (one without its newline).
func cutAfterLastRecord(path string) error {
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	size := info.Size()
	buf := make([]byte, 64<<10)
	for end := size; end > 0; {
		start := max(end-int64(len(buf)), 0)
		chunk := buf[:end-start]
		if _, err := f.ReadAt(chunk, start); err != nil {
			return err
		}
		if i := strings.LastIndexByte(string(chunk), '\n'); i >= 0 {
			if keep := start + int64(i) + 1; keep < size {
				return f.Truncate(keep)
			}
			return nil
		}
		end = start
	}
	return f.Truncate(0)
}

// model is the Agent's model: the conversation's last one when the runtime lists it (for Claude Code, the alias of its
// family, such as opus for claude-opus-5-5), else the first model the runtime lists.
func (s *Service) model(c Conversation) (string, string, error) {
	rt, ok := agentruntime.ForImportedCLI(c.CLI)
	if !ok || len(rt.Providers) == 0 {
		return "", "", fmt.Errorf("conversations of %s cannot be imported", c.CLI)
	}
	provider, runtime := rt.Providers[0], rt.Name
	var p *modelcatalog.Provider
	if s.Models != nil {
		p, _ = s.Models.Provider(provider)
	}
	if p == nil || len(p.Models) == 0 {
		return "", "", fmt.Errorf("%s lists no models on this computer; check that it is installed and signed in, then import again", runtime)
	}
	if c.Model != "" {
		for _, m := range p.Models {
			if m.ID == c.Model {
				return provider, m.ID, nil
			}
		}
		if c.CLI == CLIClaude {
			parts := strings.Split(c.Model, "-")
			for _, m := range p.Models {
				if slices.Contains(parts, m.ID) {
					return provider, m.ID, nil
				}
			}
		}
	}
	return provider, p.Models[0].ID, nil
}

// displayTurns are the turns the Agent's chat shows: whole turns (a prompt and its replies' text joined), the first
// one and then the newest, at most MaxDisplayMessages messages in all.
func displayTurns(messages []Message) []store.ImportedTurn {
	var turns []store.ImportedTurn
	for _, m := range messages {
		switch {
		case m.Role == "user":
			turns = append(turns, store.ImportedTurn{Prompt: m.Text, PromptAt: m.At})
		case len(turns) > 0:
			t := &turns[len(turns)-1]
			if t.Reply != "" {
				t.Reply += "\n\n"
			}
			t.Reply += m.Text
			t.ReplyAt = m.At
		}
	}
	size := func(t store.ImportedTurn) int {
		if t.Reply == "" {
			return 1
		}
		return 2
	}
	if len(turns) == 0 {
		return nil
	}
	used, from := size(turns[0]), len(turns)
	for from > 1 && used+size(turns[from-1]) <= MaxDisplayMessages {
		from--
		used += size(turns[from])
	}
	return append([]store.ImportedTurn{turns[0]}, turns[from:]...)
}

// cleanName is text made into a valid Agent or project name: no control characters, / or #, no leading @ or spaces,
// at most naming.MaxLen characters; fallback when nothing is left.
func cleanName(text, fallback string) string {
	text, _, _ = strings.Cut(strings.TrimSpace(text), "\n")
	text = strings.Map(func(r rune) rune {
		switch {
		case r == '/' || r == '#':
			return '-'
		case unicode.IsControl(r):
			return ' '
		}
		return r
	}, text)
	text = strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(text), "@"))
	text = strings.TrimSpace(clip(text, naming.MaxLen))
	if text == "" || naming.Validate("name", text) != "" {
		return fallback
	}
	return text
}
