package store

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"loop-now/internal/server/db"
)

// Data the plugin host reads and writes (plugins/SPEC.md §3, §8): persistent state, plugin host logs, plugin status
// and delivery of observation events.

// PluginStateEntry is one item of a plugin's persistent state.
type PluginStateEntry struct {
	Key   string          `json:"key"`
	Value json.RawMessage `json:"value"`
}

// PluginStateListLimit is the maximum number of items one state listing returns.
const PluginStateListLimit = 1000

// PluginStateGet reads one state item; returns nil when it does not exist. A nil project means plugin-global.
func (s *Store) PluginStateGet(ctx context.Context, plugin string, project *uuid.UUID, key string) (json.RawMessage, error) {
	var v json.RawMessage
	err := s.Pool.QueryRow(ctx, `SELECT value FROM plugin_state WHERE plugin_id=$1 AND project_id IS $2 AND key=$3`, plugin, project, key).Scan(&v)
	if errors.Is(err, db.ErrNoRows) {
		return nil, nil
	}
	return v, err
}

// PluginStateSet writes one state item, overwriting any existing one.
func (s *Store) PluginStateSet(ctx context.Context, plugin string, project *uuid.UUID, key string, value json.RawMessage) error {
	_, err := s.Pool.Exec(ctx, `INSERT INTO plugin_state(plugin_id,project_id,key,value) VALUES($1,$2,$3,$4)
		ON CONFLICT (plugin_id, coalesce(project_id, ''), key) DO UPDATE SET value=excluded.value, updated_at=now()`, plugin, project, key, value)
	return err
}

// PluginStateDelete deletes one state item; also succeeds when it does not exist.
func (s *Store) PluginStateDelete(ctx context.Context, plugin string, project *uuid.UUID, key string) error {
	_, err := s.Pool.Exec(ctx, `DELETE FROM plugin_state WHERE plugin_id=$1 AND project_id IS $2 AND key=$3`, plugin, project, key)
	return err
}

// PluginStateList lists, sorted by key, the state items whose key starts with prefix, at most PluginStateListLimit
// items.
func (s *Store) PluginStateList(ctx context.Context, plugin string, project *uuid.UUID, prefix string) ([]PluginStateEntry, error) {
	rows, err := s.Pool.Query(ctx, `SELECT key,value FROM plugin_state WHERE plugin_id=$1 AND project_id IS $2 AND substr(key,1,length($3))=$3
		ORDER BY key LIMIT $4`, plugin, project, prefix, PluginStateListLimit)
	if err != nil {
		return nil, err
	}
	return db.CollectRows(rows, func(r db.CollectableRow) (PluginStateEntry, error) {
		var e PluginStateEntry
		return e, r.Scan(&e.Key, &e.Value)
	})
}

// pluginLogLineMax is the maximum number of bytes kept for one plugin host log line; the excess is truncated.
const pluginLogLineMax = 8 << 10

// AppendPluginHostLog records one plugin host log line; an empty plugin means the plugin host itself.
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

// PrunePluginHostLog deletes the plugin host logs before before.
func (s *Store) PrunePluginHostLog(ctx context.Context, before time.Time) error {
	_, err := s.Pool.Exec(ctx, `DELETE FROM plugin_host_log WHERE at < $1`, before)
	return err
}

// PrunePluginEvents deletes the observation events before before, along with their delivery records.
func (s *Store) PrunePluginEvents(ctx context.Context, before time.Time) error {
	_, err := s.Pool.Exec(ctx, `DELETE FROM plugin_events WHERE at < $1`, before)
	return err
}

// SetPluginStatus updates a plugin's running status; a nil lastError clears the previous error.
func (s *Store) SetPluginStatus(ctx context.Context, plugin, status string, lastError *string) error {
	_, err := s.Pool.Exec(ctx, `UPDATE plugins SET status=$2, last_error=$3, updated_at=now() WHERE id=$1`, plugin, status, lastError)
	return err
}

// PluginEvent is one observation event. Data holds the type's own fields (a JSON object), without message text.
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

// PluginDelivery is one event claimed for delivery; Attempts already includes this attempt.
type PluginDelivery struct {
	PluginEvent
	Attempts int
}

// AddPluginEvent writes an event after the caller has committed its business transaction, and creates one pending
// delivery record for each plugin in plugins.
// A ULID is generated when ev.ID is empty.
func (s *Store) AddPluginEvent(ctx context.Context, ev PluginEvent, plugins []string) (string, error) {
	if ev.ID == "" {
		ev.ID = NewPluginEventID()
	}
	if len(ev.Data) == 0 {
		ev.Data = json.RawMessage(`{}`)
	}
	err := s.inTx(ctx, func(tx *db.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO plugin_events(id,type,at,project_id,agent_id,task_id,run_id,data) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`,
			ev.ID, ev.Type, ev.At, ev.ProjectID, ev.AgentID, ev.TaskID, ev.RunID, ev.Data); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO plugin_event_deliveries(plugin_id,event_id,next_attempt_at) SELECT value, $2, now() FROM json_each($1)`, plugins, ev.ID)
		return err
	})
	return ev.ID, err
}

