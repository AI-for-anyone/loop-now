// Package coord 封装 Redis 中可重建的协调数据：Agent 忙闲锁、两条排队队列、
// 控制台事件流、执行的累计回复文字，以及打断命令的 Pub/Sub 转发。
// PostgreSQL 才是事实来源；这里的数据丢失后可以按 PostgreSQL 重建。
package coord

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// Keys 生成带前缀的键名。前缀通常以冒号结尾，例如 "loopnow:"。
type Keys struct{ Prefix string }

// Lock 是 Agent 忙闲锁：<prefix>agent:<id>:lock。
func (k Keys) Lock(agentID string) string { return k.agent(agentID, "lock") }

// QueueInterrupt 是打断队列：<prefix>agent:<id>:q:interrupt。
func (k Keys) QueueInterrupt(agentID string) string { return k.agent(agentID, "q:interrupt") }

// QueueNormal 是普通队列：<prefix>agent:<id>:q:normal。
func (k Keys) QueueNormal(agentID string) string { return k.agent(agentID, "q:normal") }

// Cmd 是打断转发的 Pub/Sub 频道：<prefix>agent:<id>:cmd。
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
	LockTTL      time.Duration // 忙闲锁 TTL，必须为正
	StreamMaxLen int64         // XADD MAXLEN ~ n；≤ 0 时不裁剪
	RunTextTTL   time.Duration // 回复文字 TTL；≤ 0 时不设过期
	// OverviewMaxLen 是概览事件流的 MAXLEN ~ n；≤ 0 时不裁剪。
	OverviewMaxLen int64
}

// Coord 在 Redis 上执行协调操作。
type Coord struct {
	rdb  *redis.Client
	keys Keys
	opts Options
}

// New 创建协调层。
func New(rdb *redis.Client, o Options) *Coord {
	return &Coord{rdb: rdb, keys: Keys{Prefix: o.Prefix}, opts: o}
}

// Keys 返回键名生成器。
func (c *Coord) Keys() Keys { return c.keys }

// LockValue 生成锁持有者的值 "<runID>|<instanceID>"。
func LockValue(runID, instanceID string) string { return runID + "|" + instanceID }

// Ping 用于健康检查。
func (c *Coord) Ping(ctx context.Context) error { return c.rdb.Ping(ctx).Err() }

func (c *Coord) lockTTLms() int64    { return c.opts.LockTTL.Milliseconds() }
func (c *Coord) runTextTTLms() int64 { return c.opts.RunTextTTL.Milliseconds() }

func (c *Coord) lockKeys(agentID string) []string {
	return []string{c.keys.Lock(agentID), c.keys.QueueInterrupt(agentID), c.keys.QueueNormal(agentID)}
}

// ---------- 忙闲锁 ----------

// KEYS: lock, q:interrupt, q:normal；ARGV: owner, ttlMs
var acquireScript = redis.NewScript(`
if redis.call('EXISTS', KEYS[1]) == 1 then return 0 end
if redis.call('LLEN', KEYS[2]) > 0 or redis.call('LLEN', KEYS[3]) > 0 then return 0 end
redis.call('SET', KEYS[1], ARGV[1], 'PX', ARGV[2])
return 1
`)

// KEYS: lock；ARGV: owner, ttlMs
var renewScript = redis.NewScript(`
local v = redis.call('GET', KEYS[1])
if v == ARGV[1] then
  redis.call('PEXPIRE', KEYS[1], ARGV[2])
  return 1
end
if not v then
  redis.call('SET', KEYS[1], ARGV[1], 'PX', ARGV[2])
  return 1
end
return 0
`)

// KEYS: lock；ARGV: owner
var releaseScript = redis.NewScript(`
if redis.call('GET', KEYS[1]) == ARGV[1] then
  return redis.call('DEL', KEYS[1])
end
return 0
`)

// KEYS: lock, q:interrupt, q:normal；ARGV: currentOwner, instanceID, ttlMs
// 返回 {ok, nextRunID}；ok=0 表示前置条件不满足。
var handoffScript = redis.NewScript(`
local v = redis.call('GET', KEYS[1])
if ARGV[1] ~= '' then
  if v ~= ARGV[1] then return {0, ''} end
elseif v then
  return {0, ''}
end
local nxt = redis.call('LPOP', KEYS[2])
if not nxt then nxt = redis.call('LPOP', KEYS[3]) end
if nxt then
  redis.call('SET', KEYS[1], nxt .. '|' .. ARGV[2], 'PX', ARGV[3])
  return {1, nxt}
end
if ARGV[1] ~= '' then redis.call('DEL', KEYS[1]) end
return {1, ''}
`)

