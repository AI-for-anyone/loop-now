package toolcommand

import (
	"path/filepath"
	"strings"
)

// A tool's target is what it worked on: the files of a file tool or the pattern or query of a search tool. Never the
// contents, never other arguments.
const (
	MaxTargetChars = 512
	MaxQueryChars  = 200
	HiddenQuery    = "[Hidden because it contains credentials]"
)

// Files gives the display form of the files a file tool read or changed: each relative to dir when it lies inside dir
// (dir may be empty), absolute otherwise, joined with ", ". One long path keeps its end (the file name); a long list
// keeps its first files.
func Files(dir string, paths []string) string {
	var out []string
	seen := map[string]bool{}
	for _, p := range paths {
		p = strings.TrimSpace(oneLine(p))
		if p == "" {
			continue
		}
		p = relative(dir, p)
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	if len(out) == 1 {
		if r := []rune(out[0]); len(r) > MaxTargetChars {
			return "…" + string(r[len(r)-MaxTargetChars+1:])
		}
		return out[0]
	}
	return cut(strings.Join(out, ", "), MaxTargetChars)
}

// Query gives the display form of a search pattern or query, on the same redaction rule as commands.
func Query(q string) string {
	if strings.TrimSpace(q) == "" {
		return ""
	}
	if sensitive.MatchString(q) {
		return HiddenQuery
	}
	return cut(strings.TrimSpace(oneLine(q)), MaxQueryChars)
}

func relative(dir, p string) string {
	if dir == "" || !filepath.IsAbs(p) {
		return p
	}
	rel, err := filepath.Rel(filepath.Clean(dir), filepath.Clean(p))
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return p
	}
	return rel
}

func oneLine(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || r >= 0x80 && r < 0xa0 {
			return ' '
		}
		return r
	}, s)
}

func cut(s string, max int) string {
	if r := []rune(s); len(r) > max {
		return string(r[:max-1]) + "…"
	}
	return s
}
