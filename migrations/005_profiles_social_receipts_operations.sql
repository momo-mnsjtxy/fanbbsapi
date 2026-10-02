CREATE TABLE blocks (
    blocker_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    blocked_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at TEXT NOT NULL,
    CHECK (blocker_id <> blocked_id),
    PRIMARY KEY (blocker_id, blocked_id)
);
CREATE INDEX blocks_blocked ON blocks(blocked_id, blocker_id);

CREATE TABLE category_follows (
    category_id TEXT NOT NULL REFERENCES categories(id) ON DELETE CASCADE,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at TEXT NOT NULL,
    PRIMARY KEY (category_id, user_id)
);
CREATE INDEX category_follows_user ON category_follows(user_id, created_at DESC, category_id);

CREATE TABLE user_covers (
    user_id TEXT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    asset_id TEXT NOT NULL UNIQUE REFERENCES media_assets(id),
    updated_at TEXT NOT NULL
);

ALTER TABLE conversation_members ADD COLUMN read_sequence INTEGER NOT NULL DEFAULT 0 CHECK (read_sequence >= 0);
ALTER TABLE conversation_members ADD COLUMN read_at TEXT;
ALTER TABLE conversation_members ADD COLUMN left_at TEXT;
CREATE INDEX conversation_members_active ON conversation_members(user_id, left_at, conversation_id);

CREATE TABLE operations_configs (
    id TEXT PRIMARY KEY,
    version INTEGER NOT NULL DEFAULT 1 CHECK (version >= 1),
    status TEXT NOT NULL DEFAULT 'published' CHECK (status IN ('draft','published')),
    payload TEXT NOT NULL,
    updated_by TEXT REFERENCES users(id),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE operations_config_media (
    config_id TEXT NOT NULL REFERENCES operations_configs(id) ON DELETE CASCADE,
    asset_id TEXT NOT NULL REFERENCES media_assets(id),
    PRIMARY KEY (config_id, asset_id)
);

INSERT INTO operations_configs(id, version, status, payload, created_at, updated_at)
VALUES ('homepage', 1, 'published', '{"carousel":[],"announcements":[]}', strftime('%Y-%m-%dT%H:%M:%fZ','now'), strftime('%Y-%m-%dT%H:%M:%fZ','now'));
