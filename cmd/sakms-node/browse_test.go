package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBrowseDirectory_DirsOnlyAlphaSorted(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"zeta", "alpha", "mu"} {
		if err := os.Mkdir(filepath.Join(dir, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// A plain file must never appear in the results — this endpoint's whole
	// use case is picking a directory, mirroring the server's own browse.go.
	if err := os.WriteFile(filepath.Join(dir, "not-a-dir.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	entries, err := browseDirectory(dir, false)
	if err != nil {
		t.Fatalf("browseDirectory: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("got %d entries, want 3 (dirs only, file excluded): %+v", len(entries), entries)
	}
	wantOrder := []string{"alpha", "mu", "zeta"}
	for i, want := range wantOrder {
		if entries[i].Name != want {
			t.Errorf("entries[%d].Name = %q, want %q (must be alpha-sorted)", i, entries[i].Name, want)
		}
		wantPath := filepath.Join(dir, want)
		if entries[i].Path != wantPath {
			t.Errorf("entries[%d].Path = %q, want %q", i, entries[i].Path, wantPath)
		}
	}
}

func TestBrowseDirectory_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	entries, err := browseDirectory(dir, false)
	if err != nil {
		t.Fatalf("browseDirectory: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("got %d entries for an empty dir, want 0", len(entries))
	}
}

func TestBrowseDirectory_NonexistentPathReturnsError(t *testing.T) {
	_, err := browseDirectory(filepath.Join(t.TempDir(), "does-not-exist"), false)
	if err == nil {
		t.Fatal("expected an error for a nonexistent path, got nil")
	}
}

func TestBrowseDirectory_IncludeFiles_ReturnsFilesAndDirs(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "subdir"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "movie.mkv"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	entries, err := browseDirectory(dir, true)
	if err != nil {
		t.Fatalf("browseDirectory: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("got %d entries, want 2 (file + dir both included): %+v", len(entries), entries)
	}
	names := map[string]bool{}
	for _, e := range entries {
		names[e.Name] = true
	}
	if !names["movie.mkv"] || !names["subdir"] {
		t.Errorf("expected both movie.mkv and subdir present, got %+v", entries)
	}
}

func TestBrowseDirectory_IncludeFiles_FlatFileOnlyLibraryNotEmpty(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"Movie A (2020).mkv", "Movie B (2021).mkv"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// includeFiles=false (today's dirs-only picker behavior) sees an empty
	// flat library — this is the exact gap IncludeFiles exists to close for
	// the verification safeguard, which must set includeFiles=true instead.
	dirsOnly, err := browseDirectory(dir, false)
	if err != nil {
		t.Fatalf("browseDirectory: %v", err)
	}
	if len(dirsOnly) != 0 {
		t.Fatalf("expected 0 dirs-only entries for a flat file-only library, got %d", len(dirsOnly))
	}

	withFiles, err := browseDirectory(dir, true)
	if err != nil {
		t.Fatalf("browseDirectory: %v", err)
	}
	if len(withFiles) != 2 {
		t.Fatalf("expected 2 entries with includeFiles=true, got %d", len(withFiles))
	}
}
