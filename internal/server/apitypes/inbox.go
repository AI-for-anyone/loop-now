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

// Event names of the Inbox event stream.
const (
	EventInboxSnapshot  = "snapshot"
	EventInboxCreated   = "inbox.message.created"
	EventInboxRead      = "inbox.message.read"
	EventInboxCancelled = "inbox.message.cancelled"
)

// Display names of deleted participants.
const (
	DeletedIdentityName = "Deleted identity"
	DeletedAgentName    = "Deleted Agent"
)

// Whoami mirrors the Whoami schema.
type Whoami struct {
	TaskID    *uuid.UUID `json:"taskId,omitempty"`
	RuntimeID *uuid.UUID `json:"runtimeId,omitempty"`
	Kind      string     `json:"kind"`
	Address   *string    `json:"address"`
	Name      string     `json:"name"`
}

// FromPrincipal converts the current identity.
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

// InboxParticipant mirrors the InboxParticipant schema.
type InboxParticipant struct {
	Kind      string     `json:"kind"`
	ID        *uuid.UUID `json:"id"`
	Address   *string    `json:"address"`
	Name      string     `json:"name"`
	Deleted   bool       `json:"deleted"`
	PluginID  *string    `json:"pluginId"`
	ChannelID *string    `json:"channelId"`
}

