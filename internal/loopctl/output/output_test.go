package output

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"
)

var update = flag.Bool("update", false, "update golden files")

// The fixed exit codes are the CLI's external behavior; the expected values are kept separately in a test fixture.
func TestExitTableContract(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "exit-codes.json"))
	if err != nil {
		t.Fatal(err)
	}
	var got []ExitInfo
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	table := ExitTable()
	if len(got) != len(table) {
		t.Fatalf("the fixture has %d rows, the code has %d", len(got), len(table))
	}
	for i, e := range table {
		if got[i].Code != e.Code || got[i].Category != e.Category {
			t.Errorf("row %d: fixture has %d/%s, code has %d/%s", i, got[i].Code, got[i].Category, e.Code, e.Category)
		}
		if e.Code != 0 && ExitCode(e.Category) != e.Code {
			t.Errorf("ExitCode(%s) = %d, want %d", e.Category, ExitCode(e.Category), e.Code)
		}
	}
}

func TestCategoryForStatus(t *testing.T) {
	cases := map[int]Category{
		302: CategoryInternal, 400: CategoryInvalid, 401: CategoryUnauthenticated, 403: CategoryForbidden,
		404: CategoryNotFound, 405: CategoryServer, 409: CategoryConflict, 412: CategoryConflict,
		413: CategoryInvalid, 422: CategoryInvalid, 429: CategoryServer, 500: CategoryServer,
		502: CategoryUnavailable, 503: CategoryUnavailable, 504: CategoryUnavailable,
	}
	for status, want := range cases {
		if got := CategoryForStatus(status); got != want {
			t.Errorf("HTTP %d: category is %s, want %s", status, got, want)
		}
	}
}

// Every local error code has a category, and every category is in the exit code table.
func TestLocalCodes(t *testing.T) {
	cats := map[Category]bool{}
	for _, e := range ExitTable() {
		cats[e.Category] = true
	}
	for _, c := range LocalCodes() {
		if !cats[c.Category] {
			t.Errorf("%s: category %s is not in the exit code table", c.Code, c.Category)
		}
	}
	if e := Errorf("no_such_code", "x"); e.Category != CategoryInternal {
		t.Errorf("an unknown error code should fall under internal, got %s", e.Category)
	}
}

func golden(t *testing.T, name string, got []byte) {
	t.Helper()
	p := filepath.Join("testdata", name)
	if *update {
		if err := os.WriteFile(p, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read golden file: %v (generate it with -update)", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("%s differs\ngot: %s\nwant: %s", name, got, want)
	}
}

func TestEnvelopeGolden(t *testing.T) {
	meta := Meta{ClientVersion: "1.2.3", Identity: "external", Profile: "default", ServerVersion: "1.2.3", RequestID: "0192f1a2-b3c4-7d8e-9f0a-1b2c3d4e5f60", Attempts: 1}
	var buf bytes.Buffer

	buf.Reset()
	WriteSuccess(&buf, json.RawMessage("{\n  \"text\": \"<b>a & b</b>\"\n}"), nil, meta)
	golden(t, "success.json", buf.Bytes())

	buf.Reset()
	WriteSuccess(&buf, nil, nil, Meta{ClientVersion: "1.2.3"})
	golden(t, "success-null.json", buf.Bytes())

	buf.Reset()
	WriteFailure(&buf, Errorf(CodeConfirmationRequired, "--yes required"), nil, Meta{ClientVersion: "1.2.3"})
	golden(t, "local-error.json", buf.Bytes())

	buf.Reset()
	e := &Error{Code: "agent_busy", Category: CategoryConflict, Message: "忙", HTTPStatus: 409, // check-english: zh (non-ASCII message stays raw UTF-8 in the envelope)
		Details: json.RawMessage(`{"currentRunId":"r","queued":{"interrupt":0,"normal":1}}`), Fields: json.RawMessage(`[{"field":"text","message":"too long"}]`)}
	WriteFailure(&buf, e, nil, meta)
	golden(t, "server-error.json", buf.Bytes())

	buf.Reset()
	w := []Warning{{Code: WarnVersionMismatch, Message: "version mismatch", Details: map[string]string{"clientVersion": "1.2.2", "serverVersion": "1.2.3"}}}
	WriteSuccess(&buf, map[string]int{"n": 1}, w, meta)
	golden(t, "warning.json", buf.Bytes())

	for _, name := range []string{"success.json", "success-null.json", "local-error.json", "server-error.json", "warning.json"} {
		b, _ := os.ReadFile(filepath.Join("testdata", name))
		if bytes.Count(b, []byte("\n")) != 1 || b[len(b)-1] != '\n' {
			t.Errorf("%s should be a single line ending in a newline", name)
		}
	}
}
