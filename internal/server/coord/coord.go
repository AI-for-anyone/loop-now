// Package coord 在进程内保存可重建的协调数据：Agent 忙闲锁、两条排队队列、
// 控制台事件流、执行的累计回复文字，以及打断命令的转发。
// 数据库才是事实来源；进程重启后这里为空，按数据库重建。
package coord

import (
	"context"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Keys 生成带前缀的键名（事件流、队列等的名字）。前缀通常以冒号结尾，例如 "loopnow:"。
type Keys struct{ Prefix string }

// Lock 是 Agent 忙闲锁：<prefix>agent:<id>:lock。
func (k Keys) Lock(agentID string) string { return k.agent(agentID, "lock") }

// QueueInterrupt 是打断队列：<prefix>agent:<id>:q:interrupt。
func (k Keys) QueueInterrupt(agentID string) string { return k.agent(agentID, "q:interrupt") }

// QueueNormal 是普通队列：<prefix>agent:<id>:q:normal。
func (k Keys) QueueNormal(agentID string) string { return k.agent(agentID, "q:normal") }

// Cmd 是打断转发的频道：<prefix>agent:<id>:cmd。
func (k Keys) Cmd(agentID string) string { return k.agent(agentID, "cmd") }

// Events 是控制台事件流：<prefix>agent:<id>:events。
func (k Keys) Events(agentID string) string { return k.agent(agentID, "events") }

// RunText 是执行的累计回复文字：<prefix>run:<id>:text。
func (k Keys) RunText(runID string) string { return k.Prefix + "run:" + runID + ":text" }

// Overview 是概览事件流（全部 Agent 的运行状态）：<prefix>overview:events。
func (k Keys) Overview() string { return k.Prefix + "overview:events" }

// Inbox 是收件箱事件流（全部 Inbox 消息）：<prefix>inbox:events。
func (k Keys) Inbox() string { return k.Prefix + "inbox:events" }

func (k Keys) agent(agentID, suffix string) string {
	return k.Prefix + "agent:" + agentID + ":" + suffix
}

// Options 是协调层的配置；取值由 config 包校验。
type Options struct {
	Prefix       string
	LockTTL      time.Duration // 忙闲锁多久不续期就失效；≤ 0 时不失效
	StreamMaxLen int64         // 每条事件流最多保留的条数；≤ 0 时不裁剪
	RunTextTTL   time.Duration // 回复文字多久不追加就丢弃；≤ 0 时不丢弃
	// OverviewMaxLen 是概览事件流最多保留的条数；≤ 0 时不裁剪。
	OverviewMaxLen int64
	// Now 是时钟，测试可替换；为空时用 time.Now。
	Now func() time.Time
}

// Coord 在进程内执行协调操作。所有状态由一把锁保护，每个方法都是原子的
// （原来由 Redis 的 Lua 脚本或事务保证的，这里由同一把锁保证）。
type Coord struct {
	keys Keys
	opts Options
	now  func() time.Time

	mu        sync.Mutex
	locks     map[string]expiring // 键：Agent ID
	interrupt map[string][]string // 键：Agent ID
	normal    map[string][]string
	streams   map[string]*stream  // 键：Keys 生成的流名
	texts     map[string]expiring // 键：执行 ID
	subs      map[string]map[chan string]struct{}
	lastSweep time.Time
}

// expiring 是带失效时刻的值；until 为零表示不失效。
type expiring struct {
	value string
	until time.Time
}

func (e expiring) live(now time.Time) bool { return e.until.IsZero() || now.Before(e.until) }

// New 创建协调层。
func New(o Options) *Coord {
	now := o.Now
	if now == nil {
		now = time.Now
	}
	return &Coord{keys: Keys{Prefix: o.Prefix}, opts: o, now: now,
		locks: map[string]expiring{}, interrupt: map[string][]string{}, normal: map[string][]string{},
		streams: map[string]*stream{}, texts: map[string]expiring{}, subs: map[string]map[chan string]struct{}{}}
}

// Keys 返回键名生成器。
func (c *Coord) Keys() Keys { return c.keys }

// LockValue 生成锁持有者的值 "<runID>|<instanceID>"。
func LockValue(runID, instanceID string) string { return runID + "|" + instanceID }

// Ping 用于健康检查；进程内的协调层总是可用。
func (c *Coord) Ping(context.Context) error { return nil }

func after(now time.Time, d time.Duration) time.Time {
	if d <= 0 {
		return time.Time{}
	}
	return now.Add(d)
}

// ---------- 忙闲锁 ----------

// owner 返回未失效的锁值；没有时为 ""。调用方持有 c.mu。
func (c *Coord) owner(agentID string, now time.Time) string {
	l, ok := c.locks[agentID]
	if !ok {
		return ""
	}
	if !l.live(now) {
		delete(c.locks, agentID)
		return ""
	}
	return l.value
}

func (c *Coord) setLock(agentID, owner string, now time.Time) {
	c.locks[agentID] = expiring{value: owner, until: after(now, c.opts.LockTTL)}
}

// AcquireIfIdle 仅在锁不存在且两个队列都为空时设置锁，返回是否拿到。
func (c *Coord) AcquireIfIdle(_ context.Context, agentID, owner string) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	if c.owner(agentID, now) != "" || len(c.interrupt[agentID]) > 0 || len(c.normal[agentID]) > 0 {
		return false, nil
	}
	c.setLock(agentID, owner, now)
	return true, nil
}

