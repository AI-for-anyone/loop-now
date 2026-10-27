package probe

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func request(cli string) Request {
	return Request{CLI: cli, Executable: "/fixture/bin/" + cli, ConnectionID: uuid.MustParse("0192f1a2-0000-7000-8000-000000000001"), ExpectedAppID: "synthetic-app", ExpectedBrand: "feishu"}
}

func TestBuildFixedPlans(t *testing.T) {
	id := request("mysql").ConnectionID.String()
	cases := []struct {
		cli, kind string
		args      []string
	}{
		{"mysql", "database_select1", []string{"--loop-connection-id", id, "--batch", "--skip-column-names", "--skip-auto-rehash", "--connect-timeout=10", "--execute", "SELECT 1"}},
		{"psql", "database_select1", []string{"--loop-connection-id", id, "-X", "-w", "-v", "ON_ERROR_STOP=1", "-A", "-t", "-c", "SELECT 1"}},
		{"gh", "github_user", []string{"api", "--hostname", "github.com", "--method", "GET", "--include", "user"}},
		{"lark-cli", "lark_bot", []string{"auth", "status", "--verify", "--json"}},
	}
	for _, c := range cases {
		t.Run(c.cli, func(t *testing.T) {
			r := request(c.cli)
			p, err := Build(r)
			if err != nil || p.Executable != r.Executable || p.ProbeKind != c.kind || !reflect.DeepEqual(p.Args, c.args) {
				t.Fatal("unexpected fixed plan")
			}
			p.Args[0] = "mutated"
			again, _ := Build(r)
			if !reflect.DeepEqual(again.Args, c.args) {
				t.Fatal("shared mutable arguments")
			}
		})
	}
}

func TestBuildRejectsInvalidTargetsSafely(t *testing.T) {
	cases := []Request{request("gcloud"), request("mysql; arbitrary"), request("unknown")}
	r := request("gh")
	r.Executable = "relative"
	cases = append(cases, r)
	r = request("mysql")
	r.ConnectionID = uuid.Nil
	cases = append(cases, r)
	r = request("psql")
	r.ConnectionID = uuid.Nil
	cases = append(cases, r)
	r = request("lark-cli")
	r.ExpectedAppID = ""
	cases = append(cases, r)
	r = request("lark-cli")
	r.ExpectedBrand = "synthetic-secret-custom-endpoint"
	cases = append(cases, r)
	for _, r := range cases {
		p, err := Build(r)
		var safe *Error
		if !errors.As(err, &safe) || safe.Code == "" || p.Executable != "" || strings.Contains(err.Error(), "synthetic-secret") {
			t.Fatal("invalid request not safely rejected")
		}
		if Parse(r, Observation{Stdout: []byte("1\n")}).Passed {
			t.Fatal("parse accepted invalid target")
		}
	}
}

func frame(status, body string) []byte {
	return []byte("HTTP/2.0 " + status + "\r\nContent-Type: application/json\r\n\r\n" + body)
}

func TestParseFourSuccessDepths(t *testing.T) {
	cases := []struct {
		cli    string
		stdout []byte
	}{
		{"mysql", []byte("1\n")}, {"psql", []byte("1\r\n")},
		{"gh", frame("200 OK", `{"id":123,"login":"fixture-user","extra":"ignored"}`)},
		{"lark-cli", []byte(`{"appId":"synthetic-app","brand":"feishu","identity":"bot","identities":{"bot":{"verified":true}}}`)},
	}
	for _, c := range cases {
		t.Run(c.cli, func(t *testing.T) {
			o := Parse(request(c.cli), Observation{Stdout: c.stdout, Stderr: []byte("synthetic-secret warning")})
			if !o.Passed || o.ErrorCode != "" {
				t.Fatal("valid fixed result rejected")
			}
			if c.cli == "gh" && (o.HTTPStatus == nil || *o.HTTPStatus != 200) {
				t.Fatal("HTTP status missing")
			}
		})
	}
}

func TestDatabaseRequiresExactSingleRowAndExitZero(t *testing.T) {
	for _, cli := range []string{"mysql", "psql"} {
		for _, bad := range []string{"", " ", "1\n1\n", "1\n\n", " 1\n", "1 \n", "column\n1\n", "1\nsynthetic-secret", "0\n"} {
			if Parse(request(cli), Observation{Stdout: []byte(bad)}).Passed {
				t.Fatal("noncanonical row accepted")
			}
		}
		if Parse(request(cli), Observation{ExitCode: 1, Stdout: []byte("1\n")}).Passed {
			t.Fatal("failed client accepted")
		}
	}
}

