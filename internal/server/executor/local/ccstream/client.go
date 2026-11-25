// Package ccstream drives a long-running Claude Code process: claude -p --input-format stream-json --output-format
// stream-json --verbose --include-partial-messages. One process handles many turns in a row: each turn writes one user
// message to stdin and reads stdout until that turn's result. An interrupt first sends a control_request (interrupt),
// and sends SIGINT if no confirmation arrives. Unknown types in the output are always ignored, never treated as errors.
package ccstream

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"loop-now/internal/server/executor"
	"loop-now/internal/server/executor/local/runturn"
	"loop-now/internal/server/messageimage"
)

const (
	// exitGrace is the longest wait after the process exits for stdout/stderr to reach EOF, so a grandchild that
	// inherited the pipes cannot block reading forever.
	exitGrace = 2 * time.Second
	// stderrLimit is the number of bytes the stderr ring buffer keeps.
	stderrLimit = 64 << 10
	// defaultInterruptGrace is the default wait for confirmation after sending the interrupt control message.
	defaultInterruptGrace = 2 * time.Second
	// maxLine is the limit for one line of output (images and long tool results are all on one line).
	maxLine = 64 << 20
	// exitWait is how long a new message waits for a process that is exiting (sent SIGINT or already ended) to exit;
	// after the timeout the process is ended.
	exitWait = 5 * time.Second
)

// Options are the client's parameters.
type Options struct {
	// InterruptGrace is how long to wait for control_response after sending the interrupt control message; after the
	// timeout SIGINT is sent instead. 0 means 2 seconds.
	InterruptGrace time.Duration
	// InitTimeout is the limit on waiting for system/init after the process writes its first message: on timeout it
	// counts as a start failure and the process is ended. 0 means no limit.
	InitTimeout time.Duration
	// Kill ends the process (together with its process group); nil means only the process itself is ended.
	Kill func(pid int) error
	// MaxLine is the limit for one line of output; 0 means 64 MiB (images and long tool results are all on one line).
	MaxLine int
	// SelfQuiet ends a turn Claude Code began by itself once no output has come for this long (its result may be
	// lost), so the next message does not wait for it for good; 0 means 10 minutes.
	SelfQuiet time.Duration
}

// Observer receives usage and events whether or not a turn is in progress; inTurn tells whether it happened during a
// turn. Calls happen on the reader goroutine; implementations must return quickly and must not call Client methods
// synchronously from a callback.
type Observer interface {
	// Usage is the per-model usage given at the end of a turn (model_usage_since_last_result in result, one per model).
	Usage(u Usage, inTurn bool)
	// RateLimit is the rate_limit_info of rate_limit_event, passed on as is.
	RateLimit(info map[string]any, inTurn bool)
}

// BackgroundObserver is an Observer that also hears of background tasks: work Claude Code runs in the background (a
// Bash command with run_in_background, a sub-agent) that outlives the turn that started it.
type BackgroundObserver interface {
	// TaskStarted is a task moved to the background (once per task).
	TaskStarted(t Task)
	// TaskEnded is its end (once per task).
	TaskEnded(e TaskEnd)
}

// Task is a background task as Claude Code reports it. Command is the Bash command that started it, when it is one.
type Task struct {
	ID, ToolUseID, Type, Description, Command string
}

// TaskEnd is how a background task ended: Status is completed, failed or stopped; OutputFile is where Claude Code wrote
// its output; Summary is Claude Code's one-line account.
type TaskEnd struct {
	ID, Status, OutputFile, Summary string
}

// Usage is one model's usage in a turn.
type Usage struct {
	Model      string
	Input      int64
	Output     int64
	CacheRead  int64
	CacheWrite int64
	Reasoning  *int64
}

// Init holds the fields of the process's system/init report that the platform cares about.
type Init struct {
	SessionID  string
	Model      string
	Version    string
	Tools      []string
	Skills     []string
	MCPServers []MCPServer
}

// MCPServer is the state of one MCP server in init.
type MCPServer struct {
	Name   string `json:"name"`
	Status string `json:"status"`
}

// ResultInfo holds the raw fields of the latest turn's result, so the caller can further work out the failure reason
// (for example, not logged in).
type ResultInfo struct {
	Subtype        string
	IsError        bool
	TerminalReason string
	APIErrorStatus *int
	Result         string
}