// Renew 续期锁：持有者匹配时延长；锁不存在时按 owner 补设；被其他持有者占用时返回 false 且不做改动。
func (c *Coord) Renew(_ context.Context, agentID, owner string) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	if v := c.owner(agentID, now); v != "" && v != owner {
		return false, nil
	}
	c.setLock(agentID, owner, now)
	return true, nil
}

// Restore 仅在锁不存在时设置锁，用于按已有的进行中执行恢复锁。返回是否设置。
func (c *Coord) Restore(_ context.Context, agentID, owner string) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	if c.owner(agentID, now) != "" {
		return false, nil
	}
	c.setLock(agentID, owner, now)
	return true, nil
}

// Release 按持有者比对后删除锁，返回是否删除。
func (c *Coord) Release(_ context.Context, agentID, owner string) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.owner(agentID, c.now()) != owner || owner == "" {
		return false, nil
	}
	delete(c.locks, agentID)
	return true, nil
}

// LockOwner 返回当前锁的值；锁不存在时返回 ""。
func (c *Coord) LockOwner(_ context.Context, agentID string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.owner(agentID, c.now()), nil
}

// Handoff 原子地交接锁。前置条件：currentOwner 非空时锁值必须等于它；为空时锁必须不存在。
// 不满足时返回 ("", false, nil)。满足时先从打断队列、再从普通队列取队首：
// 取到则把锁设为 runID|instanceID 并返回 (runID, true, nil)；
// 两个队列都为空则删除锁（仅 currentOwner 非空时），返回 ("", true, nil)。
func (c *Coord) Handoff(_ context.Context, agentID, currentOwner, instanceID string) (string, bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	if c.owner(agentID, now) != currentOwner {
		return "", false, nil
	}
	next := ""
	for _, q := range []map[string][]string{c.interrupt, c.normal} {
		if ids := q[agentID]; len(ids) > 0 {
			next = ids[0]
			setQueue(q, agentID, ids[1:])
			break
		}
	}
	if next != "" {
		c.setLock(agentID, LockValue(next, instanceID), now)
		return next, true, nil
	}
	if currentOwner != "" {
		delete(c.locks, agentID)
	}
	return "", true, nil
}

// ---------- 队列 ----------

func setQueue(q map[string][]string, agentID string, ids []string) {
	if len(ids) == 0 {
		delete(q, agentID)
		return
	}
	q[agentID] = ids
}

func without(ids []string, runID string) ([]string, bool) {
	out := ids[:0:0]
	found := false
	for _, id := range ids {
		if id == runID {
			found = true
			continue
		}
		out = append(out, id)
	}
	return out, found
}

// PushNormal 把执行 ID 追加到普通队列尾部。
func (c *Coord) PushNormal(_ context.Context, agentID, runID string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.normal[agentID] = append(c.normal[agentID], runID)
	return nil
}

// PushInterrupt 把执行 ID 追加到打断队列尾部。
func (c *Coord) PushInterrupt(_ context.Context, agentID, runID string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.interrupt[agentID] = append(c.interrupt[agentID], runID)
	return nil
}

