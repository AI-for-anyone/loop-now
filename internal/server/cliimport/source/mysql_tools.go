package source

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// MySQLTools is trusted SERVER dependency configuration, never request input.
// The owner supplies binary digests from its verified complete official bundle.
type MySQLTools struct{ Root, EditorSHA256, PrinterSHA256 string }

func (MySQLTools) String() string   { return "[trusted MySQL source tools]" }
func (MySQLTools) GoString() string { return "[trusted MySQL source tools]" }
func (r *Reader) SetMySQLTools(ctx context.Context, t MySQLTools) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !filepath.IsAbs(t.Root) {
		return fail("tool_unavailable", "tool")
	}
	if r.tools != nil || len(r.entries) != 0 {
		return fail("tool_configuration_locked", "tool")
	}
	for _, item := range []struct{ name, sum string }{{"mysql_config_editor", t.EditorSHA256}, {"my_print_defaults", t.PrinterSHA256}} {
		if e := toolDigest(t.Root, item.name, item.sum); e != nil {
			return e
		}
		args := []string{"--version"}
		if item.name == "my_print_defaults" {
			args = []string{"--no-defaults", "--no-login-paths", "--version"}
		}
		out, e := runTool(ctx, t.Root, item.name, args, nil)
		if e != nil {
			return e
		}
		parts := strings.Fields(string(out))
		valid := false
		for i := 0; i+1 < len(parts); i++ {
			if parts[i] == "Ver" && parts[i+1] == "8.4.9" {
				valid = true
			}
		}
		clear(out)
		if !valid {
			return fail("tool_unavailable", "tool_version")
		}
	}
	r.tools = &t
	return nil
}
func toolDigest(root, name, want string) error {
	if len(want) != 64 {
		return fail("tool_unavailable", "tool_fingerprint")
	}
	if _, e := hex.DecodeString(want); e != nil {
		return fail("tool_unavailable", "tool_fingerprint")
	}
	f, e := os.OpenFile(filepath.Join(root, "bin", name), os.O_RDONLY|unix.O_NOFOLLOW, 0)
	if e != nil {
		return fail("tool_unavailable", "tool")
	}
	defer f.Close()
	info, e := f.Stat()
	if e != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0022 != 0 {
		return fail("tool_unavailable", "tool")
	}
	h := sha256.New()
	if _, e = io.Copy(h, f); e != nil || hex.EncodeToString(h.Sum(nil)) != want {
		return fail("tool_unavailable", "tool_fingerprint")
	}
	return nil
}

type privateBuffer struct{ bytes.Buffer }

func (*privateBuffer) String() string   { return "[private native capture]" }
func (*privateBuffer) GoString() string { return "[private native capture]" }

func (b *privateBuffer) Write(p []byte) (int, error) {
	if b.Len()+len(p) > maxFile {
		return 0, fail("tool_output_limit", "tool")
	}
	return b.Buffer.Write(p)
}
func runTool(ctx context.Context, root, name string, args []string, source *os.File) ([]byte, error) {
	if ctx.Err() != nil {
		return nil, fail("cancelled", "tool")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	home, e := os.MkdirTemp("", "loop-local-import-tool-")
	if e != nil {
		return nil, fail("tool_unavailable", "tool")
	}
	defer os.RemoveAll(home)
	cmd := exec.CommandContext(ctx, filepath.Join(root, "bin", name), args...)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "PATH=/usr/bin:/bin", "LC_ALL=C", "MYSQL_TEST_LOGIN_FILE=/dev/null"}
	if source != nil {
		// mysql_config_editor opens even its print source read/write. Never grant
		// write access to the host file: materialize only an anonymous private FD.
		// Unlink before writing any credential-bearing bytes; no named secret file.
		snapshot, err := os.CreateTemp(home, "private-fd-")
		if err != nil {
			return nil, fail("tool_unavailable", "private_ipc")
		}
		defer snapshot.Close()
		if err = os.Remove(snapshot.Name()); err != nil {
			return nil, fail("tool_unavailable", "private_ipc")
		}
		if ctx.Err() != nil {
			return nil, fail("cancelled", "tool")
		}
		b, err := io.ReadAll(io.LimitReader(source, maxFile+1))
		defer clear(b)
		if err != nil || len(b) > maxFile {
			return nil, fail("source_unreadable", "source")
		}
		if ctx.Err() != nil {
			return nil, fail("cancelled", "tool")
		}
		if _, err = snapshot.Write(b); err != nil {
			return nil, fail("tool_unavailable", "private_ipc")
		}
		if _, err = snapshot.Seek(0, 0); err != nil {
			return nil, fail("tool_unavailable", "private_ipc")
		}
		cmd.ExtraFiles = []*os.File{snapshot}
		cmd.Env = append(cmd.Env, "MYSQL_TEST_LOGIN_FILE=/dev/fd/3")
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return unix.Kill(-cmd.Process.Pid, unix.SIGKILL)
	}
	cmd.WaitDelay = 500 * time.Millisecond
	var stdout, stderr privateBuffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	defer clear(stderr.Bytes())
	if err != nil {
		clear(stdout.Bytes())
		if ctx.Err() != nil {
			return nil, fail("cancelled", "tool")
		}
		return nil, fail("tool_unavailable", "tool")
	}
	return stdout.Bytes(), nil
}
func (r *Reader) mysqlTools() *MySQLTools { r.mu.Lock(); defer r.mu.Unlock(); return r.tools }

