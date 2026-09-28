BEGIN;
CREATE TABLE IF NOT EXISTS request_nonces (
    device_id TEXT NOT NULL REFERENCES device_bindings(device_id),
    nonce TEXT NOT NULL,
    seen_at INTEGER NOT NULL,
    PRIMARY KEY (device_id, nonce)
);
CREATE INDEX IF NOT EXISTS request_nonces_by_time ON request_nonces(seen_at);
PRAGMA user_version = 2;
COMMIT;
