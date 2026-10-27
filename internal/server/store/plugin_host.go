package store

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// 插件宿主读写的数据（plugins/SPEC.md §3、§8）：持久状态、宿主日志、插件状态与观察类事件的投递。

// PluginStateEntry 是插件持久状态的一项。
type PluginStateEntry struct {
	Key   string          `json:"key"`
	Value json.RawMessage `json:"value"`
}

// PluginStateListLimit 是一次列出状态的最多项数。
const PluginStateListLimit = 1000

// PluginStateGet 读取一项状态；不存在时返回 nil。project 为空表示插件全局。
func (s *Store) PluginStateGet(ctx context.Context, plugin string, project *uuid.UUID, key string) (json.RawMessage, error) {
	var v json.RawMessage
	err := s.Pool.QueryRow(ctx, `SELECT value FROM plugin_state WHERE plugin_id=$1 AND project_id IS NOT DISTINCT FROM $2 AND key=$3`, plugin, project, key).Scan(&v)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return v, err
}

// PluginStateSet 写入一项状态，已有则覆盖。
func (s *Store) PluginStateSet(ctx context.Context, plugin string, project *uuid.UUID, key string, value json.RawMessage) error {
	_, err := s.Pool.Exec(ctx, `INSERT INTO plugin_state(plugin_id,project_id,key,value) VALUES($1,$2,$3,$4)
		ON CONFLICT (plugin_id,project_id,key) DO UPDATE SET value=EXCLUDED.value, updated_at=now()`, plugin, project, key, value)
	return err
}

// PluginStateDelete 删除一项状态；不存在时也成功。
func (s *Store) PluginStateDelete(ctx context.Context, plugin string, project *uuid.UUID, key string) error {
	_, err := s.Pool.Exec(ctx, `DELETE FROM plugin_state WHERE plugin_id=$1 AND project_id IS NOT DISTINCT FROM $2 AND key=$3`, plugin, project, key)
	return err
}

// PluginStateList 按键排序列出以 prefix 开头的状态，最多 PluginStateListLimit 项。
func (s *Store) PluginStateList(ctx context.Context, plugin string, project *uuid.UUID, prefix string) ([]PluginStateEntry, error) {
	rows, err := s.Pool.Query(ctx, `SELECT key,value FROM plugin_state WHERE plugin_id=$1 AND project_id IS NOT DISTINCT FROM $2 AND starts_with(key,$3)
		ORDER BY key LIMIT $4`, plugin, project, prefix, PluginStateListLimit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (PluginStateEntry, error) {
		var e PluginStateEntry
		return e, r.Scan(&e.Key, &e.Value)
	})
}

// pluginLogLineMax 是一行宿主日志保存的最多字节数，超出部分截断。
const pluginLogLineMax = 8 << 10

// AppendPluginHostLog 记录一行宿主日志；plugin 为空表示宿主自身。
func (s *Store) AppendPluginHostLog(ctx context.Context, plugin, level, line string) error {
	if len(line) > pluginLogLineMax {
		cut := pluginLogLineMax
		for cut > 0 && !utf8.RuneStart(line[cut]) {
			cut--
		}
		line = line[:cut] + "…"
	}
	var id *string
	if plugin != "" {
		id = &plugin
	}
	_, err := s.Pool.Exec(ctx, `INSERT INTO plugin_host_log(plugin_id,level,line) VALUES($1,$2,$3)`, id, level, strings.ToValidUTF8(line, "�"))
	return err
}

// PrunePluginHostLog 删除 before 之前的宿主日志。
func (s *Store) PrunePluginHostLog(ctx context.Context, before time.Time) error {
	_, err := s.Pool.Exec(ctx, `DELETE FROM plugin_host_log WHERE at < $1`, before)
	return err
}

// PrunePluginEvents 删除 before 之前的观察类事件及其投递记录。
func (s *Store) PrunePluginEvents(ctx context.Context, before time.Time) error {
	_, err := s.Pool.Exec(ctx, `DELETE FROM plugin_events WHERE at < $1`, before)
	return err
}

// SetPluginStatus 更新插件的运行状态；lastError 为空时清除上次的错误。
func (s *Store) SetPluginStatus(ctx context.Context, plugin, status string, lastError *string) error {
	_, err := s.Pool.Exec(ctx, `UPDATE plugins SET status=$2, last_error=$3, updated_at=now() WHERE id=$1`, plugin, status, lastError)
	return err
}

// PluginEvent 是一条观察类事件。Data 是该类型自己的字段（JSON 对象），不含正文。
type PluginEvent struct {
	ID        string
	Type      string
	At        time.Time
	ProjectID *uuid.UUID
	AgentID   *uuid.UUID
	TaskID    *uuid.UUID
	RunID     *uuid.UUID
	Data      json.RawMessage
}

// PluginDelivery 是取出待投递的一条事件，Attempts 已含本次。
type PluginDelivery struct {
	PluginEvent
	Attempts int
}

