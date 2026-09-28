BEGIN;

CREATE TRIGGER IF NOT EXISTS peer_room_rename AFTER UPDATE OF name ON rooms
WHEN OLD.name != NEW.name AND (SELECT node_id FROM peer_runtime WHERE id=1) != ''
    AND (SELECT applying FROM peer_runtime WHERE id=1)=0
BEGIN
    INSERT INTO peer_events VALUES(lower(printf('%08x-%04x', (CAST(strftime('%s','now') AS INTEGER)*1000+CAST(substr(strftime('%f','now'),4,3) AS INTEGER)) >> 16, (CAST(strftime('%s','now') AS INTEGER)*1000+CAST(substr(strftime('%f','now'),4,3) AS INTEGER)) & 65535)||'-'||'7'||substr(hex(randomblob(2)),2,3)||'-'||substr('89ab',(random() & 3)+1,1)||substr(hex(randomblob(2)),2,3)||'-'||hex(randomblob(6))),
        'room_rename',(SELECT node_id FROM peer_runtime WHERE id=1),unixepoch(),
        json_object('id',NEW.id,'name',NEW.name));
END;

PRAGMA user_version = 7;
COMMIT;