func TestGithubRequiresHTTPAndUserShape(t *testing.T) {
	for _, body := range []string{`{}`, `[]`, `null`, `{"id":0,"login":"fixture"}`, `{"id":1.5,"login":"fixture"}`, `{"id":1,"login":""}`, `{"id":1,"login":"bad/name"}`, `{"id":1,"login":"fixture"} {}`, `not-json`} {
		if Parse(request("gh"), Observation{Stdout: frame("200 OK", body)}).Passed {
			t.Fatal("invalid user response accepted")
		}
	}
	for _, out := range [][]byte{[]byte(`{"id":123,"login":"fixture"}`), frame("999 invalid", `{}`), frame("020 invalid", `{}`)} {
		if Parse(request("gh"), Observation{Stdout: out}).Passed {
			t.Fatal("missing or invalid status accepted")
		}
	}
	if Parse(request("gh"), Observation{ExitCode: 1, Stdout: frame("200 OK", `{"id":123,"login":"fixture"}`)}).Passed {
		t.Fatal("failed gh accepted")
	}
	for _, c := range []struct {
		status, code string
		value        int
	}{{"401 Unauthorized", "authentication_failed", 401}, {"403 Forbidden", "provider_policy", 403}, {"429 Too Many Requests", "rate_limited", 429}, {"500 Error", "probe_failed", 500}} {
		o := Parse(request("gh"), Observation{ExitCode: 1, Stdout: frame(c.status, `{"message":"synthetic-secret"}`)})
		if o.Passed || o.ErrorCode != c.code || o.HTTPStatus == nil || *o.HTTPStatus != c.value {
			t.Fatal("unsafe or incorrect HTTP classification")
		}
	}
	// stderr claims cannot supply a missing HTTP frame.
	o := Parse(request("gh"), Observation{ExitCode: 1, Stderr: []byte("HTTP 401 synthetic-secret")})
	if o.HTTPStatus != nil || o.ErrorCode != "probe_failed" {
		t.Fatal("guessed HTTP status from stderr")
	}
}

func TestLarkMatchesCapturedApplicationNotUserOrHost(t *testing.T) {
	// These are contract fixtures, not evidence that the native CLI emits every
	// required field. Missing native identity evidence must fail closed.
	good := `{"appId":"synthetic-app","brand":"feishu","identity":"bot","identities":{"bot":{"verified":true}}}`
	cases := []string{
		strings.Replace(good, "synthetic-app", "other-app", 1), strings.Replace(good, "feishu", "lark", 1),
		strings.Replace(good, `"identity":"bot"`, `"identity":"user"`, 1), strings.Replace(good, "true", "false", 1),
		`{"identity":"bot","verified":true}`, `{"identities":{"bot":{"verified":true}}}`, `null`, `[]`, good + `{}`,
		`{"appId":"synthetic-app","brand":"feishu","identity":"bot","identities":{"bot":{"verified":true},"user":{"verified":true}}}`,
	}
	for _, out := range cases {
		if Parse(request("lark-cli"), Observation{Stdout: []byte(out)}).Passed {
			t.Fatal("unbound or invalid lark identity accepted")
		}
	}
	if Parse(request("lark-cli"), Observation{ExitCode: 1, Stdout: []byte(good)}).Passed {
		t.Fatal("failed lark process accepted")
	}
}

func TestKnownNativeErrorsAndNoRawOutput(t *testing.T) {
	cases := []struct{ cli, stderr, code string }{
		{"mysql", "ERROR 1045 (28000): synthetic-secret", "authentication_failed"},
		{"mysql", "ERROR 1049 (42000): synthetic-secret", "database_unavailable"},
		{"mysql", "ERROR 2026 (HY000): synthetic-secret", "tls_failed"},
		{"mysql", "ERROR 2003 (HY000): synthetic-secret", "network_failed"},
		{"psql", "FATAL:  28P01: synthetic-secret", "authentication_failed"},
		{"psql", "ERROR:  42501: synthetic-secret", "permission_denied"},
		{"psql", "FATAL:  3D000: synthetic-secret", "database_unavailable"},
		{"psql", "password failed synthetic-secret", "probe_failed"},
	}
	for _, c := range cases {
		o := Parse(request(c.cli), Observation{ExitCode: 1, Stderr: []byte(c.stderr)})
		b, _ := json.Marshal(o)
		if o.Passed || o.ErrorCode != c.code || bytes.Contains(b, []byte("synthetic-secret")) {
			t.Fatal("incorrect or secret-bearing outcome")
		}
	}
}

func TestOutputLimitsBothStreams(t *testing.T) {
	for _, cli := range []string{"gh", "lark-cli", "mysql", "psql"} {
		for _, o := range []Observation{{Stdout: bytes.Repeat([]byte("x"), MaxOutputBytes+1)}, {Stdout: []byte("1\n"), Stderr: bytes.Repeat([]byte("x"), MaxOutputBytes+1)}} {
			if got := Parse(request(cli), o); got.Passed || got.ErrorCode != "probe_failed" || got.HTTPStatus != nil {
				t.Fatal("oversized stream accepted")
			}
		}
	}
}