// FromParty converts a participant: the address is built from the current name; for a deleted participant the display
// name becomes "已删除身份" ("Deleted identity") or "已删除 Agent" ("Deleted Agent").
func FromParty(v store.PartyView) InboxParticipant {
	out := InboxParticipant{Kind: v.Kind, Deleted: v.Deleted}
	switch v.Kind {
	case "platform":
		addr := "@platform"
		out.Address = &addr
		out.Name = "Platform"
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

// InboxMessageSummary mirrors the InboxMessageSummary schema.
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
	// CancelledAt is non-null when someone cancelled the message; the body the receiving Agent itself reads is then a
	// cancellation note, without the original text or images.
	CancelledAt *time.Time `json:"cancelledAt"`
	// Task is the task instance the message belongs to; null when it has nothing to do with a task instance.
	Task *store.InboxTaskRef `json:"task"`
}

// InboxMessage mirrors the InboxMessage schema: the summary plus the full text.
type InboxMessage struct {
	InboxMessageSummary
	Text string `json:"text"`
}

// FromInboxSummary converts a message summary.
func FromInboxSummary(m store.InboxMessage) InboxMessageSummary {
	return InboxMessageSummary{Images: m.Images, Question: m.Question, AnswerTo: m.AnswerTo, Notice: m.Notice, ID: m.ID, ThreadID: m.ThreadID, InReplyTo: m.InReplyTo, From: FromParty(m.From), To: FromParty(m.To),
		Preview: m.Preview, WhenBusy: strPtr(m.WhenBusy), CreatedAt: m.CreatedAt, ReadAt: m.ReadAt, CancelledAt: m.CancelledAt, Task: m.Task}
}

// FromInboxMessage converts a full message.
func FromInboxMessage(m store.InboxMessage) InboxMessage {
	return InboxMessage{InboxMessageSummary: FromInboxSummary(m), Text: m.Text}
}

// InboxMessagePage mirrors the InboxMessagePage schema.
type InboxMessagePage struct {
	Messages   []InboxMessageSummary `json:"messages"`
	NextCursor *string               `json:"nextCursor"`
}

// FromInboxPage converts a page of messages.
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

// InboxConversationPage mirrors the InboxConversationPage schema: a page of full messages.
type InboxConversationPage struct {
	Messages   []InboxMessage `json:"messages"`
	NextCursor *string        `json:"nextCursor"`
}

// FromConversationPage converts a page of full messages.
func FromConversationPage(p store.InboxPage) InboxConversationPage {
	out := InboxConversationPage{Messages: make([]InboxMessage, 0, len(p.Messages)), NextCursor: encodeCursor(p.Next)}
	for _, m := range p.Messages {
		out.Messages = append(out.Messages, FromInboxMessage(m))
	}
	return out
}

// InboxPending mirrors the InboxPending schema; InboxPendingItem, InboxPendingAgentCount and InboxPendingTaskCount are
// its items.
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

// FromPending converts the pending lists and totals them per sending regular Agent and per task; groups are ordered by
// first appearance (the newest message).
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

// InboxThreadSummary mirrors the InboxThreadSummary schema.
type InboxThreadSummary struct {
	ThreadID     uuid.UUID           `json:"threadId"`
	Participants []InboxParticipant  `json:"participants"`
	LastMessage  InboxMessageSummary `json:"lastMessage"`
	UnreadCount  int                 `json:"unreadCount"`
	UpdatedAt    time.Time           `json:"updatedAt"`
}

// InboxThreadPage mirrors the InboxThreadPage schema.
type InboxThreadPage struct {
	Threads     []InboxThreadSummary `json:"threads"`
	UnreadTotal int                  `json:"unreadTotal"`
	NextCursor  *string              `json:"nextCursor"`
}

// FromThreadPage converts a page of threads.
func FromThreadPage(p store.InboxThreadPage, unreadTotal int) InboxThreadPage {
	out := InboxThreadPage{Threads: make([]InboxThreadSummary, 0, len(p.Threads)), UnreadTotal: unreadTotal, NextCursor: encodeCursor(p.Next)}
	for _, t := range p.Threads {
		out.Threads = append(out.Threads, InboxThreadSummary{ThreadID: t.ThreadID,
			Participants: []InboxParticipant{FromParty(t.Root.From), FromParty(t.Root.To)},
			LastMessage:  FromInboxSummary(t.Last), UnreadCount: t.UnreadCount, UpdatedAt: t.Last.CreatedAt})
	}
	return out
}

// InboxThread mirrors the InboxThread schema.
type InboxThread struct {
	ThreadID uuid.UUID      `json:"threadId"`
	Messages []InboxMessage `json:"messages"`
}

// InboxUnreadTotal mirrors the InboxUnreadTotal schema.
type InboxUnreadTotal struct {
	UnreadTotal int `json:"unreadTotal"`
}

// InboxSendInput and InboxReplyInput are the request bodies for sending and replying.
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

// SseInboxSnapshot, SseInboxMessageCreated and SseInboxMessageRead are the data of the Inbox event stream.
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

// ExternalIdentity mirrors the ExternalIdentity schema.
type ExternalIdentity struct {
	ID        uuid.UUID              `json:"id"`
	Name      string                 `json:"name"`
	Address   string                 `json:"address"`
	CreatedAt time.Time              `json:"createdAt"`
	DeletedAt *time.Time             `json:"deletedAt"`
	ManagedBy *store.IdentityManager `json:"managedBy"`
}

// FromIdentity converts an external identity.
func FromIdentity(x store.ExternalIdentity) ExternalIdentity {
	return ExternalIdentity{ID: x.ID, Name: x.Name, Address: "@" + x.Name, CreatedAt: x.CreatedAt, DeletedAt: x.DeletedAt, ManagedBy: x.ManagedBy}
}

// ExternalIdentityList mirrors the ExternalIdentityList schema.
type ExternalIdentityList struct {
	Identities []ExternalIdentity `json:"identities"`
}

// ExternalIdentityCreate is the request body for creating an external identity.
type ExternalIdentityCreate struct {
	Name string `json:"name"`
}

// ExternalToken mirrors the ExternalToken schema.
type ExternalToken struct {
	ID         uuid.UUID  `json:"id"`
	Prefix     string     `json:"prefix"`
	CreatedAt  time.Time  `json:"createdAt"`
	ExpiresAt  *time.Time `json:"expiresAt"`
	RevokedAt  *time.Time `json:"revokedAt"`
	LastUsedAt *time.Time `json:"lastUsedAt"`
}

// FromToken converts token metadata.
func FromToken(t store.ExternalToken) ExternalToken {
	return ExternalToken{ID: t.ID, Prefix: t.Prefix, CreatedAt: t.CreatedAt, ExpiresAt: t.ExpiresAt, RevokedAt: t.RevokedAt, LastUsedAt: t.LastUsedAt}
}

// ExternalTokenList mirrors the ExternalTokenList schema.
type ExternalTokenList struct {
	Tokens []ExternalToken `json:"tokens"`
}

// ExternalTokenCreate is the request body for issuing a token.
type ExternalTokenCreate struct {
	ExpiresAt *time.Time `json:"expiresAt"`
}

// ExternalTokenIssued mirrors the ExternalTokenIssued schema; Secret is returned only once, when the token is issued.
type ExternalTokenIssued struct {
	Token  ExternalToken `json:"token"`
	Secret string        `json:"secret"`
}

// InboxOptions distinguishes omitted options from an invalid explicit null.
// Decode errors are fixed text, never a copy of the request value.
type InboxOptions []string

func (o *InboxOptions) UnmarshalJSON(data []byte) error {
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return errors.New("options must be an array of strings")
	}
	var values []string
	if json.Unmarshal(data, &values) != nil {
		return errors.New("options must be an array of strings")
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
			return errors.New("the runId field name has the wrong case (field names are case-sensitive)")
		}
	}
	for _, field := range []string{"optionId", "nodeRunId", "runId"} {
		if value, exists := fields[field]; exists && bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return errors.New(field + " must not be null")
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
		return errors.New("runId must be a non-zero UUID")
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
		return errors.New("runId must be a non-zero UUID")
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
		return errors.New("the Inbox read request must be a JSON object")
	}
	if raw, ok := fields["runId"]; ok && bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return errors.New("runId must not be null")
	}
	type wire InboxReadInput
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode((*wire)(in)); err != nil {
		return err
	}
	if in.RunID != nil && *in.RunID == uuid.Nil {
		return errors.New("runId must be a non-zero UUID")
	}
	return nil
}

// SseInboxMessageCancelled mirrors the SseInboxMessageCancelled schema: someone cancelled a queued message to an Agent.
type SseInboxMessageCancelled struct {
	ID          uuid.UUID        `json:"id"`
	ThreadID    uuid.UUID        `json:"threadId"`
	From        InboxParticipant `json:"from"`
	To          InboxParticipant `json:"to"`
	CancelledAt time.Time        `json:"cancelledAt"`
	UnreadTotal int              `json:"unreadTotal"`
}