// AcquireIfIdle 仅在锁不存在且两个队列都为空时设置锁（PX LockTTL），返回是否拿到。
func (c *Coord) AcquireIfIdle(ctx context.Context, agentID, owner string) (bool, error) {
	n, err := acquireScript.Run(ctx, c.rdb, c.lockKeys(agentID), owner, c.lockTTLms()).Int64()
	if err != nil {
		return false, fmt.Errorf("获取忙闲锁失败：%w", err)
	}
	return n == 1, nil
}

// Renew 续期锁：持有者匹配时 PEXPIRE；锁不存在时按 owner 补设（Redis 数据丢失后恢复）；
// 被其他持有者占用时返回 false 且不做改动。
func (c *Coord) Renew(ctx context.Context, agentID, owner string) (bool, error) {
	n, err := renewScript.Run(ctx, c.rdb, []string{c.keys.Lock(agentID)}, owner, c.lockTTLms()).Int64()
	if err != nil {
		return false, fmt.Errorf("续期忙闲锁失败：%w", err)
	}
	return n == 1, nil
}

// Restore 仅在锁不存在时设置锁（PX LockTTL），用于按已有的进行中执行恢复锁。返回是否设置。
func (c *Coord) Restore(ctx context.Context, agentID, owner string) (bool, error) {
	ok, err := c.rdb.SetNX(ctx, c.keys.Lock(agentID), owner, c.opts.LockTTL).Result()
	if err != nil {
		return false, fmt.Errorf("恢复忙闲锁失败：%w", err)
	}
	return ok, nil
}

// Release 按持有者比对后删除锁，返回是否删除。
func (c *Coord) Release(ctx context.Context, agentID, owner string) (bool, error) {
	n, err := releaseScript.Run(ctx, c.rdb, []string{c.keys.Lock(agentID)}, owner).Int64()
	if err != nil {
		return false, fmt.Errorf("释放忙闲锁失败：%w", err)
	}
	return n == 1, nil
}

// LockOwner 返回当前锁的值；锁不存在时返回 ""。
func (c *Coord) LockOwner(ctx context.Context, agentID string) (string, error) {
	v, err := c.rdb.Get(ctx, c.keys.Lock(agentID)).Result()
	if errors.Is(err, redis.Nil) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("读取忙闲锁失败：%w", err)
	}
	return v, nil
}

// Handoff 原子地交接锁。前置条件：currentOwner 非空时锁值必须等于它；为空时锁必须不存在。
// 不满足时返回 ("", false, nil)。满足时先从打断队列、再从普通队列 LPOP：
// 取到则把锁设为 runID|instanceID（PX LockTTL）并返回 (runID, true, nil)；
// 两个队列都为空则删除锁（仅 currentOwner 非空时），返回 ("", true, nil)。
func (c *Coord) Handoff(ctx context.Context, agentID, currentOwner, instanceID string) (string, bool, error) {
	res, err := handoffScript.Run(ctx, c.rdb, c.lockKeys(agentID), currentOwner, instanceID, c.lockTTLms()).Slice()
	if err != nil {
		return "", false, fmt.Errorf("交接忙闲锁失败：%w", err)
	}
	if len(res) != 2 {
		return "", false, fmt.Errorf("交接忙闲锁返回格式异常：%v", res)
	}
	ok, _ := res[0].(int64)
	next, _ := res[1].(string)
	return next, ok == 1, nil
}

// ---------- 队列 ----------

// PushNormal 把执行 ID 追加到普通队列尾部。
func (c *Coord) PushNormal(ctx context.Context, agentID, runID string) error {
	if err := c.rdb.RPush(ctx, c.keys.QueueNormal(agentID), runID).Err(); err != nil {
		return fmt.Errorf("加入普通队列失败：%w", err)
	}
	return nil
}

// PushInterrupt 把执行 ID 追加到打断队列尾部。
func (c *Coord) PushInterrupt(ctx context.Context, agentID, runID string) error {
	if err := c.rdb.RPush(ctx, c.keys.QueueInterrupt(agentID), runID).Err(); err != nil {
		return fmt.Errorf("加入打断队列失败：%w", err)
	}
	return nil
}

// KEYS: q:normal, q:interrupt；ARGV: runID
// 返回 1：已从普通队列移到打断队列尾部；2：已在打断队列；0：两个队列都没有（已被交接取走或 Redis 数据丢失）。
var promoteScript = redis.NewScript(`
if redis.call('LREM', KEYS[1], 0, ARGV[1]) > 0 then
  redis.call('RPUSH', KEYS[2], ARGV[1])
  return 1
end
if redis.call('LPOS', KEYS[2], ARGV[1]) then return 2 end
return 0
`)

