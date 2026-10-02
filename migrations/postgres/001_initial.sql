CREATE TABLE users (
    sequence BIGINT GENERATED ALWAYS AS IDENTITY UNIQUE,
    id TEXT PRIMARY KEY,
    handle TEXT NOT NULL,
    email TEXT NOT NULL,
    password_hash TEXT NOT NULL,
    display_name TEXT NOT NULL,
    avatar_url TEXT NOT NULL DEFAULT '',
    bio TEXT NOT NULL DEFAULT '',
    role TEXT NOT NULL DEFAULT 'member' CHECK (role IN ('member','moderator','admin')),
    status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','suspended')),
    created_at TEXT NOT NULL
);
CREATE UNIQUE INDEX users_handle_nocase ON users(lower(handle));
CREATE UNIQUE INDEX users_email_nocase ON users(lower(email));

CREATE TABLE sessions (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    access_hash TEXT NOT NULL UNIQUE,
    refresh_hash TEXT NOT NULL UNIQUE,
    access_expires_at TEXT NOT NULL,
    refresh_expires_at TEXT NOT NULL,
    revoked_at TEXT,
    replaced_by TEXT REFERENCES sessions(id),
    created_at TEXT NOT NULL
);
CREATE INDEX sessions_user_id ON sessions(user_id);

CREATE TABLE categories (
    id TEXT PRIMARY KEY,
    slug TEXT NOT NULL UNIQUE,
    name TEXT NOT NULL UNIQUE
);

CREATE TABLE posts (
    id TEXT PRIMARY KEY,
    author_id TEXT NOT NULL REFERENCES users(id),
    kind TEXT NOT NULL CHECK (kind IN ('article','image','video','repost')),
    repost_of TEXT REFERENCES posts(id),
    title TEXT NOT NULL,
    summary TEXT NOT NULL DEFAULT '',
    body TEXT NOT NULL DEFAULT '',
    category_id TEXT REFERENCES categories(id),
    status TEXT NOT NULL DEFAULT 'published' CHECK (status IN ('draft','published','deleted')),
    visibility TEXT NOT NULL DEFAULT 'public' CHECK (visibility IN ('public','followers')),
    version INTEGER NOT NULL DEFAULT 1,
    like_count INTEGER NOT NULL DEFAULT 0 CHECK (like_count >= 0),
    comment_count INTEGER NOT NULL DEFAULT 0 CHECK (comment_count >= 0),
    repost_count INTEGER NOT NULL DEFAULT 0 CHECK (repost_count >= 0),
    created_at TEXT NOT NULL,
    published_at TEXT NOT NULL
);
CREATE INDEX posts_feed ON posts(status, published_at DESC, id DESC);
CREATE INDEX posts_author ON posts(author_id, published_at DESC);

CREATE TABLE comments (
    id TEXT PRIMARY KEY,
    post_id TEXT NOT NULL REFERENCES posts(id) ON DELETE CASCADE,
    author_id TEXT NOT NULL REFERENCES users(id),
    parent_id TEXT REFERENCES comments(id),
    root_id TEXT REFERENCES comments(id),
    depth INTEGER NOT NULL DEFAULT 0 CHECK (depth BETWEEN 0 AND 2),
    body TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'published' CHECK (status IN ('published','deleted')),
    like_count INTEGER NOT NULL DEFAULT 0 CHECK (like_count >= 0),
    created_at TEXT NOT NULL
);
CREATE INDEX comments_post ON comments(post_id, created_at ASC, id ASC);

CREATE TABLE reactions (
    post_id TEXT NOT NULL REFERENCES posts(id) ON DELETE CASCADE,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at TEXT NOT NULL,
    PRIMARY KEY (post_id, user_id)
);

CREATE TABLE reposts (
    post_id TEXT NOT NULL REFERENCES posts(id) ON DELETE CASCADE,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    repost_post_id TEXT NOT NULL UNIQUE REFERENCES posts(id) ON DELETE CASCADE,
    created_at TEXT NOT NULL,
    PRIMARY KEY (post_id, user_id)
);

CREATE TABLE follows (
    follower_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    followed_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at TEXT NOT NULL,
    CHECK (follower_id <> followed_id),
    PRIMARY KEY (follower_id, followed_id)
);

CREATE TABLE idempotency_keys (
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    scope TEXT NOT NULL,
    key TEXT NOT NULL,
    resource_id TEXT NOT NULL,
    created_at TEXT NOT NULL,
    PRIMARY KEY (user_id, scope, key)
);
