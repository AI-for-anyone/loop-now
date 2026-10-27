package larkauth

import (
	"encoding/json"
	"loop-now/internal/server/clicredential"
	"loop-now/internal/server/clitemplate"
)

// DecodeApplication reads only the captured, platform-rendered bot payload.
// It cannot consult the host config, another account or an environment variable.
func DecodeApplication(plain []byte) (Application, error) {
	var payload clitemplate.Payload
	if len(plain) > clicredential.MaxBytes || json.Unmarshal(plain, &payload) != nil {
		return Application{}, &SafeError{Code: "invalid_response"}
	}
	var result Application
	found := false
	for _, f := range payload.Files {
		if f.Path != ".lark-cli/config.json" {
			continue
		}
		if found {
			return Application{}, &SafeError{Code: "invalid_response"}
		}
		found = true
		var cfg struct {
			Apps []struct {
				AppID      string            `json:"appId"`
				AppSecret  string            `json:"appSecret"`
				Brand      string            `json:"brand"`
				StrictMode string            `json:"strictMode"`
				DefaultAs  string            `json:"defaultAs"`
				Users      []json.RawMessage `json:"users"`
			} `json:"apps"`
		}
		if json.Unmarshal(f.Content, &cfg) != nil || len(cfg.Apps) != 1 || cfg.Apps[0].StrictMode != "bot" || cfg.Apps[0].DefaultAs != "bot" || len(cfg.Apps[0].Users) != 0 {
			return Application{}, &SafeError{Code: "invalid_response"}
		}
		a := cfg.Apps[0]
		result = Application{a.Brand, a.AppID, a.AppSecret}
	}
	if !found || !result.Valid() {
		return Application{}, &SafeError{Code: "invalid_response"}
	}
	return result, nil
}
