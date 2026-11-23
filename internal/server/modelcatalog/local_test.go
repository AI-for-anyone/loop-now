package modelcatalog

import (
	"slices"
	"testing"

	"loop-now/internal/server/agentruntime"
)

// The providers a runtime lists in the registry are exactly the ones its local source brings, and only runtimes that
// list providers have a source.
func TestLocalSourcesMatchRegistry(t *testing.T) {
	for _, rt := range agentruntime.All() {
		source := localSources[rt.ID]
		if (source == nil) != (len(rt.Providers) == 0) {
			t.Fatalf("%s: lists providers %v but has a source: %v", rt.ID, rt.Providers, source != nil)
		}
		if source == nil {
			continue
		}
		var got []string
		for _, p := range source() {
			got = append(got, p.ID)
			if p.Source != SourceLocalCLI {
				t.Errorf("%s: provider %s has source %q", rt.ID, p.ID, p.Source)
			}
		}
		if !slices.Equal(got, rt.Providers) {
			t.Fatalf("%s: the source brings %v, the registry lists %v", rt.ID, got, rt.Providers)
		}
	}
	for id := range localSources {
		if !agentruntime.Known(id) {
			t.Errorf("a source for an unknown runtime %s", id)
		}
	}
}
