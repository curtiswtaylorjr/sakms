package dedup

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/curtiswtaylorjr/tidyarr/internal/mediainfo"
	"github.com/curtiswtaylorjr/tidyarr/internal/mode"
	"github.com/curtiswtaylorjr/tidyarr/internal/proposals"
	"github.com/curtiswtaylorjr/tidyarr/internal/servarr"
)

// fakeProber maps a video file path to a canned mediainfo.Probe result, so
// tests never need a real ffprobe binary.
type fakeProber struct {
	byPath map[string]*mediainfo.Probe
}

func (f *fakeProber) Probe(ctx context.Context, path string) (*mediainfo.Probe, error) {
	p, ok := f.byPath[path]
	if !ok {
		return nil, os.ErrNotExist
	}
	return p, nil
}

func newTestSession(t *testing.T, app servarr.App, handler http.HandlerFunc) *mode.Session {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	m := mode.Movies
	if app == servarr.Sonarr {
		m = mode.Series
	}
	return &mode.Session{
		Mode:    m,
		Servarr: servarr.New(servarr.Config{BaseURL: srv.URL, APIKey: "test-key", App: app}, srv.Client()),
	}
}

// writeVideoFile creates dir (if needed) and a dummy video file inside it,
// returning the file's full path.
func writeVideoFile(t *testing.T, dir, name string, size int) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, make([]byte, size), 0o644); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
	return path
}

func TestFindVideoFile_PathIsAlreadyAFile(t *testing.T) {
	dir := t.TempDir()
	f := writeVideoFile(t, dir, "movie.mkv", 100)

	got, err := findVideoFile(f)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != f {
		t.Errorf("expected %q, got %q", f, got)
	}
}

func TestFindVideoFile_DirectoryPicksLargestVideoFile(t *testing.T) {
	dir := t.TempDir()
	writeVideoFile(t, dir, "sample.mkv", 10)
	big := writeVideoFile(t, dir, "movie.mkv", 1000)
	writeVideoFile(t, dir, "poster.jpg", 5000) // bigger, but not a video extension

	got, err := findVideoFile(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != big {
		t.Errorf("expected the largest video file %q, got %q", big, got)
	}
}

func TestFindVideoFile_NoVideoFilesErrors(t *testing.T) {
	dir := t.TempDir()
	writeVideoFile(t, dir, "readme.txt", 10)

	if _, err := findVideoFile(dir); err == nil {
		t.Error("expected an error when no video file exists in the directory")
	}
}

func TestFindVideoFile_MissingPathErrors(t *testing.T) {
	if _, err := findVideoFile(filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Error("expected an error for a nonexistent path")
	}
}

func TestScan_RefusesSeries(t *testing.T) {
	sess := newTestSession(t, servarr.Sonarr, func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("Scan must not make any HTTP call for an unsupported app")
	})
	if _, err := Scan(context.Background(), sess, &fakeProber{}); err == nil {
		t.Fatal("expected Scan to refuse a Series (Sonarr) session")
	}
}

