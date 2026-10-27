package clitemplate

import (
	"encoding/json"
	"fmt"
	"io"
)

// lark-cli v1.0.94 读取的文件（相对执行 HOME），格式按其源码确定：
//   - 配置文件：`$HOME/.lark-cli/config.json`（internal/core/workspace.go、internal/core/config.go）。
//     appSecret 写明文字符串时直接使用，不经过钥匙串，也不需要解密（internal/core/secret.go）。
//   - macOS 的主密钥文件：`$HOME/Library/Application Support/lark-cli/master.key.file`，32 字节原始数据。
//     存在时，会保存密钥的写命令改用它，不会去碰宿主用户的登录钥匙串（internal/keychain/keychain_darwin.go）。
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
	// 第一版只支持应用身份：strictMode 为 bot 时 lark-cli 拒绝 auth login 等用户身份命令。
	StrictMode string            `json:"strictMode"`
	DefaultAs  string            `json:"defaultAs"`
	Users      []json.RawMessage `json:"users"`
}

// renderLarkCLI 按录入项生成 lark-cli 的凭证文件。goos 为 darwin 时另生成随机主密钥文件。
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
