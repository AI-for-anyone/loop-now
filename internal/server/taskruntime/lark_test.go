package taskruntime

import (
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"loop-now/internal/server/clicredential"
	"testing"
)

func TestLarkFixedBindingCaptureAndStoredValidation(t *testing.T) {
	scope, id, input := fixture(t)
	project := uuid.New()
	input.Configuration.Prompts.ProjectID = &project
	input.Configuration.Prompts.ProjectVersion = 1
	delete(input.Credentials.CLI, "lark-cli")
	b := clicredential.LarkBinding{ProjectID: project, AccountID: uuid.New(), VersionID: uuid.New(), AgentID: scope.AgentID(), Brand: "feishu", AppID: "app-fixture", OpenID: "ou-fixture", RuntimeVariantVersion: clicredential.LarkUserVariant}
	input.Credentials.LarkBinding = &b
	snap, e := Capture(scope, id, input)
	if e != nil || snap.env.SchemaVersion != schemaVersion {
		t.Fatal("lark capture failed", e)
	}
	b.VersionID = uuid.New()
	opened, e := snap.Credentials()
	if e != nil || opened.LarkBinding == nil || opened.LarkBinding.VersionID == b.VersionID {
		t.Fatal("captured binding aliased input")
	}
	for _, mutate := range []func(*clicredential.LarkBinding){func(b *clicredential.LarkBinding) { b.ProjectID = uuid.New() }, func(b *clicredential.LarkBinding) { b.AgentID = uuid.New() }, func(b *clicredential.LarkBinding) { b.AccountID = uuid.Nil }, func(b *clicredential.LarkBinding) { b.VersionID = uuid.Nil }, func(b *clicredential.LarkBinding) { b.Brand = "other" }, func(b *clicredential.LarkBinding) { b.OpenID = "" }, func(b *clicredential.LarkBinding) { b.RuntimeVariantVersion = "host" }} {
		bad := cloneCredentials(opened)
		mutate(bad.LarkBinding)
		env := snap.env
		env.Credentials = new(toWire(bad))
		if _, e = (&Snapshot{env: env}).Credentials(); !errors.Is(e, ErrCredentials) {
			t.Fatal("invalid stored binding accepted")
		}
	}
	for _, version := range []int{1, 2, 3, 4, 5, 6} {
		env := snap.env
		env.SourceVersion = version
		if version < 3 {
			env.Configuration.Prompts = nil
		}
		if _, e = (&Snapshot{env: env}).Credentials(); e == nil {
			t.Fatal("Lark binding accepted in legacy source schema")
		}
	}
	bad := input
	bad.Credentials = cloneCredentials(opened)
	bad.Credentials.CLI["lark-cli"] = CredentialPayload{Env: map[string]string{"FORBIDDEN": "static"}}
	if _, e = Capture(scope, uuid.New(), bad); !errors.Is(e, ErrInvalidInput) {
		t.Fatal("mixed static/dynamic credential accepted")
	}
}

func TestLarkStoredBindingMissingNullAndMixedOwner(t *testing.T) {
	scope, id, input := fixture(t)
	project := uuid.New()
	input.Configuration.Prompts.ProjectID = &project
	input.Configuration.Prompts.ProjectVersion = 1
	delete(input.Credentials.CLI, "lark-cli")
	input.Credentials.LarkBinding = &clicredential.LarkBinding{ProjectID: project, AccountID: uuid.New(), VersionID: uuid.New(), AgentID: scope.AgentID(), Brand: "feishu", AppID: "app-fixture", OpenID: "ou-fixture", RuntimeVariantVersion: clicredential.LarkUserVariant}
	snap, e := Capture(scope, id, input)
	if e != nil {
		t.Fatal(e)
	}
	raw, _ := json.Marshal(toWire(input.Credentials))
	for _, key := range []string{"projectId", "accountId", "versionId", "agentId", "brand", "appId", "openId", "runtimeVariantVersion", "ownerKind"} {
		for _, missing := range []bool{false, true} {
			if key == "ownerKind" && missing {
				continue
			}
			var wire map[string]json.RawMessage
			json.Unmarshal(raw, &wire)
			var binding map[string]json.RawMessage
			json.Unmarshal(wire["larkBinding"], &binding)
			if missing {
				delete(binding, key)
			} else {
				binding[key] = json.RawMessage("null")
			}
			if key == "ownerKind" {
				binding[key] = json.RawMessage(`"agent"`)
			}
			wire["larkBinding"], _ = json.Marshal(binding)
			bad, _ := json.Marshal(wire)
			if !rejected(withCredentialsJSON(t, snap, bad)) {
				t.Fatal("malformed stored binding accepted", key, missing)
			}
		}
	}
}
