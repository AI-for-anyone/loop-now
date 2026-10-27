package credentialmodel

import (
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// GitHubIdentity is verified public metadata belonging to one credential version.
// Nil GrantedScopes means unknown; an allocated empty slice means known empty.
type GitHubIdentity struct {
	Host          string    `json:"host"`
	UserID        string    `json:"userId"`
	Login         string    `json:"login"`
	GrantedScopes []string  `json:"grantedScopes"`
	VerifiedAt    time.Time `json:"verifiedAt"`
}

var githubUserID = regexp.MustCompile(`^[1-9][0-9]*$`)

func (i GitHubIdentity) Valid() bool {
	if i.Host != "github.com" || !githubUserID.MatchString(i.UserID) || i.VerifiedAt.IsZero() || i.Login == "" || len(i.Login) > 200 || !utf8.ValidString(i.Login) || strings.IndexFunc(i.Login, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 {
		return false
	}
	seen := map[string]bool{}
	for _, scope := range i.GrantedScopes {
		if scope == "" || len(scope) > 200 || !utf8.ValidString(scope) || seen[scope] || strings.IndexFunc(scope, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 {
			return false
		}
		seen[scope] = true
	}
	return true
}
