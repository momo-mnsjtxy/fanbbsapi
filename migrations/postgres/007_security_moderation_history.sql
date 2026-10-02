CREATE TABLE recovery_codes (
    id TEXT PRIMARY KEY, user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    code_hash TEXT NOT NULL UNIQUE, created_at TEXT NOT NULL, used_at TEXT, revoked_at TEXT
);
CREATE INDEX recovery_codes_user_active ON recovery_codes(user_id, used_at, revoked_at);
CREATE TABLE post_revisions (
    post_id TEXT NOT NULL REFERENCES posts(id) ON DELETE CASCADE,
    version INTEGER NOT NULL CHECK (version >= 1), title TEXT NOT NULL, summary TEXT NOT NULL,
    body TEXT NOT NULL, category_id TEXT, tag_ids TEXT NOT NULL DEFAULT '[]', visibility TEXT NOT NULL,
    edited_by TEXT NOT NULL REFERENCES users(id), created_at TEXT NOT NULL,
    PRIMARY KEY (post_id, version)
);
CREATE FUNCTION reject_post_revision_change() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN RAISE EXCEPTION 'post revisions are immutable'; END;
$$;
CREATE TRIGGER post_revisions_no_update BEFORE UPDATE ON post_revisions FOR EACH ROW EXECUTE FUNCTION reject_post_revision_change();
CREATE TRIGGER post_revisions_no_delete BEFORE DELETE ON post_revisions FOR EACH ROW EXECUTE FUNCTION reject_post_revision_change();
CREATE TABLE post_moderation (
    post_id TEXT PRIMARY KEY REFERENCES posts(id) ON DELETE CASCADE,
    state TEXT NOT NULL CHECK (state IN ('pending','published','rejected')),
    is_pinned INTEGER NOT NULL DEFAULT 0 CHECK (is_pinned IN (0,1)),
    is_recommended INTEGER NOT NULL DEFAULT 0 CHECK (is_recommended IN (0,1)),
    reason TEXT NOT NULL DEFAULT '', decided_by TEXT REFERENCES users(id), decided_at TEXT,
    updated_at TEXT NOT NULL
);
CREATE INDEX post_moderation_queue ON post_moderation(state, updated_at ASC, post_id ASC);
CREATE INDEX post_moderation_feed ON post_moderation(state, is_pinned DESC, is_recommended DESC, updated_at DESC);
