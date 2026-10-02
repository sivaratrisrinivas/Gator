package main

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	_ "github.com/lib/pq"
	"github.com/sivaratrisrinivas/Gator/internal/database"
	"github.com/sivaratrisrinivas/Gator/internal/rss"
)

func TestBackoff(t *testing.T) {
	max := 6 * time.Hour
	cases := map[int]time.Duration{0: 0, 1: time.Minute, 2: 2 * time.Minute, 5: 16 * time.Minute, 9: 256 * time.Minute, 10: max, 200: max}
	for n, want := range cases {
		if got := Backoff(n, max); got != want {
			t.Errorf("Backoff(%d)=%v want %v", n, got, want)
		}
	}
}

// openTestDB applies the goose "Up" sections of sql/schema to a fresh schema.
// Integration tests are skipped unless TEST_DB_URL is set (CI sets it).
func openTestDB(t *testing.T) (*sql.DB, *database.Queries) {
	t.Helper()
	url := os.Getenv("TEST_DB_URL")
	if url == "" {
		t.Skip("TEST_DB_URL not set")
	}
	db, err := sql.Open("postgres", url)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DROP SCHEMA public CASCADE; CREATE SCHEMA public;`); err != nil {
		t.Fatal(err)
	}
	files, _ := filepath.Glob("sql/schema/*.sql")
	sort.Strings(files)
	for _, f := range files {
		b, _ := os.ReadFile(f)
		up := strings.SplitN(strings.SplitN(string(b), "-- +goose Down", 2)[0], "-- +goose Up", 2)[1]
		up = strings.NewReplacer("-- +goose StatementBegin", "", "-- +goose StatementEnd", "").Replace(up)
		if _, err := db.Exec(up); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
	}
	t.Cleanup(func() { db.Close() })
	return db, database.New(db)
}

func rssBody(prefix string, n int) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0"?><rss version="2.0"><channel><title>` + prefix + `</title>`)
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, `<item><title>%s %d</title><link>https://ex.com/%s/%d</link><pubDate>Mon, 02 Jan 2006 15:04:05 GMT</pubDate></item>`, prefix, i, prefix, i)
	}
	b.WriteString(`</channel></rss>`)
	return b.String()
}

func addFeed(t *testing.T, q *database.Queries, user uuid.UUID, name, url string) {
	t.Helper()
	now := time.Now().UTC()
	if _, err := q.CreateFeed(context.Background(), database.CreateFeedParams{
		ID: uuid.New(), CreatedAt: now, UpdatedAt: now, Name: name, Url: url, UserID: user,
	}); err != nil {
		t.Fatal(err)
	}
}

func newUser(t *testing.T, q *database.Queries) uuid.UUID {
	t.Helper()
	now := time.Now().UTC()
	u, err := q.CreateUser(context.Background(), database.CreateUserParams{ID: uuid.New(), CreatedAt: now, UpdatedAt: now, Name: "u-" + uuid.NewString()[:8]})
	if err != nil {
		t.Fatal(err)
	}
	return u.ID
}

