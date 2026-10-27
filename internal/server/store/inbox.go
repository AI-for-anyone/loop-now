package store

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"loop-now/internal/server/messageimage"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"loop-now/internal/server/apperr"
)

// ---------- 参与方 ----------

// Party 是 Inbox 消息的一方：Agent、控制台操作者、外部身份或插件。
type Party struct {
	RuntimeID uuid.UUID // 非零时是任务实例，不能读取常态或其他任务Inbox
	Kind      string    // agent / console / external / plugin
	ID        uuid.UUID // Agent ID、外部身份 ID 或插件一方（plugin_parties）的 ID；控制台操作者为 uuid.Nil
}

// pluginPartyNamespace 是插件一方 ID 的命名空间：每个插件对应一个固定的 ID。
var pluginPartyNamespace = uuid.MustParse("6f1c9a4e-5d2b-4e8a-9c3f-2b7d0e1a8c55")

// PluginParty 返回插件作为 Inbox 一方的参与方。
func PluginParty(plugin string) Party {
	return Party{Kind: "plugin", ID: uuid.NewSHA1(pluginPartyNamespace, []byte(plugin))}
}

// ConsoleParty 是控制台操作者。
var ConsoleParty = Party{Kind: "console"}

// AgentParty 与 IdentityParty 构造对应的参与方。
func AgentParty(id uuid.UUID) Party    { return Party{Kind: "agent", ID: id} }
func IdentityParty(id uuid.UUID) Party { return Party{Kind: "external", ID: id} }

// cond 返回“side 一方是 p”的 SQL 条件；side 为 from 或 to，arg 是 p.ID 的占位符序号（控制台不使用）。
func (p Party) cond(alias, side string, arg int) string {
	switch p.Kind {
	case "agent":
		runtime := fmt.Sprintf("%s.%s_runtime_id IS NULL", alias, side)
		if p.RuntimeID != uuid.Nil {
			runtime = fmt.Sprintf("%s.%s_runtime_id = '%s'::uuid", alias, side, p.RuntimeID.String())
		}
		return fmt.Sprintf("(%s.%s_kind = 'agent' AND %s.%s_agent_id = $%d AND %s AND %s)", alias, side, alias, side, arg, runtime, taskNodeInboxVisibleSQL(alias, p))
	case "external":
		return fmt.Sprintf("(%s.%s_kind = 'external' AND %s.%s_identity_id = $%d)", alias, side, alias, side, arg)
	case "plugin":
		return fmt.Sprintf("(%s.%s_kind = 'plugin' AND %s.%s_plugin_party_id = $%d)", alias, side, alias, side, arg)
	}
	if p.Kind == "console" {
		return fmt.Sprintf("(%s.%s_kind = 'console' AND $%d::uuid IS NOT NULL)", alias, side, arg)
	}
	return "false"
}

func (p Party) agentID() *uuid.UUID {
	if p.Kind == "agent" {
		return &p.ID
	}
	return nil
}

func (p Party) identityID() *uuid.UUID {
	if p.Kind == "external" {
		return &p.ID
	}
	return nil
}

func (p Party) pluginPartyID() *uuid.UUID {
	if p.Kind == "plugin" {
		return &p.ID
	}
	return nil
}

// PluginAddress 是插件一方的地址：plugin:<插件 id>，有渠道时加 /<渠道>。
func PluginAddress(v PartyView) string {
	if v.ChannelID == "" {
		return "plugin:" + v.PluginID
	}
	return "plugin:" + v.PluginID + "/" + v.ChannelID
}

// PartyView 是参与方的展示信息，地址由调用方按名称生成。
type PartyView struct {
	TaskID    *uuid.UUID
	ProjectID *uuid.UUID // Agent 所属项目；平台 Agent 与其他参与方为空
	Party
	Name        string // Agent 名或外部身份名；控制台为空
	OrgName     string // Agent 所属项目的组织名与项目名；平台 Agent 为空
	ProjectName string
	Platform    bool // 平台持有的 Agent
	Deleted     bool // Agent、外部身份已删除，或插件已卸载
	// PluginID 与 ChannelID：插件一方的插件与这条消息的渠道（没有渠道时为空）；Name 为这条消息上插件的发件名称。
	PluginID  string
	ChannelID string
}

// ---------- 消息 ----------

