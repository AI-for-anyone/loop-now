package plugins

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"

	"loop-now/internal/server/apperr"
	"loop-now/internal/server/modelcatalog"
)

// 插件注册的模型提供方（plugins/SPEC.md §13）：安装或更新确认后用随包 Pi 加载插件的 Pi 扩展读出，随版本保存；
// 注册了 OAuth 登录、与 Pi 目录不同 id 的新提供方并入模型目录（source 为 plugin），Agent 授权该插件后可以选用。

// captureProviders 读出插件扩展注册的模型提供方；读取失败不阻止安装，原因随版本保存。
func (s *Service) captureProviders(ctx context.Context, dir string, m Manifest) (modelcatalog.Capture, *string) {
	empty := modelcatalog.Capture{Providers: []modelcatalog.CapturedProvider{}, Errors: []modelcatalog.CaptureError{}}
	if s.CaptureProviders == nil || len(m.Extensions) == 0 {
		return empty, nil
	}
	c, err := s.CaptureProviders(ctx, dir, m.Extensions)
	if err != nil {
		s.Log.Warn("读取插件注册的模型提供方失败", "plugin", m.ID, "err", err)
		msg := err.Error()
		return empty, &msg
	}
	return c, nil
}

// checkProviderConflicts 拒绝与其他已安装插件注册的同 id 提供方（只看会进模型目录的）。
func (s *Service) checkProviderConflicts(ctx context.Context, id string, c modelcatalog.Capture) error {
	mine := map[string]bool{}
	for _, p := range c.Providers {
		if p.CatalogEligible() {
			mine[p.ID] = true
		}
	}
	if len(mine) == 0 {
		return nil
	}
	others, err := s.installedProviders(ctx)
	if err != nil {
		return err
	}
	for _, o := range others {
		if o.PluginID != id && mine[o.Provider.ID] {
			return apperr.Conflict(apperr.CodeNameConflict, "插件 "+o.PluginID+" 已注册模型提供方 "+o.Provider.ID+"，不能再由插件 "+id+" 注册")
		}
	}
	return nil
}

// installedProviders 返回各已安装插件当前版本注册的、会进模型目录的提供方。
func (s *Service) installedProviders(ctx context.Context) ([]modelcatalog.PluginProvider, error) {
	rows, err := s.Store.CurrentPluginProviders(ctx)
	if err != nil {
		return nil, err
	}
	var out []modelcatalog.PluginProvider
	for _, r := range rows {
		var c modelcatalog.Capture
		if json.Unmarshal(r.Providers, &c) != nil {
			continue
		}
		for _, p := range c.Providers {
			if p.CatalogEligible() {
				out = append(out, modelcatalog.PluginProvider{PluginID: r.PluginID, Provider: p})
			}
		}
	}
	return out, nil
}

// PublishProviders 把已安装插件注册的提供方并入模型目录；服务启动时与安装、更新、卸载后调用。
func (s *Service) PublishProviders(ctx context.Context) error {
	if s.Models == nil {
		return nil
	}
	providers, err := s.installedProviders(ctx)
	if err != nil {
		return err
	}
	s.Models.SetPluginProviders(providers)
	return nil
}

func (s *Service) publishProviders(ctx context.Context) {
	if err := s.PublishProviders(ctx); err != nil {
		s.Log.Warn("把插件提供方并入模型目录失败", "err", err)
	}
}

// ProviderPlugin 返回注册了模型提供方 provider 的插件；不是插件提供方时 ok 为 false。
func ProviderPlugin(models modelcatalog.Reader, provider string) (string, bool) {
	if models == nil {
		return "", false
	}
	p, ok := models.Provider(provider)
	if !ok || p.Source != "plugin" || p.PluginID == nil {
		return "", false
	}
	return *p.PluginID, true
}

// providerViews 把读出的提供方变成接口里的样子；InCatalog 表示它此刻确实在模型目录里、归这个插件。
func (s *Service) providerViews(id string, c modelcatalog.Capture) []ProviderView {
	out := []ProviderView{}
	for _, p := range c.Providers {
		in := false
		if s.Models != nil {
			owner, ok := ProviderPlugin(s.Models, p.ID)
			in = ok && owner == id
		}
		out = append(out, ProviderView{ID: p.ID, Name: p.Name, Extension: p.Extension, OAuth: p.OAuth, OverridesBuiltin: p.OverridesBuiltin, InCatalog: in, Error: p.Error, ModelCount: len(p.Models)})
	}
	return out
}

// PluginOfPath 返回判断文件属于哪个已安装插件的函数（按各版本安装目录）；不属于任何插件时 ok 为 false。
func (s *Service) PluginOfPath(ctx context.Context) (func(string) (string, bool), error) {
	dirs, err := s.Store.PluginInstallDirs(ctx)
	if err != nil {
		return nil, err
	}
	return func(path string) (string, bool) {
		clean := filepath.Clean(path)
		for id, list := range dirs {
			for _, d := range list {
				if strings.HasPrefix(clean, filepath.Clean(d)+string(filepath.Separator)) {
					return id, true
				}
			}
		}
		return "", false
	}, nil
}
