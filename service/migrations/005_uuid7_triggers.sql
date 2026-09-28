BEGIN;

DROP TRIGGER IF EXISTS peer_user_insert;
DROP TRIGGER IF EXISTS peer_binding_insert;
DROP TRIGGER IF EXISTS peer_room_insert;
DROP TRIGGER IF EXISTS peer_rights_insert;
DROP TRIGGER IF EXISTS peer_rights_update;
DROP TRIGGER IF EXISTS peer_rights_delete;
DROP TRIGGER IF EXISTS peer_message_insert;
DROP TRIGGER IF EXISTS peer_receipt_insert;
DROP TRIGGER IF EXISTS peer_receipt_update;
DROP TRIGGER IF EXISTS peer_presence_insert;
DROP TRIGGER IF EXISTS peer_attachment_insert;
DROP TRIGGER IF EXISTS peer_settings_update;
DROP TRIGGER IF EXISTS peer_user_delete;

CREATE TRIGGER IF NOT EXISTS peer_user_insert AFTER INSERT ON users
WHEN (SELECT node_id FROM peer_runtime WHERE id=1) != '' AND (SELECT applying FROM peer_runtime WHERE id=1)=0
BEGIN
    INSERT INTO peer_events VALUES(lower(printf('%08x-%04x', (CAST(strftime('%s','now') AS INTEGER)*1000+CAST(substr(strftime('%f','now'),4,3) AS INTEGER)) >> 16, (CAST(strftime('%s','now') AS INTEGER)*1000+CAST(substr(strftime('%f','now'),4,3) AS INTEGER)) & 65535)||'-'||'7'||substr(hex(randomblob(2)),2,3)||'-'||substr('89ab',(random() & 3)+1,1)||substr(hex(randomblob(2)),2,3)||'-'||hex(randomblob(6))),
        'user',(SELECT node_id FROM peer_runtime WHERE id=1),unixepoch(),
        json_object('id',NEW.id,'name',NEW.name,'role',NEW.role,'created_at',NEW.created_at));
END;

CREATE TRIGGER IF NOT EXISTS peer_binding_insert AFTER INSERT ON device_bindings
WHEN (SELECT node_id FROM peer_runtime WHERE id=1) != '' AND (SELECT applying FROM peer_runtime WHERE id=1)=0
BEGIN
    INSERT INTO peer_events VALUES(lower(printf('%08x-%04x', (CAST(strftime('%s','now') AS INTEGER)*1000+CAST(substr(strftime('%f','now'),4,3) AS INTEGER)) >> 16, (CAST(strftime('%s','now') AS INTEGER)*1000+CAST(substr(strftime('%f','now'),4,3) AS INTEGER)) & 65535)||'-'||'7'||substr(hex(randomblob(2)),2,3)||'-'||substr('89ab',(random() & 3)+1,1)||substr(hex(randomblob(2)),2,3)||'-'||hex(randomblob(6))),
        'binding',(SELECT node_id FROM peer_runtime WHERE id=1),unixepoch(),
        json_object('user_id',NEW.user_id,'device_id',NEW.device_id,'public_key_hex',lower(hex(NEW.public_key_p256)),'activated_at',NEW.activated_at));
END;

CREATE TRIGGER IF NOT EXISTS peer_room_insert AFTER INSERT ON rooms
WHEN (SELECT node_id FROM peer_runtime WHERE id=1) != '' AND (SELECT applying FROM peer_runtime WHERE id=1)=0
BEGIN
    INSERT INTO peer_events VALUES(lower(printf('%08x-%04x', (CAST(strftime('%s','now') AS INTEGER)*1000+CAST(substr(strftime('%f','now'),4,3) AS INTEGER)) >> 16, (CAST(strftime('%s','now') AS INTEGER)*1000+CAST(substr(strftime('%f','now'),4,3) AS INTEGER)) & 65535)||'-'||'7'||substr(hex(randomblob(2)),2,3)||'-'||substr('89ab',(random() & 3)+1,1)||substr(hex(randomblob(2)),2,3)||'-'||hex(randomblob(6))),
        'room',(SELECT node_id FROM peer_runtime WHERE id=1),unixepoch(),
        json_object('id',NEW.id,'name',NEW.name,'created_by',NEW.created_by,'created_at',NEW.created_at));
