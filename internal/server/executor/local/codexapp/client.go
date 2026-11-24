// Package codexapp drives `codex app-server`, the protocol the local Codex CLI offers to applications: JSON-RPC 2.0
// over stdio, one JSON message per line. Only methods of the stable protocol are used (initialize does not ask for
// the experimental API). One process holds one thread (a conversation) at a time: a turn sends the message with
// turn/start and reads notifications until turn/completed. Requests the server sends to the client (approvals, user
// input, elicitation) are answered at once, so a turn never waits for a person. Unknown notifications and unknown
// fields are ignored.
package codexapp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"loop-now/internal/server/executor/local/runturn"
)

const (
	// exitGrace is the longest wait after the process exits for its output to reach EOF.
	exitGrace = 2 * time.Second
	// stderrLimit is how many bytes of the error output are kept.
	stderrLimit = 64 << 10
	// maxLine is the default limit for one line of output (images and long tool output are on one line).
	maxLine = 64 << 20
)

// ErrExited means the app-server process has exited or its output has ended.
var ErrExited = runturn.ErrExited

// Options are the client's parameters.
type Options struct {
	// Kill ends the process together with its process group; nil ends only the process itself.
	Kill func(pid int) error
	// MaxLine is the limit for one line of output; 0 means 64 MiB.
	MaxLine int
}

// Observer receives usage and rate limits whether or not a turn is in progress; inTurn tells whether it happened during
// a turn. Calls happen on the reader goroutine and must return quickly.
type Observer interface {
	// Usage is the usage of one model call (thread/tokenUsage/updated, its last breakdown).
	Usage(u Usage, inTurn bool)
	// RateLimit is an account/rateLimits/updated notification, passed on as is.
	RateLimit(info map[string]any, inTurn bool)
}

// Usage is the token usage of one model call.
type Usage struct {
	Input, CachedInput, Output, Reasoning, Total int64
}

// RPCError is an error response from the server.
type RPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *RPCError) Error() string { return fmt.Sprintf("codex app-server: %s (%d)", e.Message, e.Code) }

// IsThreadNotFound reports whether err says the thread has no saved conversation (it never had a turn, or it was
// deleted or archived).
func IsThreadNotFound(err error) bool {
	var e *RPCError
	return errors.As(err, &e) && strings.Contains(e.Message, "no rollout found")
}

type wire struct {
	ID     *json.RawMessage `json:"id,omitempty"`
	Method string           `json:"method,omitempty"`
	Params json.RawMessage  `json:"params,omitempty"`
	Result json.RawMessage  `json:"result,omitempty"`
	Error  *RPCError        `json:"error,omitempty"`
}

type reply struct {
	result json.RawMessage
	err    error
}

// Client is the client of one app-server process.
type Client struct {
	o       Options
	stdin   io.WriteCloser
	writeMu sync.Mutex
	process *os.Process
	pid     int
	stderr  *ring
	nextID  atomic.Int64

	mu       sync.Mutex
	pending  map[int64]chan reply
	turn     *turn
	observer Observer
	closed   bool

	done    chan struct{}
	exitErr error
}

// Start starts cmd (the caller sets Path, Args, Env, Dir and SysProcAttr) with pipes for stdin and stdout and a bounded
// buffer for stderr. It does not initialize the connection; call Initialize.
func Start(cmd *exec.Cmd, o Options) (*Client, error) {
	if cmd.Stdin != nil || cmd.Stdout != nil {
		return nil, errors.New("codexapp: cmd.Stdin and cmd.Stdout must be nil; the client takes them over")
	}
	if o.MaxLine <= 0 {
		o.MaxLine = maxLine
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	c := &Client{o: o, stdin: stdin, stderr: &ring{limit: stderrLimit}, pending: map[int64]chan reply{}, done: make(chan struct{})}
	cmd.Stderr = c.stderr
	if cmd.WaitDelay == 0 {
		cmd.WaitDelay = exitGrace
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	c.process, c.pid = cmd.Process, cmd.Process.Pid
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		c.readLoop(stdout)
	}()
	go func() {
		err := cmd.Wait()
		<-readDone
		c.shutdown(err)
	}()
	return c, nil
}

// Pid returns the process id.
func (c *Client) Pid() int { return c.pid }

// Done is closed after the process has exited and its output has been read.
func (c *Client) Done() <-chan struct{} { return c.done }

// ExitErr returns the process's exit error; nil before it exits or when it exited normally.
func (c *Client) ExitErr() error {
	select {
	case <-c.done:
		return c.exitErr
	default:
		return nil
	}
}

// Stderr returns the end of the error output.
func (c *Client) Stderr() string { return c.stderr.String() }

// CloseStdin closes stdin: the app-server exits when its input ends.
func (c *Client) CloseStdin() error { return c.stdin.Close() }

// Kill ends the process.
func (c *Client) Kill() {
	if c.o.Kill != nil {
		_ = c.o.Kill(c.pid)
		return
	}
	if c.process != nil {
		_ = c.process.Kill()
	}
}

// SetObserver sets the observer of usage and rate limits.
func (c *Client) SetObserver(o Observer) {
	c.mu.Lock()
	c.observer = o
	c.mu.Unlock()
}

// Call sends a request and waits for its response, decoded into result when it is not nil.
func (c *Client) Call(ctx context.Context, method string, params, result any) error {
	ch, err := c.request(method, params)
	if err != nil {
		return err
	}
	select {
	case r := <-ch:
		if r.err != nil {
			return r.err
		}
		if result != nil && len(r.result) > 0 {
			if err := json.Unmarshal(r.result, result); err != nil {
				return fmt.Errorf("codexapp: decode the %s response: %w", method, err)
			}
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// request sends a request and returns the channel its response arrives on.
func (c *Client) request(method string, params any) (chan reply, error) {
	id := c.nextID.Add(1)
	ch := make(chan reply, 1)
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, ErrExited
	}
	c.pending[id] = ch
	c.mu.Unlock()
	if err := c.send(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}); err != nil {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, ErrExited
	}
	return ch, nil
}

func (c *Client) notify(method string, params any) error {
	msg := map[string]any{"jsonrpc": "2.0", "method": method}
	if params != nil {
		msg["params"] = params
	}
	return c.send(msg)
}

func (c *Client) send(msg any) error {
	b, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	_, err = c.stdin.Write(append(b, '\n'))
	return err
}

// readLoop reads the output line by line. When reading stops with an error (such as a line over the limit) rather
// than EOF, it ends the process, so a turn cannot wait for output that will never be read.
func (c *Client) readLoop(r io.Reader) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), c.o.MaxLine)
	for sc.Scan() {
		c.handle(sc.Bytes())
	}
	if sc.Err() != nil {
		c.Kill()
	}
}