func (r *Reader) loginTool(ctx context.Context, name string, args []string, e *entry) ([]byte, error) {
	tools := r.mysqlTools()
	if tools == nil {
		return nil, fail("tool_unavailable", "tool")
	}
	sum := tools.EditorSHA256
	if name == "my_print_defaults" {
		sum = tools.PrinterSHA256
	}
	if err := toolDigest(tools.Root, name, sum); err != nil {
		return nil, err
	}
	f, err := r.open(".mylogin.cnf")
	if err != nil {
		return nil, fail("source_unreadable", "source")
	}
	defer f.Close()
	before, err := f.Stat()
	if err != nil || before.Mode().Perm()&0077 != 0 {
		return nil, fail("source_unreadable", "permissions")
	}
	if e != nil && e.pins[".mylogin.cnf"] != (stamp{true, identity(before)}) {
		return nil, fail("source_changed", "source")
	}
	b, err := runTool(ctx, tools.Root, name, args, f)
	if err != nil {
		return nil, err
	}
	after, err := f.Stat()
	if err != nil || identity(before) != identity(after) {
		clear(b)
		return nil, fail("source_changed", "source")
	}
	if err = toolDigest(tools.Root, name, sum); err != nil {
		clear(b)
		return nil, err
	}
	return b, nil
}
func printerFields(raw []byte) (*group, error) {
	g := &group{map[string]string{}, map[string]bool{}, false}
	for _, line := range bytes.Split(raw, []byte{'\n'}) {
		if len(line) == 0 {
			continue
		}
		if !bytes.HasPrefix(line, []byte("--")) {
			return nil, fail("unsupported", "tool_output")
		}
		i := bytes.IndexByte(line, '=')
		if i < 3 {
			return nil, fail("unsupported", "tool_output")
		}
		key := string(line[2:i])
		mapped, ok := myKeys[strings.ReplaceAll(key, "_", "-")]
		if !ok {
			return nil, fail("unsupported", "tool_output")
		}
		if _, exists := g.values[mapped]; exists {
			return nil, fail("unsupported", "duplicate_field")
		}
		value := string(line[i+1:])
		if mapped == "sslMode" || mapped == "protocol" {
			value = strings.ToUpper(value)
		}
		g.values[mapped] = value
	}
	return g, nil
}
func (r *Reader) discoverLogin(ctx context.Context, pin stamp) []Candidate {
	pins := map[string]stamp{".mylogin.cnf": pin}
	tools := r.mysqlTools()
	if tools == nil {
		return []Candidate{r.add("mysql", "mysql_login_path", "MySQL login-path", ".mylogin.cnf", "", pins, nil, "tool_unavailable")}
	}
	e := &entry{pins: pins}
	raw, err := r.loginTool(ctx, "mysql_config_editor", []string{"print", "--all"}, e)
	if err != nil {
		return []Candidate{r.add("mysql", "mysql_login_path", "MySQL login-path", ".mylogin.cnf", "", pins, nil, "tool_unavailable")}
	}
	defer clear(raw)
	if !maskedLoginOutput(raw) {
		return []Candidate{r.add("mysql", "mysql_login_path", "MySQL login-path", ".mylogin.cnf", "", pins, nil, "unsupported_tool_output")}
	}
	groups, err := parseINI(raw, true, false)
	if err != nil {
		return []Candidate{r.add("mysql", "mysql_login_path", "MySQL login-path", ".mylogin.cnf", "", pins, nil, "unsupported")}
	}
	if len(groups) == 0 {
		return []Candidate{r.add("mysql", "mysql_login_path", "MySQL login-path source", ".mylogin.cnf", "", pins, nil, "no_discoverable_account")}
	}
	names := sortedGroups(groups)
	out := []Candidate{}
	for _, name := range names {
		g := groups[name]
		missing, reason := missingDB("mysql", g)
		if !g.secrets["password"] {
			reason = "credential_unavailable"
		}
		out = append(out, r.add("mysql", "mysql_login_path", "MySQL login-path "+name, ".mylogin.cnf", name, pins, missing, reason, supplementableDB(g)))
	}
	return out
}
func (r *Reader) extractLogin(ctx context.Context, e *entry, s Supplement) (map[string]string, error) {
	// Explicit selected group only. No client/mysql defaults are added to it.
	raw, err := r.loginTool(ctx, "my_print_defaults", []string{"--defaults-file=/dev/null", "--login-path=" + e.section, "--show", e.section}, e)
	if err != nil {
		return nil, err
	}
	defer clear(raw)
	g, err := printerFields(raw)
	if err != nil {
		return nil, err
	}
	return r.finishDB(ctx, e, s, g)
}

func maskedLoginOutput(raw []byte) bool {
	for _, line := range bytes.Split(raw, []byte{'\n'}) {
		line = bytes.TrimSpace(line)
		i := bytes.IndexByte(line, '=')
		if i < 0 {
			continue
		}
		if bytes.Equal(bytes.TrimSpace(line[:i]), []byte("password")) && !bytes.Equal(bytes.TrimSpace(line[i+1:]), []byte("*****")) {
			return false
		}
	}
	return true
}
