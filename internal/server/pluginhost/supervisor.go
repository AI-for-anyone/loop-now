package pluginhost

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/google/uuid"

	"loop-now/internal/pluginmanifest"
	"loop-now/internal/server/store"
)

// Spec 是一个有平台半边的插件的加载说明，由安装与授权服务给出；顺序就是加载与同步调用的顺序。
type Spec struct {
	ID string
	// Events 是清单 loopNow.events：观察类事件与同步 Hook 都在内。
	Events []string
	// Permissions 是清单 loopNow.permissions；宿主的 state_* 请求需要 state。
	Permissions []string
	// FailClosed 为 true 时拦截类 Hook 超时或出错按阻止处理。
	FailClosed bool
	// Load 是 load 消息的载荷，原样发给宿主。
	Load json.RawMessage
	// Prepare 不为空时在加载前调用，返回值代替 Load（例如这时才签发插件令牌，只影响这一个插件）。
	Prepare func(context.Context) (json.RawMessage, error)
	// Schedules 是清单 schedule[]：加载后按 cron 发 schedule 请求。
	Schedules []pluginmanifest.Schedule
	// AccountKinds 是清单 accounts[].kind；宿主的 accounts_list 只能读这些种类。
	AccountKinds []string
}

// Store 是宿主监护需要的持久化操作，由 *store.Store 实现。
type Store interface {
	PluginStateGet(ctx context.Context, plugin string, project *uuid.UUID, key string) (json.RawMessage, error)
	PluginStateSet(ctx context.Context, plugin string, project *uuid.UUID, key string, value json.RawMessage) error
	PluginStateDelete(ctx context.Context, plugin string, project *uuid.UUID, key string) error
	PluginStateList(ctx context.Context, plugin string, project *uuid.UUID, prefix string) ([]store.PluginStateEntry, error)
	PluginEnabledIn(ctx context.Context, plugin string, project uuid.UUID) (bool, error)
	PluginAccountsOfKind(ctx context.Context, project uuid.UUID, plugin, kind string) ([]store.PluginAccount, error)
	AppendPluginHostLog(ctx context.Context, plugin, level, line string) error
	PrunePluginHostLog(ctx context.Context, before time.Time) error
	PrunePluginEvents(ctx context.Context, before time.Time) error
	SetPluginStatus(ctx context.Context, plugin, status string, lastError *string) error
	ClaimPluginDelivery(ctx context.Context, plugin string, lease time.Duration) (*store.PluginDelivery, error)
	AckPluginDelivery(ctx context.Context, plugin, eventID string, duration time.Duration) error
	FailPluginDelivery(ctx context.Context, plugin, eventID, reason string, retryAt *time.Time) error
}

// Options 是宿主监护的参数；时长为零时取默认值。
type Options struct {
	// Command 是启动宿主的命令：node 与平台内置的宿主入口。
	Command []string
	// Dir 是宿主的工作目录：<work root>/platform/plugins/。
	Dir   string
	Env   []string
	Store Store
	// Specs 返回宿主启动后要加载的插件，每次（重新）启动都会调用。
	Specs func(context.Context) ([]Spec, error)
	Log   *slog.Logger

	ReadyTimeout  time.Duration // 等待 ready，默认 30 秒
	LoadTimeout   time.Duration // load、unload、config_changed，默认 30 秒
	CallTimeout   time.Duration // 同步 Hook，默认 15 秒
	EventTimeout  time.Duration // 观察类事件的 ack，默认 60 秒
	ShutdownGrace time.Duration // shutdown 后等宿主自行退出，默认 5 秒
	RestartDelay  time.Duration // 崩溃后重启前的等待，默认 1 秒
	RestartLimit  int           // 窗口内第几次退出后不再重启，默认 3
	RestartWindow time.Duration // 默认 5 分钟
	// Backoff 是观察类事件失败后的重投间隔；第 len(Backoff)+1 次仍失败后标为 failed。
	Backoff        []time.Duration
	PollInterval   time.Duration // 没有通知时检查到期重投的间隔，默认 5 秒
	LogRetention   time.Duration // 宿主日志保留时长，默认 7 天
	EventRetention time.Duration // 观察类事件与投递记录保留时长，默认 30 天

	Now             func() time.Time // 定时任务用的时钟，默认 time.Now
	ScheduleTick    time.Duration    // 检查定时任务的间隔，默认 1 秒
	ScheduleTimeout time.Duration    // 一次定时任务等待回应的时长，默认 10 分钟
}

