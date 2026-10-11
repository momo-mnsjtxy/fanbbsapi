-- Device labels are explicitly owner-provided; no IP, location or user-agent
-- fingerprint is collected. Activity is coarse UTC epoch seconds.
ALTER TABLE sessions ADD COLUMN device_name TEXT NOT NULL DEFAULT '';
ALTER TABLE sessions ADD COLUMN last_active_unix BIGINT NOT NULL DEFAULT 0;
UPDATE sessions SET last_active_unix = CAST(EXTRACT(EPOCH FROM created_at::timestamptz) AS BIGINT);
