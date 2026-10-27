// Package inbox 实现 Inbox：Agent、控制台操作者与外部身份之间的留言。
// 包括收件地址的解析、跨组织发送权限、发送与回复、列表与读取、线索，以及收件箱事件流。
// 发给 Agent 的消息由对话服务在同一事务中安排平台提醒。
package inbox

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/apperr"
	"loop-now/internal/server/auth"
	"loop-now/internal/server/coord"
	"loop-now/internal/server/naming"
	"loop-now/internal/server/store"
)

// Options 是 Inbox 的参数。
type Options struct {
	QuestionTTL  time.Duration
	MaxMessage   int64 // 正文的字节上限
	PreviewChars int   // 摘要中正文的字符数
	PageDefault  int   // 每页默认条数
	PageMax      int   // 每页条数上限
	DefaultOrgID uuid.UUID
}

// Deliverer 在一个事务中写入发给 Agent 的消息并安排平台提醒（由对话服务实现）；
// InboxRead 在收件 Agent 读取消息后推送排队列表的变化。
type Deliverer interface {
	DeliverInbox(ctx context.Context, msg store.NewInboxMessage) error
	InboxRead(ctx context.Context, eff store.InboxReadEffect)
}

// Service 是 Inbox 服务。
type TaskRouter interface {
	ResolveTaskRecipient(context.Context, uuid.UUID, uuid.UUID) (store.Party, bool, error)
	DeliverInbox(context.Context, store.NewInboxMessage) error
}
type Service struct {
	Tasks TaskRouter
	St    *store.Store
	Co    *coord.Coord
	Conv  Deliverer
	O     Options
	Log   *slog.Logger
}

// Party 返回身份对应的参与方。
func Party(p auth.Principal) store.Party {
	switch p.Kind {
	case auth.KindAgent:
		return store.Party{Kind: "agent", ID: p.ID, RuntimeID: p.RuntimeID}
	case auth.KindExternal:
		return store.IdentityParty(p.ID)
	case auth.KindPlugin:
		return store.PluginParty(p.Plugin)
	}
	if p.Kind == auth.KindConsole {
		return store.ConsoleParty
	}
	return store.Party{Kind: "invalid"}
}

// ---------- 地址与权限 ----------

// Participant 是判断跨组织发送权限所需的信息。
type Participant struct {
	Kind           auth.Kind
	OrgID          *uuid.UUID // Agent 所属项目的组织；平台持有的 Agent、控制台操作者与外部身份为空
	PlatformHeld   bool       // 平台持有的 Agent
	ProjectManager bool       // 担任项目 Manager 的 Agent
}

func agentParticipant(a store.Agent) Participant {
	return Participant{Kind: auth.KindAgent, OrgID: a.OrgID, PlatformHeld: a.ProjectID == nil, ProjectManager: a.IsManager}
}

// AuthorizeSend 判断 from 能否向 to 发送新消息：
//   - 回复（含在线索中追加）不受限制；
//   - 控制台操作者与外部身份属于平台，可以发给任何组织的 Agent；发给人的消息不受限制；
//   - 平台持有的 Agent 可以发给任何组织；组织内的 Agent 可以发给同组织的 Agent 与平台持有的 Agent；
//   - 项目 Manager 还可以发给其他组织的项目 Manager。
//
// 不允许时返回 forbidden。
func AuthorizeSend(from, to Participant, isReply bool) error {
	if isReply || from.Kind != auth.KindAgent || to.Kind != auth.KindAgent {
		return nil
	}
	switch {
	case from.PlatformHeld, to.PlatformHeld:
		return nil
	case from.OrgID != nil && to.OrgID != nil && *from.OrgID == *to.OrgID:
		return nil
	case from.ProjectManager && to.ProjectManager:
		return nil
	}
	return apperr.Forbidden("不能向其他组织的 Agent 发送新消息（只有项目 Manager 之间可以）")
}

// recipient 是解析后的收件人。
type recipient struct {
	taskID    uuid.UUID
	party     store.Party
	forwarded bool
	agent     *store.Agent // 收件人是 Agent 时
}

func notFoundRecipient() error { return apperr.NotFound("收件人") }

