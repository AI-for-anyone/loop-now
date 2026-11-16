package pluginmanifest

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// ProviderState declares which state of a model provider registered by the plugin travels with the project's
// accounts (plugins/SPEC.md §13): Paths are the paths, relative to HOME (separated by /), where the extension keeps
// its login and state; they are linked to the account's state directory when Pi starts.
type ProviderState struct {
	Paths []string `json:"paths"`
}

// MaxProviderStatePaths is the maximum number of paths one provider may declare.
const MaxProviderStatePaths = 8

var providerIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// CheckProviderState validates a providerState declaration (the manifest's loopNow.providerState or a market
// entry's providerState). Paths must be relative to HOME: not starting with /, without backslashes or colons,
// without empty, . or .. segments, and not under the platform's own .loop-now.
func CheckProviderState(field string, decl map[string]ProviderState) error {
	if len(decl) > 16 {
		return invalid(field + " can declare at most 16 providers.")
	}
	for provider, s := range decl {
		at := field + "." + provider
		if !providerIDPattern.MatchString(provider) {
			return invalid(at + ": The provider id is invalid.")
		}
		if len(s.Paths) == 0 || len(s.Paths) > MaxProviderStatePaths {
			return invalid(fmt.Sprintf("%s.paths must have 1 to %d paths.", at, MaxProviderStatePaths))
		}
		seen := map[string]bool{}
		for _, p := range s.Paths {
			if err := checkStatePath(p); err != nil {
				return invalid(at + ".paths: " + strconv.Quote(p) + " " + err.Error())
			}
			for q := range seen {
				if q == p || strings.HasPrefix(p, q+"/") || strings.HasPrefix(q, p+"/") {
					return invalid(at + ".paths: " + strconv.Quote(p) + " and " + strconv.Quote(q) + " are the same or one contains the other.")
				}
			}
			seen[p] = true
		}
	}
	return nil
}

func checkStatePath(p string) error {
	if p == "" || len(p) > 200 || strings.HasPrefix(p, "/") || strings.ContainsAny(p, "\\:") || strings.IndexFunc(p, unicode.IsControl) >= 0 {
		return fmt.Errorf("must be a path relative to HOME (separated by /, at most 200 bytes).")
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return fmt.Errorf("must not contain empty, . or .. segments.")
		}
	}
	if p == ".loop-now" || strings.HasPrefix(p, ".loop-now/") {
		return fmt.Errorf("must not be inside the platform's own .loop-now directory.")
	}
	return nil
}

// OverlappingStatePath finds an overlap between the declared state paths and the HOME paths the platform manages
// (such as a CLI template's credential files): being equal, or one being a parent directory of the other, both
// count. All paths are separated by /; ok is false when there is no overlap.
func OverlappingStatePath(declared, managed []string) (decl, path string, ok bool) {
	for _, d := range declared {
		for _, m := range managed {
			m = strings.Trim(strings.ReplaceAll(m, "\\", "/"), "/")
			if m == "" {
				continue
			}
			if d == m || strings.HasPrefix(m, d+"/") || strings.HasPrefix(d, m+"/") {
				return d, m, true
			}
		}
	}
	return "", "", false
}
