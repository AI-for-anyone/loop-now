package local

import (
	"strings"
	"testing"

	"github.com/google/uuid"

	"loop-now/internal/server/agentconfig"
)

// With "Use local logins" on, the prompt says which account the extension's own local login replaces: the project
// account for a project Agent, the Agent's own login for a platform Agent (plugins/SPEC.md §13).
func TestProviderLoginPromptNamesTheReplacedAccount(t *testing.T) {
	state := &agentconfig.ProviderState{PluginID: "grokish", Paths: []string{".pi/fake-oauth"}}
	project := uuid.New()
	projectPrompt := providerLoginPrompt(agentconfig.Snapshot{Provider: "fake-oauth", ContextProjectID: &project, ProviderState: state}, "/home/user")
	platformPrompt := providerLoginPrompt(agentconfig.Snapshot{Provider: "fake-oauth", ProviderState: state}, "/home/user")
	if !strings.Contains(projectPrompt, "not the subscription account configured in the project.") {
		t.Fatalf("project Agent: %q", projectPrompt)
	}
	if !strings.Contains(platformPrompt, "not the subscription account logged in for this Agent.") || strings.Contains(platformPrompt, "project") {
		t.Fatalf("platform Agent: %q", platformPrompt)
	}
	if providerLoginPrompt(agentconfig.Snapshot{Provider: "fake-oauth", ProviderState: state}, "") != "" {
		t.Fatal("no prompt when local logins are off")
	}
}
