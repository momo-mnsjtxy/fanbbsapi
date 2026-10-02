-- Durable private messaging. HTTP remains the source of truth; notification
-- sequence numbers provide a reconnect checkpoint without an external broker.
CREATE TABLE conversations (
    id TEXT PRIMARY KEY,
    created_by TEXT NOT NULL REFERENCES users(id),
    member_key TEXT NOT NULL UNIQUE,
    title TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','closed')),
    last_message_id TEXT REFERENCES messages(id) ON DELETE SET NULL,
    last_message_at TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    activity_sequence INTEGER NOT NULL DEFAULT 0 CHECK (activity_sequence >= 0)
);
CREATE INDEX conversations_activity ON conversations(activity_sequence DESC, id DESC);

CREATE UNIQUE INDEX categories_slug_nocase ON categories(slug COLLATE NOCASE);
CREATE UNIQUE INDEX categories_name_nocase ON categories(name COLLATE NOCASE);

CREATE TABLE conversation_members (
    conversation_id TEXT NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role TEXT NOT NULL DEFAULT 'member' CHECK (role IN ('owner','member')),
    joined_at TEXT NOT NULL,
    PRIMARY KEY (conversation_id, user_id)
);
CREATE INDEX conversation_members_user ON conversation_members(user_id, conversation_id);

CREATE TABLE messages (
    sequence INTEGER PRIMARY KEY AUTOINCREMENT,
    id TEXT NOT NULL UNIQUE,
    conversation_id TEXT NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
    sender_id TEXT NOT NULL REFERENCES users(id),
    client_message_id TEXT NOT NULL,
    body TEXT NOT NULL,
    created_at TEXT NOT NULL,
    UNIQUE (sender_id, client_message_id)
);
CREATE INDEX messages_conversation ON messages(conversation_id, sequence DESC);

-- SQLite cannot widen an existing CHECK constraint in place. Rebuild the
-- notification table so chat notifications share the durable event sequence.
ALTER TABLE notifications RENAME TO notifications_v2_backup;
CREATE TABLE notifications (
    sequence INTEGER PRIMARY KEY AUTOINCREMENT,
    id TEXT NOT NULL UNIQUE,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    type TEXT NOT NULL CHECK (type IN ('comment','like','repost','follow','moderation','message')),
    actor_id TEXT REFERENCES users(id) ON DELETE SET NULL,
    subject_type TEXT NOT NULL,
    subject_id TEXT NOT NULL,
    payload TEXT NOT NULL DEFAULT '{}',
    read_at TEXT,
    created_at TEXT NOT NULL
);
INSERT INTO notifications(id, user_id, type, actor_id, subject_type, subject_id, payload, read_at, created_at)
SELECT id, user_id, type, actor_id, subject_type, subject_id, payload, read_at, created_at
FROM notifications_v2_backup ORDER BY created_at, id;
DROP TABLE notifications_v2_backup;
CREATE INDEX notifications_user ON notifications(user_id, created_at DESC, id DESC);
CREATE INDEX notifications_unread ON notifications(user_id, read_at, created_at DESC);
CREATE INDEX notifications_events ON notifications(user_id, sequence ASC);

CREATE TRIGGER messages_update_conversation
AFTER INSERT ON messages
BEGIN
    UPDATE conversations
    SET last_message_id = NEW.id,
        last_message_at = NEW.created_at,
        updated_at = NEW.created_at,
        activity_sequence = NEW.sequence
    WHERE id = NEW.conversation_id;
END;
