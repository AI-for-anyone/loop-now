package apitypes

import (
	"bytes"
	"encoding/json"
	"errors"
	"loop-now/internal/server/messageimage"
	"strings"
	"time"

	"github.com/google/uuid"

	"loop-now/internal/server/auth"
	"loop-now/internal/server/naming"
	"loop-now/internal/server/store"
)

// 收件箱事件流的事件名。
const (
	EventInboxSnapshot  = "snapshot"
	EventInboxCreated   = "inbox.message.created"
	EventInboxRead      = "inbox.message.read"
	EventInboxCancelled = "inbox.message.cancelled"
)

// 已删除参与方的显示名称。
const (
	DeletedIdentityName = "已删除身份"
	DeletedAgentName    = "已删除 Agent"
)

// Whoami 对应 Whoami。
type Whoami struct {
	TaskID    *uuid.UUID `json:"taskId,omitempty"`
	RuntimeID *uuid.UUID `json:"runtimeId,omitempty"`
	Kind      string     `json:"kind"`
	Address   *string    `json:"address"`
	Name      string     `json:"name"`
}

// FromPrincipal 转换当前身份。
func FromPrincipal(p auth.Principal) Whoami {
	w := Whoami{Kind: string(p.Kind), Name: p.Name}
	if p.TaskID != uuid.Nil {
		w.TaskID = &p.TaskID
		w.RuntimeID = &p.RuntimeID
	}
	if p.Address != "" {
		w.Address = &p.Address
	}
	return w
}

// InboxParticipant 对应 InboxParticipant。
type InboxParticipant struct {
	Kind      string     `json:"kind"`
	ID        *uuid.UUID `json:"id"`
	Address   *string    `json:"address"`
	Name      string     `json:"name"`
	Deleted   bool       `json:"deleted"`
	PluginID  *string    `json:"pluginId"`
	ChannelID *string    `json:"channelId"`
}

// FromParty 转换参与方：按当前名称生成地址；已删除时显示名称换成“已删除身份”或“已删除 Agent”。
func FromParty(v store.PartyView) InboxParticipant {
	out := InboxParticipant{Kind: v.Kind, Deleted: v.Deleted}
	switch v.Kind {
	case "platform":
		addr := "@platform"
		out.Address = &addr
		out.Name = "平台"
	case "console":
		addr := auth.ConsoleAddress
		out.Address, out.Name = &addr, auth.ConsoleName
	case "external":
		id, addr := v.ID, "@"+v.Name
		out.ID, out.Address, out.Name = &id, &addr, v.Name
		if v.Deleted {
			out.Name = DeletedIdentityName
		}
	case "agent":
		id := v.ID
		out.ID, out.Name = &id, v.Name
		if addr, ok := naming.AgentAddress(v.OrgName, v.ProjectName, v.Name, v.Platform); ok {
			if v.TaskID != nil {
				addr += "#" + v.TaskID.String()
			}
			out.Address = &addr
		}
		if v.Deleted {
			out.Name = DeletedAgentName
		}
	case "plugin":
		addr, plugin := store.PluginAddress(v), v.PluginID
		out.Address, out.Name, out.PluginID = &addr, v.Name, &plugin
		if v.ChannelID != "" {
			channel := v.ChannelID
			out.ChannelID = &channel
		}
	}
	return out
}

// InboxMessageSummary 对应 InboxMessageSummary。
type InboxMessageSummary struct {
	Images    []messageimage.Ref   `json:"images,omitempty"`
	Question  *store.InboxQuestion `json:"question,omitempty"`
	AnswerTo  *store.InboxAnswerTo `json:"answerTo,omitempty"`
	Notice    *store.InboxNotice   `json:"notice,omitempty"`
	ID        uuid.UUID            `json:"id"`
	ThreadID  uuid.UUID            `json:"threadId"`
	InReplyTo *uuid.UUID           `json:"inReplyTo"`
	From      InboxParticipant     `json:"from"`
	To        InboxParticipant     `json:"to"`
	Preview   string               `json:"preview"`
	WhenBusy  *string              `json:"whenBusy"`
	CreatedAt time.Time            `json:"createdAt"`
	ReadAt    *time.Time           `json:"readAt"`
	// CancelledAt 不为空表示被人取消；收件 Agent 自己读到的正文为取消说明，不含原文与图片。
	CancelledAt *time.Time `json:"cancelledAt"`
	// Task 是消息所属的任务实例；与任务实例无关时为 null。
	Task *store.InboxTaskRef `json:"task"`
}

// InboxMessage 对应 InboxMessage：摘要加全文。
type InboxMessage struct {
	InboxMessageSummary
	Text string `json:"text"`
}

