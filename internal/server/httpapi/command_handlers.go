package httpapi

import (
	"encoding/json"
	"net/http"
	"time"
)

// agentCommand 是接口里的 AgentCommand：对话页 "/" 菜单里的一条命令。
type agentCommand struct {
	Name              string          `json:"name"`
	Source            string          `json:"source"`
	PiSource          string          `json:"piSource"`
	Description       *string         `json:"description"`
	PluginID          *string         `json:"pluginId"`
	PluginDisplayName json.RawMessage `json:"pluginDisplayName"`
}

// listAgentCommands 列出对话页可用的 Pi 命令（plugins/SPEC.md §14）：Agent 上次启动 Pi 时读到的扩展命令、提示模板与技能命令，
// 按命令来源文件所在的安装目录标出所属插件。不为列命令专门启动 Pi，从没启动过时列表为空。
func (s *Server) listAgentCommands(w http.ResponseWriter, r *http.Request) {
	id, err := pathUUID(r, "agentId")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if _, err := s.Store.GetAgent(r.Context(), id); err != nil {
		s.fail(w, r, err)
		return
	}
	out := struct {
		CapturedAt *time.Time     `json:"capturedAt"`
		Commands   []agentCommand `json:"commands"`
	}{Commands: []agentCommand{}}
	if s.PiCommands != nil {
		cached, err := s.PiCommands(id)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		if cached != nil {
			out.CapturedAt = &cached.CapturedAt
			owner := func(string) (string, bool) { return "", false }
			names := map[string]json.RawMessage{}
			if s.Plugins != nil {
				if owner, err = s.Plugins.PluginOfPath(r.Context()); err != nil {
					s.fail(w, r, err)
					return
				}
				views, err := s.Plugins.List(r.Context())
				if err != nil {
					s.fail(w, r, err)
					return
				}
				for _, v := range views {
					names[v.ID], _ = json.Marshal(v.DisplayName)
				}
			}
			for _, c := range cached.Commands {
				item := agentCommand{Name: c.Name, Source: "pi", PiSource: c.Source, Description: c.Description, PluginDisplayName: json.RawMessage("null")}
				if plugin, ok := owner(c.Path); ok {
					item.PluginID = &plugin
					if n, ok := names[plugin]; ok {
						item.PluginDisplayName = n
					}
				}
				out.Commands = append(out.Commands, item)
			}
		}
	}
	writeJSON(w, http.StatusOK, out)
}
