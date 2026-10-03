CREATE TABLE collections (
    id TEXT PRIMARY KEY,
    owner_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    visibility TEXT NOT NULL DEFAULT 'private' CHECK (visibility IN ('private','public')),
    version INTEGER NOT NULL DEFAULT 1 CHECK (version >= 1),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
CREATE INDEX collections_owner ON collections(owner_id, updated_at DESC, id DESC);
CREATE INDEX collections_public ON collections(owner_id, visibility, updated_at DESC, id DESC);
CREATE TABLE collection_items (
    collection_id TEXT NOT NULL REFERENCES collections(id) ON DELETE CASCADE,
    post_id TEXT NOT NULL REFERENCES posts(id) ON DELETE CASCADE,
    added_at TEXT NOT NULL,
    PRIMARY KEY (collection_id, post_id)
);
CREATE INDEX collection_items_added ON collection_items(collection_id, added_at DESC, post_id DESC);

CREATE TABLE shipping_addresses (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    label TEXT NOT NULL DEFAULT '',
    recipient_name TEXT NOT NULL,
    phone TEXT NOT NULL,
    region TEXT NOT NULL,
    address_line TEXT NOT NULL,
    postal_code TEXT NOT NULL DEFAULT '',
    is_default INTEGER NOT NULL DEFAULT 0 CHECK (is_default IN (0,1)),
    version INTEGER NOT NULL DEFAULT 1 CHECK (version >= 1),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
CREATE INDEX shipping_addresses_user ON shipping_addresses(user_id, is_default DESC, updated_at DESC, id DESC);
CREATE UNIQUE INDEX shipping_addresses_one_default ON shipping_addresses(user_id) WHERE is_default = 1;
CREATE TABLE order_shipping_addresses (
    order_id TEXT PRIMARY KEY REFERENCES commerce_orders(id) ON DELETE CASCADE,
    source_address_id TEXT,
    label TEXT NOT NULL DEFAULT '',
    recipient_name TEXT NOT NULL,
    phone TEXT NOT NULL,
    region TEXT NOT NULL,
    address_line TEXT NOT NULL,
    postal_code TEXT NOT NULL DEFAULT ''
);
CREATE TABLE order_tracking_events (
    id TEXT PRIMARY KEY,
    order_id TEXT NOT NULL REFERENCES commerce_orders(id) ON DELETE CASCADE,
    status TEXT NOT NULL CHECK (status IN ('label_created','in_transit','out_for_delivery','delivered','exception')),
    description TEXT NOT NULL DEFAULT '',
    location TEXT NOT NULL DEFAULT '',
    occurred_at TEXT NOT NULL,
    created_by TEXT NOT NULL REFERENCES users(id),
    created_at TEXT NOT NULL
);
CREATE INDEX order_tracking_events_order ON order_tracking_events(order_id, occurred_at ASC, id ASC);
CREATE FUNCTION reject_order_tracking_event_change() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN RAISE EXCEPTION 'order tracking events are immutable'; END;
$$;
CREATE TRIGGER order_tracking_events_no_update BEFORE UPDATE ON order_tracking_events FOR EACH ROW EXECUTE FUNCTION reject_order_tracking_event_change();
CREATE TRIGGER order_tracking_events_no_delete BEFORE DELETE ON order_tracking_events FOR EACH ROW EXECUTE FUNCTION reject_order_tracking_event_change();