// Promote 原子地把执行从普通队列移到打断队列尾部，与 Handoff 的出队互斥：同一执行不会同时在两个队列，也不会丢。
// 返回 1：已从普通队列移到打断队列；2：已在打断队列；0：两个队列都没有（已被交接取走），由调用方按数据库判断。
func (c *Coord) Promote(_ context.Context, agentID, runID string) (int64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if rest, found := without(c.normal[agentID], runID); found {
		setQueue(c.normal, agentID, rest)
		c.interrupt[agentID] = append(c.interrupt[agentID], runID)
		return 1, nil
	}
	for _, id := range c.interrupt[agentID] {
		if id == runID {
			return 2, nil
		}
	}
	return 0, nil
}

// Remove 把执行 ID 从两个队列中移除（撤回或作废的排队执行）。已被交接取走时无影响：开始前会按数据库状态跳过它。
func (c *Coord) Remove(_ context.Context, agentID, runID string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, q := range []map[string][]string{c.interrupt, c.normal} {
		rest, _ := without(q[agentID], runID)
		setQueue(q, agentID, rest)
	}
	return nil
}

// QueueContents 原子地读取两个队列的全部内容（按出队顺序）。
func (c *Coord) QueueContents(_ context.Context, agentID string) (interrupt, normal []string, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string{}, c.interrupt[agentID]...), append([]string{}, c.normal[agentID]...), nil
}

// RebuildQueues 原子地把两个队列替换为给定的有序内容；空切片表示清空对应的队列。
func (c *Coord) RebuildQueues(_ context.Context, agentID string, interrupt, normal []string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	setQueue(c.interrupt, agentID, append([]string{}, interrupt...))
	setQueue(c.normal, agentID, append([]string{}, normal...))
	return nil
}

// ---------- 事件流与回复文字 ----------

// Event 是事件流中的一条记录；ID 直接作为 SSE 事件 ID。
type Event struct {
	ID   string
	Type string
	Data []byte
}

// stream 是一条事件流：按 ID 递增保存最近的条目。ID 形如 "<毫秒>-<序号>"（与原来的 Redis Stream 相同），
// 同一毫秒内序号递增，时钟回拨时沿用上一条的毫秒。
type stream struct {
	entries []streamEntry
	lastMs  uint64
	lastSeq uint64
	// notify 在追加时关闭并换新，阻塞读等在它上面。
	notify chan struct{}
}

type streamEntry struct {
	ms, seq uint64
	event   Event
}

// stream 返回（必要时创建）名为 key 的事件流。调用方持有 c.mu。
func (c *Coord) stream(key string) *stream {
	s := c.streams[key]
	if s == nil {
		s = &stream{notify: make(chan struct{})}
		c.streams[key] = s
	}
	return s
}

// add 追加一条事件并按 maxLen 裁掉最早的条目，返回条目 ID。调用方持有 c.mu。
func (s *stream) add(now time.Time, maxLen int64, eventType string, data []byte) string {
	ms := uint64(now.UnixMilli())
	seq := uint64(0)
	if ms <= s.lastMs {
		ms, seq = s.lastMs, s.lastSeq+1
	}
	s.lastMs, s.lastSeq = ms, seq
	id := strconv.FormatUint(ms, 10) + "-" + strconv.FormatUint(seq, 10)
	s.entries = append(s.entries, streamEntry{ms: ms, seq: seq, event: Event{ID: id, Type: eventType, Data: append([]byte(nil), data...)}})
	if maxLen > 0 && int64(len(s.entries)) > maxLen {
		drop := len(s.entries) - int(maxLen)
		s.entries = append(s.entries[:0:0], s.entries[drop:]...)
	}
	close(s.notify)
	s.notify = make(chan struct{})
	return id
}

func (s *stream) lastID() string {
	if len(s.entries) == 0 {
		return "0-0"
	}
	return s.entries[len(s.entries)-1].event.ID
}

func (s *stream) firstID() string {
	if len(s.entries) == 0 {
		return ""
	}
	return s.entries[0].event.ID
}

// since 返回 afterID 之后（不含）的条目，最多 count 条（≤ 0 时不限）。
func (s *stream) since(afterID string, count int64) []Event {
	ams, aseq := parseID(afterID)
	i := sort.Search(len(s.entries), func(i int) bool {
		e := s.entries[i]
		return e.ms > ams || (e.ms == ams && e.seq > aseq)
	})
	n := len(s.entries) - i
	if count > 0 && int64(n) > count {
		n = int(count)
	}
	if n == 0 {
		return nil
	}
	out := make([]Event, n)
	for k := range n {
		out[k] = s.entries[i+k].event
	}
	return out
}

