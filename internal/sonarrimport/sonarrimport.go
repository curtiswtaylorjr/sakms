// Package sonarrimport is a one-time, human-triggered importer that reads a
// still-live Sonarr instance and TMDB, and populates internal/library's
// Series/Episode tables from what Sonarr already tracks — the migration
// path off Sonarr for an existing Series library (see the plan this was
// built from, Stage 2).
//
// This package makes ZERO write calls to Sonarr — it only reads
// (AllTracked) — and is meant to be run once during migration, then never
// again. It's safe to re-run anyway: every write here goes through
// UpsertSeries/UpsertEpisode, both idempotent, so a second run just
// re-confirms the same rows rather than duplicating anything.
//
// Episode discovery does NOT ask Sonarr for its per-episode file mapping
// (internal/servarr exposes no such endpoint) — it walks each series'
// folder on disk directly, the same "SAK computes what's on disk itself"
// philosophy internal/library.ScanRootFolder already uses for orphan
// discovery. A known v1 simplification: "missing episodes" are only
// detected for seasons that already have at least one file on disk found
// during that walk — an entire season with zero files found isn't probed
// against TMDB at all, so a completely-undownloaded season won't yet
// surface its episodes as "missing." Extending this to every season TMDB
// reports for a show would need a show-details call this client doesn't
// have yet; deferred rather than built speculatively.
package sonarrimport

import (
	"context"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"

	"github.com/curtiswtaylorjr/sakms/internal/library"
	"github.com/curtiswtaylorjr/sakms/internal/servarr"
	"github.com/curtiswtaylorjr/sakms/internal/tmdb"
)

// videoExts mirrors internal/library's own (private) list — duplicated
// rather than exported, matching this project's existing precedent
// (internal/dedup keeps its own independent copy too) of not reshaping an
// already-shipped package's surface for a second caller's convenience.
var videoExts = map[string]bool{
	".mkv": true, ".mp4": true, ".avi": true, ".m4v": true,
	".ts": true, ".wmv": true, ".mov": true, ".webm": true,
}

// SeriesResult reports what happened importing one of Sonarr's tracked
// series.
type SeriesResult struct {
	Title  string `json:"title"`
	TVDBID int    `json:"tvdbId"`
	// Imported is false if this series was skipped entirely (see Reason) —
	// still not an error for the whole run, since every other series gets
	// its own independent chance (see Import's doc comment).
	Imported        bool   `json:"imported"`
	Reason          string `json:"reason,omitempty"` // populated when Imported is false, or as a partial-failure note
	EpisodesFound   int    `json:"episodesFound"`
	EpisodesMissing int    `json:"episodesMissing"`
}

// Result is the full summary of one Import run.
type Result struct {
	Series []SeriesResult `json:"series"`
}

// Import reads every series Sonarr currently tracks, resolves each to a
// TMDB TV id, records it (and whatever episodes can be found/known) in
// libStore. A failure on one series (TVDB id doesn't resolve, its folder
// can't be read) is recorded in that series' SeriesResult and does NOT
// stop the run — every other series still gets a chance.
func Import(ctx context.Context, sonarr *servarr.Client, tmdbClient *tmdb.Client, libStore *library.Store) (Result, error) {
	tracked, err := sonarr.AllTracked(ctx)
	if err != nil {
		return Result{}, fmt.Errorf("loading Sonarr's tracked series: %w", err)
	}

	var result Result
	for _, t := range tracked {
		result.Series = append(result.Series, importOne(ctx, tmdbClient, libStore, t))
	}
	return result, nil
}

