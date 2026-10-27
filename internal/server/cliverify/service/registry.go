package service

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"loop-now/internal/server/apperr"
	"loop-now/internal/server/clitemplate"
	"loop-now/internal/server/cliverify"
	"loop-now/internal/server/store"
	"regexp"
	"strings"
)

type descriptor struct {
	Template                    *clitemplate.Template
	Fingerprint, Kind, Revision string
}
type Registry struct{ trusted map[string]descriptor }

func fingerprint(t *clitemplate.Template) (string, error) {
	b, e := json.Marshal(t)
	if e != nil {
		return "", errors.New("模板指纹无效")
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:]), nil
}
func NewRegistry() (*Registry, error) {
	catalog, e := clitemplate.Load("")
	if e != nil {
		return nil, e
	}
	r := &Registry{trusted: map[string]descriptor{}}
	for cli, kind := range map[string]string{"gh": "github_user", "lark-cli": "lark_bot", "gcloud": "google_crm_read", "mysql": "database_select1", "psql": "database_select1"} {
		t, ok := catalog.Get(cli)
		if !ok {
			return nil, errors.New("缺少固定验证模板")
		}
		h, e := fingerprint(t)
		if e != nil {
			return nil, e
		}
		r.trusted[cli] = descriptor{t, h, kind, kind + "-v1"}
	}
	return r, nil
}
func unsupported(cli, source, reason string) error {
	e := apperr.Invalid("accountId", "该CLI模板或工具版本不支持验证")
	e.Code = apperr.CodeVerificationUnsupported
	e.Details = cliverify.UnsupportedDetails{CLI: cli, TemplateSource: source, Reason: reason}
	return e
}
func (r *Registry) lookup(catalog *clitemplate.Catalog, record store.ProjectCredentialRecord) (descriptor, error) {
	if record.View.Purpose != store.PurposeCLI || record.View.CLI == nil || record.CLI == nil {
		return descriptor{}, apperr.Invalid("accountId", "须为已保存CLI账号")
	}
	cli := *record.View.CLI
	d, ok := r.trusted[cli]
	if !ok {
		return d, unsupported(cli, record.TemplateSource, "adapter_unavailable")
	}
	loaded, ok := catalog.Get(cli)
	if !ok {
		return d, unsupported(cli, record.TemplateSource, "template_unavailable")
	}
	actual, e := fingerprint(loaded)
	if e != nil || record.TemplateSource != "builtin" || actual != d.Fingerprint {
		return d, unsupported(cli, record.TemplateSource, "template_fingerprint_mismatch")
	}
	if loaded.CredentialFormat() != record.CLI.TemplateVersion {
		return d, unsupported(cli, record.TemplateSource, "template_unavailable")
	}
	if _, ok = loaded.Asset(); !ok {
		return d, unsupported(cli, record.TemplateSource, "platform_unsupported")
	}
	return d, nil
}

var versionPatterns = map[string]*regexp.Regexp{
	"gh":       regexp.MustCompile(`(?m)^gh version ([0-9]+\.[0-9]+\.[0-9]+)(?:\s|$)`),
	"lark-cli": regexp.MustCompile(`(?mi)^lark-cli(?: version)?:?\s+v?([0-9]+\.[0-9]+\.[0-9]+)(?:\s|$)`),
	"mysql":    regexp.MustCompile(`(?mi)^(?:mysql|/[^\r\n]*/mysql)\s+(?:Ver\s+)?([0-9]+\.[0-9]+\.[0-9]+)(?:\s|$)`),
	"psql":     regexp.MustCompile(`(?m)^psql \(PostgreSQL\) ([0-9]+\.[0-9]+)(?:\s|$)`),
	"gcloud":   regexp.MustCompile(`(?m)^Google Cloud SDK ([0-9]+\.[0-9]+\.[0-9]+)(?:\s|$)`),
}

func actualVersion(cli string, out []byte) (string, bool) {
	p, ok := versionPatterns[cli]
	if !ok {
		return "", false
	}
	m := p.FindSubmatch(out)
	if m == nil {
		return "", false
	}
	return strings.TrimSpace(string(m[1])), true
}
