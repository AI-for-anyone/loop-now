package executor

import (
	"context"
	"errors"

	"github.com/google/uuid"
)

// Permission modes (Agent.permissionMode), from asking the most to asking nothing.
const (
	ModeSupervised  = "supervised"
	ModeAcceptEdits = "accept_edits"
	ModeAuto        = "auto"
	ModeFull        = "full"
)

// Permission request kinds (PermissionRequest.kind).
const (
	PermissionCommand    = "command"
	PermissionFileChange = "file_change"
	PermissionTool       = "tool"
	PermissionNetwork    = "network"
	PermissionOther      = "other"
)

// MaxPermissionDetail is how much of a request's detail (a command line, a diff, a tool's input) is kept.
const MaxPermissionDetail = 16 << 10

// PermissionAsk is something a runtime asks to do, waiting for a person.
type PermissionAsk struct {
	// AgentID is the executor's Agent: a regular Agent, or the task instance a task turn runs on. RunID is the run, or
	// the task turn.
	AgentID, RunID uuid.UUID
	Runtime        string
	Kind           string
	Title          string
	Detail         string
}

// PermissionAnswer is a person's answer; Reason goes with a rejection (what the runtime is told).
type PermissionAnswer struct {
	Approved bool
	Reason   string
}

// PermissionAsker records requests and waits for their answers.
type PermissionAsker interface {
	// AskPermission records the request and waits until a person answers it. When ctx ends first, the request is
	// cancelled for the reason context.Cause(ctx) gives (a *PermissionCancel; anything else counts as the reply
	// ending), and the answer is a rejection.
	AskPermission(ctx context.Context, q PermissionAsk) PermissionAnswer
}

// PermissionCancel is why a pending request was cancelled: its Reason is PermissionRequest.reason.
type PermissionCancel struct{ Reason string }

func (e *PermissionCancel) Error() string { return "permission request cancelled: " + e.Reason }

// The causes a driver cancels a pending request with.
var (
	CancelReplyStopped  = &PermissionCancel{Reason: "reply_stopped"}
	CancelReplyEnded    = &PermissionCancel{Reason: "reply_ended"}
	CancelProcessExited = &PermissionCancel{Reason: "process_exited"}
)

// CancelReason is the reason a request waiting on ctx is cancelled for: the PermissionCancel cause, else reply_ended.
func CancelReason(ctx context.Context) string {
	var c *PermissionCancel
	if errors.As(context.Cause(ctx), &c) {
		return c.Reason
	}
	return CancelReplyEnded.Reason
}

// ClipDetail keeps at most MaxPermissionDetail bytes of a detail, cut at a character boundary.
func ClipDetail(s string) string {
	if len(s) <= MaxPermissionDetail {
		return s
	}
	cut := MaxPermissionDetail
	for cut > 0 && (s[cut]&0xC0) == 0x80 {
		cut--
	}
	return s[:cut]
}
