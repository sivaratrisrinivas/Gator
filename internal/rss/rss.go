// Package rss fetches and parses RSS 2.0 and Atom feeds politely: conditional
// GET with ETag / Last-Modified, a body size cap, and strict status handling.
package rss

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"strings"
	"time"
)

const maxFeedBytes = 10 << 20 // 10 MB

// ErrNotModified means the server answered 304 and nothing needs parsing.
var ErrNotModified = errors.New("feed not modified")

// HTTPError is a non-2xx, non-304 response.
type HTTPError struct{ StatusCode int }

func (e *HTTPError) Error() string { return fmt.Sprintf("unexpected HTTP status %d", e.StatusCode) }

// Item is one normalized feed entry.
type Item struct {
	Title       string
	Link        string
	Description string
	PublishedAt time.Time // zero if missing or unparseable
}

// Result is what a successful fetch returns, including cache validators to
// send on the next request.
type Result struct {
	Title        string
	Items        []Item
	ETag         string
	LastModified string
}

// Fetcher performs feed requests. The zero value is not usable; use NewFetcher.
type Fetcher struct {
	Client    *http.Client
	UserAgent string
}

func NewFetcher() *Fetcher {
	return &Fetcher{
		Client:    &http.Client{Timeout: 15 * time.Second},
		UserAgent: "gator/1.0 (+https://github.com/sivaratrisrinivas/Gator)",
	}
}

// Fetch downloads and parses a feed. Pass the validators from the previous
// successful fetch; a 304 returns ErrNotModified.
func (f *Fetcher) Fetch(ctx context.Context, url, etag, lastModified string) (*Result, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", f.UserAgent)
	req.Header.Set("Accept", "application/rss+xml, application/atom+xml, application/xml;q=0.9, text/xml;q=0.8, */*;q=0.1")
	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}
	if lastModified != "" {
		req.Header.Set("If-Modified-Since", lastModified)
	}

	resp, err := f.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotModified {
		return nil, ErrNotModified
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
		return nil, &HTTPError{StatusCode: resp.StatusCode}
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxFeedBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maxFeedBytes {
		return nil, fmt.Errorf("feed larger than %d bytes", maxFeedBytes)
	}

	res, err := Parse(body)
	if err != nil {
		return nil, err
	}
	res.ETag = resp.Header.Get("ETag")
	res.LastModified = resp.Header.Get("Last-Modified")
	return res, nil
}

type rssDoc struct {
	Channel struct {
		Title string `xml:"title"`
		Items []struct {
			Title       string `xml:"title"`
			Link        string `xml:"link"`
			Description string `xml:"description"`
			PubDate     string `xml:"pubDate"`
			DCDate      string `xml:"http://purl.org/dc/elements/1.1/ date"`
		} `xml:"item"`
	} `xml:"channel"`
}

type atomDoc struct {
	Title   string `xml:"title"`
	Entries []struct {
		Title string `xml:"title"`
		Links []struct {
			Href string `xml:"href,attr"`
			Rel  string `xml:"rel,attr"`
		} `xml:"link"`
		Summary   string `xml:"summary"`
		Content   string `xml:"content"`
		Published string `xml:"published"`
		Updated   string `xml:"updated"`
	} `xml:"entry"`
}

// Parse accepts RSS 2.0 or Atom 1.0. Items without a link are dropped because
// the link is the de-duplication key.
func Parse(data []byte) (*Result, error) {
	var root struct{ XMLName xml.Name }
	if err := xml.Unmarshal(data, &root); err != nil {
		return nil, fmt.Errorf("parse feed: %w", err)
	}
	res := &Result{}
	switch strings.ToLower(root.XMLName.Local) {
	case "rss", "rdf":
		var doc rssDoc
		if err := xml.Unmarshal(data, &doc); err != nil {
			return nil, fmt.Errorf("parse rss: %w", err)
		}
		res.Title = clean(doc.Channel.Title)
		for _, it := range doc.Channel.Items {
			date := it.PubDate
			if date == "" {
				date = it.DCDate
			}
			res.Items = append(res.Items, Item{
				Title: clean(it.Title), Link: strings.TrimSpace(it.Link),
				Description: clean(it.Description), PublishedAt: ParseDate(date),
			})
		}
	case "feed":
		var doc atomDoc
		if err := xml.Unmarshal(data, &doc); err != nil {
			return nil, fmt.Errorf("parse atom: %w", err)
		}
		res.Title = clean(doc.Title)
		for _, e := range doc.Entries {
			link := ""
			for _, l := range e.Links {
				if l.Rel == "" || l.Rel == "alternate" {
					link = l.Href
					break
				}
			}
			if link == "" && len(e.Links) > 0 {
				link = e.Links[0].Href
			}
			desc := e.Summary
			if desc == "" {
				desc = e.Content
			}
			date := e.Published
			if date == "" {
				date = e.Updated
			}
			res.Items = append(res.Items, Item{
				Title: clean(e.Title), Link: strings.TrimSpace(link),
				Description: clean(desc), PublishedAt: ParseDate(date),
			})
		}
	default:
		return nil, fmt.Errorf("unsupported feed root element <%s>", root.XMLName.Local)
	}

	kept := res.Items[:0]
	for _, it := range res.Items {
		if it.Link != "" {
			kept = append(kept, it)
		}
	}
	res.Items = kept
	return res, nil
}

func clean(s string) string { return strings.TrimSpace(html.UnescapeString(s)) }

var dateLayouts = []string{
	time.RFC1123Z,
	time.RFC1123,
	"Mon, 2 Jan 2006 15:04:05 -0700",
	"Mon, 2 Jan 2006 15:04:05 MST",
	"Mon, 02 Jan 2006 15:04 -0700",
	"2 Jan 2006 15:04:05 -0700",
	"02 Jan 2006 15:04:05 MST",
	time.RFC3339,
	time.RFC3339Nano,
	"2006-01-02T15:04:05Z0700",
	"2006-01-02T15:04:05",
	"2006-01-02 15:04:05",
	"2006-01-02",
}

// ParseDate tries the date formats seen in real feeds and returns UTC, or the
// zero time if none match.
func ParseDate(s string) time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}
	}
	for _, layout := range dateLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}
