package modelcatalog

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"time"

	"loop-now/internal/procgroup"
)

//go:embed providers.mjs
var providersScript []byte

// CapturedProvider 是插件的 Pi 扩展注册的一个模型提供方（plugins/SPEC.md §13）。
type CapturedProvider struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Extension 是注册它的扩展文件，相对插件目录。
	Extension        string  `json:"extension"`
	OAuth            bool    `json:"oauth"`
	APIKey           bool    `json:"apiKey"`
	OverridesBuiltin bool    `json:"overridesBuiltin"`
	Error            *string `json:"error"`
	Models           []Model `json:"models"`
}

// CaptureError 是一个扩展文件加载失败的原因（扩展原文，已截断）。
type CaptureError struct {
	Path  string `json:"path"`
	Error string `json:"error"`
}

// Capture 是一次读取的结果。
type Capture struct {
	Providers []CapturedProvider `json:"providers"`
	Errors    []CaptureError     `json:"errors"`
}

// CatalogEligible 报告这个提供方是否进模型目录：注册了 OAuth 登录的新提供方，注册时没有出错。
// 覆盖内置提供方（与内置或 models.json 同 id）的只记录，不进目录。
func (p CapturedProvider) CatalogEligible() bool {
	return p.OAuth && !p.OverridesBuiltin && p.Error == nil && len(p.Models) > 0
}

// CaptureProviders 用固定版本 Pi 自己的扩展加载器加载 dir 下的扩展文件，读出它们注册的模型提供方。
// 运行扩展代码，所以只能在用户确认安装之后调用。空环境、临时 HOME 与 PI_CODING_AGENT_DIR、PI_OFFLINE=1，
// 扩展加载时的副作用只落在临时目录，结束即删除。extensions 是相对 dir 的路径。
func CaptureProviders(ctx context.Context, o Options, dir string, extensions []string) (Capture, error) {
	out := Capture{Providers: []CapturedProvider{}, Errors: []CaptureError{}}
	if len(extensions) == 0 {
		return out, nil
	}
	if o.PiPackageDir == "" {
		return out, fmt.Errorf("读取插件提供方需要 Pi 主程序目录")
	}
	tmp, err := os.MkdirTemp("", "loopnow-providers-")
	if err != nil {
		return out, err
	}
	defer os.RemoveAll(tmp)
	home := filepath.Join(tmp, "home")
	for _, d := range []string{home, filepath.Join(home, "agent"), filepath.Join(tmp, "tmp")} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return out, err
		}
	}
	scriptPath := filepath.Join(tmp, "providers.mjs")
	if err := os.WriteFile(scriptPath, providersScript, 0o600); err != nil {
		return out, err
	}
	args := []string{scriptPath, o.PiPackageDir, o.PiAIDir}
	abs := map[string]string{}
	for _, rel := range extensions {
		p := filepath.Join(dir, filepath.Clean(rel))
		abs[p] = rel
		args = append(args, p)
	}

	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, o.NodeBin, args...)
	cmd.Env = []string{"PATH=" + o.Path, "HOME=" + home, "USERPROFILE=" + home, "PI_CODING_AGENT_DIR=" + filepath.Join(home, "agent"), "TMPDIR=" + filepath.Join(tmp, "tmp"), "PI_OFFLINE=1", "NODE_ENV=production"}
	cmd.Dir = home
	procgroup.Configure(cmd)
	cmd.Cancel = procgroup.Cancel(cmd)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return out, fmt.Errorf("读取插件提供方超时")
		}
		return out, fmt.Errorf("读取插件提供方失败：%w：%s", err, clip(bytes.TrimSpace(stderr.Bytes()), 2000))
	}
	var raw struct {
		PiVersion string             `json:"piVersion"`
		Providers []CapturedProvider `json:"providers"`
		Errors    []CaptureError     `json:"errors"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &raw); err != nil {
		return out, fmt.Errorf("解析插件提供方失败：%w", err)
	}
	if raw.PiVersion != o.PiVersion {
		return out, fmt.Errorf("Pi 版本 %s 与期望的 %s 不一致", raw.PiVersion, o.PiVersion)
	}
	rel := func(p string) string {
		if r, ok := abs[p]; ok {
			return r
		}
		if r, err := filepath.Rel(dir, p); err == nil {
			return filepath.ToSlash(r)
		}
		return p
	}
	for _, p := range raw.Providers {
		p.Extension = rel(p.Extension)
		if p.Models == nil {
			p.Models = []Model{}
		}
		for i := range p.Models {
			m := &p.Models[i]
			if len(m.ThinkingLevels) == 0 || !slices.Contains(m.ThinkingLevels, m.DefaultThinkingLevel) {
				msg := fmt.Sprintf("模型 %s 的思考等级元数据无效", m.ID)
				p.Error = &msg
			}
			if m.Input == nil {
				m.Input = []string{}
			}
		}
		out.Providers = append(out.Providers, p)
	}
	for _, e := range raw.Errors {
		out.Errors = append(out.Errors, CaptureError{Path: rel(e.Path), Error: e.Error})
	}
	return out, nil
}

func clip(b []byte, max int) string {
	if len(b) > max {
		return string(b[:max]) + "…"
	}
	return string(b)
}

// PluginProvider 是要并入模型目录的一个插件提供方。
type PluginProvider struct {
	PluginID string
	Provider CapturedProvider
}

// withPlugins 返回 base 加上插件提供方的目录：与 base 同 id 的插件提供方跳过（内置与 models.json 优先），
// 插件之间同 id 时先给出的为准。没有可并入的提供方时原样返回 base。
func withPlugins(base *Catalog, plugins []PluginProvider) *Catalog {
	if base == nil {
		return nil
	}
	var extra []Provider
	seen := map[string]bool{}
	for _, pp := range plugins {
		p := pp.Provider
		if !p.CatalogEligible() || seen[p.ID] {
			continue
		}
		if _, exists := base.byID[p.ID]; exists {
			continue
		}
		seen[p.ID] = true
		id := pp.PluginID
		extra = append(extra, Provider{ID: p.ID, Name: p.Name, Source: "plugin", PluginID: &id, AuthMethods: []string{"oauth"}, APIKeyEnvVars: []string{}, Models: slices.Clone(p.Models)})
	}
	if len(extra) == 0 {
		return base
	}
	c := New(base.PiVersion, base.GeneratedAt, append(slices.Clone(base.Providers), extra...))
	c.Revision, c.providerConfig = base.Revision, base.providerConfig
	return c
}
