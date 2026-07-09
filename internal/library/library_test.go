package library

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/curtiswtaylorjr/sakms/internal/db"
	"github.com/curtiswtaylorjr/sakms/internal/mode"
)

// newTestStore builds a Store against a real, freshly migrated SQLite file —
// exercising the actual SQL, not a mock, matching every other store test in
// this repo.
func newTestStore(t *testing.T) *Store {
	t.Helper()
	sqlDB, err := db.Open(filepath.Join(t.TempDir(), "sakms.db"))
	if err != nil {
		t.Fatalf("opening db: %v", err)
	}
	t.Cleanup(func() { sqlDB.Close() })
	return New(sqlDB)
}

func TestUpsert_CreatesThenUpdatesInPlace(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	created, err := s.Upsert(ctx, Item{
		Mode: mode.Movies, TMDBID: 100, Title: "Some Movie", Year: 2020,
		FilePath: "/movies/Some Movie/movie.mkv", RootFolderPath: "/movies",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if created.ID == 0 || created.CreatedAt == "" || created.UpdatedAt == "" {
		t.Fatalf("expected id/timestamps populated, got %+v", created)
	}

	updated, err := s.Upsert(ctx, Item{
		Mode: mode.Movies, TMDBID: 100, Title: "Some Movie (Updated)", Year: 2020,
		FilePath: "/movies/Some Movie (2020)/movie.mkv", RootFolderPath: "/movies",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if updated.ID != created.ID {
		t.Errorf("expected the same row to be updated (id %d), got id %d", created.ID, updated.ID)
	}
	if updated.Title != "Some Movie (Updated)" {
		t.Errorf("expected title to be updated, got %q", updated.Title)
	}

	all, err := s.List(ctx, mode.Movies)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("expected upsert to replace, not duplicate — got %d rows", len(all))
	}
}

func TestGetByTMDBID_ScopedByMode(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if _, err := s.Upsert(ctx, Item{Mode: mode.Movies, TMDBID: 200, Title: "Movie", RootFolderPath: "/movies"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, err := s.GetByTMDBID(ctx, mode.Movies, 200); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := s.GetByTMDBID(ctx, mode.Series, 200); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound for a different mode with the same tmdb id, got %v", err)
	}
}

func TestGet_NotFound(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.Get(context.Background(), 999); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestList_EmptyIsNotNil(t *testing.T) {
	s := newTestStore(t)
	got, err := s.List(context.Background(), mode.Movies)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got == nil {
		t.Error("expected an empty slice, not nil, so it serializes as [] not null")
	}
}

func TestDelete_CascadesTags(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	item, err := s.Upsert(ctx, Item{Mode: mode.Movies, TMDBID: 300, Title: "Movie", RootFolderPath: "/movies"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := s.AddTag(ctx, item.ID, "kids"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if err := s.Delete(ctx, item.ID); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	tags, err := s.Tags(ctx, item.ID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tags) != 0 {
		t.Errorf("expected tags to cascade-delete with the item, got %v", tags)
	}
}

func TestDelete_NotFoundIsNotAnError(t *testing.T) {
	s := newTestStore(t)
	if err := s.Delete(context.Background(), 999); err != nil {
		t.Fatalf("expected deleting a nonexistent id to be a no-op, got %v", err)
	}
}

func TestTags_AddIsIdempotentAndRemoveWorks(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	item, err := s.Upsert(ctx, Item{Mode: mode.Movies, TMDBID: 400, Title: "Movie", RootFolderPath: "/movies"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if err := s.AddTag(ctx, item.ID, "favorite"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := s.AddTag(ctx, item.ID, "favorite"); err != nil {
		t.Fatalf("adding the same tag twice should be a no-op, got error: %v", err)
	}

	tags, err := s.Tags(ctx, item.ID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tags) != 1 || tags[0] != "favorite" {
		t.Fatalf("expected exactly one tag, got %v", tags)
	}

	if err := s.RemoveTag(ctx, item.ID, "favorite"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := s.RemoveTag(ctx, item.ID, "not-there"); err != nil {
		t.Fatalf("removing a tag that isn't assigned should be a no-op, got error: %v", err)
	}

	tags, err = s.Tags(ctx, item.ID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tags) != 0 {
		t.Errorf("expected no tags after removal, got %v", tags)
	}
}

func TestTagVocabulary_DistinctAcrossItemsScopedByMode(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	movieA, err := s.Upsert(ctx, Item{Mode: mode.Movies, TMDBID: 500, Title: "A", RootFolderPath: "/movies"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	movieB, err := s.Upsert(ctx, Item{Mode: mode.Movies, TMDBID: 501, Title: "B", RootFolderPath: "/movies"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	seriesA, err := s.Upsert(ctx, Item{Mode: mode.Series, TMDBID: 502, Title: "C", RootFolderPath: "/tv"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if err := s.AddTag(ctx, movieA.ID, "kids"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := s.AddTag(ctx, movieB.ID, "kids"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := s.AddTag(ctx, movieB.ID, "favorite"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := s.AddTag(ctx, seriesA.ID, "documentary"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	vocab, err := s.TagVocabulary(ctx, mode.Movies)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(vocab) != 2 || vocab[0] != "favorite" || vocab[1] != "kids" {
		t.Fatalf("expected [favorite kids], got %v", vocab)
	}
}

func TestScanRootFolder_SkipsKnownAndSidecarFiles(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"New Movie (2024)", "Already Tracked Movie"} {
		if err := os.Mkdir(filepath.Join(dir, name), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", name, err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "poster.jpg"), []byte("x"), 0o644); err != nil {
		t.Fatalf("writing poster.jpg: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "subs.srt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("writing subs.srt: %v", err)
	}

	known := map[string]bool{filepath.Join(dir, "Already Tracked Movie"): true}
	entries, err := ScanRootFolder(dir, known)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(entries) != 1 || entries[0].Name != "New Movie (2024)" {
		t.Fatalf("expected only the one genuinely unmapped entry, got %+v", entries)
	}
}
