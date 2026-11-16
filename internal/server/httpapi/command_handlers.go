package httpapi

import (
	"encoding/json"
	"net/http"
	"time"

	"loop-now/internal/server/agentruntime"
)

// agentCommand is AgentCommand in the API: one command in the "/" menu of the conversation page.
type agentCommand struct {
	Name              string          `json:"name"`
	Source            string          `json:"source"`
	PiSource          string          `json:"piSource"`
	Description       json.RawMessage `json:"description"` // LocalizedText or null: a string for Pi commands, a Chinese/English object for platform commands
	PluginID          *string         `json:"pluginId"`
	PluginDisplayName json.RawMessage `json:"pluginDisplayName"`
	Usage             *string         `json:"usage"`
	Available         bool            `json:"available"`
	UnavailableReason *string         `json:"unavailableReason"`
}

// listAgentCommands lists the commands of the "/" menu on the conversation page: first the platform's built-in commands
// (marked as usable right now or not, by runtime and session state), then the Pi commands (plugins/SPEC.md §14): the
// extension commands, prompt templates and Skill commands Pi reported the last time the Agent started it, each tagged
// with its plugin based on the install directory of the command's source file. Pi is never started just to list
// commands; if it has never been started there are no Pi commands.
func (s *Server) listAgentCommands(w http.ResponseWriter, r *http.Request) {
	id, err := pathUUID(r, "agentId")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	agent, err := s.Store.GetAgent(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := struct {
		Runtime    string         `json:"runtime"`
		CapturedAt *time.Time     `json:"capturedAt"`
		Commands   []agentCommand `json:"commands"`
	}{Runtime: agentruntime.ForProvider(agent.ModelProvider), Commands: []agentCommand{}}
	platform, err := s.Conv.PlatformCommands(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	for _, c := range platform {
		usage := c.Usage
		desc, err := json.Marshal(c.Description)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		item := agentCommand{Name: c.Name, Source: "platform", Description: desc, PluginDisplayName: json.RawMessage("null"), Usage: &usage, Available: c.Available}
		if c.UnavailableReason != "" {
			reason := c.UnavailableReason
			item.UnavailableReason = &reason
		}
		out.Commands = append(out.Commands, item)
	}
	// Only Pi Agents have Pi commands; after switching to another runtime, the commands cached from the earlier Pi
	// start are no longer listed either.
	if s.PiCommands != nil && out.Runtime == agentruntime.Pi {
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
				desc := json.RawMessage("null")
				if c.Description != nil {
					if desc, err = json.Marshal(*c.Description); err != nil {
						s.fail(w, r, err)
						return
					}
				}
				item := agentCommand{Name: c.Name, Source: "pi", PiSource: c.Source, Description: desc, PluginDisplayName: json.RawMessage("null"), Available: true}
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