func (o *Options) defaults() {
	def := func(d *time.Duration, v time.Duration) {
		if *d <= 0 {
			*d = v
		}
	}
	def(&o.ReadyTimeout, 30*time.Second)
	def(&o.LoadTimeout, 30*time.Second)
	def(&o.CallTimeout, 15*time.Second)
	def(&o.EventTimeout, time.Minute)
	def(&o.ShutdownGrace, 5*time.Second)
	def(&o.RestartDelay, time.Second)
	def(&o.RestartWindow, 5*time.Minute)
	def(&o.PollInterval, 5*time.Second)
	def(&o.LogRetention, 7*24*time.Hour)
	def(&o.EventRetention, 30*24*time.Hour)
	def(&o.ScheduleTick, time.Second)
	def(&o.ScheduleTimeout, 10*time.Minute)
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.RestartLimit <= 0 {
		o.RestartLimit = 3
	}
	if o.Backoff == nil {
		o.Backoff = []time.Duration{5 * time.Second, 30 * time.Second, 2 * time.Minute, 10 * time.Minute}
	}
	if o.Log == nil {
		o.Log = slog.Default()
	}
}

// 宿主状态。
const (
	StateDisabled   = "disabled"   // 全局停用
	StateStarting   = "starting"   // 已拉起，等待 ready 与加载
	StateRunning    = "running"    // 运行中
	StateRestarting = "restarting" // 崩溃后等待重启
	StateStopped    = "stopped"    // 窗口内退出次数达到上限，不再自动重启
	StateIdle       = "idle"       // 服务端停止中或尚未启动
)

// 插件状态（plugins.status）。
const (
	StatusInstalled   = "installed"
	StatusActive      = "active"
	StatusError       = "error"
	StatusHostStopped = "host_stopped"
)

// Crash 记录宿主最近一次意外退出及当时在处理的请求。
type Crash struct {
	At       time.Time  `json:"at"`
	Error    string     `json:"error"`
	InFlight []inflight `json:"inFlight"`
	Last     *inflight  `json:"last"`
	Stderr   []string   `json:"stderr"`
}

// HostStatus 是宿主的当前状态，供插件页展示。
type HostStatus struct {
	State       string      `json:"state"`
	PID         int         `json:"pid,omitempty"`
	StartedAt   *time.Time  `json:"startedAt"`
	HostVersion string      `json:"hostVersion,omitempty"`
	RecentExits []time.Time `json:"recentExits"`
	LastCrash   *Crash      `json:"lastCrash"`
	Loaded      []string    `json:"loaded"`
}

// stderrTail 是崩溃记录里保留的最后几行标准错误。
const stderrTail = 10

type loadedPlugin struct {
	spec   Spec
	cancel context.CancelFunc
	wake   chan struct{}
	done   chan struct{}
}

type logLine struct{ plugin, level, line string }

// Supervisor 监护一个宿主进程。
type Supervisor struct {
	o Options

	mu          sync.Mutex
	state       string
	disabled    bool
	gaveUp      bool
	stopping    bool
	conn        *conn
	startedAt   time.Time
	hostVersion string
	exits       []time.Time
	lastCrash   *Crash
	stderr      []string
	known       []Spec
	loaded      map[string]*loadedPlugin

	signal     chan struct{}
	quit       chan struct{}
	runDone    chan struct{}
	logs       chan logLine
	logsClosed bool
	logDone    chan struct{}
}

