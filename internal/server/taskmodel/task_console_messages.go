package taskmodel

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"

	"github.com/google/uuid"
)

type TaskNodeMessageInput struct {
	ImageIDsPresent   bool        `json:"-"`
	ImageIDs          []uuid.UUID `json:"imageIds,omitempty"`
	ActionID          uuid.UUID   `json:"actionId"`
	ExpectedAgentID   uuid.UUID   `json:"expectedAgentId"`
	ExpectedRuntimeID uuid.UUID   `json:"expectedRuntimeId"`
	Text              string      `json:"text"`
	WhenBusy          string      `json:"whenBusy"`
}

func (in *TaskNodeMessageInput) UnmarshalJSON(b []byte) error {
	type wire TaskNodeMessageInput
	next := wire{WhenBusy: "queue"}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err := d.Decode(&next); err != nil {
		return err
	}
	if d.Decode(new(any)) != io.EOF {
		return errors.New("invalid node message")
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(b, &fields) != nil || fields == nil {
		return errors.New("invalid node message")
	}
	if raw, ok := fields["whenBusy"]; ok && bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return errors.New("invalid whenBusy")
	}

	if raw, ok := fields["imageIds"]; ok && bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return errors.New("invalid imageIds")
	}
	if fields["text"] == nil || bytes.Equal(bytes.TrimSpace(fields["text"]), []byte("null")) {
		return errors.New("invalid text")
	}
	next.ImageIDsPresent = fields["imageIds"] != nil
	*in = TaskNodeMessageInput(next)
	return nil
}

type TaskNodeMessageCapability struct {
	TaskID       uuid.UUID `json:"taskId"`
	NodeRunID    uuid.UUID `json:"nodeRunId"`
	AgentID      uuid.UUID `json:"agentId"`
	RuntimeID    uuid.UUID `json:"runtimeId"`
	CanSend      bool      `json:"canSend"`
	Reason       *string   `json:"reason"`
	HandlingHint *string   `json:"handlingHint"`
}

type TaskNodeMessageReceipt struct {
	TaskID    uuid.UUID `json:"taskId"`
	NodeRunID uuid.UUID `json:"nodeRunId"`
	AgentID   uuid.UUID `json:"agentId"`
	RuntimeID uuid.UUID `json:"runtimeId"`
	RunID     uuid.UUID `json:"runId"`
	Message   Message   `json:"message"`
	Status    string    `json:"status"`
}

type TaskNodeMessageConflictDetails struct {
	TaskID            uuid.UUID  `json:"taskId"`
	ExpectedNodeRunID uuid.UUID  `json:"expectedNodeRunId"`
	CurrentNodeRunID  *uuid.UUID `json:"currentNodeRunId"`
	ExpectedAgentID   uuid.UUID  `json:"expectedAgentId"`
	ExpectedRuntimeID uuid.UUID  `json:"expectedRuntimeId"`
	TaskVersion       int64      `json:"taskVersion"`
	Reason            string     `json:"reason"`
	// Set only for a single queued message (promote-to-interrupt).
	MessageID *uuid.UUID `json:"messageId,omitempty"`
	RunID     *uuid.UUID `json:"runId,omitempty"`
	RunStatus *string    `json:"runStatus,omitempty"`
}
