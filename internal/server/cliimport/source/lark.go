package source

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"regexp"
	"slices"
	"strconv"
)

const larkPlainConfig = ".lark-cli/config.json"

var larkAppID = regexp.MustCompile(`^cli_[A-Za-z0-9]{1,128}$`)

type larkMetadata struct {
	appID, brand, selector, reason string
	hasUsers                       bool
	missing                        []string
}

func jsonObject(d *json.Decoder, field func(string) error) error {
	tok, e := d.Token()
	if e != nil || tok != json.Delim('{') {
		return fail("unsupported", "source_format")
	}
	seen := map[string]bool{}
	for d.More() {
		tok, e := d.Token()
		key, ok := tok.(string)
		if e != nil || !ok || seen[key] {
			return fail("unsupported", "source_format")
		}
		seen[key] = true
		if e = field(key); e != nil {
			return e
		}
	}
	tok, e = d.Token()
	if e != nil || tok != json.Delim('}') {
		return fail("unsupported", "source_format")
	}
	return nil
}
func jsonText(d *json.Decoder) (string, error) {
	tok, e := d.Token()
	v, ok := tok.(string)
	if e != nil || !ok {
		return "", fail("unsupported", "source_format")
	}
	return v, nil
}
func jsonSkipString(d *json.Decoder) error {
	var raw json.RawMessage
	if d.Decode(&raw) != nil {
		return fail("unsupported", "source_format")
	}
	defer clear(raw)
	if len(raw) == 0 || raw[0] != '"' {
		return fail("unsupported", "source_format")
	}
	return nil
}
func larkSecretKind(raw []byte) (string, error) {
	if len(raw) > 0 && raw[0] == '"' {
		return "plain", nil
	}
	kind := ""
	hasID := false
	d := json.NewDecoder(bytes.NewReader(raw))
	e := jsonObject(d, func(key string) error {
		switch key {
		case "source":
			v, e := jsonText(d)
			kind = v
			return e
		case "id":
			hasID = true
			return jsonSkipString(d)
		case "provider":
			return jsonSkipString(d)
		default:
			return fail("unsupported", "source_format")
		}
	})
	if e != nil || !hasID || d.Decode(new(any)) != io.EOF {
		return "", fail("unsupported", "source_format")
	}
	switch kind {
	case "file":
		return "file_secret_not_supported", nil
	case "keychain":
		return "keyring_version_unbound", nil
	default:
		return "unsupported_secret_reference", nil
	}
}
func larkUsers(d *json.Decoder) (bool, error) {
	tok, e := d.Token()
	if e != nil {
		return false, fail("unsupported", "source_format")
	}
	if tok == nil {
		return false, nil
	}
	if tok != json.Delim('[') {
		return false, fail("unsupported", "source_format")
	}
	count := 0
	for d.More() {
		count++
		if count > 1000 {
			return false, fail("unsupported", "source_limit")
		}
		e := jsonObject(d, func(key string) error {
			if key != "userOpenId" && key != "userName" {
				return fail("unsupported", "source_format")
			}
			return jsonSkipString(d)
		})
		if e != nil {
			return false, e
		}
	}
	tok, e = d.Token()
	if e != nil || tok != json.Delim(']') {
		return false, fail("unsupported", "source_format")
	}
	return count > 0, nil
}
func parseLarkPass(raw []byte, selectedIndex int) ([]larkMetadata, string, error) {
	d := json.NewDecoder(bytes.NewReader(raw))
	apps := []larkMetadata{}
	secret := ""
	hasApps := false
	err := jsonObject(d, func(key string) error {
		switch key {
		case "strictMode":
			v, e := jsonText(d)
			if e != nil {
				return e
			}
			if !slices.Contains([]string{"", "off", "bot", "user"}, v) {
				return fail("unsupported", "source_format")
			}
			return nil
		case "riskControl":
			tok, e := d.Token()
			if e != nil {
				return fail("unsupported", "source_format")
			}
			if tok == nil {
				return nil
			}
			if _, ok := tok.(bool); !ok {
				return fail("unsupported", "source_format")
			}
			return nil
		case "currentApp", "previousApp":
			return jsonSkipString(d)
		case "apps":
			hasApps = true
			tok, e := d.Token()
			if e != nil || tok != json.Delim('[') {
				return fail("unsupported", "source_format")
			}
			for d.More() {
				index := len(apps)
				if index >= 1000 {
					return fail("unsupported", "source_limit")
				}
				m := larkMetadata{}
				secretKind := ""
				hasSecret := false
				e = jsonObject(d, func(key string) error {
					switch key {
					case "appId":
						v, e := jsonText(d)
						m.appID = v
						return e
					case "brand":
						v, e := jsonText(d)
						m.brand = v
						return e
					case "appSecret":
						hasSecret = true
						var skip json.RawMessage
						if d.Decode(&skip) != nil {
							return fail("unsupported", "source_format")
						}
						defer clear(skip)
						kind, e := larkSecretKind(skip)
						if e != nil {
							return e
						}
						secretKind = kind
						if index == selectedIndex && kind == "plain" {
							if json.Unmarshal(skip, &secret) != nil {
								return fail("unsupported", "source_format")
							}
						}
						return nil
					case "name", "lang":
						return jsonSkipString(d)
					case "defaultAs":
						v, e := jsonText(d)
						if e != nil {
							return e
						}
						if !slices.Contains([]string{"", "auto", "bot", "user"}, v) {
							return fail("unsupported", "source_format")
						}
						return nil
					case "strictMode":
						tok, e := d.Token()
						if e != nil {
							return fail("unsupported", "source_format")
						}
						if tok == nil {
							return nil
						}
						v, ok := tok.(string)
						if !ok || !slices.Contains([]string{"", "off", "bot", "user"}, v) {
							return fail("unsupported", "source_format")
						}
						return nil
					case "users":
						v, e := larkUsers(d)
						m.hasUsers = v
						return e
					default:
						return fail("unsupported", "source_format")
					}
				})
				if e != nil {
					return e
				}
				if !larkAppID.MatchString(m.appID) {
					m.reason = "missing_identity"
					m.missing = append(m.missing, "appId")
				}
				if m.brand != "feishu" && m.brand != "lark" {
					m.reason = "unsupported_brand"
					m.missing = append(m.missing, "brand")
				}
				if !hasSecret {
					m.reason = "credential_unavailable"
					m.missing = append(m.missing, "appSecret")
				} else if secretKind != "plain" {
					m.reason = secretKind
				}
				m.selector = strconv.Itoa(index) + ":" + m.appID
				apps = append(apps, m)
			}
			tok, e = d.Token()
			if e != nil || tok != json.Delim(']') {
				return fail("unsupported", "source_format")
			}
			return nil
		default:
			return fail("unsupported", "source_format")
		}
	})
	if err != nil || !hasApps || d.Decode(new(any)) != io.EOF {
		return nil, "", fail("unsupported", "source_format")
	}
	return apps, secret, nil
}
func (r *Reader) discoverLark(ctx context.Context) ([]Candidate, error) {
	pin, e := r.stat(larkPlainConfig)
	if e != nil {
		return []Candidate{r.add("lark-cli", "lark_bot_plaintext", "Lark 应用/Bot来源", larkPlainConfig, "", nil, nil, "source_unreadable")}, nil
	}
	if !pin.exists {
		return nil, nil
	}
	raw, e := r.read(ctx, larkPlainConfig, &pin, false)
	if e != nil {
		return nil, e
	}
	defer clear(raw)
	apps, _, e := parseLarkPass(raw, -1)
	pins := map[string]stamp{larkPlainConfig: pin}
	if e != nil {
		return []Candidate{r.add("lark-cli", "lark_bot_plaintext", "Lark 应用/Bot来源", larkPlainConfig, "", pins, nil, "unsupported")}, nil
	}
	out := []Candidate{}
	for _, a := range apps {
		label := "Lark 应用/Bot来源"
		if larkAppID.MatchString(a.appID) {
			label = "Lark 应用/Bot " + a.appID
		}
		out = append(out, r.add("lark-cli", "lark_bot_plaintext", label, larkPlainConfig, a.selector, pins, a.missing, a.reason))
		if a.hasUsers {
			out = append(out, r.add("lark-cli", "lark_user", "Lark 用户登录（不导入）", larkPlainConfig, "user:"+a.selector, pins, nil, "user_identity_unsupported"))
		}
	}
	return out, nil
}
func (r *Reader) extractLark(ctx context.Context, e *entry, s Supplement) (map[string]string, error) {
	if s != (Supplement{}) {
		return nil, fail("supplement_override", "credential")
	}
	pin := e.pins[e.file]
	raw, err := r.read(ctx, e.file, &pin, true)
	if err != nil {
		return nil, err
	}
	defer clear(raw)
	apps, _, err := parseLarkPass(raw, -1)
	if err != nil {
		return nil, err
	}
	index := -1
	for i, a := range apps {
		if a.selector == e.section && a.reason == "" {
			index = i
			break
		}
	}
	if index < 0 {
		return nil, fail("credential_unavailable", "appSecret")
	}
	_, secret, err := parseLarkPass(raw, index)
	if err != nil {
		return nil, err
	}
	if !plaintextToken(secret) {
		return nil, fail("credential_unavailable", "appSecret")
	}
	return map[string]string{"appId": apps[index].appID, "appSecret": secret, "brand": apps[index].brand}, nil
}
