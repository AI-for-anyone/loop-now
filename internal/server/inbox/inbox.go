// Package inbox implements the Inbox: messages between Agents, console operators and external identities. It covers
// resolving recipient addresses, cross-organization send permissions, sending and replying, listing and reading,
// threads, and the Inbox event stream. For messages addressed to an Agent, the conversation service schedules a
// platform reminder in the same transaction.
package inbox

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"

	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/apperr"
	"loop-now/internal/server/auth"
	"loop-now/internal/server/coord"
	"loop-now/internal/server/db"
	"loop-now/internal/server/naming"
	"loop-now/internal/server/store"
)

// Options holds the Inbox parameters.
type Options struct {
	QuestionTTL  time.Duration
	MaxMessage   int64 // byte limit of the body
	PreviewChars int   // number of body characters in a summary
	PageDefault  int   // default number of items per page
	PageMax      int   // maximum number of items per page
	DefaultOrgID uuid.UUID
}

// Deliverer writes a message addressed to an Agent and schedules a platform reminder in one transaction (implemented by
// the conversation service); InboxRead pushes the change of the queue list after the recipient Agent has read messages.
type Deliverer interface {
	DeliverInbox(ctx context.Context, msg store.NewInboxMessage) error
	InboxRead(ctx context.Context, eff store.InboxReadEffect)
}

// TaskRouter resolves and delivers Inbox messages addressed to task instances (implemented by the task service).
// Service, declared right below it, is the Inbox service.
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

// Party returns the party that corresponds to the principal.
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

// ---------- Addresses and permissions ----------

// Participant is the information needed to decide cross-organization send permissions.
type Participant struct {
	Kind           auth.Kind
	OrgID          *uuid.UUID // org of the Agent's project; nil for platform-held Agents, console operators, external identities
	PlatformHeld   bool       // an Agent held by the platform
	ProjectManager bool       // an Agent that is a project Manager
}

func agentParticipant(a store.Agent) Participant {
	return Participant{Kind: auth.KindAgent, OrgID: a.OrgID, PlatformHeld: a.ProjectID == nil, ProjectManager: a.IsManager}
}

// AuthorizeSend decides whether from may send a new message to to:
//   - replies (including appending within a thread) are not restricted;
//   - console operators and external identities belong to the platform and may send to Agents of any organization;
//     messages to people are not restricted;
//   - platform-held Agents may send to any organization; Agents within an organization may send to Agents of the same
//     organization and to platform-held Agents;
//   - project Managers may also send to project Managers of other organizations.
//
// When not allowed it returns forbidden.
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

// recipient is a resolved recipient.
type recipient struct {
	taskID    uuid.UUID
	party     store.Party
	forwarded bool
	agent     *store.Agent // when the recipient is an Agent
}

func notFoundRecipient() error { return apperr.NotFound("收件人") }

// resolve resolves a recipient address for the sender; sender is the sending Agent (nil for other principals), and
// two-segment addresses are resolved within its organization.
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

// findAgent finds an addressable Agent by address (not for addresses starting with `@` or containing `#`).
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

// FindAgent finds an Agent by address (called for the console and external identities; two-segment addresses are
// resolved within the default organization); nil when not found.
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

// sender returns the party of the sending principal; an Agent must still be addressable.
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

// ---------- Sending and replying ----------

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

// Send sends a message that starts a new thread.
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
		text = "[Original recipient: " + to + "]\n" + text
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

// pluginSend checks a message sent by a plugin (SPEC §11): it may only go to a platform Agent or to an Agent in a
// project that has enabled the plugin, and it cannot ask a question; it registers the plugin party and records the
// channel and sender name on the message.
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

// pluginSide returns the channel and sender name of the plugin party in a message; later messages in the thread reuse
// them. Empty when no plugin is involved.
func pluginSide(m store.InboxMessage) (channel, name string) {
	for _, v := range []store.PartyView{m.From, m.To} {
		if v.Kind == "plugin" {
			return v.ChannelID, v.Name
		}
	}
	return "", ""
}

