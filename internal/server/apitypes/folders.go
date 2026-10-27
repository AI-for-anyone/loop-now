package apitypes

import (
	"time"

	"github.com/google/uuid"

	"loop-now/internal/server/folders"
	"loop-now/internal/server/store"
)

// FolderEntry 对应 FolderEntry。
type FolderEntry struct {
	Name          string    `json:"name"`
	Path          string    `json:"path"`
	Type          string    `json:"type"`
	Size          *int64    `json:"size"`
	ModifiedAt    time.Time `json:"modifiedAt"`
	SymlinkTarget *string   `json:"symlinkTarget"`
}

// FromFolderEntry 转换文件夹条目。
func FromFolderEntry(e folders.Entry) FolderEntry {
	return FolderEntry{Name: e.Name, Path: e.Path, Type: e.Type, Size: e.Size, ModifiedAt: e.ModifiedAt, SymlinkTarget: e.SymlinkTarget}
}

// FolderEntryPage 对应 FolderEntryPage。
type FolderEntryPage struct {
	Path       string        `json:"path"`
	Entries    []FolderEntry `json:"entries"`
	NextCursor *string       `json:"nextCursor"`
}

// FromFolderPage 转换列出的一层。
func FromFolderPage(p folders.Page) FolderEntryPage {
	out := FolderEntryPage{Path: p.Path, Entries: make([]FolderEntry, 0, len(p.Entries)), NextCursor: p.NextCursor}
	for _, e := range p.Entries {
		out.Entries = append(out.Entries, FromFolderEntry(e))
	}
	return out
}

// FolderPathInput 与 FolderMoveInput 对应同名请求体。
type FolderPathInput struct {
	Path string `json:"path"`
}

type FolderMoveInput struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// FolderGitPullInput 对应 FolderGitPullInput。
type FolderGitPullInput struct {
	ActionID uuid.UUID `json:"actionId"`
	URL      string    `json:"url"`
	Branch   string    `json:"branch"`
	Path     string    `json:"path"`
}

// FolderGitPull 对应 FolderGitPull。
type FolderGitPull struct {
	ID           uuid.UUID  `json:"id"`
	AgentID      uuid.UUID  `json:"agentId"`
	URL          string     `json:"url"`
	Branch       string     `json:"branch"`
	Path         string     `json:"path"`
	Status       string     `json:"status"`
	ErrorCode    *string    `json:"errorCode"`
	ErrorMessage *string    `json:"errorMessage"`
	CreatedAt    time.Time  `json:"createdAt"`
	FinishedAt   *time.Time `json:"finishedAt"`
}

// FromFolderGitPull 转换拉取操作。
func FromFolderGitPull(p store.FolderGitPull) FolderGitPull {
	return FolderGitPull{ID: p.ID, AgentID: p.AgentID, URL: p.URL, Branch: p.Branch, Path: p.Path, Status: p.Status, ErrorCode: p.ErrorCode, ErrorMessage: p.ErrorMessage, CreatedAt: p.CreatedAt, FinishedAt: p.FinishedAt}
}

// FolderGitPullList 对应 FolderGitPullList。
type FolderGitPullList struct {
	Pulls []FolderGitPull `json:"pulls"`
}
