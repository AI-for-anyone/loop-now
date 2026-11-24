// Package acp is a client of the Agent Client Protocol (protocol version 1): JSON-RPC 2.0 over stdio, one JSON message
// per line, with the agent as the server. It opens or loads a session, sends prompts and streams session/update
// notifications until the prompt's response gives the stop reason. Permission requests from the agent are granted
// with the agent's own "allow" option, so a turn never waits for a person; the client offers no file system or
// terminal of its own. Unknown notifications and fields are ignored.
package acp

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

// ErrExited means the agent process has exited or its output has ended.
var ErrExited = runturn.ErrExited

// ProtocolVersion is the protocol version the client speaks.
const ProtocolVersion = 1

// Options are the client's parameters.
type Options struct {
	// Kill ends the process together with its process group; nil ends only the process itself.
	Kill func(pid int) error
	// MaxLine is the limit for one line of output; 0 means 64 MiB.
	MaxLine int
	// LoadQuiet is how long a session/load that keeps replaying but never answers may stay quiet before it counts as
	// loaded (some agents never answer it); 0 means 2 seconds.
	LoadQuiet time.Duration
}

// RPCError is an error response from the agent.
type RPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *RPCError) Error() string { return fmt.Sprintf("acp: %s (%d)", e.Message, e.Code) }

// IsAuthRequired reports whether err says the agent needs a login (the protocol's auth_required error, -32000).
func IsAuthRequired(err error) bool {
	var e *RPCError
	return errors.As(err, &e) && (e.Code == -32000 || strings.Contains(strings.ToLower(e.Message), "auth"))
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

// Client is the client of one agent process.
type Client struct {
	o       Options
	stdin   io.WriteCloser
	writeMu sync.Mutex
	process *os.Process
	pid     int
	stderr  *ring
	nextID  atomic.Int64

	mu      sync.Mutex
	pending map[int64]chan reply
	turn    *turn
	loading *loading
	closed  bool

	done    chan struct{}
	exitErr error
}

// Start starts cmd (the caller sets Path, Args, Env, Dir and SysProcAttr) with pipes for stdin and stdout and a bounded
// buffer for stderr. Call Initialize next.
func Start(cmd *exec.Cmd, o Options) (*Client, error) {
	if cmd.Stdin != nil || cmd.Stdout != nil {
		return nil, errors.New("acp: cmd.Stdin and cmd.Stdout must be nil; the client takes them over")
	}
	if o.MaxLine <= 0 {
		o.MaxLine = 64 << 20
	}
	if o.LoadQuiet <= 0 {
		o.LoadQuiet = 2 * time.Second
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	c := &Client{o: o, stdin: stdin, stderr: &ring{limit: 64 << 10}, pending: map[int64]chan reply{}, done: make(chan struct{})}
	cmd.Stderr = c.stderr
	if cmd.WaitDelay == 0 {
		cmd.WaitDelay = 2 * time.Second
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

func (c *Client) Pid() int              { return c.pid }
func (c *Client) Done() <-chan struct{} { return c.done }
func (c *Client) Stderr() string        { return c.stderr.String() }
func (c *Client) CloseStdin() error     { return c.stdin.Close() }

// ExitErr returns the process's exit error; nil before it exits or when it exited normally.
func (c *Client) ExitErr() error {
	select {
	case <-c.done:
		return c.exitErr
	default:
		return nil
	}
}

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
		if result != nil && len(r.result) > 0 && string(r.result) != "null" {
			if err := json.Unmarshal(r.result, result); err != nil {
				return fmt.Errorf("acp: decode the %s response: %w", method, err)
			}
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

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
	return c.send(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
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
		if m.Method == "session/update" {
			c.update(m.Params)
		}
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

// answer replies to a request from the agent: a permission request gets the agent's own allow option (allow always
// first, then allow once); anything else (file system, terminal: the client offers neither) gets "method not found".
func (c *Client) answer(id json.RawMessage, method string, params json.RawMessage) {
	if method != "session/request_permission" {
		_ = c.send(map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": -32601, "message": "not supported by this client: " + method}})
		return
	}
	var p struct {
		Options []struct {
			OptionID string `json:"optionId"`
			Kind     string `json:"kind"`
		} `json:"options"`
	}
	_ = json.Unmarshal(params, &p)
	choice := ""
	for _, kind := range []string{"allow_always", "allow_once"} {
		for _, o := range p.Options {
			if o.Kind == kind && choice == "" {
				choice = o.OptionID
			}
		}
	}
	outcome := map[string]any{"outcome": "cancelled"}
	if choice != "" {
		outcome = map[string]any{"outcome": "selected", "optionId": choice}
	}
	_ = c.send(map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{"outcome": outcome}})
}

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
