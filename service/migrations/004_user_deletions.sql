BEGIN;

CREATE TABLE IF NOT EXISTS user_deletions (
    user_id TEXT PRIMARY KEY REFERENCES users(id),
    deleted_at INTEGER NOT NULL
);

CREATE TRIGGER IF NOT EXISTS peer_user_delete AFTER INSERT ON user_deletions
WHEN (SELECT node_id FROM peer_runtime WHERE id=1) != '' AND (SELECT applying FROM peer_runtime WHERE id=1)=0
BEGIN
    INSERT INTO peer_events VALUES(lower(hex(randomblob(4))||'-'||hex(randomblob(2))||'-'||hex(randomblob(2))||'-'||hex(randomblob(2))||'-'||hex(randomblob(6))),
        'user_deleted',(SELECT node_id FROM peer_runtime WHERE id=1),unixepoch(),
        json_object('user_id',NEW.user_id,'deleted_at',NEW.deleted_at));
END;

PRAGMA user_version = 4;
COMMIT;
