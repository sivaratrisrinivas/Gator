package rss

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const rssSample = `<?xml version="1.0"?>
<rss version="2.0" xmlns:dc="http://purl.org/dc/elements/1.1/"><channel>
<title>Boot &amp; Blog</title>
<item><title>First &amp; best</title><link>https://ex.com/1</link><description>one</description><pubDate>Mon, 02 Jan 2006 15:04:05 -0700</pubDate></item>
<item><title>Second</title><link> https://ex.com/2 </link><pubDate>Tue, 03 Jan 2006 10:00:00 GMT</pubDate></item>
<item><title>Dublin Core date</title><link>https://ex.com/3</link><dc:date>2006-01-04T08:00:00Z</dc:date></item>
<item><title>No link is dropped</title></item>
</channel></rss>`

const atomSample = `<?xml version="1.0" encoding="utf-8"?>
<feed xmlns="http://www.w3.org/2005/Atom"><title>Atom Feed</title>
<entry><title>Atom one</title><link rel="self" href="https://ex.com/self"/><link rel="alternate" href="https://ex.com/a1"/>
<summary>sum</summary><published>2024-05-01T12:00:00+05:30</published></entry>
<entry><title>Atom two</title><link href="https://ex.com/a2"/><content>body</content><updated>2024-05-02T00:00:00Z</updated></entry>
</feed>`

func TestParseRSS(t *testing.T) {
	res, err := Parse([]byte(rssSample))
	if err != nil {
		t.Fatal(err)
	}
	if res.Title != "Boot & Blog" || len(res.Items) != 3 {
		t.Fatalf("title %q items %d", res.Title, len(res.Items))
	}
	if res.Items[0].Title != "First & best" || res.Items[1].Link != "https://ex.com/2" {
		t.Fatalf("items: %+v", res.Items)
	}
	for i, it := range res.Items {
		if it.PublishedAt.IsZero() {
			t.Errorf("item %d date not parsed", i)
		}
	}
	if want := time.Date(2006, 1, 2, 22, 4, 5, 0, time.UTC); !res.Items[0].PublishedAt.Equal(want) {
		t.Errorf("tz conversion: got %v", res.Items[0].PublishedAt)
	}
}

func TestParseAtom(t *testing.T) {
	res, err := Parse([]byte(atomSample))
	if err != nil {
		t.Fatal(err)
	}
	if res.Title != "Atom Feed" || len(res.Items) != 2 {
		t.Fatalf("title %q items %d", res.Title, len(res.Items))
	}
	if res.Items[0].Link != "https://ex.com/a1" || res.Items[1].Description != "body" {
		t.Fatalf("items: %+v", res.Items)
	}
	if want := time.Date(2024, 5, 1, 6, 30, 0, 0, time.UTC); !res.Items[0].PublishedAt.Equal(want) {
		t.Errorf("published: %v", res.Items[0].PublishedAt)
	}
}

func TestParseRejectsGarbage(t *testing.T) {
	for _, in := range []string{"", "not xml", "<html><body>hi</body></html>"} {
		if _, err := Parse([]byte(in)); err == nil {
			t.Errorf("%q: expected error", in)
		}
	}
}

func TestParseDate(t *testing.T) {
	cases := map[string]bool{
		"Mon, 02 Jan 2006 15:04:05 -0700": true,
		"Mon, 02 Jan 2006 15:04:05 GMT":   true,
		"Mon, 2 Jan 2006 15:04:05 +0000":  true,
		"2006-01-02T15:04:05Z":            true,
		"2006-01-02T15:04:05.123+02:00":   true,
		"2006-01-02":                      true,
		"yesterday":                       false,
		"":                                false,
	}
	for in, ok := range cases {
		if got := !ParseDate(in).IsZero(); got != ok {
			t.Errorf("%q: parsed=%v want %v", in, got, ok)
		}
	}
}

func TestFetchConditionalGet(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if r.Header.Get("User-Agent") == "" {
			t.Error("missing user agent")
		}
		if r.Header.Get("If-None-Match") == `"v1"` {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", `"v1"`)
		w.Header().Set("Last-Modified", "Mon, 02 Jan 2006 15:04:05 GMT")
		_, _ = w.Write([]byte(rssSample))
	}))
	defer srv.Close()

	f := NewFetcher()
	res, err := f.Fetch(context.Background(), srv.URL, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if res.ETag != `"v1"` || res.LastModified == "" || len(res.Items) != 3 {
		t.Fatalf("first fetch: %+v", res)
	}
	if _, err := f.Fetch(context.Background(), srv.URL, res.ETag, res.LastModified); err != ErrNotModified {
		t.Fatalf("second fetch: want ErrNotModified, got %v", err)
	}
	if hits != 2 {
		t.Fatalf("hits %d", hits)
	}
}

func TestFetchErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/500":
			http.Error(w, "boom", http.StatusInternalServerError)
		case "/big":
			_, _ = w.Write([]byte("<rss><channel>" + strings.Repeat("x", maxFeedBytes+10)))
		case "/slow":
			time.Sleep(300 * time.Millisecond)
		}
	}))
	defer srv.Close()

	f := NewFetcher()
	_, err := f.Fetch(context.Background(), srv.URL+"/500", "", "")
	if he, ok := err.(*HTTPError); !ok || he.StatusCode != 500 {
		t.Errorf("500: got %v", err)
	}
	if _, err := f.Fetch(context.Background(), srv.URL+"/big", "", ""); err == nil || !strings.Contains(err.Error(), "larger") {
		t.Errorf("big: got %v", err)
	}
	f.Client.Timeout = 50 * time.Millisecond
	if _, err := f.Fetch(context.Background(), srv.URL+"/slow", "", ""); err == nil {
		t.Error("slow: expected timeout")
	}
}
