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
		Summary:    "按 Agent 与模型统计时间段内的 token 用量，按总数降序；只统计 token，不含费用",
		Identities: externalOnly,
		Network:    true,
		Retry:      true,
		Flags: []cli.Flag{
			{Name: "from", Type: cli.TypeTime, Description: "起始时间（含），RFC 3339；不指定时不限"},
			{Name: "to", Type: cli.TypeTime, Description: "结束时间（不含），RFC 3339；不指定时到当前"},
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
		Summary:    "分页列出失败或被打断的回复（含服务端重启中断），新的在前；用 agent timeline 查看其中一条的运行记录",
		Identities: externalOnly,
		Network:    true,
		Retry:      true,
		Flags: []cli.Flag{
			{Name: "from", Type: cli.TypeTime, Description: "只列这个时间之后结束的回复，RFC 3339；不指定时不限"},
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
		Summary:    "查看服务端依赖（PostgreSQL、Redis）是否正常；不正常时以 server_unhealthy 失败，details 为各依赖的状态",
		Identities: externalOnly,
		Network:    true,
		Retry:      true,
		Output:     "Health（对外端口只返回各依赖的 ok，不含错误原文）",
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
	for _, dep := range []string{"postgres", "redis"} {
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
