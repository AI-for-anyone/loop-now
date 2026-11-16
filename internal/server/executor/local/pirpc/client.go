// Package pirpc implements a client for Pi 1.1.0 RPC mode (pi --mode rpc).
//
// Protocol essentials: stdin/stdout are strict JSONL, framed only on '\n'; commands carry an id, responses may arrive
// out of order and are matched by id; events carry no id. A prompt turn ends with agent_settled.
package pirpc

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"loop-now/internal/server/executor/local/runturn"
	"loop-now/internal/server/messageimage"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// ErrExited means the Pi process has exited or its stdout has been closed (shared with other runtimes, see runturn).
var ErrExited = runturn.ErrExited

const (
	// exitGrace is the longest wait after the process exits for stdout/stderr to reach EOF,
	// so a grandchild that inherited the pipes cannot block reading forever.
	exitGrace = 2 * time.Second
	// stderrLimit is the number of bytes the stderr ring buffer keeps.
	stderrLimit = 64 << 10
)

// TurnSink receives the live events of a turn. Calls happen on the reader goroutine; implementations must return
// quickly and must not call Client methods synchronously from a callback.
type TurnSink = runturn.Sink

// Outcome is how a turn ended.
type Outcome = runturn.Outcome

const (
	OutcomeCompleted = runturn.OutcomeCompleted
	OutcomeFailed    = runturn.OutcomeFailed
	OutcomeAborted   = runturn.OutcomeAborted
)

// Error codes.
const (
	ErrorCodePi       = "pi_error"
	ErrorCodePiExited = "pi_exited"
)

// TurnResult is the result of a turn; Pi's ErrorCode is "pi_error", "pi_exited" or empty.
type TurnResult = runturn.Result

// CommandNotices are the notices collected in a turn, in order; Truncated means they were cut off after exceeding the
// limit.
type CommandNotices = runturn.CommandNotices

// State holds the fields of get_state that the platform cares about.
type State struct {
	ThinkingLevel string
	SessionFile   string // empty when there is no session file
	SessionID     string
}

// Client is the client of one Pi RPC process.
type Client struct {
	stdin     io.WriteCloser
	writeMu   sync.Mutex
	pid       int
	stderr    *ringBuffer
	nextID    atomic.Uint64
	busy      atomic.Bool // whether a Prompt is in progress
	malformed atomic.Int64

	mu              sync.Mutex
	pending         map[string]chan *responseFrame
	turn            *turn
	closed          bool // stdout has ended
	observer        Observer
	dialogs         Dialogs
	compactionStart time.Time

	readerDone chan struct{}
	done       chan struct{}
	exitErr    error
}

// Start starts cmd with stdin/stdout pipes (the caller has set Path/Args/Env/Dir/SysProcAttr),
// writing stderr into a bounded ring buffer (and also into cmd.Stderr if the caller set it).
// When cmd.WaitDelay is zero it is set to exitGrace.
func Start(cmd *exec.Cmd) (*Client, error) {
	if cmd.Stdin != nil || cmd.Stdout != nil {
		return nil, errors.New("pirpc: cmd.Stdin and cmd.Stdout must be nil; the client takes them over")
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("pirpc: create the stdin pipe: %w", err)
	}
	pr, pw, err := os.Pipe()
	if err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("pirpc: create the stdout pipe: %w", err)
	}
	cmd.Stdout = pw
	ring := newRingBuffer(stderrLimit)
	if cmd.Stderr != nil {
		cmd.Stderr = io.MultiWriter(ring, cmd.Stderr)
	} else {
		cmd.Stderr = ring
	}
	if cmd.WaitDelay == 0 {
		cmd.WaitDelay = exitGrace
	}
	if err := cmd.Start(); err != nil {
		_ = pr.Close()
		_ = pw.Close()
		_ = stdin.Close()
		return nil, fmt.Errorf("pirpc: start Pi: %w", err)
	}
	// The parent no longer holds the write end, so the read end sees EOF once the child exits.
	_ = pw.Close()
	c := newClient(stdin, pr, cmd.Wait)
	c.pid = cmd.Process.Pid
	c.stderr = ring
	return c, nil
}

