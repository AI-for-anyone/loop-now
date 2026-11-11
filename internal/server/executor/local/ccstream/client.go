// Package ccstream 驱动一个常驻的 Claude Code 进程：claude -p --input-format stream-json --output-format stream-json
// --verbose --include-partial-messages。一个进程连续处理多轮：每轮在 stdin 写一条 user 消息，读 stdout 直到这一轮的 result。
// 打断先发 control_request（interrupt），等不到确认再发 SIGINT。输出里不认识的类型一律忽略，不当作错误。
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
	// exitGrace 是进程退出后等待 stdout/stderr 读到 EOF 的最长时间，防止继承了管道的孙进程使读取永久阻塞。
	exitGrace = 2 * time.Second
	// stderrLimit 是 stderr 环形缓冲保留的字节数。
	stderrLimit = 64 << 10
	// defaultInterruptGrace 是发出打断控制消息后等确认的默认时间。
	defaultInterruptGrace = 2 * time.Second
	// maxLine 是一行输出的上限（图片、长工具结果都在一行里）。
	maxLine = 64 << 20
	// exitWait 是进程正在退出（已发 SIGINT 或已结束）时，新消息等它退出的上限；超时就结束进程。
	exitWait = 5 * time.Second
)

// Options 是客户端的参数。
type Options struct {
	// InterruptGrace 是发出打断控制消息后等 control_response 的时间，超时改发 SIGINT；为 0 时取 2 秒。
	InterruptGrace time.Duration
	// InitTimeout 是进程写出第一条消息后等 system/init 的上限：超时视为启动失败并结束进程；为 0 时不限。
	InitTimeout time.Duration
	// Kill 结束进程（连同进程组）；为空时只结束进程本身。
	Kill func(pid int) error
	// MaxLine 是一行输出的上限；为 0 时取 64 MiB（图片、长工具结果都在一行里）。
	MaxLine int
}

// Observer 接收用量与事件，不论当时有没有进行中的一轮；inTurn 表示是否发生在某一轮之中。
// 调用发生在读协程上，实现必须迅速返回，且不得在回调中同步调用 Client 的方法。
type Observer interface {
	// Usage 是一轮结束时按模型给出的用量（result 的 model_usage_since_last_result，每个模型一条）。
	Usage(u Usage, inTurn bool)
	// RateLimit 是 rate_limit_event 的 rate_limit_info，原样给出。
	RateLimit(info map[string]any, inTurn bool)
}

// Usage 是一轮里一个模型的用量。
type Usage struct {
	Model      string
	Input      int64
	Output     int64
	CacheRead  int64
	CacheWrite int64
	Reasoning  *int64
}

// Init 是进程报告的 system/init 中平台关心的字段。
type Init struct {
	SessionID  string
	Model      string
	Version    string
	Tools      []string
	MCPServers []MCPServer
}

// MCPServer 是 init 里一个 MCP 服务器的状态。
type MCPServer struct {
	Name   string `json:"name"`
	Status string `json:"status"`
}

// ResultInfo 是最近一轮 result 的原始字段，供调用方进一步判断失败原因（例如未登录）。
type ResultInfo struct {
	Subtype        string
	IsError        bool
	TerminalReason string
	APIErrorStatus *int
	Result         string
}

// Client 是一个 Claude Code 进程的客户端。
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
	init     *Init
	last     ResultInfo
	controls map[string]chan bool // 打断控制消息的 request_id → 是否成功
	closed   bool                 // stdout 已结束
	exiting  bool                 // 已发 SIGINT 或已结束进程：它一定会退出，不再写入新消息

	done    chan struct{}
	exitErr error
}

// Start 以 stdin/stdout 管道启动 cmd（调用方已设置 Path/Args/Env/Dir/SysProcAttr），stderr 写入有界环形缓冲。
// cmd.WaitDelay 为零时设为 exitGrace。
func Start(cmd *exec.Cmd, o Options) (*Client, error) {
	if cmd.Stdin != nil || cmd.Stdout != nil {
		return nil, errors.New("ccstream: cmd.Stdin/cmd.Stdout 必须为空，由客户端接管")
	}
	if o.InterruptGrace <= 0 {
		o.InterruptGrace = defaultInterruptGrace
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
	c := &Client{o: o, stdin: stdin, process: cmd.Process, pid: cmd.Process.Pid, stderr: ring, controls: map[string]chan bool{}, done: make(chan struct{})}
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
		// 先公布进程已退出（Done），再结束进行中的一轮：调用方拿到这一轮的结果时，进程已经确定不在了。
		finish := c.shutdown(err)
		close(c.done)
		finish()
	}()
	return c, nil
}

