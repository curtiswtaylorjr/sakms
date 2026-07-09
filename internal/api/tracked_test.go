package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/curtiswtaylorjr/sakms/internal/library"
	"github.com/curtiswtaylorjr/sakms/internal/mode"
	"github.com/curtiswtaylorjr/sakms/internal/servarr"
)

// TestListTracked_Adult_ReturnsItemsFromTheRealApp proves the generic
// Servarr-backed path still works for Adult (Whisparr) — the only mode
// left on it now that Movies and Series both own their own library.
func TestListTracked_Adult_ReturnsItemsFromTheRealApp(t *testing.T) {
	fakeWhisparr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v3/movie" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[{"id":1,"title":"Some Scene","foreignId":"abc","tags":[2,3]}]`))
	}))
	defer fakeWhisparr.Close()

	connStore, propStore, allowStore, settingsStore, grabsStore, libStore := testStores(t)
	ctx := context.Background()
	if err := connStore.Upsert(ctx, "whisparr", fakeWhisparr.URL, "test-key"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	srv := httptest.NewServer(NewMux(testHTTPClient(), connStore, propStore, allowStore, testProber(t), settingsStore, grabsStore, libStore))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/modes/adult/tracked")
	if err != nil {
		t.Fatalf("GET failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var got []servarr.TrackedItem
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if len(got) != 1 || got[0].Title != "Some Scene" || len(got[0].TagIDs) != 2 {
		t.Fatalf("unexpected response: %+v", got)
	}
}

func TestListTracked_Adult_MissingConnection(t *testing.T) {
	connStore, propStore, allowStore, settingsStore, grabsStore, libStore := testStores(t)
	srv := httptest.NewServer(NewMux(testHTTPClient(), connStore, propStore, allowStore, testProber(t), settingsStore, grabsStore, libStore))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/modes/adult/tracked")
	if err != nil {
		t.Fatalf("GET failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 when whisparr isn't configured, got %d", resp.StatusCode)
	}
}

// TestListTracked_Movies_ReturnsLibraryItemsWithLabelTags proves Movies
// never touches Radarr at all — it's served straight from libStore, with
// Tags as label strings (not numeric Servarr tag ids).
func TestListTracked_Movies_ReturnsLibraryItemsWithLabelTags(t *testing.T) {
	connStore, propStore, allowStore, settingsStore, grabsStore, libStore := testStores(t)
	ctx := context.Background()
	item, err := libStore.Upsert(ctx, library.Item{Mode: mode.Movies, TMDBID: 453, Title: "A Beautiful Mind", RootFolderPath: "/movies"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := libStore.AddTag(ctx, item.ID, "favorite"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	srv := httptest.NewServer(NewMux(testHTTPClient(), connStore, propStore, allowStore, testProber(t), settingsStore, grabsStore, libStore))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/modes/movies/tracked")
	if err != nil {
		t.Fatalf("GET failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 with zero Radarr connection configured, got %d", resp.StatusCode)
	}
	var got []libraryTrackedItem
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if len(got) != 1 || got[0].Title != "A Beautiful Mind" || len(got[0].Tags) != 1 || got[0].Tags[0] != "favorite" {
		t.Fatalf("unexpected response: %+v", got)
	}
}

// TestListTracked_Series_ReturnsLibrarySeriesWithLabelTags proves Series
// never touches Sonarr at all now — it's served straight from libStore,
// same shape as Movies.
func TestListTracked_Series_ReturnsLibrarySeriesWithLabelTags(t *testing.T) {
	connStore, propStore, allowStore, settingsStore, grabsStore, libStore := testStores(t)
	ctx := context.Background()
	series, err := libStore.UpsertSeries(ctx, library.Series{TMDBID: 555, Title: "Some Show", RootFolderPath: "/tv"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := libStore.AddSeriesTag(ctx, series.ID, "favorite"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	srv := httptest.NewServer(NewMux(testHTTPClient(), connStore, propStore, allowStore, testProber(t), settingsStore, grabsStore, libStore))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/modes/series/tracked")
	if err != nil {
		t.Fatalf("GET failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 with zero Sonarr connection configured, got %d", resp.StatusCode)
	}
	var got []libraryTrackedItem
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if len(got) != 1 || got[0].Title != "Some Show" || len(got[0].Tags) != 1 || got[0].Tags[0] != "favorite" {
		t.Fatalf("unexpected response: %+v", got)
	}
}
