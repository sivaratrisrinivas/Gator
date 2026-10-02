package main

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/sivaratrisrinivas/Gator/internal/database"
)

// handlerRegister creates new user
func handlerRegister(s *state, cmd command) error {
	// Need exactly one argument (username)
	if len(cmd.Args) != 1 {
		return fmt.Errorf("usage: %v <name>", cmd.Name)
	}

	name := cmd.Args[0]

	// Check if user already exists
	_, err := s.db.GetUser(context.Background(), name)
	if err == nil {
		return fmt.Errorf("user %s already exists", name)
	} else if err != sql.ErrNoRows {
		return fmt.Errorf("error checking user: %v", err)
	}

	// Create new user with UUID and timestamps
	user, err := s.db.CreateUser(context.Background(), database.CreateUserParams{
		ID:        uuid.New(),
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
		Name:      name,
	})
	if err != nil {
		return fmt.Errorf("error creating user: %v", err)
	}

	// Save username to config file
	err = s.cfg.SetUser(user.Name)
	if err != nil {
		return fmt.Errorf("error setting current user: %v", err)
	}

	// Show success message
	fmt.Println("User created successfully:")
	printUser(user)
	return nil
}

// handlerLogin switches to existing user
func handlerLogin(s *state, cmd command) error {
	if len(cmd.Args) != 1 {
		return fmt.Errorf("usage: %s <name>", cmd.Name)
	}
	name := cmd.Args[0]

	// Check if user exists
	_, err := s.db.GetUser(context.Background(), name)
	if err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("user %s does not exist", name)
		}
		return fmt.Errorf("couldn't find user: %w", err)
	}

	// Save username to config
	err = s.cfg.SetUser(name)
	if err != nil {
		return fmt.Errorf("couldn't set current user: %w", err)
	}

	fmt.Println("User switched successfully!")
	return nil
}

// handlerUsers lists all users
func handlerUsers(s *state, cmd command) error {
	users, err := s.db.GetAllUsers(context.Background())
	if err != nil {
		return fmt.Errorf("couldn't list users: %w", err)
	}

	// Print users, marking current user
	for _, user := range users {
		if user.Name == s.cfg.CurrentUserName {
			fmt.Printf("* %v (current)\n", user.Name)
			continue
		}
		fmt.Printf("* %v\n", user.Name)
	}
	return nil
}

// printUser shows user details
func printUser(user database.User) {
	fmt.Printf(" * ID:      %v\n", user.ID)
	fmt.Printf(" * Name:    %v\n", user.Name)
}
