package source

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

// Fixed official 8.4.9 package already verified by the tools owner. This is
// evidence only: production masked discovery awaits Main's boundary review.
func TestNativeMySQL849MaskedDiscoveryAndPrivateSelectedExport(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("fixed reviewed vendor fixture is darwin-arm64")
	}
	tree, e := filepath.Abs("../../../.." + "/.local-build/cli-installation-proof/unpacked/mysql-8.4.9-macos15-arm64")
	if e != nil {
		t.Fatal(e)
	}
	editor := filepath.Join(tree, "bin/mysql_config_editor")
	printer := filepath.Join(tree, "bin/my_print_defaults")
	if _, e = os.Stat(editor); e != nil {
		t.Skip("fixed vendor tree unavailable")
	}
	home := t.TempDir()
	login := filepath.Join(home, ".mylogin.cnf")
	empty := filepath.Join(home, "empty.cnf")
	if e = os.WriteFile(empty, nil, 0600); e != nil {
		t.Fatal(e)
	}
	profile := filepath.Join(home, "deny.sb")
	policy := `(version 1)(allow default)(deny network*)(deny file-read* (subpath "/opt/homebrew"))(deny file-read* (subpath "/Users"))(allow file-read* (subpath "` + tree + `"))`
	if e = os.WriteFile(profile, []byte(policy), 0600); e != nil {
		t.Fatal(e)
	}
	run := func(binary string, args []string, input string) []byte {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, "/usr/bin/sandbox-exec", append([]string{"-f", profile, binary}, args...)...)
		if os.Getenv("LOOP_SOURCE_TEST_NETWORK_DENIED") == "1" {
			cmd = exec.CommandContext(ctx, binary, args...)
		}
		cmd.Dir = home
		cmd.Env = []string{"HOME=" + home, "MYSQL_TEST_LOGIN_FILE=" + login, "PATH=/usr/bin:/bin", "LC_ALL=C"}
		cmd.Stdin = strings.NewReader(input)
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		e := cmd.Run()
		defer clear(stderr.Bytes())
		if e != nil {
			clear(stdout.Bytes())
			t.Fatal("fixed native fixture execution failed (diagnostics withheld)")
		}
		return stdout.Bytes()
	}
	v := run(printer, []string{"--no-defaults", "--no-login-paths", "--version"}, "")
	if !bytes.Contains(v, []byte("8.4.9")) {
		t.Fatal("native actual version mismatch")
	}
	clear(v)
	passwords := []string{"SYNTHETIC_ALPHA_PASSWORD_4937", "SYNTHETIC_BETA_PASSWORD_5928"}
	for i, name := range []string{"alpha", "beta"} {
		out := run(editor, []string{"set", "--login-path=" + name, "--host=" + name + ".invalid", "--user=" + name, "--port=3306", "--password"}, passwords[i]+"\n")
		clear(out)
	}
	before, e := os.ReadFile(login)
	if e != nil {
		t.Fatal("synthetic login fixture absent")
	}
	defer clear(before)
	masked := run(editor, []string{"print", "--all"}, "")
	defer clear(masked)
	if !bytes.Contains(masked, []byte("[alpha]")) || !bytes.Contains(masked, []byte("[beta]")) {
		t.Fatal("masked discovery did not enumerate distinct paths")
	}
	for _, p := range passwords {
		if bytes.Contains(masked, []byte(p)) {
			t.Fatal("masked discovery exposed synthetic secret")
		}
	}
	selected := run(printer, []string{"--defaults-file=" + empty, "--login-path=alpha", "--show", "alpha"}, "")
	defer clear(selected)
	if !bytes.Contains(selected, []byte(passwords[0])) || bytes.Contains(selected, []byte(passwords[1])) {
		t.Fatal("private selected export did not isolate selected path")
	}
	after, e := os.ReadFile(login)
	if e != nil || !bytes.Equal(before, after) {
		t.Fatal("native read mutated synthetic login file")
	}
	clear(after)
	// A distinct default client path must not bleed into the selected alpha.
	defaultOut := run(editor, []string{"set", "--login-path=client", "--host=default.invalid", "--user=default", "--port=3306", "--password"}, "SYNTHETIC_DEFAULT_TRAP\n")
	clear(defaultOut)
	// Exercise the actual production Reader path as well: no alternate success
	// function. Its subprocess gets the captured source FD, not a browser path.
	tools := MySQLTools{Root: tree}
	for _, item := range []struct {
		name string
		dest *string
	}{{editor, &tools.EditorSHA256}, {printer, &tools.PrinterSHA256}} {
		b, err := os.ReadFile(item.name)
		if err != nil {
			t.Fatal("trusted tool fixture absent")
		}
		sum := sha256.Sum256(b)
		clear(b)
		*item.dest = hex.EncodeToString(sum[:])
	}
	reader, err := New(home, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	wrong := tools
	wrong.EditorSHA256 = strings.Repeat("0", 64)
	expectCode(t, reader.SetMySQLTools(context.Background(), wrong), "tool_unavailable")
	if err = reader.SetMySQLTools(context.Background(), tools); err != nil {
		t.Fatal("trusted native tools not ready")
	}
	candidates, err := reader.Discover(context.Background())
	if err != nil || len(candidates) != 3 {
		t.Fatalf("production masked discovery (three distinct paths) count=%d safe projections=%+v error=%v", len(candidates), candidates, err)
	}
	var alpha Candidate
	for _, c := range candidates {
		if c.Label == "MySQL login-path alpha" {
			alpha = c
		}
		if c.Reason != "" {
			t.Fatal("production candidate unavailable")
		}
	}
	if !slices.Equal(alpha.Missing, []string{"database", "sslMode"}) || !slices.Equal(alpha.SupplementableFields, []string{"database", "sslMode", "caCert"}) {
		t.Fatal("native login-path missing TLS did not expose independent CA supplement")
	}
	chosen, err := reader.Select(alpha.ID)
	if err != nil {
		t.Fatal("selected alpha missing")
	}
	if err = reader.Extract(context.Background(), chosen, Supplement{Database: "chosen-db", SSLMode: "DISABLED"}, func(fields map[string]string) error {
		if fields["password"] != passwords[0] || fields["user"] != "alpha" || fields["database"] != "chosen-db" {
			t.Fatal("production private export lost selected fields")
		}
		return nil
	}); err != nil {
		t.Fatal("production private FD export failed (diagnostics withheld)")
	}
	cert, _ := syntheticTLS(t)
	if err = reader.Extract(context.Background(), chosen, Supplement{Database: "chosen-db", SSLMode: "VERIFY_IDENTITY", CACert: cert}, func(fields map[string]string) error {
		if fields["sslMode"] != "VERIFY_IDENTITY" || fields["caCert"] != cert || fields["password"] != passwords[0] {
			t.Fatal("native login-path supplemental verify identity mapping lost source/CA")
		}
		return nil
	}); err != nil {
		t.Fatal("native login-path absent TLS/CA supplement rejected", err)
	}
	beforeCancel, cancel := context.WithCancel(context.Background())
	cancel()
	if err = reader.Extract(beforeCancel, chosen, Supplement{}, func(map[string]string) error { t.Fatal("cancelled extraction invoked consumer"); return nil }); err == nil {
		t.Fatal("cancel ignored")
	}
	t.Log("official 8.4.9; synthetic HOME only; network denied; print --all returns two masked groups; private --show returns selected alpha only; original encrypted bytes unchanged; no plaintext printed")
}
