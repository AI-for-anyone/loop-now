package source

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func ghFixture() string {
	return "github.com:\n    user: alice\n    oauth_token: TOP_LEVEL_MUST_NOT_SUBSTITUTE\n    git_protocol: https\n    users:\n        alice:\n            oauth_token: SELECTED_ALICE_TOKEN\n        bob:\n            oauth_token: NONSELECTED_BOB_TOKEN\n        carol:\n"
}
func TestGHExactPlaintextIdentityAndPrivateMapping(t *testing.T) {
	home, r := fixture(t)
	body := ghFixture()
	put(t, home, ghHostsFile, body)
	all, e := r.Discover(context.Background())
	if e != nil || len(all) != 3 {
		t.Fatal("expected distinct GH users")
	}
	var alice, bob, carol Candidate
	for _, c := range all {
		switch c.Label {
		case "github.com / alice":
			alice = c
		case "github.com / bob":
			bob = c
		case "github.com / carol":
			carol = c
		}
	}
	for _, c := range all {
		raw, _ := json.Marshal(c)
		if strings.Contains(string(raw), "TOKEN") || strings.Contains(string(raw), home) {
			t.Fatal("safe discovery leaked token/path")
		}
	}
	if carol.Reason != "keyring_version_unbound" {
		t.Fatal("missing plaintext incorrectly inherited active token")
	}
	if _, e = r.Select(carol.ID); e == nil {
		t.Fatal("unbound keyring accepted")
	}
	s := selection(t, r, alice)
	calls := 0
	var retained map[string]string
	e = r.Extract(context.Background(), s, Supplement{}, func(fields map[string]string) error {
		calls++
		if len(fields) != 1 || fields["token"] != "SELECTED_ALICE_TOKEN" {
			t.Fatal("selected host/user token wrong")
		}
		retained = fields
		return nil
	})
	if e != nil || calls != 1 || len(retained) != 0 {
		t.Fatal("GH private renderer/callback cleanup failed")
	}
	s = selection(t, r, bob)
	if e = r.Extract(context.Background(), s, Supplement{}, func(f map[string]string) error {
		if f["token"] != "NONSELECTED_BOB_TOKEN" {
			t.Fatal("nonactive user's own token missing")
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	// Discovery returns no token projection; selected parse projects one only.
	metadata, token, e := parseGH([]byte(body), "")
	if e != nil || token != "" || strings.Contains(fmt.Sprint(metadata), "TOKEN") {
		t.Fatal("discovery projected secret")
	}
	_, token, e = parseGH([]byte(body), "github.com\x00alice")
	if e != nil || token != "SELECTED_ALICE_TOKEN" {
		t.Fatal("private selection mismatch")
	}
}
func TestGHLegacyHostAndUnsupportedBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name, body, reason string
		wantToken          bool
	}{
		{"legacy", "github.com:\n  user: legacy\n  oauth_token: LEGACY_TOKEN\n", "", true},
		{"no_identity", "github.com:\n  oauth_token: DO_NOT_GUESS_USER\n", "missing_identity", false},
		{"keyring_only", "github.com:\n  user: secure\n  users:\n    secure:\n", "keyring_version_unbound", false},
		{"enterprise", "example.enterprise:\n  user: enterprise\n  oauth_token: NEVER_MAP_ENTERPRISE_TO_GH_TOKEN\n", "host_not_supported", false},
		{"api_mapping", "github.com:\n  user: alice\n  api_host: proxy.example\n  oauth_token: NEVER_REMAP_ENDPOINT\n", "unsupported_endpoint_mapping", false},
		{"empty_users", "github.com:\n  user: alice\n  oauth_token: NEVER_GUESS_ACTIVE_FALLBACK\n  users: {}\n", "unbound_identity", false},
		{"no_top_fallback", "github.com:\n  user: alice\n  oauth_token: NEVER_GUESS_ACTIVE_FALLBACK\n  users:\n    alice:\n", "keyring_version_unbound", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home, r := fixture(t)
			put(t, home, ghHostsFile, tc.body)
			c := candidate(t, r, "gh")
			if c.Reason != tc.reason {
				t.Fatal("wrong safe support reason")
			}
			if tc.wantToken {
				s := selection(t, r, c)
				if e := r.Extract(context.Background(), s, Supplement{}, func(f map[string]string) error {
					if f["token"] != "LEGACY_TOKEN" {
						t.Fatal("legacy identity mismatched")
					}
					return nil
				}); e != nil {
					t.Fatal(e)
				}
			} else if _, e := r.Select(c.ID); e == nil {
				t.Fatal("unsupported identity selected")
			}
		})
	}
}
func TestGHRejectsYAMLGraphsTypesAndRawDiagnostics(t *testing.T) {
	for i, body := range []string{
		"github.com:\n  user: alice\n  user: bob\n  oauth_token: SECRET_DUPLICATE\n",
		"github.com:\n  user: !secret SECRET_TAG_VALUE\n  oauth_token: unused\n",
		"github.com:\n  oauth_token: &token SECRET_ANCHOR_VALUE\n  user: *token\n",
		"github.com:\n  user: !!str alice\n  oauth_token: secret\n",
		"github.com:\n  user: 123\n  oauth_token: secret\n",
		"github.com:\n  user: alice\n  users:\n    alice:\n      oauth_token: a\n      oauth_token: SECRET_DUPLICATE\n",
		"github.com:\n  user: alice\n  oauth_token: [SECRET_PARSE_FAILURE\n",
		"github.com:\n  user: alice\n  oauth_token: secret\n---\ngithub.com: {}\n",
		"github.com:\n  user: alice\n  credential_process: DO_NOT_EXECUTE\n",
		"github.com:\n  user: alice\n  oauth_token: {source: command}\n  users:\n    alice:\n      oauth_token: secret\n",
	} {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			home, r := fixture(t)
			put(t, home, ghHostsFile, body)
			c := candidate(t, r, "gh")
			if c.Reason != "unsupported" || strings.Contains(c.Label, "SECRET") {
				t.Fatal("unsafe YAML accepted or interpolated")
			}
			if _, e := r.Select(c.ID); e == nil || strings.Contains(e.Error(), "SECRET") {
				t.Fatal("raw parser diagnostics leaked")
			}
		})
	}
}
func TestGHSourceChangePermissionsAndSupplementRejection(t *testing.T) {
	home, r := fixture(t)
	put(t, home, ghHostsFile, ghFixture())
	c := candidate(t, r, "gh")
	s := selection(t, r, c)
	e := r.Extract(context.Background(), s, Supplement{Database: "not-a-db"}, func(map[string]string) error { t.Fatal("GH accepted supplement"); return nil })
	expectCode(t, e, "supplement_override")
	put(t, home, ghHostsFile, ghFixture()+"# changed\n")
	expectCode(t, r.Recheck(context.Background(), s), "source_changed")
	c = candidate(t, r, "gh")
	s = selection(t, r, c)
	if e = os.Chmod(filepath.Join(home, ghHostsFile), 0644); e != nil {
		t.Fatal(e)
	}
	c = candidate(t, r, "gh")
	s = selection(t, r, c)
	e = r.Extract(context.Background(), s, Supplement{}, func(map[string]string) error { t.Fatal("insecure plaintext token consumed"); return nil })
	expectCode(t, e, "source_unreadable")
}
func larkConfigFixture() string {
	return `{"strictMode":"off","currentApp":"first","apps":[{"name":"first","appId":"cli_alpha","appSecret":"SELECTED_ALPHA_SECRET","brand":"feishu","defaultAs":"user","users":[{"userOpenId":"ou_synthetic","userName":"DO_NOT_EMIT_USER_NAME"}]},{"name":"second","appId":"cli_beta","appSecret":"NONSELECTED_BETA_SECRET","brand":"lark","defaultAs":"bot","users":[]}]}`
}
func TestLarkPlainBotIndependentOfUserState(t *testing.T) {
	home, r := fixture(t)
	body := larkConfigFixture()
	put(t, home, larkPlainConfig, body)
	all, e := r.Discover(context.Background())
	if e != nil || len(all) != 3 {
		t.Fatal("bot and user source must remain distinct")
	}
	var alpha, beta Candidate
	userCount := 0
	for _, c := range all {
		raw, _ := json.Marshal(c)
		for _, needle := range []string{"SECRET", "DO_NOT_EMIT_USER_NAME", home} {
			if strings.Contains(string(raw), needle) {
				t.Fatal("Lark ordinary metadata leaked private data")
			}
		}
		if c.SourceKind == "lark_user" {
			userCount++
			if c.Reason != "user_identity_unsupported" {
				t.Fatal("user OAuth disguised as bot")
			}
			if _, e := r.Select(c.ID); e == nil {
				t.Fatal("user login selected")
			}
		}
		if c.Label == "Lark 应用/Bot cli_alpha" {
			alpha = c
		}
		if c.Label == "Lark 应用/Bot cli_beta" {
			beta = c
		}
	}
	if userCount != 1 || alpha.Reason != "" || beta.Reason != "" {
		t.Fatal("complete app credential blocked just because users exist")
	}
	for _, tc := range []struct {
		c                 Candidate
		id, secret, brand string
	}{{alpha, "cli_alpha", "SELECTED_ALPHA_SECRET", "feishu"}, {beta, "cli_beta", "NONSELECTED_BETA_SECRET", "lark"}} {
		s := selection(t, r, tc.c)
		if e = r.Extract(context.Background(), s, Supplement{}, func(f map[string]string) error {
			if len(f) != 3 || f["appId"] != tc.id || f["appSecret"] != tc.secret || f["brand"] != tc.brand {
				t.Fatal("complete selected Bot mapping mismatch")
			}
			return nil
		}); e != nil {
			t.Fatal(e)
		}
	}
	metadata, secret, e := parseLarkPass([]byte(body), -1)
	if e != nil || secret != "" || strings.Contains(fmt.Sprint(metadata), "SECRET") {
		t.Fatal("Lark discovery cached secret")
	}
}
func TestLarkReferencesMissingFieldsAndUserTokensRefused(t *testing.T) {
	for _, tc := range []struct{ name, app, reason string }{
		{"keyring", `{"appId":"cli_alpha","appSecret":{"source":"keychain","id":"PRIVATE_REF_NOT_PUBLIC"},"brand":"feishu"}`, "keyring_version_unbound"},
		{"file", `{"appId":"cli_alpha","appSecret":{"source":"file","id":"/HOST_PATH_MUST_NOT_READ"},"brand":"feishu"}`, "file_secret_not_supported"},
		{"dynamic", `{"appId":"cli_alpha","appSecret":{"source":"command","id":"NEVER_EXECUTE"},"brand":"feishu"}`, "unsupported_secret_reference"},
		{"missing_secret", `{"appId":"cli_alpha","brand":"feishu"}`, "credential_unavailable"},
		{"missing_brand", `{"appId":"cli_alpha","appSecret":"SECRET"}`, "unsupported_brand"},
		{"unknown_brand", `{"appId":"cli_alpha","appSecret":"SECRET","brand":"unexpected"}`, "unsupported_brand"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home, r := fixture(t)
			put(t, home, larkPlainConfig, `{"apps":[`+tc.app+`]}`)
			c := candidate(t, r, "lark-cli")
			if c.Reason != tc.reason {
				t.Fatal("wrong safe reason")
			}
			raw, _ := json.Marshal(c)
			for _, needle := range []string{"PRIVATE_REF", "HOST_PATH", "NEVER_EXECUTE", "SECRET"} {
				if strings.Contains(string(raw), needle) {
					t.Fatal("reference/private input leaked")
				}
			}
			if _, e := r.Select(c.ID); e == nil {
				t.Fatal("nonplaintext/incomplete source selected")
			}
		})
	}
}
func TestLarkRejectsDuplicateUnknownAndMalformedJSON(t *testing.T) {
	for i, body := range []string{
		`{"apps":[{"appId":"cli_alpha","appSecret":"FIRST","appSecret":"SECRET_DUPLICATE","brand":"feishu"}]}`,
		`{"apps":[],"apps":[{"appId":"cli_alpha","appSecret":"SECRET_DUPLICATE","brand":"feishu"}]}`,
		`{"apps":[{"appId":"cli_alpha","appSecret":{"source":"keychain","source":"file","id":"SECRET_REF"},"brand":"feishu"}]}`,
		`{"apps":[{"appId":"cli_alpha","appSecret":"SECRET","brand":"feishu","userToken":"DO_NOT_CONVERT_USER"}]}`,
		`{"apps":[{"appId":{"secret":"DO_NOT_LABEL"},"appSecret":"SECRET","brand":"feishu"}]}`,
		`{"apps":[{"appId":"cli_alpha","appSecret":["SECRET"],"brand":"feishu"}]}`,
		`{"apps":[{"appId":"cli_alpha","appSecret":"SECRET_PARSE_FAILURE"`,
		`{"apps":[{"appId":"cli_alpha","appSecret":"SECRET","brand":"feishu","users":[{"userOpenId":"ou_x","accessToken":"DO_NOT_EXPORT"}]}]}`,
	} {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			home, r := fixture(t)
			put(t, home, larkPlainConfig, body)
			c := candidate(t, r, "lark-cli")
			if c.Reason != "unsupported" || strings.Contains(c.Label, "SECRET") {
				t.Fatal("malformed source accepted/exposed")
			}
			if _, e := r.Select(c.ID); e == nil || strings.Contains(e.Error(), "SECRET") {
				t.Fatal("raw JSON diagnostics leaked")
			}
		})
	}
}
func TestLarkSourceChangePrivateReadAndSupplementRejection(t *testing.T) {
	home, r := fixture(t)
	put(t, home, larkPlainConfig, larkConfigFixture())
	s := selection(t, r, candidate(t, r, "lark-cli"))
	e := r.Extract(context.Background(), s, Supplement{SSLMode: "DISABLED"}, func(map[string]string) error { t.Fatal("non-DB supplement accepted"); return nil })
	expectCode(t, e, "supplement_override")
	put(t, home, larkPlainConfig, larkConfigFixture()+"\n")
	expectCode(t, r.Recheck(context.Background(), s), "source_changed")
	if e = os.Chmod(filepath.Join(home, larkPlainConfig), 0644); e != nil {
		t.Fatal(e)
	}
	s = selection(t, r, candidate(t, r, "lark-cli"))
	e = r.Extract(context.Background(), s, Supplement{}, func(map[string]string) error { t.Fatal("insecure app secret consumed"); return nil })
	expectCode(t, e, "source_unreadable")
}
func TestGHAndLarkPlaintextDiscoveryCannotStartNativeCommands(t *testing.T) {
	// These adapters are pure Go. This test binary is run with network and
	// process creation denied; no native dependency or keyring is needed.
	home, r := fixture(t)
	put(t, home, ghHostsFile, "github.com:\n  user: alice\n  oauth_token: SYNTHETIC_TOKEN\n")
	put(t, home, larkPlainConfig, `{"apps":[{"appSecret":"SYNTHETIC_SECRET","brand":"feishu","appId":"cli_alpha","users":[]}]}`)
	all, e := r.Discover(context.Background())
	if e != nil || len(all) != 2 {
		t.Fatal("both fixed plaintext sources expected")
	}
	for _, c := range all {
		s := selection(t, r, c)
		if e = r.Extract(context.Background(), s, Supplement{}, func(map[string]string) error { return nil }); e != nil {
			t.Fatal(e)
		}
	}
}

