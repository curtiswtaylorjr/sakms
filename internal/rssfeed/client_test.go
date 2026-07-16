package rssfeed

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// nzbgeekFixture is a real-shaped RSS 2.0 document — NZBGeek saved-search
// feeds return exactly this shape: <item><enclosure url="...nzb" length="...">
// per result. One item has no enclosure at all, to exercise the fallback path
// callers (internal/api's resolve handler) need.
const nzbgeekFixture = `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0">
  <channel>
    <title>NZBGeek Saved Search</title>
    <item>
      <title>Some.Release.Name.2026</title>
      <link>https://nzbgeek.info/details/abc123</link>
      <pubDate>Wed, 15 Jul 2026 12:00:00 +0000</pubDate>
      <enclosure url="https://nzbgeek.info/fetch/abc123.nzb" length="1073741824" type="application/x-nzb"/>
    </item>
    <item>
      <title>Another.Release.2026</title>
      <link>https://nzbgeek.info/details/def456</link>
      <pubDate>Wed, 15 Jul 2026 11:00:00 +0000</pubDate>
      <enclosure url="https://nzbgeek.info/fetch/def456.nzb" length="2147483648" type="application/x-nzb"/>
    </item>
    <item>
      <title>No.Enclosure.Item</title>
      <link>https://nzbgeek.info/details/ghi789</link>
      <pubDate>Wed, 15 Jul 2026 10:00:00 +0000</pubDate>
    </item>
  </channel>
</rss>`

func TestFetchItems_ParsesRSS2Feed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/rss+xml")
		w.Write([]byte(nzbgeekFixture))
	}))
	defer srv.Close()

	items, err := FetchItems(context.Background(), srv.Client(), srv.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 3 {
		t.Fatalf("expected 3 items, got %d: %+v", len(items), items)
	}

	first := items[0]
	if first.Title != "Some.Release.Name.2026" {
		t.Errorf("unexpected title: %q", first.Title)
	}
	if first.Link != "https://nzbgeek.info/details/abc123" {
		t.Errorf("unexpected link: %q", first.Link)
	}
	if first.PubDate != "Wed, 15 Jul 2026 12:00:00 +0000" {
		t.Errorf("unexpected pubDate: %q", first.PubDate)
	}
	if first.EnclosureURL != "https://nzbgeek.info/fetch/abc123.nzb" {
		t.Errorf("unexpected enclosure url: %q", first.EnclosureURL)
	}
	if first.EnclosureLength != 1073741824 {
		t.Errorf("unexpected enclosure length: %d", first.EnclosureLength)
	}

	noEnclosure := items[2]
	if noEnclosure.EnclosureURL != "" || noEnclosure.EnclosureLength != 0 {
		t.Errorf("expected no enclosure fields for item with no <enclosure>, got %+v", noEnclosure)
	}
}

func TestFetchItems_EmptyChannelReturnsEmptySlice(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<?xml version="1.0"?><rss version="2.0"><channel><title>Empty</title></channel></rss>`))
	}))
	defer srv.Close()

	items, err := FetchItems(context.Background(), srv.Client(), srv.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 0 {
		t.Errorf("expected no items, got %+v", items)
	}
}

func TestFetchItems_NonOKStatusIsAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	_, err := FetchItems(context.Background(), srv.Client(), srv.URL)
	if err == nil {
		t.Fatal("expected an error for a 404 response")
	}
}

func TestFetchItems_MalformedXMLIsAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("not xml at all"))
	}))
	defer srv.Close()

	_, err := FetchItems(context.Background(), srv.Client(), srv.URL)
	if err == nil {
		t.Fatal("expected an error for malformed XML")
	}
	if !strings.Contains(err.Error(), "parsing rss feed") {
		t.Errorf("expected a parse error, got: %v", err)
	}
}