// Client is the client of one Claude Code process.
type Client struct {
	o       Options
	stdin   io.WriteCloser
	writeMu sync.Mutex
	process *os.Process
	pid     int
	stderr  *ringBuffer
	nextID  atomic.Uint64

	mu       sync.Mutex
	turn     *turn
	observer Observer
	// selfTurns receives the turns Claude Code begins by itself (its reply after background work ended); nil drops them.
	selfTurns func() runturn.SelfTurn
	// commands are the Bash commands of recent tool calls, by tool_use id (to label background tasks); tasks are the
	// background tasks reported, foreground the tasks not (yet) in the background, ended those whose end was reported.
	// All are used by the reader goroutine only.
	commands   map[string]string
	tasks      map[string]bool
	foreground map[string]Task
	ended      map[string]bool
	init       *Init
	last       ResultInfo
	controls   map[string]chan bool // request_id of an interrupt control message -> whether it succeeded
	// permit answers can_use_tool requests; asking cancels each request still waiting for its answer, by request_id.
	permit  PermissionFunc
	asking  map[string]context.CancelCauseFunc
	closed  bool // stdout has ended
	exiting bool // SIGINT sent or process ended: it is sure to exit, so no new messages are written

	done    chan struct{}
	exitErr error
}

// Start starts cmd with stdin/stdout pipes (the caller has set Path/Args/Env/Dir/SysProcAttr), writing stderr into a
// bounded ring buffer. When cmd.WaitDelay is zero it is set to exitGrace.
func Start(cmd *exec.Cmd, o Options) (*Client, error) {
	if cmd.Stdin != nil || cmd.Stdout != nil {
		return nil, errors.New("ccstream: cmd.Stdin and cmd.Stdout must be nil; the client takes them over")
	}
	if o.InterruptGrace <= 0 {
		o.InterruptGrace = defaultInterruptGrace
	}
	if o.MaxLine <= 0 {
		o.MaxLine = maxLine
	}
	if o.SelfQuiet <= 0 {
		o.SelfQuiet = 10 * time.Minute
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	ring := newRingBuffer(stderrLimit)
	if cmd.Stderr != nil {
		cmd.Stderr = io.MultiWriter(cmd.Stderr, ring)
	} else {
		cmd.Stderr = ring
	}
	if cmd.WaitDelay == 0 {
		cmd.WaitDelay = exitGrace
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	c := &Client{o: o, stdin: stdin, process: cmd.Process, pid: cmd.Process.Pid, stderr: ring, controls: map[string]chan bool{}, done: make(chan struct{}),
		commands: map[string]string{}, tasks: map[string]bool{}, foreground: map[string]Task{}, ended: map[string]bool{}}
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		c.readLoop(stdout)
	}()
	go func() {
		err := cmd.Wait()
		select {
		case <-readerDone:
		case <-time.After(exitGrace):
		}
		// Announce that the process has exited (Done) first, then end the turn in progress: by the time the caller gets
		// this turn's result, the process is known to be gone.
		finish := c.shutdown(err)
		close(c.done)
		finish()
	}()
	return c, nil
}

// Done is closed after the process exits (and its output has been read in full or the grace period has passed).
func (c *Client) Done() <-chan struct{} { return c.done }

// ExitErr returns the process's exit error; nil when the process has not exited or exited normally.
func (c *Client) ExitErr() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.exitErr
}

// Pid returns the process ID.
func (c *Client) Pid() int { return c.pid }

// Stderr returns the end of the error output.
func (c *Client) Stderr() string { return c.stderr.String() }

// CloseStdin closes stdin: claude exits after finishing the turn at hand.
func (c *Client) CloseStdin() error { return c.stdin.Close() }

// SetObserver sets the observer.
func (c *Client) SetObserver(o Observer) {
	c.mu.Lock()
	c.observer = o
	c.mu.Unlock()
}

// SetSelfTurns sets where the turns Claude Code begins by itself go.
func (c *Client) SetSelfTurns(f func() runturn.SelfTurn) {
	c.mu.Lock()
	c.selfTurns = f
	c.mu.Unlock()
}

// StopTask asks Claude Code to stop a background task (control request stop_task) and waits for its answer; the end
// is reported to the observer as Claude Code gives it.
func (c *Client) StopTask(ctx context.Context, taskID string) error {
	id := "loop-now-stop-task-" + strconv.FormatUint(c.nextID.Add(1), 10)
	ack := make(chan bool, 1)
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return runturn.ErrExited
	}
	c.controls[id] = ack
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.controls, id)
		c.mu.Unlock()
	}()
	b, _ := json.Marshal(map[string]any{"type": "control_request", "request_id": id, "request": map[string]any{"subtype": "stop_task", "task_id": taskID}})
	if err := c.write(b); err != nil {
		return err
	}
	select {
	case ok, open := <-ack:
		if !open {
			return runturn.ErrExited
		}
		if !ok {
			return fmt.Errorf("ccstream: Claude Code refused to stop task %s", taskID)
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-c.done:
		return runturn.ErrExited
	}
}

