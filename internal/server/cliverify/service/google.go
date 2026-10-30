package service

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"loop-now/internal/server/cliauth"
	"loop-now/internal/server/clicredential"
	"loop-now/internal/server/clitemplate"
	"loop-now/internal/server/cliverify"
	"loop-now/internal/server/store"
	"os"
	"path/filepath"
	"slices"
)

// googleSelfContained 报告当前 gcloud 运行树是否为 Linux 自包含安装（没有私有 Python.framework）。
func googleSelfContained(fw string) bool {
	fi, e := os.Stat(fw)
	return e != nil || !fi.IsDir()
}

func (s *Service) googleVersionSpec(home string, env []string, t *clitemplate.Template) CommandSpec {
	sdk, fw := s.Installer.GoogleRuntimePaths(t)
	env = append(env, "CLOUDSDK_CONFIG="+filepath.Join(home, ".loop-now/gcloud/config"))
	if googleSelfContained(fw) {
		// Linux 自包含安装：bin/gcloud 直接执行，Python 由环境提供。
		if p := clitemplate.ResolveGooglePython(); p != "" {
			env = append(env, "CLOUDSDK_PYTHON="+p)
		}
		return CommandSpec{CLI: "gcloud", Phase: "version", Home: home, Environment: env, Executable: filepath.Join(sdk, "bin", "gcloud"), Args: []string{"--version"}}
	}
	env = append(env, "PYTHONHOME="+filepath.Join(fw, "Versions/3.13"), "PYTHONNOUSERSITE=1", "DYLD_FRAMEWORK_PATH="+filepath.Dir(fw), "DYLD_LIBRARY_PATH="+filepath.Join(fw, "Versions/3.13/lib"))
	return CommandSpec{CLI: "gcloud", Phase: "version", Home: home, Environment: env, Executable: filepath.Join(fw, "Versions/3.13/bin/python3.13"), Args: []string{"-S", filepath.Join(sdk, "lib/gcloud.py"), "--version"}}
}
func (s *Service) googleProbe(ctx context.Context, op cliverify.Operation, d descriptor, home string, env []string, row store.CredentialRow, lease *os.File) cliverify.Result {
	fail := func(code string) cliverify.Result { return cliverify.Result{Outcome: "failed", ErrorCode: &code} }
	spec := s.googleVersionSpec(home, env, d.Template)
	spec.Phase = "probe"
	sdk, fw := s.Installer.GoogleRuntimePaths(d.Template)
	if !filepath.IsAbs(s.Options.GoogleEntry) {
		return fail("configuration_invalid")
	}
	if googleSelfContained(fw) {
		// Linux 自包含安装：审阅固定的验证脚本由系统 Python 解释执行。
		py := clitemplate.ResolveGooglePython()
		if py == "" {
			return fail("configuration_invalid")
		}
		spec.Executable = py
	}
	spec.Args = []string{"-S", s.Options.GoogleEntry, "--sdk-root", sdk}
	if op.AuthMethod == "oauth" {
		if s.Google == nil {
			return fail("credential_unavailable")
		}
		binding := clicredential.VerificationGrantBinding{ProjectID: op.ProjectID, AccountID: op.AccountID, VersionID: op.VersionID, OperationID: op.ID}
		// Resolve once before launch so typed requires_login remains a safe result;
		// runtime re-resolves only through the same original strong binding.
		if _, e := s.Google.ResolveVerification(ctx, binding); e != nil {
			var code = "credential_unavailable"
			if safe, ok := e.(*cliauth.SafeError); ok && safe.Code == "requires_login" {
				code = "requires_login"
			}
			return fail(code)
		}
		broker, e := cliauth.NewVerificationBroker(binding, s.Google)
		if e != nil {
			return fail("credential_unavailable")
		}
		defer func() {
			c, cancel := context.WithTimeout(context.Background(), s.Options.StopGrace)
			defer cancel()
			broker.Close(c)
		}()
		for k, v := range broker.Environment() {
			spec.Environment = append(spec.Environment, k+"="+v)
		}
		spec.Environment = append(spec.Environment, "CLOUDSDK_AUTH_CREDENTIAL_FILE_OVERRIDE="+op.VersionID.String())
	} else {
		raw := slices.Clone(row.Secret)
		defer clear(raw)
		payload, e := decodePayload(raw)
		if e != nil || len(payload.Env) != 0 {
			return fail("configuration_invalid")
		}
		defer func() {
			for _, f := range payload.Files {
				clear(f.Content)
			}
		}()
		var sa []byte
		for _, f := range payload.Files {
			if f.Path == ".loop-now/gcloud/service-account.json" && sa == nil {
				sa = f.Content
			} else if f.Path != ".loop-now/gcloud/default-project" {
				return fail("configuration_invalid")
			}
		}
		if sa == nil {
			return fail("configuration_invalid")
		}
		// Reuse the pinned template's SA validation and fixed token endpoint. The
		// saved optional business default project is deliberately not materialized.
		clean, _, e := d.Template.Render(map[string]string{"serviceAccountJson": string(sa)})
		if e != nil {
			return fail("configuration_invalid")
		}
		defer func() {
			for _, f := range clean.Files {
				clear(f.Content)
			}
		}()
		if e = privateFiles(home, clean); e != nil {
			return fail("configuration_invalid")
		}
		spec.Environment = append(spec.Environment, "CLOUDSDK_AUTH_CREDENTIAL_FILE_OVERRIDE="+filepath.Join(home, ".loop-now/gcloud/service-account.json"))
	}
	if e := os.MkdirAll(filepath.Join(home, ".loop-now/gcloud/config"), 0700); e != nil {
		return fail("configuration_invalid")
	}
	spec.Environment = append(spec.Environment, "CLOUDSDK_CORE_SHOULD_PROMPT_TO_ENABLE_API=false", "CLOUDSDK_CORE_DISABLE_PROMPTS=true", "CLOUDSDK_CORE_DISABLE_FILE_LOGGING=true")
	if e := s.Store.CLIVerificationPhase(ctx, op.ProjectID, op.AccountID, op.ID, "probing", nil); e != nil {
		return fail("target_changed")
	}
	output := s.execute(ctx, spec, op, lease)
	defer clear(output.Stdout)
	defer clear(output.Stderr)
	if ctx.Err() != nil || output.TimedOut {
		return fail("timeout")
	}
	if output.ExitCode != 0 || output.Cancelled || output.Overflow {
		if s.log != nil {
			s.log.Warn("CLI验证子进程未正常结束", "cli", op.CLI, "exitCode", output.ExitCode, "cancelled", output.Cancelled, "outputOverflow", output.Overflow)
		}
		return fail("probe_failed")
	}
	var observed struct {
		Passed     bool    `json:"passed"`
		ErrorCode  *string `json:"errorCode"`
		HTTPStatus *int    `json:"httpStatus"`
	}
	dec := json.NewDecoder(bytes.NewReader(output.Stdout))
	dec.DisallowUnknownFields()
	if dec.Decode(&observed) != nil || dec.Decode(new(any)) != io.EOF {
		return fail("probe_failed")
	}
	if observed.HTTPStatus != nil && (*observed.HTTPStatus < 100 || *observed.HTTPStatus > 599) {
		return fail("probe_failed")
	}
	if observed.Passed {
		if observed.ErrorCode != nil || observed.HTTPStatus == nil || *observed.HTTPStatus != 200 {
			return fail("probe_failed")
		}
		return cliverify.Result{Outcome: "passed", HTTPStatus: observed.HTTPStatus}
	}
	if observed.ErrorCode == nil {
		return fail("probe_failed")
	}
	switch *observed.ErrorCode {
	case "provider_policy", "authentication_failed", "rate_limited", "probe_failed", "configuration_invalid":
	default:
		return fail("probe_failed")
	}
	r := fail(*observed.ErrorCode)
	r.HTTPStatus = observed.HTTPStatus
	return r
}
