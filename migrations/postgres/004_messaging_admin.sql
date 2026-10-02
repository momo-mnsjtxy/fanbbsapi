CREATE TABLE conversations (
    id TEXT PRIMARY KEY,
    created_by TEXT NOT NULL REFERENCES users(id),
    member_key TEXT NOT NULL UNIQUE,
    title TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','closed')),
    last_message_id TEXT,
    last_message_at TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    activity_sequence BIGINT NOT NULL DEFAULT 0 CHECK (activity_sequence >= 0)
);
CREATE INDEX conversations_activity ON conversations(activity_sequence DESC, id DESC);
CREATE UNIQUE INDEX categories_slug_nocase ON categories(lower(slug));
CREATE UNIQUE INDEX categories_name_nocase ON categories(lower(name));

CREATE TABLE conversation_members (
    conversation_id TEXT NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role TEXT NOT NULL DEFAULT 'member' CHECK (role IN ('owner','member')),
    joined_at TEXT NOT NULL,
    PRIMARY KEY (conversation_id, user_id)
);
CREATE INDEX conversation_members_user ON conversation_members(user_id, conversation_id);

CREATE TABLE messages (
    sequence BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    id TEXT NOT NULL UNIQUE,
    conversation_id TEXT NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
    sender_id TEXT NOT NULL REFERENCES users(id),
    client_message_id TEXT NOT NULL,
    body TEXT NOT NULL,
    created_at TEXT NOT NULL,
    UNIQUE (sender_id, client_message_id)
);
CREATE INDEX messages_conversation ON messages(conversation_id, sequence DESC);
ALTER TABLE conversations ADD CONSTRAINT conversations_last_message_fk FOREIGN KEY (last_message_id) REFERENCES messages(id) ON DELETE SET NULL;

ALTER TABLE notifications DROP CONSTRAINT notifications_type_check;
ALTER TABLE notifications ADD CONSTRAINT notifications_type_check CHECK (type IN ('comment','like','repost','follow','moderation','message'));
ALTER TABLE notifications ADD COLUMN sequence BIGINT GENERATED ALWAYS AS IDENTITY UNIQUE;
CREATE INDEX notifications_events ON notifications(user_id, sequence ASC);

CREATE FUNCTION update_conversation_after_message() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    UPDATE conversations
       SET last_message_id = NEW.id,
           last_message_at = NEW.created_at,
           updated_at = NEW.created_at,
           activity_sequence = NEW.sequence
     WHERE id = NEW.conversation_id;
    RETURN NEW;
END;
$$;
CREATE TRIGGER messages_update_conversation AFTER INSERT ON messages FOR EACH ROW EXECUTE FUNCTION update_conversation_after_message();
