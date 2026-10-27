package taskruntime

import (
	"bytes"
	"testing"
)

func TestServiceTierCaptureClonesAndLegacyBytesStayUnchanged(t *testing.T) {
	scope, id, input := fixture(t)
	level := "priority"
	input.Configuration.ServiceTier = &level
	snapshot, e := Capture(scope, id, input)
	if e != nil {
		t.Fatal(e)
	}
	level = "ultrafast"
	copy := snapshot.Configuration()
	if copy.ServiceTier == nil || *copy.ServiceTier != "priority" {
		t.Fatal("capture alias")
	}
	*copy.ServiceTier = "default"
	if *snapshot.Configuration().ServiceTier != "priority" {
		t.Fatal("read projection aliases frozen level")
	}
	input.Configuration.ServiceTier = nil
	legacy, e := Capture(scope, id, input)
	if e != nil {
		t.Fatal(e)
	}
	before, e := legacy.Encode()
	if e != nil {
		t.Fatal(e)
	}
	decoded, e := Decode(before)
	if e != nil {
		t.Fatal(e)
	}
	after, e := decoded.Encode()
	if e != nil || !bytes.Equal(before, after) || decoded.Configuration().ServiceTier != nil || bytes.Contains(after, []byte(`"serviceTier"`)) {
		t.Fatal("legacy snapshot changed", e)
	}
}
