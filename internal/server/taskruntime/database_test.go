package taskruntime

import (
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"loop-now/internal/server/dbcli"
	"reflect"
	"testing"
)

func TestDatabaseConnectionSetBindingAndConvertedPayloads(t *testing.T) {
	scope, id, input := fixture(t)
	project := uuid.New()
	input.Configuration.Prompts.ProjectID = &project
	input.Configuration.Prompts.ProjectVersion = 1
	input.Configuration.CLIs = append(input.Configuration.CLIs, "mysql")
	connection := dbcli.Connection{ProjectID: &project, AccountID: uuid.New(), Name: "captured", VersionID: uuid.New(), Fields: dbcli.Fields{Host: "offline.example", Port: "3306", User: "u", Password: "synthetic-only", Database: "db", SSLMode: "DISABLED"}}
	b, _ := json.Marshal([]dbcli.Connection{connection})
	input.Credentials.CLI["mysql"] = CredentialPayload{Env: map[string]string{}, Files: []File{{Path: dbcli.ConfigPath("mysql"), Content: b}}}
	snap, err := Capture(scope, id, input)
	if err != nil || snap.env.SchemaVersion != schemaVersion {
		t.Fatal("connection set capture", err)
	}
	connection.VersionID = uuid.New()
	b, _ = json.Marshal([]dbcli.Connection{connection})
	input.Credentials.CLI["mysql"] = CredentialPayload{Files: []File{{Path: dbcli.ConfigPath("mysql"), Content: b}}}
	encoded, err := snap.Encode()
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := Decode(encoded)
	if err != nil {
		t.Fatal(err)
	}
	old, err := decoded.Credentials()
	if err != nil {
		t.Fatal(err)
	}
	var oldConnections []dbcli.Connection
	json.Unmarshal(old.CLI["mysql"].Files[0].Content, &oldConnections)
	if len(oldConnections) != 1 || oldConnections[0].VersionID == connection.VersionID {
		t.Fatal("snapshot retrofitted")
	}
	// Converted source 1-4 payloads keep their original content and identity.
	for _, version := range []int{1, 2, 3, 4} {
		t.Run(string(rune('0'+version)), func(t *testing.T) {
			oldScope, oldID, legacy := fixture(t)
			cfg := legacy.Configuration
			if version < 3 {
				cfg.Prompts = nil
			}
			env := envelope{SchemaVersion: frozenSourcesSchema, SourceVersion: version, SnapshotID: oldID, Scope: oldScope.wire(), Configuration: cfg, Credentials: new(toWire(legacy.Credentials))}
			data, _ := json.Marshal(env)
			restored, e := Decode(data)
			if e != nil {
				t.Fatal(e)
			}
			if _, e = restored.Credentials(); e != nil {
				t.Fatal("converted payload rejected", e)
			}
			roundtrip, _ := restored.Encode()
			var after envelope
			json.Unmarshal(roundtrip, &after)
			if !reflect.DeepEqual(after.Credentials, env.Credentials) || after.SourceVersion != version || after.Scope != env.Scope || after.SnapshotID != env.SnapshotID {
				t.Fatal("converted credentials/scope rewritten")
			}
		})
	}
	// Invalid stored owner payloads remain rejected without dereferencing nil.
	for _, mutate := range []func(*dbcli.Connection){func(c *dbcli.Connection) { c.ProjectID = nil }, func(c *dbcli.Connection) { wrong := uuid.New(); c.ProjectID = &wrong }, func(c *dbcli.Connection) { c.AccountID = uuid.Nil }, func(c *dbcli.Connection) { c.VersionID = uuid.Nil }} {
		bad := connection
		mutate(&bad)
		payload, _ := json.Marshal([]dbcli.Connection{bad})
		creds := cloneCredentials(old)
		creds.CLI["mysql"] = CredentialPayload{Files: []File{{Path: dbcli.ConfigPath("mysql"), Content: payload}}}
		forged := *snap
		forged.env.Credentials = new(toWire(creds))
		if _, e := forged.Credentials(); !errors.Is(e, ErrCredentials) {
			t.Fatal("invalid stored DB owner accepted", e)
		}
	}
}
