package credentialmodel

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
)

func TestGitHubStartDecoderPresenceAndLegacyGoogle(t *testing.T) {
	id, account := uuid.NewString(), uuid.NewString()
	base := map[string]any{"operationId": id, "cli": "gh", "authType": "github_user"}
	check := func(v map[string]any, valid bool) {
		t.Helper()
		b, _ := json.Marshal(v)
		var in CLIAuthStart
		err := json.Unmarshal(b, &in)
		if (err == nil) != valid {
			t.Fatalf("decoder valid=%t: %v", valid, err)
		}
	}
	clone := func(v map[string]any) map[string]any {
		out := map[string]any{}
		for k, x := range v {
			out[k] = x
		}
		return out
	}
	check(base, true)
	for _, key := range []string{"name", "accountId", "expectedRevision", "host", "scopes", "credential", "token"} {
		bad := clone(base)
		bad[key] = nil
		check(bad, false)
	}
	for _, key := range []string{"operationId", "cli", "authType"} {
		bad := clone(base)
		bad[key] = nil
		check(bad, false)
		delete(bad, key)
		check(bad, false)
	}
	relogin := clone(base)
	relogin["accountId"], relogin["expectedRevision"] = account, 1
	check(relogin, true)
	for _, key := range []string{"accountId", "expectedRevision"} {
		bad := clone(relogin)
		bad[key] = nil
		check(bad, false)
	}
	bad := clone(relogin)
	bad["name"] = "must not rename"
	check(bad, false)
	check(map[string]any{"operationId": id, "cli": "gcloud", "authType": "google_user", "name": "Google"}, true)
	check(map[string]any{"operationId": id, "cli": "gcloud", "authType": "google_user", "accountId": account, "expectedRevision": 1}, true)
	check(map[string]any{"operationId": id, "cli": "gh", "authType": "google_user"}, false)
}

func TestGitHubCodeNullAndGooglePropertyAbsence(t *testing.T) {
	code := "ABCD-1234"
	for _, c := range []struct {
		cli  string
		code *string
	}{{"gh", nil}, {"gh", &code}, {"gcloud", nil}, {"gcloud", &code}} {
		op := CLIAuthOperation{CLI: c.cli, UserCode: c.code}
		b, err := json.Marshal(op)
		if err != nil {
			t.Fatal(err)
		}
		var fields map[string]json.RawMessage
		if json.Unmarshal(b, &fields) != nil {
			t.Fatal("invalid wire JSON")
		}
		_, present := fields["userCode"]
		if present != (c.cli == "gh") || c.cli == "gh" && c.code == nil && string(fields["userCode"]) != "null" {
			t.Fatal("required null/absent property distinction lost")
		}
		if c.cli == "gh" && c.code != nil {
			var decoded CLIAuthOperation
			if json.Unmarshal(b, &decoded) != nil || decoded.UserCode == nil || *decoded.UserCode != code {
				t.Fatal("userCode response cannot be read back")
			}
		}
	}
}
