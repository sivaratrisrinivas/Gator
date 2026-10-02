package main

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/sivaratrisrinivas/Gator/internal/database"
)

func handlerCreateFeed(s *state, cmd command, user database.User) error {
	if len(cmd.Args) != 2 {
		return fmt.Errorf("usage: addfeed <name> <url>")
	}

	name := cmd.Args[0]
	url := cmd.Args[1]

	feed, err := s.db.CreateFeed(context.Background(), database.CreateFeedParams{
		ID:        uuid.New(),
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
		UserID:    user.ID,
		Name:      name,
		Url:       url,
	})
	if err != nil {
		return fmt.Errorf("could not create feed: %w", err)
	}

	// Auto-follow the feed
	_, err = s.db.CreateFeedFollow(context.Background(), database.CreateFeedFollowParams{
		ID:        uuid.New(),
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
		UserID:    user.ID,
		FeedID:    feed.ID,
	})
	if err != nil {
		return fmt.Errorf("could not follow feed: %w", err)
	}

	fmt.Printf("Feed created and followed successfully: %s\n", feed.Name)
	return nil
}

func handlerListFeeds(s *state, cmd command) error {
	feeds, err := s.db.GetFeeds(context.Background())
	if err != nil {
		return fmt.Errorf("couldn't get feeds: %w", err)
	}

	if len(feeds) == 0 {
		fmt.Println("No feeds found.")
		return nil
	}

	fmt.Printf("Found %d feeds:\n", len(feeds))
	for _, feed := range feeds {
		feedsWithUser, err := s.db.GetFeedsWithUser(context.Background(), feed.UserID)
		if err != nil {
			return fmt.Errorf("couldn't get feed with user: %w", err)
		}
		if len(feedsWithUser) > 0 {
			fmt.Printf("* ID:            %s\n", feed.ID)
			fmt.Printf("* Created:       %v\n", feed.CreatedAt)
			fmt.Printf("* Updated:       %v\n", feed.UpdatedAt)
			fmt.Printf("* Name:          %s\n", feed.Name)
			fmt.Printf("* URL:           %s\n", feed.Url)
			fmt.Printf("* User:          %s\n", feedsWithUser[0].UserName)
		}
		fmt.Println("=====================================")
	}

	return nil
}
