BEGIN;

CREATE TABLE IF NOT EXISTS peer_runtime (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    node_id TEXT NOT NULL,
    applying INTEGER NOT NULL CHECK (applying IN (0, 1))
);
INSERT OR IGNORE INTO peer_runtime(id,node_id,applying) VALUES(1,'',0);

CREATE TABLE IF NOT EXISTS peer_cursor (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    value INTEGER NOT NULL DEFAULT 0
);
INSERT OR IGNORE INTO peer_cursor(id,value) VALUES(1,0);

CREATE TRIGGER IF NOT EXISTS peer_user_insert AFTER INSERT ON users
WHEN (SELECT node_id FROM peer_runtime WHERE id=1) != '' AND (SELECT applying FROM peer_runtime WHERE id=1)=0
BEGIN
    INSERT INTO peer_events VALUES(lower(hex(randomblob(4))||'-'||hex(randomblob(2))||'-'||hex(randomblob(2))||'-'||hex(randomblob(2))||'-'||hex(randomblob(6))),
        'user',(SELECT node_id FROM peer_runtime WHERE id=1),unixepoch(),
        json_object('id',NEW.id,'name',NEW.name,'role',NEW.role,'created_at',NEW.created_at));
END;

CREATE TRIGGER IF NOT EXISTS peer_binding_insert AFTER INSERT ON device_bindings
WHEN (SELECT node_id FROM peer_runtime WHERE id=1) != '' AND (SELECT applying FROM peer_runtime WHERE id=1)=0
BEGIN
    INSERT INTO peer_events VALUES(lower(hex(randomblob(4))||'-'||hex(randomblob(2))||'-'||hex(randomblob(2))||'-'||hex(randomblob(2))||'-'||hex(randomblob(6))),
        'binding',(SELECT node_id FROM peer_runtime WHERE id=1),unixepoch(),
        json_object('user_id',NEW.user_id,'device_id',NEW.device_id,'public_key_hex',lower(hex(NEW.public_key_p256)),'activated_at',NEW.activated_at));
END;

CREATE TRIGGER IF NOT EXISTS peer_room_insert AFTER INSERT ON rooms
WHEN (SELECT node_id FROM peer_runtime WHERE id=1) != '' AND (SELECT applying FROM peer_runtime WHERE id=1)=0
BEGIN
    INSERT INTO peer_events VALUES(lower(hex(randomblob(4))||'-'||hex(randomblob(2))||'-'||hex(randomblob(2))||'-'||hex(randomblob(2))||'-'||hex(randomblob(6))),
        'room',(SELECT node_id FROM peer_runtime WHERE id=1),unixepoch(),
        json_object('id',NEW.id,'name',NEW.name,'created_by',NEW.created_by,'created_at',NEW.created_at));
END;

CREATE TRIGGER IF NOT EXISTS peer_rights_insert AFTER INSERT ON room_rights
WHEN (SELECT node_id FROM peer_runtime WHERE id=1) != '' AND (SELECT applying FROM peer_runtime WHERE id=1)=0
BEGIN
    INSERT INTO peer_events VALUES(lower(hex(randomblob(4))||'-'||hex(randomblob(2))||'-'||hex(randomblob(2))||'-'||hex(randomblob(2))||'-'||hex(randomblob(6))),
        'rights',(SELECT node_id FROM peer_runtime WHERE id=1),unixepoch(),
        json_object('room_id',NEW.room_id,'user_id',NEW.user_id,'read',json(CASE NEW.can_read WHEN 1 THEN 'true' ELSE 'false' END),'send_text',json(CASE NEW.can_send_text WHEN 1 THEN 'true' ELSE 'false' END),'add_attachment',json(CASE NEW.can_add_attachment WHEN 1 THEN 'true' ELSE 'false' END),'save_attachment',json(CASE NEW.can_save_attachment WHEN 1 THEN 'true' ELSE 'false' END)));
END;

CREATE TRIGGER IF NOT EXISTS peer_rights_update AFTER UPDATE ON room_rights
WHEN (SELECT node_id FROM peer_runtime WHERE id=1) != '' AND (SELECT applying FROM peer_runtime WHERE id=1)=0
BEGIN
    INSERT INTO peer_events VALUES(lower(hex(randomblob(4))||'-'||hex(randomblob(2))||'-'||hex(randomblob(2))||'-'||hex(randomblob(2))||'-'||hex(randomblob(6))),
        'rights',(SELECT node_id FROM peer_runtime WHERE id=1),unixepoch(),
        json_object('room_id',NEW.room_id,'user_id',NEW.user_id,'read',json(CASE NEW.can_read WHEN 1 THEN 'true' ELSE 'false' END),'send_text',json(CASE NEW.can_send_text WHEN 1 THEN 'true' ELSE 'false' END),'add_attachment',json(CASE NEW.can_add_attachment WHEN 1 THEN 'true' ELSE 'false' END),'save_attachment',json(CASE NEW.can_save_attachment WHEN 1 THEN 'true' ELSE 'false' END)));
END;

