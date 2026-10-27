package e2e_test

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// Run only after the server owner hands off stable DB handlers/migration.
// This uses a fresh TEST schema, Redis prefix and TLS App. No model turns,
// native database clients, provider calls, host credentials or old tasks.
func TestDBConnectionsCLIWithApp(t *testing.T) {
	w := newWorld(t, true)
	s := w.start(nil)
	defer s.stop()
	t.Logf("DB CLI resources: schema=%s redisPrefix=%s consoleAddr=%s tlsAddr=%s", w.schema, w.cfg.RedisKeyPrefix, w.addr, w.extAddr)
	_, _, token := s.issueToken("", "db-connections-cli", nil)
	ext := newLoopctl(t, map[string]string{"LOOPCTL_URL": s.ext, "LOOPCTL_TOKEN": token, "LOOPCTL_CA_FILE": w.caFile})
	project := s.defaultProject()
	provider, model := cliKeyProvider(t, ext)
	agent := ext.run("agent", "create", "--project", project, "--name", "DB集合 CLI", "--provider", provider, "--model", model).ok(t)["id"].(string)
	for _, name := range []string{"psql", "mysql"} {
		t.Run(name, func(t *testing.T) {
			show := func() map[string]any { return ext.run("agent", "db-connections", "show", agent, "--cli", name).ok(t) }
			version := func() string { return fmt.Sprintf("%.0f", ext.run("agent", "get", agent).ok(t)["configVersion"]) }
			set := func(v string, flags ...string) result {
				args := []string{"agent", "db-connections", "set", agent, "--cli", name, "--expected-config-version", v, "--yes"}
				return ext.run(append(args, flags...)...)
			}
			create := func(password string) map[string]any {
				mode := "disable"
				if name == "mysql" {
					mode = "DISABLED"
				}
				b, _ := json.Marshal(map[string]any{"type": "cli", "fields": map[string]string{"host": "127.0.0.1", "user": "synthetic-user", "password": password, "database": "synthetic-db", "sslMode": mode}})
				ext.stdin = string(b)
				r := ext.run("project", "cli-account", "create", project, "--cli", name, "--name", "同名DB连接", "--from-file", "-")
				data := r.ok(t)
				if strings.Contains(r.Stdout, password) || r.Meta.RequestID == "" {
					t.Fatal("secret leaked or no actual request ID")
				}
				return data
			}
			a, b := create("synthetic-db-password-A"), create("synthetic-db-password-B")
			idA, idB := a["id"].(string), b["id"].(string)
			if idA == idB {
				t.Fatal("duplicate display names merged identities")
			}
			// Saving accounts does not grant the Agent this CLI.
			set(version(), "--account-id", idA).fails(t, "validation_failed", 9)
			ext.run("agent", "cli", "add", agent, name).ok(t)
			if state := show(); state["mode"] != "legacy_single" || state["status"] != "selection_required" || len(state["accountIds"].([]any)) != 0 {
				t.Fatalf("candidates are not authorized connections: %v", state)
			}
			ext.run("agent", "credential-selection", "set", agent, "--cli", name, "--account-id", idA, "--expected-config-version", version()).ok(t)
			if state := show(); state["mode"] != "legacy_single" || !reflect.DeepEqual(state["accountIds"], []any{idA}) {
				t.Fatalf("legacy resolution: %v", state)
			}
			v := version()
			set(v, "--account-id", idB, "--account-id", idA).ok(t)
			state := show()
			ids := []string{idA, idB}
			sort.Strings(ids)
			if state["mode"] != "explicit_set" || !reflect.DeepEqual(state["accountIds"], []any{ids[0], ids[1]}) {
				t.Fatalf("atomic sorted set: %v", state)
			}
			impact := ext.run("project", "cli-account", "impact", project, "--cli", name, "--account-id", idA).ok(t)
			users := impact["agents"].([]any)
			if len(users) != 1 || users[0].(map[string]any)["agentId"] != agent || users[0].(map[string]any)["mode"] != "connection_set" {
				t.Fatalf("collection impact must describe current set user: %v", impact)
			}
			before := state["configVersion"]
			set(v, "--empty").fails(t, "precondition_failed", 8)
			for _, action := range []string{"set", "clear"} {
				args := []string{"agent", "credential-selection", action, agent, "--cli", name, "--expected-config-version", version()}
				if action == "set" {
					args = append(args, "--account-id", idB)
				} else {
					args = append(args, "--yes")
				}
				r := ext.run(args...)
				r.fails(t, "credential_selection_mode_conflict", 8)
				var details map[string]any
				if err := json.Unmarshal(r.Error.Details, &details); err != nil {
					t.Fatal(err)
				}
				if details["agentId"] != agent || details["cli"] != name || details["mode"] != "explicit_set" || details["configVersion"] != before {
					t.Fatalf("actual conflict details: %v", details)
				}
			}
			if after := show(); !reflect.DeepEqual(after, state) {
				t.Fatal("CAS/single conflict mutated set")
			}
			set(version(), "--empty").ok(t)
			if empty := show(); empty["mode"] != "explicit_set" || empty["status"] != "not_configured" || len(empty["accountIds"].([]any)) != 0 {
				t.Fatalf("empty fallback: %v", empty)
			}
			ext.run("agent", "db-connections", "clear", agent, "--cli", name, "--expected-config-version", version(), "--yes").ok(t)
			if restored := show(); restored["mode"] != "legacy_single" || !reflect.DeepEqual(restored["accountIds"], []any{idA}) {
				t.Fatalf("clear must restore original single: %v", restored)
			}
			v = version()
			ext.run("agent", "db-connections", "clear", agent, "--cli", name, "--expected-config-version", v, "--yes").ok(t)
			if version() != v {
				t.Fatal("idempotent clear bumped config version")
			}
			set(version(), "--account-id", idA, "--account-id", idB).ok(t)
			ext.run("project", "cli-account", "delete", project, idB, "--expected-revision", "1", "--yes").ok(t)
			deleted := show()
			if deleted["mode"] != "explicit_set" || deleted["status"] != "account_deleted" || len(deleted["accountIds"].([]any)) != 2 {
				t.Fatalf("deleted member hidden or fell back: %v", deleted)
			}
			ext.run("agent", "cli", "remove", agent, name, "--yes").ok(t)
			if account := ext.run("project", "cli-account", "get", project, idA).ok(t); account["status"] == "deleted" {
				t.Fatal("permission removal deleted project account")
			}
			if impact := ext.run("project", "cli-account", "impact", project, "--cli", name, "--account-id", idA).ok(t); len(impact["agents"].([]any)) != 0 {
				t.Fatal("permission removal retained current set impact")
			}
			ext.run("agent", "cli", "add", agent, name).ok(t)
			if state := show(); state["mode"] != "legacy_single" {
				t.Fatal("permission removal retained collection marker")
			}
			t.Logf("%s: CRUD reuse, permission, 2 connections, CAS/legacy409 details, empty/restore/idempotence/deleted member passed; no run dispatched", name)
		})
	}
	ext.run("agent", "delete", agent, "--yes").ok(t)
	ext.run("agent", "db-connections", "show", agent, "--cli", "psql").fails(t, "not_found", 7)
}
