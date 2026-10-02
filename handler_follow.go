package main

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/sivaratrisrinivas/Gator/internal/database"
)

func handlerFollow(s *state, cmd command, user database.User) error {
	if len(cmd.Args) != 1 {
		return fmt.Errorf("usage: follow <feed_url>")
	}

	url := cmd.Args[0]

	feed, err := s.db.GetFeedByURL(context.Background(), url)
	if err != nil {
		if err == sql.ErrNoRows {
			feed, err = s.db.CreateFeed(context.Background(), database.CreateFeedParams{
				ID:        uuid.New(),
				CreatedAt: time.Now().UTC(),
				UpdatedAt: time.Now().UTC(),
				Name:      url,
				Url:       url,
				UserID:    user.ID,
			})
			if err != nil {
				return fmt.Errorf("could not create feed: %w", err)
			}
		} else {
			return fmt.Errorf("could not check feed: %w", err)
		}
	}

	feedFollow, err := s.db.CreateFeedFollow(context.Background(), database.CreateFeedFollowParams{
		ID:        uuid.New(),
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
		UserID:    user.ID,
		FeedID:    feed.ID,
	})
	if err != nil {
		return fmt.Errorf("could not follow feed: %w", err)
	}

	fmt.Printf("Following '%s' successfully!\n", feedFollow.FeedName)
	return nil
}

func handlerFollowing(s *state, cmd command, user database.User) error {
	if len(cmd.Args) != 0 {
		return fmt.Errorf("usage: following")
	}

	follows, err := s.db.GetFeedFollowsForUser(context.Background(), user.ID)
	if err != nil {
		return fmt.Errorf("could not get follows: %w", err)
	}

	fmt.Printf("You are following %d feeds:\n", len(follows))
	for _, follow := range follows {
		fmt.Printf("* %s\n", follow.FeedName)
	}
	return nil
}

func handlerUnfollow(s *state, cmd command, user database.User) error {
	if len(cmd.Args) != 1 {
		return fmt.Errorf("usage: unfollow <feed_url>")
	}

	url := cmd.Args[0]
	err := s.db.DeleteFeedFollowByURL(context.Background(), database.DeleteFeedFollowByURLParams{
		Url:    url,
		UserID: user.ID,
	})
	if err != nil {
		return fmt.Errorf("could not unfollow feed: %w", err)
	}

	fmt.Printf("Successfully unfollowed '%s'\n", url)
	return nil
}
