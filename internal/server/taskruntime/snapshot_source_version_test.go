package taskruntime

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/google/uuid"

	"loop-now/internal/server/clicredential"
	"loop-now/internal/server/dbcli"
	"loop-now/internal/server/modelcredential"
)

// projectFixture binds the fixture to a project so Google, Lark and database
// credentials are representable.
func projectFixture(t *testing.T) (Scope, uuid.UUID, CaptureInput, uuid.UUID) {
	t.Helper()
	scope, id, input := fixture(t)
	project := uuid.New()
	input.Configuration.Prompts.ProjectID = &project
	input.Configuration.Prompts.ProjectVersion = 1
	return scope, id, input, project
}

// storedWith returns the decoded snapshot after replacing credentials and
// sourceVersion in its stored form.
func storedWith(t *testing.T, scope Scope, id uuid.UUID, cfg Configuration, source int, creds Credentials) (*Snapshot, error) {
	t.Helper()
	// Converted snapshots (sourceVersion set) exist only in schema 8.
	schema := schemaVersion
	if source != 0 {
		schema = frozenSourcesSchema
	}
	return Decode(encodeEnvelope(t, envelope{SchemaVersion: schema, SourceVersion: source, SnapshotID: id, Scope: scope.wire(), Configuration: cloneConfig(cfg), Credentials: new(toWire(creds))}))
}

func TestSourceVersionModelAuthOwnerKind(t *testing.T) {
	scope, id, input := fixture(t)
	legacy := modelcredential.Binding{VersionID: uuid.New(), AgentID: scope.AgentID(), Provider: input.Configuration.Provider}
	owned := legacy
	owned.OwnerKind = "agent"
	cases := []struct {
		source  int
		binding modelcredential.Binding
		ok      bool
	}{
		{3, legacy, true}, {3, owned, false},
		{4, legacy, false}, {4, owned, true},
		{0, legacy, false}, {0, owned, true},
	}
	for _, c := range cases {
		t.Run(fmt.Sprintf("source%d/%q", c.source, c.binding.OwnerKind), func(t *testing.T) {
			b := c.binding
			snap, err := storedWith(t, scope, id, input.Configuration, c.source, Credentials{ModelAuth: &b, CLI: map[string]CredentialPayload{}})
			if err != nil {
				t.Fatal(err)
			}
			got, err := snap.Credentials()
			if c.ok && (err != nil || got.ModelAuth == nil || *got.ModelAuth != b) {
				t.Fatal("valid binding rejected", err)
			}
			if !c.ok && !errors.Is(err, ErrCredentials) {
				t.Fatal("invalid owner shape accepted", err)
			}
		})
	}
}

func TestSourceVersionGatesGoogleAndLarkBindings(t *testing.T) {
	scope, id, input, project := projectFixture(t)
	cfg := cloneConfig(input.Configuration)
	cfg.CLIs = []string{"gcloud", "gh", "lark-cli"}
	google := clicredential.Binding{VersionID: uuid.New(), AccountID: uuid.New(), ProjectID: project, AgentID: scope.AgentID(), CLI: "gcloud"}
	lark := clicredential.LarkBinding{ProjectID: project, AccountID: uuid.New(), VersionID: uuid.New(), AgentID: scope.AgentID(), Brand: "feishu", AppID: "app-fixture", OpenID: "ou-fixture", RuntimeVariantVersion: clicredential.LarkUserVariant}
	for _, c := range []struct {
		name   string
		source int
		creds  Credentials
		ok     bool
	}{
		{"google5", 5, Credentials{GoogleBinding: &google}, false},
		{"google6", 6, Credentials{GoogleBinding: &google}, true},
		{"google-latest", 0, Credentials{GoogleBinding: &google}, true},
		{"lark6", 6, Credentials{LarkBinding: &lark}, false},
		{"lark7", 7, Credentials{LarkBinding: &lark}, true},
		{"lark-latest", 0, Credentials{LarkBinding: &lark}, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			snap, err := storedWith(t, scope, id, cfg, c.source, c.creds)
			if err != nil {
				t.Fatal(err)
			}
			_, err = snap.Credentials()
			if c.ok != (err == nil) {
				t.Fatalf("source %d binding result %v", c.source, err)
			}
		})
	}
}

