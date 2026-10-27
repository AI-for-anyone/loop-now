package cli

import "testing"

func TestCommandWithChild(t *testing.T) {
	r := NewRegistry()
	r.Add(&Command{Path: []string{"parent"}, Args: []Arg{{Name: "value", Variadic: true}}})
	r.Add(&Command{Path: []string{"parent", "status"}})
	for _, tc := range []struct {
		argv    []string
		command string
		argc    int
	}{
		{[]string{"parent"}, "parent", 0},
		{[]string{"parent", "status"}, "parent status", 0},
		{[]string{"parent", "value"}, "parent", 1},
		{[]string{"parent", "--", "status"}, "parent", 1},
		{[]string{"parent", "status", "--help"}, "parent status", 0},
	} {
		p, err := parse(r, tc.argv)
		if err != nil || p.cmd.Name() != tc.command || len(p.args) != tc.argc {
			t.Fatalf("%v: %+v %v", tc.argv, p, err)
		}
	}
}
