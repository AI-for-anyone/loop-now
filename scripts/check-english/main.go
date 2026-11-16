// Command check-english keeps Go comments and log messages in English, so the code base stays readable for
// contributors who do not read Chinese. It fails (exit status 1) on:
//
//   - a comment line in any .go file that contains Chinese (Han characters or CJK/full-width punctuation) outside
//     quotation marks. Quoting a Chinese UI text or error message is allowed when it is wrapped in "...", “...”,
//     ‘...’, `...`, 「...」 or 『...』 on one line, followed by its English, e.g. “用本机的登录状态” ("Use local logins");
//   - a log message (the message argument of Debug/Info/Warn/Error, their Context variants, Log and LogAttrs on a
//     logger) that is a string literal containing Chinese;
//   - any line of the API contracts api/*.yaml that contains Chinese: their descriptions, summaries, titles and
//     comments are English, and so are examples. (A pattern that must match Chinese writes it with \u escapes, which
//     are plain ASCII in the file.)
//
// It does not look at Go string literals in general: error messages and test data are covered by a later step of the
// English pass, and shipped SQL migrations are never rewritten.
//
// Run from the repository root:
//
//	go run ./scripts/check-english
package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

// quoted matches one quoted span on a single line; its content may contain Chinese.
var quoted = regexp.MustCompile("\"[^\"\\n]*\"|“[^”\\n]*”|‘[^’\\n]*’|`[^`\\n]*`|「[^」\\n]*」|『[^』\\n]*』")

func chinese(s string) bool {
	for _, r := range s {
		if unicode.Is(unicode.Han, r) || (r >= 0x3000 && r <= 0x303f) || (r >= 0xff00 && r <= 0xffef) {
			return true
		}
	}
	return false
}

// logMethods maps a logger method to the index of its message argument.
var logMethods = map[string]int{
	"Debug": 0, "Info": 0, "Warn": 0, "Error": 0,
	"DebugContext": 1, "InfoContext": 1, "WarnContext": 1, "ErrorContext": 1,
	"Log": 2, "LogAttrs": 2,
}

// logger reports whether x looks like a logger: slog, log/logger/Log/Logger (also as a field), or a call that
// returns one (x.With(...), e.snapshotLog(snap)). testing.T's Error and Log are not matched: their receivers are
// named t, b or tb.
func logger(x ast.Expr) bool {
	name := ""
	switch v := x.(type) {
	case *ast.Ident:
		name = v.Name
	case *ast.SelectorExpr:
		name = v.Sel.Name
	case *ast.CallExpr:
		switch f := v.Fun.(type) {
		case *ast.SelectorExpr:
			name = f.Sel.Name
		case *ast.Ident:
			name = f.Name
		}
		return name == "With" || name == "WithGroup" || strings.HasSuffix(name, "Log") || strings.HasSuffix(name, "Logger") || strings.HasSuffix(name, "log")
	}
	switch name {
	case "slog", "log", "Log", "logger", "Logger":
		return true
	}
	return false
}

func main() {
	root := "."
	if len(os.Args) > 1 {
		root = os.Args[1]
	}
	var problems []string
	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			n := d.Name()
			if path != root && (strings.HasPrefix(n, ".") || n == "node_modules" || n == "vendor") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		f, err := parser.ParseFile(fset, path, nil, parser.ParseComments|parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		for _, g := range f.Comments {
			for _, c := range g.List {
				for i, line := range strings.Split(c.Text, "\n") {
					if chinese(quoted.ReplaceAllString(line, "")) {
						problems = append(problems, fmt.Sprintf("%s:%d: comment is not in English: %s", path, fset.Position(c.Pos()).Line+i, strings.TrimSpace(line)))
					}
				}
			}
		}
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			idx, ok := logMethods[sel.Sel.Name]
			if !ok || len(call.Args) <= idx || !logger(sel.X) {
				return true
			}
			// Any string literal inside the message counts, also in a concatenation with non-literal parts
			// (name()+" started") or a fmt.Sprintf format.
			ast.Inspect(call.Args[idx], func(m ast.Node) bool {
				if lit, ok := m.(*ast.BasicLit); ok && lit.Kind == token.STRING {
					if s, err := strconv.Unquote(lit.Value); err == nil && chinese(s) {
						problems = append(problems, fmt.Sprintf("%s:%d: log message is not in English: %q", path, fset.Position(lit.Pos()).Line, s))
					}
				}
				return true
			})
			return true
		})
		return nil
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "check-english:", err)
		os.Exit(2)
	}
	contracts, err := filepath.Glob(filepath.Join(root, "api", "*.yaml"))
	if err != nil {
		fmt.Fprintln(os.Stderr, "check-english:", err)
		os.Exit(2)
	}
	for _, path := range contracts {
		src, err := os.ReadFile(path)
		if err != nil {
			fmt.Fprintln(os.Stderr, "check-english:", err)
			os.Exit(2)
		}
		for i, line := range strings.Split(string(src), "\n") {
			if chinese(line) {
				problems = append(problems, fmt.Sprintf("%s:%d: API contract text is not in English: %s", path, i+1, strings.TrimSpace(line)))
			}
		}
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		for _, p := range problems {
			fmt.Println(p)
		}
		fmt.Printf("check-english: %d problem(s). Write Go comments, log messages and the API contracts in English; a quoted Chinese original followed by its English is allowed in Go comments.\n", len(problems))
		os.Exit(1)
	}
	fmt.Println("check-english: ok")
}
