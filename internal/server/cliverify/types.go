// Package cliverify defines safe, version-bound CLI verification operations.
// Controllers never accept credentials, native arguments or caller-selected probes.
package cliverify

import (
	"context"
	"github.com/google/uuid"
	"loop-now/internal/server/apperr"
	"time"
)

type Start struct {
	OperationID       uuid.UUID `json:"operationId"`
	ExpectedVersionID uuid.UUID `json:"expectedVersionId"`
}

func (in Start) Validate() error {
	if in.OperationID == uuid.Nil {
		return apperr.Invalid("operationId", "须提供非零UUID")
	}
	if in.ExpectedVersionID == uuid.Nil {
		return apperr.Invalid("expectedVersionId", "须提供非零UUID")
	}
	return nil
}

type Result struct {
	Outcome    string     `json:"outcome"`
	VerifiedAt *time.Time `json:"verifiedAt"`
	DurationMS int64      `json:"durationMs"`
	ErrorCode  *string    `json:"errorCode"`
	HTTPStatus *int       `json:"httpStatus"`
}

type Operation struct {
	ID                  uuid.UUID `json:"id"`
	ProjectID           uuid.UUID `json:"projectId"`
	AccountID           uuid.UUID `json:"accountId"`
	VersionID           uuid.UUID `json:"versionId"`
	CLI                 string    `json:"cli"`
	AuthMethod          string    `json:"authMethod"`
	TemplateSource      string    `json:"templateSource"`
	CredentialFormat    string    `json:"credentialFormat"`
	TemplateFingerprint string    `json:"templateFingerprint"`
	// ToolVersion is populated only after the actual prepared tool is verified.
	ToolVersion      *string    `json:"toolVersion"`
	AdapterRevision  string     `json:"adapterRevision"`
	ProbeKind        string     `json:"probeKind"`
	Status           string     `json:"status"`
	Phase            *string    `json:"phase"`
	StartedAt        time.Time  `json:"startedAt"`
	ExpiresAt        time.Time  `json:"expiresAt"`
	CompletedAt      *time.Time `json:"completedAt"`
	Result           *Result    `json:"result"`
	IsCurrentVersion bool       `json:"isCurrentVersion"`
}

type State struct {
	AccountID        uuid.UUID  `json:"accountId"`
	CurrentVersionID *uuid.UUID `json:"currentVersionId"`
	Operation        *Operation `json:"operation"`
}

type VersionConflictDetails struct {
	ProjectID         uuid.UUID  `json:"projectId"`
	AccountID         uuid.UUID  `json:"accountId"`
	ExpectedVersionID uuid.UUID  `json:"expectedVersionId"`
	CurrentVersionID  *uuid.UUID `json:"currentVersionId"`
}

type ConflictDetails struct {
	ProjectID   uuid.UUID `json:"projectId"`
	AccountID   uuid.UUID `json:"accountId"`
	OperationID uuid.UUID `json:"operationId"`
	VersionID   uuid.UUID `json:"versionId"`
}

type UnsupportedDetails struct {
	CLI            string `json:"cli"`
	TemplateSource string `json:"templateSource"`
	Reason         string `json:"reason"`
}

// Controller separates HTTP contract assembly from persistent workers/adapters.
// A nil controller must return 503, never pretend that a probe was accepted.
type Controller interface {
	Start(context.Context, uuid.UUID, uuid.UUID, Start) (Operation, error)
	State(context.Context, uuid.UUID, uuid.UUID) (State, error)
	Get(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) (Operation, error)
	Cancel(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) (Operation, error)
}
