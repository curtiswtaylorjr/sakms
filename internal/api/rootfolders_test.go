package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestListRootFolders_Adult_ReturnsPathsFromTheRealApp proves the
// settings-UI contract still works for Adult (Whisparr) — the only mode
// left on this *arr-backed path now that Movies and Series both own their
// own library.
func TestListRootFolders_Adult_ReturnsPathsFromTheRealApp(t *testing.T) {
	fakeWhisparr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v3/rootfolder" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[{"id":1,"path":"/media/Adult","accessible":true,"freeSpace":1,"unmappedFolders":[]}]`))
	}))
	defer fakeWhisparr.Close()

	connStore, propStore, allowStore, settingsStore, grabsStore, libStore := testStores(t)
	ctx := context.Background()
	if err := connStore.Upsert(ctx, "whisparr", fakeWhisparr.URL, "test-key"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	srv := httptest.NewServer(NewMux(testHTTPClient(), connStore, propStore, allowStore, testProber(t), settingsStore, grabsStore, libStore))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/modes/adult/root-folders")
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
	if len(got) != 1 || got[0].Path != "/media/Adult" {
		t.Errorf("unexpected root folders: %+v", got)
	}
}

// TestListRootFolders_NotApplicableToMoviesOrSeries confirms both modes get
// a clear 400 instead of a nil-Servarr crash — neither has a *arr app to
// ask anymore (see GET /api/modes/{mode}/library/root-folder instead).
func TestListRootFolders_NotApplicableToMoviesOrSeries(t *testing.T) {
	connStore, propStore, allowStore, settingsStore, grabsStore, libStore := testStores(t)
	srv := httptest.NewServer(NewMux(testHTTPClient(), connStore, propStore, allowStore, testProber(t), settingsStore, grabsStore, libStore))
	defer srv.Close()

	for _, m := range []string{"movies", "series"} {
		resp, err := http.Get(srv.URL + "/api/modes/" + m + "/root-folders")
		if err != nil {
			t.Fatalf("GET failed: %v", err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("expected 400 for %s, got %d", m, resp.StatusCode)
		}
	}
}
