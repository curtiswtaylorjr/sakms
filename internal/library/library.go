// Package library is SAK's own record of "what's in my library" — the
// replacement for asking Radarr (and, eventually, Sonarr) what it tracks.
// One Item per movie today; the same table/Store is designed to be reused
// for Series in a later stage (see the plan this was built from), keyed by
// mode so both share one schema instead of two near-duplicate ones.
//
// This package owns no HTTP client and makes no outbound calls — it's a
// thin SQLite-backed Store (same shape as internal/grabs/internal/
// connections) plus ScanRootFolder, a plain directory walk that replaces
// what Radarr's RootFolder.UnmappedFolders used to compute for free.
package library

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/curtiswtaylorjr/sakms/internal/config"
	"github.com/curtiswtaylorjr/sakms/internal/mode"
)

// ErrNotFound is returned by Get/GetByTMDBID when no matching item exists.
var ErrNotFound = errors.New("library: no item found")

// Item is one thing SAK's own library tracks — a movie today.
type Item struct {
	ID             int64     `json:"id"`
	Mode           mode.Mode `json:"mode"`
	TMDBID         int       `json:"tmdbId"`
	Title          string    `json:"title"`
	Year           int       `json:"year,omitempty"`
	FilePath       string    `json:"filePath"`
	RootFolderPath string    `json:"rootFolderPath"`
	CreatedAt      string    `json:"createdAt"`
	UpdatedAt      string    `json:"updatedAt"`
}

type Store struct {
	db *sql.DB
}

func New(db *sql.DB) *Store {
	return &Store{db: db}
}

// Upsert creates item, or replaces it if one already exists for the same
// (mode, tmdbId) pair — the single re-entrant "this is now what I have for
// this title" operation Rename/Dedup/Search's check-import all use, so a
// re-scan or a re-grab of something already in the library updates it in
// place rather than erroring or duplicating.
func (s *Store) Upsert(ctx context.Context, item Item) (Item, error) {
	row := s.db.QueryRowContext(ctx, `
		INSERT INTO library_items (mode, tmdb_id, title, year, file_path, root_folder_path)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(mode, tmdb_id) DO UPDATE SET
			title = excluded.title,
			year = excluded.year,
			file_path = excluded.file_path,
			root_folder_path = excluded.root_folder_path,
			updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
		RETURNING id, created_at, updated_at
	`, string(item.Mode), item.TMDBID, item.Title, item.Year, item.FilePath, item.RootFolderPath)

	if err := row.Scan(&item.ID, &item.CreatedAt, &item.UpdatedAt); err != nil {
		return Item{}, fmt.Errorf("upserting library item %q: %w", item.Title, err)
	}
	return item, nil
}

// List returns every item tracked for m, ordered by title.
func (s *Store) List(ctx context.Context, m mode.Mode) ([]Item, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, mode, tmdb_id, title, year, file_path, root_folder_path, created_at, updated_at
		FROM library_items WHERE mode = ? ORDER BY title
	`, string(m))
	if err != nil {
		return nil, fmt.Errorf("listing library items: %w", err)
	}
	defer rows.Close()

	out := []Item{}
	for rows.Next() {
		item, err := scanItem(rows)
		if err != nil {
			return nil, fmt.Errorf("scanning library item: %w", err)
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

// Get returns a single item by ID.
func (s *Store) Get(ctx context.Context, id int64) (*Item, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, mode, tmdb_id, title, year, file_path, root_folder_path, created_at, updated_at
		FROM library_items WHERE id = ?
	`, id)
	item, err := scanItem(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("loading library item %d: %w", id, err)
	}
	return &item, nil
}

// GetByTMDBID looks up an item by its (mode, tmdbId) identity — the
// duplicate-detection key Rename/Dedup use instead of Servarr's
// TVDB/TMDB-keyed TrackedItem list.
func (s *Store) GetByTMDBID(ctx context.Context, m mode.Mode, tmdbID int) (*Item, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, mode, tmdb_id, title, year, file_path, root_folder_path, created_at, updated_at
		FROM library_items WHERE mode = ? AND tmdb_id = ?
	`, string(m), tmdbID)
	item, err := scanItem(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("loading library item for tmdb id %d: %w", tmdbID, err)
	}
	return &item, nil
}

// Delete permanently removes item id and its tags. Explicit two-statement
// delete rather than relying on the schema's ON DELETE CASCADE: SQLite only
// enforces foreign keys when a connection has run `PRAGMA foreign_keys =
// ON`, which internal/db's shared Open doesn't set (changing that
// connection-wide default is out of scope for this package). Deleting an id
// that doesn't exist is not an error — the end state is the same, matching
// connections.Store.Delete's convention.
func (s *Store) Delete(ctx context.Context, id int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("deleting library item %d: %w", id, err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `DELETE FROM library_tags WHERE item_id = ?`, id); err != nil {
		return fmt.Errorf("deleting tags for library item %d: %w", id, err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM library_items WHERE id = ?`, id); err != nil {
		return fmt.Errorf("deleting library item %d: %w", id, err)
	}
	return tx.Commit()
}

// Tags returns itemID's assigned tags, alphabetically.
func (s *Store) Tags(ctx context.Context, itemID int64) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT tag FROM library_tags WHERE item_id = ? ORDER BY tag`, itemID)
	if err != nil {
		return nil, fmt.Errorf("listing tags for item %d: %w", itemID, err)
	}
	defer rows.Close()

	out := []string{}
	for rows.Next() {
		var tag string
		if err := rows.Scan(&tag); err != nil {
			return nil, fmt.Errorf("scanning tag: %w", err)
		}
		out = append(out, tag)
	}
	return out, rows.Err()
}

// AddTag assigns tag to itemID. A no-op (not an error) if already assigned.
func (s *Store) AddTag(ctx context.Context, itemID int64, tag string) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO library_tags (item_id, tag) VALUES (?, ?)
		ON CONFLICT(item_id, tag) DO NOTHING
	`, itemID, tag)
	if err != nil {
		return fmt.Errorf("adding tag %q to item %d: %w", tag, itemID, err)
	}
	return nil
}

// RemoveTag unassigns tag from itemID. A no-op (not an error) if it wasn't assigned.
func (s *Store) RemoveTag(ctx context.Context, itemID int64, tag string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM library_tags WHERE item_id = ? AND tag = ?`, itemID, tag)
	if err != nil {
		return fmt.Errorf("removing tag %q from item %d: %w", tag, itemID, err)
	}
	return nil
}

