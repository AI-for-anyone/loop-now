// Package modelcatalog 用固定版本 Pi 的原生运行时读取模型目录（离线）。
package modelcatalog

import (
	"bytes"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"time"
)

//go:embed catalog.mjs
var script []byte

// Model 是目录中的一个模型。
type Model struct {
	ThinkingLevels       []string `json:"thinkingLevels"`
	DefaultThinkingLevel string   `json:"defaultThinkingLevel"`
	ID                   string   `json:"id"`
	Name                 string   `json:"name"`
	Reasoning            bool     `json:"reasoning"`
	ContextWindow        int      `json:"contextWindow"`
	MaxTokens            int      `json:"maxTokens"`
	Input                []string `json:"input"`
}

// Provider 是目录中的一个提供方。
type Provider struct {
	ID            string   `json:"id"`
	Source        string   `json:"source"`
	APIKeyEnvVars []string `json:"apiKeyEnvVars"`
	AuthMethods   []string `json:"authMethods"`
	Models        []Model  `json:"models"`
}

// Catalog 是一次加载产生的不可变模型目录。
type Catalog struct {
	PiVersion      string
	GeneratedAt    *time.Time
	Providers      []Provider
	Revision       int64
	byID           map[string]*Provider
	providerConfig map[string]json.RawMessage
}

// Reader 是模型目录的当前读取入口；Catalog 与 Registry 都实现它。
type Reader interface {
	Snapshot() *Catalog
	Provider(string) (*Provider, bool)
	HasModel(string, string) bool
	ResolveThinking(string, string, *string) (string, error)
	SupportsImages(string, string) bool
	ModelsJSONFor(string) ([]byte, string)
}

// Options 是读取目录所需的参数。
type Options struct {
	NodeBin      string // node 的绝对路径
	PiAIDir      string
	PiPackageDir string // 自定义配置需要 Pi 主程序的 ModelRuntime
	PiVersion    string // pi-ai 的版本必须与之相等
	Path         string // 运行 node 时的 PATH
	ModelsJSON   []byte
	Revision     int64
}