// New 创建宿主监护；调用 Start 后才拉起宿主。
func New(o Options) *Supervisor {
	o.defaults()
	return &Supervisor{o: o, state: StateIdle, loaded: map[string]*loadedPlugin{}, signal: make(chan struct{}, 1),
		quit: make(chan struct{}), runDone: make(chan struct{}), logs: make(chan logLine, 1024), logDone: make(chan struct{})}
}

// Start 开始监护；disabled 为 true（全局停用）时不拉起宿主，直到 SetDisabled(false)。
func (s *Supervisor) Start(disabled bool) {
	s.mu.Lock()
	s.disabled = disabled
	s.mu.Unlock()
	go s.writeLogs()
	go s.run()
}

// Stop 让宿主退出并停止监护。
func (s *Supervisor) Stop(ctx context.Context) error {
	s.mu.Lock()
	if s.stopping {
		s.mu.Unlock()
		return nil
	}
	s.stopping = true
	c := s.conn
	s.mu.Unlock()
	close(s.quit)
	if c != nil {
		c.stop(s.o.ShutdownGrace)
	}
	select {
	case <-s.runDone:
	case <-ctx.Done():
		return ctx.Err()
	}
	s.mu.Lock()
	s.logsClosed = true
	close(s.logs)
	s.mu.Unlock()
	select {
	case <-s.logDone:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// SetDisabled 打开或关闭全局停用：停用时停止宿主，重新启用时清零重启计数并拉起宿主。
func (s *Supervisor) SetDisabled(disabled bool) {
	s.mu.Lock()
	s.disabled = disabled
	c := s.conn
	if !disabled {
		s.gaveUp, s.exits = false, nil
	}
	s.mu.Unlock()
	if disabled && c != nil {
		c.stop(s.o.ShutdownGrace)
	}
	s.poke()
}

// Restart 人工重新启动宿主：清零重启计数；宿主在运行时先让它退出。
func (s *Supervisor) Restart() {
	s.mu.Lock()
	s.gaveUp, s.exits = false, nil
	c := s.conn
	s.mu.Unlock()
	if c != nil {
		c.stop(s.o.ShutdownGrace)
	}
	s.poke()
}

// Status 返回宿主的当前状态。
func (s *Supervisor) Status() HostStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := HostStatus{State: s.state, HostVersion: s.hostVersion, RecentExits: slices.Clone(s.exits), LastCrash: s.lastCrash, Loaded: []string{}}
	if s.conn != nil {
		st.PID = s.conn.cmd.Process.Pid
		at := s.startedAt
		st.StartedAt = &at
	}
	for _, spec := range s.known {
		if s.loaded[spec.ID] != nil {
			st.Loaded = append(st.Loaded, spec.ID)
		}
	}
	return st
}

func (s *Supervisor) poke() {
	select {
	case s.signal <- struct{}{}:
	default:
	}
}

func (s *Supervisor) setState(state string) {
	s.mu.Lock()
	s.state = state
	s.mu.Unlock()
}

// run 是监护循环：按需拉起宿主，等它退出，再按重启策略决定下一步。
func (s *Supervisor) run() {
	defer close(s.runDone)
	go s.prune()
	for {
		s.mu.Lock()
		switch {
		case s.stopping:
			s.state = StateIdle
			s.mu.Unlock()
			return
		case s.disabled:
			s.state = StateDisabled
		case s.gaveUp:
			s.state = StateStopped
		default:
			s.state = StateStarting
		}
		idle := s.state != StateStarting
		s.mu.Unlock()
		if idle {
			select {
			case <-s.signal:
			case <-s.quit:
			}
			continue
		}
		c, err := s.launch()
		if c != nil {
			<-c.done
			err = c.exitErr
		}
		s.afterExit(c, err)
		s.mu.Lock()
		restarting := s.state == StateRestarting
		s.mu.Unlock()
		if restarting {
			select {
			case <-time.After(s.o.RestartDelay):
			case <-s.quit:
			}
		}
	}
}

// launch 拉起宿主、等待 ready 并加载插件；返回 nil 时说明没有起来，err 是原因。
func (s *Supervisor) launch() (*conn, error) {
	s.mu.Lock()
	s.stderr = nil
	s.mu.Unlock()
	c, err := startConn(s.o.Command, s.o.Dir, s.o.Env, s.onMessage, s.onStderr)
	if err != nil {
		return nil, fmt.Errorf("无法启动插件宿主：%w", err)
	}
	timer := time.NewTimer(s.o.ReadyTimeout)
	defer timer.Stop()
	select {
	case p := <-c.ready:
		if p.Protocol != ProtocolVersion {
			c.kill()
			return nil, fmt.Errorf("插件宿主的协议版本为 %d，服务端只支持 %d", p.Protocol, ProtocolVersion)
		}
		s.mu.Lock()
		s.conn, s.startedAt, s.hostVersion = c, time.Now(), p.HostVersion
		stopping := s.stopping || s.disabled
		s.mu.Unlock()
		if stopping {
			c.stop(s.o.ShutdownGrace)
			return c, nil
		}
	case <-timer.C:
		c.kill()
		return nil, fmt.Errorf("插件宿主没有在 %s 内就绪", s.o.ReadyTimeout)
	case <-c.done:
		return nil, fmt.Errorf("插件宿主在就绪前退出：%v", c.exitErr)
	case <-s.quit:
		c.stop(s.o.ShutdownGrace)
		return c, nil
	}
	s.o.Log.Info("插件宿主已就绪", "pid", c.cmd.Process.Pid, "hostVersion", s.hostVersion)
	s.loadAll(c)
	s.setState(StateRunning)
	return c, nil
}

// afterExit 在宿主退出后停止投递，并按是否意外退出记录崩溃、决定是否重启。
func (s *Supervisor) afterExit(c *conn, err error) {
	// 先取下退出时未完成的请求，再停投递协程：停投递会撤回它们等待中的请求。
	var pending []inflight
	var last *inflight
	if c != nil {
		pending, last = c.snapshot()
	}
	s.mu.Lock()
	loaded := s.loaded
	s.loaded = map[string]*loadedPlugin{}
	s.conn = nil
	s.mu.Unlock()
	for _, lp := range loaded {
		lp.cancel()
		<-lp.done
	}
	if c != nil && c.intentional.Load() {
		s.mu.Lock()
		disabled := s.disabled
		known := s.known
		s.mu.Unlock()
		if disabled {
			s.markAll(known, StatusInstalled)
		}
		return
	}
	now := time.Now()
	crash := &Crash{At: now, Error: fmt.Sprint(err), InFlight: pending, Last: last, Stderr: []string{}}
	if crash.InFlight == nil {
		crash.InFlight = []inflight{}
	}
	s.mu.Lock()
	crash.Stderr = append(crash.Stderr, s.stderr...)
	s.lastCrash = crash
	kept := s.exits[:0]
	for _, at := range s.exits {
		if now.Sub(at) < s.o.RestartWindow {
			kept = append(kept, at)
		}
	}
	s.exits = append(kept, now)
	gaveUp := len(s.exits) >= s.o.RestartLimit
	s.gaveUp = gaveUp
	if gaveUp {
		s.state = StateStopped
	} else {
		s.state = StateRestarting
	}
	known := s.known
	s.mu.Unlock()
	s.o.Log.Error("插件宿主意外退出", "err", crash.Error, "inFlight", len(crash.InFlight), "willRestart", !gaveUp)
	s.enqueueLog("", "error", "插件宿主意外退出："+crash.Error)
	if gaveUp {
		s.markAll(known, StatusHostStopped)
	}
}

func (s *Supervisor) markAll(specs []Spec, status string) {
	for _, spec := range specs {
		if err := s.o.Store.SetPluginStatus(context.Background(), spec.ID, status, nil); err != nil {
			s.o.Log.Warn("更新插件状态失败", "plugin", spec.ID, "err", err)
		}
	}
}

// loadAll 加载 Specs 给出的全部插件；单个插件加载失败只让它进入错误状态。
func (s *Supervisor) loadAll(c *conn) {
	ctx := context.Background()
	specs, err := s.o.Specs(ctx)
	if err != nil {
		s.o.Log.Error("读取要加载的插件失败", "err", err)
		s.enqueueLog("", "error", "读取要加载的插件失败："+err.Error())
		return
	}
	s.mu.Lock()
	s.known = specs
	s.mu.Unlock()
	for _, spec := range specs {
		s.loadOne(ctx, c, spec)
	}
}

func (s *Supervisor) loadOne(ctx context.Context, c *conn, spec Spec) {
	payload, err := spec.Load, error(nil)
	if spec.Prepare != nil {
		payload, err = spec.Prepare(ctx)
	}
	if err == nil {
		_, err = c.request(ctx, TypeLoad, spec.ID, payload, s.o.LoadTimeout, inflight{})
	}
	if err != nil {
		msg := err.Error()
		var pe *PluginError
		if errors.As(err, &pe) {
			msg = pe.Message
		}
		s.o.Log.Warn("插件加载失败", "plugin", spec.ID, "err", msg)
		s.enqueueLog(spec.ID, "error", "加载失败："+msg)
		if serr := s.o.Store.SetPluginStatus(ctx, spec.ID, StatusError, &msg); serr != nil {
			s.o.Log.Warn("更新插件状态失败", "plugin", spec.ID, "err", serr)
		}
		return
	}
	lp := s.startDispatcher(c, spec)
	s.mu.Lock()
	s.loaded[spec.ID] = lp
	s.mu.Unlock()
	if serr := s.o.Store.SetPluginStatus(ctx, spec.ID, StatusActive, nil); serr != nil {
		s.o.Log.Warn("更新插件状态失败", "plugin", spec.ID, "err", serr)
	}
}

// Reload 在宿主内卸载并按最新的 Specs 重新加载一个插件（安装新版本、启用变化）；宿主未运行时下次启动生效。
func (s *Supervisor) Reload(ctx context.Context, id string) error {
	s.mu.Lock()
	c := s.conn
	s.mu.Unlock()
	if c == nil {
		return nil
	}
	if err := s.unloadOne(ctx, c, id); err != nil {
		s.o.Log.Warn("卸载插件失败", "plugin", id, "err", err)
	}
	specs, err := s.o.Specs(ctx)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.known = specs
	s.mu.Unlock()
	for _, spec := range specs {
		if spec.ID == id {
			s.loadOne(ctx, c, spec)
		}
	}
	return nil
}

// Unload 在宿主内卸载一个插件（卸载插件、不再有平台半边）。
func (s *Supervisor) Unload(ctx context.Context, id string) error {
	s.mu.Lock()
	c := s.conn
	s.known = slices.DeleteFunc(slices.Clone(s.known), func(spec Spec) bool { return spec.ID == id })
	s.mu.Unlock()
	if c == nil {
		return nil
	}
	return s.unloadOne(ctx, c, id)
}

func (s *Supervisor) unloadOne(ctx context.Context, c *conn, id string) error {
	s.mu.Lock()
	lp := s.loaded[id]
	delete(s.loaded, id)
	s.mu.Unlock()
	if lp == nil {
		return nil
	}
	lp.cancel()
	<-lp.done
	_, err := c.request(ctx, TypeUnload, id, struct{}{}, s.o.LoadTimeout, inflight{})
	return err
}

// ConfigChanged 把新的配置交给已加载的插件；插件未加载时返回 ErrNotLoaded。
func (s *Supervisor) ConfigChanged(ctx context.Context, id string, payload any) error {
	_, err := s.Request(ctx, TypeConfigChanged, id, payload, s.o.LoadTimeout)
	return err
}

// ErrNotLoaded 表示插件当前不在宿主里（宿主未运行、停用或加载失败）。
var ErrNotLoaded = errors.New("插件未加载")

// Request 向已加载的插件发出一个请求并等待 result（action、http、schedule 等）。
func (s *Supervisor) Request(ctx context.Context, typ, plugin string, payload any, timeout time.Duration) (json.RawMessage, error) {
	s.mu.Lock()
	c, lp := s.conn, s.loaded[plugin]
	s.mu.Unlock()
	if c == nil || lp == nil {
		return nil, ErrNotLoaded
	}
	return c.request(ctx, typ, plugin, payload, timeout, inflight{})
}

// onStderr 把宿主标准错误的一行记入平台日志与宿主日志；带 [<id>] 前缀的归到该插件。
func (s *Supervisor) onStderr(line string) {
	plugin, text := "", line
	if m := stderrPrefix.FindStringSubmatch(line); m != nil {
		plugin, text = m[1], m[3]
	}
	s.mu.Lock()
	s.stderr = append(s.stderr, line)
	if len(s.stderr) > stderrTail {
		s.stderr = s.stderr[len(s.stderr)-stderrTail:]
	}
	s.mu.Unlock()
	s.o.Log.Warn("插件宿主输出", "plugin", plugin, "line", text)
	s.enqueueLog(plugin, "warn", text)
}

// onMessage 处理宿主发来的日志与请求；请求在单独的 goroutine 里处理，不阻塞读取回应。
func (s *Supervisor) onMessage(c *conn, m Message) {
	switch m.Type {
	case TypeLog:
		var p logPayload
		_ = json.Unmarshal(m.Payload, &p)
		level := p.Level
		switch level {
		case "debug", "info", "warn", "error":
		default:
			level = "info"
		}
		s.o.Log.Log(context.Background(), slogLevel(level), "插件日志", "plugin", m.Plugin, "message", p.Message)
		s.enqueueLog(m.Plugin, level, p.Message)
	case TypeStateGet, TypeStateSet, TypeStateDelete, TypeStateList:
		go func() {
			v, err := s.handleState(m)
			c.respond(m.ID, m.Plugin, v, err)
		}()
	case TypeAccountsList:
		go func() {
			v, err := s.handleAccounts(m)
			c.respond(m.ID, m.Plugin, v, err)
		}()
	default:
		s.o.Log.Warn("插件宿主发来了未知的消息", "type", m.Type, "plugin", m.Plugin)
		if m.ID != "" {
			c.respond(m.ID, m.Plugin, nil, fmt.Errorf("不支持的请求 %s", m.Type))
		}
	}
}

// handleAccounts 回答 accounts_list：要加载的（含正在加载、lifecycle start 中的）、清单声明了 accounts.read 与这个账号种类、
// 在 projectId 启用了的插件，读到这个项目里该种类未删除的账号（按添加先后，values 含 secret 字段）。
func (s *Supervisor) handleAccounts(m Message) (any, error) {
	s.mu.Lock()
	var spec *Spec
	for i := range s.known {
		if s.known[i].ID == m.Plugin {
			spec = &s.known[i]
		}
	}
	s.mu.Unlock()
	if spec == nil {
		return nil, errors.New("插件未加载")
	}
	if !slices.Contains(spec.Permissions, "accounts.read") {
		return nil, errors.New("清单没有声明 accounts.read 权限")
	}
	var r struct {
		ProjectID string `json:"projectId"`
		Kind      string `json:"kind"`
	}
	if err := json.Unmarshal(m.Payload, &r); err != nil {
		return nil, errors.New("请求无法解析")
	}
	if !slices.Contains(spec.AccountKinds, r.Kind) {
		return nil, fmt.Errorf("清单没有声明账号种类 %s", r.Kind)
	}
	project, err := uuid.Parse(r.ProjectID)
	if err != nil {
		return nil, errors.New("projectId 无效")
	}
	ctx := context.Background()
	on, err := s.o.Store.PluginEnabledIn(ctx, m.Plugin, project)
	if err != nil {
		return nil, err
	}
	if !on {
		return nil, errors.New("插件没有在这个项目里启用")
	}
	accounts, err := s.o.Store.PluginAccountsOfKind(ctx, project, m.Plugin, r.Kind)
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(accounts))
	for _, a := range accounts {
		values := a.Values
		if values == nil {
			values = map[string]any{}
		}
		out = append(out, map[string]any{"id": a.ID, "kind": a.Kind, "name": a.Name, "values": values})
	}
	return out, nil
}

