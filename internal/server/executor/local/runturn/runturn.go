// Package runturn is the shared "turn" vocabulary between the local executor and the runtime clients (Pi RPC, Claude
// Code): the receiver of live events, a turn's result and outcome, the process-exited error, and the questions and
// notices a runtime raises during a turn. The executor handles a turn only through these types and does not care which
// runtime it is.
package runturn

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

// ErrExited means the runtime process has exited or its stdout has been closed.
var ErrExited = errors.New("the runtime process has exited")

// Sink receives the live events of a turn. Calls happen on the client's reader goroutine; implementations must return
// quickly and must not call the client's methods synchronously from a callback.
type Sink interface {
	TextDelta(delta string)
	ToolStarted(toolCallID, toolName string)
	ToolEnded(toolCallID, toolName string, isError bool)
}

// Outcome is how a turn ended.
type Outcome string

const (
	OutcomeCompleted Outcome = "completed"
	OutcomeFailed    Outcome = "failed"
	OutcomeAborted   Outcome = "aborted"
)

// Result is the result of a turn.
type Result struct {
	Outcome      Outcome
	Text         string // final reply text
	ErrorCode    string // failure reason (RunErrorCode), empty on success
	ErrorMessage string
	// Handled means the runtime handled this message itself (a Pi extension command): it was not passed to the model
	// and there is no reply.
	Handled bool
	// Notices are the messages (the command's output) extensions gave with ctx.ui.notify during this turn when Handled
	// is set; a pointer keeps Result comparable.
	Notices *CommandNotices
}

// CommandNotices are the notices collected in a turn, in order; Truncated means they were cut off after exceeding the
// limit.
type CommandNotices struct {
	Items     []CommandNotice
	Truncated bool
}

// CommandNotice is one message an extension gave with ctx.ui.notify during this turn (the extension's original text,
// plain text).
type CommandNotice struct {
	Level   string `json:"level"` // info / warning / error
	Message string `json:"message"`
}

// DialogRequest is one question a runtime asks during a turn that needs a person to answer (for Pi, an extension's
// confirm, select or input).
type DialogRequest struct {
	ID          string
	Method      string // confirm, select, input
	Title       string
	Message     string
	Placeholder string
	Options     []string
	Timeout     time.Duration // the request's own timeout; zero when it has none
}

// DialogResponse is the answer sent back to the runtime: Cancelled, or Confirmed for confirm, or Value for select and
// input. Outcome is recorded in the run event's answered field (answered, cancelled, expired); QuestionID is the
// matching console question.
type DialogResponse struct {
	Cancelled  bool
	Confirmed  bool
	Value      string
	Outcome    string
	QuestionID *uuid.UUID
}

// Notice is a runtime event that is useful for run records: automatic retries, context compaction, extension UI calls.
type Notice struct {
	Type string // see the Notice constants
	Data map[string]any
}

// Notice types.
const (
	NoticeRetryStarted = "retry_started"
	NoticeRetryEnded   = "retry_ended"
	NoticeCompaction   = "compaction"
	// NoticeExtensionUI is an extension's ctx.ui call: notifications, status, and dialogs the platform has answered
	// with the default value.
	NoticeExtensionUI = "extension_ui"
)
