package sonarrimport

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/curtiswtaylorjr/sakms/internal/db"
	"github.com/curtiswtaylorjr/sakms/internal/library"
	"github.com/curtiswtaylorjr/sakms/internal/servarr"
	"github.com/curtiswtaylorjr/sakms/internal/tmdb"
)

func newTestLibStore(t *testing.T) *library.Store {
	t.Helper()
	sqlDB, err := db.Open(filepath.Join(t.TempDir(), "sakms.db"))
	if err != nil {
		t.Fatalf("opening db: %v", err)
	}
	t.Cleanup(func() { sqlDB.Close() })
	return library.New(sqlDB)
}

func newFakeSonarr(t *testing.T, seriesJSON string) *servarr.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v3/series" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(seriesJSON))
	}))
	t.Cleanup(srv.Close)
	return servarr.New(servarr.Config{BaseURL: srv.URL, APIKey: "test-key", App: servarr.Sonarr}, srv.Client())
}

func newFakeTMDB(t *testing.T, handler http.HandlerFunc) *tmdb.Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return tmdb.New(tmdb.Config{BaseURL: srv.URL, APIKey: "test-key"}, srv.Client())
}

func TestImport_FoundAndMissingEpisodesRecorded(t *testing.T) {
	root := t.TempDir()
	showDir := filepath.Join(root, "Show A")
	seasonDir := filepath.Join(showDir, "Season 01")
	if err := os.MkdirAll(seasonDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(seasonDir, "Show A - S01E01.mkv"), []byte("x"), 0o644); err != nil {
		t.Fatalf("writing episode file: %v", err)
	}
	// Episode 2 exists in TMDB's season details but has no file on disk —
	// this is the "real missing episode" case.

	sonarrJSON := `[{"id":1,"title":"Show A","path":"` + showDir + `","rootFolderPath":"` + root + `","tvdbId":111}]`
	sonarr := newFakeSonarr(t, sonarrJSON)

	tmdbClient := newFakeTMDB(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/find/111":
			w.Write([]byte(`{"tv_results": [{"id": 555, "name": "Show A"}], "movie_results": []}`))
		case "/tv/555/season/1":
			w.Write([]byte(`{"episodes": [
			  {"episode_number": 1, "name": "Pilot", "air_date": "2020-01-01"},
			  {"episode_number": 2, "name": "Second", "air_date": "2020-01-08"}
			]}`))
		default:
			t.Errorf("unexpected TMDB path: %s", r.URL.Path)
		}
	})

	libStore := newTestLibStore(t)
	result, err := Import(context.Background(), sonarr, tmdbClient, libStore)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Series) != 1 {
		t.Fatalf("expected 1 series result, got %d", len(result.Series))
	}
	sr := result.Series[0]
	if !sr.Imported {
		t.Fatalf("expected series to be imported, got reason: %q", sr.Reason)
	}
	if sr.EpisodesFound != 1 || sr.EpisodesMissing != 1 {
		t.Fatalf("expected 1 found + 1 missing, got %+v", sr)
	}

	ctx := context.Background()
	series, err := libStore.GetSeriesByTMDBID(ctx, 555)
	if err != nil {
		t.Fatalf("expected series to be recorded, got error: %v", err)
	}
	if series.TVDBID != 111 || series.Title != "Show A" {
		t.Errorf("unexpected series row: %+v", series)
	}

	missing, err := libStore.MissingEpisodes(ctx, series.ID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(missing) != 1 || missing[0].EpisodeNumber != 2 {
		t.Fatalf("expected episode 2 missing, got %+v", missing)
	}

	all, err := libStore.ListEpisodes(ctx, series.ID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("expected 2 episode rows total, got %d", len(all))
	}
}

func TestImport_TVDBIDDoesNotResolve_SkipsWithReason(t *testing.T) {
	root := t.TempDir()
	showDir := filepath.Join(root, "Unresolvable Show")
	if err := os.MkdirAll(showDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	sonarrJSON := `[{"id":2,"title":"Unresolvable Show","path":"` + showDir + `","rootFolderPath":"` + root + `","tvdbId":999999}]`
	sonarr := newFakeSonarr(t, sonarrJSON)

	tmdbClient := newFakeTMDB(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"tv_results": [], "movie_results": []}`))
	})

	libStore := newTestLibStore(t)
	result, err := Import(context.Background(), sonarr, tmdbClient, libStore)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Series) != 1 {
		t.Fatalf("expected 1 series result, got %d", len(result.Series))
	}
	sr := result.Series[0]
	if sr.Imported {
		t.Fatal("expected Imported=false when the TVDB id doesn't resolve")
	}
	if sr.Reason == "" {
		t.Error("expected a non-empty skip reason")
	}

	if _, err := libStore.ListSeries(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	all, err := libStore.ListSeries(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(all) != 0 {
		t.Errorf("expected no series recorded when resolution fails, got %v", all)
	}
}

func TestImport_MultipleSeriesIndependentFailures(t *testing.T) {
	root := t.TempDir()
	goodDir := filepath.Join(root, "Good Show")
	if err := os.MkdirAll(goodDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(goodDir, "Good Show - S01E01.mkv"), []byte("x"), 0o644); err != nil {
		t.Fatalf("writing file: %v", err)
	}

	sonarrJSON := `[
		{"id":1,"title":"Good Show","path":"` + goodDir + `","rootFolderPath":"` + root + `","tvdbId":1},
		{"id":2,"title":"Bad Show","path":"` + filepath.Join(root, "Bad Show") + `","rootFolderPath":"` + root + `","tvdbId":2}
	]`
	sonarr := newFakeSonarr(t, sonarrJSON)

	tmdbClient := newFakeTMDB(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/find/1":
			w.Write([]byte(`{"tv_results": [{"id": 10, "name": "Good Show"}], "movie_results": []}`))
		case "/find/2":
			w.Write([]byte(`{"tv_results": [], "movie_results": []}`))
		case "/tv/10/season/1":
			w.Write([]byte(`{"episodes": [{"episode_number": 1, "name": "Pilot", "air_date": "2020-01-01"}]}`))
		default:
			t.Errorf("unexpected TMDB path: %s", r.URL.Path)
		}
	})

	libStore := newTestLibStore(t)
	result, err := Import(context.Background(), sonarr, tmdbClient, libStore)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Series) != 2 {
		t.Fatalf("expected 2 series results, got %d", len(result.Series))
	}
	byTitle := map[string]SeriesResult{}
	for _, sr := range result.Series {
		byTitle[sr.Title] = sr
	}
	if !byTitle["Good Show"].Imported {
		t.Errorf("expected Good Show to import despite Bad Show failing, got %+v", byTitle["Good Show"])
	}
	if byTitle["Bad Show"].Imported {
		t.Errorf("expected Bad Show to fail independently, got %+v", byTitle["Bad Show"])
	}
}
