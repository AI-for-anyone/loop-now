package commands

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"loop-now/internal/loopctl/cli"
	"loop-now/internal/loopctl/conn"
	"loop-now/internal/loopctl/output"
)

func addDBConnections(r *cli.Registry) {
	r.Group("agent db-connections", "Database connection sets of project Agents; independent of the old single selection; adds no CLI permission")
	for _, action := range []string{"show", "set", "clear"} {
		c := &cli.Command{Path: []string{"agent", "db-connections", action}, Args: []cli.Arg{agentArg}, Identities: externalOnly, Network: true, Output: "DBConnections", Run: dbConnectionsCommand(action), Flags: []cli.Flag{{Name: "cli", Type: cli.TypeEnum, Values: []string{"psql", "mysql"}, Required: true, Description: "Database CLI template"}}}
		switch action {
		case "show":
			c.Summary = "Read the set mode and the safe status of each connection; unavailable accounts are kept, no remote probing"
			c.Retry = true
		case "set":
			c.Summary = "Atomically replace the whole database set; an explicit empty set does not fall back, and existing tasks keep their original snapshot"
			c.Flags = append(c.Flags, cli.Flag{Name: "account-id", Type: cli.TypeString, Repeatable: true, Description: "Stable project account UUID, repeatable; not located by name and not appended to the old set"}, cli.Flag{Name: "empty", Type: cli.TypeBool, Description: "Explicitly save an empty set; falling back to the old single account is not allowed"}, credentialRevisionFlag("expected-config-version", true))
			c.Constraints = []cli.Constraint{{Kind: cli.ExactlyOne, Flags: []string{"account-id", "empty"}}}
			c.Destructive = true
		case "clear":
			c.Summary = "Delete the set configuration and restore the original single mode; project accounts are not deleted and the old single selection is not changed"
			c.Flags = append(c.Flags, credentialRevisionFlag("expected-config-version", true))
			c.Destructive = true
		}
		r.Add(c)
	}
}

func dbConnectionsCommand(action string) cli.RunFunc {
	return func(ctx context.Context, inv *cli.Invocation) (any, error) {
		// Validate before resolving an Agent address, which may perform a GET.
		ids := []string{}
		seen := map[string]bool{}
		for _, raw := range inv.Strings("account-id") {
			id := strings.ToLower(raw)
			if !IsUUID(id) || id == "00000000-0000-0000-0000-000000000000" || seen[id] {
				return nil, output.Errorf(output.CodeInvalidArgument, "account-id 必须是互不重复的非零 UUID")
			}
			seen[id] = true
			ids = append(ids, id)
		}
		if action == "set" && inv.Bool("empty") {
			ids = []string{}
		}
		id, err := agentID(ctx, inv, inv.Args[0])
		if err != nil {
			return nil, err
		}
		path := []string{"agents", id, "db-connections", inv.String("cli")}
		if action == "show" {
			return get(ctx, inv, nil, path...)
		}
		var data any
		if action == "set" {
			data, err = send(ctx, inv, http.MethodPut, map[string]any{"accountIds": ids, "expectedConfigVersion": inv.Int("expected-config-version")}, path...)
		} else {
			data, err = call(ctx, inv, conn.Request{Method: http.MethodDelete, Path: path, Query: url.Values{"expectedConfigVersion": {strconv.Itoa(inv.Int("expected-config-version"))}}})
		}
		var e *output.Error
		if errors.As(err, &e) && (e.Code == output.CodeConnectionFailed || e.Code == output.CodeTimeout || e.Code == output.CodeUnexpectedResponse) {
			copy := *e
			copy.Message += "；写入结果未知，未自动重发。请显式运行 agent db-connections show 核对集合及 configVersion，不自动合并或覆盖"
			return nil, &copy
		}
		return data, err
	}
}
