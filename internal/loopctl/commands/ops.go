package commands

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strings"

	"loop-now/internal/loopctl/cli"
	"loop-now/internal/loopctl/output"
)

func addOps(r *cli.Registry) {
	r.Add(&cli.Command{
		Path:       []string{"usage", "list"},
		Summary:    "Total token usage in a time range by Agent and model, sorted by total descending; counts tokens only, no cost",
		Identities: externalOnly,
		Network:    true,
		Retry:      true,
		Flags: []cli.Flag{
			{Name: "from", Type: cli.TypeTime, Description: "Start time (inclusive), RFC 3339; unbounded when not given"},
			{Name: "to", Type: cli.TypeTime, Description: "End time (exclusive), RFC 3339; up to now when not given"},
			scopeFlags[0], scopeFlags[1],
		},
		Constraints: []cli.Constraint{scopeOptional},
		Output:      "{from, to, rows: UsageRow[]}，每行带 scope 与 projectId；不指定范围时为全部项目与平台",
		Run: func(ctx context.Context, inv *cli.Invocation) (any, error) {
			q := url.Values{}
			for _, f := range []string{"from", "to"} {
				if inv.Has(f) {
					q.Set(f, inv.String(f))
				}
			}
			if err := scopeQuery(ctx, inv, q); err != nil {
				return nil, err
			}
			return get(ctx, inv, q, "usage")
		},
	})
	r.Add(&cli.Command{
		Path:       []string{"anomaly", "list"},
		Summary:    "Page through replies that failed or were interrupted (including those interrupted by a server restart), newest first; use agent timeline to see the run record of one of them",
		Identities: externalOnly,
		Network:    true,
		Retry:      true,
		Flags: []cli.Flag{
			{Name: "from", Type: cli.TypeTime, Description: "Only list replies that ended after this time, RFC 3339; unbounded when not given"},
			cursorFlag, limitFlag,
			scopeFlags[0], scopeFlags[1],
		},
		Constraints: []cli.Constraint{scopeOptional},
		Output:      "{items: Anomaly[], total, nextCursor}；total 为 from 之后符合条件的总条数；每项带 scope 与 projectId",
		Run: func(ctx context.Context, inv *cli.Invocation) (any, error) {
			q := pageQuery(inv)
			if inv.Has("from") {
				q.Set("from", inv.String("from"))
			}
			if err := scopeQuery(ctx, inv, q); err != nil {
				return nil, err
			}
			return get(ctx, inv, q, "anomalies")
		},
	})
	r.Add(&cli.Command{
		Path:       []string{"health"},
		Summary:    "Check whether the server's local database is healthy; when it is not, fails with server_unhealthy and details holds the health status",
		Identities: externalOnly,
		Network:    true,
		Retry:      true,
		Output:     "Health（对外端口只返回 database.ok，不含错误原文与 WAL 信息）",
		Run: func(ctx context.Context, inv *cli.Invocation) (any, error) {
			data, err := get(ctx, inv, nil, "health")
			var e *output.Error
			if errors.As(err, &e) && e.HTTPStatus == 503 {
				return nil, unhealthy(e)
			}
			return data, err
		},
	})
}

// unhealthy 把 /health 的 503 响应转为 server_unhealthy，并在说明中列出不可用的依赖。
func unhealthy(e *output.Error) *output.Error {
	var h map[string]json.RawMessage
	if json.Unmarshal(e.Details, &h) != nil {
		return e
	}
	var down []string
	for _, dep := range []string{"database"} {
		var d struct {
			OK bool `json:"ok"`
		}
		if raw, ok := h[dep]; !ok || json.Unmarshal(raw, &d) != nil {
			return e
		} else if !d.OK {
			down = append(down, dep)
		}
	}
	out := output.Errorf(output.CodeServerUnhealthy, "服务端依赖不可用：%s", strings.Join(down, "、"))
	out.HTTPStatus, out.Details = e.HTTPStatus, e.Details
	return out
}