// AddPluginEvent 在调用方提交业务事务之后写入事件，并为 plugins 中的每个插件生成一条待投递记录。
// ev.ID 为空时生成 ULID。
func (s *Store) AddPluginEvent(ctx context.Context, ev PluginEvent, plugins []string) (string, error) {
	if ev.ID == "" {
		ev.ID = NewPluginEventID()
	}
	if len(ev.Data) == 0 {
		ev.Data = json.RawMessage(`{}`)
	}
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO plugin_events(id,type,at,project_id,agent_id,task_id,run_id,data) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`,
			ev.ID, ev.Type, ev.At, ev.ProjectID, ev.AgentID, ev.TaskID, ev.RunID, ev.Data); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO plugin_event_deliveries(plugin_id,event_id) SELECT unnest($1::text[]), $2`, plugins, ev.ID)
		return err
	})
	return ev.ID, err
}

// ClaimPluginDelivery 取出该插件最早（按事件时间）到期的一条待投递事件：次数加一，下次时间推到 lease 之后，
// 这样服务端在等待确认期间退出时，租约到期后会重投。没有到期的事件时返回 nil。
func (s *Store) ClaimPluginDelivery(ctx context.Context, plugin string, lease time.Duration) (*PluginDelivery, error) {
	var d PluginDelivery
	err := s.Pool.QueryRow(ctx, `WITH next AS (
			SELECT d.event_id FROM plugin_event_deliveries d JOIN plugin_events e ON e.id=d.event_id
			WHERE d.plugin_id=$1 AND d.status='pending' AND d.next_attempt_at<=now()
			ORDER BY e.at, e.id LIMIT 1 FOR UPDATE OF d SKIP LOCKED)
		UPDATE plugin_event_deliveries d SET attempts=d.attempts+1, next_attempt_at=now()+$2::interval
		FROM next, plugin_events e WHERE d.plugin_id=$1 AND d.event_id=next.event_id AND e.id=next.event_id
		RETURNING e.id,e.type,e.at,e.project_id,e.agent_id,e.task_id,e.run_id,e.data,d.attempts`, plugin, lease).
		Scan(&d.ID, &d.Type, &d.At, &d.ProjectID, &d.AgentID, &d.TaskID, &d.RunID, &d.Data, &d.Attempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &d, nil
}

// AckPluginDelivery 记录宿主已确认处理完成，duration 是从发出到收到 ack 的时长。
func (s *Store) AckPluginDelivery(ctx context.Context, plugin, eventID string, duration time.Duration) error {
	_, err := s.Pool.Exec(ctx, `UPDATE plugin_event_deliveries SET status='delivered', acked_at=now(), last_error=NULL, duration_ms=$3
		WHERE plugin_id=$1 AND event_id=$2 AND status='pending'`, plugin, eventID, duration.Milliseconds())
	return err
}

// PluginDeliveryCounts 返回 since 以来送达与失败的投递数（全部插件）。失败按最后一次尝试的时间计，
// 即 next_attempt_at（失败时不再推进）。
func (s *Store) PluginDeliveryCounts(ctx context.Context, since time.Time) (delivered, failed int, err error) {
	err = s.Pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM plugin_event_deliveries WHERE acked_at >= $1),
		(SELECT count(*) FROM plugin_event_deliveries WHERE status='failed' AND next_attempt_at >= $1)`, since).Scan(&delivered, &failed)
	return delivered, failed, err
}

// FailPluginDelivery 记录一次投递失败：retryAt 不为空时到时重投，为空时标为 failed、不再重投。
func (s *Store) FailPluginDelivery(ctx context.Context, plugin, eventID, reason string, retryAt *time.Time) error {
	_, err := s.Pool.Exec(ctx, `UPDATE plugin_event_deliveries SET last_error=$3,
			status=CASE WHEN $4::timestamptz IS NULL THEN 'failed' ELSE 'pending' END, next_attempt_at=coalesce($4,next_attempt_at)
		WHERE plugin_id=$1 AND event_id=$2 AND status='pending'`, plugin, eventID, reason, retryAt)
	return err
}

// crockford 是 ULID 使用的 Crockford base32 字母表。
const crockford = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// NewPluginEventID 返回一个 ULID：取 UUIDv7 的 128 位按 Crockford base32 编码，
// 前 48 位是毫秒时间，同一毫秒内单调递增，所以按字符串排序就是按时间排序。
func NewPluginEventID() string {
	id := uuid.Must(uuid.NewV7())
	var out [26]byte
	// 128 位在高位补 2 个 0 位后正好是 26 组 5 位。
	var acc uint64
	bits := 2
	i := 0
	for _, b := range id {
		acc = acc<<8 | uint64(b)
		bits += 8
		for bits >= 5 {
			bits -= 5
			out[i] = crockford[(acc>>bits)&31]
			i++
		}
	}
	return string(out[:])
}
