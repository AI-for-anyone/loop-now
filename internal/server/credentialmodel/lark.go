package credentialmodel

import (
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// LarkIdentity is verified, version-bound public metadata, never a credential.
// Nil scopes means unknown; an allocated empty list means known empty.
type LarkIdentity struct {
	Brand         string    `json:"brand"`
	AppID         string    `json:"appId"`
	OpenID        string    `json:"openId"`
	DisplayName   string    `json:"displayName"`
	GrantedScopes []string  `json:"grantedScopes"`
	VerifiedAt    time.Time `json:"verifiedAt"`
}

type LarkApplication struct {
	Brand string `json:"brand"`
	AppID string `json:"appId"`
}

func (i LarkIdentity) Valid() bool {
	if (i.Brand != "feishu" && i.Brand != "lark") || !larkText(i.AppID, 200) || !larkText(i.OpenID, 200) || strings.IndexFunc(i.AppID+i.OpenID, unicode.IsSpace) >= 0 || !larkText(i.DisplayName, 200) || i.VerifiedAt.IsZero() || len(i.GrantedScopes) > 100 {
		return false
	}
	seen := map[string]bool{}
	for _, s := range i.GrantedScopes {
		if !larkText(s, 200) || strings.IndexFunc(s, unicode.IsSpace) >= 0 || seen[s] {
			return false
		}
		seen[s] = true
	}
	return true
}
func larkText(s string, max int) bool {
	return s != "" && utf8.ValidString(s) && utf8.RuneCountInString(s) <= max && strings.TrimSpace(s) == s && strings.IndexFunc(s, unicode.IsControl) < 0
}
func (i LarkIdentity) Clone() LarkIdentity {
	if i.GrantedScopes != nil {
		i.GrantedScopes = append([]string{}, i.GrantedScopes...)
	}
	return i
}