func TestGHAndLarkRegistrationPreservesFiveSourceABI(t *testing.T) {
	home, r := fixture(t)
	put(t, home, ".pg_service.conf", "[x]\nhost=offline.invalid\nport=5432\nuser=u\ndbname=db\nsslmode=disable\npassword=SYNTHETIC_PG\n")
	put(t, home, ".my.cnf", "[client]\nhost=offline.invalid\nport=3306\nuser=u\ndatabase=db\nssl-mode=DISABLED\npassword=SYNTHETIC_MYSQL\n")
	_, key := syntheticTLS(t)
	sa := map[string]string{"type": "service_account", "project_id": "synthetic-project", "private_key_id": "synthetic-key", "private_key": key, "client_email": "robot@synthetic-project.iam.gserviceaccount.com", "client_id": "123456", "token_uri": "https://oauth2.googleapis.com/token"}
	raw, e := json.Marshal(sa)
	if e != nil {
		t.Fatal(e)
	}
	put(t, home, ".config/gcloud/application_default_credentials.json", string(raw))
	clear(raw)
	put(t, home, ghHostsFile, "github.com:\n  user: alice\n  oauth_token: SYNTHETIC_GH\n")
	put(t, home, larkPlainConfig, `{"apps":[{"appId":"cli_alpha","appSecret":"SYNTHETIC_LARK","brand":"feishu"}]}`)
	all, e := r.Discover(context.Background())
	if e != nil || len(all) != 5 {
		t.Fatal("new registration changed fixed five-source discovery")
	}
	seen := map[string]bool{}
	for _, c := range all {
		seen[c.CLI] = true
		s := selection(t, r, c)
		if e = r.Extract(context.Background(), s, Supplement{}, func(fields map[string]string) error {
			if len(fields) == 0 {
				t.Fatal("empty private credential")
			}
			return nil
		}); e != nil {
			t.Fatal("existing source ABI regressed")
		}
	}
	for _, cli := range []string{"gh", "lark-cli", "gcloud", "psql", "mysql"} {
		if !seen[cli] {
			t.Fatal("source registration absent")
		}
	}
}
