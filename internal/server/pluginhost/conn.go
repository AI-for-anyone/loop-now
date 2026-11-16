package pluginhost

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"loop-now/internal/procgroup"
)

// ErrHostExited means the plugin host process exited while the request was waiting.
var ErrHostExited = errors.New("插件宿主已退出")

// ErrTimeout means the request got no response before the timeout.
var ErrTimeout = errors.New("插件宿主没有在时限内回应")

// writeTimeout is the time limit for writing one message line: a plugin host that does not read its standard input
// for a long time is considered stuck.
const writeTimeout = 10 * time.Second

// inflight is a request sent to the plugin host that has no response yet; after a crash it tells what the host was
// handling at the time.
type inflight struct {
	Type      string    `json:"type"`
	Plugin    string    `json:"plugin,omitempty"`
	EventID   string    `json:"eventId,omitempty"`
	EventType string    `json:"eventType,omitempty"`
	Since     time.Time `json:"since"`
}

// conn is one lifetime of the plugin host process: standard input and output carry the protocol, and standard error
// goes to the log line by line.
type conn struct {
	cmd      *exec.Cmd
	stdin    *os.File
	writeMu  sync.Mutex
	nextID   atomic.Uint64
	mu       sync.Mutex
	pending  map[string]chan Message
	inflight map[string]inflight
	last     *inflight
	ready    chan readyPayload
	done     chan struct{}
	exitErr  error
	// intentional is true when this exit was requested by the server (disable, stop or manual restart); it does not
	// count as a crash.
	intentional atomic.Bool
}

// startConn launches the plugin host process. onMessage handles requests and logs from the host; onStderr handles
// each line of standard error.
func startConn(command []string, dir string, env []string, onMessage func(*conn, Message), onStderr func(string)) (*conn, error) {
	if len(command) == 0 {
		return nil, errors.New("插件宿主命令未配置")
	}
	inR, inW, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	outR, outW, err := os.Pipe()
	if err != nil {
		inR.Close()
		inW.Close()
		return nil, err
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		inR.Close()
		inW.Close()
		outR.Close()
		outW.Close()
		return nil, err
	}
	cmd := exec.Command(command[0], command[1:]...)
	cmd.Dir, cmd.Env = dir, env
	cmd.Stdin, cmd.Stdout, cmd.Stderr = inR, outW, errW
	// A separate process group (a Job Object on Windows): when the host exits or is stopped, the child processes
	// launched by platform sides end with it.
	err = procgroup.Start(cmd)
	// The child process holds its own ends; the parent closes them so that the read ends see EOF once the host exits.
	inR.Close()
	outW.Close()
	errW.Close()
	if err != nil {
		inW.Close()
		outR.Close()
		errR.Close()
		return nil, err
	}
	c := &conn{cmd: cmd, stdin: inW, pending: map[string]chan Message{}, inflight: map[string]inflight{}, ready: make(chan readyPayload, 1), done: make(chan struct{})}
	var readers sync.WaitGroup
	readers.Add(2)
	go func() {
		defer readers.Done()
		c.readStdout(outR, onMessage)
	}()
	go func() {
		defer readers.Done()
		sc := bufio.NewScanner(errR)
		sc.Buffer(make([]byte, 64<<10), 1<<20)
		for sc.Scan() {
			onStderr(sc.Text())
		}
	}()
	go func() {
		c.exitErr = cmd.Wait()
		_ = procgroup.Kill(cmd.Process.Pid)
		// Allow a little time to read the last lines the host wrote before exiting, then close the read ends.
		finished := make(chan struct{})
		go func() { readers.Wait(); close(finished) }()
		select {
		case <-finished:
		case <-time.After(time.Second):
		}
		outR.Close()
		errR.Close()
		inW.Close()
		<-finished
		// Close done inside the lock: any later request is guaranteed to see that the host has exited, and never leaves
		// a request hanging that nobody will answer.
		c.mu.Lock()
		for id, ch := range c.pending {
			close(ch)
			delete(c.pending, id)
		}
		close(c.done)
		c.mu.Unlock()
	}()
	return c, nil
}

func (c *conn) readStdout(r io.Reader, onMessage func(*conn, Message)) {
	br := bufio.NewReaderSize(r, 64<<10)
	for {
		line, err := readLine(br)
		if err != nil {
			if !errors.Is(err, io.EOF) && !errors.Is(err, os.ErrClosed) {
				onMessage(c, Message{Type: TypeLog, Payload: mustJSON(logPayload{Level: "error", Message: "协议读取失败：" + err.Error()})})
			}
			return
		}
		if len(line) == 0 {
			continue
		}
		var m Message
		if json.Unmarshal(line, &m) != nil || m.Type == "" {
			onMessage(c, Message{Type: TypeLog, Payload: mustJSON(logPayload{Level: "error", Message: "宿主写出了不合协议的一行：" + truncate(string(line), 200)})})
			continue
		}
		switch m.Type {
		case TypeResult, TypeAck:
			c.mu.Lock()
			ch := c.pending[m.ID]
			delete(c.pending, m.ID)
			delete(c.inflight, m.ID)
			c.mu.Unlock()
			if ch != nil {
				ch <- m
			}
		case TypeReady:
			var p readyPayload
			_ = json.Unmarshal(m.Payload, &p)
			select {
			case c.ready <- p:
			default:
			}
		default:
			onMessage(c, m)
		}
	}
}