func TestSourceVersionDatabaseValidation(t *testing.T) {
	scope, id, input, project := projectFixture(t)
	cfg := cloneConfig(input.Configuration)
	cfg.CLIs = append(cfg.CLIs, "mysql")
	// A pre-schema5 static payload is not a connection set.
	static := Credentials{CLI: map[string]CredentialPayload{"mysql": {Env: map[string]string{"MYSQL_PWD": "synthetic-only"}}}}
	connection := dbcli.Connection{ProjectID: &project, AccountID: uuid.New(), Name: "captured", VersionID: uuid.New(), Fields: dbcli.Fields{Host: "offline.example", Port: "3306", User: "u", Password: "synthetic-only", Database: "db", SSLMode: "DISABLED"}}
	set, _ := json.Marshal([]dbcli.Connection{connection})
	valid := Credentials{CLI: map[string]CredentialPayload{"mysql": {Files: []File{{Path: dbcli.ConfigPath("mysql"), Content: set}}}}}
	for _, c := range []struct {
		source int
		creds  Credentials
		ok     bool
	}{
		{4, static, true}, {5, static, false}, {0, static, false},
		{5, valid, true}, {0, valid, true},
	} {
		t.Run(fmt.Sprint(c.source, c.ok), func(t *testing.T) {
			snap, err := storedWith(t, scope, id, cfg, c.source, c.creds)
			if err != nil {
				t.Fatal(err)
			}
			_, err = snap.Credentials()
			if c.ok != (err == nil) {
				t.Fatalf("source %d database result %v", c.source, err)
			}
		})
	}
}

func TestCaptureWritesLatestSchemaAndKeepsBindingValidation(t *testing.T) {
	scope, id, input, project := projectFixture(t)
	input.Configuration.CLIs = []string{"gcloud", "gh", "lark-cli", "mysql"}
	delete(input.Credentials.CLI, "lark-cli")
	google := clicredential.Binding{VersionID: uuid.New(), AccountID: uuid.New(), ProjectID: project, AgentID: scope.AgentID(), CLI: "gcloud"}
	lark := clicredential.LarkBinding{ProjectID: project, AccountID: uuid.New(), VersionID: uuid.New(), AgentID: scope.AgentID(), Brand: "feishu", AppID: "app-fixture", OpenID: "ou-fixture", RuntimeVariantVersion: clicredential.LarkUserVariant}
	connection := dbcli.Connection{ProjectID: &project, AccountID: uuid.New(), Name: "captured", VersionID: uuid.New(), Fields: dbcli.Fields{Host: "offline.example", Port: "3306", User: "u", Password: "synthetic-only", Database: "db", SSLMode: "DISABLED"}}
	set, _ := json.Marshal([]dbcli.Connection{connection})
	input.Credentials.GoogleBinding = &google
	input.Credentials.LarkBinding = &lark
	input.Credentials.CLI["mysql"] = CredentialPayload{Files: []File{{Path: dbcli.ConfigPath("mysql"), Content: set}}}
	snap, err := Capture(scope, id, input)
	if err != nil {
		t.Fatal(err)
	}
	if snap.env.SchemaVersion != schemaVersion || snap.env.SourceVersion != 0 {
		t.Fatal("capture did not write latest schema", snap.env.SchemaVersion, snap.env.SourceVersion)
	}
	if _, err = snap.Credentials(); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*CaptureInput){
		"google project": func(i *CaptureInput) {
			b := *i.Credentials.GoogleBinding
			b.ProjectID = uuid.New()
			i.Credentials.GoogleBinding = &b
		},
		"google agent": func(i *CaptureInput) {
			b := *i.Credentials.GoogleBinding
			b.AgentID = uuid.New()
			i.Credentials.GoogleBinding = &b
		},
		"lark project": func(i *CaptureInput) {
			b := *i.Credentials.LarkBinding
			b.ProjectID = uuid.New()
			i.Credentials.LarkBinding = &b
		},
		"lark static": func(i *CaptureInput) {
			i.Credentials.CLI["lark-cli"] = CredentialPayload{Env: map[string]string{"FORBIDDEN": "static"}}
		},
		"db owner": func(i *CaptureInput) {
			bad := connection
			other := uuid.New()
			bad.ProjectID = &other
			b, _ := json.Marshal([]dbcli.Connection{bad})
			i.Credentials.CLI["mysql"] = CredentialPayload{Files: []File{{Path: dbcli.ConfigPath("mysql"), Content: b}}}
		},
		"db static": func(i *CaptureInput) {
			i.Credentials.CLI["mysql"] = CredentialPayload{Env: map[string]string{"MYSQL_PWD": "synthetic-only"}}
		},
	} {
		t.Run(name, func(t *testing.T) {
			fresh := CaptureInput{Configuration: cloneConfig(input.Configuration), Credentials: cloneCredentials(input.Credentials)}
			mutate(&fresh)
			if s, err := Capture(scope, uuid.New(), fresh); s != nil || !errors.Is(err, ErrInvalidInput) {
				t.Fatal("invalid binding captured", err)
			}
		})
	}
}
