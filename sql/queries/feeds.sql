-- name: CreateFeed :one
INSERT INTO feeds (id, created_at, updated_at, name, url, user_id)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: GetFeeds :many
SELECT * FROM feeds;

-- name: DeleteFeed :exec
DELETE FROM feeds WHERE id = $1 AND user_id = $2;

-- name: GetFeedsWithUser :many
SELECT 
    feeds.*,
    users.name as user_name
FROM feeds
JOIN users ON feeds.user_id = users.id
WHERE feeds.user_id = $1;

-- name: GetFeedByURL :one
SELECT * FROM feeds WHERE url = $1;

-- name: ClaimFeedsToFetch :many
-- Atomically claims up to N due feeds. FOR UPDATE SKIP LOCKED in a
-- materialized CTE lets several aggregator processes run at once without
-- fetching the same feed twice. All times come from the database clock.
WITH due AS (
    SELECT id FROM feeds
    WHERE next_fetch_at IS NULL OR next_fetch_at <= NOW()
    ORDER BY last_fetched_at ASC NULLS FIRST
    LIMIT sqlc.arg('batch_size')
    FOR UPDATE SKIP LOCKED
)
UPDATE feeds
SET last_fetched_at = NOW(), updated_at = NOW()
FROM due
WHERE feeds.id = due.id
RETURNING feeds.*;

-- name: RecordFeedSuccess :exec
UPDATE feeds
SET etag = $2, last_modified = $3, failure_count = 0, last_error = NULL, next_fetch_at = NULL, updated_at = NOW()
WHERE id = $1;

-- name: RecordFeedFailure :one
UPDATE feeds
SET failure_count = failure_count + 1,
    last_error = sqlc.arg('last_error'),
    next_fetch_at = NOW() + make_interval(secs => sqlc.arg('backoff_seconds')::float8),
    updated_at = NOW()
WHERE id = sqlc.arg('id')
RETURNING failure_count;
