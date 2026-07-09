package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestListRootFolders_ReturnsPathsFromTheRealApp proves the settings-UI
// contract: the picker gets the mode's ACTUAL root folders, not a free-text
// field a user could mistype or let go stale. Series (Sonarr) still uses
// this *arr-backed path; Movies' root folder is its own free-typed setting
// now (see TestListRootFolders_Movies_NotApplicable below).
func TestListRootFolders_ReturnsPathsFromTheRealApp(t *testing.T) {
	fakeSonarr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v3/rootfolder" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[
			{"id":1,"path":"/media/Series","accessible":true,"freeSpace":1,"unmappedFolders":[]},
			{"id":2,"path":"/media/Series (Kids)","accessible":false,"freeSpace":0,"unmappedFolders":[]}
		]`))
	}))
	defer fakeSonarr.Close()

	connStore, propStore, allowStore, settingsStore, grabsStore, libStore := testStores(t)
	ctx := context.Background()
	if err := connStore.Upsert(ctx, "sonarr", fakeSonarr.URL, "test-key"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	srv := httptest.NewServer(NewMux(testHTTPClient(), connStore, propStore, allowStore, testProber(t), settingsStore, grabsStore, libStore))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/modes/series/root-folders")
	if err != nil {
		t.Fatalf("GET failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var got []rootFolderSummary
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 root folders, got %+v", got)
	}
	if got[0].Path != "/media/Series" || !got[0].Accessible {
		t.Errorf("unexpected first entry: %+v", got[0])
	}
	if got[1].Path != "/media/Series (Kids)" || got[1].Accessible {
		t.Errorf("unexpected second entry: %+v", got[1])
	}
}

// TestListRootFolders_MissingConnection confirms an unconfigured mode fails
// fast with a clear error rather than a confusing empty list.
func TestListRootFolders_MissingConnection(t *testing.T) {
	connStore, propStore, allowStore, settingsStore, grabsStore, libStore := testStores(t)
	srv := httptest.NewServer(NewMux(testHTTPClient(), connStore, propStore, allowStore, testProber(t), settingsStore, grabsStore, libStore))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/modes/series/root-folders")
	if err != nil {
		t.Fatalf("GET failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 when sonarr isn't configured, got %d", resp.StatusCode)
	}
}

// TestListRootFolders_Movies_NotApplicable confirms Movies gets a clear 400
// instead of a nil-Servarr crash — there's no *arr app to ask anymore (see
// GET /api/modes/movies/library/root-folder instead).
func TestListRootFolders_Movies_NotApplicable(t *testing.T) {
	connStore, propStore, allowStore, settingsStore, grabsStore, libStore := testStores(t)
	srv := httptest.NewServer(NewMux(testHTTPClient(), connStore, propStore, allowStore, testProber(t), settingsStore, grabsStore, libStore))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/modes/movies/root-folders")
	if err != nil {
		t.Fatalf("GET failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for movies, got %d", resp.StatusCode)
	}
}
