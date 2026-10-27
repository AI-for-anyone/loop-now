// Package modelauth owns provider authentication operations and grant versions.
package modelauth

import (
	"context"
	"github.com/google/uuid"
	"loop-now/internal/server/clitemplate"
	"time"
)

type LoginInput struct {
	Provider              string `json:"provider"`
	ExpectedConfigVersion int64  `json:"expectedConfigVersion"`
}
type Operation struct {
	ID               uuid.UUID  `json:"id"`
	AgentID          uuid.UUID  `json:"agentId"`
	Provider         string     `json:"provider"`
	ConfigVersion    int64      `json:"configVersion"`
	Status           string     `json:"status"`
	StartedAt        time.Time  `json:"startedAt"`
	ExpiresAt        time.Time  `json:"expiresAt"`
	CompletedAt      *time.Time `json:"completedAt"`
	AuthorizationURL *string    `json:"authorizationUrl"`
	ErrorCode        *string    `json:"errorCode"`
	ErrorMessage     *string    `json:"errorMessage"`
}
type State struct {
	Credential *Metadata  `json:"credential"`
	Operation  *Operation `json:"operation"`
}

// Controller exposes only redacted console views. Runtime refresh uses private IPC.
type Controller interface {
	State(context.Context, uuid.UUID) (State, error)
	Start(context.Context, uuid.UUID, LoginInput) (Operation, error)
	Get(context.Context, uuid.UUID, uuid.UUID) (Operation, error)
	Cancel(context.Context, uuid.UUID, uuid.UUID) (Operation, error)
}

// Metadata is the safe shared credential view; it never contains token material.
type Metadata struct {
	ProjectID    *uuid.UUID            `json:"projectId,omitempty"`
	AccountID    *uuid.UUID            `json:"accountId,omitempty"`
	Status       string                `json:"status"`
	UpdatedAt    string                `json:"updatedAt"`
	FieldNames   []string              `json:"fieldNames"`
	EnvNames     []string              `json:"envNames"`
	FilePaths    []string              `json:"filePaths"`
	Warnings     []clitemplate.Warning `json:"warnings"`
	Provider     *string               `json:"provider,omitempty"`
	AuthMethod   *string               `json:"authMethod,omitempty"`
	VersionID    *uuid.UUID            `json:"versionId,omitempty"`
	AccountLabel *string               `json:"accountLabel,omitempty"`
}