// InboxMessage 是一条 Inbox 消息；列表中 Text 只有前若干个字符（Preview 为 true）。
type InboxMessage struct {
	Images    []messageimage.Ref
	Question  *InboxQuestion
	AnswerTo  *InboxAnswerTo
	Notice    *InboxNotice
	ID        uuid.UUID
	ThreadID  uuid.UUID
	InReplyTo *uuid.UUID
	From      PartyView
	To        PartyView
	Text      string
	Preview   string
	WhenBusy  string // 发给人时为空
	CreatedAt time.Time
	ReadAt    *time.Time
	// CancelledAt 不为空表示这条排队消息已被人取消：收件方不再处理，不计为未读。
	CancelledAt *time.Time
	// ReminderRunID 与 ReminderTurnID 是承载这条消息的平台提醒（常态 Agent）或任务轮次（任务实例的非严格消息）。
	ReminderRunID  *uuid.UUID
	ReminderTurnID *uuid.UUID
	// Task 是消息所属的任务实例：发件方是任务实例时取发件方，否则取收件方；都不是时为空。
	Task *InboxTaskRef
}

// InboxTaskRef 对应 InboxTaskRef。NodeRunID 由 inboxNodeSQL 得出，无法确定时为空。
type InboxTaskRef struct {
	TaskID    uuid.UUID  `json:"taskId"`
	NodeRunID *uuid.UUID `json:"nodeRunId"`
	RuntimeID uuid.UUID  `json:"runtimeId"`
}

// inboxNodeSQL 是消息 a 归属的任务节点，按顺序取第一个能确定的：严格节点消息的绑定；
// 任务提问及回复它的消息取问题的绑定；发给任务实例的其他消息取承载它的提醒轮次；
// 否则取该任务实例在消息时间或之前最近一次开始的轮次。与任务实例无关或都无法确定时为 NULL。
func inboxNodeSQL(a string) string {
	return strings.ReplaceAll(`coalesce(
 (SELECT nb.node_run_id FROM task_node_inbox_messages nb WHERE nb.message_id=$m.id),
 (SELECT tq.node_run_id FROM task_questions tq WHERE tq.question_message_id IN ($m.id,$m.in_reply_to) AND tq.runtime_id=coalesce($m.from_runtime_id,$m.to_runtime_id) ORDER BY tq.question_message_id=$m.id DESC LIMIT 1),
 (SELECT tt.node_run_id FROM task_turns tt WHERE tt.id=$m.reminder_turn_id AND tt.runtime_id=$m.to_runtime_id AND $m.from_runtime_id IS NULL),
 (SELECT tt.node_run_id FROM task_turns tt WHERE tt.runtime_id=coalesce($m.from_runtime_id,$m.to_runtime_id) AND tt.node_run_id IS NOT NULL AND tt.started_at<=$m.created_at ORDER BY tt.started_at DESC,tt.id DESC LIMIT 1))`, "$m", a)
}

// partyJoin 生成某一方的 JOIN 与列；p 为别名前缀，side 为 from 或 to。
func partyJoin(p, side string) (cols, joins string) {
	r := strings.NewReplacer("$p", p, "$s", side)
	cols = r.Replace(`m.$s_kind, coalesce(m.$s_agent_id, m.$s_identity_id, m.$s_plugin_party_id, '00000000-0000-0000-0000-000000000000'::uuid),
		coalesce($pa.name, $pi.name, CASE WHEN m.$s_kind = 'plugin' THEN coalesce(m.plugin_sender_name, $pq.display_name) END, ''), coalesce($pg.name, ''), coalesce($pp.name, ''),
		($pa.id IS NOT NULL AND $pa.project_id IS NULL), $pa.project_id,
		coalesce($pa.deleted_at IS NOT NULL, false) OR coalesce($pi.deleted_at IS NOT NULL, false)
			OR ($pq.id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM plugins pl WHERE pl.id = $pq.plugin_id)),
 coalesce(m.$s_runtime_id,'00000000-0000-0000-0000-000000000000'::uuid), $pr.task_id,
		coalesce($pq.plugin_id, ''), CASE WHEN m.$s_kind = 'plugin' THEN coalesce(m.plugin_channel, '') ELSE '' END`)
	joins = r.Replace(`
		LEFT JOIN plugin_parties $pq ON $pq.id = m.$s_plugin_party_id
		LEFT JOIN task_instances $pr ON $pr.id=m.$s_runtime_id
 LEFT JOIN agents $pa ON $pa.id = m.$s_agent_id
		LEFT JOIN projects $pp ON $pp.id = $pa.project_id
		LEFT JOIN organizations $pg ON $pg.id = $pp.organization_id
		LEFT JOIN external_identities $pi ON $pi.id = m.$s_identity_id`)
	return cols, joins
}