// Load 运行内嵌脚本读取目录，并校验 pi-ai 版本与原生 models.json。
func Load(ctx context.Context, o Options) (*Catalog, error) {
	tmp, err := os.MkdirTemp("", "loopnow-catalog-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	scriptPath := filepath.Join(tmp, "catalog.mjs")
	if err := os.WriteFile(scriptPath, script, 0o600); err != nil {
		return nil, err
	}
	args := []string{scriptPath, o.PiAIDir}
	if len(o.ModelsJSON) > 0 {
		if o.PiPackageDir == "" {
			return nil, fmt.Errorf("读取自定义模型目录需要 Pi 主程序目录")
		}
		modelsPath := filepath.Join(tmp, "models.json")
		if err := os.WriteFile(modelsPath, o.ModelsJSON, 0o600); err != nil {
			return nil, err
		}
		args = append(args, modelsPath, o.PiPackageDir)
	}

	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, o.NodeBin, args...)
	// 空环境：只给 PATH 与临时 HOME，不传服务端的任何变量。
	cmd.Env = []string{"PATH=" + o.Path, "HOME=" + tmp, "PI_OFFLINE=1"}
	cmd.Dir = tmp
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("读取 Pi 模型目录失败：%w：%s", err, bytes.TrimSpace(stderr.Bytes()))
	}

	var raw struct {
		PiAIVersion    string                     `json:"piAIVersion"`
		GeneratedAt    *time.Time                 `json:"generatedAt"`
		Providers      []Provider                 `json:"providers"`
		ProviderConfig map[string]json.RawMessage `json:"providerConfigs"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &raw); err != nil {
		return nil, fmt.Errorf("解析 Pi 模型目录失败：%w", err)
	}
	if raw.PiAIVersion != o.PiVersion {
		return nil, fmt.Errorf("pi-ai 版本 %s 与期望的 Pi 版本 %s 不一致（%s）", raw.PiAIVersion, o.PiVersion, o.PiAIDir)
	}
	if len(raw.Providers) == 0 {
		return nil, fmt.Errorf("Pi 模型目录为空（%s）", o.PiAIDir)
	}
	for _, p := range raw.Providers {
		for _, m := range p.Models {
			if len(m.ThinkingLevels) == 0 || !slices.Contains(m.ThinkingLevels, m.DefaultThinkingLevel) {
				return nil, fmt.Errorf("Pi 模型思考等级元数据无效：%s/%s", p.ID, m.ID)
			}
		}
	}
	c := New(raw.PiAIVersion, raw.GeneratedAt, raw.Providers)
	c.Revision, c.providerConfig = o.Revision, raw.ProviderConfig
	return c, nil
}

// New 用给定数据构造目录；也供测试使用。
func New(piVersion string, generatedAt *time.Time, providers []Provider) *Catalog {
	c := &Catalog{PiVersion: piVersion, GeneratedAt: generatedAt, Providers: providers, byID: map[string]*Provider{}, providerConfig: map[string]json.RawMessage{}}
	sort.Slice(c.Providers, func(i, j int) bool { return c.Providers[i].ID < c.Providers[j].ID })
	for i := range c.Providers {
		p := &c.Providers[i]
		if p.Source == "" {
			p.Source = "builtin"
		}
		if p.AuthMethods == nil {
			p.AuthMethods = []string{}
			if len(p.APIKeyEnvVars) > 0 {
				p.AuthMethods = append(p.AuthMethods, "api_key")
			}
			if p.ID == "openai-codex" {
				p.AuthMethods = []string{"oauth"}
			}
		}
		if p.APIKeyEnvVars == nil {
			p.APIKeyEnvVars = []string{}
		}
		for j := range p.Models {
			if p.Models[j].ThinkingLevels == nil && !p.Models[j].Reasoning {
				p.Models[j].ThinkingLevels = []string{"off"}
				p.Models[j].DefaultThinkingLevel = "off"
			}
			if p.Models[j].Input == nil {
				p.Models[j].Input = []string{}
			}
		}
		c.byID[p.ID] = p
	}
	return c
}

func (c *Catalog) Snapshot() *Catalog { return c }

// ModelsJSONFor 返回只含所选 Provider 的原生配置及其稳定身份。
func (c *Catalog) ModelsJSONFor(provider string) ([]byte, string) {
	if c == nil {
		return nil, ""
	}
	raw, ok := c.providerConfig[provider]
	if !ok {
		return nil, ""
	}
	data, err := json.Marshal(map[string]map[string]json.RawMessage{"providers": {provider: raw}})
	if err != nil {
		return nil, ""
	}
	return data, ConfigIdentity(data)
}

// ConfigIdentity 是冻结 models.json 片段的稳定进程重建身份。
func ConfigIdentity(data []byte) string {
	if len(data) == 0 {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func (c *Catalog) ResolveThinking(provider, modelID string, selected *string) (string, error) {
	if c == nil {
		return "", fmt.Errorf("模型目录未配置")
	}
	if p, ok := c.Provider(provider); ok {
		for _, m := range p.Models {
			if m.ID == modelID {
				level := m.DefaultThinkingLevel
				if selected != nil {
					level = *selected
				}
				if level != "" && slices.Contains(m.ThinkingLevels, level) {
					return level, nil
				}
				return "", fmt.Errorf("模型 %s/%s 不支持该思考等级", provider, modelID)
			}
		}
	}
	return "", fmt.Errorf("模型目录中没有模型 %s/%s", provider, modelID)
}

// Provider 按 ID 查找提供方。
func (c *Catalog) Provider(id string) (*Provider, bool) {
	p, ok := c.byID[id]
	return p, ok
}

// HasModel 判断模型是否在目录中。
func (c *Catalog) HasModel(provider, modelID string) bool {
	p, ok := c.byID[provider]
	if !ok {
		return false
	}
	for _, m := range p.Models {
		if m.ID == modelID {
			return true
		}
	}
	return false
}

func (c *Catalog) SupportsImages(provider, modelID string) bool {
	if c == nil {
		return false
	}
	p, ok := c.byID[provider]
	if !ok {
		return false
	}
	for _, m := range p.Models {
		if m.ID == modelID {
			for _, input := range m.Input {
				if input == "image" {
					return true
				}
			}
		}
	}
	return false
}
