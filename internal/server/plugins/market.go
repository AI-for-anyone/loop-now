package plugins

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"loop-now/internal/server/apperr"
)

// 插件市场（plugins/SPEC.md §11）：市场是 HTTPS 可取到的 loop-now-market.json，列出插件 id、来源与版本。
// 官方市场由服务端配置给出，其余市场在全局插件设置里；从市场安装时按条目的 git 或 npm 来源获取。

// marketFile 是 loop-now-market.json。
type marketFile struct {
	Name    *string       `json:"name"`
	Plugins []marketEntry `json:"plugins"`
}

type marketEntry struct {
	ID          string  `json:"id"`
	Source      string  `json:"source"`
	DisplayName string  `json:"displayName"`
	Description string  `json:"description"`
	Verified    bool    `json:"verified"`
	Version     *string `json:"version"`
}

// MarketEntry 是接口里的 PluginMarketEntry。
type MarketEntry struct {
	ID               string  `json:"id"`
	DisplayName      string  `json:"displayName"`
	Description      string  `json:"description"`
	Source           string  `json:"source"`
	Version          *string `json:"version"`
	Verified         bool    `json:"verified"`
	InstalledVersion *string `json:"installedVersion"`
	UpdateAvailable  bool    `json:"updateAvailable"`
}

// Market 是接口里的 PluginMarket。
type Market struct {
	URL      string        `json:"url"`
	Name     *string       `json:"name"`
	Official bool          `json:"official"`
	Error    *string       `json:"error"`
	Plugins  []MarketEntry `json:"plugins"`
}

// maxMarketBytes 是一个市场文件的大小上限。
const maxMarketBytes = 4 << 20

// marketURLs 返回要读取的市场：官方市场在前，设置里与它相同的不重复。
func (s *Service) marketURLs(ctx context.Context) ([]string, error) {
	settings, err := s.Store.GetPluginSettings(ctx)
	if err != nil {
		return nil, err
	}
	var out []string
	if s.OfficialMarket != "" {
		out = append(out, s.OfficialMarket)
	}
	for _, u := range settings.Markets {
		if !slices.Contains(out, u) {
			out = append(out, u)
		}
	}
	return out, nil
}

// Markets 依次读取各市场，标出已安装的版本与可更新的条目；某个市场读取失败时只在它的 error 里说明。
func (s *Service) Markets(ctx context.Context) ([]Market, error) {
	urls, err := s.marketURLs(ctx)
	if err != nil {
		return nil, err
	}
	installed, err := s.Store.ListPlugins(ctx)
	if err != nil {
		return nil, err
	}
	versions := map[string]string{}
	for _, p := range installed {
		versions[p.ID] = p.Version
	}
	out := []Market{}
	for _, u := range urls {
		m := Market{URL: u, Official: u == s.OfficialMarket, Plugins: []MarketEntry{}}
		file, err := s.fetchMarket(ctx, u)
		if err != nil {
			msg := err.Error()
			m.Error = &msg
			out = append(out, m)
			continue
		}
		m.Name = file.Name
		for _, e := range file.Plugins {
			entry := MarketEntry{ID: e.ID, DisplayName: e.DisplayName, Description: e.Description, Source: e.Source, Version: e.Version, Verified: e.Verified}
			if v, ok := versions[e.ID]; ok {
				entry.InstalledVersion = &v
				entry.UpdateAvailable = e.Version != nil && compareVersions(*e.Version, v) > 0
			}
			m.Plugins = append(m.Plugins, entry)
		}
		out = append(out, m)
	}
	return out, nil
}

// fetchMarket 读取并检查一个市场文件；条目缺少 id 或来源写法不对的整份视为不合法。
func (s *Service) fetchMarket(ctx context.Context, raw string) (marketFile, error) {
	var file marketFile
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return file, fmt.Errorf("市场地址须是 https 地址")
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return file, err
	}
	client := s.MarketClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return file, fmt.Errorf("读取市场失败：%w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return file, fmt.Errorf("读取市场失败：HTTP %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxMarketBytes+1))
	if err != nil {
		return file, fmt.Errorf("读取市场失败：%w", err)
	}
	if len(b) > maxMarketBytes {
		return file, fmt.Errorf("市场文件超过 4 MiB")
	}
	if err := json.Unmarshal(b, &file); err != nil {
		return file, fmt.Errorf("市场文件不是合法的 JSON：%w", err)
	}
	if file.Plugins == nil {
		file.Plugins = []marketEntry{}
	}
	for _, e := range file.Plugins {
		if e.ID == "" {
			return file, fmt.Errorf("市场里有缺少 id 的条目")
		}
		if _, err := marketSource(e, ""); err != nil {
			return file, fmt.Errorf("市场条目 %s：%s", e.ID, apperrMessage(err))
		}
	}
	return file, nil
}