func inboxSelect(previewChars int) string {
	fc, fj := partyJoin("f", "from")
	tc, tj := partyJoin("t", "to")
	return `SELECT m.id, m.thread_id, m.in_reply_to, m.text, left(m.text, ` + strconv.Itoa(previewChars) + `), coalesce(m.when_busy, ''),
		m.created_at, m.read_at, ` + fc + `, ` + tc + `,
 (SELECT jsonb_build_object('options',q.options,'expiresAt',q.expires_at,'state',q.state,'answer',CASE WHEN q.answer_message_id IS NULL THEN NULL ELSE jsonb_build_object('messageId',q.answer_message_id,'optionId',q.answer_option_id,'optionLabel',(SELECT opt->>'label' FROM jsonb_array_elements(q.options)opt WHERE opt->>'id'=q.answer_option_id),'textPreview',left(a.text,` + strconv.Itoa(previewChars) + `),'by',q.answered_by,'at',q.answered_at) END) FROM inbox_questions q LEFT JOIN inbox_messages a ON a.id=q.answer_message_id WHERE q.message_id=m.id),
 CASE WHEN m.answers_message_id IS NULL THEN NULL ELSE jsonb_build_object('messageId',m.answers_message_id,'optionId',m.answer_option_id,'optionLabel',(SELECT opt->>'label' FROM inbox_questions q,jsonb_array_elements(q.options)opt WHERE q.message_id=m.answers_message_id AND opt->>'id'=m.answer_option_id))END,m.notice,m.images,
 m.cancelled_at,m.reminder_run_id,m.reminder_turn_id,` + inboxNodeSQL("m") + `
 FROM inbox_messages m` + fj + tj
}

func scanInbox(row pgx.Row) (InboxMessage, error) {
	var m InboxMessage
	var question, answer, notice, images []byte
	var node *uuid.UUID
	f, t := &m.From, &m.To
	err := row.Scan(&m.ID, &m.ThreadID, &m.InReplyTo, &m.Text, &m.Preview, &m.WhenBusy, &m.CreatedAt, &m.ReadAt,
		&f.Kind, &f.ID, &f.Name, &f.OrgName, &f.ProjectName, &f.Platform, &f.ProjectID, &f.Deleted, &f.RuntimeID, &f.TaskID, &f.PluginID, &f.ChannelID,
		&t.Kind, &t.ID, &t.Name, &t.OrgName, &t.ProjectName, &t.Platform, &t.ProjectID, &t.Deleted, &t.RuntimeID, &t.TaskID, &t.PluginID, &t.ChannelID, &question, &answer, &notice, &images,
		&m.CancelledAt, &m.ReminderRunID, &m.ReminderTurnID, &node)
	if err == nil {
		for _, v := range []*PartyView{f, t} {
			if v.RuntimeID != uuid.Nil && v.TaskID != nil {
				m.Task = &InboxTaskRef{TaskID: *v.TaskID, NodeRunID: node, RuntimeID: v.RuntimeID}
				break
			}
		}
		for _, pair := range []struct {
			data   []byte
			target any
		}{{images, &m.Images}, {question, &m.Question}, {answer, &m.AnswerTo}, {notice, &m.Notice}} {
			if len(pair.data) > 0 {
				if err = json.Unmarshal(pair.data, pair.target); err != nil {
					return m, err
				}
			}
		}
	}
	return m, err
}

// InboxCursor 是列表的游标：上一页最后一条的 (时间, ID)。
type InboxCursor struct {
	At time.Time
	ID uuid.UUID
}

// Encode 返回不透明的游标字符串。
func (c InboxCursor) Encode() string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.FormatInt(c.At.UnixMicro(), 10) + "_" + c.ID.String()))
}

// DecodeInboxCursor 解析游标；无效时返回 validation_failed。
func DecodeInboxCursor(s string) (*InboxCursor, error) {
	if s == "" {
		return nil, nil
	}
	bad := apperr.Invalid("cursor", "游标无效")
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return nil, bad
	}
	at, id, ok := strings.Cut(string(raw), "_")
	if !ok {
		return nil, bad
	}
	us, err := strconv.ParseInt(at, 10, 64)
	if err != nil {
		return nil, bad
	}
	uid, err := uuid.Parse(id)
	if err != nil {
		return nil, bad
	}
	return &InboxCursor{At: time.UnixMicro(us), ID: uid}, nil
}

// InboxPage 是一页消息与下一页的游标。
type InboxPage struct {
	Messages []InboxMessage
	Next     *InboxCursor
}

// InboxListQuery 是列表的筛选条件。
type InboxListQuery struct {
	UnreadOnly bool
	Cursor     *InboxCursor
	Limit      int
	Preview    int // 摘要的字符数
}

