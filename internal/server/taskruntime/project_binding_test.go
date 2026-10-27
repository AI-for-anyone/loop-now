package taskruntime

import (
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"loop-now/internal/server/modelcredential"
	"testing"
)

func TestProjectBindingMalformedStoredBindingNeverPanics(t *testing.T) {
	scope, id, input := fixture(t)
	project, account := uuid.New(), uuid.New()
	input.Configuration.Prompts.ProjectID = &project
	input.Configuration.Prompts.ProjectVersion = 1
	input.Credentials.Model = map[string]string{}
	good := modelcredential.Binding{VersionID: uuid.New(), AgentID: scope.AgentID(), Provider: input.Configuration.Provider, OwnerKind: "project", ProjectID: &project, AccountID: &account}
	tests := map[string]func(map[string]any){
		"missing-project": func(m map[string]any) { delete(m, "projectId") }, "null-project": func(m map[string]any) { m["projectId"] = nil }, "zero-project": func(m map[string]any) { m["projectId"] = uuid.Nil.String() },
		"missing-account": func(m map[string]any) { delete(m, "accountId") }, "null-account": func(m map[string]any) { m["accountId"] = nil }, "zero-account": func(m map[string]any) { m["accountId"] = uuid.Nil.String() },
		"mixed-owner": func(m map[string]any) { m["ownerKind"] = "agent" }, "unknown-owner": func(m map[string]any) { m["ownerKind"] = "unknown" }, "wrong-project": func(m map[string]any) { m["projectId"] = uuid.NewString() },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			input.Credentials.ModelAuth = &good
			snap, err := Capture(scope, id, input)
			if err != nil {
				t.Fatal(err)
			}
			raw, _ := json.Marshal(good)
			var binding map[string]any
			if err = json.Unmarshal(raw, &binding); err != nil {
				t.Fatal(err)
			}
			mutate(binding)
			payload, err := json.Marshal(map[string]any{"modelAuth": binding, "model": map[string]string{}, "cli": map[string]any{}})
			if err != nil {
				t.Fatal(err)
			}
			if !rejected(withCredentialsJSON(t, snap, payload)) {
				t.Fatal("malformed stored binding not rejected")
			}
			encoded, _ := json.Marshal(binding)
			var b modelcredential.Binding
			if err = json.Unmarshal(encoded, &b); err != nil {
				t.Fatal(err)
			}
			input.Credentials.ModelAuth = &b
			if _, err = Capture(scope, id, input); !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("malformed capture not rejected: %v", err)
			}
		})
	}
}
func TestProjectBindingDeepCopy(t *testing.T) {
	scope, id, input := fixture(t)
	project, account := uuid.New(), uuid.New()
	originalProject, originalAccount := project, account
	input.Configuration.Prompts.ProjectID = &project
	input.Configuration.Prompts.ProjectVersion = 1
	input.Credentials.Model = map[string]string{}
	input.Credentials.ModelAuth = &modelcredential.Binding{VersionID: uuid.New(), AgentID: scope.AgentID(), Provider: input.Configuration.Provider, OwnerKind: "project", ProjectID: &project, AccountID: &account}
	snap, err := Capture(scope, id, input)
	if err != nil {
		t.Fatal(err)
	}
	project, account = uuid.New(), uuid.New()
	opened, err := snap.Credentials()
	if err != nil || *opened.ModelAuth.ProjectID != originalProject || *opened.ModelAuth.AccountID != originalAccount {
		t.Fatal("capture retained mutable pointers", err)
	}
	*opened.ModelAuth.ProjectID = uuid.New()
	again, err := snap.Credentials()
	if err != nil || *again.ModelAuth.ProjectID != originalProject {
		t.Fatal("read mutated binding", err)
	}
}
