package tvdb

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// newTestServer builds a minimal TVDB v4 httptest server that returns
// loginToken on POST /v4/login and items on GET /v4/search.
func newTestServer(t *testing.T, loginToken string, items []searchItem) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v4/login", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"status":"success","data":{"token":%q}}`, loginToken)
	})
	mux.HandleFunc("GET /v4/search", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// Build the data array inline — avoid referencing the unexported
		// searchItem type in a json.Marshal call that the compiler can't infer.
		w.Write([]byte(`{"status":"success","data":[`))
		for i, it := range items {
			if i > 0 {
				w.Write([]byte(","))
			}
			fmt.Fprintf(w, `{"tvdb_id":%q,"name":%q,"year":%q}`, it.TVDBID, it.Name, it.Year)
		}
		w.Write([]byte(`]}`))
	})
	return httptest.NewServer(mux)
}

func TestSearchSeries(t *testing.T) {
	srv := newTestServer(t, "tok-abc", []searchItem{
		{TVDBID: "81189", Name: "Breaking Bad", Year: "2008"},
		{TVDBID: "153021", Name: "Breaking In", Year: "2011"},
	})
	defer srv.Close()

	c := New(Config{BaseURL: srv.URL, APIKey: "k"}, srv.Client())
	results, err := c.SearchSeries(context.Background(), "breaking bad")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if results[0].TVDBID != 81189 {
		t.Errorf("TVDBID: want 81189, got %d", results[0].TVDBID)
	}
	if results[0].Name != "Breaking Bad" {
		t.Errorf("Name: want %q, got %q", "Breaking Bad", results[0].Name)
	}
	if results[0].Year != 2008 {
		t.Errorf("Year: want 2008, got %d", results[0].Year)
	}
}

func TestSearchMovies(t *testing.T) {
	srv := newTestServer(t, "tok-xyz", []searchItem{
		{TVDBID: "376754", Name: "Avengers: Endgame", Year: "2019"},
	})
	defer srv.Close()

	c := New(Config{BaseURL: srv.URL, APIKey: "k"}, srv.Client())
	results, err := c.SearchMovies(context.Background(), "endgame")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].TVDBID != 376754 {
		t.Errorf("unexpected results: %+v", results)
	}
}

func TestToken_CachedAcrossCalls(t *testing.T) {
	loginCalls := 0
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v4/login", func(w http.ResponseWriter, r *http.Request) {
		loginCalls++
		fmt.Fprintf(w, `{"status":"success","data":{"token":"t"}}`)
	})
	mux.HandleFunc("GET /v4/search", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"status":"success","data":[]}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := New(Config{BaseURL: srv.URL, APIKey: "k"}, srv.Client())
	c.SearchSeries(context.Background(), "x") //nolint:errcheck
	c.SearchSeries(context.Background(), "y") //nolint:errcheck
	if loginCalls != 1 {
		t.Errorf("expected 1 login call, got %d (token should be cached)", loginCalls)
	}
}

func TestToken_RefreshesAfterExpiry(t *testing.T) {
	loginCalls := 0
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v4/login", func(w http.ResponseWriter, r *http.Request) {
		loginCalls++
		fmt.Fprintf(w, `{"status":"success","data":{"token":"t"}}`)
	})
	mux.HandleFunc("GET /v4/search", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"status":"success","data":[]}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := New(Config{BaseURL: srv.URL, APIKey: "k"}, srv.Client())
	c.SearchSeries(context.Background(), "x") //nolint:errcheck
	// Backdate tokenAt past the TTL so the next call must re-fetch.
	c.mu.Lock()
	c.tokenAt = time.Now().Add(-(tokenTTL + time.Second))
	c.mu.Unlock()
	c.SearchSeries(context.Background(), "y") //nolint:errcheck
	if loginCalls != 2 {
		t.Errorf("expected 2 login calls after expiry, got %d", loginCalls)
	}
}

func TestSearchIgnoresMalformedItems(t *testing.T) {
	// Items with empty TVDBID, non-numeric TVDBID, or empty Name must be dropped.
	srv := newTestServer(t, "tok", []searchItem{
		{TVDBID: "", Name: "no id"},
		{TVDBID: "not-a-number", Name: "bad id"},
		{TVDBID: "12345", Name: ""},
		{TVDBID: "99", Name: "Valid", Year: "2020"},
	})
	defer srv.Close()

	c := New(Config{BaseURL: srv.URL, APIKey: "k"}, srv.Client())
	results, err := c.SearchSeries(context.Background(), "q")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].TVDBID != 99 {
		t.Errorf("expected exactly the Valid item, got %+v", results)
	}
}

func TestSearchYearAbsent(t *testing.T) {
	// An item with no year should parse to Year==0 without error.
	srv := newTestServer(t, "tok", []searchItem{
		{TVDBID: "100", Name: "No Year Show", Year: ""},
	})
	defer srv.Close()

	c := New(Config{BaseURL: srv.URL, APIKey: "k"}, srv.Client())
	results, err := c.SearchSeries(context.Background(), "q")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Year != 0 {
		t.Errorf("expected Year==0 for absent year, got %+v", results)
	}
}

func TestPing_InvalidKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	defer srv.Close()

	c := New(Config{BaseURL: srv.URL, APIKey: "bad"}, srv.Client())
	if err := c.Ping(context.Background()); err == nil {
		t.Error("expected error for 401 login response, got nil")
	}
}
