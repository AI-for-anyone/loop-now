// Package modelcredential defines explicit, non-printable Pi credentials and
// immutable authorization references. It performs no environment or file reads.
package modelcredential

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"io"
	"maps"
	"strings"
	"time"
)

const MaxBytes = 64 * 1024

var ErrInvalid = errors.New("模型凭证格式无效")
var ErrImplicitEncoding = errors.New("模型秘密只能通过显式认证通道编码")

type Binding struct {
	OwnerKind string     `json:"ownerKind,omitempty"`
	ProjectID *uuid.UUID `json:"projectId,omitempty"`
	AccountID *uuid.UUID `json:"accountId,omitempty"`
	VersionID uuid.UUID  `json:"versionId"`
	AgentID   uuid.UUID  `json:"agentId"`
	Provider  string     `json:"provider"`
}

func (b Binding) Valid() bool {
	if b.VersionID == uuid.Nil || b.AgentID == uuid.Nil || b.Provider == "" || strings.ContainsAny(b.Provider, "/\\\x00\r\n\t ") {
		return false
	}
	switch b.OwnerKind {
	case "":
		return b.ProjectID == nil && b.AccountID == nil
	case "agent":
		return b.ProjectID == nil && b.AccountID == nil
	case "project":
		return b.ProjectID != nil && *b.ProjectID != uuid.Nil && b.AccountID != nil && *b.AccountID != uuid.Nil
	default:
		return false
	}
}

func (b Binding) Clone() Binding {
	out := b
	if b.ProjectID != nil {
		id := *b.ProjectID
		out.ProjectID = &id
	}
	if b.AccountID != nil {
		id := *b.AccountID
		out.AccountID = &id
	}
	return out
}

type wire struct {
	Type      string            `json:"type"`
	Key       string            `json:"key,omitempty"`
	Env       map[string]string `json:"env,omitempty"`
	Access    string            `json:"access,omitempty"`
	Refresh   string            `json:"refresh,omitempty"`
	Expires   int64             `json:"expires,omitempty"`
	AccountID string            `json:"accountId,omitempty"`
}

// Credential never serializes or prints implicitly. Encode is the only secret
// serialization path, used by the encrypted store, auth.json and private IPC.
type Credential struct{ value wire }

func APIKey(key string) (Credential, error) { return validated(wire{Type: "api_key", Key: key}) }
func Decode(data []byte) (Credential, error) {
	if len(data) > MaxBytes {
		return Credential{}, ErrInvalid
	}
	var w wire
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if d.Decode(&w) != nil || d.Decode(new(any)) != io.EOF {
		return Credential{}, ErrInvalid
	}
	return validated(w)
}
func validated(w wire) (Credential, error) {
	switch w.Type {
	case "api_key":
		if (strings.TrimSpace(w.Key) == "" && !validLegacyAnthropicEnv(w.Env)) || (w.Key != "" && len(w.Env) > 0) || w.Access != "" || w.Refresh != "" || w.AccountID != "" || w.Expires != 0 {
			return Credential{}, ErrInvalid
		}
	case "oauth":
		if len(w.Env) != 0 || w.Key != "" || w.Access == "" || w.Refresh == "" || w.AccountID == "" || w.Expires <= 0 {
			return Credential{}, ErrInvalid
		}
	default:
		return Credential{}, ErrInvalid
	}
	data, e := json.Marshal(w)
	if e != nil || len(data) > MaxBytes {
		return Credential{}, ErrInvalid
	}
	clear(data)
	w.Env = maps.Clone(w.Env)
	return Credential{value: w}, nil
}
func (c Credential) NeedsLegacyAnthropic() bool { return len(c.value.Env) != 0 }
func (c Credential) Kind() string               { return c.value.Type }
func (c Credential) ExpiresAt() time.Time       { return time.UnixMilli(c.value.Expires) }
func (c Credential) SameAccount(next Credential) bool {
	return c.value.Type == next.value.Type && c.value.AccountID == next.value.AccountID
}
func (c Credential) Encode() ([]byte, error) {
	if _, e := validated(c.value); e != nil {
		return nil, e
	}
	return json.Marshal(c.value)
}
func (c Credential) AuthFile(provider string) ([]byte, error) {
	if (len(c.value.Env) != 0 && provider != "anthropic") || provider == "" || strings.ContainsAny(provider, "/\\\x00\r\n\t ") {
		return nil, ErrInvalid
	}
	data, e := c.Encode()
	if e != nil {
		return nil, e
	}
	defer clear(data)
	return json.Marshal(map[string]json.RawMessage{provider: data})
}
func (Credential) MarshalJSON() ([]byte, error) { return nil, ErrImplicitEncoding }
func (Credential) String() string               { return "modelcredential.Credential<redacted>" }
func (Credential) GoString() string             { return "modelcredential.Credential<redacted>" }
func (Credential) Format(f fmt.State, _ rune) {
	io.WriteString(f, "modelcredential.Credential<redacted>")
}
