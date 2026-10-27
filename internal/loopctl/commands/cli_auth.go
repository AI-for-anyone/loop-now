package commands

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"

	"loop-now/internal/loopctl/cli"
	"loop-now/internal/loopctl/output"
)

func addCLIAuth(r *cli.Registry) {
	for _, action := range []string{"login", "login-list", "login-status", "login-cancel"} {
		c := &cli.Command{Path: []string{"project", "cli-account", action}, Identities: externalOnly, Network: true, Args: []cli.Arg{projectArg}, Output: "CLIAuthState", Run: cliAuthCommand(action)}
		switch action {
		case "login":
			c.Summary = "发起 Google 个人登录；显式 operation UUID，不自动重发/轮询；由人本机浏览器完成 server 回调，不转发或手贴 code"
			c.Flags = []cli.Flag{{Name: "cli", Type: cli.TypeEnum, Values: []string{"gcloud"}, Required: true, Description: "Google CLI；此命令仅 google_user，SA 使用 create/replace 文件录入"}, {Name: "operation-id", Type: cli.TypeString, Required: true, Description: "调用者生成并保留的稳定非零操作 UUID；响应未知用该 ID 查状态"}, {Name: "name", Type: cli.TypeString, Description: "新账号显示标签，不能用于身份定位"}, {Name: "account-id", Type: cli.TypeString, Description: "重登既有项目 gcloud 账号 UUID，需预期版本与 --yes"}, credentialRevisionFlag("expected-revision", false), cli.YesFlag}
			c.Constraints = []cli.Constraint{{Kind: cli.ExactlyOne, Flags: []string{"name", "account-id"}}, {Kind: cli.Together, Flags: []string{"account-id", "expected-revision"}}}
		case "login-list":
			c.Summary = "发现活动 Google CLI 登录；无 account-id 包含新建与重登，未知发起结果不凭名称猜成功"
			c.Flags = []cli.Flag{{Name: "account-id", Type: cli.TypeString, Description: "可选既有账号 UUID 过滤；不传 cli/provider 查询参数"}}
			c.Retry = true
			c.Output = "CLIAuthOperations"
		case "login-status":
			c.Summary = "按操作 UUID 读取 Google CLI 登录；只有活动态可含短期 URL，终态 null，不持久化或自动打开"
			c.Args = append(c.Args, operationArg)
			c.Retry = true
		case "login-cancel":
			c.Summary = "取消 Google CLI 登录操作，不删除旧账号或重发 POST；终态保留原结果"
			c.Args = append(c.Args, operationArg)
			c.Destructive = true
		}
		r.Add(c)
	}
}

func cliAuthCommand(action string) cli.RunFunc {
	return func(ctx context.Context, inv *cli.Invocation) (any, error) {
		validID := func(id string) bool { return IsUUID(id) && id != "00000000-0000-0000-0000-000000000000" }
		if len(inv.Args) > 1 && !validID(inv.Args[1]) {
			return nil, credentialUUIDError("operationId")
		}
		for _, flag := range []string{"operation-id", "account-id"} {
			if inv.Has(flag) && !validID(inv.String(flag)) {
				return nil, credentialUUIDError(flag)
			}
		}
		if inv.Has("name") && (strings.TrimSpace(inv.String("name")) == "" || utf8.RuneCountInString(inv.String("name")) > 200) {
			return nil, output.Errorf(output.CodeInvalidArgument, "账号名称须非空白且不超过200字符")
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
		path := []string{"projects", id, "cli-auth", "operations"}
		switch action {
		case "login":
			body := map[string]any{"operationId": inv.String("operation-id"), "cli": inv.String("cli"), "authType": "google_user"}
			if inv.Has("account-id") {
				body["accountId"], body["expectedRevision"] = inv.String("account-id"), inv.Int("expected-revision")
			} else {
				body["name"] = inv.String("name")
			}
			return credentialSend(ctx, inv, http.MethodPost, body, path...)
		case "login-list":
			q := url.Values{}
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