// newClient creates a client on the given read and write ends; when wait is nil, Done only waits for the reader
// goroutine to finish (for in-process tests).
func newClient(stdin io.WriteCloser, stdout io.Reader, wait func() error) *Client {
	c := &Client{
		stdin:      stdin,
		pending:    make(map[string]chan *responseFrame),
		readerDone: make(chan struct{}),
		done:       make(chan struct{}),
	}
	go c.readLoop(stdout)
	go c.finish(stdout, wait)
	return c
}

func (c *Client) finish(stdout io.Reader, wait func() error) {
	var err error
	if wait != nil {
		err = wait()
		select {
		case <-c.readerDone:
		case <-time.After(exitGrace):
			// The process has exited but stdout is still held open: force reading to stop.
			if d, ok := stdout.(interface{ SetReadDeadline(time.Time) error }); ok {
				_ = d.SetReadDeadline(time.Now())
			}
			<-c.readerDone
		}
		if cl, ok := stdout.(io.Closer); ok {
			_ = cl.Close()
		}
	} else {
		<-c.readerDone
	}
	c.exitErr = err
	close(c.done)
}

// Done is closed after the process exits and the reader goroutine has finished.
func (c *Client) Done() <-chan struct{} { return c.done }

// ExitErr returns the process's exit error; nil before Done is closed.
func (c *Client) ExitErr() error {
	select {
	case <-c.done:
		return c.exitErr
	default:
		return nil
	}
}

// Pid returns the Pi process ID.
func (c *Client) Pid() int { return c.pid }

// Stderr returns roughly the last 64KB of stderr output.
func (c *Client) Stderr() string {
	if c.stderr == nil {
		return ""
	}
	return c.stderr.String()
}

// CloseStdin closes Pi's stdin, asking it to exit in an orderly way.
func (c *Client) CloseStdin() error { return c.stdin.Close() }

// Call sends one command and waits for the response with the matching id: on success it returns data; on success:false
// it returns an error containing Pi's error text; if the process ends first it returns ErrExited; if ctx is cancelled
// it returns ctx.Err().
func (c *Client) Call(ctx context.Context, typ string, params map[string]any) (json.RawMessage, error) {
	resp, err := c.roundTrip(ctx, c.newID(), typ, params)
	if err != nil {
		return nil, err
	}
	if !resp.Success {
		return nil, fmt.Errorf("pirpc: command %s failed: %s", typ, resp.Error)
	}
	return resp.Data, nil
}

// GetState queries the session state.
func (c *Client) GetState(ctx context.Context) (State, error) {
	data, err := c.Call(ctx, "get_state", nil)
	if err != nil {
		return State{}, err
	}
	var s struct {
		ThinkingLevel string `json:"thinkingLevel"`
		SessionFile   string `json:"sessionFile"`
		SessionID     string `json:"sessionId"`
	}
	if len(data) > 0 {
		if err := json.Unmarshal(data, &s); err != nil {
			return State{}, fmt.Errorf("pirpc: parse the get_state response: %w", err)
		}
	}
	return State{SessionFile: s.SessionFile, SessionID: s.SessionID, ThinkingLevel: s.ThinkingLevel}, nil
}

// SetThinkingLevel checks the actual post-clamp state; an acknowledgement
// alone must never turn an unsupported requested level into silent success.
func (c *Client) SetThinkingLevel(ctx context.Context, level string) error {
	if _, err := c.Call(ctx, "set_thinking_level", map[string]any{"level": level}); err != nil {
		return err
	}
	state, err := c.GetState(ctx)
	if err != nil {
		return err
	}
	if state.ThinkingLevel != level {
		return fmt.Errorf("pirpc: the actual thinking level does not match the setting for this turn")
	}
	return nil
}

// CompactResult is the change in context tokens from a manual compaction. EstimatedTokensAfter is Pi's estimate.
// A non-empty Skipped means Pi did not compact (CompactTooSmall, CompactAlreadyDone, CompactCancelled); all token
// counts are then 0.
type CompactResult struct {
	TokensBefore         int64
	EstimatedTokensAfter int64
	Skipped              string
}

