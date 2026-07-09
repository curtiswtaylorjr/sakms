package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/curtiswtaylorjr/sakms/internal/proposals"
)

// fakeRadarrHandler serves just enough of Radarr's API for a Scan followed
// by an Apply to succeed end to end. Kept for Series' equivalent Sonarr
// wire shape (itemResource() differs only in path segment) — Movies no
// longer uses Radarr at all (see TestRenameWorkflow_Movies_ScanThenApply_EndToEnd).
func fakeSonarrRenameHandler(t *testing.T, addedID int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v3/rootfolder":
			w.Write([]byte(`[{"id":1,"path":"/media/Series","accessible":true,"freeSpace":1,"unmappedFolders":[
				{"name":"Some.Show.S01E01.1080p.WEB-DL.x264-GROUP","path":"/media/Series/Some.Show.S01E01.1080p.WEB-DL.x264-GROUP","relativePath":"Some.Show.S01E01.1080p.WEB-DL.x264-GROUP"}
			]}]`))
		case r.URL.Path == "/api/v3/series" && r.Method == http.MethodGet:
			w.Write([]byte(`[]`))
		case r.URL.Path == "/api/v3/series" && r.Method == http.MethodPost:
			json.NewEncoder(w).Encode(map[string]any{"id": addedID})
		case r.URL.Path == "/api/v3/series/lookup":
			w.Write([]byte(`[{"title":"Some Show","year":2020,"tvdbId":453}]`))
		case r.URL.Path == "/api/v3/qualityprofile":
			w.Write([]byte(`[{"id":4,"name":"HD-1080p"}]`))
		case r.URL.Path == "/api/v3/command":
			w.Write([]byte(`{}`))
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}
}

// TestRenameWorkflow_ScanThenApply_EndToEnd exercises the full staged-review
// loop the design spec describes: Scan populates the queue, the queue is
// visible via List, and Apply commits exactly the one proposal a human
// approved — hitting SAK's real HTTP handlers, a real migrated SQLite
// database, and a fake Sonarr, not any package in isolation. Series still
// uses the *arr-backed path; Movies' own libStore-backed path is covered
// separately below.
func TestRenameWorkflow_ScanThenApply_EndToEnd(t *testing.T) {
	fakeSonarr := httptest.NewServer(fakeSonarrRenameHandler(t, 55))
	defer fakeSonarr.Close()

	connStore, propStore, allowStore, settingsStore, grabsStore, libStore := testStores(t)
	if err := connStore.Upsert(context.Background(), "sonarr", fakeSonarr.URL, "test-key"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	srv := httptest.NewServer(NewMux(testHTTPClient(), connStore, propStore, allowStore, testProber(t), settingsStore, grabsStore, libStore))
	defer srv.Close()

	scanResp, err := http.Post(srv.URL+"/api/modes/series/rename/scan", "application/json", nil)
	if err != nil {
		t.Fatalf("scan POST failed: %v", err)
	}
	defer scanResp.Body.Close()
	if scanResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 from scan, got %d", scanResp.StatusCode)
	}
	var scanned []proposals.Proposal
	if err := json.NewDecoder(scanResp.Body).Decode(&scanned); err != nil {
		t.Fatalf("decoding scan response: %v", err)
	}
	if len(scanned) != 1 || scanned[0].Status != proposals.Pending || scanned[0].Title != "Some Show" {
		t.Fatalf("unexpected scan result: %+v", scanned)
	}

	listResp, err := http.Get(srv.URL + "/api/modes/series/rename/proposals")
	if err != nil {
		t.Fatalf("list GET failed: %v", err)
	}
	defer listResp.Body.Close()
	var listed []proposals.Proposal
	json.NewDecoder(listResp.Body).Decode(&listed)
	if len(listed) != 1 || listed[0].ID != scanned[0].ID {
		t.Fatalf("expected the queue to reflect what scan just staged, got %+v", listed)
	}

	applyResp, err := http.Post(
		srv.URL+"/api/proposals/"+strconv.FormatInt(scanned[0].ID, 10)+"/apply", "application/json", nil)
	if err != nil {
		t.Fatalf("apply POST failed: %v", err)
	}
	defer applyResp.Body.Close()
	if applyResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 from apply, got %d", applyResp.StatusCode)
	}
	var applied proposals.Proposal
	if err := json.NewDecoder(applyResp.Body).Decode(&applied); err != nil {
		t.Fatalf("decoding apply response: %v", err)
	}
	if applied.Status != proposals.Applied || applied.TrackedID != 55 {
		t.Fatalf("expected the proposal to come back Applied with trackedId=55, got %+v", applied)
	}
}

// fakeTMDBSearchHandler serves TMDB's /search/movie endpoint with one
// canned result, for Movies' libStore-backed Rename path.
func fakeTMDBSearchHandler(t *testing.T, tmdbID int, title string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/search/movie" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"results": []map[string]any{
			{"id": tmdbID, "title": title},
		}})
	}
}

