-- Keep filesystem deletion retryable after media metadata is removed. The
-- queue is committed atomically with the metadata delete, so foreign keys can
-- protect every attached asset while blob cleanup remains eventually safe.
CREATE TABLE media_deletion_queue (
    object_key TEXT PRIMARY KEY,
    queued_at TEXT NOT NULL,
    attempts INTEGER NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    last_error TEXT NOT NULL DEFAULT ''
);
CREATE INDEX media_deletion_queue_queued ON media_deletion_queue(queued_at, object_key);