// resolve 按发件人解析收件地址；sender 为发件 Agent（其他身份为空），两段地址按它所在的组织解析。
func (s *Service) resolve(ctx context.Context, sender *store.Agent, addr string) (recipient, error) {
	if addr == "" {
		return recipient{}, apperr.Invalid("to", "收件地址不能为空")
	}
	if name, ok := strings.CutPrefix(addr, "@"); ok {
		if name == "console" {
			return recipient{party: store.ConsoleParty}, nil
		}
		x, err := s.St.GetLiveExternalIdentityByName(ctx, name)
		if err != nil {
			return recipient{}, notFoundAs(err)
		}
		return recipient{party: store.IdentityParty(x.ID)}, nil
	}
	if base, task, ok := strings.Cut(addr, "#"); ok {
		if s.Tasks == nil {
			return recipient{}, notFoundRecipient()
		}
		id, e := uuid.Parse(task)
		if e != nil {
			return recipient{}, notFoundRecipient()
		}
		a, e := s.findAgent(ctx, sender, base)
		if e != nil {
			return recipient{}, notFoundAs(e)
		}
		party, forwarded, e := s.Tasks.ResolveTaskRecipient(ctx, a.ID, id)
		if e != nil {
			return recipient{}, e
		}
		return recipient{party: party, agent: &a, forwarded: forwarded, taskID: id}, nil
	}
	a, err := s.findAgent(ctx, sender, addr)
	if err != nil {
		return recipient{}, notFoundAs(err)
	}
	return recipient{party: store.AgentParty(a.ID), agent: &a}, nil
}

// findAgent 按地址查找可寻址的 Agent（不含 `@` 开头与带 `#` 的地址）。
func (s *Service) findAgent(ctx context.Context, sender *store.Agent, addr string) (store.Agent, error) {
	segs := strings.Split(addr, "/")
	switch len(segs) {
	case 3:
		return s.St.FindAgentByPath(ctx, segs[0], nil, segs[1], segs[2])
	case 2:
		org := s.O.DefaultOrgID
		if sender != nil && sender.OrgID != nil {
			org = *sender.OrgID
		}
		return s.St.FindAgentByPath(ctx, "", &org, segs[0], segs[1])
	case 1:
		if naming.IsUUIDForm(addr) {
			id, err := uuid.Parse(addr)
			if err != nil {
				return store.Agent{}, notFoundRecipient()
			}
			return s.St.GetAddressableAgent(ctx, id)
		}
		return s.St.FindPlatformAgent(ctx, addr)
	}
	return store.Agent{}, notFoundRecipient()
}

func notFoundAs(err error) error {
	if store.IsNotFound(err) {
		return notFoundRecipient()
	}
	return err
}

// FindAgent 按地址查找 Agent（控制台与外部身份调用，两段地址按默认组织解析）；找不到时返回 nil。
func (s *Service) FindAgent(ctx context.Context, addr string) (*store.Agent, error) {
	if addr == "" || strings.HasPrefix(addr, "@") || strings.Contains(addr, "#") {
		return nil, nil
	}
	a, err := s.findAgent(ctx, nil, addr)
	if store.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &a, nil
}

// sender 返回发件身份的参与方；Agent 必须仍可寻址。
func (s *Service) sender(ctx context.Context, p auth.Principal) (Participant, *store.Agent, error) {
	if p.Kind != auth.KindAgent {
		return Participant{Kind: p.Kind}, nil, nil
	}
	a, err := s.St.GetAddressableAgent(ctx, p.ID)
	if err != nil {
		if store.IsNotFound(err) {
			return Participant{}, nil, apperr.Unauthenticated("Agent 已删除")
		}
		return Participant{}, nil, err
	}
	return agentParticipant(a), &a, nil
}

// ---------- 发送与回复 ----------

func (s *Service) checkText(text, whenBusy string) error {
	if strings.TrimSpace(text) == "" {
		return apperr.Invalid("text", "正文不能为空")
	}
	if int64(len(text)) > s.O.MaxMessage {
		return apperr.TooLarge("正文超过长度上限")
	}
	if whenBusy != "" && whenBusy != "interrupt" && whenBusy != "queue" {
		return apperr.Invalid("whenBusy", "只能是 interrupt 或 queue")
	}
	return nil
}

