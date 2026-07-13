package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/curtiswtaylorjr/sakms/internal/apidto"
	"github.com/curtiswtaylorjr/sakms/internal/mode"
	"github.com/curtiswtaylorjr/sakms/internal/quality"
)

// fakeTMDBMovieRuntime serves /movie/{id} with a real runtime — the autograb
// Movies path needs it as the bitrate scorer's denominator (fakeTMDBServer in
// availability_test.go omits runtime, which would force every candidate to
// unknown-bitrate).
func fakeTMDBMovieRuntime(t *testing.T, runtimeMinutes int) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/movie/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"id": 42, "title": "Some Movie", "imdb_id": "tt1234567", "runtime": runtimeMinutes,
		})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// TestAutoGrabHandler_Movies_QualifiedGrabsExactlyOne is the qualified path:
// a healthy, high-bitrate release clears the floor, so auto-grab sends it to
// qBittorrent (exactly once — the backend no-bulk proof) and records exactly
// one grab. No manual release-pick happens; that is the whole point.
func TestAutoGrabHandler_Movies_QualifiedGrabsExactlyOne(t *testing.T) {
	var qbAdds int32
	fakeQB := fakeQBittorrent(t, func(r *http.Request) { atomic.AddInt32(&qbAdds, 1) })
	tmdbSrv := fakeTMDBMovieRuntime(t, 100) // 100 min = 6000 s
	// 8 GB / 6000 s ≈ 10.7 Mbps implied; x265 → ~21 Mbps x264-equiv; clears
	// every 1080p tier floor even after the 25% non-AV1 padding.
	prowlarr := fakeProwlarr(t, `[{"guid":"1","title":"Some.Movie.2023.1080p.WEB-DL.x265-GROUP","indexer":"I","protocol":"torrent","size":8000000000,"seeders":50,"downloadUrl":"magnet:?xt=urn:btih:ABCDEF1234567890abcdef1234567890abcdef12"}]`)

	connStore, propStore, allowStore, settingsStore, grabsStore, libStore := testStores(t)
	ctx := context.Background()
	if err := connStore.Upsert(ctx, "tmdb", tmdbSrv.URL, "key"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := connStore.Upsert(ctx, "prowlarr", prowlarr.URL, "key"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := connStore.UpsertWithUsername(ctx, "qbittorrent", fakeQB.URL, "wade", "hunter2"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := settingsStore.Set(ctx, qualityTierKey(mode.Movies), string(quality.Low)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := settingsStore.Set(ctx, moviesLibraryRootFolderKey, "/movies"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	srv := httptest.NewServer(NewMux(testHTTPClient(), connStore, propStore, allowStore, testProber(t), testPHasher(t), testVideoHasher(t), settingsStore, grabsStore, libStore))
	defer srv.Close()

	body, _ := json.Marshal(apidto.AutoGrabRequest{Title: "Some Movie", TMDBID: 42})
	resp, err := http.Post(srv.URL+"/api/modes/movies/autograb", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var out apidto.AutoGrabResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if !out.Grabbed || out.Fallback || out.Grab == nil {
		t.Fatalf("expected a qualified grab, got %+v", out)
	}
	if out.Grab.DownloadClient != "qbittorrent" || out.Grab.RootFolderPath != "/movies" {
		t.Errorf("unexpected grab: %+v", out.Grab)
	}
	if got := atomic.LoadInt32(&qbAdds); got != 1 {
		t.Errorf("expected exactly ONE download-client add (no bulk), got %d", got)
	}
	list, err := grabsStore.List(ctx, mode.Movies)
	if err != nil {
		t.Fatalf("listing grabs: %v", err)
	}
	if len(list) != 1 {
		t.Errorf("expected exactly one recorded grab, got %d", len(list))
	}
}

// TestAutoGrabHandler_Movies_FallbackGrabsNothing is the fallback path: a
// tiny, mislabeled-looking release clears nothing, so auto-grab must NOT touch
// the download client and must return the ranked manual pick list instead of
// grabbing the least-bad option.
func TestAutoGrabHandler_Movies_FallbackGrabsNothing(t *testing.T) {
	var qbAdds int32
	fakeQB := fakeQBittorrent(t, func(r *http.Request) { atomic.AddInt32(&qbAdds, 1) })
	tmdbSrv := fakeTMDBMovieRuntime(t, 100)
	// size:1 → an absurdly low implied bitrate for a "1080p" release → the
	// pre-grab mislabel check excludes it; nothing qualifies.
	prowlarr := fakeProwlarr(t, `[{"guid":"1","title":"Some.Movie.2023.1080p.WEB-DL.x265-GROUP","indexer":"BadIndexer","protocol":"torrent","size":1,"seeders":50,"downloadUrl":"magnet:?xt=urn:btih:ABCDEF1234567890abcdef1234567890abcdef12"}]`)

	connStore, propStore, allowStore, settingsStore, grabsStore, libStore := testStores(t)
	ctx := context.Background()
	if err := connStore.Upsert(ctx, "tmdb", tmdbSrv.URL, "key"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := connStore.Upsert(ctx, "prowlarr", prowlarr.URL, "key"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := connStore.UpsertWithUsername(ctx, "qbittorrent", fakeQB.URL, "wade", "hunter2"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := settingsStore.Set(ctx, moviesLibraryRootFolderKey, "/movies"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	srv := httptest.NewServer(NewMux(testHTTPClient(), connStore, propStore, allowStore, testProber(t), testPHasher(t), testVideoHasher(t), settingsStore, grabsStore, libStore))
	defer srv.Close()

	body, _ := json.Marshal(apidto.AutoGrabRequest{Title: "Some Movie", TMDBID: 42})
	resp, err := http.Post(srv.URL+"/api/modes/movies/autograb", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var out apidto.AutoGrabResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if out.Grabbed || !out.Fallback || out.Grab != nil {
		t.Fatalf("expected a fallback (no auto-grab), got %+v", out)
	}
	if len(out.Candidates) != 1 || out.Candidates[0].Qualified {
		t.Errorf("expected one non-qualified candidate in the pick list, got %+v", out.Candidates)
	}
	if got := atomic.LoadInt32(&qbAdds); got != 0 {
		t.Errorf("expected ZERO download-client adds on fallback, got %d", got)
	}
	list, err := grabsStore.List(ctx, mode.Movies)
	if err != nil {
		t.Fatalf("listing grabs: %v", err)
	}
	if len(list) != 0 {
		t.Errorf("expected zero recorded grabs on fallback, got %d", len(list))
	}
}

// TestAutoGrabHandler_Series_PickerGatedFallback proves Series always lands in
// the manual pick list today (no per-episode runtime pre-grab → every
// candidate is unknown-bitrate → neutral → nothing qualifies), and that the
// season/episode selection is carried on the request. It also confirms no
// download-client call fires without a qualifying release.
func TestAutoGrabHandler_Series_PickerGatedFallback(t *testing.T) {
	var qbAdds int32
	fakeQB := fakeQBittorrent(t, func(r *http.Request) { atomic.AddInt32(&qbAdds, 1) })
	tmdbSrv := fakeTMDBServer(t) // /tv/{id}/external_ids → tvdb_id
	prowlarr := fakeProwlarr(t, `[{"guid":"1","title":"Some.Show.S03E05.1080p.WEB-DL.x265-GROUP","indexer":"I","protocol":"torrent","size":900000000,"seeders":50,"downloadUrl":"magnet:?xt=urn:btih:ABCDEF1234567890abcdef1234567890abcdef12"}]`)

	connStore, propStore, allowStore, settingsStore, grabsStore, libStore := testStores(t)
	ctx := context.Background()
	if err := connStore.Upsert(ctx, "tmdb", tmdbSrv.URL, "key"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := connStore.Upsert(ctx, "prowlarr", prowlarr.URL, "key"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := connStore.UpsertWithUsername(ctx, "qbittorrent", fakeQB.URL, "wade", "hunter2"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	srv := httptest.NewServer(NewMux(testHTTPClient(), connStore, propStore, allowStore, testProber(t), testPHasher(t), testVideoHasher(t), settingsStore, grabsStore, libStore))
	defer srv.Close()

	body, _ := json.Marshal(apidto.AutoGrabRequest{Title: "Some Show", TMDBID: 100, SeasonNumber: 3, EpisodeNumber: 5, SeasonSpecified: true})
	resp, err := http.Post(srv.URL+"/api/modes/series/autograb", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var out apidto.AutoGrabResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if !out.Fallback || len(out.Candidates) != 1 {
		t.Fatalf("expected Series to fall back to a one-item pick list, got %+v", out)
	}
	if out.Candidates[0].Status != "unknown-bitrate" {
		t.Errorf("expected unknown-bitrate status (no pre-grab runtime), got %q", out.Candidates[0].Status)
	}
	if got := atomic.LoadInt32(&qbAdds); got != 0 {
		t.Errorf("expected zero download-client adds for a Series fallback, got %d", got)
	}
}
