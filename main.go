package main

import (
	"database/sql"
	"fmt"
	"os"

	_ "github.com/lib/pq"
	"github.com/sivaratrisrinivas/Gator/internal/config"
	"github.com/sivaratrisrinivas/Gator/internal/database"
)

// state holds core dependencies needed throughout the application
// Using a struct instead of globals makes the app more testable
type state struct {
	// db holds all our database operations
	// Using Queries type from generated sqlc code for type safety
	db *database.Queries
	// cfg holds user preferences and DB connection info
	// Pointer because we need to modify it
	cfg *config.Config
}

func main() {
	// Load user settings from ~/.gatorconfig.json
	// This contains database URL and current user
	cfg, err := config.Read()
	if err != nil {
		// If config can't be read, print error and exit with status 1
		fmt.Fprintf(os.Stderr, "error reading config: %v\n", err)
		os.Exit(1)
	}

	// Open connection to PostgreSQL using config URL
	// sql.Open validates URL but doesn't actually connect yet
	db, err := sql.Open("postgres", cfg.DBURL)
	if err != nil {
		// If DB URL is invalid, print error and exit
		fmt.Fprintf(os.Stderr, "error connecting to db: %v\n", err)
		os.Exit(1)
	}
	// Ensure DB connection is closed when program exits
	defer db.Close()
	// Create wrapper for type-safe database operations
	dbQueries := database.New(db)

	// Create program state with all dependencies
	// This will be passed to command handlers
	programState := &state{
		db:  dbQueries,
		cfg: &cfg,
	}

	// Initialize command system
	// commands struct holds all registered CLI commands
	cmds := commands{
		// Map command names to their handler functions
		// e.g., "register" -> handlerRegister
		registeredCommands: make(map[string]func(*state, command) error),
	}
	// Register all available commands
	cmds.register("register", handlerRegister) // Create new user
	cmds.register("login", handlerLogin)       // Switch current user
	cmds.register("reset", handlerReset)       // Clear database
	cmds.register("users", handlerUsers)       // List all users
	cmds.register("addfeed", middlewareLoggedIn(handlerCreateFeed))
	cmds.register("feeds", handlerListFeeds)
	cmds.register("follow", middlewareLoggedIn(handlerFollow))
	cmds.register("following", middlewareLoggedIn(handlerFollowing))
	cmds.register("agg", middlewareLoggedIn(handlerAgg))
	cmds.register("unfollow", middlewareLoggedIn(handlerUnfollow))
	cmds.register("browse", middlewareLoggedIn(handlerBrowse))

	// Check if user provided a command
	// os.Args[0] is program name, need at least one more arg
	if len(os.Args) < 2 {
		// If no command provided, show usage and exit
		fmt.Println("Usage: cli <command> [args...]")
		os.Exit(1)
	}

	// Parse command line arguments
	// First argument after program name is command
	cmdName := os.Args[1]
	// Remaining arguments are command parameters
	cmdArgs := os.Args[2:]

	// Execute the requested command
	err = cmds.run(programState, command{Name: cmdName, Args: cmdArgs})
	if err != nil {
		// If command fails, print error and exit with status 1
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
}
