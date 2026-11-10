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
	cursorFlag = cli.Flag{Name: "cursor", Type: cli.TypeString, Description: "nextCursor from the previous page's result, to get the next page"}
	limitFlag  = cli.Flag{Name: "limit", Type: cli.TypeInt, Min: intp(1), Description: "Items per page; the default and maximum are set by the server"}
	textFlags  = []cli.Flag{
		{Name: "text", Type: cli.TypeString, Description: "Message text; for sensitive content use --text-file - instead, so it does not appear in the process list"},
		{Name: "text-file", Type: cli.TypePathOrStdin, Description: "Read the text from a file; - means standard input"},
		{Name: "when-busy", Type: cli.TypeEnum, Values: []string{"queue", "interrupt"}, Description: "When the receiving Agent is busy: queue queues the message, interrupt ends its current execution immediately; queues when not given; has no effect when the recipient is a person"},
	}
	questionFlags = []cli.Flag{
		{Name: "option", Type: cli.TypeString, Repeatable: true, Description: "Question option text, repeatable and passed in order; count/length/duplicates are checked by the server"},
		{Name: "node-run", Type: cli.TypeString, Description: "When a task instance asks a new question, give the current node run UUID explicitly (with --option); ordinary identities do not pass it, and it is not read and filled in automatically"},
	}
	inboxRunFlag   = cli.Flag{Name: "run", Type: cli.TypeString, Description: "The run UUID for this turn, needed to read/reply to strict task node Inbox messages and for questions from the dedicated recovery node; take it from the platform's prompt for this turn; it is not fetched or replaced automatically"}
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
		Summary:    "List summaries of the messages in your own Inbox, newest first, marking whether each is read; does not change the read state",
		Identities: both,
		Network:    true,
		Retry:      true,
		Flags: []cli.Flag{
			{Name: "unread", Type: cli.TypeBool, Description: "List only unread messages"},
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
		Summary:    "Read a message and mark it read; strict task node messages need an explicit --run; reading again keeps the first read time",
		Flags:      []cli.Flag{inboxRunFlag},
		Identities: both,
		Network:    true,
		Retry:      true,
		Args:       []cli.Arg{{Name: "messageId", Description: "Message ID, from the result of inbox list"}},
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
		Summary:     "Send a message to an address; every send starts a new thread; after a timeout the message may already have been delivered, and it is not resent automatically",
		Identities:  both,
		Network:     true,
		Stdin:       true,
		Args:        []cli.Arg{{Name: "address", Description: "Recipient address: for an Agent, organization/project/Agent (project/Agent within the same organization; append #taskID for an Agent inside a task); for an Agent held by the platform, just its name; @console for the console operator; @<identity name> for an external identity"}},
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
		Summary:     "Reply in the thread of a message; the recipient is the other party of the thread; after a timeout the reply may already have been delivered, and it is not resent automatically",
		Identities:  both,
		Network:     true,
		Stdin:       true,
		Args:        []cli.Arg{{Name: "messageId", Description: "The ID of any message in the thread"}},
		Flags:       append(append(append([]cli.Flag(nil), textFlags...), questionFlags...), inboxRunFlag, cli.Flag{Name: "answer", Type: cli.TypeString, Description: "Option ID of the original question; with it the text can be omitted, or you can also add text or options for a new question"}),
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
		Summary:    "List the threads you take part in and their unread counts; does not change the read state",
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
		Summary:    "Show all messages of a thread in order; does not change the read state",
		Identities: externalOnly,
		Network:    true,
		Retry:      true,
		Args:       []cli.Arg{{Name: "threadId", Description: "Thread ID"}},
		Output:     "{threadId, messages: InboxMessage[]}",
		Run: func(ctx context.Context, inv *cli.Invocation) (any, error) {
			return get(ctx, inv, nil, "inbox", "threads", inv.Args[0])
		},
	})
}