// readLine reads one line (without the newline); a line longer than MaxLine is discarded and an error is returned,
// and reading continues.
func readLine(br *bufio.Reader) ([]byte, error) {
	var buf []byte
	for {
		chunk, err := br.ReadSlice('\n')
		if len(buf)+len(chunk) > MaxLine {
			// Discard the rest of this line.
			for errors.Is(err, bufio.ErrBufferFull) {
				_, err = br.ReadSlice('\n')
			}
			if err != nil {
				return nil, err
			}
			return nil, fmt.Errorf("一行超过 %d 字节", MaxLine)
		}
		buf = append(buf, chunk...)
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		if err != nil {
			if len(buf) > 0 && errors.Is(err, io.EOF) {
				return buf, nil
			}
			return nil, err
		}
		return buf[:len(buf)-1], nil
	}
}

// send writes one message line.
func (c *conn) send(m Message) error {
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	if len(b)+1 > MaxLine {
		return fmt.Errorf("消息超过 %d 字节", MaxLine)
	}
	b = append(b, '\n')
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	_ = c.stdin.SetWriteDeadline(time.Now().Add(writeTimeout))
	_, err = c.stdin.Write(b)
	return err
}

// request sends a request and waits for result or ack; when the response has ok false it returns a *PluginError.
func (c *conn) request(ctx context.Context, typ, plugin string, payload any, timeout time.Duration, info inflight) (json.RawMessage, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	id := "g" + strconv.FormatUint(c.nextID.Add(1), 10)
	ch := make(chan Message, 1)
	info.Type, info.Plugin, info.Since = typ, plugin, time.Now()
	c.mu.Lock()
	select {
	case <-c.done:
		c.mu.Unlock()
		return nil, ErrHostExited
	default:
	}
	c.pending[id] = ch
	c.inflight[id] = info
	c.last = &info
	c.mu.Unlock()
	forget := func() {
		c.mu.Lock()
		delete(c.pending, id)
		delete(c.inflight, id)
		c.mu.Unlock()
	}
	if err := c.send(Message{ID: id, Type: typ, Plugin: plugin, Payload: raw}); err != nil {
		forget()
		return nil, fmt.Errorf("写入插件宿主失败：%w", err)
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case m, ok := <-ch:
		if !ok {
			return nil, ErrHostExited
		}
		var r Reply
		if err := json.Unmarshal(m.Payload, &r); err != nil {
			return nil, fmt.Errorf("宿主的回应无法解析：%w", err)
		}
		if !r.OK {
			return nil, &PluginError{Plugin: plugin, Message: r.Error}
		}
		return r.Value, nil
	case <-timer.C:
		forget()
		return nil, ErrTimeout
	case <-ctx.Done():
		forget()
		return nil, ctx.Err()
	}
}

// respond answers a request from the plugin host.
func (c *conn) respond(id, plugin string, value any, err error) {
	r := Reply{OK: err == nil}
	if err != nil {
		r.Error = err.Error()
	} else if value != nil {
		r.Value = mustJSON(value)
	}
	_ = c.send(Message{ID: id, Type: TypeResult, Plugin: plugin, Payload: mustJSON(r)})
}

// snapshot returns the requests that currently have no response and the most recently sent request.
func (c *conn) snapshot() ([]inflight, *inflight) {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]inflight, 0, len(c.inflight))
	for _, f := range c.inflight {
		out = append(out, f)
	}
	return out, c.last
}

// stop asks the plugin host to exit and kills the whole process group if it has not exited within grace.
func (c *conn) stop(grace time.Duration) {
	c.intentional.Store(true)
	_ = c.send(Message{Type: TypeShutdown})
	select {
	case <-c.done:
		return
	case <-time.After(grace):
	}
	_ = procgroup.Kill(c.cmd.Process.Pid)
	<-c.done
}

// kill ends the plugin host process group immediately (ready timeout, stuck writes).
func (c *conn) kill() {
	_ = procgroup.Kill(c.cmd.Process.Pid)
	<-c.done
}

// PluginError is a failure reported by the plugin itself (the plugin host responded with ok false).
type PluginError struct {
	Plugin  string
	Message string
}

func (e *PluginError) Error() string { return fmt.Sprintf("插件 %s：%s", e.Plugin, e.Message) }

// stderrPrefix matches the [<id>] the plugin host puts before each line of plugin output.
var stderrPrefix = regexp.MustCompile(`^\[([a-z0-9]([a-z0-9-]{0,62}[a-z0-9])?)\] ?(.*)$`)

func mustJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
