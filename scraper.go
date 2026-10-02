package main

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"math"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/sivaratrisrinivas/Gator/internal/database"
	"github.com/sivaratrisrinivas/Gator/internal/rss"
)

// Scraper claims due feeds in batches and fetches them with a bounded pool of
// workers. Failing feeds back off exponentially so a dead site is not hammered.
type Scraper struct {
	DB         *database.Queries
	Fetcher    *rss.Fetcher
	Workers    int
	BatchSize  int
	MaxBackoff time.Duration
	Now        func() time.Time
}

// FeedOutcome summarizes one feed fetch.
type FeedOutcome struct {
	Feed        database.Feed
	NewPosts    int
	NotModified bool
	Err         error
}

// RunOnce claims one batch of due feeds and processes it concurrently.
func (s *Scraper) RunOnce(ctx context.Context) ([]FeedOutcome, error) {
	feeds, err := s.DB.ClaimFeedsToFetch(ctx, int32(s.BatchSize))
	if err != nil {
		return nil, err
	}
	if len(feeds) == 0 {
		return nil, nil
	}

	jobs := make(chan database.Feed)
	results := make([]FeedOutcome, len(feeds))
	index := make(map[uuid.UUID]int, len(feeds))
	for i, f := range feeds {
		index[f.ID] = i
	}

	var wg sync.WaitGroup
	workers := min(s.Workers, len(feeds))
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for f := range jobs {
				results[index[f.ID]] = s.processFeed(ctx, f)
			}
		}()
	}
	for _, f := range feeds {
		jobs <- f
	}
	close(jobs)
	wg.Wait()
	return results, nil
}

func (s *Scraper) processFeed(ctx context.Context, feed database.Feed) FeedOutcome {
	out := FeedOutcome{Feed: feed}
	res, err := s.Fetcher.Fetch(ctx, feed.Url, feed.Etag.String, feed.LastModified.String)
	if errors.Is(err, rss.ErrNotModified) {
		out.NotModified = true
		out.Err = s.DB.RecordFeedSuccess(ctx, database.RecordFeedSuccessParams{ID: feed.ID, Etag: feed.Etag, LastModified: feed.LastModified})
		return out
	}
	if err != nil {
		out.Err = err
		s.recordFailure(ctx, feed, err)
		return out
	}

	now := s.Now().UTC()
	for _, item := range res.Items {
		published := sql.NullTime{Time: item.PublishedAt, Valid: !item.PublishedAt.IsZero()}
		n, err := s.DB.CreatePost(ctx, database.CreatePostParams{
			ID:          uuid.New(),
			CreatedAt:   now,
			UpdatedAt:   now,
			Title:       item.Title,
			Url:         item.Link,
			Description: sql.NullString{String: item.Description, Valid: item.Description != ""},
			PublishedAt: published,
			FeedID:      feed.ID,
		})
		if err != nil {
			slog.Warn("save post", "feed", feed.Name, "url", item.Link, "err", err)
			continue
		}
		out.NewPosts += int(n)
	}
	out.Err = s.DB.RecordFeedSuccess(ctx, database.RecordFeedSuccessParams{
		ID:           feed.ID,
		Etag:         sql.NullString{String: res.ETag, Valid: res.ETag != ""},
		LastModified: sql.NullString{String: res.LastModified, Valid: res.LastModified != ""},
	})
	return out
}

func (s *Scraper) recordFailure(ctx context.Context, feed database.Feed, cause error) {
	wait := Backoff(int(feed.FailureCount)+1, s.MaxBackoff)
	_, err := s.DB.RecordFeedFailure(ctx, database.RecordFeedFailureParams{
		ID:             feed.ID,
		LastError:      sql.NullString{String: cause.Error(), Valid: true},
		BackoffSeconds: wait.Seconds(),
	})
	if err != nil {
		slog.Error("record feed failure", "feed", feed.Name, "err", err)
	}
}

// Backoff returns 1m, 2m, 4m, ... capped at max, for the nth consecutive failure.
func Backoff(failures int, max time.Duration) time.Duration {
	if failures < 1 {
		return 0
	}
	d := time.Duration(math.Pow(2, float64(failures-1))) * time.Minute
	if d <= 0 || d > max {
		return max
	}
	return d
}