// Promote 原子地把执行从普通队列移到打断队列尾部，与 Handoff 的出队互斥：同一执行不会同时在两个队列，也不会丢。
// 返回值见 promoteScript；0 时由调用方按数据库判断（已出队会照常开始，数据丢失由对账重建）。
func (c *Coord) Promote(ctx context.Context, agentID, runID string) (int64, error) {
	n, err := promoteScript.Run(ctx, c.rdb, []string{c.keys.QueueNormal(agentID), c.keys.QueueInterrupt(agentID)}, runID).Int64()
	if err != nil {
		return 0, fmt.Errorf("移到打断队列失败：%w", err)
	}
	return n, nil
}

// Remove 把执行 ID 从两个队列中移除（撤回或作废的排队执行）。已被交接取走时无影响：开始前会按数据库状态跳过它。
func (c *Coord) Remove(ctx context.Context, agentID, runID string) error {
	_, err := c.rdb.TxPipelined(ctx, func(p redis.Pipeliner) error {
		p.LRem(ctx, c.keys.QueueInterrupt(agentID), 0, runID)
		p.LRem(ctx, c.keys.QueueNormal(agentID), 0, runID)
		return nil
	})
	if err != nil {
		return fmt.Errorf("移出队列失败：%w", err)
	}
	return nil
}

// QueueContents 在同一个事务中读取两个队列的全部内容（按出队顺序）。
func (c *Coord) QueueContents(ctx context.Context, agentID string) (interrupt, normal []string, err error) {
	var qi, qn *redis.StringSliceCmd
	_, err = c.rdb.TxPipelined(ctx, func(p redis.Pipeliner) error {
		qi = p.LRange(ctx, c.keys.QueueInterrupt(agentID), 0, -1)
		qn = p.LRange(ctx, c.keys.QueueNormal(agentID), 0, -1)
		return nil
	})
	if err != nil {
		return nil, nil, fmt.Errorf("读取队列失败：%w", err)
	}
	return qi.Val(), qn.Val(), nil
}

