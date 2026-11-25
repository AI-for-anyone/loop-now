package modelcatalog

import (
	"fmt"
	"slices"
	"strings"
	"testing"
)

// agyRows are rows as agy 1.5.14 lists them (agy models, or session/new's availableModels), in its order (Claude Opus
// 5.5 without its medium row, to show the default without medium), plus rows that must not be merged: a name whose
// level is not the id's, a name without the level, an id whose level is not lowercase (it would not be the id sent
// back), a level the platform does not know, and a model also listed under its plain id.
var agyRows = []LocalModel{
	{ID: "gemini-4-neon-high", Name: "Gemini 4 Neon (High)", Images: true},
	{ID: "gemini-4-neon-xhigh", Name: "Gemini 4 Neon (XHigh)", Images: true},
	{ID: "gemini-4-neon-medium", Name: "Gemini 4 Neon (Medium)", Images: true},
	{ID: "gemini-4-neon-low", Name: "Gemini 4 Neon (Low)", Images: true},
	{ID: "gemini-4-carbon-medium", Name: "Gemini 4 Carbon (Medium)", Images: true},
	{ID: "gemini-4-carbon-high", Name: "Gemini 4 Carbon (High)", Images: true},
	{ID: "gemini-4-carbon-low", Name: "Gemini 4 Carbon (Low)", Images: true},
	{ID: "gemini-pro-orchestrator-neon", Name: "Orchestrator (Neon)", Images: true},
	{ID: "gemini-pro-orchestrator", Name: "Orchestrator (Carbon)", Images: true},
	{ID: "gemini-3.8-flash-high", Name: "Gemini 3.8 Flash (High)", Images: true},
	{ID: "gemini-3.8-flash-medium", Name: "Gemini 3.8 Flash (Medium)", Images: true},
	{ID: "gemini-3.8-flash-low", Name: "Gemini 3.8 Flash (Low)", Images: true},
	{ID: "claude-opus-5-5-low", Name: "Claude Opus 5.5 (Low)", Images: true},
	{ID: "claude-opus-5-5-high", Name: "Claude Opus 5.5 (High)", Images: true},
	{ID: "gpt-oss-120b-medium", Name: "GPT-OSS 120B (Medium)", Images: true},
	{ID: "odd-model-high", Name: "Odd Model (Low)"},
	{ID: "plain-model-high", Name: "Plain Model High"},
	{ID: "shouty-model-HIGH", Name: "Shouty Model (High)"},
	{ID: "ultra-model-ultra", Name: "Ultra Model (Ultra)"},
	{ID: "twin-model", Name: "Twin Model"},
	{ID: "twin-model-high", Name: "Twin Model (High)"},
}

func describe(models []LocalModel) string {
	var out []string
	for _, m := range models {
		out = append(out, fmt.Sprintf("%s|%s|%s|%v", m.ID, m.Name, strings.Join(m.Efforts, ","), m.LevelIDs))
	}
	return strings.Join(out, "\n")
}

// Rows that name one model at several thinking levels (the id ends with -<level> and the name with " (<Level>)", the
// same level, any case in the name) become one model: the id and name without the level, the levels it was listed at
// in the platform's order, in the place of its first row. A model listed at one level only is merged too. Every other
// row stays as it was.
func TestMergeLevelModels(t *testing.T) {
	got := MergeLevelModels(agyRows)
	want := strings.Join([]string{
		"gemini-4-neon|Gemini 4 Neon|low,medium,high,xhigh|true",
		"gemini-4-carbon|Gemini 4 Carbon|low,medium,high|true",
		"gemini-pro-orchestrator-neon|Orchestrator (Neon)||false",
		"gemini-pro-orchestrator|Orchestrator (Carbon)||false",
		"gemini-3.8-flash|Gemini 3.8 Flash|low,medium,high|true",
		"claude-opus-5-5|Claude Opus 5.5|low,high|true",
		"gpt-oss-120b|GPT-OSS 120B|medium|true",
		"odd-model-high|Odd Model (Low)||false",
		"plain-model-high|Plain Model High||false",
		"shouty-model-HIGH|Shouty Model (High)||false",
		"ultra-model-ultra|Ultra Model (Ultra)||false",
		"twin-model|Twin Model||false",
		"twin-model-high|Twin Model (High)||false",
	}, "\n")
	if describe(got) != want {
		t.Fatalf("got:\n%s\nwant:\n%s", describe(got), want)
	}
	if !got[0].Images || got[0].DefaultEffort != "" {
		t.Fatalf("a merged model keeps its first row's other fields: %+v", got[0])
	}
	// A row listed twice counts once.
	if twice := MergeLevelModels(append(slices.Clone(agyRows[:2]), agyRows[0])); describe(twice) != "gemini-4-neon|Gemini 4 Neon|high,xhigh|true" {
		t.Fatalf("listed twice: %s", describe(twice))
	}
}

// In the catalog a merged model offers the levels it was listed at; its default is medium when listed, otherwise the
// lowest (as for the other CLIs' models); a model at one level offers only that one; a row left alone has none to
// choose (off).
func TestAntigravityProviderLevels(t *testing.T) {
	p := AntigravityProvider(MergeLevelModels(agyRows))
	levels := map[string]string{}
	for _, m := range p.Models {
		levels[m.ID] = strings.Join(m.ThinkingLevels, ",") + " default " + m.DefaultThinkingLevel
	}
	for id, want := range map[string]string{
		"gemini-4-neon":                "low,medium,high,xhigh default medium",
		"claude-opus-5-5":              "low,high default low",
		"gpt-oss-120b":                 "medium default medium",
		"gemini-pro-orchestrator-neon": "off default off",
	} {
		if levels[id] != want {
			t.Errorf("%s: %q, want %q", id, levels[id], want)
		}
	}
}

// A model with levels runs as the id its CLI listed for the level; off, no level or a level the CLI does not name
// leaves the id alone.
func TestLevelModelID(t *testing.T) {
	for _, c := range []struct{ id, level, want string }{
		{"gemini-4-neon", "xhigh", "gemini-4-neon-xhigh"},
		{"gpt-oss-120b", "medium", "gpt-oss-120b-medium"},
		{"gemini-pro-orchestrator-neon", "off", "gemini-pro-orchestrator-neon"},
		{"gemini-4-neon-xhigh", "", "gemini-4-neon-xhigh"},
		{"gemini-4-neon", "ultra", "gemini-4-neon"},
	} {
		if got := LevelModelID(c.id, c.level); got != c.want {
			t.Errorf("LevelModelID(%q, %q) = %q, want %q", c.id, c.level, got, c.want)
		}
	}
}

func TestValidThinkingLevel(t *testing.T) {
	for _, l := range []string{"off", "minimal", "low", "medium", "high", "xhigh", "max"} {
		if !ValidThinkingLevel(l) {
			t.Errorf("%s is a level", l)
		}
	}
	for _, l := range []string{"", "ultra", "High"} {
		if ValidThinkingLevel(l) {
			t.Errorf("%q is not a level", l)
		}
	}
}
