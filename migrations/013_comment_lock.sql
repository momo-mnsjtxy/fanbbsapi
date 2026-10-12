-- A moderator can pause new discussion without deleting existing comments.
ALTER TABLE posts ADD COLUMN comments_locked INTEGER NOT NULL DEFAULT 0 CHECK (comments_locked IN (0, 1));
