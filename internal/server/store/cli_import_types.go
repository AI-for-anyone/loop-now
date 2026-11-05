package store

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	"loop-now/internal/server/db"
)

type CLIImportItem struct {
	CandidateID uuid.UUID  `json:"candidateId"`
	CLI         string     `json:"cli"`
	Label       string     `json:"label"`
	Status      string     `json:"status"`
	AccountID   *uuid.UUID `json:"accountId"`
	VersionID   *uuid.UUID `json:"versionId"`
	ErrorCode   *string    `json:"errorCode"`
}
type CLIImportOperation struct {
	ID                  uuid.UUID       `json:"id"`
	ProjectID           uuid.UUID       `json:"projectId"`
	ScanID              uuid.UUID       `json:"scanId"`
	Status              string          `json:"status"`
	Phase               *string         `json:"phase"`
	CreatedAt           time.Time       `json:"createdAt"`
	DeadlineAt          time.Time       `json:"deadlineAt"`
	CompletedAt         *time.Time      `json:"completedAt"`
	Items               []CLIImportItem `json:"items"`
	ErrorCode           *string         `json:"errorCode"`
	CredentialsRevision *int64          `json:"credentialsRevision"`
}

func (o CLIImportOperation) Terminal() bool { return o.Status != "pending" && o.Status != "running" }

// Internal-only inputs. No formatter or JSON projection may expose digests or credential bytes.
type CLIImportBegin struct {
	ID, ProjectID, ScanID, InstanceID uuid.UUID
	RequestFingerprint                []byte
	ExpectedRevision                  int64
	ProvenanceKeyID                   string
	Deadline                          time.Time
	Items                             []CLIImportItem
}
type CLIImportPreparedItem struct {
	CandidateID, AccountID, VersionID                       uuid.UUID
	CLI, Name, TemplateSource, CredentialFormat, AuthMethod string
	OriginMAC, ContentMAC                                   []byte
	ProvenanceKeyID                                         string
	Secret                                                  []byte
	Meta                                                    CredentialMeta
}

func (CLIImportPreparedItem) String() string   { return "[private credential import]" }
func (CLIImportPreparedItem) GoString() string { return "[private credential import]" }
func (CLIImportPreparedItem) MarshalJSON() ([]byte, error) {
	return nil, errors.New("private credential import")
}
func (CLIImportBegin) String() string               { return "[private import request]" }
func (CLIImportBegin) GoString() string             { return "[private import request]" }
func (CLIImportBegin) MarshalJSON() ([]byte, error) { return nil, errors.New("private import request") }

// CommitUnknown is never equivalent to rollback. Callers only read the original
// operation or perform a same-lock-order conditional transition; never replay work.
type CLIImportCommitUnknown struct{}

// CLIImportItemFailure preserves the exact failed candidate without exposing
// source paths, digests, credential bytes, or native error text in its formatter.
type CLIImportItemFailure struct {
	CandidateID uuid.UUID
	Code        string
	cause       error
}

func (e *CLIImportItemFailure) Error() string { return "import item failure: " + e.Code }
func (e *CLIImportItemFailure) Unwrap() error { return e.cause }
func importItemFailure(id uuid.UUID, code string, cause error) error {
	return &CLIImportItemFailure{CandidateID: id, Code: importCode(code), cause: cause}
}

func (*CLIImportCommitUnknown) Error() string { return "import commit outcome unknown" }
func IsCLIImportCommitUnknown(err error) bool {
	var e *CLIImportCommitUnknown
	return errors.As(err, &e)
}

// cliImportTx 在写事务中执行 fn。SQLite 的提交不可取消，提交返回的错误就是确定的失败，不再有“结果未知”；
// CLIImportCommitUnknown 留给导入服务的既有分支，这里不再产生。
func (s *Store) cliImportTx(ctx context.Context, fn func(*db.Tx) error) error {
	return s.Pool.InTx(ctx, fn)
}
