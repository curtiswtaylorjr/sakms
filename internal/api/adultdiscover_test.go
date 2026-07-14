package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// fakeTPDB serves ThePornDB's /scenes REST endpoint from a handler the test
// supplies, so a test can assert the exact query params (browse vs. search).
func fakeTPDB(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return srv
}

func TestAdultDiscoverHandler_Browse(t *testing.T) {
	var gotPage, gotPerPage, gotQ string
	tpdb := fakeTPDB(t, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		gotPage, gotPerPage = q.Get("page"), q.Get("per_page")
		_, hasQ := q["q"]
		if hasQ {
			gotQ = "present"
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data":[{"_id":"s1","title":"A Scene","date":"2024-01-01","site":{"name":"Tushy"},"duration":1800}]}`))
	})

	connStore, propStore, allowStore, settingsStore, grabsStore, libStore, slidersStore, traktStore := testStores(t)
	if err := connStore.Upsert(context.Background(), "tpdb", tpdb.URL, "key"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	srv := httptest.NewServer(NewMux(testHTTPClient(), connStore, propStore, allowStore, testProber(t), testPHasher(t), testVideoHasher(t), settingsStore, grabsStore, libStore, slidersStore, traktStore))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/modes/adult/discover?page=2&perPage=15")
	if err != nil {
		t.Fatalf("GET failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if gotPage != "2" || gotPerPage != "15" {
		t.Errorf("expected page=2 per_page=15, got page=%q per_page=%q", gotPage, gotPerPage)
	}
	if gotQ != "" {
		t.Errorf("expected no search term on a browse, got one")
	}

	var items []adultScene
	if err := json.NewDecoder(resp.Body).Decode(&items); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if len(items) != 1 || items[0].Title != "A Scene" || items[0].Studio != "Tushy" || items[0].ID != "s1" {
		t.Errorf("unexpected items (studio must map from Site): %+v", items)
	}
	if items[0].DurationSeconds != 1800 {
		t.Errorf("expected DurationSeconds to map from TPDB's duration field, got %d", items[0].DurationSeconds)
	}
}

// TestAdultDiscoverHandler_SearchByTerm proves the q param routes to
// SearchByTitle (the search-by-term entry point) rather than the browse path —
// the "browse + search-by-term for v1" the plan requires.
func TestAdultDiscoverHandler_SearchByTerm(t *testing.T) {
	var gotQ string
	tpdb := fakeTPDB(t, func(w http.ResponseWriter, r *http.Request) {
		gotQ = r.URL.Query().Get("q")
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data":[{"_id":"s2","title":"Found Scene","date":"2023-05-05","site":{"name":"Vixen"}}]}`))
	})

	connStore, propStore, allowStore, settingsStore, grabsStore, libStore, slidersStore, traktStore := testStores(t)
	if err := connStore.Upsert(context.Background(), "tpdb", tpdb.URL, "key"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	srv := httptest.NewServer(NewMux(testHTTPClient(), connStore, propStore, allowStore, testProber(t), testPHasher(t), testVideoHasher(t), settingsStore, grabsStore, libStore, slidersStore, traktStore))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/modes/adult/discover?q=found+scene")
	if err != nil {
		t.Fatalf("GET failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if gotQ != "found scene" {
		t.Errorf("expected the search term to reach SearchByTitle as q=found scene, got %q", gotQ)
	}
	var items []adultScene
	if err := json.NewDecoder(resp.Body).Decode(&items); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if len(items) != 1 || items[0].Title != "Found Scene" {
		t.Errorf("unexpected items: %+v", items)
	}
}

func TestAdultDiscoverHandler_TPDBNotConfigured(t *testing.T) {
	connStore, propStore, allowStore, settingsStore, grabsStore, libStore, slidersStore, traktStore := testStores(t)
	srv := httptest.NewServer(NewMux(testHTTPClient(), connStore, propStore, allowStore, testProber(t), testPHasher(t), testVideoHasher(t), settingsStore, grabsStore, libStore, slidersStore, traktStore))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/modes/adult/discover")
	if err != nil {
		t.Fatalf("GET failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 when tpdb isn't configured, got %d", resp.StatusCode)
	}
}