// TestRenameWorkflow_Movies_ScanThenApply_EndToEnd is Movies' own
// libStore-backed counterpart — no Radarr connection at all, a real
// temp-dir root folder, and a fake TMDB standing in for Servarr's Lookup.
func TestRenameWorkflow_Movies_ScanThenApply_EndToEnd(t *testing.T) {
	root := t.TempDir()
	orphanDir := filepath.Join(root, "A.Beautiful.Mind.2001.1080p.BluRay.x264-GROUP")
	if err := os.Mkdir(orphanDir, 0o755); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(orphanDir, "movie.mkv"), []byte("fake video data"), 0o644); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	fakeTMDB := httptest.NewServer(fakeTMDBSearchHandler(t, 453, "A Beautiful Mind"))
	defer fakeTMDB.Close()

	connStore, propStore, allowStore, settingsStore, grabsStore, libStore := testStores(t)
	ctx := context.Background()
	if err := connStore.Upsert(ctx, "tmdb", fakeTMDB.URL, "test-key"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := settingsStore.Set(ctx, moviesLibraryRootFolderKey, root); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	srv := httptest.NewServer(NewMux(testHTTPClient(), connStore, propStore, allowStore, testProber(t), settingsStore, grabsStore, libStore))
	defer srv.Close()

	scanResp, err := http.Post(srv.URL+"/api/modes/movies/rename/scan", "application/json", nil)
	if err != nil {
		t.Fatalf("scan POST failed: %v", err)
	}
	defer scanResp.Body.Close()
	if scanResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 from scan, got %d", scanResp.StatusCode)
	}
	var scanned []proposals.Proposal
	if err := json.NewDecoder(scanResp.Body).Decode(&scanned); err != nil {
		t.Fatalf("decoding scan response: %v", err)
	}
	if len(scanned) != 1 || scanned[0].Status != proposals.Pending || scanned[0].Title != "A Beautiful Mind" {
		t.Fatalf("unexpected scan result: %+v", scanned)
	}

	applyResp, err := http.Post(
		srv.URL+"/api/proposals/"+strconv.FormatInt(scanned[0].ID, 10)+"/apply", "application/json", nil)
	if err != nil {
		t.Fatalf("apply POST failed: %v", err)
	}
	defer applyResp.Body.Close()
	if applyResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 from apply, got %d", applyResp.StatusCode)
	}
	var applied proposals.Proposal
	if err := json.NewDecoder(applyResp.Body).Decode(&applied); err != nil {
		t.Fatalf("decoding apply response: %v", err)
	}
	if applied.Status != proposals.Applied || applied.TrackedID == 0 {
		t.Fatalf("expected the proposal to come back Applied with a nonzero library item id, got %+v", applied)
	}

	item, err := libStore.Get(ctx, int64(applied.TrackedID))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if item.TMDBID != 453 || item.Title != "A Beautiful Mind" {
		t.Errorf("unexpected library item: %+v", item)
	}
}

func TestDismissProposalHandler_EndToEnd(t *testing.T) {
	connStore, propStore, allowStore, settingsStore, grabsStore, libStore := testStores(t)
	saved, err := propStore.ReplacePending(context.Background(), "movies", proposals.Rename, []proposals.Proposal{
		{Status: proposals.Pending, SourceName: "x", SourcePath: "/x", RootFolderPath: "/media/Movies", Title: "X"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	srv := httptest.NewServer(NewMux(testHTTPClient(), connStore, propStore, allowStore, testProber(t), settingsStore, grabsStore, libStore))
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/api/proposals/"+strconv.FormatInt(saved[0].ID, 10)+"/dismiss", "application/json", nil)
	if err != nil {
		t.Fatalf("dismiss POST failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", resp.StatusCode)
	}

	got, err := propStore.Get(context.Background(), saved[0].ID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Status != proposals.Dismissed {
		t.Errorf("expected Dismissed, got %+v", got)
	}
}

func TestApplyProposalHandler_UnknownID(t *testing.T) {
	connStore, propStore, allowStore, settingsStore, grabsStore, libStore := testStores(t)
	srv := httptest.NewServer(NewMux(testHTTPClient(), connStore, propStore, allowStore, testProber(t), settingsStore, grabsStore, libStore))
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/api/proposals/999/apply", "application/json", nil)
	if err != nil {
		t.Fatalf("POST failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 for an unknown proposal id, got %d", resp.StatusCode)
	}
}

func TestScanHandler_ModeNotConfigured(t *testing.T) {
	connStore, propStore, allowStore, settingsStore, grabsStore, libStore := testStores(t)
	srv := httptest.NewServer(NewMux(testHTTPClient(), connStore, propStore, allowStore, testProber(t), settingsStore, grabsStore, libStore))
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/api/modes/series/rename/scan", "application/json", nil)
	if err != nil {
		t.Fatalf("POST failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 when sonarr isn't configured yet, got %d", resp.StatusCode)
	}
}

// TestScanHandler_Movies_RequiresTMDBConfigured confirms Movies' Scan fails
// with a clear 400 (not a crash) when TMDB isn't set up — there's no Radarr
// connection requirement to check anymore.
func TestScanHandler_Movies_RequiresTMDBConfigured(t *testing.T) {
	connStore, propStore, allowStore, settingsStore, grabsStore, libStore := testStores(t)
	if err := settingsStore.Set(context.Background(), moviesLibraryRootFolderKey, t.TempDir()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	srv := httptest.NewServer(NewMux(testHTTPClient(), connStore, propStore, allowStore, testProber(t), settingsStore, grabsStore, libStore))
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/api/modes/movies/rename/scan", "application/json", nil)
	if err != nil {
		t.Fatalf("POST failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("expected 502 when tmdb isn't configured yet (ScanLibrary's error surfaces the same way Scan's does), got %d", resp.StatusCode)
	}
}
