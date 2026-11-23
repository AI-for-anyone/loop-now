package conversation

import (
	"slices"
	"testing"

	"loop-now/internal/server/agentruntime"
)

// The platform commands marked as session commands are exactly the registry's session commands, in the same order, so
// the sessionCommands capability covers every one of them.
func TestSessionCommandsMatchRegistry(t *testing.T) {
	var got []string
	for _, c := range platformCommands {
		if c.sessionCommand {
			got = append(got, c.name)
		}
	}
	if !slices.Equal(got, agentruntime.SessionCommands) {
		t.Fatalf("session commands %v, registry %v", got, agentruntime.SessionCommands)
	}
}
