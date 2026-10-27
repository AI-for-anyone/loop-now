package commands_test

import (
	"encoding/json"
	"strings"
	"testing"

	"loop-now/internal/loopctl/clitest"
	"loop-now/internal/loopctl/output"
)

// Only the formal credential envelope is parsed by loopctl. Template validation,
// rendering, native availability and remote authentication belong to the server.
func TestCloudDatabaseCredentialEnvelope(t *testing.T) {
	for _, name := range []string{"gcloud", "psql", "mysql"} {
		t.Run(name, func(t *testing.T) {
			s, e := credentialStub(t)
			fields := map[string]string{"host": "fixture.invalid", "user": "synthetic-user", "password": "synthetic-password", "database": "synthetic-database", "sslMode": "disable"}
			if name == "mysql" {
				fields["sslMode"] = "DISABLED"
			}
			if name == "gcloud" {
				fields = map[string]string{"serviceAccountJson": "{\n\"type\":\"service_account\",\"private_key\":\"synthetic-only-not-valid-key\"\n}", "defaultProject": "synthetic-gcp-target"}
			}
			a := projectAccount("cli")
			a["cli"] = name
			if name == "gcloud" {
				a["authMethod"] = "service_account"
			}
			s.On("POST /projects/{projectId}/credential-accounts", clitest.Sample{Status: 201, Body: a})
			s.On("PUT /projects/{projectId}/credential-accounts/{accountId}/credential", clitest.Sample{Status: 200, Body: a})
			credential := map[string]any{"type": "cli", "fields": fields}
			b, _ := json.Marshal(credential)
			e.stdin = string(b)
			r := e.run("project", "cli-account", "create", credentialProjectID, "--cli", name, "--name", "stable label", "--from-file", "-")
			r.ok(t)
			body := lastBody(t, s)
			want, _ := json.Marshal(credential)
			got, _ := json.Marshal(body["credential"])
			if string(got) != string(want) || body["name"] != "stable label" || body["cli"] != name || len(body) != 4 {
				t.Fatalf("credential must be unchanged formal envelope: %v", body)
			}
			if strings.Contains(r.Stdout, "synthetic-password") || strings.Contains(r.Stdout, "synthetic-only-not-valid-key") {
				t.Fatal("secret echoed")
			}
			e.run("project", "cli-account", "replace", credentialProjectID, credentialAccountID, "--expected-revision", "2", "--from-file", writeFile(t, "credential.json", string(b)), "--yes").ok(t)
			body = lastBody(t, s)
			got, _ = json.Marshal(body["credential"])
			if len(body) != 2 || body["expectedRevision"] != float64(2) || string(got) != string(want) {
				t.Fatalf("replace envelope: %v", body)
			}
			if len(s.Requests()) != 2 {
				t.Fatal("no template GET or automatic validation/provider request")
			}
		})
	}
}

func TestGoogleSAInputBoundary(t *testing.T) {
	for _, input := range []string{
		`{"type":"cli","fields":{"serviceAccountJson":{"type":"service_account"}}}`,
		`{"type":"cli","fields":{"serviceAccountJson":"synthetic-secret"},"metadata":{"name":"label"}}`,
		`{"type":"cli","fields":{"serviceAccountJson":"synthetic-secret"},"expectedRevision":1}`,
		`{"credential":{"type":"cli","fields":{"serviceAccountJson":"synthetic-secret"}}}`,
		`{"type":"cli","fields":{"serviceAccountJson":"synthetic-secret","serviceAccountJson":"second"}}`,
	} {
		s, e := credentialStub(t)
		e.stdin, e.block = input, true
		r := e.run("project", "cli-account", "create", "named-project", "--cli", "gcloud", "--name", "label", "--from-file", "-")
		r.fails(t, output.CodeInvalidArgument, 2)
		if len(s.Requests()) != 0 || e.dialCount() != 0 || strings.Contains(r.Stdout, "synthetic-secret") {
			t.Fatal("invalid secret envelope dialed/leaked")
		}
	}
	s, e := credentialStub(t)
	e.run("project", "cli-account", "create", "named-project", "--cli", "gcloud", "--name", "label", "--service-account-json", "synthetic-secret", "--from-file", "-").fails(t, output.CodeInvalidArgument, 2)
	if len(s.Requests()) != 0 {
		t.Fatal("secret argv made request")
	}
}
