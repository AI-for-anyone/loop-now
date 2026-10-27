// Package pirpc 实现 Pi 1.1.0 RPC 模式（pi --mode rpc）的客户端。
//
// 协议要点：stdin/stdout 为严格 JSONL，只按 '\n' 分帧；命令带 id，响应可能乱序，按 id 关联；
// 事件不带 id。一轮 prompt 以 agent_settled 为结束标志。
package pirpc

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"loop-now/internal/server/messageimage"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

// ErrExited 表示 Pi 进程已退出或其 stdout 已关闭。
var ErrExited = errors.New("pirpc: Pi 进程已退出")

const (
	// exitGrace 是进程退出后等待 stdout/stderr 读到 EOF 的最长时间，
	// 防止继承了管道的孙进程使读取永久阻塞。
	exitGrace = 2 * time.Second
	// stderrLimit 是 stderr 环形缓冲保留的字节数。
	stderrLimit = 64 << 10
)

// TurnSink 接收一轮执行的实时事件。调用发生在读协程上，实现必须迅速返回，
// 且不得在回调中同步调用 Client 的方法。
type TurnSink interface {
	TextDelta(delta string)
	ToolStarted(toolCallID, toolName string)
	ToolEnded(toolCallID, toolName string, isError bool)
}

// Outcome 是一轮执行的结局。
type Outcome string

const (
	OutcomeCompleted Outcome = "completed"
	OutcomeFailed    Outcome = "failed"
	OutcomeAborted   Outcome = "aborted"
)

// 错误码。
const (
	ErrorCodePi       = "pi_error"
	ErrorCodePiExited = "pi_exited"
)

// TurnResult 是一轮执行的结果。
type TurnResult struct {
	Outcome      Outcome
	Text         string // 最终回复文本
	ErrorCode    string // "pi_error" | "pi_exited" | ""
	ErrorMessage string
}

// State 是 get_state 中平台关心的字段。
type State struct {
	ThinkingLevel string
	SessionFile   string // 无会话文件时为空
	SessionID     string
}

// Client 是一个 Pi RPC 进程的客户端。
type Client struct {
	stdin     io.WriteCloser
	writeMu   sync.Mutex
	pid       int
	stderr    *ringBuffer
	nextID    atomic.Uint64
	busy      atomic.Bool // 是否有进行中的 Prompt
	malformed atomic.Int64

	mu              sync.Mutex
	pending         map[string]chan *responseFrame
	turn            *turn
	closed          bool // stdout 已结束
	observer        Observer
	dialogs         Dialogs
	compactionStart time.Time

	readerDone chan struct{}
	done       chan struct{}
	exitErr    error
}

// Start 以 stdin/stdout 管道启动 cmd（调用方已设置 Path/Args/Env/Dir/SysProcAttr），
// stderr 写入有界环形缓冲（若调用方设置了 cmd.Stderr，则同时写入它）。
// cmd.WaitDelay 为零时设为 exitGrace。
func Start(cmd *exec.Cmd) (*Client, error) {
	if cmd.Stdin != nil || cmd.Stdout != nil {
		return nil, errors.New("pirpc: cmd.Stdin/cmd.Stdout 必须为空，由客户端接管")
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("pirpc: 创建 stdin 管道失败：%w", err)
	}
	pr, pw, err := os.Pipe()
	if err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("pirpc: 创建 stdout 管道失败：%w", err)
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
		return nil, fmt.Errorf("pirpc: 启动 Pi 失败：%w", err)
	}
	// 父进程不再持有写端，子进程退出后读端才能读到 EOF。
	_ = pw.Close()
	c := newClient(stdin, pr, cmd.Wait)
	c.pid = cmd.Process.Pid
	c.stderr = ring
	return c, nil
}

// newClient 基于给定的读写端创建客户端；wait 为空时 Done 只等待读协程结束（用于进程内测试）。
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
			// 进程已退出但 stdout 仍被占用，强制结束读取。
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

