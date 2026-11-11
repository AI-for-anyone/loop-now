package local

import (
	"context"

	"loop-now/internal/server/agentconfig"
	"loop-now/internal/server/executor/local/pirpc"
	"loop-now/internal/server/executor/local/runturn"
	"loop-now/internal/server/messageimage"
	"loop-now/internal/server/modelauth"
)

// runtimeProcess 是执行器管理的一个运行时进程。进程表、空闲超时、停止、打断、pid 记录与一轮的执行只经这个接口，
// 不关心它是哪种运行时。
type runtimeProcess interface {
	Pid() int
	Done() <-chan struct{}
	ExitErr() error
	Stderr() string
	CloseStdin() error
	// Abort 打断进行中的一轮。
	Abort(ctx context.Context) error
	// Prompt 发出一条消息并等这一轮结束；进程已退出时返回 runturn.ErrExited。
	Prompt(ctx context.Context, text string, sink runturn.Sink, images ...messageimage.Content) (runturn.Result, error)
}

// driver 是一种 Agent 运行时在本机执行器里各不相同的部分：怎样启动进程、复用前刷新什么、每轮前做什么、失败怎样称呼。
// 进程表、名额与淘汰、空闲超时、Agent 令牌、pid 文件、打断、任务冻结与交接，以及 prepare（CLI、Skills、凭证文件、
// 工作区来源、本机模式、追加提示）都由执行器统一负责。
type driver interface {
	// name 是日志里的运行时名称；runtime 是它的 AgentRuntime 取值（记在进程事件里）。
	name() string
	runtime() string
	// identity 是运行时自己的进程指纹：与存活进程的不同即按新配置重启。
	identity(snap agentconfig.Snapshot) string
	// refresh 在复用存活进程前重写运行时自己的缓存文件；可预期的失败为 *failure。
	refresh(snap agentconfig.Snapshot, d agentDirs) error
	// start 启动进程。spec 里是执行器准备好的公共环境、目录与要接续的会话；失败时 start 自己释放它建的东西，
	// 可预期的失败为 *failure。
	start(ctx context.Context, spec startSpec) (started, error)
	// attach 在进程登记进执行器之后、写 pid 文件之前调用：接上运行记录与对话等回调。
	attach(p *proc)
	// ready 等进程就绪（例如读出会话文件）；失败时执行器停止进程，可预期的失败为 *failure。
	ready(ctx context.Context, p *proc, spec startSpec) error
	// beforeTurn 在这一轮发出消息前调用；可预期的失败为 *failure。
	beforeTurn(ctx context.Context, p *proc, snap agentconfig.Snapshot) error
	// afterTurn 在这一轮结束、进程转为空闲之前调用：记下会话文件、细化失败原因。
	afterTurn(p *proc, tr runturn.Result) runturn.Result
	// exited 是这一轮中进程退出时的失败码与消息；crashMessage 是带上 stderr 尾部的最终消息。
	exited() (code, msg string)
	crashMessage(p *proc, stderrTail string) string
}

// startSpec 是执行器为启动运行时进程准备好的东西。
type startSpec struct {
	snap agentconfig.Snapshot
	d    agentDirs
	prep prepared
	// env 是平台公共的环境：目录、执行 PATH、凭证、Lark、Agent 令牌与 Google 刷新通道。
	env []string
	// sessionFile 是要接续的会话文件；为空时新建会话（任务实例首次启动时按 snap.ForkSessionFile 分叉）。
	sessionFile string
}

// started 是驱动启动的进程与它附带的资源。
type started struct {
	rt runtimeProcess
	// client 是 Pi 的 RPC 客户端，只供 Pi 专有的操作使用；其他运行时为空。
	client     *pirpc.Client
	authBridge *modelauth.RuntimeBridge
	// binary、version 是运行时可执行文件的真实路径与版本（Claude Code）；sessionID 是平台给它的会话 ID。
	binary, version, sessionID string
}

// driverFor 返回执行这个配置的运行时：模型提供方为 claude-code 时是本机的 Claude Code，其余为随包的 Pi。
func (e *Executor) driverFor(snap agentconfig.Snapshot) driver {
	if isClaude(snap) {
		return claudeDriver{e: e}
	}
	return piDriver{e: e}
}
