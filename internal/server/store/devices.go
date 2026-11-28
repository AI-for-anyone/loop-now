package store

import (
	"context"
	"time"

	"github.com/google/uuid"

	"loop-now/internal/server/db"
)

// RemoteAccessSettings is the saved setting of the remote door: whether it is on, the port a person set (nil lets the
// server pick one) and the port the server picked last time (kept so the address does not change across restarts).
type RemoteAccessSettings struct {
	Enabled    bool
	Port       *int
	PickedPort *int
}

// GetRemoteAccessSettings reads the remote door setting.
func (s *Store) GetRemoteAccessSettings(ctx context.Context) (RemoteAccessSettings, error) {
	var out RemoteAccessSettings
	var port, picked *int64
	err := s.Pool.QueryRow(ctx, `SELECT enabled, port, picked_port FROM remote_access_settings WHERE singleton`).Scan(&out.Enabled, &port, &picked)
	out.Port, out.PickedPort = intPtr(port), intPtr(picked)
	return out, err
}

// PutRemoteAccessSettings saves whether the remote door is on and the port a person set; the picked port is kept.
func (s *Store) PutRemoteAccessSettings(ctx context.Context, enabled bool, port *int) error {
	_, err := s.Pool.Exec(ctx, `UPDATE remote_access_settings SET enabled = $1, port = $2, updated_at = now() WHERE singleton`, enabled, port)
	return err
}

// SetRemoteAccessPickedPort records the port the server picked for the remote door.
func (s *Store) SetRemoteAccessPickedPort(ctx context.Context, port int) error {
	_, err := s.Pool.Exec(ctx, `UPDATE remote_access_settings SET picked_port = $1, updated_at = now() WHERE singleton`, port)
	return err
}

func intPtr(v *int64) *int {
	if v == nil {
		return nil
	}
	i := int(*v)
	return &i
}

// DeviceSession is a paired device's session at the remote door.
type DeviceSession struct {
	ID          uuid.UUID
	Name        string
	CreatedAt   time.Time
	LastSeenAt  time.Time
	LastAddress *string
}

const deviceColumns = `id, name, created_at, last_seen_at, last_address`

func scanDevice(row db.Scanner) (DeviceSession, error) {
	var d DeviceSession
	err := row.Scan(&d.ID, &d.Name, &d.CreatedAt, &d.LastSeenAt, &d.LastAddress)
	return d, err
}

// InsertDeviceSession stores a new device session with the hash of its secret; CreatedAt and LastSeenAt are taken from
// d.
func (s *Store) InsertDeviceSession(ctx context.Context, d DeviceSession, hash []byte) (DeviceSession, error) {
	return scanDevice(s.Pool.QueryRow(ctx, `INSERT INTO device_sessions (id, name, secret_hash, created_at, last_seen_at, last_address)
		VALUES ($1, $2, $3, $4, $5, $6) RETURNING `+deviceColumns, d.ID, d.Name, hash, d.CreatedAt, d.LastSeenAt, d.LastAddress))
}

// LookupDeviceSession reads a device session and the hash of its secret; not_found when there is none. The caller
// decides whether it is still valid.
func (s *Store) LookupDeviceSession(ctx context.Context, id uuid.UUID) (DeviceSession, []byte, error) {
	var d DeviceSession
	var hash []byte
	err := s.Pool.QueryRow(ctx, `SELECT `+deviceColumns+`, secret_hash FROM device_sessions WHERE id = $1`, id).
		Scan(&d.ID, &d.Name, &d.CreatedAt, &d.LastSeenAt, &d.LastAddress, &hash)
	return d, hash, notFoundIfNoRows(err, "Device")
}

// TouchDeviceSession records a device's request at time at from address, at most once a minute; it reports whether it
// wrote.
func (s *Store) TouchDeviceSession(ctx context.Context, id uuid.UUID, at time.Time, address *string) (bool, error) {
	n, err := s.Pool.Exec(ctx, `UPDATE device_sessions SET last_seen_at = $2, last_address = $3 WHERE id = $1 AND last_seen_at < $4`,
		id, at, address, at.Add(-time.Minute))
	if err != nil {
		return false, err
	}
	return n.RowsAffected() > 0, nil
}

// ListDeviceSessions deletes the sessions last seen before cutoff (expired) and lists the others, the most recently
// seen first.
func (s *Store) ListDeviceSessions(ctx context.Context, cutoff time.Time) ([]DeviceSession, error) {
	var out []DeviceSession
	err := s.inTx(ctx, func(tx *db.Tx) error {
		if _, err := tx.Exec(ctx, `DELETE FROM device_sessions WHERE last_seen_at < $1`, cutoff); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT `+deviceColumns+` FROM device_sessions ORDER BY last_seen_at DESC, id DESC`)
		if err != nil {
			return err
		}
		out, err = db.CollectRows(rows, func(r db.CollectableRow) (DeviceSession, error) { return scanDevice(r) })
		return err
	})
	return out, err
}

// RenameDeviceSession changes a device's name; not_found when there is no such device.
func (s *Store) RenameDeviceSession(ctx context.Context, id uuid.UUID, name string) (DeviceSession, error) {
	d, err := scanDevice(s.Pool.QueryRow(ctx, `UPDATE device_sessions SET name = $2 WHERE id = $1 RETURNING `+deviceColumns, id, name))
	return d, notFoundIfNoRows(err, "Device")
}

// DeleteDeviceSession removes a device's session; not_found when there is no such device.
func (s *Store) DeleteDeviceSession(ctx context.Context, id uuid.UUID) error {
	n, err := s.Pool.Exec(ctx, `DELETE FROM device_sessions WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if n.RowsAffected() == 0 {
		return notFoundIfNoRows(db.ErrNoRows, "Device")
	}
	return nil
}