// stateValueMax 是一项状态值的最多字节数。
const stateValueMax = 1 << 20

func (s *Supervisor) handleState(m Message) (any, error) {
	s.mu.Lock()
	var spec *Spec
	for i := range s.known {
		if s.known[i].ID == m.Plugin {
			spec = &s.known[i]
		}
	}
	s.mu.Unlock()
	if spec == nil {
		return nil, fmt.Errorf("插件 %q 未加载", m.Plugin)
	}
	if !slices.Contains(spec.Permissions, "state") {
		return nil, errors.New("清单没有声明 state 权限")
	}
	var r stateRequest
	if err := json.Unmarshal(m.Payload, &r); err != nil {
		return nil, errors.New("请求无法解析")
	}
	var project *uuid.UUID
	if r.ProjectID != nil {
		id, err := uuid.Parse(*r.ProjectID)
		if err != nil {
			return nil, errors.New("projectId 必须是 UUID")
		}
		project = &id
	}
	if m.Type != TypeStateList && (len(r.Key) == 0 || len(r.Key) > 512) {
		return nil, errors.New("key 必须是 1 到 512 字节")
	}
	ctx := context.Background()
	switch m.Type {
	case TypeStateGet:
		v, err := s.o.Store.PluginStateGet(ctx, m.Plugin, project, r.Key)
		if v == nil {
			return json.RawMessage("null"), err
		}
		return v, err
	case TypeStateSet:
		if len(r.Value) == 0 {
			return nil, errors.New("缺少 value")
		}
		if len(r.Value) > stateValueMax {
			return nil, fmt.Errorf("value 不能超过 %d 字节", stateValueMax)
		}
		return nil, s.o.Store.PluginStateSet(ctx, m.Plugin, project, r.Key, r.Value)
	case TypeStateDelete:
		return nil, s.o.Store.PluginStateDelete(ctx, m.Plugin, project, r.Key)
	default:
		return s.o.Store.PluginStateList(ctx, m.Plugin, project, r.Prefix)
	}
}

