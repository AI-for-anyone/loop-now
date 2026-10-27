package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"loop-now/internal/server/clitemplate"
	"loop-now/internal/server/cliverify"
	"loop-now/internal/server/cliverify/probe"
	"loop-now/internal/server/dbcli"
	"loop-now/internal/server/store"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
)

func baseEnvironment(home string, t *clitemplate.Template) []string {
	env := []string{"HOME=" + home, "XDG_CONFIG_HOME=" + filepath.Join(home, ".config"), "PATH=/usr/bin:/bin:/usr/sbin:/sbin", "LANG=C", "LC_ALL=C", "TMPDIR=" + home}
	keys := make([]string, 0, len(t.Env))
	for k := range t.Env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		env = append(env, k+"="+t.Env[k])
	}
	return env
}
func decodePayload(raw []byte) (clitemplate.Payload, error) {
	var p clitemplate.Payload
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&p) != nil || d.Decode(new(any)) != io.EOF {
		return p, errors.New("CLI载荷无效")
	}
	return p, nil
}
func privateFiles(home string, p clitemplate.Payload) error {
	for _, f := range p.Files {
		if f.Path == "" || filepath.IsAbs(f.Path) || filepath.Clean(f.Path) != f.Path || f.Path == ".." || strings.HasPrefix(f.Path, "../") || strings.ContainsRune(f.Path, 0) {
			return errors.New("CLI秘密路径无效")
		}
		path := filepath.Join(home, f.Path)
		if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
			return e
		}
		file, e := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e != nil {
			return e
		}
		_, e = file.Write(f.Content)
		closeErr := file.Close()
		if e != nil {
			return e
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}
func (s *Service) materialize(ctx context.Context, op cliverify.Operation, home string, row store.CredentialRow) (probe.Request, []string, error) {
	req := probe.Request{CLI: op.CLI, ConnectionID: op.AccountID}
	raw := slices.Clone(row.Secret)
	defer clear(raw)
	p, e := decodePayload(raw)
	if e != nil {
		return req, nil, e
	}
	defer func() {
		for _, f := range p.Files {
			clear(f.Content)
		}
	}()
	extra := []string{}
	switch op.CLI {
	case "mysql", "psql":
		p, e = clitemplate.AggregateDatabase(op.CLI, []clitemplate.DatabaseEntry{{Connection: dbcli.Connection{ProjectID: &op.ProjectID, AccountID: op.AccountID, VersionID: op.VersionID, Name: op.CLI}, Payload: p}})
		if e != nil {
			return req, nil, e
		}
	case "gh":
		if len(p.Files) != 0 || len(p.Env) != 1 || p.Env["GH_TOKEN"] == "" || strings.ContainsRune(p.Env["GH_TOKEN"], 0) {
			return req, nil, errors.New("GitHub载荷无效")
		}
		extra = append(extra, "GH_TOKEN="+p.Env["GH_TOKEN"], "GH_CONFIG_DIR="+filepath.Join(home, ".config/gh"), "GH_PROMPT_DISABLED=1", "GH_NO_UPDATE_NOTIFIER=1")
	case "lark-cli":
		if len(p.Env) != 0 {
			return req, nil, errors.New("Lark载荷无效")
		}
		found := false
		for _, f := range p.Files {
			switch f.Path {
			case ".lark-cli/config.json":
				if found {
					return req, nil, errors.New("Lark载荷重复")
				}
				found = true
				var cfg struct {
					Apps []struct {
						AppID      string            `json:"appId"`
						AppSecret  string            `json:"appSecret"`
						Brand      string            `json:"brand"`
						StrictMode string            `json:"strictMode"`
						DefaultAs  string            `json:"defaultAs"`
						Users      []json.RawMessage `json:"users"`
					} `json:"apps"`
				}
				d := json.NewDecoder(bytes.NewReader(f.Content))
				d.DisallowUnknownFields()
				if d.Decode(&cfg) != nil || d.Decode(new(any)) != io.EOF || len(cfg.Apps) != 1 {
					return req, nil, errors.New("Lark载荷无效")
				}
				a := cfg.Apps[0]
				if a.AppID == "" || a.AppSecret == "" || a.StrictMode != "bot" || a.DefaultAs != "bot" || len(a.Users) != 0 || (a.Brand != "lark" && a.Brand != "feishu") {
					return req, nil, errors.New("Lark身份无效")
				}
				req.ExpectedAppID = a.AppID
				req.ExpectedBrand = a.Brand
			case "Library/Application Support/lark-cli/master.key.file":
				if len(f.Content) != 32 {
					return req, nil, errors.New("Lark私有key无效")
				}
			default:
				return req, nil, errors.New("Lark路径无效")
			}
		}
		if !found {
			return req, nil, errors.New("缺少Lark配置")
		}
	default:
		return req, nil, errors.New("不支持该CLI")
	}
	if e = privateFiles(home, p); e != nil {
		return req, nil, e
	}
	return req, extra, nil
}
func fixedPlan(req probe.Request) (probe.Plan, error) { return probe.Build(req) }
func parseOutput(req probe.Request, out privateOutput, ctx context.Context) cliverify.Result {
	result := cliverify.Result{Outcome: "failed"}
	code := "probe_failed"
	result.ErrorCode = &code
	if ctx.Err() != nil || out.TimedOut {
		code = "timeout"
		return result
	}
	if out.Cancelled || out.Overflow {
		return result
	}
	parsed := probe.Parse(req, probe.Observation{ExitCode: out.ExitCode, Stdout: out.Stdout, Stderr: out.Stderr})
	if parsed.Passed {
		result.Outcome = "passed"
		result.ErrorCode = nil
	} else {
		code = parsed.ErrorCode
	}
	result.HTTPStatus = parsed.HTTPStatus
	return result
}
