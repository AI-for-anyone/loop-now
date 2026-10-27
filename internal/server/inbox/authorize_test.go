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
		{"同组织的 Agent 互发", agentIn(orgA, false), agentIn(orgA, false), false, true},
		{"发给其他组织的普通 Agent", agentIn(orgA, false), agentIn(orgB, false), false, false},
		{"普通 Agent 发给其他组织的 Manager", agentIn(orgA, false), agentIn(orgB, true), false, false},
		{"Manager 发给其他组织的普通 Agent", agentIn(orgA, true), agentIn(orgB, false), false, false},
		{"Manager 发给其他组织的 Manager", agentIn(orgA, true), agentIn(orgB, true), false, true},
		{"组织内 Agent 发给平台 Agent", agentIn(orgA, false), platform, false, true},
		{"平台 Agent 发给任何组织的 Agent", platform, agentIn(orgB, false), false, true},
		{"平台 Agent 互发", platform, platform, false, true},
		{"控制台发给任何组织的 Agent", console, agentIn(orgB, false), false, true},
		{"外部身份发给任何组织的 Agent", external, agentIn(orgB, false), false, true},
		{"Agent 发给控制台", agentIn(orgA, false), console, false, true},
		{"Agent 发给外部身份", agentIn(orgA, false), external, false, true},
		{"回复其他组织的 Agent", agentIn(orgB, false), agentIn(orgA, false), true, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := AuthorizeSend(c.from, c.to, c.reply)
			if c.allowed && err != nil {
				t.Fatalf("应允许：%v", err)
			}
			if !c.allowed && !apperr.Is(err, apperr.CodeForbidden) {
				t.Fatalf("应以 forbidden 拒绝：%v", err)
			}
		})
	}
}