func (c *Coord) append(key string, maxLen int64, eventType string, data []byte) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.stream(key).add(c.now(), maxLen, eventType, data)
}

// AppendEvent 向 Agent 的事件流追加一条事件（保留最近 StreamMaxLen 条），返回条目 ID。
func (c *Coord) AppendEvent(_ context.Context, agentID, eventType string, data []byte) (string, error) {
	return c.append(c.keys.Events(agentID), c.opts.StreamMaxLen, eventType, data), nil
}

// AppendOverview 向概览事件流追加一条事件（保留最近 OverviewMaxLen 条），返回条目 ID。
func (c *Coord) AppendOverview(_ context.Context, eventType string, data []byte) (string, error) {
	return c.append(c.keys.Overview(), c.opts.OverviewMaxLen, eventType, data), nil
}

func (c *Coord) lastID(key string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if s := c.streams[key]; s != nil {
		return s.lastID()
	}
	return "0-0"
}

func (c *Coord) firstID(key string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if s := c.streams[key]; s != nil {
		return s.firstID()
	}
	return ""
}

// OverviewLastID 返回概览事件流最新条目的 ID；为空时返回 "0-0"。
func (c *Coord) OverviewLastID(context.Context) (string, error) {
	return c.lastID(c.keys.Overview()), nil
}

// OverviewFirstID 返回概览事件流中保留的最早条目 ID；为空时返回 ""。
func (c *Coord) OverviewFirstID(context.Context) (string, error) {
	return c.firstID(c.keys.Overview()), nil
}

// ReadOverview 读取概览事件流中 afterID 之后（不含）的事件，最多 count 条；规则同 ReadEvents。
func (c *Coord) ReadOverview(ctx context.Context, afterID string, count int64, block time.Duration) ([]Event, error) {
	return c.readStream(ctx, c.keys.Overview(), afterID, count, block)
}

// AppendInbox 向收件箱事件流追加一条事件（保留最近 StreamMaxLen 条），返回条目 ID。
func (c *Coord) AppendInbox(_ context.Context, eventType string, data []byte) (string, error) {
	return c.append(c.keys.Inbox(), c.opts.StreamMaxLen, eventType, data), nil
}

// InboxLastID 返回收件箱事件流最新条目的 ID；为空时返回 "0-0"。
func (c *Coord) InboxLastID(context.Context) (string, error) { return c.lastID(c.keys.Inbox()), nil }

// InboxFirstID 返回收件箱事件流中保留的最早条目 ID；为空时返回 ""。
func (c *Coord) InboxFirstID(context.Context) (string, error) { return c.firstID(c.keys.Inbox()), nil }

// ReadInbox 读取收件箱事件流中 afterID 之后（不含）的事件，最多 count 条；规则同 ReadEvents。
func (c *Coord) ReadInbox(ctx context.Context, afterID string, count int64, block time.Duration) ([]Event, error) {
	return c.readStream(ctx, c.keys.Inbox(), afterID, count, block)
}

// AppendText 原子地把 delta 追加到执行的回复文字（刷新 RunTextTTL），同时向 Agent 的事件流追加一条事件。返回条目 ID。
func (c *Coord) AppendText(_ context.Context, agentID, runID, delta, eventType string, data []byte) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	c.sweepTexts(now)
	t := c.texts[runID]
	if !t.live(now) {
		t.value = ""
	}
	c.texts[runID] = expiring{value: t.value + delta, until: after(now, c.opts.RunTextTTL)}
	return c.stream(c.keys.Events(agentID)).add(now, c.opts.StreamMaxLen, eventType, data), nil
}

// sweepTexts 每分钟至多一次丢弃过期的回复文字。调用方持有 c.mu。
func (c *Coord) sweepTexts(now time.Time) {
	if now.Sub(c.lastSweep) < time.Minute {
		return
	}
	c.lastSweep = now
	for id, t := range c.texts {
		if !t.live(now) {
			delete(c.texts, id)
		}
	}
}

