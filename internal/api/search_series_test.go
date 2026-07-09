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

	"github.com/curtiswtaylorjr/sakms/internal/grabs"
	"github.com/curtiswtaylorjr/sakms/internal/library"
	"github.com/curtiswtaylorjr/sakms/internal/mode"
)

// TestCheckImportHandler_Series_SingleEpisode_PerformsImport mirrors
// TestCheckImportHandler_QBittorrentCompleted_PerformsImport but for a
// single-episode Series grab — no Sonarr involved anywhere.
func TestCheckImportHandler_Series_SingleEpisode_PerformsImport(t *testing.T) {
	dir := t.TempDir()
	downloadDir := filepath.Join(dir, "downloads", "Some.Show.S01E01.1080p.WEB-DL.x264-GROUP")
	tvRoot := filepath.Join(dir, "TV")
	if err := os.MkdirAll(downloadDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.MkdirAll(tvRoot, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(downloadDir, "episode.mkv"), []byte("fake video"), 0o644); err != nil {
		t.Fatalf("writing file: %v", err)
	}

	fakeQB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/auth/login":
			http.SetCookie(w, &http.Cookie{Name: "SID", Value: "test-sid"})
			w.Write([]byte("Ok."))
		case "/api/v2/torrents/info":
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`[{"hash":"abc123","state":"uploading","progress":1,"content_path":"` + downloadDir + `"}]`))
		default:
			t.Fatalf("unexpected request: %s", r.URL.Path)
		}
	}))
	defer fakeQB.Close()

	connStore, propStore, allowStore, settingsStore, grabsStore, libStore := testStores(t)
	ctx := context.Background()
	if err := connStore.UpsertWithUsername(ctx, "qbittorrent", fakeQB.URL, "wade", "hunter2"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	g, err := grabsStore.Create(ctx, grabs.Grab{
		Mode: mode.Series, Title: "Some Show", TMDBID: 555, SeasonNumber: 1, EpisodeNumber: 1,
		Indexer: "I", Protocol: "torrent", DownloadClient: "qbittorrent",
		ClientRef: "abc123", RootFolderPath: tvRoot,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	srv := httptest.NewServer(NewMux(testHTTPClient(), connStore, propStore, allowStore, testProber(t), settingsStore, grabsStore, libStore))
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/api/grabs/"+strconv.FormatInt(g.ID, 10)+"/check-import", "application/json", nil)
	if err != nil {
		t.Fatalf("POST failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var updated grabs.Grab
	if err := json.NewDecoder(resp.Body).Decode(&updated); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if updated.Status != grabs.Imported {
		t.Errorf("expected status Imported, got %q", updated.Status)
	}

	series, err := libStore.GetSeriesByTMDBID(ctx, 555)
	if err != nil {
		t.Fatalf("expected the series to be recorded, got err=%v", err)
	}
	episodes, err := libStore.ListEpisodes(ctx, series.ID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(episodes) != 1 || episodes[0].SeasonNumber != 1 || episodes[0].EpisodeNumber != 1 || episodes[0].FilePath == "" {
		t.Fatalf("unexpected episodes: %+v", episodes)
	}
}

// TestCheckImportHandler_Series_SeasonPack_PerformsImport proves a
// season-pack grab (a directory containing multiple episode files) records
// one episode row per file, not just one for the whole pack.
func TestCheckImportHandler_Series_SeasonPack_PerformsImport(t *testing.T) {
	dir := t.TempDir()
	downloadDir := filepath.Join(dir, "downloads", "Some.Show.S01.1080p.WEB-DL.x264-GROUP")
	tvRoot := filepath.Join(dir, "TV")
	if err := os.MkdirAll(downloadDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.MkdirAll(tvRoot, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	for _, name := range []string{"Some.Show.S01E01.mkv", "Some.Show.S01E02.mkv"} {
		if err := os.WriteFile(filepath.Join(downloadDir, name), []byte("fake video"), 0o644); err != nil {
			t.Fatalf("writing %s: %v", name, err)
		}
	}

	fakeQB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/auth/login":
			http.SetCookie(w, &http.Cookie{Name: "SID", Value: "test-sid"})
			w.Write([]byte("Ok."))
		case "/api/v2/torrents/info":
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`[{"hash":"def456","state":"uploading","progress":1,"content_path":"` + downloadDir + `"}]`))
		default:
			t.Fatalf("unexpected request: %s", r.URL.Path)
		}
	}))
	defer fakeQB.Close()

	connStore, propStore, allowStore, settingsStore, grabsStore, libStore := testStores(t)
	ctx := context.Background()
	if err := connStore.UpsertWithUsername(ctx, "qbittorrent", fakeQB.URL, "wade", "hunter2"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Season-pack grab: SeasonNumber set, EpisodeNumber left 0.
	g, err := grabsStore.Create(ctx, grabs.Grab{
		Mode: mode.Series, Title: "Some Show", TMDBID: 555, SeasonNumber: 1,
		Indexer: "I", Protocol: "torrent", DownloadClient: "qbittorrent",
		ClientRef: "def456", RootFolderPath: tvRoot,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	srv := httptest.NewServer(NewMux(testHTTPClient(), connStore, propStore, allowStore, testProber(t), settingsStore, grabsStore, libStore))
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/api/grabs/"+strconv.FormatInt(g.ID, 10)+"/check-import", "application/json", nil)
	if err != nil {
		t.Fatalf("POST failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var updated grabs.Grab
	if err := json.NewDecoder(resp.Body).Decode(&updated); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if updated.Status != grabs.Imported {
		t.Errorf("expected status Imported, got %q", updated.Status)
	}

	series, err := libStore.GetSeriesByTMDBID(ctx, 555)
	if err != nil {
		t.Fatalf("expected the series to be recorded, got err=%v", err)
	}
	episodes, err := libStore.ListEpisodes(ctx, series.ID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(episodes) != 2 {
		t.Fatalf("expected one episode row per file in the season pack, got %+v", episodes)
	}
	byEpisode := map[int]library.Episode{}
	for _, ep := range episodes {
		byEpisode[ep.EpisodeNumber] = ep
	}
	if byEpisode[1].FilePath == "" || byEpisode[2].FilePath == "" {
		t.Fatalf("expected both episode files resolved, got %+v", episodes)
	}
}
