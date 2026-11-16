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

	"loop-now/internal/server/apperr"
	"loop-now/internal/server/db"
)

// ---------- Parties ----------

// Party is one side of an Inbox message: an Agent, the console operator, an external identity or a plugin.
type Party struct {
	RuntimeID uuid.UUID // when non-zero, it is a task instance and cannot read the regular or another task's Inbox
	Kind      string    // agent / console / external / plugin
	ID        uuid.UUID // Agent ID, external identity ID or plugin party ID (plugin_parties); uuid.Nil for the console
}

// pluginPartyNamespace is the namespace for plugin party IDs: each plugin maps to one fixed ID.
var pluginPartyNamespace = uuid.MustParse("6f1c9a4e-5d2b-4e8a-9c3f-2b7d0e1a8c55")

// PluginParty returns the party for a plugin acting as one side of the Inbox.
func PluginParty(plugin string) Party {
	return Party{Kind: "plugin", ID: uuid.NewSHA1(pluginPartyNamespace, []byte(plugin))}
}

// ConsoleParty is the console operator.
var ConsoleParty = Party{Kind: "console"}

// AgentParty and IdentityParty build the corresponding parties.
func AgentParty(id uuid.UUID) Party    { return Party{Kind: "agent", ID: id} }
func IdentityParty(id uuid.UUID) Party { return Party{Kind: "external", ID: id} }

