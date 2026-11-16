package inbox

import (
	"testing"

	"github.com/google/uuid"

	"loop-now/internal/server/apperr"
	"loop-now/internal/server/auth"
)

func TestAuthorizeSend(t *testing.T) {
	orgA, orgB := uuid.New(), uuid.New()
	agentIn := func(org uuid.UUID, manager bool) Participant {
		return Participant{Kind: auth.KindAgent, OrgID: &org, ProjectManager: manager}
	}
	platform := Participant{Kind: auth.KindAgent, PlatformHeld: true}
	console := Participant{Kind: auth.KindConsole}
	external := Participant{Kind: auth.KindExternal}

	cases := []struct {
		name     string
		from, to Participant
		reply    bool
		allowed  bool
	}{
		{"Agent to Agent in the same organization", agentIn(orgA, false), agentIn(orgA, false), false, true},
		{"Agent to a regular Agent in another organization", agentIn(orgA, false), agentIn(orgB, false), false, false},
		{"regular Agent to a Manager in another organization", agentIn(orgA, false), agentIn(orgB, true), false, false},
		{"Manager to a regular Agent in another organization", agentIn(orgA, true), agentIn(orgB, false), false, false},
		{"Manager to a Manager in another organization", agentIn(orgA, true), agentIn(orgB, true), false, true},
		{"organization Agent to a platform Agent", agentIn(orgA, false), platform, false, true},
		{"platform Agent to an Agent in any organization", platform, agentIn(orgB, false), false, true},
		{"platform Agents to each other", platform, platform, false, true},
		{"console to an Agent in any organization", console, agentIn(orgB, false), false, true},
		{"external identity to an Agent in any organization", external, agentIn(orgB, false), false, true},
		{"Agent to the console", agentIn(orgA, false), console, false, true},
		{"Agent to an external identity", agentIn(orgA, false), external, false, true},
		{"reply to an Agent in another organization", agentIn(orgB, false), agentIn(orgA, false), true, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := AuthorizeSend(c.from, c.to, c.reply)
			if c.allowed && err != nil {
				t.Fatalf("should be allowed: %v", err)
			}
			if !c.allowed && !apperr.Is(err, apperr.CodeForbidden) {
				t.Fatalf("should be rejected as forbidden: %v", err)
			}
		})
	}
}
