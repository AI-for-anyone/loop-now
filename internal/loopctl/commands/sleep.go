package commands

import (
	"context"
	"errors"
	"net/http"

	"loop-now/internal/loopctl/cli"
	"loop-now/internal/loopctl/output"
)

func addSleep(r *cli.Registry) {
	r.Add(&cli.Command{
		Path: []string{"sleep"}, Identities: []cli.Identity{cli.IdentityAgent}, Network: true,
		Summary: "Register a sleep for yourself; it takes effect only after the current turn then ends normally; on a network failure it may already be registered, so check the status first",
		Flags: []cli.Flag{
			{Name: "run", Type: cli.TypeString, Required: true, Description: "The run UUID from the platform's prompt for this turn; it is not queried or replaced with another run"},
			{Name: "seconds", Type: cli.TypeInt, Required: true, Min: intp(1), Max: intp(2147483647), Description: "Seconds to wait, 1..2147483647; counted from when the current turn finishes successfully"},
			taskActionFlag,
			{Name: "reason", Type: cli.TypeString, Description: "Optional note, subject to the server's message text length limit"},
		}, Output: "SleepReceipt",
		Run: func(ctx context.Context, inv *cli.Invocation) (any, error) {
			for _, name := range []string{"run", "action-id"} {
				if !IsUUID(inv.String(name)) {
					return nil, output.Errorf(output.CodeInvalidArgument, "--%s 必须为 UUID", name)
				}
			}
			body := map[string]any{"runId": inv.String("run"), "actionId": inv.String("action-id"), "seconds": inv.Int("seconds")}
			if inv.Has("reason") {
				body["reason"] = inv.String("reason")
			}
			data, err := send(ctx, inv, http.MethodPost, body, "sleep")
			var e *output.Error
			if errors.As(err, &e) && e.HTTPStatus == 0 && (e.Code == output.CodeTimeout || e.Code == output.CodeConnectionFailed) {
				copy := *e
				copy.Message += "；sleep 可能已登记，先运行 loopctl sleep status；人工重发须保持相同 action-id、run 和内容"
				err = &copy
			}
			return data, err
		},
	})
	r.Add(&cli.Command{
		Path: []string{"sleep", "status"}, Identities: []cli.Identity{cli.IdentityAgent}, Network: true,
		Summary: "Read-only query of your current run and active sleep; null when there is none; does not create or reset a sleep",
		Retry:   true, Output: "SleepState",
		Run: func(ctx context.Context, inv *cli.Invocation) (any, error) {
			return get(ctx, inv, nil, "sleep")
		},
	})
}
