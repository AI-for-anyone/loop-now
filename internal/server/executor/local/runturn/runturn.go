// Package runturn is the shared "turn" vocabulary between the local executor and the runtime clients (Pi RPC, Claude
// Code): the receiver of live events, a turn's result and outcome, and the process-exited error. The executor handles a
// turn only through these types and does not care which runtime it is.
package runturn

import "errors"

// ErrExited means the runtime process has exited or its stdout has been closed.
var ErrExited = errors.New("运行时进程已退出")

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
