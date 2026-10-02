-- +goose Up
ALTER TABLE feeds ADD COLUMN etag TEXT;
ALTER TABLE feeds ADD COLUMN last_modified TEXT;
ALTER TABLE feeds ADD COLUMN failure_count INT NOT NULL DEFAULT 0;
ALTER TABLE feeds ADD COLUMN last_error TEXT;
ALTER TABLE feeds ADD COLUMN next_fetch_at TIMESTAMP;
CREATE INDEX idx_feeds_fetch_order ON feeds (last_fetched_at NULLS FIRST);
CREATE INDEX idx_posts_feed_published ON posts (feed_id, published_at DESC);

-- +goose Down
DROP INDEX IF EXISTS idx_posts_feed_published;
DROP INDEX IF EXISTS idx_feeds_fetch_order;
ALTER TABLE feeds DROP COLUMN next_fetch_at;
ALTER TABLE feeds DROP COLUMN last_error;
ALTER TABLE feeds DROP COLUMN failure_count;
ALTER TABLE feeds DROP COLUMN last_modified;
ALTER TABLE feeds DROP COLUMN etag;
