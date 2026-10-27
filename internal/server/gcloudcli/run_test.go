package gcloudcli

import "testing"

func TestControlledArguments(t *testing.T) {
	for _, args := range [][]string{{"--verbosity=info", "auth", "login"}, {"--project=p", "beta", "auth", "login"}, {"alpha", "--project", "p", "config", "set", "account", "x"}, {"projects", "list", "--authorization-token-file=x"}, {"projects", "list", "--authority-selector=x"}, {"--verbosity", "info", "auth", "login"}, {"--project", "p", "auth", "login"}, {"beta", "--verbosity", "info", "auth", "login"}, {"--unknown", "p", "auth", "login"}, {"--format", "json", "config", "set", "account", "x"}, {"beta", "auth", "login"}, {"alpha", "config", "set", "account", "x"}, {"auth", "login"}, {"config", "set", "account", "x"}, {"projects", "list", "--account=x"}, {"run", "deploy", "--credential-file-override", "x"}, {"projects", "list", "--verbosity=debug"}, {"--configuration=x", "projects", "list"}, {"projects", "list", "--flags-file=x"}, {"projects", "list", "--log-http"}} {
		if Allowed(args) {
			t.Fatal("authentication override accepted")
		}
	}
	for _, args := range [][]string{{"--project", "p", "projects", "list"}, {"--verbosity", "info", "beta", "projects", "list"}, {"projects", "list", "--format=json"}, {"run", "deploy", "service", "--project=fixture", "--quiet"}, {"builds", "list", "--verbosity=warning"}, {"--version"}} {
		if !Allowed(args) {
			t.Fatal("normal native command rejected")
		}
	}
}