func TestScan_TrackedItemPlusOrphan_ProposesWithCorrectWinner(t *testing.T) {
	dir := t.TempDir()
	trackedDir := filepath.Join(dir, "Movies", "Some Movie (2020)")
	orphanDir := filepath.Join(dir, "Movies", "Some.Movie.2020.1080p.BluRay.x264-GROUP")
	trackedFile := writeVideoFile(t, trackedDir, "movie.mkv", 100)
	orphanFile := writeVideoFile(t, orphanDir, "movie.mkv", 100)

	sess := newTestSession(t, servarr.Radarr, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v3/rootfolder":
			w.Write([]byte(`[{"id":1,"path":"` + filepath.Join(dir, "Movies") + `","accessible":true,"freeSpace":1,"unmappedFolders":[
				{"name":"Some.Movie.2020.1080p.BluRay.x264-GROUP","path":"` + orphanDir + `","relativePath":"Some.Movie.2020.1080p.BluRay.x264-GROUP"}
			]}]`))
		case "/api/v3/movie":
			json.NewEncoder(w).Encode([]servarr.TrackedItem{
				{ID: 9, Title: "Some Movie", Path: trackedDir, RootFolderPath: filepath.Join(dir, "Movies"), TMDBID: 42, QualityProfileID: 4},
			})
		case "/api/v3/movie/lookup":
			w.Write([]byte(`[{"title":"Some Movie","year":2020,"tmdbId":42}]`))
		case "/api/v3/qualityprofile":
			w.Write([]byte(`[{"id":4,"name":"HD-1080p"}]`))
		default:
			t.Fatalf("unexpected request: %s", r.URL.Path)
		}
	})

	prober := &fakeProber{byPath: map[string]*mediainfo.Probe{
		trackedFile: {CodecName: "h264", Width: 1280, Height: 720, BitRate: 3000},
		orphanFile:  {CodecName: "h265", Width: 1920, Height: 1080, BitRate: 8000},
	}}

	got, err := Scan(context.Background(), sess, prober)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected exactly 1 duplicate group, got %d: %+v", len(got), got)
	}
	p := got[0]
	if p.Status != proposals.Pending || p.TMDBID != 42 || len(p.Candidates) != 2 {
		t.Fatalf("unexpected proposal: %+v", p)
	}

	var winner, loser proposals.Candidate
	for _, c := range p.Candidates {
		if c.Winner {
			winner = c
		} else {
			loser = c
		}
	}
	if winner.Path != orphanFile {
		t.Errorf("expected the higher-resolution orphan to win, got winner=%+v", winner)
	}
	if loser.Path != trackedFile || loser.TrackedID != 9 {
		t.Errorf("expected the tracked file to be the loser, got %+v", loser)
	}
}

func TestScan_SingleNewOrphanIsNotADuplicate(t *testing.T) {
	dir := t.TempDir()
	orphanDir := filepath.Join(dir, "Movies", "New.Movie.2020")
	writeVideoFile(t, orphanDir, "movie.mkv", 100)

	sess := newTestSession(t, servarr.Radarr, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v3/rootfolder":
			w.Write([]byte(`[{"id":1,"path":"` + filepath.Join(dir, "Movies") + `","accessible":true,"freeSpace":1,"unmappedFolders":[
				{"name":"New.Movie.2020","path":"` + orphanDir + `","relativePath":"New.Movie.2020"}
			]}]`))
		case "/api/v3/movie":
			w.Write([]byte(`[]`))
		case "/api/v3/movie/lookup":
			w.Write([]byte(`[{"title":"New Movie","year":2020,"tmdbId":99}]`))
		case "/api/v3/qualityprofile":
			w.Write([]byte(`[]`))
		default:
			t.Fatalf("unexpected request: %s", r.URL.Path)
		}
	})

	got, err := Scan(context.Background(), sess, &fakeProber{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected no duplicate groups for a single new item, got %+v", got)
	}
}

func TestApply_KeepsWinnerByDefault_DeletesOrphanLoser(t *testing.T) {
	dir := t.TempDir()
	loserPath := writeVideoFile(t, dir, "loser.mkv", 10)

	sess := newTestSession(t, servarr.Radarr, func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
	})

	p := proposals.Proposal{
		ID: 1, Status: proposals.Pending, Title: "X", TMDBID: 1,
		Candidates: []proposals.Candidate{
			{Label: "winner", Path: "/winner.mkv", TrackedID: 9, Winner: true},
			{Label: "loser", Path: loserPath},
		},
	}
	id, err := Apply(context.Background(), sess, p, nil, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != 9 {
		t.Errorf("expected the already-tracked winner's id (9), got %d", id)
	}
	if _, err := os.Stat(loserPath); !os.IsNotExist(err) {
		t.Error("expected the losing orphan file to be deleted")
	}
}

