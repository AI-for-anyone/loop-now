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

var accountArg = cli.Arg{Name: "accountId", Description: "稳定项目账号 UUID；显示名称可重名，不能用于定位"}
var operationArg = cli.Arg{Name: "operationId", Description: "项目 OAuth 操作 UUID，取自 login 或 login-list"}
var credentialNamePattern = regexp.MustCompile(`^[a-z0-9._-]+$`)

func credentialRevisionFlag(name string, required bool) cli.Flag {
	return cli.Flag{Name: name, Type: cli.TypeInt, Min: intp(1), Required: required, Description: "明确提交读取到的预期版本；冲突不自动读取新版或覆盖重试"}
}
func credentialLocatorFlag(purpose string, required bool) cli.Flag {
	name, description := "provider", "模型提供方 ID，取自 model providers"
	if purpose == "cli" {
		name, description = "cli", "已声明模板的 CLI 名称；保存账号不添加 Agent 权限"
	}
	return cli.Flag{Name: name, Type: cli.TypeString, Required: required, Description: description}
}

func addProjectCredentials(r *cli.Registry) {
	for _, purpose := range []string{"model", "cli"} {
		group := purpose + "-account"
		r.Group("project "+group, "项目共享账号：秘密只从 credential 文件/stdin 录入；不验证远端认证")
		for _, action := range []string{"list", "get", "create", "replace", "rename", "delete", "impact"} {
			c := &cli.Command{Path: []string{"project", group, action}, Identities: externalOnly, Network: true, Args: []cli.Arg{projectArg}, Output: "ProjectCredentialAccount", Run: projectCredentialCommand(purpose, action)}
			switch action {
			case "list":
				c.Summary = "列出项目存活账号，可按用途对应的提供方或 CLI 筛选；不远端探测"
				c.Flags = []cli.Flag{credentialLocatorFlag(purpose, false)}
				c.Retry = true
				c.Output = "ProjectCredentialAccountList"
			case "get":
				c.Summary = "按稳定 UUID 读取安全账号状态；已删除账号可返回 tombstone"
				c.Args = append(c.Args, accountArg)
				c.Retry = true
			case "create":
				c.Summary = "新增项目账号；仅保存凭证，不宣称远端认证成功"
				c.Flags = []cli.Flag{{Name: "name", Type: cli.TypeString, Required: true, Description: "显示标签，可重名，不作账号身份"}, credentialLocatorFlag(purpose, true), projectCredentialFileFlag(purpose)}
				c.Stdin = true
			case "replace":
				c.Summary = "整体替换项目账号秘密；已有任务保留旧版本，OAuth 重登使用 login"
				c.Args = append(c.Args, accountArg)
				c.Flags = []cli.Flag{credentialRevisionFlag("expected-revision", true), projectCredentialFileFlag(purpose)}
				c.Stdin = true
				c.Destructive = true
			case "rename":
				c.Summary = "修改显示标签，保留账号 ID 和凭证版本"
				c.Args = append(c.Args, accountArg)
				c.Flags = []cli.Flag{{Name: "name", Type: cli.TypeString, Required: true, Description: "新显示标签"}, credentialRevisionFlag("expected-revision", true)}
			case "delete":
				c.Summary = "删除共享账号，阻止新取用；明确选择不会回退到其他账号，已有任务保留旧版本"
				c.Args = append(c.Args, accountArg)
				c.Flags = []cli.Flag{credentialRevisionFlag("expected-revision", true)}
				c.Destructive = true
			case "impact":
				c.Summary = "读取当前保存配置的使用者影响，区分 explicit/automatic；不含历史任务，读取基线不保证后续写结果"
				c.Flags = []cli.Flag{credentialLocatorFlag(purpose, true), {Name: "account-id", Type: cli.TypeString, Description: "既有账号 UUID；省略时查看新增账号对 automatic 使用者的影响"}}
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
	description := `仅 credential 对象 {"type":"api_key","key":"..."}；不接受 metadata/版本条件/provider/整请求。- 表示 stdin；秘密不接受命令行值`
	if purpose == "cli" {
		description = `仅 credential 对象 {"type":"cli","fields":{"模板字段":"字符串值"}}；gcloud SA 用 serviceAccountJson 的 JSON 字符串及可选 defaultProject，不加 authType/嵌套对象/宿主ADC；DB 字段以 cli-template list 为准。不接受 metadata/版本条件/整请求。- 表示 stdin`
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
			c.Summary = "发起项目 Codex 登录；浏览器须在能接收 server 本机回调的本机完成，不自动认证/粘贴 code/转发回调；响应未知请查 login-list，不重发"
			c.Flags = []cli.Flag{{Name: "provider", Type: cli.TypeEnum, Values: []string{"openai-codex"}, Required: true, Description: "Codex 提供方"}, {Name: "name", Type: cli.TypeString, Description: "新建账号显示标签"}, {Name: "account-id", Type: cli.TypeString, Description: "重登既有账号 UUID，与 --expected-revision 和 --yes 一起提供"}, credentialRevisionFlag("expected-revision", false), cli.YesFlag}
			c.Constraints = []cli.Constraint{{Kind: cli.ExactlyOne, Flags: []string{"name", "account-id"}}, {Kind: cli.Together, Flags: []string{"account-id", "expected-revision"}}}
		case "login-list":
			c.Summary = "发现当前活动项目 OAuth 操作；无账号 ID 时包括新建和重登；不持久化授权 URL"
			c.Flags = []cli.Flag{{Name: "provider", Type: cli.TypeEnum, Values: []string{"openai-codex"}, Required: true, Description: "Codex 提供方"}, {Name: "account-id", Type: cli.TypeString, Description: "可选既有账号 UUID"}}
			c.Retry = true
			c.Output = "ProjectModelAuthOperationList"
		case "login-status":
			c.Summary = "读取登录操作；短期授权 URL 仅活动操作可见，终态清除"
			c.Args = append(c.Args, operationArg)
			c.Retry = true
		case "login-cancel":
			c.Summary = "取消项目登录操作，不删除已保存账号；已终态保留原结果"
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
	r.Group("agent credential-selection", "读取及单项选择项目账号；不增加 CLI 权限，不提供全局清除或禁用")
	for _, action := range []string{"get", "set", "clear"} {
		c := &cli.Command{Path: []string{"agent", "credential-selection", action}, Identities: externalOnly, Network: true, Args: []cli.Arg{agentArg}, Output: "CredentialSelections", Run: credentialSelectionCommand(action)}
		if action == "get" {
			c.Summary = "读取当前保存模型提供方及已有 single 选择；DB 集合以 db-connections show 为准，不探测远端"
			c.Retry = true
		} else {
			c.Summary = "只修改一个 provider/CLI single 选择；DB explicit_set 时返回409，不转换集合；其他项及权限保留，不自动读取版本或重试"
			c.Flags = []cli.Flag{credentialLocatorFlag("model", false), credentialLocatorFlag("cli", false), credentialRevisionFlag("expected-config-version", true)}
			c.Constraints = []cli.Constraint{{Kind: cli.ExactlyOne, Flags: []string{"provider", "cli"}}}
			if action == "set" {
				c.Flags = append(c.Flags, cli.Flag{Name: "account-id", Type: cli.TypeString, Required: true, Description: "本项目同用途账号 UUID"})
			} else {
				c.Destructive = true
				c.Summary = "仅该 provider/CLI 恢复 automatic；DB explicit_set 时返回409，不清集合；迁移屏障仅写该 provider null override，其他 provider 仍 legacy_cleared；不删除账号"
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
