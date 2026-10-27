package source

import (
	"bytes"
	"context"
	"io"
	"regexp"
	"slices"
	"strings"

	yaml "github.com/oasdiff/yaml3"
)

const ghHostsFile = ".config/gh/hosts.yml"

var ghUserName = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,37}[A-Za-z0-9])?$`)
var ghHostName = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9.-]{0,251}[A-Za-z0-9])?$`)

type ghMetadata struct{ host, user, selector, reason string }

// validateYAML rejects graph/typing tricks before any node becomes a label.
// It does not Decode into credential maps or expand aliases.
func validateYAML(n *yaml.Node, depth int, budget *int) error {
	*budget--
	if depth > 12 || *budget < 0 || n.Anchor != "" || n.Kind == yaml.AliasNode || n.Style&yaml.TaggedStyle != 0 {
		return fail("unsupported", "source_format")
	}
	if !slices.Contains([]string{"!!map", "!!str", "!!null", ""}, n.Tag) {
		return fail("unsupported", "source_format")
	}
	if n.Kind == yaml.MappingNode {
		seen := map[string]bool{}
		if len(n.Content)%2 != 0 {
			return fail("unsupported", "source_format")
		}
		for i := 0; i < len(n.Content); i += 2 {
			k := n.Content[i]
			if k.Kind != yaml.ScalarNode || k.Tag != "!!str" || seen[k.Value] {
				return fail("unsupported", "source_format")
			}
			seen[k.Value] = true
		}
	}
	for _, c := range n.Content {
		if e := validateYAML(c, depth+1, budget); e != nil {
			return e
		}
	}
	return nil
}
func clearYAML(n *yaml.Node) {
	n.Value = ""
	n.HeadComment = ""
	n.LineComment = ""
	n.FootComment = ""
	for _, c := range n.Content {
		clearYAML(c)
	}
}
func yamlMapping(n *yaml.Node, allowed []string) (map[string]*yaml.Node, error) {
	if n == nil || n.Kind != yaml.MappingNode {
		return nil, fail("unsupported", "source_format")
	}
	out := map[string]*yaml.Node{}
	for i := 0; i < len(n.Content); i += 2 {
		key := n.Content[i].Value
		if allowed != nil && !slices.Contains(allowed, key) {
			return nil, fail("unsupported", "source_format")
		}
		out[key] = n.Content[i+1]
	}
	return out, nil
}
func yamlString(n *yaml.Node) (string, error) {
	if n == nil || n.Tag == "!!null" {
		return "", nil
	}
	if n.Kind != yaml.ScalarNode || n.Tag != "!!str" {
		return "", fail("unsupported", "source_format")
	}
	return n.Value, nil
}
func plaintextToken(value string) bool {
	return len(value) > 0 && len(value) <= 16<<10 && strings.IndexFunc(value, func(r rune) bool { return r < 33 || r > 126 }) < 0
}

