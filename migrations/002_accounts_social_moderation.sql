ALTER TABLE users ADD COLUMN updated_at TEXT;
ALTER TABLE users ADD COLUMN password_changed_at TEXT;
CREATE UNIQUE INDEX users_active_display_name ON users(lower(display_name)) WHERE status = 'active';

CREATE TABLE tags (
    id TEXT PRIMARY KEY,
    slug TEXT NOT NULL UNIQUE COLLATE NOCASE,
    name TEXT NOT NULL UNIQUE COLLATE NOCASE,
    created_at TEXT NOT NULL
);

CREATE TABLE post_tags (
    post_id TEXT NOT NULL REFERENCES posts(id) ON DELETE CASCADE,
    tag_id TEXT NOT NULL REFERENCES tags(id) ON DELETE CASCADE,
    PRIMARY KEY (post_id, tag_id)
);
CREATE INDEX post_tags_tag ON post_tags(tag_id, post_id);

CREATE TABLE bookmarks (
    post_id TEXT NOT NULL REFERENCES posts(id) ON DELETE CASCADE,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at TEXT NOT NULL,
    PRIMARY KEY (post_id, user_id)
);
CREATE INDEX bookmarks_user ON bookmarks(user_id, created_at DESC, post_id DESC);

CREATE TABLE notifications (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    type TEXT NOT NULL CHECK (type IN ('comment','like','repost','follow','moderation')),
    actor_id TEXT REFERENCES users(id) ON DELETE SET NULL,
    subject_type TEXT NOT NULL,
    subject_id TEXT NOT NULL,
    payload TEXT NOT NULL DEFAULT '{}',
    read_at TEXT,
    created_at TEXT NOT NULL
);
CREATE INDEX notifications_user ON notifications(user_id, created_at DESC, id DESC);
CREATE INDEX notifications_unread ON notifications(user_id, read_at, created_at DESC);

CREATE TABLE reports (
    id TEXT PRIMARY KEY,
    reporter_id TEXT NOT NULL REFERENCES users(id),
    target_type TEXT NOT NULL CHECK (target_type IN ('post','comment','user')),
    target_id TEXT NOT NULL,
    reason TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'open' CHECK (status IN ('open','dismissed','actioned')),
    decision TEXT,
    decision_reason TEXT,
    decided_by TEXT REFERENCES users(id),
    decided_at TEXT,
    created_at TEXT NOT NULL
);
CREATE INDEX reports_queue ON reports(status, created_at ASC, id ASC);
CREATE UNIQUE INDEX reports_one_open_per_target ON reports(reporter_id, target_type, target_id) WHERE status = 'open';

CREATE TABLE audit_events (
    id TEXT PRIMARY KEY,
    actor_id TEXT NOT NULL REFERENCES users(id),
    action TEXT NOT NULL,
    target_type TEXT NOT NULL,
    target_id TEXT NOT NULL,
    before_value TEXT NOT NULL DEFAULT '{}',
    after_value TEXT NOT NULL DEFAULT '{}',
    reason TEXT NOT NULL,
    request_id TEXT NOT NULL,
    created_at TEXT NOT NULL
);
CREATE INDEX audit_events_created ON audit_events(created_at DESC, id DESC);

CREATE TRIGGER audit_events_no_update
BEFORE UPDATE ON audit_events
BEGIN
    SELECT RAISE(ABORT, 'audit events are immutable');
END;

CREATE TRIGGER audit_events_no_delete
BEFORE DELETE ON audit_events
BEGIN
    SELECT RAISE(ABORT, 'audit events are immutable');
END;