func TestApply_WinnerIsOrphan_DeletesTrackedLoserAndRegistersWinner(t *testing.T) {
	dir := t.TempDir()
	winnerPath := writeVideoFile(t, dir, "winner.mkv", 10)

	var deletedTrackedID int
	var addedBody map[string]any
	var scanTriggered bool
	sess := newTestSession(t, servarr.Radarr, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodDelete && r.URL.Path == "/api/v3/movie/9":
			deletedTrackedID = 9
		case r.Method == http.MethodPost && r.URL.Path == "/api/v3/movie":
			json.NewDecoder(r.Body).Decode(&addedBody)
			w.Write([]byte(`{"id":55}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v3/command":
			scanTriggered = true
			w.Write([]byte(`{}`))
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	})

	p := proposals.Proposal{
		ID: 1, Status: proposals.Pending, Title: "Some Movie", TMDBID: 42,
		RootFolderPath: "/media/Movies", QualityProfileID: 4,
		Candidates: []proposals.Candidate{
			{Label: "tracked", Path: "/tracked.mkv", TrackedID: 9},
			{Label: "winner", Path: winnerPath, Winner: true},
		},
	}
	id, err := Apply(context.Background(), sess, p, nil, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != 55 {
		t.Errorf("expected the newly registered id (55), got %d", id)
	}
	if deletedTrackedID != 9 {
		t.Error("expected the losing tracked item to be deleted")
	}
	if addedBody["tmdbId"] != float64(42) || addedBody["title"] != "Some Movie" {
		t.Errorf("unexpected Add request body: %+v", addedBody)
	}
	if !scanTriggered {
		t.Error("expected a downloaded-files scan to be triggered after registering the winner")
	}
}

func TestApply_KeepAll_NoMutation(t *testing.T) {
	sess := newTestSession(t, servarr.Radarr, func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("keepAll must not make any HTTP or filesystem mutation")
	})

	p := proposals.Proposal{
		ID: 1, Status: proposals.Pending,
		Candidates: []proposals.Candidate{
			{Label: "a", Path: "/a.mkv", TrackedID: 9},
			{Label: "b", Path: "/b.mkv"},
		},
	}
	id, err := Apply(context.Background(), sess, p, nil, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != 9 {
		t.Errorf("expected keepAll to still report the existing tracked id, got %d", id)
	}
}

func TestApply_ExplicitKeepIndexOverridesWinner(t *testing.T) {
	dir := t.TempDir()
	loserPath := writeVideoFile(t, dir, "b.mkv", 10)

	sess := newTestSession(t, servarr.Radarr, func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("keeping the already-tracked candidate should need no HTTP call")
	})

	p := proposals.Proposal{
		ID: 1, Status: proposals.Pending,
		Candidates: []proposals.Candidate{
			{Label: "a", Path: "/a.mkv", TrackedID: 9},
			{Label: "b", Path: loserPath, Winner: true}, // Scan's pick, overridden below
		},
	}
	keepA := 0
	id, err := Apply(context.Background(), sess, p, &keepA, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != 9 {
		t.Errorf("expected the explicitly kept candidate's tracked id (9), got %d", id)
	}
	if _, err := os.Stat(loserPath); !os.IsNotExist(err) {
		t.Error("expected the explicitly-not-kept file to be deleted")
	}
}

func TestApply_RejectsNonPendingProposal(t *testing.T) {
	sess := newTestSession(t, servarr.Radarr, func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("Apply must not make any HTTP call for a non-pending proposal")
	})
	p := proposals.Proposal{
		Status:     proposals.Applied,
		Candidates: []proposals.Candidate{{Path: "/a.mkv"}, {Path: "/b.mkv"}},
	}
	if _, err := Apply(context.Background(), sess, p, nil, false); err == nil {
		t.Fatal("expected Apply to refuse an already-applied proposal")
	}
}

func TestApply_RejectsFewerThanTwoCandidates(t *testing.T) {
	sess := newTestSession(t, servarr.Radarr, func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("Apply must not make any HTTP call with too few candidates")
	})
	p := proposals.Proposal{Status: proposals.Pending, Candidates: []proposals.Candidate{{Path: "/a.mkv"}}}
	if _, err := Apply(context.Background(), sess, p, nil, false); err == nil {
		t.Fatal("expected Apply to refuse a proposal with fewer than 2 candidates")
	}
}

func TestApply_RejectsOutOfRangeKeepIndex(t *testing.T) {
	sess := newTestSession(t, servarr.Radarr, func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("Apply must not make any HTTP call for an invalid keepIndex")
	})
	p := proposals.Proposal{
		Status:     proposals.Pending,
		Candidates: []proposals.Candidate{{Path: "/a.mkv"}, {Path: "/b.mkv"}},
	}
	bad := 5
	if _, err := Apply(context.Background(), sess, p, &bad, false); err == nil {
		t.Fatal("expected Apply to refuse an out-of-range keepIndex")
	}
}