// FromInboxSummary 转换消息摘要。
func FromInboxSummary(m store.InboxMessage) InboxMessageSummary {
	return InboxMessageSummary{Images: m.Images, Question: m.Question, AnswerTo: m.AnswerTo, Notice: m.Notice, ID: m.ID, ThreadID: m.ThreadID, InReplyTo: m.InReplyTo, From: FromParty(m.From), To: FromParty(m.To),
		Preview: m.Preview, WhenBusy: strPtr(m.WhenBusy), CreatedAt: m.CreatedAt, ReadAt: m.ReadAt, CancelledAt: m.CancelledAt, Task: m.Task}
}

// FromInboxMessage 转换消息全文。
func FromInboxMessage(m store.InboxMessage) InboxMessage {
	return InboxMessage{InboxMessageSummary: FromInboxSummary(m), Text: m.Text}
}

// InboxMessagePage 对应 InboxMessagePage。
type InboxMessagePage struct {
	Messages   []InboxMessageSummary `json:"messages"`
	NextCursor *string               `json:"nextCursor"`
}

// FromInboxPage 转换一页消息。
func FromInboxPage(p store.InboxPage) InboxMessagePage {
	out := InboxMessagePage{Messages: make([]InboxMessageSummary, 0, len(p.Messages)), NextCursor: encodeCursor(p.Next)}
	for _, m := range p.Messages {
		out.Messages = append(out.Messages, FromInboxSummary(m))
	}
	return out
}

func encodeCursor(c *store.InboxCursor) *string {
	if c == nil {
		return nil
	}
	s := c.Encode()
	return &s
}

// InboxConversationPage 对应 InboxConversationPage：一页消息全文。
type InboxConversationPage struct {
	Messages   []InboxMessage `json:"messages"`
	NextCursor *string        `json:"nextCursor"`
}

// FromConversationPage 转换一页消息全文。
func FromConversationPage(p store.InboxPage) InboxConversationPage {
	out := InboxConversationPage{Messages: make([]InboxMessage, 0, len(p.Messages)), NextCursor: encodeCursor(p.Next)}
	for _, m := range p.Messages {
		out.Messages = append(out.Messages, FromInboxMessage(m))
	}
	return out
}

// InboxPending 对应 InboxPending；InboxPendingItem、InboxPendingAgentCount、InboxPendingTaskCount 是其中的项。
type InboxPending struct {
	Questions []InboxPendingItem       `json:"questions"`
	Unread    []InboxPendingItem       `json:"unread"`
	ByAgent   []InboxPendingAgentCount `json:"byAgent"`
	ByTask    []InboxPendingTaskCount  `json:"byTask"`
}

type InboxPendingItem struct {
	Message   InboxMessageSummary `json:"message"`
	ProjectID *uuid.UUID          `json:"projectId"`
}

type InboxPendingAgentCount struct {
	AgentID   uuid.UUID  `json:"agentId"`
	ProjectID *uuid.UUID `json:"projectId"`
	Questions int        `json:"questions"`
	Unread    int        `json:"unread"`
}

type InboxPendingTaskCount struct {
	TaskID    uuid.UUID  `json:"taskId"`
	ProjectID *uuid.UUID `json:"projectId"`
	Questions int        `json:"questions"`
	Unread    int        `json:"unread"`
}

// FromPending 转换待处理列表，并按发件的常态 Agent 与任务汇总；分组按首次出现（最新的一条）排序。
func FromPending(questions, unread []store.InboxMessage) InboxPending {
	out := InboxPending{Questions: []InboxPendingItem{}, Unread: []InboxPendingItem{}, ByAgent: []InboxPendingAgentCount{}, ByTask: []InboxPendingTaskCount{}}
	agents, tasks := map[uuid.UUID]int{}, map[uuid.UUID]int{}
	add := func(list *[]InboxPendingItem, m store.InboxMessage, question bool) {
		*list = append(*list, InboxPendingItem{Message: FromInboxSummary(m), ProjectID: m.From.ProjectID})
		var questions, unread *int
		switch {
		case m.Task != nil:
			i, ok := tasks[m.Task.TaskID]
			if !ok {
				i = len(out.ByTask)
				tasks[m.Task.TaskID] = i
				out.ByTask = append(out.ByTask, InboxPendingTaskCount{TaskID: m.Task.TaskID, ProjectID: m.From.ProjectID})
			}
			questions, unread = &out.ByTask[i].Questions, &out.ByTask[i].Unread
		case m.From.Kind == "agent":
			i, ok := agents[m.From.ID]
			if !ok {
				i = len(out.ByAgent)
				agents[m.From.ID] = i
				out.ByAgent = append(out.ByAgent, InboxPendingAgentCount{AgentID: m.From.ID, ProjectID: m.From.ProjectID})
			}
			questions, unread = &out.ByAgent[i].Questions, &out.ByAgent[i].Unread
		default:
			return
		}
		if question {
			*questions++
		} else {
			*unread++
		}
	}
	for _, m := range questions {
		add(&out.Questions, m, true)
	}
	for _, m := range unread {
		add(&out.Unread, m, false)
	}
	return out
}

