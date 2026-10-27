// Package credentialmodel contains safe project credential views and strict
// input DTOs. Secrets have no implicit JSON encoding or printable representation.
package credentialmodel

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"io"
	"loop-now/internal/server/clitemplate"
	"time"
)

type Template struct {
	Source           string                `json:"source"`
	CredentialFormat string                `json:"credentialFormat"`
	FieldNames       []string              `json:"fieldNames"`
	Warnings         []clitemplate.Warning `json:"warnings"`
	Status           string                `json:"status"`
}
type Account struct {
	ID               uuid.UUID        `json:"id"`
	ProjectID        uuid.UUID        `json:"projectId"`
	Purpose          string           `json:"purpose"`
	Provider         *string          `json:"provider"`
	CLI              *string          `json:"cli"`
	Name             string           `json:"name"`
	AccountLabel     *string          `json:"accountLabel"`
	GitHubIdentity   *GitHubIdentity  `json:"githubIdentity"`
	LarkIdentity     *LarkIdentity    `json:"larkIdentity"`
	LarkApplication  *LarkApplication `json:"larkApplication"`
	Revision         int64            `json:"revision"`
	CurrentVersionID *uuid.UUID       `json:"currentVersionId"`
	AuthMethod       *string          `json:"authMethod"`
	Status           string           `json:"status"`
	Template         *Template        `json:"template"`
	CreatedAt        time.Time        `json:"createdAt"`
	UpdatedAt        time.Time        `json:"updatedAt"`
	DeletedAt        *time.Time       `json:"deletedAt"`
}
type AccountList struct {
	ProjectID           uuid.UUID `json:"projectId"`
	CredentialsRevision int64     `json:"credentialsRevision"`
	Accounts            []Account `json:"accounts"`
}
type Selection struct {
	Purpose           string     `json:"purpose"`
	Provider          *string    `json:"provider"`
	CLI               *string    `json:"cli"`
	Mode              string     `json:"mode"`
	SelectedAccountID *uuid.UUID `json:"selectedAccountId"`
	ResolvedAccountID *uuid.UUID `json:"resolvedAccountId"`
	ResolvedVersionID *uuid.UUID `json:"resolvedVersionId"`
	Status            string     `json:"status"`
	Candidates        []Account  `json:"candidates"`
}
type Selections struct {
	AgentID             uuid.UUID   `json:"agentId"`
	ProjectID           uuid.UUID   `json:"projectId"`
	ConfigVersion       int64       `json:"configVersion"`
	CredentialsRevision int64       `json:"credentialsRevision"`
	Selections          []Selection `json:"selections"`
}
type ImpactAgent struct {
	AgentID           uuid.UUID  `json:"agentId"`
	Name              string     `json:"name"`
	Mode              string     `json:"mode"`
	SelectedAccountID *uuid.UUID `json:"selectedAccountId"`
	ResolvedAccountID *uuid.UUID `json:"resolvedAccountId"`
	Status            string     `json:"status"`
}
type Impact struct {
	ProjectID           uuid.UUID     `json:"projectId"`
	CredentialsRevision int64         `json:"credentialsRevision"`
	AccountID           *uuid.UUID    `json:"accountId"`
	Agents              []ImpactAgent `json:"agents"`
}
type CredentialInput struct {
	Type   string
	Key    string
	Fields map[string]string
}

func (CredentialInput) String() string   { return "[credential input]" }
func (CredentialInput) GoString() string { return "[credential input]" }
func (CredentialInput) MarshalJSON() ([]byte, error) {
	return nil, errors.New("秘密只能通过显式凭证通道编码")
}
func strict(data []byte, v any) error {
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if d.Decode(v) != nil || d.Decode(new(any)) != io.EOF {
		return errors.New("请求字段或格式无效")
	}
	return nil
}
func (c *CredentialInput) UnmarshalJSON(data []byte) error {
	var kind struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(data, &kind) != nil {
		return errors.New("credential格式无效")
	}
	switch kind.Type {
	case "api_key":
		var v struct {
			Type string  `json:"type"`
			Key  *string `json:"key"`
		}
		if strict(data, &v) != nil || v.Key == nil || *v.Key == "" {
			return errors.New("credential API Key字段无效")
		}
		*c = CredentialInput{Type: v.Type, Key: *v.Key}
	case "cli":
		var v struct {
			Type   string             `json:"type"`
			Fields map[string]*string `json:"fields"`
		}
		if strict(data, &v) != nil || len(v.Fields) == 0 {
			return errors.New("credential CLI字段无效")
		}
		f := map[string]string{}
		for k, v := range v.Fields {
			if v == nil {
				return errors.New("credential CLI字段无效")
			}
			f[k] = *v
		}
		*c = CredentialInput{Type: v.Type, Fields: f}
	default:
		return errors.New("credential用途无效")
	}
	return nil
}

