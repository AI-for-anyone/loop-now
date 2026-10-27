package plugins

import (
	"context"
	"encoding/json"
	"maps"
	"path/filepath"
	"slices"
	"strings"

	"github.com/google/uuid"

	"loop-now/internal/server/agentconfig"
	"loop-now/internal/server/store"
)

// AgentAssets 返回 Agent 已授权插件交给 Pi 的东西（plugins/SPEC.md §2.1）：
// 项目 Agent 只取其项目已启用的插件；全局停用时为空。下次启动 Pi 时生效。
func (s *Service) AgentAssets(ctx context.Context, agentID uuid.UUID) (agentconfig.PluginAssets, error) {
	out := agentconfig.PluginAssets{}
	settings, err := s.Store.GetPluginSettings(ctx)
	if err != nil || settings.Disabled {
		return out, err
	}
	a, err := s.Store.GetAgent(ctx, agentID)
	if err != nil {
		return out, err
	}
	grants, err := s.Store.AgentPluginGrants(ctx, agentID)
	if err != nil || len(grants) == 0 {
		return out, err
	}
	out.MCPServers, out.Env = map[string]json.RawMessage{}, map[string]string{}
	for _, g := range grants {
		p, err := s.Store.GetPlugin(ctx, g.PluginID)
		if err != nil {
			return out, err
		}
		if a.ProjectID != nil {
			on, err := s.Store.PluginEnabledIn(ctx, p.ID, *a.ProjectID)
			if err != nil {
				return out, err
			}
			if !on {
				continue
			}
		}
		_, m, err := view(p)
		if err != nil {
			return out, err
		}
		abs := func(rel string) string { return filepath.Join(p.InstallDir, filepath.Clean(rel)) }
		for _, e := range m.Extensions {
			out.Extensions = append(out.Extensions, abs(e))
		}
		for _, sk := range m.Skills {
			out.Skills = append(out.Skills, abs(sk))
		}
		for _, b := range m.Bin {
			out.Bin = append(out.Bin, abs(b))
		}
		for _, name := range slices.Sorted(maps.Keys(m.MCPServers)) {
			raw, err := s.mcpServer(m.MCPServers[name], p.InstallDir, s.dataDir(p.ID), agentID)
			if err != nil {
				return out, err
			}
			out.MCPServers[p.ID+"-"+name] = raw
		}
		if err := s.agentEnv(ctx, out.Env, a, p, m, g); err != nil {
			return out, err
		}
	}
	return out, nil
}

// mcpServer 把一条 mcpServers 条目变成写进 mcp.json 的样子：${LOOP_NOW_PLUGIN_ROOT}、${LOOP_NOW_PLUGIN_DATA}、
// ${LOOP_NOW_AGENT_ID} 换成这个插件与 Agent 的值（每个插件不同，不能靠环境变量），以 ./ 或 ../ 开头的 command 与 args
// 换成以安装目录为基准的绝对路径；其余 ${...} 留给 Pi 按环境变量展开。
func (s *Service) mcpServer(raw json.RawMessage, root, data string, agent uuid.UUID) (json.RawMessage, error) {
	var srv map[string]any
	if err := json.Unmarshal(raw, &srv); err != nil {
		return nil, err
	}
	replace := strings.NewReplacer("${LOOP_NOW_PLUGIN_ROOT}", root, "${LOOP_NOW_PLUGIN_DATA}", data, "${LOOP_NOW_AGENT_ID}", agent.String())
	relative := func(v string) string {
		if strings.HasPrefix(v, "./") || strings.HasPrefix(v, "../") {
			return filepath.Join(root, filepath.Clean(v))
		}
		return v
	}
	var walk func(v any) any
	walk = func(v any) any {
		switch t := v.(type) {
		case string:
			return replace.Replace(t)
		case []any:
			for i := range t {
				t[i] = walk(t[i])
			}
		case map[string]any:
			for k := range t {
				t[k] = walk(t[k])
			}
		}
		return v
	}
	walk(srv)
	if c, ok := srv["command"].(string); ok {
		srv["command"] = relative(c)
	}
	if args, ok := srv["args"].([]any); ok {
		for i, a := range args {
			if v, ok := a.(string); ok {
				args[i] = relative(v)
			}
		}
	}
	return json.Marshal(srv)
}

// envName 把插件 id 或账号种类、字段名变成环境变量名的一段：大写，非字母数字换成下划线。
func envName(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z':
			return r - 'a' + 'A'
		case r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			return r
		}
		return '_'
	}, s)
}

// agentEnv 写入插件给这个 Agent 的环境变量：LOOP_NOW_PLUGIN_<ID>_CONFIG 是生效的配置（项目值覆盖全局值，不含 secret 字段）；
// 账号按种类注入，单账号种类为 LOOP_NOW_ACCOUNT_<KIND>_<FIELD>，多账号种类为 LOOP_NOW_ACCOUNTS_<KIND>（JSON 数组，每项带 name）。
func (s *Service) agentEnv(ctx context.Context, env map[string]string, a store.Agent, p store.Plugin, m Manifest, g store.AgentPluginGrant) error {
	global, err := s.Store.PluginConfig(ctx, p.ID, nil)
	if err != nil {
		return err
	}
	cfg := maps.Clone(nonNilMap(global.Values))
	if a.ProjectID != nil {
		proj, err := s.Store.PluginConfig(ctx, p.ID, a.ProjectID)
		if err != nil {
			return err
		}
		maps.Copy(cfg, proj.Values)
	}
	for _, f := range secretFields(m.ConfigSchema) {
		delete(cfg, f)
	}
	b, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	env["LOOP_NOW_PLUGIN_"+envName(p.ID)+"_CONFIG"] = string(b)
	if a.ProjectID == nil {
		return nil
	}
	multi := map[string][]map[string]any{}
	for _, id := range g.AccountIDs {
		acc, err := s.Store.GetPluginAccount(ctx, *a.ProjectID, p.ID, id)
		if err != nil {
			if store.IsNotFound(err) {
				continue // 账号已删除：授权里的引用随删除移除，这里只是并发时的保险
			}
			return err
		}
		k, ok := accountKind(m, acc.Kind)
		if !ok {
			continue
		}
		if k.Multiple {
			item := maps.Clone(acc.Values)
			item["name"] = acc.Name
			multi[acc.Kind] = append(multi[acc.Kind], item)
			continue
		}
		for f, v := range acc.Values {
			key := "LOOP_NOW_ACCOUNT_" + envName(acc.Kind) + "_" + envName(f)
			if _, set := env[key]; set {
				continue // 单账号种类只取第一个
			}
			if str, ok := v.(string); ok {
				env[key] = str
			} else if vb, err := json.Marshal(v); err == nil {
				env[key] = string(vb)
			}
		}
	}
	for kind, items := range multi {
		b, err := json.Marshal(items)
		if err != nil {
			return err
		}
		env["LOOP_NOW_ACCOUNTS_"+envName(kind)] = string(b)
	}
	return nil
}
