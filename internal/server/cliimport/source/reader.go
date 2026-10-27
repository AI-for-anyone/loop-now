// Package source discovers fixed local formats. It has no HTTP, database,
// network, environment lookup or host-HOME fallback.
package source

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"strings"
	"sync"

	"github.com/google/uuid"
	"golang.org/x/sys/unix"
	"loop-now/internal/server/clitemplate"
)

const maxFile = 1 << 20

// Failure carries only fixed safe codes and field names, never native errors.
type Failure struct{ Code, Field string }

func (f Failure) Error() string     { return "local import: " + f.Code + ": " + f.Field }
func fail(code, field string) error { return Failure{code, field} }

// Candidate is a safe discovery projection, NOT a private extraction handle.
type Candidate struct {
	ID         string
	CLI        string
	SourceKind string
	Label      string
	Missing    []string
	// SupplementableFields lists source-absent nonsecret fields, independently
	// of the currently required Missing fields. Final combinations still validate.
	SupplementableFields []string
	Reason               string
}

// Supplement is internal; the HTTP owner defines its wire contract.
type Supplement struct{ Database, SSLMode, CACert string }
type Selection struct {
	reader *Reader
	entry  *entry
}

func (Selection) String() string               { return "[private local selection]" }
func (Selection) GoString() string             { return "[private local selection]" }
func (Selection) MarshalJSON() ([]byte, error) { return nil, errors.New("private local selection") }

type stamp struct {
	exists   bool
	identity string
}
type entry struct {
	candidate     Candidate
	file, section string
	pins          map[string]stamp
	template      [32]byte
}
type Reader struct {
	tools            *MySQLTools
	base             string
	root             *os.File
	catalog, builtin *clitemplate.Catalog
	mu               sync.Mutex
	entries          map[string]*entry
}

