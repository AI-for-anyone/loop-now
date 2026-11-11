package commands

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"loop-now/internal/loopctl/cli"
	"loop-now/internal/loopctl/conn"
	"loop-now/internal/loopctl/output"
)

var accountArg = cli.Arg{Name: "accountId", Description: "Stable project account UUID; display names can repeat and cannot be used to locate it"}
var operationArg = cli.Arg{Name: "operationId", Description: "Project OAuth operation UUID, from login or login-list"}
var credentialNamePattern = regexp.MustCompile(`^[a-z0-9._-]+$`)

func credentialRevisionFlag(name string, required bool) cli.Flag {
	return cli.Flag{Name: name, Type: cli.TypeInt, Min: intp(1), Required: required, Description: "Explicitly submit the expected revision you read; on conflict it does not read the new revision or override and retry automatically"}
}
func credentialLocatorFlag(purpose string, required bool) cli.Flag {
	name, description := "provider", "Model provider ID, from model providers"
	if purpose == "cli" {
		name, description = "cli", "Name of a CLI with a declared template; saving an account adds no Agent permission"
	}
	return cli.Flag{Name: name, Type: cli.TypeString, Required: required, Description: description}
}

func addProjectCredentials(r *cli.Registry) {
	for _, purpose := range []string{"model", "cli"} {
		group := purpose + "-account"
		r.Group("project "+group, "Shared project accounts: secrets are entered only from a credential file/stdin; remote authentication is not verified")
		for _, action := range []string{"list", "get", "create", "replace", "rename", "delete", "impact"} {
			c := &cli.Command{Path: []string{"project", group, action}, Identities: externalOnly, Network: true, Args: []cli.Arg{projectArg}, Output: "ProjectCredentialAccount", Run: projectCredentialCommand(purpose, action)}
			switch action {
			case "list":
				c.Summary = "List the project's live accounts, optionally filtered by the provider or CLI of the purpose; no remote probing"
				c.Flags = []cli.Flag{credentialLocatorFlag(purpose, false)}
				c.Retry = true
				c.Output = "ProjectCredentialAccountList"
			case "get":
				c.Summary = "Read the safe account status by stable UUID; a deleted account may return a tombstone"
				c.Args = append(c.Args, accountArg)
				c.Retry = true
			case "create":
				c.Summary = "Add a project account; only saves the credential and does not claim remote authentication succeeded"
				c.Flags = []cli.Flag{{Name: "name", Type: cli.TypeString, Required: true, Description: "Display label; can repeat and is not the account identity"}, credentialLocatorFlag(purpose, true), projectCredentialFileFlag(purpose)}
				c.Stdin = true
			case "replace":
				c.Summary = "Replace a project account's secret entirely; existing tasks keep the old version; use login for an OAuth re-login"
				c.Args = append(c.Args, accountArg)
				c.Flags = []cli.Flag{credentialRevisionFlag("expected-revision", true), projectCredentialFileFlag(purpose)}
				c.Stdin = true
				c.Destructive = true
			case "rename":
				c.Summary = "Change the display label, keeping the account ID and credential version"
				c.Args = append(c.Args, accountArg)
				c.Flags = []cli.Flag{{Name: "name", Type: cli.TypeString, Required: true, Description: "New display label"}, credentialRevisionFlag("expected-revision", true)}
			case "delete":
				c.Summary = "Delete a shared account and block new use; explicit selections do not fall back to other accounts, and existing tasks keep the old version"
				c.Args = append(c.Args, accountArg)
				c.Flags = []cli.Flag{credentialRevisionFlag("expected-revision", true)}
				c.Destructive = true
			case "impact":
				c.Summary = "Read how the currently saved configuration affects its users, distinguishing explicit/automatic; excludes past tasks, and the read baseline does not guarantee later write results"
				c.Flags = []cli.Flag{credentialLocatorFlag(purpose, true), {Name: "account-id", Type: cli.TypeString, Description: "Existing account UUID; when omitted, shows the effect of a new account on automatic users"}}
				c.Retry = true
				c.Output = "CredentialAccountImpact"
			}
			r.Add(c)
		}
	}
	addProjectCredentialOAuth(r)
	addCredentialSelection(r)
}