CREATE TRIGGER IF NOT EXISTS peer_rights_delete AFTER DELETE ON room_rights
WHEN (SELECT node_id FROM peer_runtime WHERE id=1) != '' AND (SELECT applying FROM peer_runtime WHERE id=1)=0
BEGIN
    INSERT INTO peer_events VALUES(lower(hex(randomblob(4))||'-'||hex(randomblob(2))||'-'||hex(randomblob(2))||'-'||hex(randomblob(2))||'-'||hex(randomblob(6))),
        'rights',(SELECT node_id FROM peer_runtime WHERE id=1),unixepoch(),
        json_object('room_id',OLD.room_id,'user_id',OLD.user_id,'deleted',json('true')));
END;

CREATE TRIGGER IF NOT EXISTS peer_message_insert AFTER INSERT ON messages
WHEN (SELECT node_id FROM peer_runtime WHERE id=1) != '' AND (SELECT applying FROM peer_runtime WHERE id=1)=0
BEGIN
    INSERT INTO peer_events VALUES(lower(hex(randomblob(4))||'-'||hex(randomblob(2))||'-'||hex(randomblob(2))||'-'||hex(randomblob(2))||'-'||hex(randomblob(6))),
        'message',(SELECT node_id FROM peer_runtime WHERE id=1),unixepoch(),
        json_object('id',NEW.id,'room_id',NEW.room_id,'user_id',NEW.sender_id,'text',NEW.text,'sent_at',NEW.sent_at,'expires_at',NEW.expires_at));
END;

CREATE TRIGGER IF NOT EXISTS peer_receipt_insert AFTER INSERT ON receipts
WHEN (SELECT node_id FROM peer_runtime WHERE id=1) != '' AND (SELECT applying FROM peer_runtime WHERE id=1)=0
BEGIN
    INSERT INTO peer_events VALUES(lower(hex(randomblob(4))||'-'||hex(randomblob(2))||'-'||hex(randomblob(2))||'-'||hex(randomblob(2))||'-'||hex(randomblob(6))),
        'receipt',(SELECT node_id FROM peer_runtime WHERE id=1),unixepoch(),
        json_object('message_id',NEW.message_id,'user_id',NEW.user_id,'state',NEW.state,'at',NEW.at));
END;

CREATE TRIGGER IF NOT EXISTS peer_receipt_update AFTER UPDATE ON receipts
WHEN (SELECT node_id FROM peer_runtime WHERE id=1) != '' AND (SELECT applying FROM peer_runtime WHERE id=1)=0
BEGIN
    INSERT INTO peer_events VALUES(lower(hex(randomblob(4))||'-'||hex(randomblob(2))||'-'||hex(randomblob(2))||'-'||hex(randomblob(2))||'-'||hex(randomblob(6))),
        'receipt',(SELECT node_id FROM peer_runtime WHERE id=1),unixepoch(),
        json_object('message_id',NEW.message_id,'user_id',NEW.user_id,'state',NEW.state,'at',NEW.at));
END;

CREATE TRIGGER IF NOT EXISTS peer_presence_insert AFTER INSERT ON room_events
WHEN (SELECT node_id FROM peer_runtime WHERE id=1) != '' AND (SELECT applying FROM peer_runtime WHERE id=1)=0
BEGIN
    INSERT INTO peer_events VALUES(lower(hex(randomblob(4))||'-'||hex(randomblob(2))||'-'||hex(randomblob(2))||'-'||hex(randomblob(2))||'-'||hex(randomblob(6))),
        'presence',(SELECT node_id FROM peer_runtime WHERE id=1),unixepoch(),
        json_object('id',NEW.id,'room_id',NEW.room_id,'actor_id',NEW.actor_id,'created_at',NEW.created_at));
END;

CREATE TRIGGER IF NOT EXISTS peer_attachment_insert AFTER INSERT ON attachments
WHEN (SELECT node_id FROM peer_runtime WHERE id=1) != '' AND (SELECT applying FROM peer_runtime WHERE id=1)=0
BEGIN
    INSERT INTO peer_events VALUES(lower(hex(randomblob(4))||'-'||hex(randomblob(2))||'-'||hex(randomblob(2))||'-'||hex(randomblob(2))||'-'||hex(randomblob(6))),
        'attachment',(SELECT node_id FROM peer_runtime WHERE id=1),unixepoch(),
        json_object('id',NEW.id,'room_id',NEW.room_id,'user_id',NEW.sender_id,'name',NEW.name,'mime_type',NEW.mime_type,
            'size',NEW.size,'sha256',lower(hex(NEW.sha256)),'sent_at',NEW.sent_at,'expires_at',NEW.expires_at));
END;

CREATE TRIGGER IF NOT EXISTS peer_settings_update AFTER UPDATE ON settings
WHEN (SELECT node_id FROM peer_runtime WHERE id=1) != '' AND (SELECT applying FROM peer_runtime WHERE id=1)=0
BEGIN
    INSERT INTO peer_events VALUES(lower(hex(randomblob(4))||'-'||hex(randomblob(2))||'-'||hex(randomblob(2))||'-'||hex(randomblob(6))),
        'settings',(SELECT node_id FROM peer_runtime WHERE id=1),unixepoch(),
        json_object('attachment_max_bytes',NEW.attachment_max_bytes,'retention_seconds',NEW.retention_seconds));
END;

PRAGMA user_version = 3;
COMMIT;