// Send 发送一条消息，开启新线索。
func (s *Service) Send(ctx context.Context, p auth.Principal, to, text, whenBusy string, extra ...QuestionInput) (store.InboxMessage, error) {
	if err := s.checkText(text, whenBusy); err != nil {
		return store.InboxMessage{}, err
	}
	input := QuestionInput{}
	if len(extra) > 0 {
		input = extra[0]
	}
	if p.Kind != auth.KindPlugin && (input.Channel != "" || input.SenderName != "") {
		return store.InboxMessage{}, apperr.Invalid("channelId", "只有插件可以指定渠道与发件名称")
	}
	from, senderAgent, err := s.sender(ctx, p)
	if err != nil {
		return store.InboxMessage{}, err
	}
	rcpt, err := s.resolve(ctx, senderAgent, to)
	if err != nil {
		return store.InboxMessage{}, err
	}
	toP := Participant{Kind: auth.Kind(rcpt.party.Kind)}
	if rcpt.agent != nil {
		toP = agentParticipant(*rcpt.agent)
	}
	if err := AuthorizeSend(from, toP, false); err != nil {
		return store.InboxMessage{}, err
	}
	if rcpt.forwarded {
		text = "[原收件人：" + to + "]\n" + text
	}
	msg := store.NewInboxMessage{ID: uuid.Must(uuid.NewV7()), From: Party(p), To: rcpt.party, Text: text, WhenBusy: whenBusy}
	if p.Kind == auth.KindPlugin {
		if err := s.pluginSend(ctx, p, rcpt, &msg, input); err != nil {
			return store.InboxMessage{}, err
		}
	}
	if rcpt.forwarded {
		msg.ForwardTaskID = rcpt.taskID
	}
	if msg.To.Kind == "agent" && msg.WhenBusy == "" {
		msg.WhenBusy = "queue"
	}
	if len(extra) > 0 && extra[0].Options != nil {
		return s.questionDeliver(ctx, p, msg, extra[0])
	}
	return s.deliver(ctx, msg)
}

// pluginSend 检查插件发件（SPEC §11）：只能发给平台 Agent 或启用了该插件的项目里的 Agent，不能提问；
// 登记插件一方，并在消息上记下渠道与发件名称。
func (s *Service) pluginSend(ctx context.Context, p auth.Principal, rcpt recipient, msg *store.NewInboxMessage, input QuestionInput) error {
	if rcpt.agent == nil {
		return apperr.Forbidden("插件只能发给 Agent")
	}
	if input.Options != nil {
		return apperr.Invalid("options", "插件不能发出问题")
	}
	if project := rcpt.agent.ProjectID; project != nil {
		on, err := s.St.PluginEnabledIn(ctx, p.Plugin, *project)
		if err != nil {
			return err
		}
		if !on {
			return apperr.Forbidden("插件没有在收件 Agent 的项目里启用")
		}
	}
	from, err := s.St.EnsurePluginParty(ctx, p.Plugin)
	if err != nil {
		return err
	}
	msg.From, msg.PluginChannel, msg.PluginSender = from, input.Channel, input.SenderName
	return nil
}

// pluginSide 返回消息中插件一方的渠道与发件名称；线索里的后续消息沿用它们。与插件无关时为空。
func pluginSide(m store.InboxMessage) (channel, name string) {
	for _, v := range []store.PartyView{m.From, m.To} {
		if v.Kind == "plugin" {
			return v.ChannelID, v.Name
		}
	}
	return "", ""
}

