-- +goose Up
-- The remote door: a second loopback listener for phones and other devices (one row, off at first). port is the port
-- a person set; NULL lets the server pick one, and picked_port keeps the one it picked so the address stays the same
-- across restarts while that port is free.
CREATE TABLE remote_access_settings (
    singleton    INTEGER NOT NULL DEFAULT 1 CHECK (singleton IN (0, 1)),
    enabled      INTEGER NOT NULL DEFAULT 0 CHECK (enabled IN (0, 1)),
    port         INTEGER CHECK (port BETWEEN 1 AND 65535),
    picked_port  INTEGER CHECK (picked_port BETWEEN 1 AND 65535),
    updated_at   INTEGER NOT NULL DEFAULT (now()),
    CONSTRAINT remote_access_settings_pkey PRIMARY KEY (singleton),
    CONSTRAINT remote_access_settings_singleton_check CHECK (singleton)
) STRICT;

INSERT INTO remote_access_settings DEFAULT VALUES;

-- A paired device's session at the remote door. Only the sha256 of the session secret is kept; removing a device
-- deletes its row. A session whose last_seen_at is more than 30 days old has expired (checked by the server).
CREATE TABLE device_sessions (
    id            TEXT NOT NULL,
    name          TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 64),
    secret_hash   BLOB NOT NULL CHECK (length(secret_hash) = 32),
    created_at    INTEGER NOT NULL,
    last_seen_at  INTEGER NOT NULL,
    last_address  TEXT,
    CONSTRAINT device_sessions_pkey PRIMARY KEY (id)
) STRICT;
CREATE INDEX device_sessions_by_last_seen ON device_sessions (last_seen_at DESC);

-- +goose Down
DROP TABLE device_sessions;
DROP TABLE remote_access_settings;
