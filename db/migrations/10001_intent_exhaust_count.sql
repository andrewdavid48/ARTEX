-- An intent counts how many times it settled as exhausted (turn cap, timeout,
-- or every model refused). The third time it settles as blocked with reason
-- 'exhausted_limit' instead. schema.sql carries the same column for a fresh
-- install.
ALTER TABLE exploration_nodes ADD COLUMN IF NOT EXISTS exhaust_count INT NOT NULL DEFAULT 0;