// RebuildQueues 在一个事务中把两个队列替换为给定的有序内容；空切片表示删除对应的键。
func (c *Coord) RebuildQueues(ctx context.Context, agentID string, interrupt, normal []string) error {
	ki, kn := c.keys.QueueInterrupt(agentID), c.keys.QueueNormal(agentID)
	_, err := c.rdb.TxPipelined(ctx, func(p redis.Pipeliner) error {
		p.Del(ctx, ki, kn)
		if len(interrupt) > 0 {
			p.RPush(ctx, ki, toArgs(interrupt)...)
		}
		if len(normal) > 0 {
			p.RPush(ctx, kn, toArgs(normal)...)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("重建队列失败：%w", err)
	}
	return nil
}

func toArgs(ss []string) []any {
	out := make([]any, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}

// ---------- 事件流与回复文字 ----------

// Event 是事件流中的一条记录；ID 直接作为 SSE 事件 ID。
type Event struct {
	ID   string
	Type string
	Data []byte
}

// KEYS: runText, events；ARGV: delta, textTTLms, maxLen, type, data
var appendTextScript = redis.NewScript(`
redis.call('APPEND', KEYS[1], ARGV[1])
if tonumber(ARGV[2]) > 0 then redis.call('PEXPIRE', KEYS[1], ARGV[2]) end
if tonumber(ARGV[3]) > 0 then
  return redis.call('XADD', KEYS[2], 'MAXLEN', '~', ARGV[3], '*', 'type', ARGV[4], 'data', ARGV[5])
end
return redis.call('XADD', KEYS[2], '*', 'type', ARGV[4], 'data', ARGV[5])
`)

// KEYS: runText, events；ARGV: readText(1/0)
// 返回 {text, lastID}；事件流为空时 lastID 为 "0-0"。
var snapshotScript = redis.NewScript(`
local text = ''
if ARGV[1] == '1' then
  text = redis.call('GET', KEYS[1]) or ''
end
local last = redis.call('XREVRANGE', KEYS[2], '+', '-', 'COUNT', 1)
local id = '0-0'
if #last > 0 then id = last[1][1] end
return {text, id}
`)

// AppendEvent 追加一条事件（XADD MAXLEN ~ StreamMaxLen），返回条目 ID。
func (c *Coord) AppendEvent(ctx context.Context, agentID, eventType string, data []byte) (string, error) {
	return c.xadd(ctx, c.keys.Events(agentID), c.opts.StreamMaxLen, eventType, data)
}

// AppendOverview 向概览事件流追加一条事件（XADD MAXLEN ~ OverviewMaxLen），返回条目 ID。
func (c *Coord) AppendOverview(ctx context.Context, eventType string, data []byte) (string, error) {
	return c.xadd(ctx, c.keys.Overview(), c.opts.OverviewMaxLen, eventType, data)
}

func (c *Coord) xadd(ctx context.Context, key string, maxLen int64, eventType string, data []byte) (string, error) {
	id, err := c.rdb.XAdd(ctx, &redis.XAddArgs{
		Stream: key,
		MaxLen: maxLen, // ≤ 0 时 go-redis 不附加 MAXLEN
		Approx: true,
		Values: []any{"type", eventType, "data", data},
	}).Result()
	if err != nil {
		return "", fmt.Errorf("追加事件失败：%w", err)
	}
	return id, nil
}

// OverviewLastID 返回概览事件流最新条目的 ID；为空时返回 "0-0"。
func (c *Coord) OverviewLastID(ctx context.Context) (string, error) {
	msgs, err := c.rdb.XRevRangeN(ctx, c.keys.Overview(), "+", "-", 1).Result()
	if err != nil {
		return "", fmt.Errorf("读取概览事件失败：%w", err)
	}
	if len(msgs) == 0 {
		return "0-0", nil
	}
	return msgs[0].ID, nil
}

// OverviewFirstID 返回概览事件流中保留的最早条目 ID；为空时返回 ""。
func (c *Coord) OverviewFirstID(ctx context.Context) (string, error) {
	return c.firstID(ctx, c.keys.Overview())
}

// ReadOverview 读取概览事件流中 afterID 之后（不含）的事件，最多 count 条；规则同 ReadEvents。
func (c *Coord) ReadOverview(ctx context.Context, afterID string, count int64, block time.Duration) ([]Event, error) {
	return c.readStream(ctx, c.keys.Overview(), afterID, count, block)
}

// AppendInbox 向收件箱事件流追加一条事件（XADD MAXLEN ~ StreamMaxLen），返回条目 ID。
func (c *Coord) AppendInbox(ctx context.Context, eventType string, data []byte) (string, error) {
	return c.xadd(ctx, c.keys.Inbox(), c.opts.StreamMaxLen, eventType, data)
}

// InboxLastID 返回收件箱事件流最新条目的 ID；为空时返回 "0-0"。
func (c *Coord) InboxLastID(ctx context.Context) (string, error) {
	msgs, err := c.rdb.XRevRangeN(ctx, c.keys.Inbox(), "+", "-", 1).Result()
	if err != nil {
		return "", fmt.Errorf("读取收件箱事件失败：%w", err)
	}
	if len(msgs) == 0 {
		return "0-0", nil
	}
	return msgs[0].ID, nil
}

// InboxFirstID 返回收件箱事件流中保留的最早条目 ID；为空时返回 ""。
func (c *Coord) InboxFirstID(ctx context.Context) (string, error) {
	return c.firstID(ctx, c.keys.Inbox())
}

// ReadInbox 读取收件箱事件流中 afterID 之后（不含）的事件，最多 count 条；规则同 ReadEvents。
func (c *Coord) ReadInbox(ctx context.Context, afterID string, count int64, block time.Duration) ([]Event, error) {
	return c.readStream(ctx, c.keys.Inbox(), afterID, count, block)
}

// AppendText 在一个 Lua 脚本内：把 delta 追加到 RunText(runID) 并刷新 TTL，
// 同时向事件流追加一条事件。返回条目 ID。
func (c *Coord) AppendText(ctx context.Context, agentID, runID, delta, eventType string, data []byte) (string, error) {
	keys := []string{c.keys.RunText(runID), c.keys.Events(agentID)}
	id, err := appendTextScript.Run(ctx, c.rdb, keys,
		delta, c.runTextTTLms(), c.opts.StreamMaxLen, eventType, data).Text()
	if err != nil {
		return "", fmt.Errorf("追加回复文字失败：%w", err)
	}
	return id, nil
}

// Snapshot 在一个 Lua 脚本内读取 RunText(runID)（runID 为空或不存在时为 ""）
// 与事件流最新条目 ID（为空时 "0-0"），两者互相一致。
func (c *Coord) Snapshot(ctx context.Context, agentID, runID string) (text, lastID string, err error) {
	readText := "0"
	if runID != "" {
		readText = "1"
	}
	keys := []string{c.keys.RunText(runID), c.keys.Events(agentID)}
	res, err := snapshotScript.Run(ctx, c.rdb, keys, readText).StringSlice()
	if err != nil {
		return "", "", fmt.Errorf("读取快照失败：%w", err)
	}
	if len(res) != 2 {
		return "", "", fmt.Errorf("读取快照返回格式异常：%v", res)
	}
	return res[0], res[1], nil
}

// FirstEventID 返回事件流中保留的最早条目 ID；为空时返回 ""。
func (c *Coord) FirstEventID(ctx context.Context, agentID string) (string, error) {
	return c.firstID(ctx, c.keys.Events(agentID))
}

func (c *Coord) firstID(ctx context.Context, key string) (string, error) {
	msgs, err := c.rdb.XRangeN(ctx, key, "-", "+", 1).Result()
	if err != nil {
		return "", fmt.Errorf("读取最早事件失败：%w", err)
	}
	if len(msgs) == 0 {
		return "", nil
	}
	return msgs[0].ID, nil
}

// ReadEvents 读取 afterID 之后（不含）的事件，最多 count 条。
// block > 0 时最多阻塞 block，超时返回空结果；block ≤ 0 时不阻塞。
func (c *Coord) ReadEvents(ctx context.Context, agentID, afterID string, count int64, block time.Duration) ([]Event, error) {
	return c.readStream(ctx, c.keys.Events(agentID), afterID, count, block)
}

func (c *Coord) readStream(ctx context.Context, key, afterID string, count int64, block time.Duration) ([]Event, error) {
	args := &redis.XReadArgs{
		Streams: []string{key, afterID},
		Count:   count,
		Block:   -1,
	}
	if block > 0 {
		args.Block = block
	}
	streams, err := c.rdb.XRead(ctx, args).Result()
	if errors.Is(err, redis.Nil) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("读取事件失败：%w", err)
	}
	var out []Event
	for _, s := range streams {
		for _, m := range s.Messages {
			out = append(out, toEvent(m))
		}
	}
	return out, nil
}

func toEvent(m redis.XMessage) Event {
	e := Event{ID: m.ID}
	if v, ok := m.Values["type"].(string); ok {
		e.Type = v
	}
	if v, ok := m.Values["data"].(string); ok {
		e.Data = []byte(v)
	}
	return e
}

// CompareIDs 按数值比较两个 Stream ID（"<ms>-<seq>"，缺少序号时视为 0），
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
func (c *Coord) RunText(ctx context.Context, runID string) (string, error) {
	v, err := c.rdb.Get(ctx, c.keys.RunText(runID)).Result()
	if errors.Is(err, redis.Nil) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("读取回复文字失败：%w", err)
	}
	return v, nil
}

// DeleteRunText 删除执行的累计回复文字。
func (c *Coord) DeleteRunText(ctx context.Context, runID string) error {
	if err := c.rdb.Del(ctx, c.keys.RunText(runID)).Err(); err != nil {
		return fmt.Errorf("删除回复文字失败：%w", err)
	}
	return nil
}

// ---------- 打断转发 ----------

// PublishInterrupt 向 Agent 的命令频道发布打断命令，载荷为执行 ID。
func (c *Coord) PublishInterrupt(ctx context.Context, agentID, runID string) error {
	if err := c.rdb.Publish(ctx, c.keys.Cmd(agentID), runID).Err(); err != nil {
		return fmt.Errorf("发布打断命令失败：%w", err)
	}
	return nil
}

// SubscribeInterrupts 订阅 Agent 的命令频道，返回收到的执行 ID。
// 返回前已确认订阅生效，之后发布的消息不会丢失；ctx 结束时关闭订阅与通道。
func (c *Coord) SubscribeInterrupts(ctx context.Context, agentID string) (<-chan string, error) {
	ps := c.rdb.Subscribe(ctx, c.keys.Cmd(agentID))
	msg, err := ps.Receive(ctx)
	if err != nil {
		_ = ps.Close()
		return nil, fmt.Errorf("订阅打断命令失败：%w", err)
	}
	if _, ok := msg.(*redis.Subscription); !ok {
		_ = ps.Close()
		return nil, fmt.Errorf("订阅打断命令失败：未收到订阅确认（%T）", msg)
	}
	in := ps.Channel()
	out := make(chan string, 16)
	go func() {
		defer close(out)
		defer ps.Close()
		for {
			select {
			case <-ctx.Done():
				return
			case m, ok := <-in:
				if !ok {
					return
				}
				select {
				case out <- m.Payload:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return out, nil
}

// TaskStream/ProjectTaskStream 使用独立命名空间，不与常态Agent事件混用。
func TaskStream(id string) string        { return "task/" + id }
func ProjectTaskStream(id string) string { return "project/" + id + "/tasks" }
