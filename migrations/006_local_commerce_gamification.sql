-- Local, non-payment commerce. Orders reserve bounded integer inventory but
-- never contain prices, payment state, wallet state, or provider identifiers.
CREATE TABLE product_types (
    id TEXT PRIMARY KEY,
    slug TEXT NOT NULL UNIQUE COLLATE NOCASE,
    name TEXT NOT NULL UNIQUE COLLATE NOCASE,
    status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','archived')),
    created_by TEXT NOT NULL REFERENCES users(id),
    updated_by TEXT NOT NULL REFERENCES users(id),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE products (
    id TEXT PRIMARY KEY,
    type_id TEXT NOT NULL REFERENCES product_types(id),
    sku TEXT NOT NULL UNIQUE COLLATE NOCASE,
    name TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    inventory INTEGER NOT NULL DEFAULT 0 CHECK (inventory BETWEEN 0 AND 1000000),
    status TEXT NOT NULL DEFAULT 'draft' CHECK (status IN ('draft','active','archived')),
    version INTEGER NOT NULL DEFAULT 1 CHECK (version >= 1),
    created_by TEXT NOT NULL REFERENCES users(id),
    updated_by TEXT NOT NULL REFERENCES users(id),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
CREATE INDEX products_catalog ON products(status, type_id, created_at DESC, id DESC);

CREATE TABLE cart_items (
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    product_id TEXT NOT NULL REFERENCES products(id) ON DELETE CASCADE,
    quantity INTEGER NOT NULL CHECK (quantity BETWEEN 1 AND 100),
    updated_at TEXT NOT NULL,
    PRIMARY KEY (user_id, product_id)
);

CREATE TABLE commerce_orders (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES users(id),
    status TEXT NOT NULL DEFAULT 'created' CHECK (status IN ('created','cancelled','fulfilled')),
    fulfillment_carrier TEXT NOT NULL DEFAULT '',
    tracking_code TEXT NOT NULL DEFAULT '',
    cancelled_at TEXT,
    fulfilled_at TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
CREATE INDEX commerce_orders_user ON commerce_orders(user_id, created_at DESC, id DESC);
CREATE INDEX commerce_orders_admin ON commerce_orders(status, created_at DESC, id DESC);

CREATE TABLE commerce_order_items (
    order_id TEXT NOT NULL REFERENCES commerce_orders(id) ON DELETE CASCADE,
    product_id TEXT NOT NULL REFERENCES products(id),
    sku_snapshot TEXT NOT NULL,
    name_snapshot TEXT NOT NULL,
    quantity INTEGER NOT NULL CHECK (quantity BETWEEN 1 AND 100),
    PRIMARY KEY (order_id, product_id)
);

-- Local community points are award-only, non-transferable, non-purchasable,
-- non-redeemable, and intentionally have no relationship to cash or orders.
CREATE TABLE user_points (
    user_id TEXT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    balance INTEGER NOT NULL DEFAULT 0 CHECK (balance BETWEEN 0 AND 1000000000),
    lifetime_points INTEGER NOT NULL DEFAULT 0 CHECK (lifetime_points BETWEEN 0 AND 1000000000),
    updated_at TEXT NOT NULL
);

CREATE TABLE local_point_events (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    amount INTEGER NOT NULL CHECK (amount BETWEEN 1 AND 100000),
    event_type TEXT NOT NULL CHECK (event_type IN ('daily_checkin','task_reward','admin_grant')),
    event_key TEXT NOT NULL,
    description TEXT NOT NULL,
    created_at TEXT NOT NULL,
    UNIQUE (user_id, event_type, event_key)
);
CREATE INDEX local_point_events_user ON local_point_events(user_id, created_at DESC, id DESC);

CREATE TRIGGER local_point_events_no_update
BEFORE UPDATE ON local_point_events
BEGIN
    SELECT RAISE(ABORT, 'local point events are immutable');
END;

CREATE TRIGGER local_point_events_no_delete
BEFORE DELETE ON local_point_events
BEGIN
    SELECT RAISE(ABORT, 'local point events are immutable');
END;

CREATE TABLE daily_checkins (
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    checkin_date TEXT NOT NULL,
    point_event_id TEXT NOT NULL UNIQUE REFERENCES local_point_events(id),
    created_at TEXT NOT NULL,
    PRIMARY KEY (user_id, checkin_date)
);

CREATE TABLE avatar_frames (
    id TEXT PRIMARY KEY,
    slug TEXT NOT NULL UNIQUE COLLATE NOCASE,
    name TEXT NOT NULL UNIQUE COLLATE NOCASE,
    image_url TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','archived')),
    created_by TEXT NOT NULL REFERENCES users(id),
    updated_by TEXT NOT NULL REFERENCES users(id),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE gamification_tasks (
    id TEXT PRIMARY KEY,
    code TEXT NOT NULL UNIQUE COLLATE NOCASE,
    name TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    repeat_policy TEXT NOT NULL CHECK (repeat_policy IN ('once','daily')),
    reward_points INTEGER NOT NULL DEFAULT 0 CHECK (reward_points BETWEEN 0 AND 100000),
    reward_frame_id TEXT REFERENCES avatar_frames(id),
    status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','archived')),
    created_by TEXT NOT NULL REFERENCES users(id),
    updated_by TEXT NOT NULL REFERENCES users(id),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    CHECK (reward_points > 0 OR reward_frame_id IS NOT NULL)
);

CREATE TABLE task_claims (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    task_id TEXT NOT NULL REFERENCES gamification_tasks(id),
    claim_key TEXT NOT NULL,
    point_event_id TEXT REFERENCES local_point_events(id),
    created_at TEXT NOT NULL,
    UNIQUE (user_id, task_id, claim_key)
);
CREATE INDEX task_claims_user ON task_claims(user_id, created_at DESC, id DESC);

CREATE TABLE user_avatar_frames (
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    frame_id TEXT NOT NULL REFERENCES avatar_frames(id),
    source_type TEXT NOT NULL CHECK (source_type IN ('task_reward','admin_grant')),
    source_id TEXT NOT NULL,
    granted_at TEXT NOT NULL,
    PRIMARY KEY (user_id, frame_id)
);

CREATE TABLE user_avatar_frame_selection (
    user_id TEXT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    frame_id TEXT NOT NULL REFERENCES avatar_frames(id),
    selected_at TEXT NOT NULL
);

CREATE TABLE gamification_levels (
    level INTEGER PRIMARY KEY CHECK (level BETWEEN 1 AND 1000),
    name TEXT NOT NULL UNIQUE,
    title TEXT NOT NULL UNIQUE,
    min_lifetime_points INTEGER NOT NULL UNIQUE CHECK (min_lifetime_points BETWEEN 0 AND 1000000000)
);

INSERT INTO gamification_levels(level, name, title, min_lifetime_points) VALUES
    (1, '新成员', '初来乍到', 0),
    (2, '活跃成员', '社区常客', 100),
    (3, '资深成员', '社区达人', 500),
    (4, '社区之星', '社区之星', 2000);