// Reasons Pi did not compact. Pi reports these cases as a failed compact command (compact in agent-session.js); the
// platform treats them as a normal result. CompactCancelled is when Pi says "Compaction cancelled" but the platform did
// not send abort: an extension cancelled this compaction in session_before_compact. A compaction the platform itself
// aborted returns ErrCompactAborted.
const (
	CompactTooSmall    = "too_small"
	CompactAlreadyDone = "already_compacted"
	CompactCancelled   = "cancelled_by_extension"
)

// ErrCompactAborted means a platform Abort cancelled a manual compaction in progress.
var ErrCompactAborted = errors.New("pirpc: the manual compaction was cancelled")

// compactSkipped tells from Pi's error text whether it is a normal "did not compact" result.
func compactSkipped(message string) string {
	switch {
	case strings.HasPrefix(message, "Nothing to compact"):
		return CompactTooSmall
	case message == "Already compacted":
		return CompactAlreadyDone
	case message == "Compaction cancelled":
		return CompactCancelled
	}
	return ""
}

// Compact compacts the current session. It shares the mutual exclusion and event ownership with Prompt, so Abort can
// cancel this compaction.
func (c *Client) Compact(ctx context.Context, customInstructions string) (CompactResult, error) {
	if !c.busy.CompareAndSwap(false, true) {
		return CompactResult{}, errors.New("pirpc: a prompt or compact is already in progress")
	}
	defer c.busy.Store(false)
	if err := ctx.Err(); err != nil {
		return CompactResult{}, err
	}
	t := newTurn(c.newID(), nopSink{})
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return CompactResult{}, ErrExited
	}
	c.turn = t
	c.mu.Unlock()
	// The compact response only comes back after compaction finishes; Abort must not wait for this response first, or
	// it could not cancel the compaction.
	t.responseReceived(true)
	defer c.clearTurn(t)
	params := map[string]any{}
	if customInstructions != "" {
		params["customInstructions"] = customInstructions
	}
	resp, err := c.roundTrip(ctx, t.promptID, "compact", params)
	if err != nil {
		return CompactResult{}, err
	}
	if !resp.Success {
		if reason := compactSkipped(resp.Error); reason != "" {
			if reason == CompactCancelled && t.abortRequested.Load() {
				return CompactResult{}, ErrCompactAborted
			}
			return CompactResult{Skipped: reason}, nil
		}
		return CompactResult{}, fmt.Errorf("pirpc: command compact failed: %s", resp.Error)
	}
	var data struct {
		TokensBefore         int64 `json:"tokensBefore"`
		EstimatedTokensAfter int64 `json:"estimatedTokensAfter"`
	}
	if err := json.Unmarshal(resp.Data, &data); err != nil {
		return CompactResult{}, fmt.Errorf("pirpc: parse the compact response: %w", err)
	}
	return CompactResult{TokensBefore: data.TokensBefore, EstimatedTokensAfter: data.EstimatedTokensAfter}, nil
}

// Prompt sends a prompt and consumes events until the turn ends (agent_settled), calling sink along the way. Only one
// Prompt or Compact is allowed at a time. If the prompt could not be sent it returns ErrExited; if the process exits
// after it was sent it returns a result with Outcome failed / pi_exited and a nil error. Cancelling ctx only stops
// waiting; it does not abort Pi.
func (c *Client) Prompt(ctx context.Context, text string, sink TurnSink, images ...messageimage.Content) (TurnResult, error) {
	if sink == nil {
		sink = nopSink{}
	}
	if !c.busy.CompareAndSwap(false, true) {
		return TurnResult{}, errors.New("pirpc: a prompt is already in progress")
	}
	defer c.busy.Store(false)
	if err := ctx.Err(); err != nil {
		return TurnResult{}, err
	}

	t := newTurn(c.newID(), sink)
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return TurnResult{}, ErrExited
	}
	c.turn = t
	c.mu.Unlock()
	defer c.clearTurn(t)

	input := map[string]any{"message": text}
	if len(images) > 0 {
		input["images"] = images
	}
	resp, err := c.roundTrip(ctx, t.promptID, "prompt", input)
	if err != nil {
		var ns *notSentError
		if errors.Is(err, ErrExited) && !errors.As(err, &ns) {
			// The process exited while waiting for the response: the reader goroutine has already ended the turn as
			// pi_exited.
			return awaitResult(ctx, t)
		}
		return TurnResult{}, err
	}
	if !resp.Success {
		msg := resp.Error
		if msg == "" {
			msg = "Pi rejected the prompt."
		}
		return TurnResult{Outcome: OutcomeFailed, ErrorCode: ErrorCodePi, ErrorMessage: msg}, nil
	}
	if disposition(resp.Data) == "handled" {
		res := TurnResult{Outcome: OutcomeCompleted, Handled: true}
		if notices, cut := t.commandNotices(); len(notices) > 0 || cut {
			res.Notices = &CommandNotices{Items: notices, Truncated: cut}
		}
		return res, nil
	}
	return awaitResult(ctx, t)
}

