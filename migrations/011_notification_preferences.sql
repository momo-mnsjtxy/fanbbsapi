-- Owner-scoped preferences for future optional inbox events. Mandatory
-- moderation notices deliberately have no preference row.
CREATE TABLE notification_preferences (
 user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 type TEXT NOT NULL CHECK (type IN ('comment','like','repost','follow','message')),
 enabled INTEGER NOT NULL CHECK (enabled IN (0,1)),
 updated_at TEXT NOT NULL,
 PRIMARY KEY (user_id, type)
);
CREATE INDEX notifications_kind_sequence ON notifications(user_id, type, sequence DESC);

-- Muting hides inbox entries and badges, never the reconnect event log.
ALTER TABLE notifications ADD COLUMN muted INTEGER NOT NULL DEFAULT 0 CHECK (muted IN (0,1));
