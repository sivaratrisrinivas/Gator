package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os/signal"
	"syscall"
	"time"

	"github.com/sivaratrisrinivas/Gator/internal/database"
	"github.com/sivaratrisrinivas/Gator/internal/rss"
)

// handlerAgg runs the aggregator: agg <interval> [-workers N] [-batch N] [-once]
func handlerAgg(s *state, cmd command, _ database.User) error {
	fs := flag.NewFlagSet("agg", flag.ContinueOnError)
	workers := fs.Int("workers", 8, "concurrent feed fetches")
	batch := fs.Int("batch", 32, "feeds claimed per tick")
	once := fs.Bool("once", false, "run a single batch and exit")
	if len(cmd.Args) < 1 {
		return fmt.Errorf("usage: agg <time_between_reqs> [-workers N] [-batch N] [-once]")
	}
	interval, err := time.ParseDuration(cmd.Args[0])
	if err != nil {
		return fmt.Errorf("couldn't parse duration: %w", err)
	}
	if err := fs.Parse(cmd.Args[1:]); err != nil {
		return err
	}
	if *workers < 1 || *batch < 1 {
		return fmt.Errorf("workers and batch must be positive")
	}

	sc := &Scraper{
		DB: s.db, Fetcher: rss.NewFetcher(),
		Workers: *workers, BatchSize: *batch,
		MaxBackoff: 6 * time.Hour, Now: time.Now,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	fmt.Printf("Collecting feeds every %v with %d workers (batch %d)\n", interval, *workers, *batch)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		start := time.Now()
		outcomes, err := sc.RunOnce(ctx)
		if err != nil {
			slog.Error("scrape batch", "err", err)
		}
		report(outcomes, time.Since(start))
		if *once {
			return nil
		}
		select {
		case <-ctx.Done():
			fmt.Println("stopping aggregator")
			return nil
		case <-ticker.C:
		}
	}
}

func report(outcomes []FeedOutcome, took time.Duration) {
	var newPosts, notModified, failed int
	for _, o := range outcomes {
		switch {
		case o.Err != nil:
			failed++
			fmt.Printf("  x %s: %v\n", o.Feed.Name, o.Err)
		case o.NotModified:
			notModified++
			fmt.Printf("  = %s: not modified\n", o.Feed.Name)
		default:
			newPosts += o.NewPosts
			fmt.Printf("  + %s: %d new posts\n", o.Feed.Name, o.NewPosts)
		}
	}
	fmt.Printf("batch: %d feeds, %d new posts, %d not modified, %d failed in %v\n",
		len(outcomes), newPosts, notModified, failed, took.Round(time.Millisecond))
}