// InboxThreadSummary 对应 InboxThreadSummary。
type InboxThreadSummary struct {
	ThreadID     uuid.UUID           `json:"threadId"`
	Participants []InboxParticipant  `json:"participants"`
	LastMessage  InboxMessageSummary `json:"lastMessage"`
	UnreadCount  int                 `json:"unreadCount"`
	UpdatedAt    time.Time           `json:"updatedAt"`
}

// InboxThreadPage 对应 InboxThreadPage。
type InboxThreadPage struct {
	Threads     []InboxThreadSummary `json:"threads"`
	UnreadTotal int                  `json:"unreadTotal"`
	NextCursor  *string              `json:"nextCursor"`
}

// FromThreadPage 转换一页线索。
func FromThreadPage(p store.InboxThreadPage, unreadTotal int) InboxThreadPage {
	out := InboxThreadPage{Threads: make([]InboxThreadSummary, 0, len(p.Threads)), UnreadTotal: unreadTotal, NextCursor: encodeCursor(p.Next)}
	for _, t := range p.Threads {
		out.Threads = append(out.Threads, InboxThreadSummary{ThreadID: t.ThreadID,
			Participants: []InboxParticipant{FromParty(t.Root.From), FromParty(t.Root.To)},
			LastMessage:  FromInboxSummary(t.Last), UnreadCount: t.UnreadCount, UpdatedAt: t.Last.CreatedAt})
	}
	return out
}

// InboxThread 对应 InboxThread。
type InboxThread struct {
	ThreadID uuid.UUID      `json:"threadId"`
	Messages []InboxMessage `json:"messages"`
}

// InboxUnreadTotal 对应 InboxUnreadTotal。
type InboxUnreadTotal struct {
	UnreadTotal int `json:"unreadTotal"`
}

// InboxSendInput 与 InboxReplyInput 是发送与回复的请求体。
type InboxSendInput struct {
	RunID      *uuid.UUID   `json:"runId,omitempty"`
	Options    InboxOptions `json:"options,omitempty"`
	NodeRunID  uuid.UUID    `json:"nodeRunId"`
	To         string       `json:"to"`
	Text       string       `json:"text"`
	WhenBusy   string       `json:"whenBusy"`
	ChannelID  string       `json:"channelId"`
	SenderName string       `json:"senderName"`
}

type InboxReplyInput struct {
	AsIdentityID *uuid.UUID   `json:"asIdentityId,omitempty"`
	RunID        *uuid.UUID   `json:"runId,omitempty"`
	Options      InboxOptions `json:"options,omitempty"`
	OptionID     *string      `json:"optionId,omitempty"`
	NodeRunID    uuid.UUID    `json:"nodeRunId"`
	Text         string       `json:"text"`
	WhenBusy     string       `json:"whenBusy"`
}

// SseInboxSnapshot、SseInboxMessageCreated、SseInboxMessageRead 是收件箱事件流的数据。
type SseInboxSnapshot struct {
	UnreadTotal int `json:"unreadTotal"`
}

type SseInboxMessageCreated struct {
	Message     InboxMessageSummary `json:"message"`
	UnreadTotal int                 `json:"unreadTotal"`
}

type SseInboxMessageRead struct {
	ID          uuid.UUID        `json:"id"`
	ThreadID    uuid.UUID        `json:"threadId"`
	To          InboxParticipant `json:"to"`
	ReadAt      time.Time        `json:"readAt"`
	UnreadTotal int              `json:"unreadTotal"`
}

// ExternalIdentity 对应 ExternalIdentity。
type ExternalIdentity struct {
	ID        uuid.UUID              `json:"id"`
	Name      string                 `json:"name"`
	Address   string                 `json:"address"`
	CreatedAt time.Time              `json:"createdAt"`
	DeletedAt *time.Time             `json:"deletedAt"`
	ManagedBy *store.IdentityManager `json:"managedBy"`
}