func projectCredentialFileFlag(purpose string) cli.Flag {
	description := `Only a credential object {"type":"api_key","key":"..."}; metadata/version conditions/provider/the whole request are not accepted. - means stdin; secrets are not accepted as command-line values`
	if purpose == "cli" {
		description = `Only a credential object {"type":"cli","fields":{"template field":"string value"}}; a gcloud service account uses the JSON string of serviceAccountJson and an optional defaultProject, without authType/nested objects/host ADC; database fields follow cli-template list. metadata/version conditions/the whole request are not accepted. - means stdin`
	}
	return cli.Flag{Name: "from-file", Type: cli.TypePathOrStdin, Required: true, Description: description}
}

func projectCredentialCommand(purpose, action string) cli.RunFunc {
	return func(ctx context.Context, inv *cli.Invocation) (any, error) {
		// 所有本地条件及秘密文件先验证，再执行名称解析 GET。
		if len(inv.Args) > 1 && !IsUUID(inv.Args[1]) {
			return nil, credentialUUIDError("accountId")
		}
		if inv.Has("account-id") && !IsUUID(inv.String("account-id")) {
			return nil, credentialUUIDError("account-id")
		}
		locator := "provider"
		if purpose == "cli" {
			locator = "cli"
		}
		if inv.Has(locator) && !credentialNamePattern.MatchString(inv.String(locator)) {
			return nil, output.Errorf(output.CodeInvalidArgument, "提供方或 CLI 名称格式不合法")
		}
		if inv.Has("name") && strings.TrimSpace(inv.String("name")) == "" {
			return nil, output.Errorf(output.CodeInvalidArgument, "账号名称不能为空白")
		}
		var secret json.RawMessage
		if action == "create" || action == "replace" {
			var err error
			secret, err = projectCredentialFile(inv, purpose)
			if err != nil {
				return nil, err
			}
		}
		id, err := projectID(ctx, inv, inv.Args[0])
		if err != nil {
			return nil, err
		}
		path := []string{"projects", id, "credential-accounts"}
		if len(inv.Args) > 1 {
			path = append(path, inv.Args[1])
		}
		switch action {
		case "list":
			q := url.Values{"purpose": {purpose}}
			if inv.Has(locator) {
				q.Set(locator, inv.String(locator))
			}
			return get(ctx, inv, q, path...)
		case "get":
			return get(ctx, inv, nil, path...)
		case "impact":
			q := url.Values{"purpose": {purpose}, locator: {inv.String(locator)}}
			if inv.Has("account-id") {
				q.Set("accountId", inv.String("account-id"))
			}
			return get(ctx, inv, q, "projects", id, "credential-account-impact")
		case "create":
			return credentialSend(ctx, inv, http.MethodPost, map[string]any{"purpose": purpose, locator: inv.String(locator), "name": inv.String("name"), "credential": secret}, path...)
		case "replace":
			return credentialSend(ctx, inv, http.MethodPut, map[string]any{"credential": secret, "expectedRevision": inv.Int("expected-revision")}, append(path, "credential")...)
		case "rename":
			return credentialSend(ctx, inv, http.MethodPatch, map[string]any{"name": inv.String("name"), "expectedRevision": inv.Int("expected-revision")}, path...)
		default:
			return credentialCall(ctx, inv, conn.Request{Method: http.MethodDelete, Path: path, Query: url.Values{"expectedRevision": {strconv.Itoa(inv.Int("expected-revision"))}}})
		}
	}
}