// Snapshot 原子地读取执行的回复文字（runID 为空或不存在时为 ""）与 Agent 事件流最新条目 ID（为空时 "0-0"），两者互相一致。
func (c *Coord) Snapshot(_ context.Context, agentID, runID string) (text, lastID string, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if runID != "" {
		if t, ok := c.texts[runID]; ok && t.live(c.now()) {
			text = t.value
		}
	}
	lastID = "0-0"
	if s := c.streams[c.keys.Events(agentID)]; s != nil {
		lastID = s.lastID()
	}
	return text, lastID, nil
}

// FirstEventID 返回 Agent 事件流中保留的最早条目 ID；为空时返回 ""。
func (c *Coord) FirstEventID(_ context.Context, agentID string) (string, error) {
	return c.firstID(c.keys.Events(agentID)), nil
}

// ReadEvents 读取 afterID 之后（不含）的事件，最多 count 条。
// block > 0 时没有新事件最多等 block，超时返回空结果；block ≤ 0 时不等待。ctx 结束时返回它的错误。
func (c *Coord) ReadEvents(ctx context.Context, agentID, afterID string, count int64, block time.Duration) ([]Event, error) {
	return c.readStream(ctx, c.keys.Events(agentID), afterID, count, block)
}

func (c *Coord) readStream(ctx context.Context, key, afterID string, count int64, block time.Duration) ([]Event, error) {
	var timer <-chan time.Time
	if block > 0 {
		t := time.NewTimer(block)
		defer t.Stop()
		timer = t.C
	}
	for {
		c.mu.Lock()
		s := c.stream(key)
		out := s.since(afterID, count)
		wait := s.notify
		c.mu.Unlock()
		if len(out) > 0 || block <= 0 {
			return out, nil
		}
		select {
		case <-wait:
		case <-timer:
			return nil, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

// CompareIDs 按数值比较两个事件 ID（"<ms>-<seq>"，缺少序号时视为 0），
// a < b 返回 -1，相等返回 0，a > b 返回 1。无法解析的部分按 0 处理。
func CompareIDs(a, b string) int {
	am, as := parseID(a)
	bm, bs := parseID(b)
	switch {
	case am < bm:
		return -1
	case am > bm:
		return 1
	case as < bs:
		return -1
	case as > bs:
		return 1
	}
	return 0
}

func parseID(id string) (ms, seq uint64) {
	msPart, seqPart, _ := strings.Cut(id, "-")
	ms, _ = strconv.ParseUint(msPart, 10, 64)
	seq, _ = strconv.ParseUint(seqPart, 10, 64)
	return ms, seq
}

// RunText 返回执行的累计回复文字；不存在时返回 ""。
func (c *Coord) RunText(_ context.Context, runID string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if t, ok := c.texts[runID]; ok && t.live(c.now()) {
		return t.value, nil
	}
	return "", nil
}

// DeleteRunText 删除执行的累计回复文字。
func (c *Coord) DeleteRunText(_ context.Context, runID string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.texts, runID)
	return nil
}

// ---------- 打断转发 ----------

// PublishInterrupt 把打断命令（执行 ID）发给 Agent 当前的全部订阅者；没有订阅者时丢弃，订阅者积压满时丢弃这一条。
func (c *Coord) PublishInterrupt(_ context.Context, agentID, runID string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	for ch := range c.subs[agentID] {
		select {
		case ch <- runID:
		default:
		}
	}
	return nil
}

// SubscribeInterrupts 订阅 Agent 的打断命令，返回收到的执行 ID。
// 返回时订阅已生效，之后发布的命令不会丢失；ctx 结束时取消订阅并关闭通道。
func (c *Coord) SubscribeInterrupts(ctx context.Context, agentID string) (<-chan string, error) {
	ch := make(chan string, 16)
	c.mu.Lock()
	if c.subs[agentID] == nil {
		c.subs[agentID] = map[chan string]struct{}{}
	}
	c.subs[agentID][ch] = struct{}{}
	c.mu.Unlock()
	go func() {
		<-ctx.Done()
		c.mu.Lock()
		defer c.mu.Unlock()
		delete(c.subs[agentID], ch)
		if len(c.subs[agentID]) == 0 {
			delete(c.subs, agentID)
		}
		close(ch)
	}()
	return ch, nil
}

// TaskStream/ProjectTaskStream 使用独立命名空间，不与常态Agent事件混用。
func TaskStream(id string) string        { return "task/" + id }
func ProjectTaskStream(id string) string { return "project/" + id + "/tasks" }