func TestScraperEndToEnd(t *testing.T) {
	db, q := openTestDB(t)
	var goodHits, badHits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/good":
			goodHits.Add(1)
			if r.Header.Get("If-None-Match") == `"g1"` {
				w.WriteHeader(http.StatusNotModified)
				return
			}
			w.Header().Set("ETag", `"g1"`)
			fmt.Fprint(w, rssBody("good", 5))
		case "/dup": // shares two links with /good: must not create duplicates
			fmt.Fprint(w, rssBody("good", 2)+"")
		case "/bad":
			badHits.Add(1)
			http.Error(w, "down", http.StatusBadGateway)
		}
	}))
	defer srv.Close()

	user := newUser(t, q)
	addFeed(t, q, user, "good", srv.URL+"/good")
	addFeed(t, q, user, "dup", srv.URL+"/dup")
	addFeed(t, q, user, "bad", srv.URL+"/bad")

	sc := &Scraper{DB: q, Fetcher: rss.NewFetcher(), Workers: 4, BatchSize: 10, MaxBackoff: time.Hour, Now: time.Now}
	out, err := sc.RunOnce(context.Background())
	if err != nil || len(out) != 3 {
		t.Fatalf("first run: %v, %d outcomes", err, len(out))
	}
	var posts int
	_ = db.QueryRow(`SELECT count(*) FROM posts`).Scan(&posts)
	if posts != 5 {
		t.Fatalf("want 5 unique posts, got %d", posts)
	}
	var failures int
	var nextFetch sql.NullTime
	var lastErr sql.NullString
	_ = db.QueryRow(`SELECT failure_count, next_fetch_at, last_error FROM feeds WHERE name='bad'`).Scan(&failures, &nextFetch, &lastErr)
	if failures != 1 || !nextFetch.Valid || !strings.Contains(lastErr.String, "502") {
		t.Fatalf("bad feed state: %d %v %q", failures, nextFetch, lastErr.String)
	}

	// Second run: the good feed answers 304, the bad feed is still backing off.
	out, err = sc.RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	notModified := 0
	for _, o := range out {
		if o.Feed.Name == "bad" {
			t.Fatal("bad feed fetched during backoff")
		}
		if o.NotModified {
			notModified++
		}
	}
	if notModified != 1 || badHits.Load() != 1 || goodHits.Load() != 2 {
		t.Fatalf("notModified=%d badHits=%d goodHits=%d", notModified, badHits.Load(), goodHits.Load())
	}
}

func TestConcurrentClaimsDoNotOverlap(t *testing.T) {
	_, q := openTestDB(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(20 * time.Millisecond)
		fmt.Fprint(w, rssBody(strings.Trim(r.URL.Path, "/"), 1))
	}))
	defer srv.Close()
	user := newUser(t, q)
	for i := 0; i < 20; i++ {
		addFeed(t, q, user, fmt.Sprintf("f%d", i), fmt.Sprintf("%s/f%d", srv.URL, i))
	}

	var mu sync.Mutex
	seen := map[string]int{}
	var wg sync.WaitGroup
	for p := 0; p < 4; p++ { // four aggregator processes racing for the same table
		wg.Add(1)
		go func() {
			defer wg.Done()
			sc := &Scraper{DB: q, Fetcher: rss.NewFetcher(), Workers: 2, BatchSize: 5, MaxBackoff: time.Hour, Now: time.Now}
			out, err := sc.RunOnce(context.Background())
			if err != nil {
				t.Error(err)
				return
			}
			mu.Lock()
			for _, o := range out {
				seen[o.Feed.Name]++
			}
			mu.Unlock()
		}()
	}
	wg.Wait()
	if len(seen) != 20 {
		t.Fatalf("claimed %d distinct feeds, want 20", len(seen))
	}
	for name, n := range seen {
		if n != 1 {
			t.Fatalf("%s claimed %d times", name, n)
		}
	}
}

func TestBrowsePagination(t *testing.T) {
	db, q := openTestDB(t)
	user := newUser(t, q)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, rssBody("p", 7)) }))
	defer srv.Close()
	addFeed(t, q, user, "p", srv.URL)
	var feedID uuid.UUID
	_ = db.QueryRow(`SELECT id FROM feeds`).Scan(&feedID)
	now := time.Now().UTC()
	if _, err := q.CreateFeedFollow(context.Background(), database.CreateFeedFollowParams{ID: uuid.New(), CreatedAt: now, UpdatedAt: now, UserID: user, FeedID: feedID}); err != nil {
		t.Fatal(err)
	}
	sc := &Scraper{DB: q, Fetcher: rss.NewFetcher(), Workers: 1, BatchSize: 1, MaxBackoff: time.Hour, Now: time.Now}
	if _, err := sc.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	seen := map[uuid.UUID]bool{}
	for page := 0; page < 3; page++ {
		posts, err := q.GetPostsForUser(context.Background(), database.GetPostsForUserParams{UserID: user, Limit: 3, Offset: int32(page * 3)})
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range posts {
			if seen[p.ID] {
				t.Fatalf("post %s repeated across pages", p.Title)
			}
			seen[p.ID] = true
		}
	}
	if len(seen) != 7 {
		t.Fatalf("paged through %d posts, want 7", len(seen))
	}
}
