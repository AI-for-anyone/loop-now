package commands

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"

	"loop-now/internal/loopctl/cli"
	"loop-now/internal/loopctl/conn"
	"loop-now/internal/loopctl/output"
)

var (
	cursorFlag = cli.Flag{Name: "cursor", Type: cli.TypeString, Description: "上一页结果中的 nextCursor，用于取下一页"}
	limitFlag  = cli.Flag{Name: "limit", Type: cli.TypeInt, Min: intp(1), Description: "每页条数；默认值与上限由服务端确定"}
	textFlags  = []cli.Flag{
		{Name: "text", Type: cli.TypeString, Description: "正文；内容较敏感时改用 --text-file -，避免出现在进程列表中"},
		{Name: "text-file", Type: cli.TypePathOrStdin, Description: "从文件读取正文，- 表示标准输入"},
		{Name: "when-busy", Type: cli.TypeEnum, Values: []string{"queue", "interrupt"}, Description: "收件 Agent 正忙时：queue 排队，interrupt 立即终止它当前的执行；不指定时按排队处理；收件人是人时不起作用"},
	}
	questionFlags = []cli.Flag{
		{Name: "option", Type: cli.TypeString, Repeatable: true, Description: "提问选项文字，可重复且按顺序传递；个数/长度/重复由服务端校验"},
		{Name: "node-run", Type: cli.TypeString, Description: "任务实例提出新题时显式提供当前节点运行UUID（随 --option）；普通身份不传，不自动读取节点代填"},
	}
	inboxRunFlag   = cli.Flag{Name: "run", Type: cli.TypeString, Description: "严格任务节点Inbox读/回复及专用恢复节点提问所需的本轮运行UUID，取平台本轮prompt；不自动GET或替换"}
	textConstraint = []cli.Constraint{{Kind: cli.ExactlyOne, Flags: []string{"text", "text-file"}}}
)

func pageQuery(inv *cli.Invocation) url.Values {
	q := url.Values{}
	if inv.Has("cursor") {
		q.Set("cursor", inv.String("cursor"))
	}
	if inv.Has("limit") {
		q.Set("limit", strconv.Itoa(inv.Int("limit")))
	}
	return q
}

// messageBody 组装发送与回复的请求体；不带 --when-busy 时请求中不带 whenBusy。
func messageBody(inv *cli.Invocation, to string) (map[string]any, error) {
	body := map[string]any{}
	if err := taskRun(inv); err != nil {
		return nil, err
	}
	if id, present := explicitTaskRun(inv); present {
		body["runId"] = id
	}
	if inv.Has("text") || inv.Has("text-file") {
		t, err := text(inv)
		if err != nil {
			return nil, err
		}
		body["text"] = t
	}
	options := inv.Strings("option")
	if len(options) > 0 {
		body["options"] = options
	}
	if inv.Has("answer") {
		body["optionId"] = inv.String("answer")
	}
	if inv.Has("node-run") {
		node := inv.String("node-run")
		if len(options) == 0 || !IsUUID(node) {
			return nil, output.Errorf(output.CodeInvalidArgument, "--node-run 必须为 UUID，且仅随 --option 提出新题时提供；不自动读取当前节点")
		}
		body["nodeRunId"] = node
	}
	if to != "" {
		body["to"] = to
	}
	if inv.Has("when-busy") {
		body["whenBusy"] = inv.String("when-busy")
	}
	return body, nil
}

