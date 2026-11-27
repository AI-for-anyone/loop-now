package taskruntime

import (
	"encoding/json"
	"testing"
)

// Tasks created from schema 11 on start their instances from a clean conversation; tasks created before keep forking
// the regular conversation, and a schema 10 snapshot with its models.json fragment still reads.
func TestCleanStartSchema(t *testing.T) {
	scope, id, input := fixture(t)
	input.Configuration.ModelsJSON = json.RawMessage(`{"providers":{"example-provider":{"baseUrl":"https://models.example.test"}}}`)
	snap, err := Capture(scope, id, input)
	if err != nil {
		t.Fatal(err)
	}
	if !snap.StartsClean() || !snap.CopiesAgentFolder() {
		t.Fatal("a new capture starts clean and copies the Agent folder")
	}
	for _, old := range []int{frozenSourcesSchema, copiedAgentFolderSchema, modelsJSONSchema} {
		env := snap.env
		env.SchemaVersion = old
		if old < modelsJSONSchema {
			env.Configuration.ModelsJSON = nil
		}
		restored, err := Decode(encodeEnvelope(t, env))
		if err != nil {
			t.Fatalf("schema %d: %v", old, err)
		}
		if restored.StartsClean() {
			t.Fatalf("a schema %d task must keep forking", old)
		}
	}
	env := snap.env
	env.SchemaVersion = copiedAgentFolderSchema
	if _, err := Decode(encodeEnvelope(t, env)); err == nil {
		t.Fatal("models.json before schema 10 accepted")
	}
}