func addProjectCredentialOAuth(r *cli.Registry) {
	for _, action := range []string{"login", "login-list", "login-status", "login-cancel"} {
		c := &cli.Command{Path: []string{"project", "model-account", action}, Identities: externalOnly, Network: true, Args: []cli.Arg{projectArg}, Output: "ProjectModelAuthState", Run: projectOAuthCommand(action)}
		switch action {
		case "login":
			c.Summary = "Start a project Codex login; the browser step must be completed on the machine that can receive the server's local callback; no automatic authentication/pasting of the code/forwarding of the callback; when the response is unknown, check login-list and do not resend"
			c.Flags = []cli.Flag{{Name: "provider", Type: cli.TypeEnum, Values: []string{"openai-codex"}, Required: true, Description: "Codex provider"}, {Name: "name", Type: cli.TypeString, Description: "Display label for the new account"}, {Name: "account-id", Type: cli.TypeString, Description: "UUID of the existing account to log in again; give it together with --expected-revision and --yes"}, credentialRevisionFlag("expected-revision", false), cli.YesFlag}
			c.Constraints = []cli.Constraint{{Kind: cli.ExactlyOne, Flags: []string{"name", "account-id"}}, {Kind: cli.Together, Flags: []string{"account-id", "expected-revision"}}}
		case "login-list":
			c.Summary = "Discover the project's current active OAuth operations; without an account ID includes new logins and re-logins; the authorization URL is not persisted"
			c.Flags = []cli.Flag{{Name: "provider", Type: cli.TypeEnum, Values: []string{"openai-codex"}, Required: true, Description: "Codex provider"}, {Name: "account-id", Type: cli.TypeString, Description: "Optional existing account UUID"}}
			c.Retry = true
			c.Output = "ProjectModelAuthOperationList"
		case "login-status":
			c.Summary = "Read a login operation; the short-lived authorization URL is visible only while the operation is active and is cleared in a final state"
			c.Args = append(c.Args, operationArg)
			c.Retry = true
		case "login-cancel":
			c.Summary = "Cancel a project login operation without deleting the saved account; a final state keeps its original result"
			c.Args = append(c.Args, operationArg)
			c.Destructive = true
		}
		r.Add(c)
	}
}
func projectOAuthCommand(action string) cli.RunFunc {
	return func(ctx context.Context, inv *cli.Invocation) (any, error) {
		if len(inv.Args) > 1 && !IsUUID(inv.Args[1]) {
			return nil, credentialUUIDError("operationId")
		}
		if inv.Has("account-id") && !IsUUID(inv.String("account-id")) {
			return nil, credentialUUIDError("account-id")
		}
		if inv.Has("name") && strings.TrimSpace(inv.String("name")) == "" {
			return nil, output.Errorf(output.CodeInvalidArgument, "账号名称不能为空白")
		}
		if action == "login" {
			yes := inv.Bool("yes")
			if inv.Has("account-id") && !yes {
				return nil, output.Errorf(output.CodeConfirmationRequired, "重登覆盖账号需 --yes 确认；失败保留原版本")
			}
		}
		id, err := projectID(ctx, inv, inv.Args[0])
		if err != nil {
			return nil, err
		}
		path := []string{"projects", id, "model-auth", "operations"}
		switch action {
		case "login":
			body := map[string]any{"provider": inv.String("provider")}
			if inv.Has("account-id") {
				body["accountId"] = inv.String("account-id")
				body["expectedRevision"] = inv.Int("expected-revision")
			} else {
				body["name"] = inv.String("name")
			}
			return credentialSend(ctx, inv, http.MethodPost, body, path...)
		case "login-list":
			q := url.Values{"provider": {inv.String("provider")}}
			if inv.Has("account-id") {
				q.Set("accountId", inv.String("account-id"))
			}
			return get(ctx, inv, q, path...)
		case "login-status":
			return get(ctx, inv, nil, append(path, inv.Args[1])...)
		default:
			return credentialSend(ctx, inv, http.MethodPost, nil, append(path, inv.Args[1], "cancel")...)
		}
	}
}

func addCredentialSelection(r *cli.Registry) {
	r.Group("agent credential-selection", "Read and make single selections of project accounts; adds no CLI permission and offers no global clear or disable")
	for _, action := range []string{"get", "set", "clear"} {
		c := &cli.Command{Path: []string{"agent", "credential-selection", action}, Identities: externalOnly, Network: true, Args: []cli.Arg{agentArg}, Output: "CredentialSelections", Run: credentialSelectionCommand(action)}
		if action == "get" {
			c.Summary = "Read the currently saved model providers and existing single selections; for database sets go by db-connections show; no remote probing"
			c.Retry = true
		} else {
			c.Summary = "Change only one provider/CLI single selection; returns 409 for a database explicit_set and does not convert the set; other items and permissions are kept; does not read the version or retry automatically"
			c.Flags = []cli.Flag{credentialLocatorFlag("model", false), credentialLocatorFlag("cli", false), credentialRevisionFlag("expected-config-version", true)}
			c.Constraints = []cli.Constraint{{Kind: cli.ExactlyOne, Flags: []string{"provider", "cli"}}}
			if action == "set" {
				c.Flags = append(c.Flags, cli.Flag{Name: "account-id", Type: cli.TypeString, Required: true, Description: "UUID of an account of the same purpose in this project"})
			} else {
				c.Destructive = true
				c.Summary = "Restore automatic for that provider/CLI only; returns 409 for a database explicit_set and does not clear the set; the migration barrier writes a null override for that provider only, other providers stay legacy_cleared; no account is deleted"
			}
		}
		r.Add(c)
	}
}
func credentialSelectionCommand(action string) cli.RunFunc {
	return func(ctx context.Context, inv *cli.Invocation) (any, error) {
		if inv.Has("account-id") && !IsUUID(inv.String("account-id")) {
			return nil, credentialUUIDError("account-id")
		}
		locator, purpose := "provider", "model"
		if inv.Has("cli") {
			locator, purpose = "cli", "cli"
		}
		if action != "get" && !credentialNamePattern.MatchString(inv.String(locator)) {
			return nil, output.Errorf(output.CodeInvalidArgument, "提供方或 CLI 名称格式不合法")
		}
		id, err := agentID(ctx, inv, inv.Args[0])
		if err != nil {
			return nil, err
		}
		if action == "get" {
			return get(ctx, inv, nil, "agents", id, "credential-selections")
		}
		var account any
		if action == "set" {
			account = inv.String("account-id")
		}
		return credentialSend(ctx, inv, http.MethodPut, map[string]any{"purpose": purpose, locator: inv.String(locator), "accountId": account, "expectedConfigVersion": inv.Int("expected-config-version")}, "agents", id, "credential-selections")
	}
}
func credentialUUIDError(field string) error {
	return output.Errorf(output.CodeInvalidArgument, "%s 必须为稳定 UUID，不能按显示名称操作", field)
}

