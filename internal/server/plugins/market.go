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

	"loop-now/internal/pluginmanifest"
	"loop-now/internal/server/apperr"
)

// Plugin markets (plugins/SPEC.md §11): a market is a loop-now-market.json reachable over HTTPS that lists plugin
// ids, sources and versions. The official market comes from the server config; other markets are in the global
// plugin settings. Installing from a market fetches the entry's git or npm source.

// marketFile is loop-now-market.json.
type marketFile struct {
	Name    *string       `json:"name"`
	Plugins []marketEntry `json:"plugins"`
}

type marketEntry struct {
	ID          string  `json:"id"`
	Source      string  `json:"source"`
	DisplayName Text    `json:"displayName"`
	Description Text    `json:"description"`
	Verified    bool    `json:"verified"`
	Version     *string `json:"version"`
	// ProviderState declares which state of the model providers registered by this plugin travels with the project's
	// accounts (plugins/SPEC.md §13). A third-party Pi package without a loopNow section declares it here; it takes
	// precedence over the package's own loopNow.providerState.
	ProviderState map[string]pluginmanifest.ProviderState `json:"providerState,omitempty"`
}

// MarketEntry is the API's PluginMarketEntry.
type MarketEntry struct {
	ID               string                                  `json:"id"`
	DisplayName      Text                                    `json:"displayName"`
	Description      Text                                    `json:"description"`
	Source           string                                  `json:"source"`
	Version          *string                                 `json:"version"`
	Verified         bool                                    `json:"verified"`
	InstalledVersion *string                                 `json:"installedVersion"`
	UpdateAvailable  bool                                    `json:"updateAvailable"`
	ProviderState    map[string]pluginmanifest.ProviderState `json:"providerState"`
}

// Market is the API's PluginMarket.
type Market struct {
	URL      string        `json:"url"`
	Name     *string       `json:"name"`
	Official bool          `json:"official"`
	Error    *string       `json:"error"`
	Plugins  []MarketEntry `json:"plugins"`
}

// maxMarketBytes is the size limit of one market file.
const maxMarketBytes = 4 << 20

// marketURLs returns the markets to read: the official market first; a market in the settings that is the same as
// it is not repeated.
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

// Markets reads each market in turn and marks installed versions and entries with an update available; when one
// market fails to load, only its error says so.
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
			entry := MarketEntry{ID: e.ID, DisplayName: e.DisplayName, Description: e.Description, Source: e.Source, Version: e.Version, Verified: e.Verified, ProviderState: e.ProviderState}
			if entry.ProviderState == nil {
				entry.ProviderState = map[string]pluginmanifest.ProviderState{}
			}
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

// fetchMarket reads and checks one market file; if any entry lacks an id or has a malformed source, the whole file
// counts as invalid.
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
		for _, err := range []error{e.DisplayName.Check("displayName"), e.Description.Check("description"), pluginmanifest.CheckProviderState("providerState", e.ProviderState)} {
			if err != nil {
				return file, fmt.Errorf("市场条目 %s：%s", e.ID, err.Error())
			}
		}
	}
	return file, nil
}

// marketSource turns a market entry's source notation into a git or npm source; when version is not empty, that
// version is installed (only npm entries can choose one).
func marketSource(e marketEntry, version string) (Source, error) {
	kind, loc, ok := strings.Cut(e.Source, ":")
	if !ok || loc == "" {
		return Source{}, apperr.Invalid("source", "来源须写成 git:<地址>[@ref] 或 npm:<包规格>")
	}
	switch kind {
	case SourceGit:
		src := Source{Kind: SourceGit, Location: loc}
		// A git address may itself contain @ (git@host:repo), so only an @ after the last / and : counts as the ref.
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

// resolveMarket finds the entry in the market and returns the actual source and whether it needs no confirmation
// (the official market or a verified entry).
func (s *Service) resolveMarket(ctx context.Context, src Source) (Source, bool, *marketEntry, error) {
	if src.Market == "" || src.ID == "" {
		return Source{}, false, nil, apperr.Invalid("source", "市场来源须给出 market 与 id")
	}
	file, err := s.fetchMarket(ctx, src.Market)
	if err != nil {
		return Source{}, false, nil, sourceFailed("%s", err.Error())
	}
	i := slices.IndexFunc(file.Plugins, func(e marketEntry) bool { return e.ID == src.ID })
	if i < 0 {
		return Source{}, false, nil, apperr.NotFound("市场里的插件 " + src.ID)
	}
	e := file.Plugins[i]
	out, err := marketSource(e, src.Version)
	return out, src.Market == s.OfficialMarket || e.Verified, &e, err
}

func apperrMessage(err error) string {
	if e, ok := apperr.As(err); ok {
		return e.Message
	}
	return err.Error()
}

// compareVersions compares two dotted version numbers (optionally with a v prefix and a -prerelease suffix):
// numeric parts compare by value, and a release is higher than a prerelease of the same number.
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
