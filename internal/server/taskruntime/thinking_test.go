package taskruntime

import (
	"bytes"
	"testing"
)

func TestThinkingCaptureClonesAndLegacyBytesStayUnchanged(t *testing.T) {
	scope, id, input := fixture(t)
	level := "high"
	input.Configuration.ThinkingLevel = &level
	snapshot, e := Capture(scope, id, input)
	if e != nil {
		t.Fatal(e)
	}
	level = "low"
	copy := snapshot.Configuration()
	if copy.ThinkingLevel == nil || *copy.ThinkingLevel != "high" {
		t.Fatal("capture alias")
	}
	*copy.ThinkingLevel = "medium"
	if *snapshot.Configuration().ThinkingLevel != "high" {
		t.Fatal("read projection aliases frozen level")
	}
	input.Configuration.ThinkingLevel = nil
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
	if e != nil || !bytes.Equal(before, after) || decoded.Configuration().ThinkingLevel != nil || bytes.Contains(after, []byte(`"thinkingLevel"`)) {
		t.Fatal("legacy snapshot changed", e)
	}
}
