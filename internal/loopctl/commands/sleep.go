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
		Summary: "登记自身 sleep；成功后正常结束当前轮才生效，网络失败可能已登记，先查询状态",
		Flags: []cli.Flag{
			{Name: "run", Type: cli.TypeString, Required: true, Description: "平台本轮说明中的运行 UUID；不查询或替换为其他运行"},
			{Name: "seconds", Type: cli.TypeInt, Required: true, Min: intp(1), Max: intp(2147483647), Description: "等待秒数，1..2147483647；从当前轮成功收尾后开始计算"},
			taskActionFlag,
			{Name: "reason", Type: cli.TypeString, Description: "可选说明，沿用服务端消息文本长度上限"},
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
		Summary: "只读查询自身当前运行与有效 sleep；没有时为 null，不创建或重置 sleep",
		Retry:   true, Output: "SleepState",
		Run: func(ctx context.Context, inv *cli.Invocation) (any, error) {
			return get(ctx, inv, nil, "sleep")
		},
	})
}