// FromIdentity 转换外部身份。
func FromIdentity(x store.ExternalIdentity) ExternalIdentity {
	return ExternalIdentity{ID: x.ID, Name: x.Name, Address: "@" + x.Name, CreatedAt: x.CreatedAt, DeletedAt: x.DeletedAt, ManagedBy: x.ManagedBy}
}

// ExternalIdentityList 对应 ExternalIdentityList。
type ExternalIdentityList struct {
	Identities []ExternalIdentity `json:"identities"`
}

// ExternalIdentityCreate 是新建外部身份的请求体。
type ExternalIdentityCreate struct {
	Name string `json:"name"`
}

// ExternalToken 对应 ExternalToken。
type ExternalToken struct {
	ID         uuid.UUID  `json:"id"`
	Prefix     string     `json:"prefix"`
	CreatedAt  time.Time  `json:"createdAt"`
	ExpiresAt  *time.Time `json:"expiresAt"`
	RevokedAt  *time.Time `json:"revokedAt"`
	LastUsedAt *time.Time `json:"lastUsedAt"`
}

// FromToken 转换令牌元数据。
func FromToken(t store.ExternalToken) ExternalToken {
	return ExternalToken{ID: t.ID, Prefix: t.Prefix, CreatedAt: t.CreatedAt, ExpiresAt: t.ExpiresAt, RevokedAt: t.RevokedAt, LastUsedAt: t.LastUsedAt}
}

// ExternalTokenList 对应 ExternalTokenList。
type ExternalTokenList struct {
	Tokens []ExternalToken `json:"tokens"`
}

// ExternalTokenCreate 是签发令牌的请求体。
type ExternalTokenCreate struct {
	ExpiresAt *time.Time `json:"expiresAt"`
}

// ExternalTokenIssued 对应 ExternalTokenIssued；Secret 只在签发时返回一次。
type ExternalTokenIssued struct {
	Token  ExternalToken `json:"token"`
	Secret string        `json:"secret"`
}

// InboxOptions distinguishes omitted options from an invalid explicit null.
// Decode errors are fixed text, never a copy of the request value.
type InboxOptions []string

func (o *InboxOptions) UnmarshalJSON(data []byte) error {
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return errors.New("options须为字符串数组")
	}
	var values []string
	if json.Unmarshal(data, &values) != nil {
		return errors.New("options须为字符串数组")
	}
	*o = values
	return nil
}

func validateQuestionInputPresence(data []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	for key := range fields {
		if strings.EqualFold(key, "runId") && key != "runId" {
			return errors.New("runId字段名称无效")
		}
	}
	for _, field := range []string{"optionId", "nodeRunId", "runId"} {
		if value, exists := fields[field]; exists && bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return errors.New(field + "不能为null")
		}
	}
	return nil
}

// Keep unknown-field rejection when custom decoding the new presence checks.
func (in *InboxSendInput) UnmarshalJSON(data []byte) error {
	if err := validateQuestionInputPresence(data); err != nil {
		return err
	}
	type wire InboxSendInput
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode((*wire)(in)); err != nil {
		return err
	}
	if in.RunID != nil && *in.RunID == uuid.Nil {
		return errors.New("runId必须为非零UUID")
	}
	return nil
}
func (in *InboxReplyInput) UnmarshalJSON(data []byte) error {
	if err := validateQuestionInputPresence(data); err != nil {
		return err
	}
	type wire InboxReplyInput
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode((*wire)(in)); err != nil {
		return err
	}
	if in.RunID != nil && *in.RunID == uuid.Nil {
		return errors.New("runId必须为非零UUID")
	}
	return nil
}

// InboxReadInput optionally binds an Agent action to its explicit task turn.
type InboxReadInput struct {
	RunID *uuid.UUID `json:"runId,omitempty"`
}

func (in *InboxReadInput) UnmarshalJSON(data []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil || fields == nil {
		return errors.New("Inbox读取请求须为对象")
	}
	if raw, ok := fields["runId"]; ok && bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return errors.New("runId不能为null")
	}
	type wire InboxReadInput
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode((*wire)(in)); err != nil {
		return err
	}
	if in.RunID != nil && *in.RunID == uuid.Nil {
		return errors.New("runId必须为非零UUID")
	}
	return nil
}

// SseInboxMessageCancelled 对应 SseInboxMessageCancelled：发给 Agent 的一条排队消息被人取消。
type SseInboxMessageCancelled struct {
	ID          uuid.UUID        `json:"id"`
	ThreadID    uuid.UUID        `json:"threadId"`
	From        InboxParticipant `json:"from"`
	To          InboxParticipant `json:"to"`
	CancelledAt time.Time        `json:"cancelledAt"`
	UnreadTotal int              `json:"unreadTotal"`
}