// Init returns the latest system/init; nil when none has been received yet.
func (c *Client) Init() *Init {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.init == nil {
		return nil
	}
	i := *c.init
	return &i
}

// LastResult returns the raw fields of the latest turn's result.
func (c *Client) LastResult() ResultInfo {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.last
}

// Prompt sends one message and waits for the turn to end. When the process has already exited it returns
// runturn.ErrExited; when the process exits during the turn it returns a failed result (runtime_start_failed if it
// exits before the first turn's init, runtime_exited after that). When ctx ends it stops waiting (the turn carries on
// in the process as usual).
func (c *Client) Prompt(ctx context.Context, text string, sink runturn.Sink, images ...messageimage.Content) (runturn.Result, error) {
	if sink == nil {
		sink = nopSink{}
	}
	line, err := userLine(text, images)
	if err != nil {
		return runturn.Result{}, err
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return c.exitedBeforeTurn()
	}
	if c.exiting {
		// A process that was sent SIGINT on interrupt is sure to exit afterwards, and a message written to it would be
		// lost: do not write; wait for it to exit and let the caller start a new process with --resume.
		c.mu.Unlock()
		c.awaitExit()
		return runturn.Result{}, runturn.ErrExited
	}
	if c.turn != nil && c.turn.self {
		// Claude Code is in a turn of its own: the message would only be read after it. Wait for it to end, so its
		// output is not taken for the reply to this message.
		self := c.turn
		c.mu.Unlock()
		select {
		case <-self.done:
		case <-ctx.Done():
			return runturn.Result{}, ctx.Err()
		}
		return c.Prompt(ctx, text, sink, images...)
	}
	if c.turn != nil {
		c.mu.Unlock()
		return runturn.Result{}, errors.New("ccstream: the previous turn has not ended yet")
	}
	t := newTurn(sink)
	c.turn = t
	waitInit := c.init == nil && c.o.InitTimeout > 0
	c.mu.Unlock()
	if err := c.write(line); err != nil {
		// Failing to write most likely means the process has already exited: wait for the wrap-up (with a limit) before
		// deciding, so that exiting right at startup is reliably reported as a start failure.
		select {
		case <-c.done:
		case <-time.After(exitGrace):
		}
		c.mu.Lock()
		closed := c.closed
		if c.turn == t {
			c.turn = nil
		}
		c.mu.Unlock()
		if closed {
			return c.exitedBeforeTurn()
		}
		return runturn.Result{}, fmt.Errorf("ccstream: write the message: %w", err)
	}
	if waitInit {
		timer := time.AfterFunc(c.o.InitTimeout, func() { c.initTimedOut(t) })
		defer timer.Stop()
	}
	select {
	case <-t.done:
		return t.result, nil
	case <-ctx.Done():
		c.mu.Lock()
		if c.turn == t {
			t.detached = true
		}
		c.mu.Unlock()
		return runturn.Result{}, ctx.Err()
	}
}

// exitedBeforeTurn is the result when a message could not be handed to a process that has exited: if no init was ever
// received it counts as a start failure (for example, an unknown argument made it exit right at startup); otherwise it
// returns runturn.ErrExited.
func (c *Client) exitedBeforeTurn() (runturn.Result, error) {
	c.mu.Lock()
	started := c.init != nil
	c.mu.Unlock()
	if !started {
		return runturn.Result{Outcome: runturn.OutcomeFailed, ErrorCode: codeStartFailed, ErrorMessage: "Claude Code exited before the first turn started."}, nil
	}
	return runturn.Result{}, runturn.ErrExited
}

// initTimedOut ends the process when the first turn gets no init for too long: the turn ends as a start failure as the
// process exits.
func (c *Client) initTimedOut(t *turn) {
	c.mu.Lock()
	stuck := c.init == nil && c.turn == t
	if stuck {
		t.initTimeout = true
	}
	c.mu.Unlock()
	if stuck {
		c.kill()
	}
}