// Reply 回复线索中的一条消息：收件人是线索中的另一方，不检查跨组织发送权限。
// 当前身份不是线索的一方时返回 not_found；插件令牌为 forbidden。
func (s *Service) Reply(ctx context.Context, p auth.Principal, messageID uuid.UUID, text, whenBusy string, extra ...QuestionInput) (store.InboxMessage, error) {
	input := QuestionInput{ReplyTo: messageID}
	if len(extra) > 0 {
		input = extra[0]
		input.ReplyTo = messageID
	}
	check := text
	if input.OptionID != nil && check == "" {
		check = "选择"
	}
	if err := s.checkText(check, whenBusy); err != nil {
		return store.InboxMessage{}, err
	}
	if _, _, err := s.sender(ctx, p); err != nil {
		return store.InboxMessage{}, err
	}
	orig, err := s.St.GetInboxMessage(ctx, messageID, s.O.PreviewChars)
	if err != nil {
		return store.InboxMessage{}, err
	}
	me := Party(p)
	if _, strict, e := s.St.TaskNodeInboxBinding(ctx, messageID); e != nil {
		return store.InboxMessage{}, e
	} else if strict {
		if input.Options != nil || input.OptionID != nil || input.NodeRunID != uuid.Nil {
			return store.InboxMessage{}, apperr.Invalid("body", "严格节点普通Inbox回复不接受原题答案或新题字段")
		}
		m, _, e := s.St.ReplyTaskNodeInbox(ctx, me, messageID, input.RunID, text, whenBusy)
		if e == nil {
			s.emitCreated(ctx, m)
			if m.To.RuntimeID != uuid.Nil {
				if bridge, ok := s.Tasks.(interface {
					InboxCommitted(context.Context, uuid.UUID)
				}); ok {
					bridge.InboxCommitted(ctx, m.To.RuntimeID)
				}
			}
		}
		return m, e
	}
	a, b, err := s.St.ThreadParties(ctx, orig.ThreadID)
	if err != nil {
		return store.InboxMessage{}, err
	}
	var other store.Party
	switch me {
	case a:
		other = b
	case b:
		other = a
	default:
		if me.Kind == "plugin" {
			return store.InboxMessage{}, apperr.Forbidden("消息不在这个插件的线索里")
		}
		return store.InboxMessage{}, apperr.NotFound("消息")
	}
	if other.Kind == "platform" {
		return store.InboxMessage{}, apperr.Forbidden("平台通知不接受回复")
	}
	// 插件发来的线索里 Agent 可以提问，插件回复即作答（SPEC §11）；插件自己不能提问。
	if me.Kind == "plugin" && input.Options != nil {
		return store.InboxMessage{}, apperr.Invalid("options", "插件不能发出问题")
	}
	channel, sender := pluginSide(orig)
	thread := orig.ThreadID
	if other.RuntimeID != uuid.Nil && s.Tasks != nil {
		inst, e := s.St.TaskInstance(ctx, other.RuntimeID)
		if e != nil {
			return store.InboxMessage{}, e
		}
		target, forwarded, e := s.Tasks.ResolveTaskRecipient(ctx, other.ID, inst.TaskID)
		if e != nil {
			return store.InboxMessage{}, e
		}
		if forwarded {
			msg := store.NewInboxMessage{ID: uuid.New(), From: me, To: target, Text: "[原收件人：" + other.ID.String() + "#" + inst.TaskID.String() + "]\n" + text, WhenBusy: whenBusy,
				PluginChannel: channel, PluginSender: sender}
			msg.ForwardTaskID = inst.TaskID
			if msg.WhenBusy == "" {
				msg.WhenBusy = "queue"
			}
			if orig.Question != nil || input.Options != nil || input.OptionID != nil {
				return s.questionDeliver(ctx, p, msg, input)
			}
			return s.deliver(ctx, msg)
		}
	}
	msg := store.NewInboxMessage{ID: uuid.New(), ThreadID: &thread, InReplyTo: &messageID, From: me, To: other, Text: text, WhenBusy: whenBusy,
		PluginChannel: channel, PluginSender: sender}
	if msg.To.Kind == "agent" && msg.WhenBusy == "" {
		msg.WhenBusy = "queue"
	}
	if orig.Question != nil || input.Options != nil || input.OptionID != nil {
		return s.questionDeliver(ctx, p, msg, input)
	}
	return s.deliver(ctx, msg)
}

