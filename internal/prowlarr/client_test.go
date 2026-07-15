package prowlarr

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newTestClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return New(Config{BaseURL: srv.URL, APIKey: "test-key"}, srv.Client())
}

// searchFixture is a plausible (but not live-confirmed — see package doc)
// /api/v1/search response spanning both protocols.
const searchFixture = `[
  {
    "guid": "prowlarr-guid-1",
    "title": "Some.Movie.2023.1080p.WEB-DL.x264-GROUP",
    "indexer": "SomeTorrentIndexer",
    "protocol": "torrent",
    "size": 4294967296,
    "seeders": 42,
    "downloadUrl": "https://indexer.example/download/1.torrent",
    "publishDate": "2023-05-01T00:00:00Z",
    "categories": [{"id": 2000}, {"id": 2040}],
    "indexerFlags": ["freeleech"]
  },
  {
    "guid": "prowlarr-guid-2",
    "title": "Some.Movie.2023.2160p.WEB-DL.x265-GROUP",
    "indexer": "SomeUsenetIndexer",
    "protocol": "usenet",
    "size": 8589934592,
    "seeders": 0,
    "downloadUrl": "https://indexer.example/download/2.nzb",
    "publishDate": "2023-05-02T00:00:00Z",
    "categories": [{"id": 2000}]
  }
]`

func TestSearch_ParsesFixtureAcrossBothProtocols(t *testing.T) {
	var gotPath string
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.String()
		if r.Header.Get("X-Api-Key") != "test-key" {
			t.Error("missing X-Api-Key header")
		}
		w.Write([]byte(searchFixture))
	})

	releases, err := c.Search(context.Background(), "Some Movie 2023", []int{2000})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(releases) != 2 {
		t.Fatalf("expected 2 releases, got %d", len(releases))
	}
	if releases[0].Protocol != Torrent || releases[0].Seeders != 42 {
		t.Errorf("unexpected first release: %+v", releases[0])
	}
	if len(releases[0].IndexerFlags) != 1 || releases[0].IndexerFlags[0] != "freeleech" {
		t.Errorf("expected indexerFlags to parse, got %+v", releases[0].IndexerFlags)
	}
	if releases[1].Protocol != Usenet {
		t.Errorf("unexpected second release: %+v", releases[1])
	}
	if !strings.Contains(gotPath, "query=Some+Movie+2023") {
		t.Errorf("expected query param in request path, got %q", gotPath)
	}
	if !strings.Contains(gotPath, "categories=2000") {
		t.Errorf("expected categories param in request path, got %q", gotPath)
	}
}

func TestSearch_NoCategoriesOmitsParam(t *testing.T) {
	var gotPath string
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.String()
		w.Write([]byte(`[]`))
	})

	if _, err := c.Search(context.Background(), "anything", nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(gotPath, "categories") {
		t.Errorf("expected no categories param when none given, got %q", gotPath)
	}
}

func TestSearch_PropagatesErrorStatus(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})

	if _, err := c.Search(context.Background(), "anything", nil); err == nil {
		t.Fatal("expected an error for a 401 response")
	}
}

// TestSearch_QueryStringUnaffectedByStructuredSearch guards the refactor:
// factoring the shared do+parse helper must leave Search's exact wire
// contract (type=search + query + no structured params) byte-identical.
func TestSearch_QueryStringUnaffectedByStructuredSearch(t *testing.T) {
	var gotPath string
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.String()
		w.Write([]byte(`[]`))
	})

	if _, err := c.Search(context.Background(), "Some Movie", []int{2000}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(gotPath, "type=search") {
		t.Errorf("expected type=search, got %q", gotPath)
	}
	if !strings.Contains(gotPath, "query=Some+Movie") {
		t.Errorf("expected query param, got %q", gotPath)
	}
	for _, structured := range []string{"tmdbid", "imdbid", "tvdbid", "season", "ep="} {
		if strings.Contains(gotPath, structured) {
			t.Errorf("free-text Search leaked structured param %q: %q", structured, gotPath)
		}
	}
}

func TestSearchByID(t *testing.T) {
	tests := []struct {
		name        string
		params      SearchByIDParams
		wantType    string
		wantPresent []string // substrings that must appear in the query string
		wantAbsent  []string // substrings that must NOT appear
	}{
		{
			name:        "TMDBID only routes to movie search",
			params:      SearchByIDParams{TMDBID: 550, Categories: []int{2000}},
			wantType:    "type=movie",
			wantPresent: []string{"tmdbid=550", "categories=2000"},
			wantAbsent:  []string{"type=tvsearch", "imdbid", "tvdbid", "season", "ep=", "query="},
		},
		{
			// Regression for a real "nothing is being found to grab" bug: an
			// id-only request (no query text) wasn't reliably honored as a
			// precise filter by every indexer — some fall back to Torznab's
			// "empty query = list recent releases" RSS-style behavior,
			// silently ignoring the id params. Query must travel ALONGSIDE
			// the id params, not replace them.
			name:        "Query travels alongside id params, not instead of them",
			params:      SearchByIDParams{Query: "Moana", TMDBID: 550, Categories: []int{2000}},
			wantType:    "type=movie",
			wantPresent: []string{"query=Moana", "tmdbid=550", "categories=2000"},
			wantAbsent:  []string{"type=tvsearch"},
		},
		{
			name:        "IMDBID only strips tt prefix and routes to movie search",
			params:      SearchByIDParams{IMDBID: "tt0137523"},
			wantType:    "type=movie",
			wantPresent: []string{"imdbid=0137523"},
			wantAbsent:  []string{"imdbid=tt0137523", "tmdbid", "tvdbid", "type=tvsearch"},
		},
		{
			name:        "TVDBID with season and episode routes to tv search",
			params:      SearchByIDParams{TVDBID: 81189, Season: 2, Episode: 5},
			wantType:    "type=tvsearch",
			wantPresent: []string{"tvdbid=81189", "season=2", "ep=5"},
			wantAbsent:  []string{"type=movie", "tmdbid", "imdbid"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotPath string
			c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				gotPath = r.URL.String()
				if r.Header.Get("X-Api-Key") != "test-key" {
					t.Error("missing X-Api-Key header")
				}
				w.Write([]byte(searchFixture))
			})

			releases, err := c.SearchByID(context.Background(), tt.params)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			// Shared parse path must yield the same Release shape as Search.
			if len(releases) != 2 || releases[0].Protocol != Torrent || releases[1].Protocol != Usenet {
				t.Errorf("unexpected releases from shared parse path: %+v", releases)
			}
			if !strings.Contains(gotPath, tt.wantType) {
				t.Errorf("expected %q in %q", tt.wantType, gotPath)
			}
			for _, want := range tt.wantPresent {
				if !strings.Contains(gotPath, want) {
					t.Errorf("expected %q in %q", want, gotPath)
				}
			}
			for _, absent := range tt.wantAbsent {
				if strings.Contains(gotPath, absent) {
					t.Errorf("did not expect %q in %q", absent, gotPath)
				}
			}
		})
	}
}

func TestSearchByID_PropagatesErrorStatus(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})

	if _, err := c.SearchByID(context.Background(), SearchByIDParams{TMDBID: 1}); err == nil {
		t.Fatal("expected an error for a 401 response")
	}
}