// parseGH returns safe identity metadata. Only the exact selected node's value
// is projected as a token; the transient YAML parser tree is never scan state.
func parseGH(raw []byte, selected string) ([]ghMetadata, string, error) {
	d := yaml.NewDecoder(bytes.NewReader(raw))
	var doc yaml.Node
	defer clearYAML(&doc)
	if d.Decode(&doc) != nil {
		return nil, "", fail("unsupported", "source_format")
	}
	var extra yaml.Node
	if d.Decode(&extra) != io.EOF {
		clearYAML(&extra)
		return nil, "", fail("unsupported", "source_format")
	}
	budget := 4096
	if e := validateYAML(&doc, 0, &budget); e != nil {
		return nil, "", e
	}
	if len(doc.Content) != 1 {
		return nil, "", fail("unsupported", "source_format")
	}
	hosts, e := yamlMapping(doc.Content[0], nil)
	if e != nil {
		return nil, "", e
	}
	names := []string{}
	for h := range hosts {
		names = append(names, h)
	}
	slices.Sort(names)
	out := []ghMetadata{}
	token := ""
	for _, host := range names {
		if !ghHostName.MatchString(host) {
			return nil, "", fail("unsupported", "source_format")
		}
		h, e := yamlMapping(hosts[host], []string{"user", "users", "oauth_token", "git_protocol", "api_host"})
		if e != nil {
			return nil, "", e
		}
		if n := h["oauth_token"]; n != nil && (n.Kind != yaml.ScalarNode || (n.Tag != "!!str" && n.Tag != "!!null")) {
			return nil, "", fail("unsupported", "source_format")
		}
		active, e := yamlString(h["user"])
		if e != nil {
			return nil, "", e
		}
		protocol, e := yamlString(h["git_protocol"])
		if e != nil {
			return nil, "", e
		}
		apiHost, e := yamlString(h["api_host"])
		if e != nil {
			return nil, "", e
		}
		reason := ""
		if host != "github.com" {
			reason = "host_not_supported"
		}
		if apiHost != "" {
			reason = "unsupported_endpoint_mapping"
		}
		if protocol != "" && protocol != "ssh" && protocol != "https" {
			reason = "unsupported"
		}
		users := map[string]*yaml.Node{}
		if h["users"] != nil {
			users, e = yamlMapping(h["users"], nil)
			if e != nil {
				return nil, "", e
			}
		} else {
			if active == "" {
				out = append(out, ghMetadata{host, "", "", "missing_identity"})
				continue
			}
			users[active] = nil
		}
		if len(users) == 0 && h["users"] != nil {
			if active != "" && !ghUserName.MatchString(active) {
				return nil, "", fail("unsupported", "source_format")
			}
			why := "unbound_identity"
			if reason != "" {
				why = reason
			}
			out = append(out, ghMetadata{host, active, host + "\x00" + active, why})
		}
		ordered := []string{}
		for user := range users {
			ordered = append(ordered, user)
		}
		slices.Sort(ordered)
		for _, user := range ordered {
			if !ghUserName.MatchString(user) {
				return nil, "", fail("unsupported", "source_format")
			}
			var node *yaml.Node
			if h["users"] == nil {
				node = h["oauth_token"]
			} else if users[user].Tag != "!!null" {
				account, e := yamlMapping(users[user], []string{"oauth_token"})
				if e != nil {
					return nil, "", e
				}
				node = account["oauth_token"]
			}
			why := reason
			if node == nil || node.Tag == "!!null" {
				if why == "" {
					why = "keyring_version_unbound"
				}
			} else if node.Kind != yaml.ScalarNode || node.Tag != "!!str" {
				return nil, "", fail("unsupported", "source_format")
			}
			selector := host + "\x00" + user
			out = append(out, ghMetadata{host, user, selector, why})
			if selected == selector && why == "" {
				token = node.Value
			}
		}
	}
	return out, token, nil
}
func (r *Reader) discoverGH(ctx context.Context) ([]Candidate, error) {
	pin, e := r.stat(ghHostsFile)
	if e != nil {
		return []Candidate{r.add("gh", "gh_hosts_plaintext", "GitHub CLI source", ghHostsFile, "", nil, nil, "source_unreadable")}, nil
	}
	if !pin.exists {
		return nil, nil
	}
	raw, e := r.read(ctx, ghHostsFile, &pin, false)
	if e != nil {
		return nil, e
	}
	defer clear(raw)
	accounts, _, e := parseGH(raw, "")
	pins := map[string]stamp{ghHostsFile: pin}
	if e != nil {
		return []Candidate{r.add("gh", "gh_hosts_plaintext", "GitHub CLI source", ghHostsFile, "", pins, nil, "unsupported")}, nil
	}
	out := []Candidate{}
	for _, a := range accounts {
		label := "GitHub CLI source"
		if a.user != "" {
			label = a.host + " / " + a.user
		}
		out = append(out, r.add("gh", "gh_hosts_plaintext", label, ghHostsFile, a.selector, pins, nil, a.reason))
	}
	return out, nil
}
func (r *Reader) extractGH(ctx context.Context, e *entry, s Supplement) (map[string]string, error) {
	if s != (Supplement{}) {
		return nil, fail("supplement_override", "credential")
	}
	pin := e.pins[e.file]
	raw, err := r.read(ctx, e.file, &pin, true)
	if err != nil {
		return nil, err
	}
	defer clear(raw)
	_, token, err := parseGH(raw, e.section)
	if err != nil {
		return nil, err
	}
	if !plaintextToken(token) {
		return nil, fail("credential_unavailable", "token")
	}
	return map[string]string{"token": token}, nil
}