// deliver 写入消息：发给 Agent 时由对话服务一并安排提醒（whenBusy 默认 queue）；发给人时 whenBusy 记为空。
// 提交后追加收件箱事件，返回保存的消息。
func (s *Service) deliver(ctx context.Context, msg store.NewInboxMessage) (store.InboxMessage, error) {
	if msg.ForwardTaskID != uuid.Nil {
		return s.questionDeliver(ctx, auth.Principal{Kind: auth.Kind(msg.From.Kind), ID: msg.From.ID, RuntimeID: msg.From.RuntimeID}, msg, QuestionInput{})
	}
	var err error
	if msg.To.Kind == "agent" {
		if msg.WhenBusy == "" {
			msg.WhenBusy = "queue"
		}
		if msg.To.RuntimeID != uuid.Nil {
			if s.Tasks == nil {
				return store.InboxMessage{}, notFoundRecipient()
			}
			err = s.Tasks.DeliverInbox(ctx, msg)
		} else {
			err = s.Conv.DeliverInbox(ctx, msg)
		}
	} else {
		msg.WhenBusy = ""
		err = s.St.InboxTx(ctx, func(tx pgx.Tx) error { return store.InsertInboxMessage(ctx, tx, msg) })
	}
	if apperr.Is(err, apperr.CodeTaskConflict) && msg.To.RuntimeID != uuid.Nil && s.Tasks != nil {
		inst, e := s.St.TaskInstance(ctx, msg.To.RuntimeID)
		if e != nil {
			return store.InboxMessage{}, e
		}
		target, forwarded, e := s.Tasks.ResolveTaskRecipient(ctx, msg.To.ID, inst.TaskID)
		if e != nil {
			return store.InboxMessage{}, e
		}
		if forwarded {
			original := msg.To.ID.String() + "#" + inst.TaskID.String()
			if a, e := s.St.GetAddressableAgent(ctx, msg.To.ID); e == nil {
				if addr, ok := a.Address(); ok {
					original = addr + "#" + inst.TaskID.String()
				}
			}
			msg.To = target
			msg.ForwardTaskID = inst.TaskID
			msg.ThreadID = nil
			msg.InReplyTo = nil
			msg.Text = "[原收件人：" + original + "]\n" + msg.Text
			return s.deliver(ctx, msg)
		}
	}
	if err != nil {
		return store.InboxMessage{}, err
	}
	saved, err := s.St.GetInboxMessage(ctx, msg.ID, s.O.PreviewChars)
	if err != nil {
		return store.InboxMessage{}, err
	}
	s.emitCreated(ctx, saved)
	return saved, nil
}

// ---------- 列表、读取与线索 ----------

// Query 返回按参数整理后的列表条件：limit 为 0 时取默认值，超过上限时取上限。
func (s *Service) Query(unread bool, cursor string, limit int) (store.InboxListQuery, error) {
	c, err := store.DecodeInboxCursor(cursor)
	if err != nil {
		return store.InboxListQuery{}, err
	}
	switch {
	case limit == 0:
		limit = s.O.PageDefault
	case limit < 0:
		return store.InboxListQuery{}, apperr.Invalid("limit", "必须至少为 1")
	case limit > s.O.PageMax:
		limit = s.O.PageMax
	}
	return store.InboxListQuery{UnreadOnly: unread, Cursor: c, Limit: limit, Preview: s.O.PreviewChars}, nil
}

// List 列出发给当前身份的消息摘要，不改变已读状态。收件 Agent 看不到被人取消的消息的正文。
func (s *Service) List(ctx context.Context, p auth.Principal, q store.InboxListQuery) (store.InboxPage, error) {
	page, err := s.St.ListInboxReceived(ctx, Party(p), q)
	for i := range page.Messages {
		store.RedactCancelledFor(Party(p), &page.Messages[i])
	}
	return page, err
}

// Read 读取发给当前身份的一条消息并标为已读；重复读取不报错，已读时间保持第一次的值。
func (s *Service) Read(ctx context.Context, p auth.Principal, id uuid.UUID, runs ...uuid.UUID) (store.InboxMessage, error) {
	var run uuid.UUID
	if len(runs) > 0 {
		run = runs[0]
	}
	if m, handled, changed, e := s.St.ReadTaskNodeInbox(ctx, Party(p), id, run); handled || e != nil {
		if e == nil && changed {
			s.emitRead(ctx, m)
		}
		store.RedactCancelledFor(Party(p), &m)
		return m, e
	}
	changed, eff, err := s.St.MarkInboxRead(ctx, id, Party(p))
	if err != nil {
		return store.InboxMessage{}, err
	}
	s.Conv.InboxRead(ctx, eff)
	m, err := s.St.GetInboxMessage(ctx, id, s.O.PreviewChars)
	if err != nil {
		return store.InboxMessage{}, err
	}
	if changed {
		s.emitRead(ctx, m)
	}
	store.RedactCancelledFor(Party(p), &m)
	return m, nil
}

// Threads 列出当前身份参与的线索，并给出它的未读总数。
func (s *Service) Threads(ctx context.Context, p auth.Principal, cursor string, limit int) (store.InboxThreadPage, int, error) {
	q, err := s.Query(false, cursor, limit)
	if err != nil {
		return store.InboxThreadPage{}, 0, err
	}
	page, err := s.St.ListInboxThreads(ctx, Party(p), q.Cursor, q.Limit, q.Preview)
	if err != nil {
		return page, 0, err
	}
	total, err := s.St.CountInboxUnread(ctx, Party(p))
	return page, total, err
}