// 严格验证唯一 credential 对象；不把 decoder 错误、未知键或输入值放入错误。
func projectCredentialFile(inv *cli.Invocation, purpose string) (json.RawMessage, error) {
	b, err := readInput(inv, "from-file")
	if err != nil {
		return nil, err
	}
	fail := func() (json.RawMessage, error) {
		return nil, output.Errorf(output.CodeInvalidArgument, "凭证文件只接受对应用途的 credential 对象；不允许 metadata、版本条件、重复字段或整份请求，秘密值必须为字符串")
	}
	if !utf8.Valid(b) || !uniqueCredentialJSON(b) {
		return fail()
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(b, &fields) != nil || fields == nil || len(fields) != 2 {
		return fail()
	}
	var kind string
	if json.Unmarshal(fields["type"], &kind) != nil {
		return fail()
	}
	if purpose == "model" {
		var key *string
		if kind != "api_key" || json.Unmarshal(fields["key"], &key) != nil || key == nil || strings.TrimSpace(*key) == "" {
			return fail()
		}
		inv.ProtectSecrets(*key)
	} else {
		var values map[string]string
		if kind != "cli" || json.Unmarshal(fields["fields"], &values) != nil || len(values) == 0 {
			return fail()
		}
		// map[string]string accepts JSON null as empty string; reject null explicitly.
		var raw map[string]json.RawMessage
		_ = json.Unmarshal(fields["fields"], &raw)
		for k, v := range values {
			if k == "" || bytes.Equal(bytes.TrimSpace(raw[k]), []byte("null")) {
				return fail()
			}
			inv.ProtectSecrets(v)
		}
	}
	return json.RawMessage(b), nil
}
func uniqueCredentialJSON(b []byte) bool {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var value func(int) bool
	value = func(depth int) bool {
		if depth > 4 {
			return false
		}
		t, err := dec.Token()
		if err != nil {
			return false
		}
		delim, ok := t.(json.Delim)
		if !ok {
			return true
		}
		if delim != '{' {
			return false
		}
		seen := map[string]bool{}
		for dec.More() {
			k, err := dec.Token()
			name, ok := k.(string)
			if err != nil || !ok || seen[name] {
				return false
			}
			seen[name] = true
			if !value(depth + 1) {
				return false
			}
		}
		end, err := dec.Token()
		return err == nil && end == json.Delim('}')
	}
	return value(0) && func() bool { _, err := dec.Token(); return err == io.EOF }()
}

// A transport failure cannot establish whether a mutation committed. Keep the
// categorized code, and offer only explicit readback, never a retry or name match.
func credentialWriteResult(data any, err error) (any, error) {
	var e *output.Error
	if errors.As(err, &e) && (e.Code == output.CodeConnectionFailed || e.Code == output.CodeTimeout || e.Code == output.CodeUnexpectedResponse) {
		copy := *e
		copy.Message += "；写入结果未知，未自动重发。请用账号 get/list 或 login-list/login-status 核对；显示名称可重名，不能据此认定提交成功"
		return nil, &copy
	}
	return data, err
}
func credentialSend(ctx context.Context, inv *cli.Invocation, method string, body any, path ...string) (any, error) {
	data, err := send(ctx, inv, method, body, path...)
	return credentialWriteResult(data, err)
}
func credentialCall(ctx context.Context, inv *cli.Invocation, req conn.Request) (any, error) {
	data, err := call(ctx, inv, req)
	return credentialWriteResult(data, err)
}