// cond returns the SQL condition "the side party is p"; side is from or to, and arg is the placeholder number for
// p.ID (unused for the console).
func (p Party) cond(alias, side string, arg int) string {
	switch p.Kind {
	case "agent":
		runtime := fmt.Sprintf("%s.%s_runtime_id IS NULL", alias, side)
		if p.RuntimeID != uuid.Nil {
			runtime = fmt.Sprintf("%s.%s_runtime_id = '%s'", alias, side, p.RuntimeID.String())
		}
		return fmt.Sprintf("(%s.%s_kind = 'agent' AND %s.%s_agent_id = $%d AND %s AND %s)", alias, side, alias, side, arg, runtime, taskNodeInboxVisibleSQL(alias, p))
	case "external":
		return fmt.Sprintf("(%s.%s_kind = 'external' AND %s.%s_identity_id = $%d)", alias, side, alias, side, arg)
	case "plugin":
		return fmt.Sprintf("(%s.%s_kind = 'plugin' AND %s.%s_plugin_party_id = $%d)", alias, side, alias, side, arg)
	}
	if p.Kind == "console" {
		return fmt.Sprintf("(%s.%s_kind = 'console' AND $%d IS NOT NULL)", alias, side, arg)
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

// PluginAddress is the address of a plugin party: plugin:<plugin id>, plus /<channel> when there is a channel.
func PluginAddress(v PartyView) string {
	if v.ChannelID == "" {
		return "plugin:" + v.PluginID
	}
	return "plugin:" + v.PluginID + "/" + v.ChannelID
}

// PartyView is the display information of a party; the caller builds the address from the names.
type PartyView struct {
	TaskID    *uuid.UUID
	ProjectID *uuid.UUID // project of the Agent; nil for a platform Agent and for other parties
	Party
	Name        string // Agent name or external identity name; empty for the console
	OrgName     string // organization and project names of the Agent's project; empty for a platform Agent
	ProjectName string
	Platform    bool // an Agent owned by the platform
	Deleted     bool // the Agent or external identity is deleted, or the plugin is uninstalled
	// PluginID and ChannelID: the plugin of a plugin party and the channel of this message (empty when there is no
	// channel); Name is the plugin's sender name on this message.
	PluginID  string
	ChannelID string
}

// ---------- Messages ----------

// InboxMessage is one Inbox message; in lists, Text holds only the first few characters (Preview is true).
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
	WhenBusy  string // empty when sent to a person
	CreatedAt time.Time
	ReadAt    *time.Time
	// A non-nil CancelledAt means a person cancelled this queued message: the recipient no longer handles it, and it
	// does not count as unread.
	CancelledAt *time.Time
	// ReminderRunID and ReminderTurnID are the platform reminder (regular Agent) or task turn (non-strict message of a
	// task instance) that carries this message.
	ReminderRunID  *uuid.UUID
	ReminderTurnID *uuid.UUID
	// Task is the task instance the message belongs to: the sender when the sender is a task instance, otherwise the
	// recipient; nil when neither is.
	Task *InboxTaskRef
}

// InboxTaskRef corresponds to InboxTaskRef. NodeRunID comes from inboxNodeSQL and is nil when it cannot be determined.
type InboxTaskRef struct {
	TaskID    uuid.UUID  `json:"taskId"`
	NodeRunID *uuid.UUID `json:"nodeRunId"`
	RuntimeID uuid.UUID  `json:"runtimeId"`
}

// inboxNodeSQL is the task node that message a belongs to, taking the first one that can be determined, in order: the
// binding of a strict node message; for a task question and the messages replying to it, the question's binding; for
// other messages to a task instance, the reminder turn that carries it; otherwise the task instance's most recent turn
// that started at or before the message time. NULL when it is unrelated to a task instance or none can be determined.
func inboxNodeSQL(a string) string {
	return strings.ReplaceAll(`coalesce(
 (SELECT nb.node_run_id FROM task_node_inbox_messages nb WHERE nb.message_id=$m.id),
 (SELECT tq.node_run_id FROM task_questions tq WHERE tq.question_message_id IN ($m.id,$m.in_reply_to) AND tq.runtime_id=coalesce($m.from_runtime_id,$m.to_runtime_id) ORDER BY tq.question_message_id=$m.id DESC LIMIT 1),
 (SELECT tt.node_run_id FROM task_turns tt WHERE tt.id=$m.reminder_turn_id AND tt.runtime_id=$m.to_runtime_id AND $m.from_runtime_id IS NULL),
 (SELECT tt.node_run_id FROM task_turns tt WHERE tt.runtime_id=coalesce($m.from_runtime_id,$m.to_runtime_id) AND tt.node_run_id IS NOT NULL AND tt.started_at<=$m.created_at ORDER BY tt.started_at DESC,tt.id DESC LIMIT 1))`, "$m", a)
}

// partyJoin builds the JOINs and columns for one party; p is the alias prefix and side is from or to.
func partyJoin(p, side string) (cols, joins string) {
	r := strings.NewReplacer("$p", p, "$s", side)
	cols = r.Replace(`m.$s_kind, coalesce(m.$s_agent_id, m.$s_identity_id, m.$s_plugin_party_id, '00000000-0000-0000-0000-000000000000'),
		coalesce($pa.name, $pi.name, CASE WHEN m.$s_kind = 'plugin' THEN coalesce(m.plugin_sender_name, $pq.display_name) END, ''), coalesce($pg.name, ''), coalesce($pp.name, ''),
		($pa.id IS NOT NULL AND $pa.project_id IS NULL), $pa.project_id,
		coalesce($pa.deleted_at IS NOT NULL, false) OR coalesce($pi.deleted_at IS NOT NULL, false)
			OR ($pq.id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM plugins pl WHERE pl.id = $pq.plugin_id)),
 coalesce(m.$s_runtime_id,'00000000-0000-0000-0000-000000000000'), $pr.task_id,
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
	n := strconv.Itoa(previewChars)
	// Questions and answers are assembled into JSON: nested JSON columns must be wrapped in json(), and times are
	// converted with iso_time() into strings Go can parse.
	return `SELECT m.id, m.thread_id, m.in_reply_to, m.text, substr(m.text, 1, ` + n + `), coalesce(m.when_busy, ''),
		m.created_at, m.read_at, ` + fc + `, ` + tc + `,
		(SELECT json_object('options', json(q.options), 'expiresAt', iso_time(q.expires_at), 'state', q.state, 'answer',
			CASE WHEN q.answer_message_id IS NULL THEN NULL ELSE json_object('messageId', q.answer_message_id, 'optionId', q.answer_option_id,
				'optionLabel', (SELECT opt.value ->> 'label' FROM json_each(q.options) opt WHERE opt.value ->> 'id' = q.answer_option_id),
				'textPreview', substr(a.text, 1, ` + n + `), 'by', json(q.answered_by), 'at', iso_time(q.answered_at)) END)
			FROM inbox_questions q LEFT JOIN inbox_messages a ON a.id = q.answer_message_id WHERE q.message_id = m.id),
		CASE WHEN m.answers_message_id IS NULL THEN NULL ELSE json_object('messageId', m.answers_message_id, 'optionId', m.answer_option_id,
			'optionLabel', (SELECT opt.value ->> 'label' FROM inbox_questions q, json_each(q.options) opt
				WHERE q.message_id = m.answers_message_id AND opt.value ->> 'id' = m.answer_option_id)) END, m.notice, m.images,
		m.cancelled_at, m.reminder_run_id, m.reminder_turn_id, ` + inboxNodeSQL("m") + `
	FROM inbox_messages m` + fj + tj
}

func scanInbox(row db.Scanner) (InboxMessage, error) {
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

// InboxCursor is the list cursor: (time, ID) of the last item on the previous page.
type InboxCursor struct {
	At time.Time
	ID uuid.UUID
}

// Encode returns an opaque cursor string.
func (c InboxCursor) Encode() string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.FormatInt(c.At.UnixMicro(), 10) + "_" + c.ID.String()))
}

// DecodeInboxCursor parses a cursor; returns validation_failed when it is invalid.
func DecodeInboxCursor(s string) (*InboxCursor, error) {
	if s == "" {
		return nil, nil
	}
	bad := apperr.Invalid("cursor", "The cursor is invalid.")
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

// InboxPage is one page of messages and the cursor for the next page.
type InboxPage struct {
	Messages []InboxMessage
	Next     *InboxCursor
}

// InboxListQuery holds the list filters.
type InboxListQuery struct {
	UnreadOnly bool
	Cursor     *InboxCursor
	Limit      int
	Preview    int // number of characters in the preview
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
	sql += fmt.Sprintf(` AND ($%d IS NULL OR (m.created_at, m.id) < ($%d, $%d)) ORDER BY m.created_at DESC, m.id DESC LIMIT $%d`,
		n+1, n+1, n+2, n+3)
	rows, err := s.Pool.Query(ctx, sql, append(args, cAt, cID, q.Limit+1)...)
	if err != nil {
		return InboxPage{}, err
	}
	msgs, err := db.CollectRows(rows, func(r db.CollectableRow) (InboxMessage, error) { return scanInbox(r) })
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

// ListInboxReceived lists the messages sent to p, newest first.
func (s *Store) ListInboxReceived(ctx context.Context, p Party, q InboxListQuery) (InboxPage, error) {
	return s.pageInbox(ctx, p.cond("m", "to", 1), []any{p.ID}, q)
}

// ListInboxOfIdentity lists the messages an external identity received and/or sent, newest first; direction is
// received, sent or all.
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

// ListConsoleConversation lists the messages in both directions between the console and a regular Agent (not its task
// instances), newest first.
func (s *Store) ListConsoleConversation(ctx context.Context, agentID uuid.UUID, q InboxListQuery) (InboxPage, error) {
	return s.pageInbox(ctx, `((m.from_kind = 'console' AND m.to_kind = 'agent' AND m.to_agent_id = $1 AND m.to_runtime_id IS NULL)
		OR (m.to_kind = 'console' AND m.from_kind = 'agent' AND m.from_agent_id = $1 AND m.from_runtime_id IS NULL))`, []any{agentID}, q)
}

// ConsolePending returns the messages pending for the console, newest first: questions to the console that are still
// open and not expired, and other unread messages. Both queries run in one read transaction (ReadTx) and see the same
// snapshot. now() on a read connection is the time of each call, though, so a question that expires in the moment
// between the two queries can appear in both lists.
func (s *Store) ConsolePending(ctx context.Context, preview int) (questions, unread []InboxMessage, err error) {
	open := `EXISTS (SELECT 1 FROM inbox_questions q WHERE q.message_id = m.id AND q.state = 'open' AND q.expires_at > now())`
	list := func(tx *db.Tx, where string) ([]InboxMessage, error) {
		rows, err := tx.Query(ctx, inboxSelect(preview)+` WHERE m.to_kind = 'console' AND `+where+` ORDER BY m.created_at DESC, m.id DESC`)
		if err != nil {
			return nil, err
		}
		return db.CollectRows(rows, func(r db.CollectableRow) (InboxMessage, error) { return scanInbox(r) })
	}
	err = s.Pool.ReadTx(ctx, func(tx *db.Tx) error {
		if questions, err = list(tx, open); err != nil {
			return err
		}
		unread, err = list(tx, unreadCond("m")+` AND NOT `+open)
		return err
	})
	return questions, unread, err
}

// GetInboxMessage returns the full text of one message; returns not_found when it does not exist.
func (s *Store) GetInboxMessage(ctx context.Context, id uuid.UUID, preview int) (InboxMessage, error) {
	m, err := scanInbox(s.Pool.QueryRow(ctx, inboxSelect(preview)+` WHERE m.id = $1`, id))
	return m, notFoundIfNoRows(err, "Message")
}

// NewInboxMessage holds the parameters for writing a message. An empty ThreadID starts a new thread.
type NewInboxMessage struct {
	Images        []messageimage.Ref
	ForwardTaskID uuid.UUID // internal ended-task routing hint, never accepted from requests
	ID            uuid.UUID
	ThreadID      *uuid.UUID
	InReplyTo     *uuid.UUID
	From, To      Party
	Text          string
	WhenBusy      string // required when sent to an Agent; must be empty when sent to a person
	// PluginChannel and PluginSender are the channel and sender name of the plugin party in the message (SPEC §11);
	// empty when unrelated to a plugin or absent.
	PluginChannel string
	PluginSender  string
}

// InsertInboxMessage writes one message in the transaction; when the recipient is an Agent or an external identity,
// it first locks it and confirms it is not deleted.
// The caller goes on to arrange the reminder in the same transaction, so the message and the reminder take effect
// together.
func InsertInboxMessage(ctx context.Context, tx *db.Tx, in NewInboxMessage) error {
	switch in.To.Kind {
	case "agent":
		if in.To.RuntimeID != uuid.Nil {
			// An unfinished task already protects the Agent from deletion; callers lock the task first and must not
			// lock the Agent in the reverse order.
			var exists bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM task_instances WHERE id=$1 AND agent_id=$2 AND NOT retired)`, in.To.RuntimeID, in.To.ID).Scan(&exists); err != nil {
				return err
			}
			if !exists {
				return apperr.NotFound("Agent in this task")
			}
		} else if _, err := lockAgent(ctx, tx, in.To.ID); err != nil {
			return notFoundAs(err, "Recipient")
		}

	case "external":
		if err := requireLiveIdentity(ctx, tx, in.To.ID); err != nil {
			return notFoundAs(err, "Recipient")
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
		in.To.Kind, in.To.agentID(), in.To.identityID(), in.Text, in.WhenBusy, runtimePtr(in.From.RuntimeID), runtimePtr(in.To.RuntimeID), json.RawMessage(images),
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

// InboxTx runs fn in a transaction (so the Inbox service can write the message and arrange the reminder in the same
// transaction).
func (s *Store) InboxTx(ctx context.Context, fn func(*db.Tx) error) error { return s.inTx(ctx, fn) }

// ThreadParties returns the two parties of a thread (the sender and recipient of the first message); returns
// not_found when the thread does not exist.
func (s *Store) ThreadParties(ctx context.Context, threadID uuid.UUID) (from, to Party, err error) {
	var fa, fi, fp, ta, ti, tp *uuid.UUID
	err = s.Pool.QueryRow(ctx, `SELECT from_kind, from_agent_id, from_identity_id, from_plugin_party_id, to_kind, to_agent_id, to_identity_id, to_plugin_party_id,
			coalesce(from_runtime_id,'00000000-0000-0000-0000-000000000000'),coalesce(to_runtime_id,'00000000-0000-0000-0000-000000000000')
		FROM inbox_messages WHERE id = $1 AND thread_id = id`, threadID).Scan(&from.Kind, &fa, &fi, &fp, &to.Kind, &ta, &ti, &tp, &from.RuntimeID, &to.RuntimeID)
	if err != nil {
		return from, to, notFoundIfNoRows(err, "Thread")
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

// EnsurePluginParty registers an installed plugin as an Inbox party, named after the plugin's current display name;
// returns not_found when the plugin does not exist.
func (s *Store) EnsurePluginParty(ctx context.Context, plugin string) (Party, error) {
	p := PluginParty(plugin)
	tag, err := s.Pool.Exec(ctx, `INSERT INTO plugin_parties(id,plugin_id,display_name)
		SELECT $1,id,display_name FROM plugins WHERE id=$2
		ON CONFLICT (plugin_id) DO UPDATE SET display_name=EXCLUDED.display_name`, p.ID, plugin)
	if err == nil && tag.RowsAffected() == 0 {
		err = apperr.NotFound("Plugin")
	}
	return p, err
}

// MarkInboxRead marks a message sent to p as read; changed means this call moved it from unread to read. Returns
// not_found when the message does not exist or was not sent to p.
// A cancelled message never becomes read. eff is the effect of the read on the queue (see markRead).
func (s *Store) MarkInboxRead(ctx context.Context, id uuid.UUID, p Party) (changed bool, eff InboxReadEffect, err error) {
	receipts, eff, err := s.markRead(ctx, p, "m.id", id)
	if err != nil || len(receipts) == 1 {
		return err == nil, eff, err
	}
	var one int
	err = s.Pool.QueryRow(ctx, `SELECT 1 FROM inbox_messages m WHERE m.id = $2 AND `+p.cond("m", "to", 1), p.ID, id).Scan(&one)
	return false, eff, notFoundIfNoRows(err, "Message")
}

// ReadReceipt is a message just marked as read.
type ReadReceipt struct {
	ID       uuid.UUID
	ThreadID uuid.UUID
	ReadAt   time.Time
}

// MarkThreadRead marks all unread messages sent to p in a thread as read, and returns the messages marked by this call
// and the effect of the read on the queue.
func (s *Store) MarkThreadRead(ctx context.Context, threadID uuid.UUID, p Party) ([]ReadReceipt, InboxReadEffect, error) {
	return s.markRead(ctx, p, "m.thread_id", threadID)
}

// InboxReadEffect is the effect of one read on the queue: QueueChanged is true when a message that was read had been
// carried by a queued reminder; reminders left with no pending messages as a result are voided right away
// (no_pending_messages), and those of regular Agents are listed in Voided.
// AgentID is the recipient regular Agent; changes for task instances are already pushed in task events, so only
// QueueChanged is given here.
type InboxReadEffect struct {
	AgentID      uuid.UUID
	QueueChanged bool
	Voided       []uuid.UUID
}

// markRead marks the unread messages sent to p whose column equals id as read. For a regular Agent it runs in one
// write transaction that first checks the Agent is live (writes are serialized, so no row lock is needed); for a task
// instance it runs inside MutateTask on the task, like cancel, promote to interrupt and dequeue. The transaction only
// marks and voids; it does no other work such as images.
func (s *Store) markRead(ctx context.Context, p Party, column string, id uuid.UUID) ([]ReadReceipt, InboxReadEffect, error) {
	var out []ReadReceipt
	var links []readLink
	eff := InboxReadEffect{}
	switch {
	case p.Kind == "agent" && p.RuntimeID != uuid.Nil:
		var task uuid.UUID
		if err := s.Pool.QueryRow(ctx, `SELECT task_id FROM task_instances WHERE id=$1`, p.RuntimeID).Scan(&task); err != nil {
			return nil, eff, notFoundIfNoRows(err, "Message")
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
		err := s.inTx(ctx, func(tx *db.Tx) error {
			eff.QueueChanged, eff.Voided = false, nil
			if err := requireLiveAgent(ctx, tx, p.ID); err != nil {
				return notFoundAs(err, "Message")
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
	rows, err := q.Query(ctx, `UPDATE inbox_messages AS m SET read_at = now()
		WHERE `+column+` = $2 AND `+unreadCond("m")+` AND `+p.cond("m", "to", 1)+` RETURNING id, thread_id, read_at, reminder_run_id, reminder_turn_id`, p.ID, id)
	if err != nil {
		return nil, nil, err
	}
	var links []readLink
	out, err := db.CollectRows(rows, func(r db.CollectableRow) (ReadReceipt, error) {
		var x ReadReceipt
		var l readLink
		err := r.Scan(&x.ID, &x.ThreadID, &x.ReadAt, &l.run, &l.turn)
		links = append(links, l)
		return x, err
	})
	return out, links, err
}

// EarliestUnreadInbox returns the earliest unread message sent to the Agent; ok is false when there is nothing unread.
func (s *Store) EarliestUnreadInbox(ctx context.Context, agentID uuid.UUID) (id uuid.UUID, ok bool, err error) {
	err = s.Pool.QueryRow(ctx, `SELECT id FROM inbox_messages m WHERE `+unreadCond("m")+` AND `+AgentParty(agentID).cond("m", "to", 1)+`
		ORDER BY m.created_at, m.id LIMIT 1`, agentID).Scan(&id)
	if errors.Is(err, db.ErrNoRows) {
		return uuid.Nil, false, nil
	}
	return id, err == nil, err
}

// CountInboxUnread counts the unread messages sent to p.
func (s *Store) CountInboxUnread(ctx context.Context, p Party) (int, error) {
	return countUnread(ctx, s.Pool, p)
}

type queryRower interface {
	QueryRow(ctx context.Context, sql string, args ...any) *db.Row
}

func countUnread(ctx context.Context, q queryRower, p Party) (int, error) {
	var n int
	err := q.QueryRow(ctx, `SELECT count(*) FROM inbox_messages m WHERE `+unreadCond("m")+` AND `+p.cond("m", "to", 1), p.ID).Scan(&n)
	return n, err
}

// inboxEventLockKey is the advisory lock key that serializes appending Inbox events.
const inboxEventLockKey = 0x6c6e5f696e626f78 // "ln_inbox"

// WithInboxEventLock takes the advisory lock in a short transaction, counts the console operator's total unread, then
// calls fn (to append an event or read the snapshot position).
// Appending events is therefore serialized: the later the event, the more recent its unread total.
func (s *Store) WithInboxEventLock(ctx context.Context, fn func(consoleUnread int) error) error {
	return s.inTx(ctx, func(tx *db.Tx) error {
		n, err := countUnread(ctx, tx, ConsoleParty)
		if err != nil {
			return err
		}
		return fn(n)
	})
}

// ---------- Threads ----------

// InboxThreadRow is one item in the thread list.
type InboxThreadRow struct {
	ThreadID    uuid.UUID
	Root        InboxMessage // first message: its two sides are the thread's parties
	Last        InboxMessage
	UnreadCount int
}

// InboxThreadPage is one page of threads.
type InboxThreadPage struct {
	Threads []InboxThreadRow
	Next    *InboxCursor
}

// ListInboxThreads lists the threads p takes part in, ordered by the time of the last message, newest first.
func (s *Store) ListInboxThreads(ctx context.Context, p Party, cursor *InboxCursor, limit, preview int) (InboxThreadPage, error) {
	var cAt *time.Time
	var cID *uuid.UUID
	if cursor != nil {
		cAt, cID = &cursor.At, &cursor.ID
	}
	// First select each thread's first message by party, then use a correlated subquery to take each thread's last
	// message (using the thread_id index).
	rows, err := s.Pool.Query(ctx, `SELECT t.root_id, last.id, t.unread FROM (
			SELECT root.id AS root_id,
				(SELECT l.id FROM inbox_messages l WHERE l.thread_id = root.id ORDER BY l.created_at DESC, l.id DESC LIMIT 1) AS last_id,
				(SELECT count(*) FROM inbox_messages u WHERE u.thread_id = root.id AND `+unreadCond("u")+` AND `+p.cond("u", "to", 1)+`) AS unread
			FROM inbox_messages root
			WHERE root.id = root.thread_id AND (`+p.cond("root", "from", 1)+` OR `+p.cond("root", "to", 1)+`)) t
		JOIN inbox_messages last ON last.id = t.last_id
		WHERE ($2 IS NULL OR (last.created_at, last.id) < ($2, $3))
		ORDER BY last.created_at DESC, last.id DESC LIMIT $4`, p.ID, cAt, cID, limit+1)
	if err != nil {
		return InboxThreadPage{}, err
	}
	type row struct {
		root, last uuid.UUID
		unread     int
	}
	found, err := db.CollectRows(rows, func(r db.CollectableRow) (row, error) {
		var x row
		return x, r.Scan(&x.root, &x.last, &x.unread)
	})
	if err != nil {
		return InboxThreadPage{}, err
	}
	var page InboxThreadPage
	if len(found) > limit {
		found = found[:limit]
		page.Next = &InboxCursor{} // completed from the last message of the last item
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
	rows, err := s.Pool.Query(ctx, inboxSelect(preview)+` WHERE m.id  IN (SELECT value FROM json_each($1))`, ids)
	if err != nil {
		return nil, err
	}
	msgs, err := db.CollectRows(rows, func(r db.CollectableRow) (InboxMessage, error) { return scanInbox(r) })
	if err != nil {
		return nil, err
	}
	for _, m := range msgs {
		out[m.ID] = m
	}
	return out, nil
}

// ListThreadMessages returns all messages in a thread, oldest first.
func (s *Store) ListThreadMessages(ctx context.Context, threadID uuid.UUID, preview int) ([]InboxMessage, error) {
	rows, err := s.Pool.Query(ctx, inboxSelect(preview)+` WHERE m.thread_id = $1 ORDER BY m.created_at, m.id`, threadID)
	if err != nil {
		return nil, err
	}
	return db.CollectRows(rows, func(r db.CollectableRow) (InboxMessage, error) { return scanInbox(r) })
}

// ---------- Address resolution ----------

// FindAgentByPath finds a non-deleted Agent by organization name, project name and Agent name in non-deleted projects;
// when orgID is not nil it restricts the search to that organization ID.
func (s *Store) FindAgentByPath(ctx context.Context, orgName string, orgID *uuid.UUID, project, name string) (Agent, error) {
	a, err := scanAgent(s.Pool.QueryRow(ctx, `SELECT `+agentColumns+` FROM agents
		JOIN projects p ON p.id = agents.project_id AND p.deleted_at IS NULL
		JOIN organizations o ON o.id = p.organization_id
		WHERE agents.deleted_at IS NULL AND agents.name = $1 AND p.name = $2
			AND (($3 IS NOT NULL AND o.id = $3) OR ($3 IS NULL AND o.name = $4))`, name, project, orgID, orgName))
	return a, notFoundIfNoRows(err, "Agent")
}

// FindPlatformAgent finds a non-deleted platform Agent by name.
func (s *Store) FindPlatformAgent(ctx context.Context, name string) (Agent, error) {
	a, err := scanAgent(s.Pool.QueryRow(ctx, `SELECT `+agentColumns+` FROM agents
		WHERE agents.deleted_at IS NULL AND agents.project_id IS NULL AND agents.name = $1`, name))
	return a, notFoundIfNoRows(err, "Agent")
}

// GetAddressableAgent returns an addressable Agent: not deleted, and belonging to a non-deleted project or to the
// platform.
func (s *Store) GetAddressableAgent(ctx context.Context, id uuid.UUID) (Agent, error) {
	a, err := scanAgent(s.Pool.QueryRow(ctx, `SELECT `+agentColumns+` FROM agents
		LEFT JOIN projects p ON p.id = agents.project_id
		WHERE agents.id = $1 AND agents.deleted_at IS NULL AND (agents.project_id IS NULL OR p.deleted_at IS NULL)`, id))
	return a, notFoundIfNoRows(err, "Agent")
}

// CancelledNotice is the text the recipient Agent gets when it reads a message a person cancelled.
const CancelledNotice = "This message was cancelled by a person: do not handle it, and there is no need to wait or reply."

// RedactCancelledFor hides the text and images of a message a person cancelled when the recipient Agent itself reads
// or lists it, leaving only the cancellation notice: if the model saw the original text it might still act on it. The
// sender and the console see it unchanged.
func RedactCancelledFor(p Party, m *InboxMessage) {
	if m.CancelledAt == nil || p.Kind != "agent" || m.To.Party != p {
		return
	}
	m.Text, m.Preview, m.Images = CancelledNotice, CancelledNotice, nil
}

// IsNotFound reports whether the error is not_found.
func IsNotFound(err error) bool {
	return errors.Is(err, db.ErrNoRows) || apperr.Is(err, apperr.CodeNotFound)
}

func GetInboxMessageTx(ctx context.Context, tx *db.Tx, id uuid.UUID, preview int) (InboxMessage, error) {
	m, err := scanInbox(tx.QueryRow(ctx, inboxSelect(preview)+` WHERE m.id=$1`, id))
	return m, notFoundIfNoRows(err, "Message")
}
