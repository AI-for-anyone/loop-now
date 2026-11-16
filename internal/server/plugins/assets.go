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

// AgentAssets returns what the plugins granted to the Agent hand to Pi (plugins/SPEC.md §2.1): a project Agent only
// gets the plugins enabled in its project; it is empty when plugins are disabled globally. Changes take effect the
// next time Pi starts.
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
		out.IDs = append(out.IDs, p.ID)
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

// mcpServer turns one mcpServers entry into the form written to mcp.json: ${LOOP_NOW_PLUGIN_ROOT},
// ${LOOP_NOW_PLUGIN_DATA} and ${LOOP_NOW_AGENT_ID} are replaced with the values for this plugin and Agent (they differ
// per plugin, so environment variables cannot carry them); a command or args starting with ./ or ../ become absolute
// paths based on the install directory; any other ${...} is left for Pi to expand from environment variables.
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

// envName turns a plugin id, account kind or field name into one segment of an environment variable name: upper
// case, with every non-alphanumeric character replaced by an underscore.
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

// agentEnv writes the plugin's environment variables for this Agent: LOOP_NOW_PLUGIN_<ID>_CONFIG is the effective
// config (project values override global values; secret fields are excluded). Accounts are injected per kind: a
// single-account kind as LOOP_NOW_ACCOUNT_<KIND>_<FIELD>, a multi-account kind as LOOP_NOW_ACCOUNTS_<KIND> (a JSON
// array, each item with a name).
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
				continue // account deleted: the grant's reference goes with it; this only guards against races
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
				continue // a single-account kind only takes the first one
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
