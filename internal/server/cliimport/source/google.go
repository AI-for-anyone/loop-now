package source

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"sort"
	"strings"
)

func googleMetadata(raw []byte) (string, error) {
	// Token values are skipped as raw JSON by a streaming decoder, never put into
	// a generic map or credential struct in discovery.
	d := json.NewDecoder(bytes.NewReader(raw))
	t, e := d.Token()
	if e != nil || t != json.Delim('{') {
		return "", fail("unsupported", "source_format")
	}
	kind := ""
	seen := map[string]bool{}
	for d.More() {
		k, e := d.Token()
		if e != nil {
			return "", fail("unsupported", "source_format")
		}
		key, ok := k.(string)
		if !ok || seen[key] {
			return "", fail("unsupported", "source_format")
		}
		seen[key] = true
		if key == "type" {
			if d.Decode(&kind) != nil {
				return "", fail("unsupported", "source_format")
			}
		} else {
			var skip json.RawMessage
			if d.Decode(&skip) != nil {
				return "", fail("unsupported", "source_format")
			}
			clear(skip)
		}
	}
	if _, e = d.Token(); e != nil || d.Decode(new(any)) != io.EOF {
		return "", fail("unsupported", "source_format")
	}
	return kind, nil
}
func (r *Reader) discoverGoogle(ctx context.Context) ([]Candidate, error) {
	names := []string{".config/gcloud/application_default_credentials.json"}
	out := []Candidate{}
	// Enumerate directory names only; no gcloud command or SQLite credential load.
	dir, e := r.openDirectory(".config/gcloud/legacy_credentials")
	if e == nil {
		entries, err := dir.ReadDir(1000)
		dir.Close()
		if err != nil && err != io.EOF {
			return nil, fail("source_unreadable", "source")
		}
		for _, x := range entries {
			if x.IsDir() && safeName(x.Name()) {
				names = append(names, ".config/gcloud/legacy_credentials/"+x.Name()+"/adc.json")
			}
		}
	} else if !errors.Is(e, os.ErrNotExist) {
		out = append(out, r.add("gcloud", "google_legacy_adc", "Google legacy credential sources", ".config/gcloud/legacy_credentials", "", nil, nil, "source_unreadable"))
	}
	sort.Strings(names)
	for _, name := range names {
		sourceKind := "google_adc"
		if strings.Contains(name, "/legacy_credentials/") {
			sourceKind = "google_legacy_adc"
		}
		pin, e := r.stat(name)
		if e != nil {
			out = append(out, r.add("gcloud", sourceKind, "Google credential source", name, "", nil, nil, "source_unreadable"))
			continue
		}
		if !pin.exists {
			continue
		}
		raw, e := r.read(ctx, name, &pin, false)
		if e != nil {
			return nil, e
		}
		kind, e := googleMetadata(raw)
		clear(raw)
		reason := ""
		if e != nil {
			reason = "unsupported"
		} else {
			switch kind {
			case "service_account":
			case "authorized_user":
				reason = "requires_platform_login"
			default:
				reason = "unsupported"
			}
		}
		label := "Google credential source"
		if kind == "service_account" {
			label = "Google service account"
		}
		if kind == "authorized_user" {
			label = "Google personal credential"
		}
		out = append(out, r.add("gcloud", sourceKind, label, name, "", map[string]stamp{name: pin}, nil, reason))
	}
	for _, item := range []struct{ name, kind, reason string }{{"credentials.db", "google_cli_store", "unsupported_cli_store"}, {"access_tokens.db", "google_access_cache", "cached_token_not_credential"}} {
		name := ".config/gcloud/" + item.name
		pin, err := r.stat(name)
		if err != nil {
			out = append(out, r.add("gcloud", item.kind, "Google credential store", name, "", nil, nil, "source_unreadable"))
		} else if pin.exists {
			out = append(out, r.add("gcloud", item.kind, "Google credential store", name, "", map[string]stamp{name: pin}, nil, item.reason))
		}
	}
	return out, nil
}
func (r *Reader) extractGoogle(ctx context.Context, e *entry, s Supplement) (map[string]string, error) {
	if s != (Supplement{}) {
		return nil, fail("supplement_override", "credential")
	}
	pin := e.pins[e.file]
	raw, err := r.read(ctx, e.file, &pin, false)
	if err != nil {
		return nil, err
	}
	defer clear(raw)
	kind, err := googleMetadata(raw)
	if err != nil || kind != "service_account" {
		return nil, fail("unsupported", "credential")
	}
	return map[string]string{"serviceAccountJson": string(raw)}, nil
}