type Create struct {
	Purpose    string          `json:"purpose"`
	Provider   *string         `json:"provider,omitempty"`
	CLI        *string         `json:"cli,omitempty"`
	Name       string          `json:"name"`
	Credential CredentialInput `json:"credential"`
}
type Rename struct {
	Name             string `json:"name"`
	ExpectedRevision int64  `json:"expectedRevision"`
}
type Replace struct {
	Credential       CredentialInput `json:"credential"`
	ExpectedRevision int64           `json:"expectedRevision"`
}
type SelectionPut struct {
	Purpose               string     `json:"purpose"`
	Provider              *string    `json:"provider,omitempty"`
	CLI                   *string    `json:"cli,omitempty"`
	AccountID             *uuid.UUID `json:"accountId"`
	ExpectedConfigVersion int64      `json:"expectedConfigVersion"`
}

func (s *SelectionPut) UnmarshalJSON(data []byte) error {
	type plain SelectionPut
	var v plain
	if strict(data, &v) != nil {
		return errors.New("账号选择字段无效")
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(data, &fields) != nil {
		return errors.New("账号选择字段无效")
	}
	if _, ok := fields["accountId"]; !ok {
		return errors.New("accountId必须显式提供")
	}
	for _, key := range []string{"purpose", "provider", "cli", "expectedConfigVersion"} {
		if raw, ok := fields[key]; ok && bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return fmt.Errorf("%s不能为null", key)
		}
	}
	if v.AccountID != nil && *v.AccountID == uuid.Nil {
		return errors.New("accountId无效")
	}
	*s = SelectionPut(v)
	return nil
}

type LoginStart struct {
	Provider         string     `json:"provider"`
	Name             *string    `json:"name,omitempty"`
	AccountID        *uuid.UUID `json:"accountId,omitempty"`
	ExpectedRevision *int64     `json:"expectedRevision,omitempty"`
}
type Operation struct {
	ID               uuid.UUID  `json:"id"`
	ProjectID        uuid.UUID  `json:"projectId"`
	Provider         string     `json:"provider"`
	TargetAccountID  *uuid.UUID `json:"targetAccountId"`
	TargetRevision   *int64     `json:"targetRevision"`
	Name             *string    `json:"name"`
	Status           string     `json:"status"`
	AccountID        *uuid.UUID `json:"accountId"`
	StartedAt        time.Time  `json:"startedAt"`
	ExpiresAt        time.Time  `json:"expiresAt"`
	CompletedAt      *time.Time `json:"completedAt"`
	AuthorizationURL *string    `json:"authorizationUrl"`
	ErrorCode        *string    `json:"errorCode"`
	ErrorMessage     *string    `json:"errorMessage"`
}
type LoginState struct {
	Operation Operation `json:"operation"`
	Account   *Account  `json:"account"`
}
type Operations struct {
	Operations []Operation `json:"operations"`
}
type PreconditionDetails struct {
	ResourceKind    string    `json:"resourceKind"`
	ResourceID      uuid.UUID `json:"resourceId"`
	ExpectedVersion int64     `json:"expectedVersion"`
	CurrentVersion  int64     `json:"currentVersion"`
}
type ResolutionDetails struct {
	AgentID             uuid.UUID   `json:"agentId"`
	ProjectID           uuid.UUID   `json:"projectId"`
	Purpose             string      `json:"purpose"`
	Provider            *string     `json:"provider"`
	CLI                 *string     `json:"cli"`
	AccountID           *uuid.UUID  `json:"accountId"`
	CandidateAccountIDs []uuid.UUID `json:"candidateAccountIds"`
}

func (c *Create) UnmarshalJSON(data []byte) error {
	type plain Create
	var v plain
	if strict(data, &v) != nil {
		return errors.New("账号请求字段或格式无效")
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(data, &fields) != nil {
		return errors.New("账号请求格式无效")
	}
	if v.Purpose == "model" {
		if _, ok := fields["cli"]; ok {
			return errors.New("模型账号不能提供cli字段")
		}
	} else if v.Purpose == "cli" {
		if _, ok := fields["provider"]; ok {
			return errors.New("CLI账号不能提供provider字段")
		}
	}
	*c = Create(v)
	return nil
}
func (l *LoginStart) UnmarshalJSON(data []byte) error {
	type plain LoginStart
	var v plain
	if strict(data, &v) != nil {
		return errors.New("登录请求字段或格式无效")
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(data, &fields) != nil {
		return errors.New("登录请求格式无效")
	}
	for _, raw := range fields {
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return errors.New("登录输入字段不能为null")
		}
	}
	*l = LoginStart(v)
	return nil
}