// Abort sends clear_queue and then abort, and waits for the abort response (Pi back to idle).
// If the prompt has been sent but its response has not arrived yet, it waits for that response first, so the abort does
// not come before the run starts and have no effect. It may be called concurrently with Prompt from another goroutine.
func (c *Client) Abort(ctx context.Context) error {
	c.mu.Lock()
	t := c.turn
	c.mu.Unlock()
	if t != nil {
		t.abortRequested.Store(true)
		select {
		case <-t.responded:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if _, err := c.Call(ctx, "clear_queue", nil); err != nil {
		return err
	}
	_, err := c.Call(ctx, "abort", nil)
	return err
}

func awaitResult(ctx context.Context, t *turn) (TurnResult, error) {
	select {
	case res := <-t.result:
		return res, nil
	case <-ctx.Done():
		return TurnResult{}, ctx.Err()
	}
}

func (c *Client) clearTurn(t *turn) {
	t.detach()
	c.mu.Lock()
	if c.turn == t {
		c.turn = nil
	}
	c.mu.Unlock()
}

func (c *Client) newID() string {
	return "lp-" + strconv.FormatUint(c.nextID.Add(1), 10)
}

// notSentError means a command could not be written to Pi's stdin.
type notSentError struct{ err error }

func (e *notSentError) Error() string { return "pirpc: write the command: " + e.err.Error() }
func (e *notSentError) Unwrap() []error {
	return []error{ErrExited, e.err}
}

func (c *Client) roundTrip(ctx context.Context, id, typ string, params map[string]any) (*responseFrame, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	ch := make(chan *responseFrame, 1)
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, ErrExited
	}
	c.pending[id] = ch
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
	}()

	msg := make(map[string]any, len(params)+2)
	for k, v := range params {
		msg[k] = v
	}
	msg["id"] = id
	msg["type"] = typ
	line, err := json.Marshal(msg)
	if err != nil {
		return nil, fmt.Errorf("pirpc: encode command %s: %w", typ, err)
	}
	line = append(line, '\n')
	c.writeMu.Lock()
	_, err = c.stdin.Write(line)
	c.writeMu.Unlock()
	if err != nil {
		return nil, &notSentError{err: err}
	}

	select {
	case resp, ok := <-ch:
		if !ok {
			return nil, ErrExited
		}
		return resp, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func disposition(data json.RawMessage) string {
	if len(data) == 0 {
		return ""
	}
	var d struct {
		Disposition string `json:"disposition"`
	}
	_ = json.Unmarshal(data, &d)
	return d.Disposition
}

type nopSink struct{}

func (nopSink) TextDelta(string)               {}
func (nopSink) ToolStarted(string, string)     {}
func (nopSink) ToolEnded(string, string, bool) {}

// readLoop reads stdout framed on '\n' until EOF or a read error.
func (c *Client) readLoop(r io.Reader) {
	defer close(c.readerDone)
	br := bufio.NewReaderSize(r, 64<<10)
	for {
		line, err := br.ReadBytes('\n')
		if len(line) > 0 {
			c.handleLine(line)
		}
		if err != nil {
			break
		}
	}
	c.shutdown()
}

func (c *Client) shutdown() {
	c.mu.Lock()
	c.closed = true
	pending := c.pending
	c.pending = nil
	t := c.turn
	c.mu.Unlock()
	for _, ch := range pending {
		close(ch)
	}
	if t != nil {
		t.exited()
	}
}

func (c *Client) activeTurn() *turn {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.turn
}

// malformedLines returns the number of unparseable lines that were skipped.
func (c *Client) malformedLines() int64 { return c.malformed.Load() }
