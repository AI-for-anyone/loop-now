package commands_test

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"
)

var update = flag.Bool("update", false, "update golden files")

// The complete schema output is pinned in a golden file, so any change to the commands shows up as a diff in
// review.
func TestSchemaGolden(t *testing.T) {
	r := newEnv(t).run("schema")
	r.ok(t)
	var buf bytes.Buffer
	json.Indent(&buf, r.Env.Data, "", "  ")
	buf.WriteByte('\n')
	p := filepath.Join("testdata", "schema.json")
	if *update {
		os.WriteFile(p, buf.Bytes(), 0o644)
	}
	want, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read golden file: %v (generate it with -update)", err)
	}
	if !bytes.Equal(buf.Bytes(), want) {
		t.Errorf("schema output differs from %s; after checking the change, update it with go test -run TestSchemaGolden -update", p)
	}
}
