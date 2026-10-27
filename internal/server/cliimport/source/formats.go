package source

import (
	"bytes"
	"context"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"unicode"
)

type group struct {
	values      map[string]string
	secrets     map[string]bool
	unsupported bool
}

var pgKeys = map[string]string{"host": "host", "port": "port", "user": "user", "dbname": "database", "sslmode": "sslMode", "sslrootcert": "caCert", "sslcert": "clientCert", "sslkey": "clientKey", "password": "password", "passfile": "passfile"}
var myKeys = map[string]string{"host": "host", "port": "port", "user": "user", "database": "database", "ssl-mode": "sslMode", "ssl-ca": "caCert", "ssl-cert": "clientCert", "ssl-key": "clientKey", "password": "password", "protocol": "protocol"}

func safeName(s string) bool {
	return s != "" && !strings.HasPrefix(s, "-") && len(s) <= 120 && strings.IndexFunc(s, func(c rune) bool {
		return !unicode.IsLetter(c) && !unicode.IsDigit(c) && !strings.ContainsRune("._-@", c)
	}) < 0
}

// parseINI projects secret presence only during discovery. It never saves the
// corresponding value in a discovery map. Mixed files still contain secret bytes.
func parseINI(b []byte, mysql, private bool, selected ...string) (map[string]*group, error) {
	groups := map[string]*group{}
	var current *group
	currentName := ""
	for _, raw := range bytes.Split(b, []byte{'\n'}) {
		line := bytes.TrimSpace(raw)
		if len(line) == 0 || line[0] == '#' || (mysql && line[0] == ';') {
			continue
		}
		if line[0] == '!' {
			return nil, fail("unsupported", "dynamic_include")
		}
		if line[0] == '[' {
			if line[len(line)-1] != ']' {
				return nil, fail("unsupported", "source_format")
			}
			name := string(line[1 : len(line)-1])
			if !safeName(name) {
				return nil, fail("unsupported", "source_format")
			}
			if _, ok := groups[name]; ok {
				return nil, fail("unsupported", "duplicate_section")
			}
			currentName = name
			current = &group{map[string]string{}, map[string]bool{}, false}
			groups[name] = current
			continue
		}
		if current == nil {
			return nil, fail("unsupported", "source_format")
		}
		i := bytes.IndexByte(line, '=')
		if i < 1 {
			current.unsupported = true
			continue
		}
		key := string(bytes.TrimSpace(line[:i]))
		keys := pgKeys
		if mysql {
			keys = myKeys
			key = strings.ReplaceAll(key, "_", "-")
		}
		mapped, ok := keys[key]
		if !ok {
			current.unsupported = true
			continue
		}
		if _, ok := current.values[mapped]; ok {
			return nil, fail("unsupported", "duplicate_field")
		}
		if current.secrets[mapped] {
			return nil, fail("unsupported", "duplicate_field")
		}
		if mapped == "password" && (!private || !slices.Contains(selected, currentName)) {
			current.secrets[mapped] = true
			continue
		}
		value := string(bytes.TrimSpace(line[i+1:]))
		if mysql {
			var e error
			value, e = mysqlValue(value)
			if e != nil {
				return nil, e
			}
		}
		if mysql && (mapped == "sslMode" || mapped == "protocol") {
			value = strings.ToUpper(value)
		}
		current.values[mapped] = value
	}
	return groups, nil
}
func mysqlValue(s string) (string, error) {
	// Strip # comments only outside quotes; recognize MySQL option-file escapes.
	quote := byte(0)
	escaped := false
	end := len(s)
	for i := 0; i < len(s); i++ {
		c := s[i]
		if escaped {
			escaped = false
			continue
		}
		if c == '\\' {
			escaped = true
			continue
		}
		if quote != 0 {
			if c == quote {
				quote = 0
			}
			continue
		}
		if c == '\'' || c == '"' {
			quote = c
		}
		if c == '#' {
			end = i
			break
		}
	}
	if quote != 0 {
		return "", fail("unsupported", "source_format")
	}
	s = strings.TrimSpace(s[:end])
	if len(s) > 0 && (s[0] == '\'' || s[0] == '"') {
		if len(s) < 2 || s[len(s)-1] != s[0] {
			return "", fail("unsupported", "source_format")
		}
		s = s[1 : len(s)-1]
	}
	var out strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' || i+1 == len(s) {
			out.WriteByte(s[i])
			continue
		}
		i++
		switch s[i] {
		case 'b':
			out.WriteByte('\b')
		case 't':
			out.WriteByte('\t')
		case 'n':
			out.WriteByte('\n')
		case 'r':
			out.WriteByte('\r')
		case 's':
			out.WriteByte(' ')
		case '\\', '\'', '"':
			out.WriteByte(s[i])
		default:
			out.WriteByte('\\')
			out.WriteByte(s[i])
		}
	}
	return out.String(), nil
}
func references(values map[string]string) map[string]string {
	out := map[string]string{}
	for _, k := range []string{"passfile", "caCert", "clientCert", "clientKey"} {
		if v := values[k]; v != "" {
			out[k] = v
		}
	}
	return out
}
func (r *Reader) reference(name string) (string, error) {
	// A native relative reference depends on the host command's unknown CWD.
	// Do not reinterpret it as HOME. Fixed default .pgpass is handled separately.
	if !filepath.IsAbs(name) {
		return "", fail("unsupported", "relative_reference")
	}
	rel, e := filepath.Rel(r.base, name)
	if e != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, "../") || path.Clean(rel) != rel || strings.Contains(rel, "\\") {
		return "", fail("unsupported", "source_reference")
	}
	return rel, nil
}
func (r *Reader) pinReferences(pins map[string]stamp, g *group) error {
	for key, name := range references(g.values) {
		rel, e := r.reference(name)
		if e != nil {
			return e
		}
		p, e := r.stat(rel)
		if e != nil {
			return e
		}
		pins[rel] = p
		if !p.exists {
			return fail("source_unreadable", "source_reference")
		}
		g.values[key] = rel
	}
	return nil
}
func missingDB(cli string, g *group) ([]string, string) {
	if g.unsupported {
		return nil, "unsupported"
	}
	for _, k := range []string{"host", "port", "user"} {
		if g.values[k] == "" {
			return nil, "missing_identity"
		}
	}
	if strings.ContainsAny(g.values["host"], "/,\\ \t") || strings.HasPrefix(g.values["host"], "-") {
		return nil, "unsupported"
	}
	if p := g.values["protocol"]; p != "" && p != "TCP" {
		return nil, "unsupported"
	}
	modes := []string{"disable", "require", "verify-ca", "verify-full"}
	if cli == "mysql" {
		modes = []string{"DISABLED", "REQUIRED", "VERIFY_CA", "VERIFY_IDENTITY"}
	}
	missing := []string{}
	if g.values["database"] == "" {
		missing = append(missing, "database")
	}
	mode := g.values["sslMode"]
	if mode == "" {
		missing = append(missing, "sslMode")
	} else if !slices.Contains(modes, mode) {
		return nil, "unsupported"
	}
	if mode == "verify-ca" || mode == "verify-full" || mode == "VERIFY_CA" || mode == "VERIFY_IDENTITY" {
		if g.values["caCert"] == "" {
			missing = append(missing, "caCert")
		}
	}
	if (g.values["clientCert"] == "") != (g.values["clientKey"] == "") {
		return nil, "unsupported"
	}
	return missing, ""
}

