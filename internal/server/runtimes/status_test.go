package runtimes

import (
	"testing"

	"loop-now/internal/server/agentruntime"
)

// Every runtime has exactly one way to report its status, matching its bundled capability: a bundled runtime has a
// version reader and no probe; every other runtime has a probe.
func TestEveryRuntimeHasAStatus(t *testing.T) {
	for _, rt := range agentruntime.All() {
		_, version := bundledVersions[rt.ID]
		_, probe := probes[rt.ID]
		if rt.Capabilities.Bundled != version || rt.Capabilities.Bundled == probe {
			t.Errorf("%s: bundled=%v, version reader=%v, probe=%v", rt.ID, rt.Capabilities.Bundled, version, probe)
		}
	}
	for id := range bundledVersions {
		if !agentruntime.Known(id) {
			t.Errorf("version reader for unknown runtime %s", id)
		}
	}
	for id := range probes {
		if !agentruntime.Known(id) {
			t.Errorf("probe for unknown runtime %s", id)
		}
	}
}
