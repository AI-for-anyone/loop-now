package plugins

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"

	"loop-now/internal/pluginmanifest"
	"loop-now/internal/server/apperr"
)

// Plugin READMEs (plugins/SPEC.md §2.3): README.md and the per-language README.<language>.md in the plugin's root
// directory, shown by the console in the plugin details and in the install preview.

// MaxReadmeBytes is the size limit of a returned README; anything beyond it is cut off.
const MaxReadmeBytes = 1 << 20

// Readme is the API's PluginReadme; File and Markdown are nil when there is no README at all.
type Readme struct {
	File      *string `json:"file"`
	Markdown  *string `json:"markdown"`
	Truncated bool    `json:"truncated"`
}

// Readme reads the README of the current version of an installed plugin; an empty locale means no language was
// specified.
func (s *Service) Readme(ctx context.Context, id, locale string) (Readme, error) {
	p, err := s.Store.GetPlugin(ctx, id)
	if err != nil {
		return Readme{}, err
	}
	return ReadReadme(p.InstallDir, locale)
}

// PreviewReadme reads the README of the plugin in a preview; it returns 404 when the preview has expired, has
// already been used for an install, or does not exist.
func (s *Service) PreviewReadme(id uuid.UUID, locale string) (Readme, error) {
	s.expirePreviews()
	s.mu.Lock()
	pv := s.previews[id]
	s.mu.Unlock()
	if pv == nil {
		return Readme{}, apperr.NotFound("Preview")
	}
	r, err := ReadReadme(pv.dir, locale)
	if errors.Is(err, fs.ErrNotExist) {
		// The preview was installed or expired while we were reading, and its staging directory is already gone.
		return Readme{}, apperr.NotFound("Preview")
	}
	return r, err
}

// ReadReadme picks a README for locale among the direct child files of the plugin root directory dir and reads it.
// Symlinks that resolve outside dir or cannot be resolved, and anything that is not a regular file, count as
// missing.
func ReadReadme(dir, locale string) (Readme, error) {
	root, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return Readme{}, err
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return Readme{}, err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	for _, name := range readmeOrder(names, locale) {
		f, err := openInside(root, name)
		if err != nil {
			return Readme{}, err
		}
		if f == nil {
			continue
		}
		b, err := io.ReadAll(io.LimitReader(f, MaxReadmeBytes+1))
		f.Close()
		if err != nil {
			return Readme{}, err
		}
		truncated := len(b) > MaxReadmeBytes
		if truncated {
			b = cutRunes(b[:MaxReadmeBytes])
		}
		markdown := string(b)
		return Readme{File: &name, Markdown: &markdown, Truncated: truncated}, nil
	}
	return Readme{}, nil
}

// readmeOrder gives the order in which READMEs are tried: with a locale, README.<locale>.md,
// README.<primary language>.md, README.md; without one, README.md; after that the remaining README.<language>.md,
// sorted by language tag. Base names are case-insensitive; when one language has files in several spellings of case,
// they are sorted by file name.
func readmeOrder(names []string, locale string) []string {
	byTag := map[string][]string{} // lower-case language tag -> file names; "" is README.md
	for _, name := range names {
		lower := strings.ToLower(name)
		if len(lower) < len("readme.md") || !strings.HasPrefix(lower, "readme") || !strings.HasSuffix(lower, ".md") {
			continue
		}
		tag := lower[len("readme") : len(lower)-len(".md")]
		if tag != "" {
			if tag[0] != '.' || !pluginmanifest.ValidLanguageTag(tag[1:]) {
				continue
			}
			tag = tag[1:]
		}
		byTag[tag] = append(byTag[tag], name)
	}
	var first []string
	if locale != "" {
		l := strings.ToLower(locale)
		first = append(first, l)
		if primary, _, ok := strings.Cut(l, "-"); ok {
			first = append(first, primary)
		}
	}
	first = append(first, "")
	var out []string
	for _, tag := range append(first, slices.Sorted(maps.Keys(byTag))...) {
		out = append(out, slices.Sorted(slices.Values(byTag[tag]))...)
		delete(byTag, tag)
	}
	return out
}

// openInside opens the direct child file name of root; it returns nil when the resolved path is not inside root or
// is not a regular file.
func openInside(root, name string) (*os.File, error) {
	real, err := filepath.EvalSymlinks(filepath.Join(root, name))
	if err != nil {
		return nil, nil
	}
	if rel, err := filepath.Rel(root, real); err != nil || !filepath.IsLocal(rel) {
		return nil, nil
	}
	f, err := os.Open(real)
	if err != nil {
		return nil, err
	}
	if info, err := f.Stat(); err != nil || !info.Mode().IsRegular() {
		f.Close()
		return nil, err
	}
	return f, nil
}

// cutRunes removes a partial UTF-8 character left at the end by truncation.
func cutRunes(b []byte) []byte {
	for i := len(b) - 1; i >= 0 && i >= len(b)-utf8.UTFMax; i-- {
		if utf8.RuneStart(b[i]) {
			if !utf8.FullRune(b[i:]) {
				return b[:i]
			}
			break
		}
	}
	return b
}