// checkThread 确认当前身份是线索的一方；否则返回 not_found。
func (s *Service) checkThread(ctx context.Context, p auth.Principal, threadID uuid.UUID) error {
	a, b, err := s.St.ThreadParties(ctx, threadID)
	if err != nil {
		return err
	}
	if me := Party(p); me != a && me != b {
		return apperr.NotFound("线索")
	}
	return nil
}

// Thread 返回线索中的全部消息，不改变已读状态。
func (s *Service) Thread(ctx context.Context, p auth.Principal, threadID uuid.UUID, runs ...uuid.UUID) ([]store.InboxMessage, error) {
	var run uuid.UUID
	if len(runs) > 0 {
		run = runs[0]
	}
	rows, handled, e := s.St.TaskNodeInboxThread(ctx, Party(p), threadID, run, false)
	if !handled && e == nil {
		if e = s.checkThread(ctx, p, threadID); e == nil {
			rows, e = s.St.ListThreadMessages(ctx, threadID, s.O.PreviewChars)
		}
	}
	for i := range rows {
		store.RedactCancelledFor(Party(p), &rows[i])
	}
	return rows, e
}

// ReadThread 把线索中发给当前身份的消息全部标为已读，返回当前身份最新的未读总数。
func (s *Service) ReadThread(ctx context.Context, p auth.Principal, threadID uuid.UUID, runs ...uuid.UUID) (int, error) {
	var run uuid.UUID
	if len(runs) > 0 {
		run = runs[0]
	}
	if rows, handled, e := s.St.TaskNodeInboxThread(ctx, Party(p), threadID, run, true); handled || e != nil {
		if e != nil {
			return 0, e
		}
		for _, m := range rows {
			if m.To.Party == Party(p) {
				s.emitRead(ctx, m)
			}
		}
		return s.St.CountInboxUnread(ctx, Party(p))
	}
	if err := s.checkThread(ctx, p, threadID); err != nil {
		return 0, err
	}
	receipts, eff, err := s.St.MarkThreadRead(ctx, threadID, Party(p))
	if err != nil {
		return 0, err
	}
	s.Conv.InboxRead(ctx, eff)
	for _, r := range receipts {
		if m, err := s.St.GetInboxMessage(ctx, r.ID, s.O.PreviewChars); err == nil {
			s.emitRead(ctx, m)
		}
	}
	return s.St.CountInboxUnread(ctx, Party(p))
}

// ---------- 控制台的只读查看 ----------

// AgentInbox 列出 Agent 收到的消息（只读）；Agent 不存在或已删除时返回 not_found。
func (s *Service) AgentInbox(ctx context.Context, agentID uuid.UUID, q store.InboxListQuery) (store.InboxPage, error) {
	if _, err := s.St.GetAgent(ctx, agentID); err != nil {
		return store.InboxPage{}, err
	}
	return s.St.ListInboxReceived(ctx, store.AgentParty(agentID), q)
}

// ConsoleMessages 列出控制台与常态 Agent 之间两个方向的消息全文（只读）；Agent 不存在时返回 not_found，已删除的仍可查看。
func (s *Service) ConsoleMessages(ctx context.Context, agentID uuid.UUID, q store.InboxListQuery) (store.InboxPage, error) {
	if _, err := s.St.GetAgent(ctx, agentID); err != nil {
		return store.InboxPage{}, err
	}
	return s.St.ListConsoleConversation(ctx, agentID, q)
}

// Pending 返回控制台待处理的 open 问题与其他未读消息。
func (s *Service) Pending(ctx context.Context) (questions, unread []store.InboxMessage, err error) {
	return s.St.ConsolePending(ctx, s.O.PreviewChars)
}

// AgentInboxMessage 返回 Agent 收到的一条消息的全文（只读）。
func (s *Service) AgentInboxMessage(ctx context.Context, agentID, messageID uuid.UUID) (store.InboxMessage, error) {
	if _, err := s.St.GetAgent(ctx, agentID); err != nil {
		return store.InboxMessage{}, err
	}
	m, err := s.St.GetInboxMessage(ctx, messageID, s.O.PreviewChars)
	if err != nil {
		return m, err
	}
	if m.To.Party != store.AgentParty(agentID) {
		return store.InboxMessage{}, apperr.NotFound("消息")
	}
	return m, nil
}

