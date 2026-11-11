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
			c.Summary = "Start a personal Google login; explicit operation UUID, no automatic resend/polling; a person completes the server callback in a browser on the machine; the code is not forwarded or pasted by hand"
			c.Flags = []cli.Flag{{Name: "cli", Type: cli.TypeEnum, Values: []string{"gcloud"}, Required: true, Description: "Google CLI; this command is google_user only; a service account is entered from a file with create/replace"}, {Name: "operation-id", Type: cli.TypeString, Required: true, Description: "A stable non-zero operation UUID generated and kept by the caller; when the response is unknown, check the status with this ID"}, {Name: "name", Type: cli.TypeString, Description: "Display label for the new account; cannot be used to locate the identity"}, {Name: "account-id", Type: cli.TypeString, Description: "UUID of the existing project gcloud account to log in again; requires the expected revision and --yes"}, credentialRevisionFlag("expected-revision", false), cli.YesFlag}
			c.Constraints = []cli.Constraint{{Kind: cli.ExactlyOne, Flags: []string{"name", "account-id"}}, {Kind: cli.Together, Flags: []string{"account-id", "expected-revision"}}}
		case "login-list":
			c.Summary = "Discover active Google CLI logins; without account-id includes both new logins and re-logins; an unknown start result is not assumed successful by name"
			c.Flags = []cli.Flag{{Name: "account-id", Type: cli.TypeString, Description: "Optional filter by existing account UUID; no cli/provider query parameters are sent"}}
			c.Retry = true
			c.Output = "CLIAuthOperations"
		case "login-status":
			c.Summary = "Read a Google CLI login by operation UUID; only an active one may contain a short-lived URL, null in a final state; it is not persisted or opened automatically"
			c.Args = append(c.Args, operationArg)
			c.Retry = true
		case "login-cancel":
			c.Summary = "Cancel a Google CLI login operation without deleting the old account or resending the POST; a final state keeps its original result"
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
