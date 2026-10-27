package source

import (
	"bytes"
	"context"
	"errors"
	"sort"
	"strings"
)

func (r *Reader) discoverPG(ctx context.Context) ([]Candidate, error) {
	name := ".pg_service.conf"
	pin, err := r.stat(name)
	if err != nil {
		return []Candidate{r.add("psql", "pg_service", "PostgreSQL service", name, "", nil, nil, "source_unreadable")}, nil
	}
	if !pin.exists {
		return nil, nil
	}
	raw, err := r.read(ctx, name, &pin, false)
	if err != nil {
		return nil, err
	}
	defer clear(raw)
	groups, err := parseINI(raw, false, false)
	if err != nil {
		return []Candidate{r.add("psql", "pg_service", "PostgreSQL service", name, "", map[string]stamp{name: pin}, nil, "unsupported")}, nil
	}
	names := []string{}
	for k := range groups {
		names = append(names, k)
	}
	sort.Strings(names)
	out := []Candidate{}
	for _, section := range names {
		g := groups[section]
		pins := map[string]stamp{name: pin}
		missing, reason := missingDB("psql", g)
		if err = r.pinReferences(pins, g); err != nil {
			reason = "unsupported_reference"
		}
		if !g.secrets["password"] {
			pass := g.values["passfile"]
			if pass == "" {
				pass = ".pgpass"
			}
			p, e := r.stat(pass)
			pins[pass] = p
			if e != nil || !p.exists {
				reason = "credential_unavailable"
			}
		}
		out = append(out, r.add("psql", "pg_service", "PostgreSQL service "+section, name, section, pins, missing, reason, supplementableDB(g)))
	}
	return out, nil
}

// pgPassword freezes the official FIRST match, including wildcards. A missing
// database is resolved by an allowed supplement before this is ever called.
func pgPassword(raw []byte, target []string) (string, error) {
	for _, v := range target {
		if v == "" {
			return "", fail("missing_field", "connection_target")
		}
	}
	for _, line := range bytes.Split(raw, []byte{'\n'}) {
		if len(line) == 0 || line[0] == '#' {
			continue
		}
		line = bytes.TrimSuffix(line, []byte{'\r'})
		fields := []string{}
		wild := []bool{}
		var b strings.Builder
		start := 0
		passwordAt := -1
		for i := 0; i < len(line); i++ {
			c := line[i]
			if c == '\\' && i+1 < len(line) {
				i++
				b.WriteByte(line[i])
				continue
			}
			if c == ':' {
				fields = append(fields, b.String())
				wild = append(wild, i-start == 1 && line[start] == '*')
				b.Reset()
				start = i + 1
				if len(fields) == 4 {
					passwordAt = i + 1
					break
				}
				continue
			}
			b.WriteByte(c)
		}
		if passwordAt < 0 {
			continue
		}
		match := true
		for i := 0; i < 4; i++ {
			if !wild[i] && fields[i] != target[i] {
				match = false
			}
		}
		if !match {
			continue
		}
		// Do not decode any password until the first matching target is known.
		for i := passwordAt; i < len(line); i++ {
			if line[i] == '\\' && i+1 < len(line) {
				i++
			}
			b.WriteByte(line[i])
		}
		if b.Len() == 0 {
			return "", fail("credential_unavailable", "password")
		}
		return b.String(), nil
	}
	return "", fail("credential_unavailable", "password")
}

// Discover enumerates only the fixed native locations. Source failures are safe
// candidates, not an indistinguishable empty list. Only reviewed MySQL masked discovery executes fixed tools.
func (r *Reader) Discover(ctx context.Context) ([]Candidate, error) {
	if ctx.Err() != nil {
		return nil, fail("cancelled", "operation")
	}
	out := []Candidate{}
	for _, fn := range []func(context.Context) ([]Candidate, error){r.discoverPG, r.discoverMySQL, r.discoverGoogle, r.discoverGH, r.discoverLark} {
		c, e := fn(ctx)
		if e != nil {
			return nil, e
		}
		out = append(out, c...)
	}
	return out, nil
}
func (r *Reader) extract(ctx context.Context, e *entry, s Supplement) (map[string]string, error) {
	switch e.candidate.CLI {
	case "psql", "mysql":
		return r.extractDB(ctx, e, s)
	case "gcloud":
		return r.extractGoogle(ctx, e, s)
	case "gh":
		return r.extractGH(ctx, e, s)
	case "lark-cli":
		return r.extractLark(ctx, e, s)
	}
	return nil, errors.New("local import: unsupported: source_kind")
}
