package dbcli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type Options struct {
	Getenv         func(string) string
	Stdin          io.Reader
	Stdout, Stderr io.Writer
}

func privateWrite(file string, b []byte) error {
	if e := os.MkdirAll(filepath.Dir(file), 0700); e != nil {
		return e
	}
	f, e := os.CreateTemp(filepath.Dir(file), ".config-")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	if _, e = f.Write(b); e == nil {
		e = f.Chmod(0600)
	}
	ce := f.Close()
	if e != nil {
		return e
	}
	if ce != nil {
		return ce
	}
	return os.Rename(f.Name(), file)
}
func pgEscape(s string) string { return strings.NewReplacer("\\", "\\\\", ":", "\\:").Replace(s) }
func mysqlQuote(s string) string {
	return "\"" + strings.NewReplacer("\\", "\\\\", "\"", "\\\"", "\n", "\\n", "\r", "\\r", "\t", "\\t", "\b", "\\b").Replace(s) + "\""
}
func nativeConfig(cli, home string, c Connection) ([]string, []string, error) {
	dir := filepath.Join(home, ".loop-now", "db", cli, c.AccountID.String())
	if e := os.MkdirAll(dir, 0700); e != nil {
		return nil, nil, e
	}
	files := map[string]string{"ca.pem": c.Fields.CACert, "cert.pem": c.Fields.ClientCert, "key.pem": c.Fields.ClientKey}
	for n, v := range files {
		if v != "" {
			if e := privateWrite(filepath.Join(dir, n), []byte(v)); e != nil {
				return nil, nil, e
			}
		}
	}
	f := c.Fields
	env := []string{"HOME=" + home, "PATH=/usr/bin:/bin", "LANG=C.UTF-8"}
	if cli == "mysql" {
		lines := []string{"[client]", "protocol=TCP", "host=" + mysqlQuote(f.Host), "port=" + f.Port, "user=" + mysqlQuote(f.User), "password=" + mysqlQuote(f.Password), "database=" + mysqlQuote(f.Database), "ssl-mode=" + f.SSLMode}
		for key, name := range map[string]string{"ssl-ca": "ca.pem", "ssl-cert": "cert.pem", "ssl-key": "key.pem"} {
			if files[name] != "" {
				lines = append(lines, key+"="+mysqlQuote(filepath.Join(dir, name)))
			}
		}
		file := filepath.Join(dir, "client.cnf")
		if e := privateWrite(file, []byte(strings.Join(lines, "\n")+"\n")); e != nil {
			return nil, nil, e
		}
		return []string{"--defaults-file=" + file, "--no-login-paths"}, env, nil
	}
	// libpq service values are not generic INI: no quoting or backslash decoding.
	for _, v := range []string{f.Host, f.User, f.Database, dir} {
		if strings.TrimRight(v, " \t") != v || strings.ContainsAny(v, "\r\n") {
			return nil, nil, errors.New("配置值不能安全表达")
		}
	}
	if e := privateWrite(filepath.Join(dir, "pgpass"), []byte(strings.Join([]string{pgEscape(f.Host), f.Port, pgEscape(f.Database), pgEscape(f.User), pgEscape(f.Password)}, ":")+"\n")); e != nil {
		return nil, nil, e
	}
	lines := []string{"[loop]", "host=" + f.Host, "port=" + f.Port, "dbname=" + f.Database, "user=" + f.User, "sslmode=" + f.SSLMode}
	for key, name := range map[string]string{"sslrootcert": "ca.pem", "sslcert": "cert.pem", "sslkey": "key.pem"} {
		if files[name] != "" {
			lines = append(lines, key+"="+filepath.Join(dir, name))
		}
	}
	file := filepath.Join(dir, "pg_service.conf")
	if e := privateWrite(file, []byte(strings.Join(lines, "\n")+"\n")); e != nil {
		return nil, nil, e
	}
	env = append(env, "PGSERVICE=loop", "PGSERVICEFILE="+file, "PGPASSFILE="+filepath.Join(dir, "pgpass"), "PGSYSCONFDIR="+dir, "PGCONNECT_TIMEOUT=10", "PGGSSENCMODE=disable")
	// Do not discover default client certificates or root files in private HOME.
	env = append(env, "PGSSLROOTCERT="+filepath.Join(dir, "no-default-root.pem"), "PGSSLCERT="+filepath.Join(dir, "no-default-client.pem"), "PGSSLKEY="+filepath.Join(dir, "no-default-key.pem"))
	return []string{"-X"}, env, nil
}
func parseArgs(cli string, args []string) (uuid.UUID, bool, []string, error) {
	fail := errors.New("数据库CLI参数无效或试图覆盖连接配置")
	var id uuid.UUID
	list := false
	native := []string{}
	withValue := map[string]bool{}
	flags := map[string]bool{"--help": true, "--version": true}
	if cli == "mysql" {
		for _, k := range []string{"--execute", "-e", "--default-character-set", "--connect-timeout"} {
			withValue[k] = true
		}
		for _, k := range []string{"--skip-auto-rehash", "--batch", "-B", "--raw", "-r", "--skip-column-names", "-N", "--xml", "-X", "--silent", "-s", "--verbose", "-v", "--unbuffered", "--binary-mode"} {
			flags[k] = true
		}
	} else {
		for _, k := range []string{"--command", "-c", "--file", "-f", "--set", "-v", "--pset", "-P"} {
			withValue[k] = true
		}
		for _, k := range []string{"--csv", "--no-align", "-A", "--tuples-only", "-t", "--quiet", "-q", "--echo-all", "-a", "--echo-queries", "-e", "--no-psqlrc", "-X", "--no-password", "-w", "--single-transaction", "-1"} {
			flags[k] = true
		}
	}
	for j := 0; j < len(args); j++ {
		arg := args[j]
		if arg == "--" {
			continue
		}
		if arg == "--loop-list-connections" {
			if list {
				return id, false, nil, fail
			}
			list = true
			continue
		}
		key, val, has := strings.Cut(arg, "=")
		if key == "--loop-connection-id" {
			if id != uuid.Nil {
				return id, false, nil, fail
			}
			if !has {
				j++
				if j >= len(args) {
					return id, false, nil, fail
				}
				val = args[j]
			}
			var e error
			id, e = uuid.Parse(val)
			if e != nil || id == uuid.Nil {
				return id, false, nil, fail
			}
			continue
		}
		if !has && len(arg) > 2 && strings.HasPrefix(arg, "-") && !strings.HasPrefix(arg, "--") && withValue[arg[:2]] {
			key, val, has = arg[:2], arg[2:], true
		}
		if withValue[key] {
			if !has {
				j++
				if j >= len(args) {
					return id, false, nil, fail
				}
				val = args[j]
			}
			if key == "--connect-timeout" && val != "10" {
				return id, false, nil, fail
			}
			if key == "--default-character-set" && val != "utf8mb4" {
				return id, false, nil, fail
			}
			native = append(native, key, val)
			continue
		}
		if flags[arg] {
			native = append(native, arg)
			continue
		}
		return id, false, nil, fail
	}
	if list && (id != uuid.Nil || len(native) > 0) {
		return id, false, nil, fail
	}
	return id, list, native, nil
}

