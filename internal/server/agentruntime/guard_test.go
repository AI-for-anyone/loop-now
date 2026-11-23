package agentruntime

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// guardAllowed are the files (relative to the repository root, / separated) that may compare runtime ids: the registry
// itself and the executor's driver files. Everything else asks the registry about a capability.
var guardAllowed = []string{
	"internal/server/agentruntime/*.go",
	"internal/server/executor/local/driver.go",
	"internal/server/executor/local/*_driver.go",
}

// TestNoRuntimeIDComparisons fails when Go code outside the registry and the driver files compares a runtime id or a
// provider a runtime lists: == or != against one of the constants or string literals, or a switch case on one. Test
// files are not checked.
func TestNoRuntimeIDComparisons(t *testing.T) {
	root := repoRoot(t)
	var found []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != root && (strings.HasPrefix(d.Name(), ".") || d.Name() == "node_modules" || d.Name() == "testdata" || d.Name() == "vendor") {
				return filepath.SkipDir
			}
			return nil
		}
		rel := filepath.ToSlash(strings.TrimPrefix(path, root+string(filepath.Separator)))
		if !strings.HasSuffix(rel, ".go") || strings.HasSuffix(rel, "_test.go") || guardAllows(rel) {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		hits, err := runtimeIDComparisons(rel, src)
		if err != nil {
			return err
		}
		found = append(found, hits...)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(found) > 0 {
		t.Fatalf("runtime ids are compared outside the registry and the driver files; ask agentruntime about a capability instead:\n%s", strings.Join(found, "\n"))
	}
}

// The guard itself finds comparisons with the constants (also under another import name), with string literals and in
// switch cases, and leaves other uses of the ids alone.
func TestRuntimeIDComparisonsFound(t *testing.T) {
	src := `package x

import rt "loop-now/internal/server/agentruntime"

func f(id, p string) {
	if id == rt.ClaudeCode || "pi" != id {
	}
	if rt.ForProvider(p).ID == rt.Pi {
	}
	switch id {
	case "claude-code":
	}
	_ = p == rt.ClaudeCodeProvider
	_ = map[string]bool{rt.Pi: true}
	_ = rt.ForProvider(p).Capabilities.Bundled
	_ = id == "pipe"
	var other struct{ Pi string }
	_ = id == other.Pi
}
`
	hits, err := runtimeIDComparisons("x.go", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"x.go:6", "x.go:6", "x.go:8", "x.go:11", "x.go:13"}
	var got []string
	for _, h := range hits {
		got = append(got, h[:strings.Index(h, ": ")])
	}
	if !slices.Equal(got, want) {
		t.Fatalf("got %v, want %v\n%s", got, want, strings.Join(hits, "\n"))
	}
}

// runtimeIDComparisons returns "file:line: expression" for each comparison of a runtime id in one Go file.
func runtimeIDComparisons(name string, src []byte) ([]string, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, name, src, parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	pkg := ""
	for _, imp := range file.Imports {
		if path, _ := strconv.Unquote(imp.Path.Value); path == "loop-now/internal/server/agentruntime" {
			pkg = "agentruntime"
			if imp.Name != nil {
				pkg = imp.Name.Name
			}
		}
	}
	literals := map[string]bool{}
	for _, r := range All() {
		literals[r.ID] = true
		for _, p := range r.Providers {
			literals[p] = true
		}
	}
	isID := func(e ast.Expr) bool {
		switch e := e.(type) {
		case *ast.BasicLit:
			v, err := strconv.Unquote(e.Value)
			return e.Kind == token.STRING && err == nil && literals[v]
		case *ast.SelectorExpr:
			x, ok := e.X.(*ast.Ident)
			return ok && pkg != "" && x.Name == pkg && slices.Contains([]string{"Pi", "ClaudeCode", "ClaudeCodeProvider"}, e.Sel.Name)
		}
		return false
	}
	var out []string
	report := func(n ast.Node) {
		out = append(out, fmt.Sprintf("%s:%d: %s", name, fset.Position(n.Pos()).Line, src[fset.Position(n.Pos()).Offset:fset.Position(n.End()).Offset]))
	}
	ast.Inspect(file, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.BinaryExpr:
			if (n.Op == token.EQL || n.Op == token.NEQ) && (isID(n.X) || isID(n.Y)) {
				report(n)
			}
		case *ast.CaseClause:
			for _, e := range n.List {
				if isID(e) {
					report(e)
				}
			}
		}
		return true
	})
	return out, nil
}

func guardAllows(rel string) bool {
	for _, pattern := range guardAllowed {
		if ok, _ := filepath.Match(pattern, rel); ok {
			return true
		}
	}
	return false
}

// repoRoot is the directory holding go.mod, found upwards from the package directory.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs(".")
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found")
		}
		dir = parent
	}
}
