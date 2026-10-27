// Package probe builds fixed verification commands and parses private native
// output. It performs no IO; the caller owns tool trust, credentials and process
// lifetime. Output must never be logged or included in public operation results.
package probe

import (
	"bytes"
	"encoding/json"
	"io"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/google/uuid"
)

const MaxOutputBytes = 64 << 10

type Request struct {
	CLI           string
	Executable    string
	ConnectionID  uuid.UUID
	ExpectedAppID string
	ExpectedBrand string
}

type Plan struct {
	Executable string
	Args       []string
	ProbeKind  string
}

type Observation struct {
	ExitCode       int
	Stdout, Stderr []byte
}

type Outcome struct {
	Passed     bool
	ErrorCode  string
	HTTPStatus *int
}

type Error struct{ Code string }

func (*Error) Error() string { return "CLI验证配置无效或不受支持" }

func Build(r Request) (Plan, error) {
	if !filepath.IsAbs(r.Executable) {
		return Plan{}, &Error{Code: "configuration_invalid"}
	}
	p := Plan{Executable: r.Executable}
	switch r.CLI {
	case "mysql", "psql":
		if r.ConnectionID == uuid.Nil {
			return Plan{}, &Error{Code: "configuration_invalid"}
		}
		p.ProbeKind = "database_select1"
		p.Args = []string{"--loop-connection-id", r.ConnectionID.String()}
		if r.CLI == "mysql" {
			p.Args = append(p.Args, "--batch", "--skip-column-names", "--skip-auto-rehash", "--connect-timeout=10", "--execute", "SELECT 1")
		} else {
			p.Args = append(p.Args, "-X", "-w", "-v", "ON_ERROR_STOP=1", "-A", "-t", "-c", "SELECT 1")
		}
	case "gh":
		p.ProbeKind = "github_user"
		p.Args = []string{"api", "--hostname", "github.com", "--method", "GET", "--include", "user"}
	case "lark-cli":
		if strings.TrimSpace(r.ExpectedAppID) == "" || (r.ExpectedBrand != "feishu" && r.ExpectedBrand != "lark") {
			return Plan{}, &Error{Code: "configuration_invalid"}
		}
		p.ProbeKind = "lark_bot"
		p.Args = []string{"auth", "status", "--verify", "--json"}
	default:
		return Plan{}, &Error{Code: "tool_unsupported"}
	}
	return p, nil
}

func Parse(r Request, o Observation) Outcome {
	if _, err := Build(r); err != nil {
		return Outcome{ErrorCode: err.(*Error).Code}
	}
	if len(o.Stdout) > MaxOutputBytes || len(o.Stderr) > MaxOutputBytes {
		return failed()
	}
	switch r.CLI {
	case "mysql", "psql":
		if o.ExitCode != 0 {
			return Outcome{ErrorCode: databaseError(r.CLI, o.Stderr)}
		}
		// Accept one native row, optionally terminated by LF/CRLF, not arbitrary
		// whitespace, additional rows, headings or a successful empty response.
		if bytes.Equal(o.Stdout, []byte("1")) || bytes.Equal(o.Stdout, []byte("1\n")) || bytes.Equal(o.Stdout, []byte("1\r\n")) {
			return Outcome{Passed: true}
		}
	case "gh":
		return github(o)
	case "lark-cli":
		if o.ExitCode != 0 {
			return failed()
		}
		var result struct {
			AppID      string `json:"appId"`
			Brand      string `json:"brand"`
			Identity   string `json:"identity"`
			Identities struct {
				Bot struct {
					Verified bool `json:"verified"`
				} `json:"bot"`
				User struct {
					Verified bool `json:"verified"`
				} `json:"user"`
			} `json:"identities"`
		}
		if decode(o.Stdout, &result) && result.AppID == r.ExpectedAppID && result.Brand == r.ExpectedBrand && result.Identity == "bot" && result.Identities.Bot.Verified && !result.Identities.User.Verified {
			return Outcome{Passed: true}
		}
	}
	return failed()
}

func failed() Outcome { return Outcome{ErrorCode: "probe_failed"} }

func decode(b []byte, out any) bool {
	d := json.NewDecoder(bytes.NewReader(b))
	return d.Decode(out) == nil && d.Decode(new(any)) == io.EOF
}

var statusLine = regexp.MustCompile(`^HTTP/(?:1\.[01]|2(?:\.0)?|3(?:\.0)?) ([0-9]{3})(?: [^\r\n]*)?$`)

func github(o Observation) Outcome {
	// gh --include prints the response headers followed by its decoded body.
	// HTTP status comes only from this frame, never from error body/stderr text.
	head, body, ok := bytes.Cut(o.Stdout, []byte("\r\n\r\n"))
	if !ok {
		head, body, ok = bytes.Cut(o.Stdout, []byte("\n\n"))
	}
	if !ok {
		return failed()
	}
	line, _, _ := bytes.Cut(head, []byte("\n"))
	m := statusLine.FindSubmatch(bytes.TrimSuffix(line, []byte("\r")))
	if m == nil {
		return failed()
	}
	status, _ := strconv.Atoi(string(m[1]))
	if status < 100 || status > 599 {
		return failed()
	}
	result := Outcome{ErrorCode: "probe_failed", HTTPStatus: &status}
	switch status {
	case 401:
		result.ErrorCode = "authentication_failed"
	case 403:
		result.ErrorCode = "provider_policy"
	case 429:
		result.ErrorCode = "rate_limited"
	case 200:
		var user struct {
			ID    int64  `json:"id"`
			Login string `json:"login"`
		}
		if o.ExitCode == 0 && decode(body, &user) && user.ID > 0 && githubLogin.MatchString(user.Login) {
			result.Passed, result.ErrorCode = true, ""
		}
	}
	return result
}

var githubLogin = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,37}[A-Za-z0-9])?$`)
var mysqlError = regexp.MustCompile(`(?m)^ERROR ([0-9]{4}) \([A-Z0-9]{5}\):`)
var postgresError = regexp.MustCompile(`(?m)^(?:ERROR|FATAL):[ \t]+([A-Z0-9]{5}):`)

func databaseError(cli string, b []byte) string {
	if cli == "mysql" {
		if m := mysqlError.FindSubmatch(b); m != nil {
			switch string(m[1]) {
			case "1045":
				return "authentication_failed"
			case "1044", "1142":
				return "permission_denied"
			case "1049":
				return "database_unavailable"
			case "2026":
				return "tls_failed"
			case "2002", "2003", "2013":
				return "network_failed"
			}
		}
	} else if m := postgresError.FindSubmatch(b); m != nil {
		switch string(m[1]) {
		case "28P01", "28000":
			return "authentication_failed"
		case "42501":
			return "permission_denied"
		case "3D000":
			return "database_unavailable"
		}
	}
	return "probe_failed"
}
