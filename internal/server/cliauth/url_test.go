package cliauth

import (
	"net/url"
	"testing"
)

func TestGoogleAuthorizationURL(t *testing.T) {
	q := url.Values{"client_id": {"synthetic-client"}, "response_type": {"code"}, "redirect_uri": {"http://localhost:8085/"}, "scope": {"https://www.googleapis.com/auth/cloud-platform"}, "state": {"synthetic-state"}, "code_challenge": {"synthetic-challenge"}, "code_challenge_method": {"S256"}, "access_type": {"offline"}, "prompt": {"consent"}}
	good := "https://accounts.google.com/o/oauth2/auth?" + q.Encode()
	if !ValidAuthorizationURL(good) {
		t.Fatal("official shape rejected")
	}
	for _, suffix := range []string{"&code=secret", "&id_token=secret", "&code_verifier=secret", "&state=duplicate", "&access_token=secret", "&unknown=1", "#fragment"} {
		if ValidAuthorizationURL(good + suffix) {
			t.Fatal("unsafe URL accepted")
		}
	}
	for _, uri := range []string{"https://localhost:8085/", "http://localhost:1455/", "http://external.example:8085/", "http://localhost:8085/other"} {
		copy := url.Values{}
		for k, v := range q {
			copy[k] = append([]string{}, v...)
		}
		copy.Set("redirect_uri", uri)
		if ValidAuthorizationURL("https://accounts.google.com/o/oauth2/auth?" + copy.Encode()) {
			t.Fatal("unbound redirect accepted")
		}
	}
}
