package httpapi_test

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/executor"
	"loop-now/internal/server/modelcatalog"
	"loop-now/internal/server/store"
)

type commandList struct {
	Runtime    string     `json:"runtime"`
	CapturedAt *time.Time `json:"capturedAt"`
	Commands   []struct {
		Name              string          `json:"name"`
		Source            string          `json:"source"`
		PiSource          string          `json:"piSource"`
		Description       json.RawMessage `json:"description"`
		PluginID          *string         `json:"pluginId"`
		PluginDisplayName json.RawMessage `json:"pluginDisplayName"`
		Usage             *string         `json:"usage"`
		Available         bool            `json:"available"`
		UnavailableReason *string         `json:"unavailableReason"`
	} `json:"commands"`
}

// platformAvailability writes the platform commands in the list as name=available or name:reason.
func platformAvailability(l commandList) []string {
	var out []string
	for _, c := range l.Commands {
		if c.Source != "platform" {
			continue
		}
		if c.Available {
			out = append(out, c.Name)
		} else {
			out = append(out, c.Name+":"+*c.UnavailableReason)
		}
	}
	return out
}

// The platform's built-in commands come first, marked as usable right now or not by runtime and session state.
func TestListAgentCommandsPlatform(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	agent, path := e.newAgent("Command list")
	get := func() commandList { return decode[commandList](t, e.expect(200, "GET", path+"/commands", nil)) }
	l := get()
	if l.Runtime != "pi" {
		t.Fatalf("Pi Agent: %s", l.Runtime)
	}
	if got := strings.Join(platformAvailability(l), " "); got != "session new compact:session_none reload copy export:session_none resume" {
		t.Fatalf("no session yet: %s", got)
	}
	if c := l.Commands[2]; c.Usage == nil || *c.Usage != "/compact [instructions]" || c.PiSource != "" {
		t.Fatalf("platform command fields: %+v", c)
	}
	// Every platform command has both a Chinese and an English description, and the placeholder words in brackets in
	// the usage appear in both descriptions.
	for _, c := range l.Commands {
		var desc struct {
			Zh string `json:"zh-CN"`
			En string `json:"en-US"`
		}
		if err := json.Unmarshal(c.Description, &desc); err != nil || desc.Zh == "" || desc.En == "" {
			t.Fatalf("%s should have Chinese and English descriptions: %s %v", c.Name, c.Description, err)
		}
		for _, m := range regexp.MustCompile(`\[([^\]]+)\]`).FindAllStringSubmatch(*c.Usage, -1) {
			for _, word := range strings.Split(m[1], "|") {
				if !strings.Contains(desc.Zh, word) || !strings.Contains(desc.En, word) {
					t.Fatalf("the descriptions of %s do not mention %s from the usage: %+v", c.Name, word, desc)
				}
			}
		}
	}
	if c := l.Commands[6]; *c.Usage != "/resume [session-id]" {
		t.Fatalf("usage of /resume: %s", *c.Usage)
	}
	if err := e.st.RecordSessionFile(ctx, agent.ID, "/managed/s.jsonl"); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(platformAvailability(get()), " "); got != "session new compact reload copy export resume" {
		t.Fatalf("all usable with a session: %s", got)
	}
	if err := e.st.MarkSessionMissing(ctx, agent.ID, nil); err != nil {
		t.Fatal(err)
	}
	// A lost session starts over: until the next message there is no session to compact or export.
	if got := strings.Join(platformAvailability(get()), " "); got != "session new compact:session_none reload copy export:session_none resume" {
		t.Fatalf("session lost: %s", got)
	}
	reg := modelcatalog.NewRegistry(e.api.Models.Snapshot())
	e.api.Models, e.api.Config.Models = reg, reg
	e.expect(200, "PATCH", path, map[string]any{"model": map[string]string{"provider": "claude-code", "modelId": "sonnet"}})
	if got := strings.Join(platformAvailability(get()), " "); got != "session new compact:runtime_unsupported reload copy export:runtime_unsupported resume:runtime_unsupported" {
		t.Fatalf("Claude Code: %s", got)
	}
	// After switching to Claude Code, the commands cached from the earlier Pi start are no longer listed, capturedAt is
	// null, and runtime states that it is not Pi.
	e.api.PiCommands = func(uuid.UUID) (*executor.PiCommands, error) {
		return &executor.PiCommands{CapturedAt: time.Now().UTC(), Commands: []executor.PiCommand{{Name: "stale", Source: "extension"}}}, nil
	}
	if l := get(); l.Runtime != "claude-code" || l.CapturedAt != nil || len(l.Commands) != 7 {
		t.Fatalf("Claude Code should not list Pi commands: %+v", l)
	}
}