func slogLevel(level string) slog.Level {
	switch level {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	}
	return slog.LevelInfo
}

// enqueueLog 把一行日志交给写入协程；队列满时丢弃，不阻塞协议读取。
func (s *Supervisor) enqueueLog(plugin, level, line string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.logsClosed {
		return
	}
	select {
	case s.logs <- logLine{plugin, level, line}:
	default:
		s.o.Log.Warn("插件宿主日志队列已满，丢弃一行", "plugin", plugin)
	}
}

func (s *Supervisor) writeLogs() {
	defer close(s.logDone)
	for l := range s.logs {
		if err := s.o.Store.AppendPluginHostLog(context.Background(), l.plugin, l.level, l.line); err != nil {
			s.o.Log.Warn("写入插件宿主日志失败", "err", err)
		}
	}
}

// prune 每小时删除超过保留时长的宿主日志与观察类事件（含投递记录）。
func (s *Supervisor) prune() {
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		if err := s.o.Store.PrunePluginHostLog(context.Background(), time.Now().Add(-s.o.LogRetention)); err != nil {
			s.o.Log.Warn("清理插件宿主日志失败", "err", err)
		}
		if err := s.o.Store.PrunePluginEvents(context.Background(), time.Now().Add(-s.o.EventRetention)); err != nil {
			s.o.Log.Warn("清理插件事件失败", "err", err)
		}
		select {
		case <-t.C:
		case <-s.quit:
			return
		}
	}
}