func addInbox(r *cli.Registry) {
	r.Add(&cli.Command{
		Path:       []string{"inbox", "list"},
		Summary:    "列出自己收件箱中的消息摘要，新的在前，标出是否已读；不改变已读状态",
		Identities: both,
		Network:    true,
		Retry:      true,
		Flags: []cli.Flag{
			{Name: "unread", Type: cli.TypeBool, Description: "只列未读消息"},
			cursorFlag, limitFlag,
		},
		Output: "{messages: InboxMessageSummary[], nextCursor}",
		Run: func(ctx context.Context, inv *cli.Invocation) (any, error) {
			q := pageQuery(inv)
			if inv.Bool("unread") {
				q.Set("unread", "true")
			}
			return get(ctx, inv, q, "inbox", "messages")
		},
	})
	r.Add(&cli.Command{
		Path:       []string{"inbox", "read"},
		Summary:    "读取一条消息并标已读；严格任务节点消息显式 --run，重复读取保持首次已读时间",
		Flags:      []cli.Flag{inboxRunFlag},
		Identities: both,
		Network:    true,
		Retry:      true,
		Args:       []cli.Arg{{Name: "messageId", Description: "消息 ID，取自 inbox list 的结果"}},
		Output:     "InboxMessage",
		Run: func(ctx context.Context, inv *cli.Invocation) (any, error) {
			// 已读动作幂等；严格消息始终保留调用者提供的原run。
			var body []byte
			contentType := ""
			if inv.Has("run") {
				v, e := messageBody(inv, "")
				if e != nil {
					return nil, e
				}
				body, _ = json.Marshal(v)
				contentType = "application/json"
			}
			return call(ctx, inv, conn.Request{Method: http.MethodPost, Path: []string{"inbox", "messages", inv.Args[0], "read"}, Body: body, ContentType: contentType, Retry: true})
		},
	})
	r.Add(&cli.Command{
		Path:        []string{"inbox", "send"},
		Summary:     "向一个地址发送消息，每次发送都开启一条新线索；超时后消息可能已经送达，不会自动重发",
		Identities:  both,
		Network:     true,
		Stdin:       true,
		Args:        []cli.Arg{{Name: "address", Description: "收件地址：Agent 写 组织名/项目名/Agent名（同组织可写 项目名/Agent名，任务内的 Agent 在后面加 #任务ID）；平台持有的 Agent 直接写名称；控制台操作者写 @console；外部身份写 @<身份名>"}},
		Flags:       append(append(append([]cli.Flag(nil), textFlags...), questionFlags...), inboxRunFlag, taskRunIDFlag),
		Constraints: textConstraint,
		Output:      "InboxMessage",
		Run: func(ctx context.Context, inv *cli.Invocation) (any, error) {
			body, err := messageBody(inv, inv.Args[0])
			if err != nil {
				return nil, err
			}
			return send(ctx, inv, http.MethodPost, body, "inbox", "messages")
		},
	})
	r.Add(&cli.Command{
		Path:        []string{"inbox", "reply"},
		Summary:     "在一条消息所在的线索中回复，收件人是线索中的另一方；超时后回复可能已经送达，不会自动重发",
		Identities:  both,
		Network:     true,
		Stdin:       true,
		Args:        []cli.Arg{{Name: "messageId", Description: "线索中任意一条消息的 ID"}},
		Flags:       append(append(append([]cli.Flag(nil), textFlags...), questionFlags...), inboxRunFlag, cli.Flag{Name: "answer", Type: cli.TypeString, Description: "原题选项ID；带此选项可省正文，也可同时附正文或新提问选项"}),
		Constraints: []cli.Constraint{{Kind: cli.AtMostOne, Flags: []string{"text", "text-file"}}, {Kind: cli.AtLeastOne, Flags: []string{"text", "text-file", "answer"}, Hint: "没有 --answer 时需要 --text 或 --text-file"}},
		Output:      "InboxMessage",
		Run: func(ctx context.Context, inv *cli.Invocation) (any, error) {
			body, err := messageBody(inv, "")
			if err != nil {
				return nil, err
			}
			return send(ctx, inv, http.MethodPost, body, "inbox", "messages", inv.Args[0], "reply")
		},
	})
	r.Add(&cli.Command{
		Path:       []string{"inbox", "threads"},
		Summary:    "列出自己参与的线索及未读数量；不改变已读状态",
		Identities: externalOnly,
		Network:    true,
		Retry:      true,
		Flags:      []cli.Flag{cursorFlag, limitFlag},
		Output:     "{threads: InboxThreadSummary[], unreadTotal, nextCursor}",
		Run: func(ctx context.Context, inv *cli.Invocation) (any, error) {
			return get(ctx, inv, pageQuery(inv), "inbox", "threads")
		},
	})
	r.Add(&cli.Command{
		Path:       []string{"inbox", "thread"},
		Summary:    "按顺序查看一条线索中的全部消息；不改变已读状态",
		Identities: externalOnly,
		Network:    true,
		Retry:      true,
		Args:       []cli.Arg{{Name: "threadId", Description: "线索 ID"}},
		Output:     "{threadId, messages: InboxMessage[]}",
		Run: func(ctx context.Context, inv *cli.Invocation) (any, error) {
			return get(ctx, inv, nil, "inbox", "threads", inv.Args[0])
		},
	})
}
