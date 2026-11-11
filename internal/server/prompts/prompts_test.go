package prompts

import (
	"github.com/google/uuid"
	"strings"
	"testing"
)

func TestExplicitPromptComposition(t *testing.T) {
	project := uuid.New()
	for _, tc := range []struct {
		name  string
		layer Layers
		want  string
	}{
		{"platform", Layers{Format: FormatV1, AgentVersion: 1, AgentPrompt: "own\n"}, "own\n"},
		{"both-empty", Layers{Format: FormatV1, ProjectID: &project, ProjectVersion: 1, AgentVersion: 1}, ""},
		{"empty-project", Layers{Format: FormatV1, ProjectID: &project, ProjectVersion: 1, AgentVersion: 1, AgentPrompt: "own"}, "own"},
		{"empty-agent", Layers{Format: FormatV1, ProjectID: &project, ProjectVersion: 1, AgentVersion: 1, ProjectPrompt: "rules"}, "Project rules take precedence; Agent rules add to them within the scope of the project rules.\n\n## Project rules\nrules"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.layer.Compose()
			if err != nil || got != tc.want {
				t.Fatal(got, err)
			}
		})
	}
	layer := Layers{Format: FormatV1, ProjectID: &project, ProjectVersion: 1, AgentVersion: 1, ProjectPrompt: "rules", AgentPrompt: "own"}
	got, err := layer.Compose()
	if err != nil || !strings.Contains(got, "## Project rules\nrules\n\n## Agent role and rules\nown") {
		t.Fatal(got, err)
	}
	other := Clone(&layer)
	*other.ProjectID = uuid.New()
	other.ProjectVersion++
	if *layer.ProjectID != project || Identity(other) == Identity(&layer) {
		t.Fatal("shared project or version identity")
	}
	layer.Format = "future"
	if _, err = layer.Compose(); err == nil {
		t.Fatal("unknown format accepted")
	}
	layer.Format = FormatV1
	layer.ProjectID = nil
	if _, err = layer.Compose(); err == nil {
		t.Fatal("platform inherited project")
	}
}