// supplementableDB is an availability projection, not a required-field list.
// Missing CA is available even when TLS mode has not yet been chosen. Values
// that already came from the source remain immutable in supplement().
func supplementableDB(g *group) []string {
	out := []string{}
	if g == nil {
		return out
	}
	for _, name := range []string{"database", "sslMode", "caCert"} {
		if g.values[name] == "" {
			out = append(out, name)
		}
	}
	return out
}
func supplement(fields map[string]string, s Supplement) error {
	for k, v := range map[string]string{"database": s.Database, "sslMode": s.SSLMode, "caCert": s.CACert} {
		if v == "" {
			continue
		}
		if fields[k] != "" {
			return fail("supplement_override", k)
		}
		fields[k] = v
	}
	for _, k := range []string{"database", "sslMode"} {
		if fields[k] == "" {
			return fail("missing_field", k)
		}
	}
	return nil
}
func (r *Reader) extractDB(ctx context.Context, e *entry, s Supplement) (map[string]string, error) {
	if e.candidate.SourceKind == "mysql_login_path" {
		return r.extractLogin(ctx, e, s)
	}
	pin := e.pins[e.file]
	raw, err := r.read(ctx, e.file, &pin, false)
	if err != nil {
		return nil, err
	}
	defer clear(raw)
	groups, err := parseINI(raw, e.candidate.CLI == "mysql", true, func() []string {
		if e.candidate.CLI == "mysql" {
			return []string{"client", "mysql"}
		}
		return []string{e.section}
	}()...)
	if err != nil {
		return nil, err
	}
	var g *group
	if e.candidate.CLI == "mysql" {
		g = mysqlEffective(groups)
	} else {
		g = groups[e.section]
	}
	if g == nil {
		return nil, fail("source_changed", "source")
	}
	return r.finishDB(ctx, e, s, g)
}
func (r *Reader) finishDB(ctx context.Context, e *entry, s Supplement, g *group) (map[string]string, error) {
	fields := g.values
	defer clear(fields)

	_, reason := missingDB(e.candidate.CLI, g)
	if reason != "" {
		return nil, fail(reason, "credential")
	}
	if e.candidate.CLI == "psql" && fields["password"] == "" {
		if s.Database != "" && fields["database"] != "" {
			return nil, fail("supplement_override", "database")
		}
		target := fields["database"]
		if target == "" {
			target = s.Database
		}
		if target == "" {
			return nil, fail("missing_field", "database")
		}
		passfile := fields["passfile"]
		if passfile != "" {
			var err error
			passfile, err = r.reference(passfile)
			if err != nil {
				return nil, err
			}
		}
		if passfile == "" {
			passfile = ".pgpass"
		}
		p := e.pins[passfile]
		pass, err := r.read(ctx, passfile, &p, true)
		if err != nil {
			return nil, err
		}
		password, err := pgPassword(pass, []string{fields["host"], fields["port"], target, fields["user"]})
		clear(pass)
		if err != nil {
			return nil, err
		}
		fields["password"] = password
	}
	delete(fields, "passfile")
	delete(fields, "protocol")
	// Resolve only references frozen at discovery, never caller paths.
	for _, k := range []string{"caCert", "clientCert", "clientKey"} {
		name := fields[k]
		if name == "" {
			continue
		}
		resolved, err := r.reference(name)
		name = resolved
		if err != nil {
			return nil, err
		}
		pin, ok := e.pins[name]
		if !ok {
			return nil, fail("source_changed", "source_reference")
		}
		raw, err := r.read(ctx, name, &pin, k == "clientKey")
		if err != nil {
			return nil, err
		}
		fields[k] = string(raw)
		clear(raw)
	}
	if err := supplement(fields, s); err != nil {
		return nil, err
	}
	if fields["password"] == "" {
		return nil, fail("credential_unavailable", "password")
	}
	result := map[string]string{}
	for k, v := range fields {
		result[k] = v
	}
	return result, nil
}
