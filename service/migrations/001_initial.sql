-- Включать PRAGMA foreign_keys=ON для каждого соединения до записи данных.
BEGIN;

CREATE TABLE IF NOT EXISTS users (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 200),
    role TEXT NOT NULL CHECK (role IN ('user', 'admin', 'commander')),
    created_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS device_bindings (
    user_id TEXT PRIMARY KEY REFERENCES users(id),
    device_id TEXT NOT NULL UNIQUE,
    public_key_p256 BLOB NOT NULL,
    activated_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS activation_codes (
    code_hash BLOB PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES users(id),
    expires_at INTEGER NOT NULL,
    used_at INTEGER
);

CREATE TABLE IF NOT EXISTS rooms (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 200),
    created_by TEXT NOT NULL REFERENCES users(id),
    created_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS room_rights (
    room_id TEXT NOT NULL REFERENCES rooms(id),
    user_id TEXT NOT NULL REFERENCES users(id),
    can_read INTEGER NOT NULL DEFAULT 0 CHECK (can_read IN (0, 1)),
    can_send_text INTEGER NOT NULL DEFAULT 0 CHECK (can_send_text IN (0, 1)),
    can_add_attachment INTEGER NOT NULL DEFAULT 0 CHECK (can_add_attachment IN (0, 1)),
    can_save_attachment INTEGER NOT NULL DEFAULT 0 CHECK (can_save_attachment IN (0, 1)),
    PRIMARY KEY (room_id, user_id)
);
CREATE INDEX IF NOT EXISTS room_rights_by_user ON room_rights(user_id, room_id);

CREATE TABLE IF NOT EXISTS messages (
    id TEXT PRIMARY KEY,
    room_id TEXT NOT NULL REFERENCES rooms(id),
    sender_id TEXT NOT NULL REFERENCES users(id),
    text TEXT NOT NULL CHECK (length(text) > 0),
    sent_at INTEGER NOT NULL,
    expires_at INTEGER NOT NULL CHECK (expires_at > sent_at)
);
CREATE INDEX IF NOT EXISTS messages_by_room ON messages(room_id, sent_at, id);
CREATE INDEX IF NOT EXISTS messages_by_expiry ON messages(expires_at);

CREATE TABLE IF NOT EXISTS receipts (
    message_id TEXT NOT NULL REFERENCES messages(id),
    user_id TEXT NOT NULL REFERENCES users(id),
    state TEXT NOT NULL CHECK (state IN ('delivered', 'read')),
    at INTEGER NOT NULL,
    PRIMARY KEY (message_id, user_id)
);

CREATE TABLE IF NOT EXISTS attachments (
    id TEXT PRIMARY KEY,
    room_id TEXT NOT NULL REFERENCES rooms(id),
    sender_id TEXT NOT NULL REFERENCES users(id),
    name TEXT NOT NULL,
    mime_type TEXT NOT NULL,
    size INTEGER NOT NULL CHECK (size >= 0),
    sha256 BLOB NOT NULL CHECK (length(sha256) = 32),
    storage_name TEXT NOT NULL UNIQUE,
    sent_at INTEGER NOT NULL,
    expires_at INTEGER NOT NULL CHECK (expires_at > sent_at)
);
CREATE INDEX IF NOT EXISTS attachments_by_room ON attachments(room_id, sent_at, id);
CREATE INDEX IF NOT EXISTS attachments_by_expiry ON attachments(expires_at);

CREATE TABLE IF NOT EXISTS room_events (
    id TEXT PRIMARY KEY,
    room_id TEXT NOT NULL REFERENCES rooms(id),
    kind TEXT NOT NULL CHECK (kind = 'commander_entered'),
    actor_id TEXT NOT NULL REFERENCES users(id),
    created_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS room_events_by_room ON room_events(room_id, created_at, id);

CREATE TABLE IF NOT EXISTS peer_events (
    id TEXT PRIMARY KEY,
    kind TEXT NOT NULL,
    author_node_id TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    payload BLOB NOT NULL
);
CREATE INDEX IF NOT EXISTS peer_events_order ON peer_events(created_at, id);

CREATE TABLE IF NOT EXISTS settings (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    attachment_max_bytes INTEGER NOT NULL CHECK (attachment_max_bytes > 0),
    retention_seconds INTEGER NOT NULL CHECK (retention_seconds > 0)
);
INSERT OR IGNORE INTO settings(id, attachment_max_bytes, retention_seconds)
VALUES (1, 104857600, 259200);

PRAGMA user_version = 1;
COMMIT;