func (c *Client) handle(line []byte) {
	var m wire
	if json.Unmarshal(line, &m) != nil {
		return
	}
	switch {
	case m.Method != "" && m.ID != nil:
		c.answer(*m.ID, m.Method, m.Params)
	case m.Method != "":
		c.notification(m.Method, m.Params)
	case m.ID != nil:
		var id int64
		if json.Unmarshal(*m.ID, &id) != nil {
			return
		}
		c.mu.Lock()
		ch := c.pending[id]
		delete(c.pending, id)
		c.mu.Unlock()
		if ch == nil {
			return
		}
		if m.Error != nil {
			ch <- reply{err: m.Error}
		} else {
			ch <- reply{result: m.Result}
		}
	}
}

// answer replies to a request from the server. Nobody watches an Agent's turn to answer prompts, so approvals are
// granted (the thread already runs with full access and approval policy never; anything that still asks is allowed),
// and questions to the user and MCP elicitations are declined; any other request gets "method not found".
func (c *Client) answer(id json.RawMessage, method string, params json.RawMessage) {
	var result any
	switch method {
	case "item/commandExecution/requestApproval", "item/fileChange/requestApproval":
		result = map[string]any{"decision": "accept"}
	case "execCommandApproval", "applyPatchApproval":
		result = map[string]any{"decision": "approved"}
	case "item/permissions/requestApproval":
		var p struct {
			Permissions json.RawMessage `json:"permissions"`
		}
		_ = json.Unmarshal(params, &p)
		granted := p.Permissions
		if len(granted) == 0 {
			granted = json.RawMessage(`{}`)
		}
		result = map[string]any{"permissions": granted, "scope": "turn"}
	case "item/tool/requestUserInput":
		result = map[string]any{"answers": map[string]any{}}
	case "mcpServer/elicitation/request":
		result = map[string]any{"action": "decline"}
	default:
		_ = c.send(map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": -32601, "message": "not supported by this client: " + method}})
		return
	}
	_ = c.send(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
}

func (c *Client) notification(method string, params json.RawMessage) {
	switch method {
	case "thread/tokenUsage/updated":
		var p struct {
			TokenUsage struct {
				Last struct {
					InputTokens           int64 `json:"inputTokens"`
					CachedInputTokens     int64 `json:"cachedInputTokens"`
					OutputTokens          int64 `json:"outputTokens"`
					ReasoningOutputTokens int64 `json:"reasoningOutputTokens"`
					TotalTokens           int64 `json:"totalTokens"`
				} `json:"last"`
			} `json:"tokenUsage"`
		}
		if json.Unmarshal(params, &p) != nil {
			return
		}
		l := p.TokenUsage.Last
		c.mu.Lock()
		obs, inTurn := c.observer, c.turn != nil
		c.mu.Unlock()
		if obs != nil {
			obs.Usage(Usage{Input: l.InputTokens, CachedInput: l.CachedInputTokens, Output: l.OutputTokens, Reasoning: l.ReasoningOutputTokens, Total: l.TotalTokens}, inTurn)
		}
		return
	case "account/rateLimits/updated":
		var info map[string]any
		if json.Unmarshal(params, &info) != nil {
			return
		}
		c.mu.Lock()
		obs, inTurn := c.observer, c.turn != nil
		c.mu.Unlock()
		if obs != nil {
			obs.RateLimit(info, inTurn)
		}
		return
	}
	c.mu.Lock()
	t := c.turn
	c.mu.Unlock()
	if t != nil {
		t.notification(c, method, params)
	}
}

// shutdown wraps up after the process exits: pending requests fail, a turn in progress ends as failed.
func (c *Client) shutdown(err error) {
	c.mu.Lock()
	c.closed = true
	pending := c.pending
	c.pending = map[int64]chan reply{}
	t := c.turn
	c.mu.Unlock()
	for _, ch := range pending {
		ch <- reply{err: ErrExited}
	}
	if t != nil {
		t.exited()
	}
	c.exitErr = err
	close(c.done)
}

// ring keeps the last bytes written to it.
type ring struct {
	mu    sync.Mutex
	limit int
	buf   []byte
}

func (r *ring) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.buf = append(r.buf, p...)
	if over := len(r.buf) - r.limit; over > 0 {
		r.buf = append([]byte(nil), r.buf[over:]...)
	}
	return len(p), nil
}

func (r *ring) String() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return string(r.buf)
}
