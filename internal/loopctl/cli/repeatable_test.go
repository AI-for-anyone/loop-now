package cli

import (
	"context"
	"reflect"
	"testing"

	"loop-now/internal/loopctl/output"
)

func TestRepeatableFlags(t *testing.T) {
	r := NewRegistry()
	r.Add(&Command{Path: []string{"pick"}, Flags: []Flag{
		{Name: "option", Type: TypeString, Repeatable: true},
		{Name: "number", Type: TypeInt, Repeatable: true},
		{Name: "single", Type: TypeString},
	}, Run: func(context.Context, *Invocation) (any, error) { return nil, nil }})
	for _, tc := range []struct {
		args []string
		want []string
		code string
	}{
		{[]string{"--option", "first", "--single", "x", "--option=second", "--option", "third"}, []string{"first", "second", "third"}, ""},
		{[]string{"--option", "same", "--option", "same"}, []string{"same", "same"}, ""},
		{[]string{"--option=", "--option", "valid"}, nil, output.CodeInvalidArgument},
		{[]string{"--option", "valid", "--option="}, nil, output.CodeInvalidArgument},
		{[]string{"--option"}, nil, output.CodeMissingArgument},
		{[]string{"--single", "a", "--single", "b"}, nil, output.CodeInvalidArgument},
		{[]string{"--number", "bad", "--number", "2"}, nil, output.CodeInvalidArgument},
	} {
		p, err := parse(r, append([]string{"pick"}, tc.args...))
		if tc.code != "" {
			if err == nil || err.Code != tc.code {
				t.Fatalf("%v expected %s got %v", tc.args, tc.code, err)
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		inv := &Invocation{Cmd: p.cmd, flags: p.flags, repeated: p.repeated, accessed: map[string]bool{}}
		got := inv.Strings("option")
		if !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("option order changed: %v", got)
		}
		got[0] = "changed"
		if reflect.DeepEqual(got, inv.Strings("option")) {
			t.Fatal("Strings returned mutable parser storage")
		}
		if inv.Strings("missing") != nil {
			t.Fatal("absent option fabricated values")
		}
	}
	schema := Describe(r.Find("pick"))
	if !schema.Flags[0].Repeatable || schema.Flags[2].Repeatable {
		t.Fatal("schema lost repeatability")
	}
}