// ClaimPluginDelivery claims the plugin's earliest due pending event (by event time): the attempt count goes up by
// one and the next attempt time is pushed past lease, so that if the server exits while waiting for the ack, the
// event is redelivered once the lease expires. Returns nil when no event is due.
func (s *Store) ClaimPluginDelivery(ctx context.Context, plugin string, lease time.Duration) (*PluginDelivery, error) {
	var d *PluginDelivery
	err := s.inTx(ctx, func(tx *db.Tx) error {
		var eventID string
		err := tx.QueryRow(ctx, `SELECT d.event_id FROM plugin_event_deliveries d JOIN plugin_events e ON e.id=d.event_id
			WHERE d.plugin_id=$1 AND d.status='pending' AND d.next_attempt_at<=now()
			ORDER BY e.at, e.id LIMIT 1`, plugin).Scan(&eventID)
		if errors.Is(err, db.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		d = &PluginDelivery{}
		if err := tx.QueryRow(ctx, `UPDATE plugin_event_deliveries SET attempts=attempts+1, next_attempt_at=now()+$3
			WHERE plugin_id=$1 AND event_id=$2 RETURNING attempts`, plugin, eventID, lease.Microseconds()).Scan(&d.Attempts); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `SELECT id,type,at,project_id,agent_id,task_id,run_id,data FROM plugin_events WHERE id=$1`, eventID).
			Scan(&d.ID, &d.Type, &d.At, &d.ProjectID, &d.AgentID, &d.TaskID, &d.RunID, &d.Data)
	})
	if err != nil {
		return nil, err
	}
	return d, nil
}

// AckPluginDelivery records that the plugin host confirmed it finished handling the event; duration is the time from
// sending to receiving the ack.
func (s *Store) AckPluginDelivery(ctx context.Context, plugin, eventID string, duration time.Duration) error {
	_, err := s.Pool.Exec(ctx, `UPDATE plugin_event_deliveries SET status='delivered', acked_at=now(), last_error=NULL, duration_ms=$3
		WHERE plugin_id=$1 AND event_id=$2 AND status='pending'`, plugin, eventID, duration.Milliseconds())
	return err
}

// PluginDeliveryCounts returns the number of delivered and failed deliveries since since (all plugins). Failures are
// counted by the time of the last attempt, i.e. next_attempt_at (which no longer advances on failure).
func (s *Store) PluginDeliveryCounts(ctx context.Context, since time.Time) (delivered, failed int, err error) {
	err = s.Pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM plugin_event_deliveries WHERE acked_at >= $1),
		(SELECT count(*) FROM plugin_event_deliveries WHERE status='failed' AND next_attempt_at >= $1)`, since).Scan(&delivered, &failed)
	return delivered, failed, err
}

// FailPluginDelivery records a failed delivery: when retryAt is not nil the event is redelivered at that time; when it
// is nil it is marked failed and never redelivered.
func (s *Store) FailPluginDelivery(ctx context.Context, plugin, eventID, reason string, retryAt *time.Time) error {
	_, err := s.Pool.Exec(ctx, `UPDATE plugin_event_deliveries SET last_error=$3,
			status=CASE WHEN $4 IS NULL THEN 'failed' ELSE 'pending' END, next_attempt_at=coalesce($4,next_attempt_at)
		WHERE plugin_id=$1 AND event_id=$2 AND status='pending'`, plugin, eventID, reason, retryAt)
	return err
}

// crockford is the Crockford base32 alphabet that ULID uses.
const crockford = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// NewPluginEventID returns a ULID: the 128 bits of a UUIDv7 encoded in Crockford base32. The first 48 bits are the
// millisecond time and are monotonically increasing within a millisecond, so sorting as strings sorts by time.
func NewPluginEventID() string {
	id := uuid.Must(uuid.NewV7())
	var out [26]byte
	// 128 bits, padded with 2 zero bits at the top, are exactly 26 groups of 5 bits.
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

// eventLogTables are the log tables in events.db pruned by time (plugin_host_log is pruned to 7 days by the plugin
// host itself).
var eventLogTables = []string{"lifecycle_events", "run_tool_events", "audit_log"}

// PruneEventLogs deletes the rows before before from the three log tables, at most batch rows per transaction, so as
// not to hold the write connection for long.
// Returns the number of rows deleted.
func (s *Store) PruneEventLogs(ctx context.Context, before time.Time, batch int) (int64, error) {
	var total int64
	for _, table := range eventLogTables {
		for {
			tag, err := s.Pool.Exec(ctx, `DELETE FROM `+table+` WHERE rowid IN (SELECT rowid FROM `+table+` WHERE at < $1 LIMIT $2)`, before, batch)
			if err != nil {
				return total, err
			}
			total += tag.RowsAffected()
			if tag.RowsAffected() < int64(batch) {
				break
			}
		}
	}
	return total, nil
}