// Done 在进程退出且读协程结束后关闭。
func (c *Client) Done() <-chan struct{} { return c.done }

// ExitErr 返回进程的退出错误；Done 关闭前返回 nil。
func (c *Client) ExitErr() error {
	select {
	case <-c.done:
		return c.exitErr
	default:
		return nil
	}
}

// Pid 返回 Pi 进程号。
func (c *Client) Pid() int { return c.pid }

// Stderr 返回最近约 64KB 的 stderr 输出。
func (c *Client) Stderr() string {
	if c.stderr == nil {
		return ""
	}
	return c.stderr.String()
}

// CloseStdin 关闭 Pi 的 stdin，请求其有序退出。
func (c *Client) CloseStdin() error { return c.stdin.Close() }

// Call 发送一条命令并等待对应 id 的响应：成功时返回 data；success:false 时返回含 Pi 错误文本的错误；
// 进程先结束返回 ErrExited；ctx 取消返回 ctx.Err()。
func (c *Client) Call(ctx context.Context, typ string, params map[string]any) (json.RawMessage, error) {
	resp, err := c.roundTrip(ctx, c.newID(), typ, params)
	if err != nil {
		return nil, err
	}
	if !resp.Success {
		return nil, fmt.Errorf("pirpc: 命令 %s 失败：%s", typ, resp.Error)
	}
	return resp.Data, nil
}

// GetState 查询会话状态。
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
			return State{}, fmt.Errorf("pirpc: 解析 get_state 响应失败：%w", err)
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
		return fmt.Errorf("pirpc: 实际思考等级与本轮配置不一致")
	}
	return nil
}

// Prompt 发送 prompt 并消费事件直到本轮结束（agent_settled），期间回调 sink。
// 同一时刻只允许一个 Prompt。prompt 未能发出时返回 ErrExited；发出后进程退出则返回
// Outcome failed / pi_exited 的结果且 error 为 nil。ctx 取消只停止等待，不会中止 Pi。
func (c *Client) Prompt(ctx context.Context, text string, sink TurnSink, images ...messageimage.Content) (TurnResult, error) {
	if sink == nil {
		sink = nopSink{}
	}
	if !c.busy.CompareAndSwap(false, true) {
		return TurnResult{}, errors.New("pirpc: 已有进行中的 prompt")
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
			// 等待响应期间进程退出：读协程已按 pi_exited 结束本轮。
			return awaitResult(ctx, t)
		}
		return TurnResult{}, err
	}
	if !resp.Success {
		msg := resp.Error
		if msg == "" {
			msg = "Pi 拒绝了 prompt"
		}
		return TurnResult{Outcome: OutcomeFailed, ErrorCode: ErrorCodePi, ErrorMessage: msg}, nil
	}
	if disposition(resp.Data) == "handled" {
		return TurnResult{Outcome: OutcomeCompleted}, nil
	}
	return awaitResult(ctx, t)
}

// Abort 先发送 clear_queue 再发送 abort，并等待 abort 响应（Pi 回到空闲）。
// 若 prompt 已发出但尚未收到响应，先等待该响应，避免 abort 早于运行开始而失效。
// 可与 Prompt 在不同 goroutine 并发调用。
func (c *Client) Abort(ctx context.Context) error {
	c.mu.Lock()
	t := c.turn
	c.mu.Unlock()
	if t != nil {
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

// notSentError 表示命令未能写入 Pi 的 stdin。
type notSentError struct{ err error }

func (e *notSentError) Error() string { return "pirpc: 写入命令失败：" + e.err.Error() }
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
		return nil, fmt.Errorf("pirpc: 编码命令 %s 失败：%w", typ, err)
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

// readLoop 按 '\n' 分帧读取 stdout，直到 EOF 或读错误。
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

// malformedLines 返回被跳过的无法解析的行数。
func (c *Client) malformedLines() int64 { return c.malformed.Load() }
