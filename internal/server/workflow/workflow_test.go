package workflow

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/google/uuid"
)

func ptr[T any](v T) *T { return &v }
func fixture() Definition {
	a := uuid.New()
	return Definition{StartStepID: "draft", Steps: []Step{
		{ID: "draft", Title: "起草", AgentID: a, InputFields: []Field{{"request", "object", true}}, OutputFields: []Field{{"text", "string", true}}, Inputs: map[string]Source{"request": {Kind: "request", Field: ""}}},
		{ID: "check", Title: "检查", AgentID: a, InputFields: []Field{{"text", "string", true}}, OutputFields: []Field{{"ok", "boolean", true}}, Inputs: map[string]Source{"text": {Kind: "node", NodeID: ptr("draft"), Field: "text"}}},
		{ID: "finish", Title: "完成", AgentID: a, InputFields: []Field{}, OutputFields: []Field{}, Inputs: map[string]Source{}},
	}, Edges: []Edge{{ID: "to_check", From: "draft", To: "check", Label: "检查"}, {ID: "revise", From: "check", To: "draft", Label: "修改", MaxTraversals: ptr(2)}, {ID: "finish", From: "check", To: "finish", Label: "通过"}}}
}

func TestValidationAndBoundedLoops(t *testing.T) {
	d := fixture()
	if err := d.Validate(Limits{MaxSteps: 10, MaxEdges: 10}); err != nil {
		t.Fatal(err)
	}
	cases := map[string]func(*Definition){
		"unbounded cycle": func(d *Definition) { d.Edges[1].MaxTraversals = nil },
		"zero bound":      func(d *Definition) { d.Edges[1].MaxTraversals = ptr(0) },
		"invalid source": func(d *Definition) {
			d.Steps[1].Inputs["text"] = Source{Kind: "node", NodeID: ptr("missing"), Field: "text"}
		},
		"unknown output": func(d *Definition) {
			d.Steps[1].Inputs["text"] = Source{Kind: "node", NodeID: ptr("draft"), Field: "unknown"}
		},
		"unknown input":  func(d *Definition) { d.Steps[1].Inputs["extra"] = Source{Kind: "request"} },
		"unreachable":    func(d *Definition) { d.Edges = d.Edges[:2] },
		"duplicate node": func(d *Definition) { d.Steps[1].ID = "draft" },
		"duplicate edge": func(d *Definition) { d.Edges[1].ID = d.Edges[0].ID },
		"duplicate field": func(d *Definition) {
			d.Steps[0].OutputFields = append(d.Steps[0].OutputFields, d.Steps[0].OutputFields[0])
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			d := fixture()
			mutate(&d)
			if d.Validate(Limits{}) == nil {
				t.Fatal("invalid graph accepted")
			}
		})
	}
	if d.Validate(Limits{MaxSteps: 2}) == nil || d.Validate(Limits{MaxEdges: 1}) == nil {
		t.Fatal("configured limits ignored")
	}
}

func TestLatestSuccessfulInputAndVersion(t *testing.T) {
	d := fixture()
	s, _ := d.Step("check")
	first, last := uuid.New(), uuid.New()
	results := []SuccessfulResult{{first, "draft", map[string]any{"text": "old"}}, {last, "draft", map[string]any{"text": "new"}}}
	input, versions, err := ResolveInputs(s, map[string]any{}, results)
	if err != nil || input["text"] != "new" || len(versions) != 1 || versions[0].NodeRunID != last {
		t.Fatal("not latest successful version", err)
	}
	results[1].Output["text"] = "mutated"
	if input["text"] != "new" {
		t.Fatal("input aliases result")
	}
	if _, _, err = ResolveInputs(s, nil, nil); err == nil {
		t.Fatal("missing required source accepted")
	}
	s.InputFields[0].Required = false
	input, _, err = ResolveInputs(s, nil, nil)
	if err != nil || len(input) != 0 {
		t.Fatal("optional missing source not omitted")
	}
}

func TestRequestMappingCopiesNestedValues(t *testing.T) {
	d := fixture()
	s, _ := d.Step("draft")
	request := map[string]any{"items": []any{"a", map[string]any{"count": 3}}}
	input, versions, err := ResolveInputs(s, request, nil)
	if err != nil || len(versions) != 0 {
		t.Fatal(err)
	}
	request["items"].([]any)[0] = "changed"
	if input["request"].(map[string]any)["items"].([]any)[0] != "a" {
		t.Fatal("request not copied")
	}
}

func TestStrictFieldsAndArtifacts(t *testing.T) {
	for _, number := range []string{"1.0", "10e-1", "9007199254740993", "0e-10000000000000000000000000"} {
		if err := ValidateValues([]Field{{"n", "integer", true}}, map[string]any{"n": json.Number(number)}); err != nil {
			t.Fatal("integer rejected", number)
		}
	}
	for _, number := range []string{"9007199254740992.1", "1e-100000000", "0.01", "1e999999999"} {
		if err := ValidateValues([]Field{{"n", "integer", true}}, map[string]any{"n": json.Number(number)}); err == nil {
			t.Fatal("non-integer or nonfinite number accepted", number)
		}
	}

	fields := []Field{{"count", "integer", true}, {"enabled", "boolean", true}, {"artifact", "artifact", false}}
	good := map[string]any{"count": json.Number("4"), "enabled": false, "artifact": map[string]any{"artifactId": uuid.New().String()}}
	if err := ValidateValues(fields, good); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []map[string]any{
		{"count": "4", "enabled": false},
		{"count": 4.2, "enabled": false},
		{"count": nil, "enabled": false},
		{"count": 4, "enabled": false, "extra": 1},
		{"count": 4, "enabled": false, "artifact": map[string]any{"artifactId": "bad"}},
	} {
		if ValidateValues(fields, bad) == nil {
			t.Fatal("invalid values accepted")
		}
	}
}

func TestEdgeSelectionDoesNotCountRetries(t *testing.T) {
	d := fixture()
	counts := map[string]int{"revise": 1}
	before := map[string]int{"revise": 1}
	for range 3 {
		edge, err := d.SelectEdge("check", ptr("revise"), counts)
		if err != nil || edge.To != "draft" {
			t.Fatal(err)
		}
	}
	if !reflect.DeepEqual(counts, before) {
		t.Fatal("selecting/retrying modified traversal counters")
	}
	counts["revise"] = 2
	if _, err := d.SelectEdge("check", ptr("revise"), counts); err == nil {
		t.Fatal("loop limit ignored")
	}
	if _, err := d.SelectEdge("draft", ptr("finish"), nil); err == nil {
		t.Fatal("other node edge accepted")
	}
	if e, err := d.SelectEdge("finish", nil, nil); err != nil || e != nil {
		t.Fatal("terminal node requires no edge")
	}
}