// marketSource 把市场条目的来源写法变成 git 或 npm 来源；version 不为空时安装这个版本（只有 npm 条目可以指定）。
func marketSource(e marketEntry, version string) (Source, error) {
	kind, loc, ok := strings.Cut(e.Source, ":")
	if !ok || loc == "" {
		return Source{}, apperr.Invalid("source", "来源须写成 git:<地址>[@ref] 或 npm:<包规格>")
	}
	switch kind {
	case SourceGit:
		src := Source{Kind: SourceGit, Location: loc}
		// git 地址本身可能含 @（git@host:repo），只把最后一个 / 与 : 之后的 @ 视为 ref。
		if i := strings.LastIndex(loc, "@"); i > max(strings.LastIndex(loc, "/"), strings.LastIndex(loc, ":")) {
			ref := loc[i+1:]
			src.Location, src.Ref = loc[:i], &ref
		}
		if version != "" && (e.Version == nil || *e.Version != version) {
			return Source{}, apperr.Invalid("source.version", "市场里的 git 条目只能安装市场给出的版本")
		}
		return src, nil
	case SourceNPM:
		if version != "" {
			name := loc
			if i := strings.LastIndex(loc, "@"); i > 0 {
				name = loc[:i]
			}
			loc = name + "@" + version
		}
		return Source{Kind: SourceNPM, Location: loc}, nil
	}
	return Source{}, apperr.Invalid("source", "来源须写成 git:<地址>[@ref] 或 npm:<包规格>")
}

// resolveMarket 在市场里找到条目，返回实际来源与是否免确认（官方市场或已验证的条目）。
func (s *Service) resolveMarket(ctx context.Context, src Source) (Source, bool, error) {
	if src.Market == "" || src.ID == "" {
		return Source{}, false, apperr.Invalid("source", "市场来源须给出 market 与 id")
	}
	file, err := s.fetchMarket(ctx, src.Market)
	if err != nil {
		return Source{}, false, sourceFailed("%s", err.Error())
	}
	i := slices.IndexFunc(file.Plugins, func(e marketEntry) bool { return e.ID == src.ID })
	if i < 0 {
		return Source{}, false, apperr.NotFound("市场里的插件 " + src.ID)
	}
	e := file.Plugins[i]
	out, err := marketSource(e, src.Version)
	return out, src.Market == s.OfficialMarket || e.Verified, err
}

func apperrMessage(err error) string {
	if e, ok := apperr.As(err); ok {
		return e.Message
	}
	return err.Error()
}

// compareVersions 比较两个点分版本号（可带 v 前缀与 -预发布后缀）：数字段按数值比较，正式版高于同号的预发布版。
func compareVersions(a, b string) int {
	split := func(v string) ([]string, string) {
		v = strings.TrimPrefix(v, "v")
		v, _, _ = strings.Cut(v, "+")
		core, pre, _ := strings.Cut(v, "-")
		return strings.Split(core, "."), pre
	}
	ac, ap := split(a)
	bc, bp := split(b)
	for i := range max(len(ac), len(bc)) {
		x, y := "0", "0"
		if i < len(ac) {
			x = ac[i]
		}
		if i < len(bc) {
			y = bc[i]
		}
		xn, xe := strconv.Atoi(x)
		yn, ye := strconv.Atoi(y)
		switch {
		case xe == nil && ye == nil && xn != yn:
			if xn < yn {
				return -1
			}
			return 1
		case (xe != nil || ye != nil) && x != y:
			return strings.Compare(x, y)
		}
	}
	switch {
	case ap == bp:
		return 0
	case ap == "":
		return 1
	case bp == "":
		return -1
	}
	return strings.Compare(ap, bp)
}