// IdentityInbox 列出外部身份收到与（或）发出的消息（只读）；已删除的身份也可以查看。
func (s *Service) IdentityInbox(ctx context.Context, identityID uuid.UUID, direction string, q store.InboxListQuery) (store.InboxPage, error) {
	switch direction {
	case "":
		direction = "all"
	case "all", "received", "sent":
	default:
		return store.InboxPage{}, apperr.Invalid("direction", "只能是 received、sent 或 all")
	}
	if _, err := s.St.GetExternalIdentity(ctx, identityID); err != nil {
		return store.InboxPage{}, err
	}
	return s.St.ListInboxOfIdentity(ctx, identityID, direction, q)
}

// IdentityInboxMessage 返回外部身份收到或发出的一条消息的全文（只读）。
func (s *Service) IdentityInboxMessage(ctx context.Context, identityID, messageID uuid.UUID) (store.InboxMessage, error) {
	if _, err := s.St.GetExternalIdentity(ctx, identityID); err != nil {
		return store.InboxMessage{}, err
	}
	m, err := s.St.GetInboxMessage(ctx, messageID, s.O.PreviewChars)
	if err != nil {
		return m, err
	}
	if p := store.IdentityParty(identityID); m.To.Party != p && m.From.Party != p {
		return store.InboxMessage{}, apperr.NotFound("消息")
	}
	return m, nil
}

// ---------- 事件流 ----------

// emitCreated 与 emitRead 在数据库提交后追加收件箱事件；追加失败只记日志，不回滚。
func (s *Service) emitCreated(ctx context.Context, m store.InboxMessage) {
	s.append(ctx, apitypes.EventInboxCreated, func(unread int) any {
		return apitypes.SseInboxMessageCreated{Message: apitypes.FromInboxSummary(m), UnreadTotal: unread}
	})
}

func (s *Service) emitRead(ctx context.Context, m store.InboxMessage) {
	if m.ReadAt == nil {
		return
	}
	s.append(ctx, apitypes.EventInboxRead, func(unread int) any {
		return apitypes.SseInboxMessageRead{ID: m.ID, ThreadID: m.ThreadID, To: apitypes.FromParty(m.To), ReadAt: *m.ReadAt, UnreadTotal: unread}
	})
}

// EmitCancelled 在一条发给 Agent 的排队消息被人取消、事务提交后追加收件箱事件。
func (s *Service) EmitCancelled(ctx context.Context, m store.InboxMessage) {
	if m.CancelledAt == nil {
		return
	}
	s.append(ctx, apitypes.EventInboxCancelled, func(unread int) any {
		return apitypes.SseInboxMessageCancelled{ID: m.ID, ThreadID: m.ThreadID, From: apitypes.FromParty(m.From), To: apitypes.FromParty(m.To), CancelledAt: *m.CancelledAt, UnreadTotal: unread}
	})
}

func (s *Service) append(ctx context.Context, typ string, payload func(unread int) any) error {
	ctx = context.WithoutCancel(ctx) // 请求已结束也要写完事件
	err := s.St.WithInboxEventLock(ctx, func(unread int) error {
		data, err := json.Marshal(payload(unread))
		if err != nil {
			return err
		}
		_, err = s.Co.AppendInbox(ctx, typ, data)
		return err
	})
	if err != nil {
		s.Log.Warn("写入收件箱事件流失败", "type", typ, "err", err)
	}
	return err
}

// Snapshot 返回事件流的快照：控制台操作者的未读总数与事件流当时的最新位置，两者在同一把锁内取得。
func (s *Service) Snapshot(ctx context.Context) (apitypes.SseInboxSnapshot, string, error) {
	var snap apitypes.SseInboxSnapshot
	var last string
	err := s.St.WithInboxEventLock(ctx, func(unread int) error {
		snap.UnreadTotal = unread
		var err error
		last, err = s.Co.InboxLastID(ctx)
		return err
	})
	if err != nil {
		return snap, "", errors.Join(errors.New("读取收件箱快照失败"), err)
	}
	return snap, last, nil
}