func importOne(ctx context.Context, tmdbClient *tmdb.Client, libStore *library.Store, t servarr.TrackedItem) SeriesResult {
	sr := SeriesResult{Title: t.Title, TVDBID: t.TVDBID}

	tmdbID, ok, err := tmdbClient.FindByTVDBID(ctx, t.TVDBID)
	if err != nil {
		sr.Reason = fmt.Sprintf("TMDB lookup by TVDB id failed: %v", err)
		return sr
	}
	if !ok {
		sr.Reason = "no TMDB match found for this show's TVDB id"
		return sr
	}

	series, err := libStore.UpsertSeries(ctx, library.Series{
		TMDBID: tmdbID, TVDBID: t.TVDBID, Title: t.Title, RootFolderPath: t.RootFolderPath,
	})
	if err != nil {
		sr.Reason = fmt.Sprintf("recording the series in the library failed: %v", err)
		return sr
	}

	onDisk, err := scanEpisodeFiles(t.Path)
	if err != nil {
		sr.Reason = fmt.Sprintf("scanning %q for episode files failed: %v", t.Path, err)
		return sr
	}

	bySeason := map[int][]onDiskEpisode{}
	for _, e := range onDisk {
		bySeason[e.season] = append(bySeason[e.season], e)
	}

	var partialFailures []string
	for season, files := range bySeason {
		found, missing := importSeason(ctx, tmdbClient, libStore, series, season, files)
		sr.EpisodesFound += found
		sr.EpisodesMissing += missing
		if found+missing == 0 {
			partialFailures = append(partialFailures, fmt.Sprintf("season %d: TMDB season details unavailable", season))
		}
	}

	sr.Imported = true
	if len(partialFailures) > 0 {
		sr.Reason = strings.Join(partialFailures, "; ")
	}
	return sr
}

// importSeason records every episode TMDB reports for (series, season),
// filling in FilePath from files where a matching episode number was found
// on disk, and upserts any on-disk file whose episode number TMDB didn't
// report (so it's still tracked, just without title/air-date metadata).
// Falls back to recording on-disk files alone (no metadata) if the TMDB
// call itself fails, rather than losing those files from the import
// entirely.
func importSeason(ctx context.Context, tmdbClient *tmdb.Client, libStore *library.Store, series library.Series, season int, files []onDiskEpisode) (found, missing int) {
	byEpisode := map[int]string{}
	for _, f := range files {
		if _, exists := byEpisode[f.episode]; !exists {
			byEpisode[f.episode] = f.path
		}
	}

	canonical, err := tmdbClient.SeasonDetails(ctx, series.TMDBID, season)
	if err != nil {
		for episode, path := range byEpisode {
			if _, err := libStore.UpsertEpisode(ctx, library.Episode{
				SeriesID: series.ID, SeasonNumber: season, EpisodeNumber: episode, FilePath: path,
			}); err == nil {
				found++
			}
		}
		return found, missing
	}

	canonicalByEpisode := map[int]tmdb.SeasonEpisode{}
	for _, ep := range canonical {
		canonicalByEpisode[ep.EpisodeNumber] = ep
	}

	episodeNumbers := map[int]bool{}
	for n := range byEpisode {
		episodeNumbers[n] = true
	}
	for n := range canonicalByEpisode {
		episodeNumbers[n] = true
	}
	ordered := make([]int, 0, len(episodeNumbers))
	for n := range episodeNumbers {
		ordered = append(ordered, n)
	}
	sort.Ints(ordered)

	for _, n := range ordered {
		ep := library.Episode{SeriesID: series.ID, SeasonNumber: season, EpisodeNumber: n}
		if meta, ok := canonicalByEpisode[n]; ok {
			ep.Title, ep.AirDate = meta.Name, meta.AirDate
		}
		if path, ok := byEpisode[n]; ok {
			ep.FilePath = path
		}
		if _, err := libStore.UpsertEpisode(ctx, ep); err != nil {
			continue
		}
		if ep.FilePath != "" {
			found++
		} else {
			missing++
		}
	}
	return found, missing
}

type onDiskEpisode struct {
	season, episode int
	path            string
}

// scanEpisodeFiles walks root (a tracked series' whole folder, season
// subdirectories and all) and returns every video file whose name parses
// as an episode via library.ParseEpisodeFilename — deliberately ignoring
// season-subfolder naming conventions entirely (Sonarr's own "Season 01"
// style isn't guaranteed, and this only needs the file names, not the
// directory structure, to resolve season/episode).
func scanEpisodeFiles(root string) ([]onDiskEpisode, error) {
	var out []onDiskEpisode
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !videoExts[strings.ToLower(filepath.Ext(d.Name()))] {
			return nil
		}
		season, episode, ok := library.ParseEpisodeFilename(d.Name())
		if !ok {
			return nil
		}
		out = append(out, onDiskEpisode{season: season, episode: episode, path: path})
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}
