CREATE TABLE media_assets (
    id TEXT PRIMARY KEY,
    owner_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    purpose TEXT NOT NULL CHECK (purpose IN ('post','avatar')),
    object_key TEXT NOT NULL UNIQUE,
    mime_type TEXT NOT NULL,
    size_bytes BIGINT NOT NULL CHECK (size_bytes > 0),
    checksum TEXT NOT NULL,
    original_name TEXT NOT NULL,
    alt_text TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'ready' CHECK (status IN ('ready','deleted')),
    created_at TEXT NOT NULL
);
CREATE INDEX media_assets_owner ON media_assets(owner_id, created_at DESC);
ALTER TABLE users ADD COLUMN avatar_media_id TEXT REFERENCES media_assets(id);

CREATE TABLE post_media (
    post_id TEXT NOT NULL REFERENCES posts(id) ON DELETE CASCADE,
    asset_id TEXT NOT NULL UNIQUE REFERENCES media_assets(id),
    position INTEGER NOT NULL CHECK (position >= 0),
    PRIMARY KEY (post_id, asset_id)
);
CREATE INDEX post_media_position ON post_media(post_id, position);
ALTER TABLE comments ADD COLUMN version INTEGER NOT NULL DEFAULT 1;

CREATE TABLE comment_reactions (
    comment_id TEXT NOT NULL REFERENCES comments(id) ON DELETE CASCADE,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at TEXT NOT NULL,
    PRIMARY KEY (comment_id, user_id)
);

CREATE TABLE legacy_import_runs (
    id TEXT PRIMARY KEY,
    source_hash TEXT NOT NULL UNIQUE,
    report_json TEXT NOT NULL,
    created_at TEXT NOT NULL
);
CREATE TABLE legacy_quarantine (
    run_id TEXT NOT NULL REFERENCES legacy_import_runs(id) ON DELETE CASCADE,
    entity_type TEXT NOT NULL,
    legacy_id TEXT NOT NULL,
    reason TEXT NOT NULL,
    source_json TEXT NOT NULL,
    created_at TEXT NOT NULL,
    PRIMARY KEY (run_id, entity_type, legacy_id)
);