END;

CREATE TRIGGER IF NOT EXISTS peer_rights_insert AFTER INSERT ON room_rights
WHEN (SELECT node_id FROM peer_runtime WHERE id=1) != '' AND (SELECT applying FROM peer_runtime WHERE id=1)=0
BEGIN
    INSERT INTO peer_events VALUES(lower(printf('%08x-%04x', (CAST(strftime('%s','now') AS INTEGER)*1000+CAST(substr(strftime('%f','now'),4,3) AS INTEGER)) >> 16, (CAST(strftime('%s','now') AS INTEGER)*1000+CAST(substr(strftime('%f','now'),4,3) AS INTEGER)) & 65535)||'-'||'7'||substr(hex(randomblob(2)),2,3)||'-'||substr('89ab',(random() & 3)+1,1)||substr(hex(randomblob(2)),2,3)||'-'||hex(randomblob(6))),
        'rights',(SELECT node_id FROM peer_runtime WHERE id=1),unixepoch(),
        json_object('room_id',NEW.room_id,'user_id',NEW.user_id,'read',json(CASE NEW.can_read WHEN 1 THEN 'true' ELSE 'false' END),'send_text',json(CASE NEW.can_send_text WHEN 1 THEN 'true' ELSE 'false' END),'add_attachment',json(CASE NEW.can_add_attachment WHEN 1 THEN 'true' ELSE 'false' END),'save_attachment',json(CASE NEW.can_save_attachment WHEN 1 THEN 'true' ELSE 'false' END)));
END;

CREATE TRIGGER IF NOT EXISTS peer_rights_update AFTER UPDATE ON room_rights
WHEN (SELECT node_id FROM peer_runtime WHERE id=1) != '' AND (SELECT applying FROM peer_runtime WHERE id=1)=0
BEGIN
    INSERT INTO peer_events VALUES(lower(printf('%08x-%04x', (CAST(strftime('%s','now') AS INTEGER)*1000+CAST(substr(strftime('%f','now'),4,3) AS INTEGER)) >> 16, (CAST(strftime('%s','now') AS INTEGER)*1000+CAST(substr(strftime('%f','now'),4,3) AS INTEGER)) & 65535)||'-'||'7'||substr(hex(randomblob(2)),2,3)||'-'||substr('89ab',(random() & 3)+1,1)||substr(hex(randomblob(2)),2,3)||'-'||hex(randomblob(6))),
        'rights',(SELECT node_id FROM peer_runtime WHERE id=1),unixepoch(),
        json_object('room_id',NEW.room_id,'user_id',NEW.user_id,'read',json(CASE NEW.can_read WHEN 1 THEN 'true' ELSE 'false' END),'send_text',json(CASE NEW.can_send_text WHEN 1 THEN 'true' ELSE 'false' END),'add_attachment',json(CASE NEW.can_add_attachment WHEN 1 THEN 'true' ELSE 'false' END),'save_attachment',json(CASE NEW.can_save_attachment WHEN 1 THEN 'true' ELSE 'false' END)));
END;

CREATE TRIGGER IF NOT EXISTS peer_rights_delete AFTER DELETE ON room_rights
WHEN (SELECT node_id FROM peer_runtime WHERE id=1) != '' AND (SELECT applying FROM peer_runtime WHERE id=1)=0
BEGIN
    INSERT INTO peer_events VALUES(lower(printf('%08x-%04x', (CAST(strftime('%s','now') AS INTEGER)*1000+CAST(substr(strftime('%f','now'),4,3) AS INTEGER)) >> 16, (CAST(strftime('%s','now') AS INTEGER)*1000+CAST(substr(strftime('%f','now'),4,3) AS INTEGER)) & 65535)||'-'||'7'||substr(hex(randomblob(2)),2,3)||'-'||substr('89ab',(random() & 3)+1,1)||substr(hex(randomblob(2)),2,3)||'-'||hex(randomblob(6))),
        'rights',(SELECT node_id FROM peer_runtime WHERE id=1),unixepoch(),
        json_object('room_id',OLD.room_id,'user_id',OLD.user_id,'deleted',json('true')));
