// Package clicredential defines internal CLI grant identities and secret codecs.
package clicredential

import (
	"bytes"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"io"
	"strings"
	"unicode"
)

const MaxBytes = 64 << 10

type Binding struct {
	VersionID uuid.UUID `json:"versionId"`
	AgentID   uuid.UUID `json:"agentId"`
	ProjectID uuid.UUID `json:"projectId"`
	AccountID uuid.UUID `json:"accountId"`
	CLI       string    `json:"cli"`
}

func (b Binding) Valid() bool {
	return b.VersionID != uuid.Nil && b.AgentID != uuid.Nil && b.ProjectID != uuid.Nil && b.AccountID != uuid.Nil && b.CLI == "gcloud"
}

type Google struct {
	Type         string   `json:"type"`
	Account      string   `json:"account"`
	ClientID     string   `json:"clientId"`
	ClientSecret string   `json:"clientSecret"`
	RefreshToken string   `json:"refreshToken"`
	AccessToken  string   `json:"accessToken"`
	ExpiresAt    int64    `json:"expiresAt"`
	Scopes       []string `json:"scopes"`
	RAPTToken    *string  `json:"raptToken"`
}

func (Google) String() string               { return "[Google CLI credential]" }
func (Google) GoString() string             { return "[Google CLI credential]" }
func (Google) MarshalJSON() ([]byte, error) { return nil, errors.New("Google秘密仅可显式编码") }
func (c Google) Valid() bool {
	if c.Type != "authorized_user" || c.ExpiresAt <= 0 || c.ExpiresAt > 253402300799999 || len(c.Scopes) == 0 || len(c.Scopes) > 100 {
		return false
	}
	for _, s := range []string{c.Account, c.ClientID, c.ClientSecret, c.RefreshToken, c.AccessToken} {
		if s == "" || strings.IndexFunc(s, unicode.IsControl) >= 0 {
			return false
		}
	}
	for _, s := range c.Scopes {
		if s == "" || len(s) > 2048 || strings.IndexFunc(s, unicode.IsControl) >= 0 {
			return false
		}
	}
	return c.RAPTToken == nil || *c.RAPTToken != "" && strings.IndexFunc(*c.RAPTToken, unicode.IsControl) < 0
}
func (c Google) Encode() ([]byte, error) {
	if !c.Valid() {
		return nil, errors.New("Google授权格式无效")
	}
	type wire Google
	b, e := json.Marshal(wire(c))
	if e != nil || len(b) > MaxBytes {
		return nil, errors.New("Google授权格式无效")
	}
	return b, nil
}
func DecodeGoogle(b []byte) (Google, error) {
	var c Google
	type wire Google
	if len(b) > MaxBytes {
		return c, errors.New("Google授权格式无效")
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if d.Decode((*wire)(&c)) != nil || d.Decode(new(any)) != io.EOF || !c.Valid() {
		return Google{}, errors.New("Google授权格式无效")
	}
	return c, nil
}
func (c Google) Clone() Google {
	c.Scopes = append([]string{}, c.Scopes...)
	if c.RAPTToken != nil {
		v := *c.RAPTToken
		c.RAPTToken = &v
	}
	return c
}

// VerificationGrantBinding is a management-only principal for a persisted
// verification operation. It cannot be substituted for an Agent runtime binding.
type VerificationGrantBinding struct {
	ProjectID   uuid.UUID
	AccountID   uuid.UUID
	VersionID   uuid.UUID
	OperationID uuid.UUID
}

func (b VerificationGrantBinding) Valid() bool {
	return b.ProjectID != uuid.Nil && b.AccountID != uuid.Nil && b.VersionID != uuid.Nil && b.OperationID != uuid.Nil
}