// New accepts only a trusted server root. It never discovers the caller's HOME.
func New(home string, catalog *clitemplate.Catalog) (*Reader, error) {
	if home == "" || !filepath.IsAbs(home) {
		return nil, fail("source_unreadable", "root")
	}
	fd, e := unix.Open(home, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if e != nil {
		return nil, fail("source_unreadable", "root")
	}
	builtin, e := clitemplate.Load("")
	if e != nil {
		unix.Close(fd)
		return nil, fail("template_unavailable", "template")
	}
	if catalog == nil {
		catalog = builtin
	}
	base, err := filepath.Abs(home)
	if err != nil {
		unix.Close(fd)
		return nil, fail("source_unreadable", "root")
	}
	return &Reader{base: base, root: os.NewFile(uintptr(fd), "local-source-root"), catalog: catalog, builtin: builtin, entries: map[string]*entry{}}, nil
}
func (r *Reader) Close() error { return r.root.Close() }

// open traverses every path component with NOFOLLOW, including intermediate
// directories. Native error paths are intentionally discarded.
func (r *Reader) open(name string) (*os.File, error)          { return r.openPath(name, false) }
func (r *Reader) openDirectory(name string) (*os.File, error) { return r.openPath(name, true) }
func (r *Reader) openPath(name string, directory bool) (*os.File, error) {
	if name == "" || path.IsAbs(name) || path.Clean(name) != name || name == ".." || strings.HasPrefix(name, "../") || strings.Contains(name, "\\") {
		return nil, fail("unsupported", "source_reference")
	}
	fd, e := unix.Dup(int(r.root.Fd()))
	if e != nil {
		return nil, fail("source_unreadable", "source")
	}
	parts := strings.Split(name, "/")
	for i, p := range parts {
		flags := unix.O_RDONLY | unix.O_CLOEXEC | unix.O_NOFOLLOW | unix.O_NONBLOCK
		if i < len(parts)-1 || directory {
			flags |= unix.O_DIRECTORY
		}
		next, err := unix.Openat(fd, p, flags, 0)
		unix.Close(fd)
		if err != nil {
			if errors.Is(err, unix.ENOENT) {
				return nil, os.ErrNotExist
			}
			return nil, fail("source_unreadable", "source")
		}
		fd = next
	}
	f := os.NewFile(uintptr(fd), "local-source")
	info, e := f.Stat()
	if e != nil || (!directory && (!info.Mode().IsRegular() || info.Size() > maxFile)) || (directory && !info.IsDir()) {
		f.Close()
		return nil, fail("source_unreadable", "source")
	}
	return f, nil
}
func identity(info os.FileInfo) string {
	// Do not bind atime: reading must not invalidate our own candidate. Include
	// inode/device and ctime in addition to mode/size/mtime (Darwin and Linux).
	v := reflect.Indirect(reflect.ValueOf(info.Sys()))
	extra := ""
	for _, name := range []string{"Dev", "Ino", "Ctim", "Ctimespec"} {
		f := v.FieldByName(name)
		if f.IsValid() {
			extra += fmt.Sprint(f.Interface()) + "/"
		}
	}
	return fmt.Sprint(info.Mode(), "/", info.Size(), "/", info.ModTime().UnixNano(), "/", extra)
}
func (r *Reader) stat(name string) (stamp, error) {
	f, e := r.open(name)
	if errors.Is(e, os.ErrNotExist) {
		return stamp{}, nil
	}
	if e != nil {
		return stamp{}, e
	}
	defer f.Close()
	info, e := f.Stat()
	if e != nil {
		return stamp{}, fail("source_unreadable", "source")
	}
	return stamp{true, identity(info)}, nil
}
func (r *Reader) read(ctx context.Context, name string, pin *stamp, private bool) ([]byte, error) {
	if ctx.Err() != nil {
		return nil, fail("cancelled", "operation")
	}
	f, e := r.open(name)
	if e != nil {
		return nil, fail("source_unreadable", "source")
	}
	defer f.Close()
	before, e := f.Stat()
	if e != nil {
		return nil, fail("source_unreadable", "source")
	}
	if pin != nil && *pin != (stamp{true, identity(before)}) {
		return nil, fail("source_changed", "source")
	}
	if (private && before.Mode().Perm()&0077 != 0) || (name == ".my.cnf" && before.Mode().Perm()&0002 != 0) {
		return nil, fail("source_unreadable", "permissions")
	}
	b, e := io.ReadAll(io.LimitReader(f, maxFile+1))
	after, se := f.Stat()
	if e != nil || se != nil || len(b) > maxFile {
		clear(b)
		return nil, fail("source_unreadable", "source")
	}
	if identity(before) != identity(after) {
		clear(b)
		return nil, fail("source_changed", "source")
	}
	if ctx.Err() != nil {
		clear(b)
		return nil, fail("cancelled", "operation")
	}
	return b, nil
}
func (r *Reader) template(cli string) (*clitemplate.Template, [32]byte, error) {
	a, ok := r.catalog.Get(cli)
	b, bok := r.builtin.Get(cli)
	if !ok || !bok {
		return nil, [32]byte{}, fail("template_unavailable", "template")
	}
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	h := sha256.Sum256(x)
	if h != sha256.Sum256(y) {
		return nil, h, fail("template_mismatch", "template")
	}
	return a, h, nil
}
func (r *Reader) add(cli, kind, label, file, section string, pins map[string]stamp, missing []string, reason string, supplemental ...[]string) Candidate {
	_, hash, e := r.template(cli)
	if e != nil {
		reason = "template_mismatch"
	}
	allowed := []string{}
	if len(supplemental) == 1 && reason == "" {
		allowed = append(allowed, supplemental[0]...)
	}
	c := Candidate{ID: uuid.NewString(), CLI: cli, SourceKind: kind, Label: label, Missing: append([]string{}, missing...), SupplementableFields: allowed, Reason: reason}
	private := c
	private.Missing = append([]string{}, c.Missing...)
	private.SupplementableFields = append([]string{}, c.SupplementableFields...)
	r.mu.Lock()
	r.entries[c.ID] = &entry{private, file, section, pins, hash}
	r.mu.Unlock()
	return c
}
func (r *Reader) Select(id string) (Selection, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	e := r.entries[id]
	if e == nil {
		return Selection{}, fail("candidate_not_found", "candidate")
	}
	if e.candidate.Reason != "" {
		return Selection{}, fail(e.candidate.Reason, "candidate")
	}
	return Selection{r, e}, nil
}
func (r *Reader) Recheck(ctx context.Context, s Selection) error {
	if s.reader != r || s.entry == nil {
		return fail("candidate_not_found", "candidate")
	}
	if ctx.Err() != nil {
		return fail("cancelled", "operation")
	}
	if s.entry.candidate.SourceKind == "mysql_login_path" {
		tools := r.mysqlTools()
		if tools == nil {
			return fail("tool_unavailable", "tool")
		}
		if e := toolDigest(tools.Root, "mysql_config_editor", tools.EditorSHA256); e != nil {
			return e
		}
		if e := toolDigest(tools.Root, "my_print_defaults", tools.PrinterSHA256); e != nil {
			return e
		}
	}
	_, h, e := r.template(s.entry.candidate.CLI)
	if e != nil {
		return e
	}
	if h != s.entry.template {
		return fail("template_mismatch", "template")
	}
	for name, pin := range s.entry.pins {
		now, e := r.stat(name)
		if e != nil {
			return e
		}
		if now != pin {
			return fail("source_changed", "source")
		}
	}
	return nil
}
func (r *Reader) Extract(ctx context.Context, s Selection, sup Supplement, consume func(map[string]string) error) error {
	if e := r.Recheck(ctx, s); e != nil {
		return e
	}
	if consume == nil {
		return fail("invalid_consumer", "operation")
	}
	fields, e := r.extract(ctx, s.entry, sup)
	if fields != nil {
		defer clear(fields)
	}
	if e != nil {
		return e
	}
	t, _, e := r.template(s.entry.candidate.CLI)
	if e != nil {
		return e
	}
	payload, _, e := t.Render(fields)
	for _, f := range payload.Files {
		clear(f.Content)
	}
	clear(payload.Env)
	if e != nil {
		return fail("invalid_credential", "credential")
	}
	if e = r.Recheck(ctx, s); e != nil {
		return e
	}
	if consume(fields) != nil {
		return fail("consumer_failed", "operation")
	}
	return nil
}

// WithOrigin is a private server-only provenance/HMAC callback. No source
// version/mtime/secret is part of origin; those belong to separate checks.
func (s Selection) WithOrigin(consume func([]byte) error) error {
	if s.reader == nil || s.entry == nil || consume == nil {
		return fail("candidate_not_found", "candidate")
	}
	s.reader.mu.Lock()
	known := s.reader.entries[s.entry.candidate.ID] == s.entry
	s.reader.mu.Unlock()
	if !known {
		return fail("candidate_not_found", "candidate")
	}
	info, err := s.reader.root.Stat()
	if err != nil {
		return fail("source_unreadable", "root")
	}
	v := reflect.Indirect(reflect.ValueOf(info.Sys()))
	rootID := fmt.Sprint(v.FieldByName("Dev").Interface(), "/", v.FieldByName("Ino").Interface())
	b, err := json.Marshal([]string{"local-cli-source-v1", rootID, s.entry.candidate.CLI, s.entry.candidate.SourceKind, s.entry.file, s.entry.section, "adapter-v1"})
	if err != nil {
		return fail("source_unreadable", "origin")
	}
	defer clear(b)
	if consume(b) != nil {
		return fail("consumer_failed", "origin")
	}
	return nil
}
