package dbcli

import (
	"github.com/google/uuid"
	"testing"
)

func TestVerificationMySQLArgumentsRemainBounded(t *testing.T) {
	id := uuid.NewString()
	args := []string{"--loop-connection-id", id, "--batch", "--skip-column-names", "--skip-auto-rehash", "--connect-timeout=10", "--execute", "SELECT 1"}
	got, list, native, e := parseArgs("mysql", args)
	if e != nil || list || got.String() != id || len(native) == 0 {
		t.Fatal("fixed verification plan rejected", e)
	}
	for _, bad := range [][]string{{"--connect-timeout=0"}, {"--connect-timeout=999999"}, {"--connect-timeout", "-1"}, {"--connect-timeout=10", "--host=external"}, {"--skip-auto-rehash", "--password=secret"}} {
		if _, _, _, e := parseArgs("mysql", bad); e == nil {
			t.Fatal("unbounded/identity override accepted")
		}
	}
	if _, _, _, e := parseArgs("psql", []string{"--connect-timeout=10"}); e == nil {
		t.Fatal("mysql-only option escaped tool scope")
	}
}
