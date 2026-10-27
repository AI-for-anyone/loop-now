// Package dbcli implements execution-scoped native DB client configuration.
// It never opens the platform database or resolves current project accounts.
package dbcli

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"net"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

type Fields struct {
	Host       string `json:"host"`
	Port       string `json:"port"`
	User       string `json:"user"`
	Password   string `json:"password"`
	Database   string `json:"database"`
	SSLMode    string `json:"sslMode"`
	CACert     string `json:"caCert"`
	ClientCert string `json:"clientCert"`
	ClientKey  string `json:"clientKey"`
}
type Connection struct {
	ProjectID *uuid.UUID `json:"projectId"`
	AccountID uuid.UUID  `json:"accountId"`
	Name      string     `json:"name"`
	VersionID uuid.UUID  `json:"versionId"`
	Fields    Fields     `json:"fields"`
}
type Info struct {
	AccountID uuid.UUID `json:"accountId"`
	Name      string    `json:"name"`
	VersionID uuid.UUID `json:"versionId"`
}

func (Fields) String() string       { return "[database credentials]" }
func (Fields) GoString() string     { return "[database credentials]" }
func (Connection) String() string   { return "[database connection]" }
func (Connection) GoString() string { return "[database connection]" }
func validText(s string) bool {
	return utf8.ValidString(s) && strings.IndexFunc(s, unicode.IsControl) < 0
}
func Validate(cli string, f Fields) error {
	fail := errors.New("数据库连接字段或TLS配置无效")
	if cli != "psql" && cli != "mysql" {
		return fail
	}
	for _, s := range []string{f.Host, f.Port, f.User, f.Password, f.Database} {
		if s == "" || !validText(s) {
			return fail
		}
	}
	if strings.TrimSpace(f.Host) != f.Host || strings.ContainsAny(f.Host, "/\\,= \t") || strings.HasPrefix(f.Host, "-") {
		return fail
	}
	if net.ParseIP(f.Host) == nil && strings.Contains(f.Host, ":") {
		return fail
	}
	for _, s := range []string{f.User, f.Database} {
		if strings.TrimSpace(s) != s {
			return fail
		}
	}
	port, e := strconv.ParseUint(f.Port, 10, 16)
	if e != nil || port == 0 || strconv.FormatUint(port, 10) != f.Port {
		return fail
	}
	verify := false
	switch cli {
	case "psql":
		switch f.SSLMode {
		case "disable", "require":
		case "verify-ca", "verify-full":
			verify = true
		default:
			return fail
		}
	case "mysql":
		switch f.SSLMode {
		case "DISABLED", "REQUIRED":
		case "VERIFY_CA", "VERIFY_IDENTITY":
			verify = true
		default:
			return fail
		}
	}
	if verify && f.CACert == "" {
		return fail
	}
	if f.CACert != "" {
		roots := x509.NewCertPool()
		if !roots.AppendCertsFromPEM([]byte(f.CACert)) {
			return fail
		}
	}
	if (f.ClientCert == "") != (f.ClientKey == "") {
		return fail
	}
	if f.ClientCert != "" {
		if _, e := tls.X509KeyPair([]byte(f.ClientCert), []byte(f.ClientKey)); e != nil {
			return fail
		}
	}
	if (f.SSLMode == "disable" || f.SSLMode == "DISABLED") && (f.CACert != "" || f.ClientCert != "") {
		return fail
	}
	return nil
}
func ParseFields(cli string, values map[string]string) (Fields, error) {
	b, e := json.Marshal(values)
	if e != nil {
		return Fields{}, errors.New("数据库字段无效")
	}
	var f Fields
	d := json.NewDecoder(strings.NewReader(string(b)))
	d.DisallowUnknownFields()
	if d.Decode(&f) != nil {
		return Fields{}, errors.New("数据库字段无效")
	}
	if f.Port == "" {
		if cli == "psql" {
			f.Port = "5432"
		} else {
			f.Port = "3306"
		}
	}
	if f.SSLMode == "" {
		if cli == "psql" {
			f.SSLMode = "verify-full"
		} else {
			f.SSLMode = "VERIFY_IDENTITY"
		}
	}
	return f, Validate(cli, f)
}
func ConfigPath(cli string) string { return ".loop-now/db/" + cli + "/connections.json" }
func InputPath(cli string) string  { return ".loop-now/db/" + cli + "/credential.json" }
