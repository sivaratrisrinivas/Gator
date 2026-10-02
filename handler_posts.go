package main

import (
	"context"
	"fmt"
	"strconv"

	"github.com/sivaratrisrinivas/Gator/internal/database"
)

func handlerBrowse(s *state, cmd command, user database.User) error {
	limit := 10 // Default limit
	page := 1   // Default page

	// Parse arguments: browse [limit] [page]
	if len(cmd.Args) > 0 {
		var err error
		limit, err = strconv.Atoi(cmd.Args[0])
		if err != nil {
			return fmt.Errorf("limit must be a number: %w", err)
		}
		if limit < 1 || limit > 100 {
			return fmt.Errorf("limit must be between 1 and 100")
		}
	}

	if len(cmd.Args) > 1 {
		var err error
		page, err = strconv.Atoi(cmd.Args[1])
		if err != nil {
			return fmt.Errorf("page must be a number: %w", err)
		}
		if page < 1 {
			return fmt.Errorf("page must be >= 1")
		}
	}

	posts, err := s.db.GetPostsForUser(context.Background(), database.GetPostsForUserParams{
		UserID: user.ID,
		Limit:  int32(limit),
		Offset: int32((page - 1) * limit),
	})
	if err != nil {
		return fmt.Errorf("error getting posts: %w", err)
	}

	fmt.Printf("Found %d posts (page %d):\n", len(posts), page)
	for _, post := range posts {
		fmt.Printf("\nTitle: %s\n", post.Title)
		fmt.Printf("URL: %s\n", post.Url)
		if post.Description.Valid {
			fmt.Printf("Description: %s\n", post.Description.String)
		}
		if post.PublishedAt.Valid {
			fmt.Printf("Published: %v\n", post.PublishedAt.Time.Format("2006-01-02 15:04:05"))
		}
		fmt.Println("------------------------")
	}

	if len(posts) == limit {
		fmt.Printf("\nFor next page, use: browse %d %d\n", limit, page+1)
	}
	return nil
}