// Abort interrupts the turn in progress: it first sends the interrupt control message, and if there is no confirmation
// within InterruptGrace it sends SIGINT (not available on Windows, where the process is ended instead). When called
// again in the same turn: if the signal was already sent it does nothing; if the interrupt was confirmed but the turn
// still does not end, it sends the signal once more. With no turn in progress it does nothing.
func (c *Client) Abort(ctx context.Context) error {
	c.mu.Lock()
	t := c.turn
	if c.closed {
		c.mu.Unlock()
		return runturn.ErrExited
	}
	if t == nil {
		c.mu.Unlock()
		return nil
	}
	t.interrupted = true
	switch {
	case t.signalled:
		c.mu.Unlock()
		return nil
	case !t.ackedAt.IsZero():
		late := time.Since(t.ackedAt) > c.o.InterruptGrace
		if late {
			t.signalled = true
		}
		c.mu.Unlock()
		if late {
			c.interruptSignal()
		}
		return nil
	}
	id := "loop-now-interrupt-" + strconv.FormatUint(c.nextID.Add(1), 10)
	ack := make(chan bool, 1)
	c.controls[id] = ack
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.controls, id)
		c.mu.Unlock()
	}()
	b, _ := json.Marshal(map[string]any{"type": "control_request", "request_id": id, "request": map[string]any{"subtype": "interrupt"}})
	ok := false
	if c.write(b) == nil {
		timer := time.NewTimer(c.o.InterruptGrace)
		defer timer.Stop()
		select {
		case ok = <-ack:
		case <-timer.C:
		case <-ctx.Done():
		case <-c.done:
			return runturn.ErrExited
		}
	}
	c.mu.Lock()
	if ok {
		t.ackedAt = time.Now()
	} else {
		t.signalled = true
	}
	c.mu.Unlock()
	if !ok {
		c.interruptSignal()
	}
	return nil
}

// interruptSignal sends SIGINT: the current turn ends with error_during_execution, then the process exits, and the
// caller starts a new process with --resume for the next message.
func (c *Client) interruptSignal() {
	if runtime.GOOS == "windows" {
		c.kill()
		return
	}
	c.markExiting()
	_ = c.process.Signal(os.Interrupt)
}

func (c *Client) kill() {
	c.markExiting()
	if c.o.Kill != nil {
		_ = c.o.Kill(c.pid)
		return
	}
	_ = c.process.Kill()
}

func (c *Client) markExiting() {
	c.mu.Lock()
	c.exiting = true
	c.mu.Unlock()
}

// Exiting reports whether the process is already exiting (SIGINT sent, or already ended): such a process must not be
// given new messages.
func (c *Client) Exiting() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.exiting || c.closed
}

// awaitExit waits for an exiting process to exit; past the limit it ends the process and waits a little longer for the
// wrap-up.
func (c *Client) awaitExit() {
	select {
	case <-c.done:
		return
	case <-time.After(exitWait):
	}
	c.kill()
	select {
	case <-c.done:
	case <-time.After(exitGrace + time.Second):
	}
}

func (c *Client) write(line []byte) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	_, err := c.stdin.Write(append(line, '\n'))
	return err
}

// readLoop reads the output line by line. When reading stops with an error (for example, a line over the limit) rather
// than EOF, it ends the process: otherwise the process would hang writing to stdout and the turn would never get a
// result; once the process exits, the turn ends with runtime_exited.
func (c *Client) readLoop(r io.Reader) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, min(64<<10, c.o.MaxLine)), c.o.MaxLine)
	for sc.Scan() {
		c.handleLine(sc.Bytes())
	}
	if sc.Err() != nil {
		c.kill()
		_, _ = io.Copy(io.Discard, r)
	}
}

// shutdown wraps up after the process exits: it records the exit error, marks the client as ended, and returns a
// function that ends the turn in progress and gives up interrupts waiting for confirmation (called after Done is
// announced).
func (c *Client) shutdown(exitErr error) func() {
	c.mu.Lock()
	c.exitErr = exitErr
	c.closed = true
	c.cancelAskingLocked(executor.CancelProcessExited)
	t := c.turn
	c.turn = nil
	started := c.init != nil
	var pending []chan bool
	for id, ch := range c.controls {
		delete(c.controls, id)
		pending = append(pending, ch)
	}
	c.mu.Unlock()
	return func() {
		for _, ch := range pending {
			close(ch)
		}
		if t != nil {
			t.exited(started)
		}
	}
}

// userLine is one user message: the content is a string when there are no images, and a text block plus base64 image
// blocks when there are.
func userLine(text string, images []messageimage.Content) ([]byte, error) {
	var content any = text
	if len(images) > 0 {
		blocks := []map[string]any{{"type": "text", "text": text}}
		for _, img := range images {
			blocks = append(blocks, map[string]any{"type": "image", "source": map[string]any{"type": "base64", "media_type": img.MimeType, "data": img.Data}})
		}
		content = blocks
	}
	return json.Marshal(map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": content}, "parent_tool_use_id": nil})
}

type nopSink struct{}

func (nopSink) TextDelta(string)               {}
func (nopSink) ToolStarted(string, string)     {}
func (nopSink) ToolEnded(string, string, bool) {}

// Failure codes, matching executor's RunErrorCode.
const (
	codeStartFailed = executor.ErrRuntimeStartFailed
	codeExited      = executor.ErrRuntimeExited
	codeError       = executor.ErrRuntimeError
)