// Run is a local execution-only reexec entry. No platform config/DB is loaded.
func Run(ctx context.Context, args []string, o Options) int {
	report := func(msg string) int { io.WriteString(o.Stderr, "loop-db-cli: "+msg+"\n"); return 2 }
	if len(args) < 2 || args[0] != "mysql" && args[0] != "psql" {
		return report("invalid invocation")
	}
	cli, nativePath := args[0], args[1]
	if !filepath.IsAbs(nativePath) {
		return report("invalid tool path")
	}
	id, list, native, e := parseArgs(cli, args[2:])
	if e != nil {
		return report("invalid arguments or connection override")
	}
	home := o.Getenv("HOME")
	if !filepath.IsAbs(home) {
		return report("scope HOME unavailable")
	}
	// Only version/help may run without a captured connection manifest.
	probe := len(native) == 1 && (native[0] == "--version" || native[0] == "--help")
	var connections []Connection
	if !probe {
		b, e := os.ReadFile(filepath.Join(home, filepath.FromSlash(ConfigPath(cli))))
		if e != nil {
			return report("captured connections unavailable")
		}
		defer clear(b)
		d := json.NewDecoder(bytes.NewReader(b))
		d.DisallowUnknownFields()
		if d.Decode(&connections) != nil || connections == nil || d.Decode(new(any)) != io.EOF {
			return report("invalid captured connections")
		}
		seen := map[uuid.UUID]bool{}
		for _, c := range connections {
			if c.AccountID == uuid.Nil || c.VersionID == uuid.Nil || seen[c.AccountID] || Validate(cli, c.Fields) != nil {
				return report("invalid captured connections")
			}
			seen[c.AccountID] = true
		}
	}
	if list {
		infos := make([]Info, 0, len(connections))
		for _, c := range connections {
			infos = append(infos, Info{c.AccountID, c.Name, c.VersionID})
		}
		if json.NewEncoder(o.Stdout).Encode(infos) != nil {
			return 1
		}
		return 0
	}
	var prefix []string
	env := []string{"HOME=" + home, "PATH=/usr/bin:/bin", "LANG=C.UTF-8"}
	if !probe {
		if id == uuid.Nil {
			if len(connections) == 0 {
				return report("no authorized database connection")
			}
			if len(connections) != 1 {
				return report("ambiguous connection; use --loop-connection-id")
			}
			id = connections[0].AccountID
		}
		found := false
		for _, c := range connections {
			if c.AccountID == id {
				found = true
				prefix, env, e = nativeConfig(cli, home, c)
				break
			}
		}
		if !found {
			return report("connection not captured in this scope")
		}
		if e != nil {
			return report("private connection configuration failed")
		}
	}
	cmd := exec.CommandContext(ctx, nativePath, append(prefix, native...)...)
	cmd.Env = env
	cmd.Stdin = o.Stdin
	cmd.Stdout = o.Stdout
	cmd.Stderr = o.Stderr
	if e = cmd.Run(); e != nil {
		var exit *exec.ExitError
		if errors.As(e, &exit) {
			return exit.ExitCode()
		}
		return report("native client unavailable")
	}
	return 0
}