// Reply replies to a message in a thread: the recipient is the other party of the thread, and cross-organization send
// permissions are not checked. When the current principal is not a party of the thread it returns not_found; for a
// plugin token, forbidden.
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
	// In a thread started by a plugin, the Agent may ask questions and the plugin's reply answers them (SPEC §11); the
	// plugin itself cannot ask questions.
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
			msg := store.NewInboxMessage{ID: uuid.New(), From: me, To: target, Text: "[Original recipient: " + other.ID.String() + "#" + inst.TaskID.String() + "]\n" + text, WhenBusy: whenBusy,
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

// deliver writes the message: for a message to an Agent the conversation service also schedules the reminder (whenBusy
// defaults to queue); for a message to a person whenBusy is stored empty. After the commit it appends an Inbox event
// and returns the saved message.
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
		err = s.St.InboxTx(ctx, func(tx *db.Tx) error { return store.InsertInboxMessage(ctx, tx, msg) })
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
			msg.Text = "[Original recipient: " + original + "]\n" + msg.Text
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

// ---------- Listing, reading and threads ----------

// Query returns the list conditions normalized from the parameters: a limit of 0 takes the default, and a limit above
// the maximum takes the maximum.
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

// List lists summaries of messages addressed to the current principal without changing the read state. The recipient
// Agent cannot see the body of messages cancelled by a person.
func (s *Service) List(ctx context.Context, p auth.Principal, q store.InboxListQuery) (store.InboxPage, error) {
	page, err := s.St.ListInboxReceived(ctx, Party(p), q)
	for i := range page.Messages {
		store.RedactCancelledFor(Party(p), &page.Messages[i])
	}
	return page, err
}

// Read reads one message addressed to the current principal and marks it read; reading again is not an error, and the
// read time keeps its first value.
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

// Threads lists the threads the current principal takes part in, along with its total unread count.
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

// checkThread confirms that the current principal is a party of the thread; otherwise it returns not_found.
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

// Thread returns all messages in the thread without changing the read state.
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

// ReadThread marks all messages in the thread addressed to the current principal as read, and returns the principal's
// latest total unread count.
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

// ---------- Read-only views for the console ----------

// AgentInbox lists the messages an Agent received (read-only); not_found when the Agent does not exist or was deleted.
func (s *Service) AgentInbox(ctx context.Context, agentID uuid.UUID, q store.InboxListQuery) (store.InboxPage, error) {
	if _, err := s.St.GetAgent(ctx, agentID); err != nil {
		return store.InboxPage{}, err
	}
	return s.St.ListInboxReceived(ctx, store.AgentParty(agentID), q)
}

// ConsoleMessages lists the full text of messages in both directions between the console and a regular Agent
// (read-only); not_found when the Agent does not exist, while deleted Agents can still be viewed.
func (s *Service) ConsoleMessages(ctx context.Context, agentID uuid.UUID, q store.InboxListQuery) (store.InboxPage, error) {
	if _, err := s.St.GetAgent(ctx, agentID); err != nil {
		return store.InboxPage{}, err
	}
	return s.St.ListConsoleConversation(ctx, agentID, q)
}

// Pending returns the open questions and other unread messages waiting for the console.
func (s *Service) Pending(ctx context.Context) (questions, unread []store.InboxMessage, err error) {
	return s.St.ConsolePending(ctx, s.O.PreviewChars)
}

// AgentInboxMessage returns the full text of one message an Agent received (read-only).
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

// IdentityInbox lists the messages an external identity received and/or sent (read-only); deleted identities can be
// viewed too.
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

// IdentityInboxMessage returns the full text of one message an external identity received or sent (read-only).
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

// ---------- Event stream ----------

// emitCreated and emitRead append Inbox events after the database commit; a failed append is only logged and nothing is
// rolled back.
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

// EmitCancelled appends an Inbox event after a queued message addressed to an Agent has been cancelled by a person and
// the transaction has committed.
func (s *Service) EmitCancelled(ctx context.Context, m store.InboxMessage) {
	if m.CancelledAt == nil {
		return
	}
	s.append(ctx, apitypes.EventInboxCancelled, func(unread int) any {
		return apitypes.SseInboxMessageCancelled{ID: m.ID, ThreadID: m.ThreadID, From: apitypes.FromParty(m.From), To: apitypes.FromParty(m.To), CancelledAt: *m.CancelledAt, UnreadTotal: unread}
	})
}

func (s *Service) append(ctx context.Context, typ string, payload func(unread int) any) error {
	ctx = context.WithoutCancel(ctx) // the events must be written even if the request has ended
	err := s.St.WithInboxEventLock(ctx, func(unread int) error {
		data, err := json.Marshal(payload(unread))
		if err != nil {
			return err
		}
		_, err = s.Co.AppendInbox(ctx, typ, data)
		return err
	})
	if err != nil {
		s.Log.Warn("failed to write to the Inbox event stream", "type", typ, "err", err)
	}
	return err
}

// Snapshot returns the event stream snapshot: the console operator's total unread count and the stream's latest
// position at that moment, both taken under the same lock.
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