// GET /agents/{id}/commands: only platform commands while no Pi commands have been read; once read, each is tagged with
// its plugin and plugin display name based on the install directory of its source file.
func TestListAgentCommands(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	agent := decode[apitypes.Agent](t, e.expect(201, "POST", e.agents(), map[string]any{"name": "Assistant", "model": map[string]string{"provider": "anthropic", "modelId": "claude-x"}}))
	path := "/agents/" + agent.ID.String() + "/commands"
	type list = commandList
	const platform = 7
	if l := decode[list](t, e.expect(200, "GET", path, nil)); l.CapturedAt != nil || len(l.Commands) != platform {
		t.Fatalf("without PiCommands only platform commands are listed: %+v", l)
	}
	cached := map[uuid.UUID]*executor.PiCommands{}
	e.api.PiCommands = func(id uuid.UUID) (*executor.PiCommands, error) { return cached[id], nil }
	if l := decode[list](t, e.expect(200, "GET", path, nil)); l.CapturedAt != nil || len(l.Commands) != platform {
		t.Fatalf("never started: %+v", l)
	}
	if err := e.st.InstallPlugin(ctx, store.Plugin{ID: "grokish", DisplayName: "Grokish", Source: json.RawMessage(`{"kind":"local","location":"/opt/grokish"}`), Version: "0.1.0",
		Manifest: json.RawMessage(`{"id":"grokish","name":"pi-grokish","version":"0.1.0","displayName":"Grokish"}`), InstallDir: "/opt/grokish"}, "d", store.PluginVersionExtras{Providers: json.RawMessage(`{"providers":[],"errors":[]}`)}); err != nil {
		t.Fatal(err)
	}
	desc := "Manage accounts"
	cached[agent.ID] = &executor.PiCommands{CapturedAt: time.Now().UTC(), Commands: []executor.PiCommand{
		{Name: "grok-cli-accounts", Description: &desc, Source: "extension", Path: "/opt/grokish/src/index.ts"},
		{Name: "skill:loopctl", Source: "skill", Path: "/work/agents/x/skills/loopctl"},
		{Name: "near-miss", Source: "extension", Path: "/opt/grokish-other/index.ts"},
	}}
	all := decode[list](t, e.expect(200, "GET", path, nil))
	if all.CapturedAt == nil || len(all.Commands) != platform+3 {
		t.Fatalf("commands: %+v", all)
	}
	l := all
	l.Commands = all.Commands[platform:]
	c := l.Commands[0]
	if c.Name != "grok-cli-accounts" || c.Source != "pi" || c.PiSource != "extension" || string(c.Description) != `"Manage accounts"` || c.PluginID == nil || *c.PluginID != "grokish" || string(c.PluginDisplayName) != `"Grokish"` ||
		!c.Available || c.Usage != nil || c.UnavailableReason != nil {
		t.Fatalf("plugin command: %+v %s", c, c.PluginDisplayName)
	}
	if l.Commands[1].PluginID != nil || string(l.Commands[1].PluginDisplayName) != "null" || l.Commands[2].PluginID != nil {
		t.Fatalf("commands outside the plugin directory: %+v", l.Commands[1:])
	}
	e.expect(404, "GET", "/agents/"+uuid.NewString()+"/commands", nil)
}

// Changing the model switched the runtime: the Agent's event stream has session.updated, and the console reloads the
// command list on it. The session number stays (the conversation is carried over, not started again), so the push
// comes from the change of runtime itself, also for an Agent that has not talked yet.
func TestRuntimeSwitchPushesSessionUpdated(t *testing.T) {
	e := newEnv(t)
	reg := modelcatalog.NewRegistry(e.api.Models.Snapshot())
	e.api.Models, e.api.Config.Models = reg, reg
	_, path := e.newAgent("Switch runtime")
	events, closeEvents := readSSE(t, e, path, "")
	defer closeEvents()
	_ = next(t, events) // snapshot
	// Let the pushes caused by creating the Agent arrive first: the session (none, epoch 1) has then been pushed, and
	// only the change of runtime can push it again.
	for quiet := false; !quiet; {
		select {
		case <-events:
		case <-time.After(time.Second):
			quiet = true
		}
	}
	e.expect(200, "PATCH", path, map[string]any{"model": map[string]string{"provider": "claude-code", "modelId": "sonnet"}})
	for range 20 {
		ev := next(t, events)
		if ev.event != "session.updated" {
			continue
		}
		var u apitypes.SseSessionUpdated
		if err := json.Unmarshal([]byte(ev.data), &u); err != nil {
			t.Fatal(err)
		}
		if u.Epoch == 1 && u.State == "none" {
			return
		}
	}
	t.Fatal("after switching runtime a session.updated with session epoch 1 should arrive")
}
