package clitemplate

import (
	"encoding/json"
	"fmt"
	"io"
)

// Files read by lark-cli v1.0.94 (relative to the run's HOME), with formats taken from its source code:
//   - Config file: `$HOME/.lark-cli/config.json` (internal/core/workspace.go, internal/core/config.go).
//     When appSecret is written as a plain string it is used directly, without going through the keychain and
//     without decryption (internal/core/secret.go).
//   - macOS master key file: `$HOME/Library/Application Support/lark-cli/master.key.file`, 32 bytes of raw data.
//     When it exists, write commands that store secrets use it instead and never touch the host user's login
//     keychain (internal/keychain/keychain_darwin.go).
const (
	larkConfigPath    = ".lark-cli/config.json"
	larkMasterKeyPath = "Library/Application Support/lark-cli/master.key.file"
)

type larkConfig struct {
	Apps []larkApp `json:"apps"`
}

type larkApp struct {
	AppID     string `json:"appId"`
	AppSecret string `json:"appSecret"`
	Brand     string `json:"brand"`
	// The first version supports only the app identity: with strictMode set to bot, lark-cli refuses user-identity
	// commands such as auth login.
	StrictMode string            `json:"strictMode"`
	DefaultAs  string            `json:"defaultAs"`
	Users      []json.RawMessage `json:"users"`
}

// renderLarkCLI generates lark-cli's credential files from the input fields. When goos is darwin it also generates a
// random master key file.
func renderLarkCLI(values map[string]string, goos string, random io.Reader) ([]PayloadFile, error) {
	cfg, err := json.MarshalIndent(larkConfig{Apps: []larkApp{{
		AppID:      values["appId"],
		AppSecret:  values["appSecret"],
		Brand:      values["brand"],
		StrictMode: "bot",
		DefaultAs:  "bot",
		Users:      []json.RawMessage{},
	}}}, "", "  ")
	if err != nil {
		return nil, err
	}
	files := []PayloadFile{{Path: larkConfigPath, Content: append(cfg, '\n')}}
	if goos == "darwin" {
		key := make([]byte, 32)
		if _, err := io.ReadFull(random, key); err != nil {
			return nil, fmt.Errorf("生成 lark-cli 主密钥失败：%w", err)
		}
		files = append(files, PayloadFile{Path: larkMasterKeyPath, Content: key})
	}
	return files, nil
}
