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
	"syscall"
	"time"
)

// ErrHostExited 表示请求等待期间宿主进程已退出。
var ErrHostExited = errors.New("插件宿主已退出")

// ErrTimeout 表示请求在超时前没有得到回应。
var ErrTimeout = errors.New("插件宿主没有在时限内回应")

// writeTimeout 是写一行消息的时限：宿主长时间不读标准输入视为卡死。
const writeTimeout = 10 * time.Second

// inflight 是已发给宿主、还没有回应的一个请求，崩溃时用来说明宿主当时在处理什么。
type inflight struct {
	Type      string    `json:"type"`
	Plugin    string    `json:"plugin,omitempty"`
	EventID   string    `json:"eventId,omitempty"`
	EventType string    `json:"eventType,omitempty"`
	Since     time.Time `json:"since"`
}

// conn 是一次宿主进程的生命周期：标准输入输出走协议，标准错误逐行进日志。
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
	// intentional 为 true 时这次退出是服务端要求的（停用、停止或手动重启），不计入崩溃。
	intentional atomic.Bool
}

// startConn 拉起宿主进程。onMessage 处理宿主发来的请求与日志；onStderr 处理标准错误的每一行。
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
	// 单独的进程组：退出或被停止时连同平台半边起的子进程一起结束。
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	err = cmd.Start()
	// 子进程持有自己那一端；父进程关掉，宿主退出后读端才能读到 EOF。
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
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		// 留一点时间读完宿主退出前写下的最后几行，再关掉读端。
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
		// 在锁内关闭 done：之后的 request 一定能看到宿主已退出，不会挂起一个没人回应的请求。
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

// readLine 读一行（不含换行）；超过 MaxLine 的行丢弃并返回错误，读取继续。
func readLine(br *bufio.Reader) ([]byte, error) {
	var buf []byte
	for {
		chunk, err := br.ReadSlice('\n')
		if len(buf)+len(chunk) > MaxLine {
			// 丢掉这一行余下的部分。
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

// send 写一行消息。
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

// request 发出一个请求并等待 result 或 ack；回应 ok 为 false 时返回 *PluginError。
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

// respond 回应宿主发来的请求。
func (c *conn) respond(id, plugin string, value any, err error) {
	r := Reply{OK: err == nil}
	if err != nil {
		r.Error = err.Error()
	} else if value != nil {
		r.Value = mustJSON(value)
	}
	_ = c.send(Message{ID: id, Type: TypeResult, Plugin: plugin, Payload: mustJSON(r)})
}

// snapshot 返回当前未回应的请求与最近发出的一个请求。
func (c *conn) snapshot() ([]inflight, *inflight) {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]inflight, 0, len(c.inflight))
	for _, f := range c.inflight {
		out = append(out, f)
	}
	return out, c.last
}

// stop 请宿主退出，grace 内没有退出就结束整个进程组。
func (c *conn) stop(grace time.Duration) {
	c.intentional.Store(true)
	_ = c.send(Message{Type: TypeShutdown})
	select {
	case <-c.done:
		return
	case <-time.After(grace):
	}
	_ = syscall.Kill(-c.cmd.Process.Pid, syscall.SIGKILL)
	<-c.done
}

// kill 立即结束宿主进程组（就绪超时、写入卡死）。
func (c *conn) kill() {
	_ = syscall.Kill(-c.cmd.Process.Pid, syscall.SIGKILL)
	<-c.done
}

// PluginError 是插件自己报告的失败（宿主回应 ok 为 false）。
type PluginError struct {
	Plugin  string
	Message string
}

func (e *PluginError) Error() string { return fmt.Sprintf("插件 %s：%s", e.Plugin, e.Message) }

// stderrPrefix 匹配宿主在插件输出的每行前加的 [<id>]。
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