func (s *Store) pageInbox(ctx context.Context, where string, args []any, q InboxListQuery) (InboxPage, error) {
	n := len(args)
	sql := inboxSelect(q.Preview) + ` WHERE ` + where
	if q.UnreadOnly {
		sql += ` AND ` + unreadCond("m")
	}
	var cAt *time.Time
	var cID *uuid.UUID
	if q.Cursor != nil {
		cAt, cID = &q.Cursor.At, &q.Cursor.ID
	}
	sql += fmt.Sprintf(` AND ($%d::timestamptz IS NULL OR (m.created_at, m.id) < ($%d, $%d::uuid)) ORDER BY m.created_at DESC, m.id DESC LIMIT $%d`,
		n+1, n+1, n+2, n+3)
	rows, err := s.Pool.Query(ctx, sql, append(args, cAt, cID, q.Limit+1)...)
	if err != nil {
		return InboxPage{}, err
	}
	msgs, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (InboxMessage, error) { return scanInbox(r) })
	if err != nil {
		return InboxPage{}, err
	}
	page := InboxPage{Messages: msgs}
	if len(msgs) > q.Limit {
		page.Messages = msgs[:q.Limit]
		last := page.Messages[q.Limit-1]
		page.Next = &InboxCursor{At: last.CreatedAt, ID: last.ID}
	}
	return page, nil
}

// ListInboxReceived 列出发给 p 的消息，新的在前。
func (s *Store) ListInboxReceived(ctx context.Context, p Party, q InboxListQuery) (InboxPage, error) {
	return s.pageInbox(ctx, p.cond("m", "to", 1), []any{p.ID}, q)
}

// ListInboxOfIdentity 列出外部身份收到与（或）发出的消息，新的在前；direction 为 received、sent 或 all。
func (s *Store) ListInboxOfIdentity(ctx context.Context, identityID uuid.UUID, direction string, q InboxListQuery) (InboxPage, error) {
	p := IdentityParty(identityID)
	where := "(" + p.cond("m", "to", 1) + " OR " + p.cond("m", "from", 1) + ")"
	switch direction {
	case "received":
		where = p.cond("m", "to", 1)
	case "sent":
		where = p.cond("m", "from", 1)
	}
	return s.pageInbox(ctx, where, []any{identityID}, q)
}

// ListConsoleConversation 列出控制台与常态 Agent（不含它的任务实例）之间两个方向的消息，新的在前。
func (s *Store) ListConsoleConversation(ctx context.Context, agentID uuid.UUID, q InboxListQuery) (InboxPage, error) {
	return s.pageInbox(ctx, `((m.from_kind = 'console' AND m.to_kind = 'agent' AND m.to_agent_id = $1 AND m.to_runtime_id IS NULL)
		OR (m.to_kind = 'console' AND m.from_kind = 'agent' AND m.from_agent_id = $1 AND m.from_runtime_id IS NULL))`, []any{agentID}, q)
}

// ConsolePending 返回控制台待处理的消息，新的在前：发给控制台、仍为 open 且未过期的问题，
// 与其他未读消息。两次查询在同一个可重复读事务中，快照与 now() 相同，两张列表恰好互不重复。
func (s *Store) ConsolePending(ctx context.Context, preview int) (questions, unread []InboxMessage, err error) {
	open := `EXISTS (SELECT 1 FROM inbox_questions q WHERE q.message_id = m.id AND q.state = 'open' AND q.expires_at > now())`
	list := func(tx pgx.Tx, where string) ([]InboxMessage, error) {
		rows, err := tx.Query(ctx, inboxSelect(preview)+` WHERE m.to_kind = 'console' AND `+where+` ORDER BY m.created_at DESC, m.id DESC`)
		if err != nil {
			return nil, err
		}
		return pgx.CollectRows(rows, func(r pgx.CollectableRow) (InboxMessage, error) { return scanInbox(r) })
	}
	err = pgx.BeginTxFunc(ctx, s.Pool, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}, func(tx pgx.Tx) error {
		if questions, err = list(tx, open); err != nil {
			return err
		}
		unread, err = list(tx, unreadCond("m")+` AND NOT `+open)
		return err
	})
	return questions, unread, err
}

// GetInboxMessage 返回一条消息的全文；不存在时返回 not_found。
func (s *Store) GetInboxMessage(ctx context.Context, id uuid.UUID, preview int) (InboxMessage, error) {
	m, err := scanInbox(s.Pool.QueryRow(ctx, inboxSelect(preview)+` WHERE m.id = $1`, id))
	return m, notFoundIfNoRows(err, "消息")
}

// NewInboxMessage 是写入消息的参数。ThreadID 为空表示开启新线索。
type NewInboxMessage struct {
	Images        []messageimage.Ref
	ForwardTaskID uuid.UUID // internal ended-task routing hint, never accepted from requests
	ID            uuid.UUID
	ThreadID      *uuid.UUID
	InReplyTo     *uuid.UUID
	From, To      Party
	Text          string
	WhenBusy      string // 发给 Agent 时必填；发给人时必须为空
	// PluginChannel 与 PluginSender 是消息中插件一方的渠道与发件名称（SPEC §11）；与插件无关或没有时为空。
	PluginChannel string
	PluginSender  string
}

