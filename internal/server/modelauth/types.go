// Package modelauth owns provider authentication operations and grant versions.
package modelauth

import (
	"context"
	"github.com/google/uuid"
	"loop-now/internal/server/clitemplate"
	cm "loop-now/internal/server/credentialmodel"
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
	// DeviceCode and Prompt are only set for plugin-provider logins (plugins/SPEC.md §13); they live in memory and
	// are nil once the operation ends.
	DeviceCode *cm.DeviceCode  `json:"deviceCode"`
	Prompt     *cm.LoginPrompt `json:"prompt"`
}
type State struct {
	Credential *Metadata  `json:"credential"`
	Operation  *Operation `json:"operation"`
	// LocalLogin is set only for a plugin provider that declares account state paths, while "use this machine's
	// logins" is on: active or missing.
	LocalLogin *string `json:"localLogin,omitempty"`
}

// Controller exposes only redacted console views. Runtime refresh uses private IPC.
type Controller interface {
	State(context.Context, uuid.UUID) (State, error)
	Start(context.Context, uuid.UUID, LoginInput) (Operation, error)
	Get(context.Context, uuid.UUID, uuid.UUID) (Operation, error)
	Cancel(context.Context, uuid.UUID, uuid.UUID) (Operation, error)
	// Input answers the question a plugin-provider login is waiting on.
	Input(context.Context, uuid.UUID, uuid.UUID, cm.LoginInput) (State, error)
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
