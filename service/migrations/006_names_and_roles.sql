BEGIN;

CREATE UNIQUE INDEX IF NOT EXISTS users_unique_name ON users(name COLLATE NOCASE);
CREATE UNIQUE INDEX IF NOT EXISTS rooms_unique_name ON rooms(name COLLATE NOCASE);

DROP TRIGGER IF EXISTS peer_user_update;
CREATE TRIGGER peer_user_update AFTER UPDATE OF role ON users
WHEN OLD.role != NEW.role AND (SELECT node_id FROM peer_runtime WHERE id=1) != ''
    AND (SELECT applying FROM peer_runtime WHERE id=1)=0
BEGIN
    INSERT INTO peer_events VALUES(lower(printf('%08x-%04x', (CAST(strftime('%s','now') AS INTEGER)*1000+CAST(substr(strftime('%f','now'),4,3) AS INTEGER)) >> 16, (CAST(strftime('%s','now') AS INTEGER)*1000+CAST(substr(strftime('%f','now'),4,3) AS INTEGER)) & 65535)||'-'||'7'||substr(hex(randomblob(2)),2,3)||'-'||substr('89ab',(random() & 3)+1,1)||substr(hex(randomblob(2)),2,3)||'-'||hex(randomblob(6))),
        'user_role',(SELECT node_id FROM peer_runtime WHERE id=1),unixepoch(),
        json_object('id',NEW.id,'role',NEW.role));
END;

PRAGMA user_version = 6;
COMMIT;