// InsertInboxMessage 在事务中写入一条消息；收件人是 Agent 或外部身份时，先锁住它并确认未删除。
// 调用方在同一事务中继续安排提醒，保证消息与提醒一起生效。
func InsertInboxMessage(ctx context.Context, tx pgx.Tx, in NewInboxMessage) error {
	switch in.To.Kind {
	case "agent":
		if in.To.RuntimeID != uuid.Nil {
			// 未结束任务已保护Agent免于删除；调用者先锁任务，不能倒锁Agent。
			var exists bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM task_instances WHERE id=$1 AND agent_id=$2 AND NOT retired)`, in.To.RuntimeID, in.To.ID).Scan(&exists); err != nil {
				return err
			}
			if !exists {
				return apperr.NotFound("任务内Agent")
			}
		} else if _, err := lockAgent(ctx, tx, in.To.ID); err != nil {
			return notFoundAs(err, "收件人")
		}

	case "external":
		if err := lockLiveIdentity(ctx, tx, in.To.ID); err != nil {
			return notFoundAs(err, "收件人")
		}
	}
	if in.Images == nil {
		in.Images = []messageimage.Ref{}
	}
	images, _ := json.Marshal(in.Images)
	thread := in.ID
	if in.ThreadID != nil {
		thread = *in.ThreadID
	}
	_, err := tx.Exec(ctx, `INSERT INTO inbox_messages (id, thread_id, in_reply_to, from_kind, from_agent_id, from_identity_id,
			to_kind, to_agent_id, to_identity_id, text, when_busy,from_runtime_id,to_runtime_id,images,from_plugin_party_id,to_plugin_party_id,
			plugin_channel,plugin_sender_name)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, nullif($11, ''),$12,$13,$14,$15,$16,nullif($17,''),nullif($18,''))`,
		in.ID, thread, in.InReplyTo, in.From.Kind, in.From.agentID(), in.From.identityID(),
		in.To.Kind, in.To.agentID(), in.To.identityID(), in.Text, in.WhenBusy, runtimePtr(in.From.RuntimeID), runtimePtr(in.To.RuntimeID), images,
		in.From.pluginPartyID(), in.To.pluginPartyID(), in.PluginChannel, in.PluginSender)
	return err
}

func runtimePtr(id uuid.UUID) *uuid.UUID {
	if id == uuid.Nil {
		return nil
	}
	return &id
}

func notFoundAs(err error, what string) error {
	if apperr.Is(err, apperr.CodeNotFound) {
		return apperr.NotFound(what)
	}
	return err
}

// InboxTx 在事务中执行 fn（供 Inbox 服务把写消息与安排提醒放在同一事务中）。
func (s *Store) InboxTx(ctx context.Context, fn func(pgx.Tx) error) error { return s.inTx(ctx, fn) }

// ThreadParties 返回线索的两方（第一条消息的发件人与收件人）；线索不存在时返回 not_found。
func (s *Store) ThreadParties(ctx context.Context, threadID uuid.UUID) (from, to Party, err error) {
	var fa, fi, fp, ta, ti, tp *uuid.UUID
	err = s.Pool.QueryRow(ctx, `SELECT from_kind, from_agent_id, from_identity_id, from_plugin_party_id, to_kind, to_agent_id, to_identity_id, to_plugin_party_id,
			coalesce(from_runtime_id,'00000000-0000-0000-0000-000000000000'::uuid),coalesce(to_runtime_id,'00000000-0000-0000-0000-000000000000'::uuid)
		FROM inbox_messages WHERE id = $1 AND thread_id = id`, threadID).Scan(&from.Kind, &fa, &fi, &fp, &to.Kind, &ta, &ti, &tp, &from.RuntimeID, &to.RuntimeID)
	if err != nil {
		return from, to, notFoundIfNoRows(err, "线索")
	}
	from.ID, to.ID = partyID(fa, fi, fp), partyID(ta, ti, tp)
	return from, to, nil
}

func partyID(ids ...*uuid.UUID) uuid.UUID {
	for _, id := range ids {
		if id != nil {
			return *id
		}
	}
	return uuid.Nil
}

// EnsurePluginParty 登记已安装的插件作为 Inbox 的一方，名称取插件当前的显示名称；插件不存在时返回 not_found。
func (s *Store) EnsurePluginParty(ctx context.Context, plugin string) (Party, error) {
	p := PluginParty(plugin)
	tag, err := s.Pool.Exec(ctx, `INSERT INTO plugin_parties(id,plugin_id,display_name)
		SELECT $1,id,display_name FROM plugins WHERE id=$2
		ON CONFLICT (plugin_id) DO UPDATE SET display_name=EXCLUDED.display_name`, p.ID, plugin)
	if err == nil && tag.RowsAffected() == 0 {
		err = apperr.NotFound("插件")
	}
	return p, err
}

// MarkInboxRead 把发给 p 的消息标为已读；changed 表示这次由未读变为已读。消息不存在或不是发给 p 时返回 not_found。
// 已取消的消息不会变为已读。eff 是读取对排队的影响（见 markRead）。
func (s *Store) MarkInboxRead(ctx context.Context, id uuid.UUID, p Party) (changed bool, eff InboxReadEffect, err error) {
	receipts, eff, err := s.markRead(ctx, p, "m.id", id)
	if err != nil || len(receipts) == 1 {
		return err == nil, eff, err
	}
	var one int
	err = s.Pool.QueryRow(ctx, `SELECT 1 FROM inbox_messages m WHERE m.id = $2 AND `+p.cond("m", "to", 1), p.ID, id).Scan(&one)
	return false, eff, notFoundIfNoRows(err, "消息")
}

// ReadReceipt 是一条刚标为已读的消息。
type ReadReceipt struct {
	ID       uuid.UUID
	ThreadID uuid.UUID
	ReadAt   time.Time
}

// MarkThreadRead 把线索中发给 p 的未读消息全部标为已读，返回这次标记的消息与读取对排队的影响。
func (s *Store) MarkThreadRead(ctx context.Context, threadID uuid.UUID, p Party) ([]ReadReceipt, InboxReadEffect, error) {
	return s.markRead(ctx, p, "m.thread_id", threadID)
}

// InboxReadEffect 是一次读取对排队的影响：读走的消息原本由排队中的提醒承载时 QueueChanged 为 true；
// 因此不再有待处理消息的提醒随即作废（no_pending_messages），常态 Agent 的列在 Voided 中。
// AgentID 为收件的常态 Agent；任务实例的变化已在任务事件中推送，这里只给出 QueueChanged。
type InboxReadEffect struct {
	AgentID      uuid.UUID
	QueueChanged bool
	Voided       []uuid.UUID
}

// markRead 把发给 p、column 等于 id 的未读消息标为已读。收件方是常态 Agent 时先以 FOR NO KEY UPDATE 锁 Agent 行，
// 任务实例先锁任务行，再写已读：与取消、转为打断、出队同一加锁顺序。事务只做标记与作废，不处理图片等其他工作。
func (s *Store) markRead(ctx context.Context, p Party, column string, id uuid.UUID) ([]ReadReceipt, InboxReadEffect, error) {
	var out []ReadReceipt
	var links []readLink
	eff := InboxReadEffect{}
	switch {
	case p.Kind == "agent" && p.RuntimeID != uuid.Nil:
		var task uuid.UUID
		if err := s.Pool.QueryRow(ctx, `SELECT task_id FROM task_instances WHERE id=$1`, p.RuntimeID).Scan(&task); err != nil {
			return nil, eff, notFoundIfNoRows(err, "消息")
		}
		_, err := s.MutateTask(ctx, task, nil, func(m *TaskMutation) (any, error) {
			m.Unchanged()
			var err error
			if out, links, err = markReadTx(ctx, m.Tx, p, column, id); err != nil {
				return nil, err
			}
			for _, l := range links {
				if l.turn == nil {
					continue
				}
				var queued bool
				if err = m.Tx.QueryRow(ctx, `SELECT status='queued' FROM task_turns WHERE id=$1`, *l.turn).Scan(&queued); err != nil {
					return nil, err
				}
				if !queued {
					continue
				}
				eff.QueueChanged = true
				m.Changed()
				if _, err = s.voidTaskTurnIfEmptyTx(ctx, m, *l.turn, "no_pending_messages"); err != nil {
					return nil, err
				}
			}
			return nil, nil
		})
		return out, eff, err
	case p.Kind == "agent":
		eff.AgentID = p.ID
		err := s.inTx(ctx, func(tx pgx.Tx) error {
			eff.QueueChanged, eff.Voided = false, nil
			if err := lockAgentQueue(ctx, tx, p.ID); err != nil {
				return notFoundAs(err, "消息")
			}
			var err error
			if out, links, err = markReadTx(ctx, tx, p, column, id); err != nil {
				return err
			}
			for _, l := range links {
				if l.run == nil {
					continue
				}
				var queued bool
				if err = tx.QueryRow(ctx, `SELECT status='queued' FROM runs WHERE id=$1`, *l.run).Scan(&queued); err != nil {
					return err
				}
				if !queued {
					continue
				}
				eff.QueueChanged = true
				voided, err := s.voidRunIfEmptyTx(ctx, tx, *l.run, "no_pending_messages")
				if err != nil {
					return err
				}
				if voided && !slices.Contains(eff.Voided, *l.run) {
					eff.Voided = append(eff.Voided, *l.run)
				}
			}
			return nil
		})
		return out, eff, err
	}
	var err error
	out, _, err = markReadTx(ctx, s.Pool, p, column, id)
	return out, eff, err
}

type readLink struct{ run, turn *uuid.UUID }

func markReadTx(ctx context.Context, q queueQuerier, p Party, column string, id uuid.UUID) ([]ReadReceipt, []readLink, error) {
	rows, err := q.Query(ctx, `UPDATE inbox_messages m SET read_at = now()
		WHERE `+column+` = $2 AND `+unreadCond("m")+` AND `+p.cond("m", "to", 1)+` RETURNING m.id, m.thread_id, m.read_at, m.reminder_run_id, m.reminder_turn_id`, p.ID, id)
	if err != nil {
		return nil, nil, err
	}
	var links []readLink
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (ReadReceipt, error) {
		var x ReadReceipt
		var l readLink
		err := r.Scan(&x.ID, &x.ThreadID, &x.ReadAt, &l.run, &l.turn)
		links = append(links, l)
		return x, err
	})
	return out, links, err
}

// EarliestUnreadInbox 返回发给 Agent 的最早一条未读消息；没有未读时 ok 为 false。
func (s *Store) EarliestUnreadInbox(ctx context.Context, agentID uuid.UUID) (id uuid.UUID, ok bool, err error) {
	err = s.Pool.QueryRow(ctx, `SELECT id FROM inbox_messages m WHERE `+unreadCond("m")+` AND `+AgentParty(agentID).cond("m", "to", 1)+`
		ORDER BY m.created_at, m.id LIMIT 1`, agentID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, false, nil
	}
	return id, err == nil, err
}

// CountInboxUnread 统计发给 p 的未读消息数。
func (s *Store) CountInboxUnread(ctx context.Context, p Party) (int, error) {
	return countUnread(ctx, s.Pool, p)
}

type queryRower interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

func countUnread(ctx context.Context, q queryRower, p Party) (int, error) {
	var n int
	err := q.QueryRow(ctx, `SELECT count(*) FROM inbox_messages m WHERE `+unreadCond("m")+` AND `+p.cond("m", "to", 1), p.ID).Scan(&n)
	return n, err
}

// inboxEventLockKey 是串行化收件箱事件追加的 advisory lock 键。
const inboxEventLockKey = 0x6c6e5f696e626f78 // "ln_inbox"

// WithInboxEventLock 在一个短事务中取 advisory lock，统计控制台操作者的未读总数后调用 fn（追加事件或读取快照位置）。
// 追加事件因此是串行的：越靠后的事件，其未读总数越新。
func (s *Store) WithInboxEventLock(ctx context.Context, fn func(consoleUnread int) error) error {
	return s.inTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, int64(inboxEventLockKey)); err != nil {
			return err
		}
		n, err := countUnread(ctx, tx, ConsoleParty)
		if err != nil {
			return err
		}
		return fn(n)
	})
}

// ---------- 线索 ----------

// InboxThreadRow 是线索列表中的一项。
type InboxThreadRow struct {
	ThreadID    uuid.UUID
	Root        InboxMessage // 第一条消息：两方即线索的参与方
	Last        InboxMessage
	UnreadCount int
}

// InboxThreadPage 是一页线索。
type InboxThreadPage struct {
	Threads []InboxThreadRow
	Next    *InboxCursor
}

// ListInboxThreads 列出 p 参与的线索，按最后一条消息的时间倒序。
func (s *Store) ListInboxThreads(ctx context.Context, p Party, cursor *InboxCursor, limit, preview int) (InboxThreadPage, error) {
	var cAt *time.Time
	var cID *uuid.UUID
	if cursor != nil {
		cAt, cID = &cursor.At, &cursor.ID
	}
	rows, err := s.Pool.Query(ctx, `SELECT root.id, last.id,
			(SELECT count(*) FROM inbox_messages u WHERE u.thread_id = root.id AND `+unreadCond("u")+` AND `+p.cond("u", "to", 1)+`)
		FROM inbox_messages root
		CROSS JOIN LATERAL (SELECT l.id, l.created_at FROM inbox_messages l WHERE l.thread_id = root.id
			ORDER BY l.created_at DESC, l.id DESC LIMIT 1) last
		WHERE root.id = root.thread_id AND (`+p.cond("root", "from", 1)+` OR `+p.cond("root", "to", 1)+`)
			AND ($2::timestamptz IS NULL OR (last.created_at, last.id) < ($2, $3::uuid))
		ORDER BY last.created_at DESC, last.id DESC LIMIT $4`, p.ID, cAt, cID, limit+1)
	if err != nil {
		return InboxThreadPage{}, err
	}
	type row struct {
		root, last uuid.UUID
		unread     int
	}
	found, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (row, error) {
		var x row
		return x, r.Scan(&x.root, &x.last, &x.unread)
	})
	if err != nil {
		return InboxThreadPage{}, err
	}
	var page InboxThreadPage
	if len(found) > limit {
		found = found[:limit]
		page.Next = &InboxCursor{} // 由最后一项的最后一条消息补全
	}
	ids := make([]uuid.UUID, 0, 2*len(found))
	for _, x := range found {
		ids = append(ids, x.root, x.last)
	}
	byID, err := s.inboxByIDs(ctx, ids, preview)
	if err != nil {
		return page, err
	}
	for _, x := range found {
		page.Threads = append(page.Threads, InboxThreadRow{ThreadID: x.root, Root: byID[x.root], Last: byID[x.last], UnreadCount: x.unread})
	}
	if page.Next != nil {
		last := page.Threads[len(page.Threads)-1].Last
		page.Next = &InboxCursor{At: last.CreatedAt, ID: last.ID}
	}
	return page, nil
}

func (s *Store) inboxByIDs(ctx context.Context, ids []uuid.UUID, preview int) (map[uuid.UUID]InboxMessage, error) {
	out := map[uuid.UUID]InboxMessage{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := s.Pool.Query(ctx, inboxSelect(preview)+` WHERE m.id = ANY($1)`, ids)
	if err != nil {
		return nil, err
	}
	msgs, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (InboxMessage, error) { return scanInbox(r) })
	if err != nil {
		return nil, err
	}
	for _, m := range msgs {
		out[m.ID] = m
	}
	return out, nil
}

// ListThreadMessages 返回线索中的全部消息，按时间正序。
func (s *Store) ListThreadMessages(ctx context.Context, threadID uuid.UUID, preview int) ([]InboxMessage, error) {
	rows, err := s.Pool.Query(ctx, inboxSelect(preview)+` WHERE m.thread_id = $1 ORDER BY m.created_at, m.id`, threadID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (InboxMessage, error) { return scanInbox(r) })
}

// ---------- 地址解析 ----------

// FindAgentByPath 在未删除的项目中按组织名、项目名与 Agent 名查找未删除的 Agent；orgID 不为空时按组织 ID 限定。
func (s *Store) FindAgentByPath(ctx context.Context, orgName string, orgID *uuid.UUID, project, name string) (Agent, error) {
	a, err := scanAgent(s.Pool.QueryRow(ctx, `SELECT `+agentColumns+` FROM agents
		JOIN projects p ON p.id = agents.project_id AND p.deleted_at IS NULL
		JOIN organizations o ON o.id = p.organization_id
		WHERE agents.deleted_at IS NULL AND agents.name = $1 AND p.name = $2
			AND (($3::uuid IS NOT NULL AND o.id = $3) OR ($3::uuid IS NULL AND o.name = $4))`, name, project, orgID, orgName))
	return a, notFoundIfNoRows(err, "Agent")
}

// FindPlatformAgent 按名称查找未删除的平台 Agent。
func (s *Store) FindPlatformAgent(ctx context.Context, name string) (Agent, error) {
	a, err := scanAgent(s.Pool.QueryRow(ctx, `SELECT `+agentColumns+` FROM agents
		WHERE agents.deleted_at IS NULL AND agents.project_id IS NULL AND agents.name = $1`, name))
	return a, notFoundIfNoRows(err, "Agent")
}

// GetAddressableAgent 返回可寻址的 Agent：未删除，且属于未删除的项目或平台。
func (s *Store) GetAddressableAgent(ctx context.Context, id uuid.UUID) (Agent, error) {
	a, err := scanAgent(s.Pool.QueryRow(ctx, `SELECT `+agentColumns+` FROM agents
		LEFT JOIN projects p ON p.id = agents.project_id
		WHERE agents.id = $1 AND agents.deleted_at IS NULL AND (agents.project_id IS NULL OR p.deleted_at IS NULL)`, id))
	return a, notFoundIfNoRows(err, "Agent")
}

// CancelledNotice 是收件 Agent 读到被人取消的消息时得到的正文。
const CancelledNotice = "这条消息已被人取消：不要处理，也不必等待或回复。"

// RedactCancelledFor 在收件 Agent 自己读取或列出时隐去被人取消的消息的正文与图片，只留取消说明：
// 模型看到原文仍可能照做。发件方与控制台看到的保持原样。
func RedactCancelledFor(p Party, m *InboxMessage) {
	if m.CancelledAt == nil || p.Kind != "agent" || m.To.Party != p {
		return
	}
	m.Text, m.Preview, m.Images = CancelledNotice, CancelledNotice, nil
}

// IsNotFound 报告错误是否为 not_found。
func IsNotFound(err error) bool {
	return errors.Is(err, pgx.ErrNoRows) || apperr.Is(err, apperr.CodeNotFound)
}

func GetInboxMessageTx(ctx context.Context, tx pgx.Tx, id uuid.UUID, preview int) (InboxMessage, error) {
	m, err := scanInbox(tx.QueryRow(ctx, inboxSelect(preview)+` WHERE m.id=$1`, id))
	return m, notFoundIfNoRows(err, "消息")
}