// TagVocabulary returns every distinct tag currently used by any item in m —
// what a Tag picker autocompletes against, imported live from usage rather
// than a separately-maintained vocabulary list (the same principle
// internal/tag's Servarr-backed Vocabulary already follows).
func (s *Store) TagVocabulary(ctx context.Context, m mode.Mode) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT DISTINCT t.tag FROM library_tags t
		JOIN library_items i ON i.id = t.item_id
		WHERE i.mode = ? ORDER BY t.tag
	`, string(m))
	if err != nil {
		return nil, fmt.Errorf("listing tag vocabulary: %w", err)
	}
	defer rows.Close()

	out := []string{}
	for rows.Next() {
		var tag string
		if err := rows.Scan(&tag); err != nil {
			return nil, fmt.Errorf("scanning tag: %w", err)
		}
		out = append(out, tag)
	}
	return out, rows.Err()
}

// rowScanner is satisfied by both *sql.Row and *sql.Rows.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanItem(row rowScanner) (Item, error) {
	var item Item
	var m string
	err := row.Scan(&item.ID, &m, &item.TMDBID, &item.Title, &item.Year,
		&item.FilePath, &item.RootFolderPath, &item.CreatedAt, &item.UpdatedAt)
	item.Mode = mode.Mode(m)
	return item, err
}

// UnmappedEntry is one file or directory found directly under a root folder
// that ScanRootFolder couldn't match to any already-known library item —
// the library-local equivalent of Radarr's RootFolder.UnmappedFolders.
type UnmappedEntry struct {
	Name string
	Path string
}

// ScanRootFolder lists every entry directly under rootPath that known
// (keyed by absolute path) doesn't already claim, skipping sidecar files
// (subtitles, .nfo, thumbnails — see config.SidecarExts, the same filter
// Rename already applies to Radarr/Sonarr's own unmapped-folder lists).
// Mode-agnostic by design, so a later Series orphan-episode scan can reuse
// it unchanged.
func ScanRootFolder(rootPath string, known map[string]bool) ([]UnmappedEntry, error) {
	entries, err := os.ReadDir(rootPath)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", rootPath, err)
	}

	var out []UnmappedEntry
	for _, e := range entries {
		if config.SidecarExts[strings.ToLower(filepath.Ext(e.Name()))] {
			continue
		}
		path := filepath.Join(rootPath, e.Name())
		if known[path] {
			continue
		}
		out = append(out, UnmappedEntry{Name: e.Name(), Path: path})
	}
	return out, nil
}

// videoExts are the file extensions ResolveVideoFile treats as playable
// video content — matches internal/dedup's own (private, independently
// tested) videoExts list.
var videoExts = map[string]bool{
	".mkv": true, ".mp4": true, ".avi": true, ".m4v": true,
	".ts": true, ".wmv": true, ".mov": true, ".webm": true,
}

// ResolveVideoFile resolves path to an actual video file: itself, if it's
// already one, or the largest video-extensioned file directly inside it, if
// it's a directory. Needed anywhere a relocated path might be a wrapping
// folder (root/Title (Year)/movie.mkv) rather than the file itself — e.g.
// after rename.Relocate or a completed grab's contentPath, both of which
// preserve whatever shape the source had (a single file, or a directory
// containing one).
func ResolveVideoFile(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("stat %s: %w", path, err)
	}
	if !info.IsDir() {
		return path, nil
	}

	entries, err := os.ReadDir(path)
	if err != nil {
		return "", fmt.Errorf("reading %s: %w", path, err)
	}
	var best string
	var bestSize int64
	for _, e := range entries {
		if e.IsDir() || !videoExts[strings.ToLower(filepath.Ext(e.Name()))] {
			continue
		}
		fi, err := e.Info()
		if err != nil {
			continue
		}
		if fi.Size() > bestSize {
			bestSize = fi.Size()
			best = filepath.Join(path, e.Name())
		}
	}
	if best == "" {
		return "", fmt.Errorf("no video file found under %s", path)
	}
	return best, nil
}
