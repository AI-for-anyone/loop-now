package credentialmodel

import (
	"encoding/json"
	"github.com/google/uuid"
	"testing"
)

func TestLarkStartPresenceAndOperationIsolation(t *testing.T) {
	start := map[string]any{"operationId": uuid.NewString(), "cli": "lark-cli", "authType": "lark_user", "sourceAccountId": uuid.NewString(), "expectedSourceRevision": 1}
	check := func(v map[string]any, want bool) {
		t.Helper()
		raw, _ := json.Marshal(v)
		var in CLIAuthStart
		if e := json.Unmarshal(raw, &in); (e == nil) != want {
			t.Fatal("start decoder contract disagreement")
		}
	}
	check(start, true)
	for _, alias := range []string{"SourceAccountID", "SOURCEACCOUNTID", "Name"} {
		start[alias] = start["sourceAccountId"]
		check(start, false)
		delete(start, alias)
	}
	for _, key := range []string{"sourceAccountId", "expectedSourceRevision"} {
		old := start[key]
		start[key] = nil
		check(start, false)
		delete(start, key)
		check(start, false)
		start[key] = old
	}
	for _, key := range []string{"name", "accountId", "expectedRevision", "appSecret", "scope"} {
		start[key] = nil
		check(start, false)
		delete(start, key)
	}
	for _, cli := range []string{"gcloud", "gh", "lark-cli"} {
		o := CLIAuthOperation{CLI: cli}
		raw, e := json.Marshal(o)
		if e != nil {
			t.Fatal(e)
		}
		var fields map[string]json.RawMessage
		json.Unmarshal(raw, &fields)
		_, code := fields["userCode"]
		if code != (cli != "gcloud") {
			t.Fatal("userCode cross-CLI shape changed")
		}
		for _, k := range []string{"sourceAccountId", "sourceVersionId", "sourceRevision"} {
			v, ok := fields[k]
			if ok != (cli == "lark-cli") || ok && string(v) != "null" {
				t.Fatal("source projection cross-CLI shape changed")
			}
		}
	}
}
