# Gator

A multi-user RSS and Atom aggregator for the terminal, written in Go with Postgres. The first version came from Boot.dev's "Build a Blog Aggregator in Go" course. I then rebuilt the ingestion side so it behaves like a real feed crawler: concurrent, polite to the sites it polls, safe to run as several processes, and tested.

## How the aggregator works

```mermaid
flowchart LR
    T[ticker] --> C["ClaimFeedsToFetch<br/>(CTE + FOR UPDATE SKIP LOCKED)"]
    C --> P{{worker pool}}
    P --> F["conditional GET<br/>If-None-Match / If-Modified-Since"]
    F -->|200| X["parse RSS 2.0 or Atom<br/>13 date formats"] --> U["INSERT ... ON CONFLICT (url) DO NOTHING"]
    F -->|304| S[record success, keep validators]
    F -->|error| B["failure_count + 1<br/>next_fetch_at = now + 2^(n-1) min, max 6 h"]
```

1. Each tick claims a batch of due feeds in one SQL statement. `FOR UPDATE SKIP LOCKED` means two or more `gator agg` processes never fetch the same feed.
2. A bounded worker pool fetches them in parallel.
3. Requests send the feed's stored `ETag` and `Last-Modified`. A `304 Not Modified` costs the site almost nothing and skips parsing.
4. Responses are checked for status, capped at 10 MB, and parsed as RSS 2.0 or Atom.
5. Posts are de-duplicated by URL in the database with `ON CONFLICT DO NOTHING`.
6. A failing feed backs off exponentially (1, 2, 4 ... minutes, capped at 6 hours) and its last error is stored, so a dead site is not hammered every tick.

## What changed from the course version

| | Course version | Now |
|---|---|---|
| Feeds per tick | 1, serially | A claimed batch (default 32) across 8 workers |
| Multiple aggregators | Would fetch the same feed twice | Safe: `SKIP LOCKED` claims |
| HTTP | Status ignored, body never closed, no size cap | Status checked, body closed and capped, User-Agent set |
| Caching | Re-downloaded everything | Conditional GET with ETag and Last-Modified |
| Formats | RSS only, one date layout (Atom feeds saved 0 posts) | RSS 2.0, Atom 1.0, Dublin Core dates, 13 date layouts |
| Duplicates | Insert, then string-match "duplicate key" in the error | `ON CONFLICT (url) DO NOTHING`, counts new rows |
| Failures | Retried every tick forever | Exponential backoff, `last_error` stored |
| `browse` paging | Page argument was ignored | Real `LIMIT/OFFSET` |
| Shutdown | Ctrl-C mid-write | Stops cleanly on SIGINT/SIGTERM |
| Tests | None | Parser and fetcher tests (91% of the rss package) plus Postgres integration tests for the scraper |
| CI | None | gofmt, vet, race tests against Postgres, sqlc drift check |

### Measured

- One batch of 40 feeds, each server taking 200 ms to answer, 20 items per feed (local Postgres, `go test`): 1 worker took 8.26 s, 8 workers took 1.11 s, both stored all 800 posts. The course version fetched one feed per tick, so the same 40 feeds took 40 ticks.
- Live run against Hacker News, the Go blog (Atom), a 404 feed and a non-existent host: 40 posts stored with published dates in 1.1 s, both failures recorded with backoff instead of crashing the loop. The course parser would have stored 0 posts from the Go blog because it only understood RSS.

## Use it

```bash
go install github.com/sivaratrisrinivas/Gator@latest
echo '{"db_url":"postgres://postgres:postgres@localhost:5432/gator?sslmode=disable"}' > ~/.gatorconfig.json
goose -dir sql/schema postgres "$DB_URL" up

gator register alice
gator addfeed "Go Blog" https://go.dev/blog/feed.atom
gator addfeed "Hacker News" https://news.ycombinator.com/rss
gator agg 1m -workers 8 -batch 32     # or -once for a single batch
gator browse 10 2                      # 10 posts, page 2
```

Other commands: `login`, `users`, `feeds`, `follow <url>`, `following`, `unfollow <url>`, `reset`.

## Test

```bash
go test ./internal/rss/                                   # no database needed
createdb gator_test
TEST_DB_URL="postgres://postgres:postgres@localhost:5432/gator_test?sslmode=disable" go test -race -p 1 ./...
```

The integration tests spin up local HTTP feed servers and check de-duplication, 304 handling, backoff after a 502, that four racing aggregators claim 20 feeds with no overlap, and that `browse` pages never repeat a post.

## Stack

Go, Postgres, sqlc for typed queries, goose migrations, standard library HTTP and XML.