END;

CREATE TRIGGER IF NOT EXISTS peer_message_insert AFTER INSERT ON messages
WHEN (SELECT node_id FROM peer_runtime WHERE id=1) != '' AND (SELECT applying FROM peer_runtime WHERE id=1)=0
BEGIN
    INSERT INTO peer_events VALUES(lower(printf('%08x-%04x', (CAST(strftime('%s','now') AS INTEGER)*1000+CAST(substr(strftime('%f','now'),4,3) AS INTEGER)) >> 16, (CAST(strftime('%s','now') AS INTEGER)*1000+CAST(substr(strftime('%f','now'),4,3) AS INTEGER)) & 65535)||'-'||'7'||substr(hex(randomblob(2)),2,3)||'-'||substr('89ab',(random() & 3)+1,1)||substr(hex(randomblob(2)),2,3)||'-'||hex(randomblob(6))),
        'message',(SELECT node_id FROM peer_runtime WHERE id=1),unixepoch(),
        json_object('id',NEW.id,'room_id',NEW.room_id,'user_id',NEW.sender_id,'text',NEW.text,'sent_at',NEW.sent_at,'expires_at',NEW.expires_at));
END;

CREATE TRIGGER IF NOT EXISTS peer_receipt_insert AFTER INSERT ON receipts
WHEN (SELECT node_id FROM peer_runtime WHERE id=1) != '' AND (SELECT applying FROM peer_runtime WHERE id=1)=0
BEGIN
    INSERT INTO peer_events VALUES(lower(printf('%08x-%04x', (CAST(strftime('%s','now') AS INTEGER)*1000+CAST(substr(strftime('%f','now'),4,3) AS INTEGER)) >> 16, (CAST(strftime('%s','now') AS INTEGER)*1000+CAST(substr(strftime('%f','now'),4,3) AS INTEGER)) & 65535)||'-'||'7'||substr(hex(randomblob(2)),2,3)||'-'||substr('89ab',(random() & 3)+1,1)||substr(hex(randomblob(2)),2,3)||'-'||hex(randomblob(6))),
        'receipt',(SELECT node_id FROM peer_runtime WHERE id=1),unixepoch(),
        json_object('message_id',NEW.message_id,'user_id',NEW.user_id,'state',NEW.state,'at',NEW.at));
END;

CREATE TRIGGER IF NOT EXISTS peer_receipt_update AFTER UPDATE ON receipts
WHEN (SELECT node_id FROM peer_runtime WHERE id=1) != '' AND (SELECT applying FROM peer_runtime WHERE id=1)=0
BEGIN
    INSERT INTO peer_events VALUES(lower(printf('%08x-%04x', (CAST(strftime('%s','now') AS INTEGER)*1000+CAST(substr(strftime('%f','now'),4,3) AS INTEGER)) >> 16, (CAST(strftime('%s','now') AS INTEGER)*1000+CAST(substr(strftime('%f','now'),4,3) AS INTEGER)) & 65535)||'-'||'7'||substr(hex(randomblob(2)),2,3)||'-'||substr('89ab',(random() & 3)+1,1)||substr(hex(randomblob(2)),2,3)||'-'||hex(randomblob(6))),
        'receipt',(SELECT node_id FROM peer_runtime WHERE id=1),unixepoch(),
        json_object('message_id',NEW.message_id,'user_id',NEW.user_id,'state',NEW.state,'at',NEW.at));
END;

