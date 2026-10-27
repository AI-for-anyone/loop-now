package source

import (
	"context"
	"sort"
)

func sortedGroups(groups map[string]*group) []string {
	names := []string{}
	for k := range groups {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}

func mysqlEffective(groups map[string]*group) *group {
	out := &group{map[string]string{}, map[string]bool{}, false}
	found := false
	for _, name := range []string{"client", "mysql"} {
		g := groups[name]
		if g == nil {
			continue
		}
		found = true
		out.unsupported = out.unsupported || g.unsupported
		for k, v := range g.values {
			if _, ok := out.values[k]; ok {
				out.unsupported = true
			}
			if out.secrets[k] {
				out.unsupported = true
			}
			out.values[k] = v
		}
		for k := range g.secrets {
			if _, ok := out.values[k]; ok {
				out.unsupported = true
			}
			if out.secrets[k] {
				out.unsupported = true
			}
			out.secrets[k] = true
		}
	}
	if !found {
		return nil
	}
	return out
}
func (r *Reader) discoverMySQL(ctx context.Context) ([]Candidate, error) {
	out := []Candidate{}
	name := ".my.cnf"
	pin, err := r.stat(name)
	if err != nil {
		out = append(out, r.add("mysql", "mysql_option_file", "MySQL option file", name, "", nil, nil, "source_unreadable"))
	} else if pin.exists {
		raw, e := r.read(ctx, name, &pin, false)
		if e != nil {
			return nil, e
		}
		groups, e := parseINI(raw, true, false)
		clear(raw)
		pins := map[string]stamp{name: pin}
		var g *group
		if e == nil {
			g = mysqlEffective(groups)
		}
		missing := []string{}
		reason := "unsupported"
		if g != nil {
			missing, reason = missingDB("mysql", g)
			if !g.secrets["password"] {
				reason = "credential_unavailable"
			}
			if e = r.pinReferences(pins, g); e != nil {
				reason = "unsupported_reference"
			}
		}
		out = append(out, r.add("mysql", "mysql_option_file", "MySQL client/mysql configuration", name, "", pins, missing, reason, supplementableDB(g)))
	}

	pin, err = r.stat(".mylogin.cnf")
	if err != nil {
		out = append(out, r.add("mysql", "mysql_login_path", "MySQL login-path", ".mylogin.cnf", "", nil, nil, "source_unreadable"))
	} else if pin.exists {
		out = append(out, r.discoverLogin(ctx, pin)...)
	}
	return out, nil
}