// Done 在进程退出（且输出读完或超过宽限）后关闭。
func (c *Client) Done() <-chan struct{} { return c.done }

// ExitErr 返回进程的退出错误；进程未退出或正常退出时为空。
func (c *Client) ExitErr() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.exitErr
}

// Pid 返回进程号。
func (c *Client) Pid() int { return c.pid }

// Stderr 返回错误输出的末尾。
func (c *Client) Stderr() string { return c.stderr.String() }

// CloseStdin 关闭 stdin：claude 处理完手上的一轮后退出。
func (c *Client) CloseStdin() error { return c.stdin.Close() }

// SetObserver 设置观察者。
func (c *Client) SetObserver(o Observer) {
	c.mu.Lock()
	c.observer = o
	c.mu.Unlock()
}

// Init 返回最近一次 system/init；还没有收到时为空。
func (c *Client) Init() *Init {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.init == nil {
		return nil
	}
	i := *c.init
	return &i
}

// LastResult 返回最近一轮 result 的原始字段。
func (c *Client) LastResult() ResultInfo {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.last
}

// Prompt 发出一条消息并等这一轮结束。进程已退出时返回 runturn.ErrExited；这一轮中进程退出时返回失败的结果
// （第一轮 init 之前退出为 runtime_start_failed，之后为 runtime_exited）。ctx 结束时放弃等待（这一轮在进程里照常继续）。
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
		// 打断时发过 SIGINT 的进程随后一定退出，写进去的消息会丢：不写，等它退出，让调用方用 --resume 重新起进程。
		c.mu.Unlock()
		c.awaitExit()
		return runturn.Result{}, runturn.ErrExited
	}
	if c.turn != nil {
		c.mu.Unlock()
		return runturn.Result{}, errors.New("ccstream: 上一轮还没有结束")
	}
	t := newTurn(sink)
	c.turn = t
	waitInit := c.init == nil && c.o.InitTimeout > 0
	c.mu.Unlock()
	if err := c.write(line); err != nil {
		// 写不进去多半是进程已经退出：等收尾（有上限）再判断，让启动即退出稳定地报成启动失败。
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
		return runturn.Result{}, fmt.Errorf("ccstream: 写入消息失败：%w", err)
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

// exitedBeforeTurn 是消息没能交给已退出的进程时的结果：从没收到过 init 的算启动失败（例如不认识的参数使它启动即退出），
// 否则返回 runturn.ErrExited。
func (c *Client) exitedBeforeTurn() (runturn.Result, error) {
	c.mu.Lock()
	started := c.init != nil
	c.mu.Unlock()
	if !started {
		return runturn.Result{Outcome: runturn.OutcomeFailed, ErrorCode: codeStartFailed, ErrorMessage: "Claude Code 在第一轮开始前退出"}, nil
	}
	return runturn.Result{}, runturn.ErrExited
}

// initTimedOut 在第一轮迟迟没有 init 时结束进程：这一轮随进程退出以启动失败结束。
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

// Abort 打断进行中的一轮：先发 interrupt 控制消息，InterruptGrace 内没有确认就发 SIGINT（Windows 上没有，改为结束进程）。
// 同一轮里再次调用时：已发过信号则什么也不做；已确认但这一轮迟迟不结束则补发一次信号。没有进行中的一轮时什么也不做。
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

// interruptSignal 发 SIGINT：当前一轮以 error_during_execution 结束，随后进程退出，下一条消息由调用方用 --resume 重新起进程。
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

// Exiting 报告进程是否已在退出（发过 SIGINT 或已结束）：这样的进程不能再交给它新消息。
func (c *Client) Exiting() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.exiting || c.closed
}

// awaitExit 等正在退出的进程退出；超过上限就结束它，再等一会儿收尾。
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

// readLoop 逐行读输出。读到错误（例如一行超过上限）而不是 EOF 时结束进程：否则它会卡在写 stdout 上，这一轮永远等不到结果；
// 进程退出后这一轮以 runtime_exited 结束。
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

// shutdown 在进程退出后收尾：记下退出错误、标记已结束，返回结束进行中的一轮与放弃等待确认的打断的函数（在公布 Done 之后调用）。
func (c *Client) shutdown(exitErr error) func() {
	c.mu.Lock()
	c.exitErr = exitErr
	c.closed = true
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

// userLine 是一条 user 消息：没有图片时内容为字符串，有图片时为文字块加 base64 图片块。
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

// 失败码，与 executor 的 RunErrorCode 一致。
const (
	codeStartFailed = executor.ErrRuntimeStartFailed
	codeExited      = executor.ErrRuntimeExited
	codeError       = executor.ErrRuntimeError
)
