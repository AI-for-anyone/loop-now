package taskruntime

import (
	"errors"
	"github.com/google/uuid"
	"loop-now/internal/server/clicredential"
	"testing"
)

func TestGoogleBindingCaptureAndStoredValidation(t *testing.T) {
	scope, id, input := fixture(t)
	project := uuid.New()
	input.Configuration.Prompts.ProjectID = &project
	input.Configuration.Prompts.ProjectVersion = 1
	input.Configuration.CLIs = append(input.Configuration.CLIs, "gcloud")
	binding := clicredential.Binding{VersionID: uuid.New(), AccountID: uuid.New(), ProjectID: project, AgentID: scope.AgentID(), CLI: "gcloud"}
	input.Credentials.GoogleBinding = &binding
	snap, e := Capture(scope, id, input)
	if e != nil || snap.env.SchemaVersion != schemaVersion {
		t.Fatal("google capture failed", e)
	}
	binding.VersionID = uuid.New()
	opened, e := snap.Credentials()
	if e != nil || opened.GoogleBinding == nil || opened.GoogleBinding.VersionID == binding.VersionID {
		t.Fatal("captured grant was retrofitted")
	}
	for _, mutate := range []func(*clicredential.Binding){func(b *clicredential.Binding) { b.ProjectID = uuid.New() }, func(b *clicredential.Binding) { b.AgentID = uuid.New() }, func(b *clicredential.Binding) { b.AccountID = uuid.Nil }, func(b *clicredential.Binding) { b.VersionID = uuid.Nil }, func(b *clicredential.Binding) { b.CLI = "gh" }} {
		bad := cloneCredentials(opened)
		mutate(bad.GoogleBinding)
		env := snap.env
		env.Credentials = new(toWire(bad))
		if _, e = (&Snapshot{env: env}).Credentials(); !errors.Is(e, ErrCredentials) {
			t.Fatal("bad stored Google binding accepted")
		}
	}
	// A converted schema5 snapshot cannot carry a Google grant.
	env := snap.env
	env.SchemaVersion, env.SourceVersion = frozenSourcesSchema, 5
	if _, e = (&Snapshot{env: env}).Credentials(); !errors.Is(e, ErrCredentials) {
		t.Fatal("Google grant in older source schema accepted")
	}
}