CREATE TRIGGER IF NOT EXISTS peer_presence_insert AFTER INSERT ON room_events
WHEN (SELECT node_id FROM peer_runtime WHERE id=1) != '' AND (SELECT applying FROM peer_runtime WHERE id=1)=0
BEGIN
    INSERT INTO peer_events VALUES(lower(printf('%08x-%04x', (CAST(strftime('%s','now') AS INTEGER)*1000+CAST(substr(strftime('%f','now'),4,3) AS INTEGER)) >> 16, (CAST(strftime('%s','now') AS INTEGER)*1000+CAST(substr(strftime('%f','now'),4,3) AS INTEGER)) & 65535)||'-'||'7'||substr(hex(randomblob(2)),2,3)||'-'||substr('89ab',(random() & 3)+1,1)||substr(hex(randomblob(2)),2,3)||'-'||hex(randomblob(6))),
        'presence',(SELECT node_id FROM peer_runtime WHERE id=1),unixepoch(),
        json_object('id',NEW.id,'room_id',NEW.room_id,'actor_id',NEW.actor_id,'created_at',NEW.created_at));
END;

CREATE TRIGGER IF NOT EXISTS peer_attachment_insert AFTER INSERT ON attachments
WHEN (SELECT node_id FROM peer_runtime WHERE id=1) != '' AND (SELECT applying FROM peer_runtime WHERE id=1)=0
BEGIN
    INSERT INTO peer_events VALUES(lower(printf('%08x-%04x', (CAST(strftime('%s','now') AS INTEGER)*1000+CAST(substr(strftime('%f','now'),4,3) AS INTEGER)) >> 16, (CAST(strftime('%s','now') AS INTEGER)*1000+CAST(substr(strftime('%f','now'),4,3) AS INTEGER)) & 65535)||'-'||'7'||substr(hex(randomblob(2)),2,3)||'-'||substr('89ab',(random() & 3)+1,1)||substr(hex(randomblob(2)),2,3)||'-'||hex(randomblob(6))),
        'attachment',(SELECT node_id FROM peer_runtime WHERE id=1),unixepoch(),
        json_object('id',NEW.id,'room_id',NEW.room_id,'user_id',NEW.sender_id,'name',NEW.name,'mime_type',NEW.mime_type,
            'size',NEW.size,'sha256',lower(hex(NEW.sha256)),'sent_at',NEW.sent_at,'expires_at',NEW.expires_at));
END;

CREATE TRIGGER IF NOT EXISTS peer_settings_update AFTER UPDATE ON settings
WHEN (SELECT node_id FROM peer_runtime WHERE id=1) != '' AND (SELECT applying FROM peer_runtime WHERE id=1)=0
BEGIN
    INSERT INTO peer_events VALUES(lower(printf('%08x-%04x', (CAST(strftime('%s','now') AS INTEGER)*1000+CAST(substr(strftime('%f','now'),4,3) AS INTEGER)) >> 16, (CAST(strftime('%s','now') AS INTEGER)*1000+CAST(substr(strftime('%f','now'),4,3) AS INTEGER)) & 65535)||'-'||'7'||substr(hex(randomblob(2)),2,3)||'-'||substr('89ab',(random() & 3)+1,1)||substr(hex(randomblob(2)),2,3)||'-'||hex(randomblob(6))),
        'settings',(SELECT node_id FROM peer_runtime WHERE id=1),unixepoch(),
        json_object('attachment_max_bytes',NEW.attachment_max_bytes,'retention_seconds',NEW.retention_seconds));
END;

CREATE TRIGGER IF NOT EXISTS peer_user_delete AFTER INSERT ON user_deletions
WHEN (SELECT node_id FROM peer_runtime WHERE id=1) != '' AND (SELECT applying FROM peer_runtime WHERE id=1)=0
BEGIN
    INSERT INTO peer_events VALUES(lower(printf('%08x-%04x', (CAST(strftime('%s','now') AS INTEGER)*1000+CAST(substr(strftime('%f','now'),4,3) AS INTEGER)) >> 16, (CAST(strftime('%s','now') AS INTEGER)*1000+CAST(substr(strftime('%f','now'),4,3) AS INTEGER)) & 65535)||'-'||'7'||substr(hex(randomblob(2)),2,3)||'-'||substr('89ab',(random() & 3)+1,1)||substr(hex(randomblob(2)),2,3)||'-'||hex(randomblob(6))),
        'user_deleted',(SELECT node_id FROM peer_runtime WHERE id=1),unixepoch(),
        json_object('user_id',NEW.user_id,'deleted_at',NEW.deleted_at));
END;

PRAGMA user_version = 5;
COMMIT;
