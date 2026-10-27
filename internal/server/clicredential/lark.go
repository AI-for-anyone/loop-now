package clicredential

import (
	"bytes"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"io"
	cm "loop-now/internal/server/credentialmodel"
	"strings"
	"unicode"
	"unicode/utf8"
)

const LarkUserVariant = "lark-cli-user-1.0.94-loop-authsidecar.1"

// LarkBinding is an internal fixed grant identity. It is not a token endpoint
// input, and it never follows an account's current pointer.
type LarkBinding struct {
	ProjectID             uuid.UUID `json:"projectId"`
	AccountID             uuid.UUID `json:"accountId"`
	VersionID             uuid.UUID `json:"versionId"`
	AgentID               uuid.UUID `json:"agentId"`
	Brand                 string    `json:"brand"`
	AppID                 string    `json:"appId"`
	OpenID                string    `json:"openId"`
	RuntimeVariantVersion string    `json:"runtimeVariantVersion"`
}

func (b LarkBinding) Valid() bool {
	return b.ProjectID != uuid.Nil && b.AccountID != uuid.Nil && b.VersionID != uuid.Nil && b.AgentID != uuid.Nil && (b.Brand == "feishu" || b.Brand == "lark") && larkSecret(b.AppID) && larkSecret(b.OpenID) && len(b.AppID) <= 200 && len(b.OpenID) <= 200 && utf8.ValidString(b.AppID+b.OpenID) && b.RuntimeVariantVersion == LarkUserVariant
}

type Lark struct {
	Type                  string          `json:"type"`
	AppSecret             string          `json:"appSecret"`
	AccessToken           string          `json:"accessToken"`
	RefreshToken          string          `json:"refreshToken"`
	ExpiresAt             int64           `json:"expiresAt"`
	RefreshExpiresAt      int64           `json:"refreshExpiresAt"`
	Identity              cm.LarkIdentity `json:"identity"`
	RuntimeVariantVersion string          `json:"runtimeVariantVersion"`
}

func (Lark) String() string               { return "[Lark CLI credential]" }
func (Lark) GoString() string             { return "[Lark CLI credential]" }
func (Lark) MarshalJSON() ([]byte, error) { return nil, errors.New("Lark秘密仅可显式编码") }
func larkSecret(s string) bool {
	return s != "" && len(s) <= 8192 && strings.IndexFunc(s, unicode.IsSpace) < 0 && strings.IndexFunc(s, unicode.IsControl) < 0
}
func (c Lark) Valid() bool {
	return c.Type == "lark_user" && c.Identity.Valid() && larkSecret(c.AppSecret) && larkSecret(c.AccessToken) && larkSecret(c.RefreshToken) && c.ExpiresAt > 0 && c.ExpiresAt <= 253402300799999 && c.RefreshExpiresAt > 0 && c.RefreshExpiresAt <= 253402300799999 && c.RuntimeVariantVersion == LarkUserVariant
}
func (c Lark) Encode() ([]byte, error) {
	if !c.Valid() {
		return nil, errors.New("Lark授权格式无效")
	}
	type wire Lark
	b, e := json.Marshal(wire(c))
	if e != nil || len(b) > MaxBytes {
		return nil, errors.New("Lark授权格式无效")
	}
	return b, nil
}
func DecodeLark(b []byte) (Lark, error) {
	var c Lark
	type wire Lark
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if len(b) > MaxBytes || d.Decode((*wire)(&c)) != nil || d.Decode(new(any)) != io.EOF || !c.Valid() {
		return Lark{}, errors.New("Lark授权格式无效")
	}
	return c, nil
}
func (c Lark) Clone() Lark { c.Identity = c.Identity.Clone(); return c }
func (c Lark) Matches(b LarkBinding) bool {
	return b.Valid() && c.Identity.Brand == b.Brand && c.Identity.AppID == b.AppID && c.Identity.OpenID == b.OpenID && c.RuntimeVariantVersion == b.RuntimeVariantVersion
}
