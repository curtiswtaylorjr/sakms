package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/curtiswtaylorjr/sakms/internal/apidto"
	"github.com/curtiswtaylorjr/sakms/internal/autograb"
	"github.com/curtiswtaylorjr/sakms/internal/connections"
	"github.com/curtiswtaylorjr/sakms/internal/grabs"
	"github.com/curtiswtaylorjr/sakms/internal/mode"
	"github.com/curtiswtaylorjr/sakms/internal/prowlarr"
	"github.com/curtiswtaylorjr/sakms/internal/quality"
	"github.com/curtiswtaylorjr/sakms/internal/release"
	"github.com/curtiswtaylorjr/sakms/internal/settings"
)

// adultAutoGrabCategory is the XXX (6000-range) Newznab category Adult
// releases live in — the same value availability.CheckAdultScene probes.
// Deliberately NOT categoriesForSearch(adult), which still returns 2000
// (the Movies category) for legacy reasons; auto-grabbing Adult against 2000
// would search the wrong catalog entirely.
const adultAutoGrabCategory = 6000

// autoGrabHandler is Discover's one-click unattended auto-grab (Stage 2). It
// searches Prowlarr for the requested title/scene, grades every release with
// internal/autograb's bitrate-quality-floor scorer, and either
//
//   - sends the single highest-scored qualifying release straight to the
//     download client (no human release-pick — that IS auto-grab), recording
//     it in grabsStore exactly like grabHandler; or
//   - when nothing clears the floor, returns the ranked candidate list for the
//     frontend's manual pick fallback (never "grab the least-bad option").
//
// Exactly one release is ever grabbed per call: no bulk action, the same
// staged-single-mutation invariant every other SAK workflow keeps.
func autoGrabHandler(httpClient *http.Client, connStore *connections.Store, settingsStore *settings.Store, grabsStore *grabs.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		m := mode.Mode(r.PathValue("mode"))
		ctx := r.Context()

		var req apidto.AutoGrabRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		if strings.TrimSpace(req.Title) == "" {
			http.Error(w, "title is required", http.StatusBadRequest)
			return
		}

		sess, err := mode.Build(ctx, connStore, settingsStore, httpClient, m)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if sess.Prowlarr == nil {
			http.Error(w, "prowlarr isn't configured yet — add it in Settings first", http.StatusBadRequest)
			return
		}
		// Movies/Series both resolve ids/runtime through TMDB; Adult never does.
		if m != mode.Adult && sess.TMDB == nil {
			http.Error(w, "tmdb isn't configured yet — add it in Settings first", http.StatusBadRequest)
			return
		}

		releases, runtimeSeconds, err := autoGrabSearch(ctx, sess, m, req)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}

		candidates := buildAutoGrabCandidates(releases, runtimeSeconds)
		sel := autograb.Select(candidates, autoGrabTier(ctx, settingsStore, m), autograb.DefaultMinSeeders)

		// Fallback: nothing cleared the floor → hand back the ranked pick list
		// (best bitrate score first, the same score that rejected them all).
		if sel.Fallback {
			writeAutoGrabJSON(w, apidto.AutoGrabResponse{
				Fallback:   true,
				Message:    "nothing cleared the quality floor automatically — pick one below",
				Candidates: rankedAutoGrabCandidates(sel, releases),
			})
			return
		}

		// Qualified: send exactly the one top-scored release to the download
		// client and record it. Root folder is resolved server-side — a true
		// one-click grab supplies only the title.
		rootFolder, err := autoGrabRootFolder(ctx, settingsStore, m)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		picked := releases[sel.PickIndex]

		downloadClient, clientRef, status, err := dispatchToDownloadClient(ctx, sess, m, string(picked.Protocol), picked.DownloadURL, picked.Title)
		if err != nil {
			http.Error(w, err.Error(), status)
			return
		}

		created, err := grabsStore.Create(ctx, grabs.Grab{
			Mode: m, Title: req.Title, TMDBID: req.TMDBID,
			SeasonNumber: req.SeasonNumber, EpisodeNumber: req.EpisodeNumber, SeasonSpecified: req.SeasonSpecified,
			Indexer: picked.Indexer, Protocol: string(picked.Protocol),
			DownloadClient: downloadClient, ClientRef: clientRef, RootFolderPath: rootFolder,
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		dto := toDTOGrab(created)
		writeAutoGrabJSON(w, apidto.AutoGrabResponse{
			Grabbed: true,
			Message: "auto-grabbed " + picked.Title,
			Grab:    &dto,
		})
	}
}

// autoGrabSearch runs the per-mode Prowlarr search and resolves the known
// pre-grab runtime (seconds) the bitrate scorer needs. Movies/Series probe
// id-scoped (mirroring availability.CheckMovie/CheckSeries); Adult uses a
// studio+title free-text query over the XXX category (mirroring
// availability.CheckAdultScene). Runtime: Movies from TMDB MovieDetails;
// Adult from the request's DurationSeconds; Series 0 (unknown — TMDB exposes
// no per-episode runtime pre-grab, so Series always falls through to the
// manual pick list, which is the graceful, documented outcome). Callers must
// have already confirmed sess.TMDB != nil for Movies/Series.
func autoGrabSearch(ctx context.Context, sess *mode.Session, m mode.Mode, req apidto.AutoGrabRequest) ([]prowlarr.Release, float64, error) {
	switch m {
	case mode.Adult:
		query := strings.TrimSpace(strings.TrimSpace(req.Studio) + " " + strings.TrimSpace(req.Title))
		releases, err := sess.Prowlarr.Search(ctx, query, []int{adultAutoGrabCategory})
		return releases, float64(req.DurationSeconds), err
	case mode.Series:
		tvdbID, err := sess.TMDB.ExternalIDs(ctx, req.TMDBID)
		if err != nil {
			return nil, 0, err
		}
		releases, err := sess.Prowlarr.SearchByID(ctx, prowlarr.SearchByIDParams{
			TVDBID: tvdbID, Season: req.SeasonNumber, Episode: req.EpisodeNumber,
			Categories: categoriesForSearch(mode.Series),
		})
		return releases, 0, err
	default: // Movies
		details, err := sess.TMDB.MovieDetails(ctx, req.TMDBID)
		if err != nil {
			return nil, 0, err
		}
		releases, err := sess.Prowlarr.SearchByID(ctx, prowlarr.SearchByIDParams{
			TMDBID: req.TMDBID, IMDBID: details.IMDBID,
			Categories: categoriesForSearch(mode.Movies),
		})
		return releases, float64(details.Runtime) * 60, err
	}
}

// buildAutoGrabCandidates turns Prowlarr releases into autograb.Candidates by
// combining release.Parse's title-derived Resolution/Codec/Source with each
// release's Prowlarr-reported Size/Seeders/Protocol and the shared known
// runtime. Pure and order-preserving: candidates[i] corresponds to
// releases[i], so a Selection's indices map straight back to the originating
// release for grabbing.
func buildAutoGrabCandidates(releases []prowlarr.Release, runtimeSeconds float64) []autograb.Candidate {
	candidates := make([]autograb.Candidate, len(releases))
	for i, rel := range releases {
		info := release.Parse(rel.Title)
		candidates[i] = autograb.Candidate{
			Title:          rel.Title,
			Protocol:       string(rel.Protocol),
			Seeders:        rel.Seeders,
			SizeBytes:      rel.Size,
			RuntimeSeconds: runtimeSeconds,
			Resolution:     info.Resolution,
			Codec:          info.Codec,
			Source:         info.Source,
		}
	}
	return candidates
}

// rankedAutoGrabCandidates flattens a fallback Selection into the wire pick
// list, ordered by Selection.Ranked (best bitrate score first). Each row pairs
// the grade (status/score/why) with the originating release's grab identity.
func rankedAutoGrabCandidates(sel autograb.Selection, releases []prowlarr.Release) []apidto.AutoGrabCandidate {
	out := make([]apidto.AutoGrabCandidate, 0, len(sel.Ranked))
	for _, idx := range sel.Ranked {
		g := sel.Grades[idx]
		rel := releases[idx]
		out = append(out, apidto.AutoGrabCandidate{
			Title:       rel.Title,
			Indexer:     rel.Indexer,
			Protocol:    string(rel.Protocol),
			DownloadURL: rel.DownloadURL,
			Size:        rel.Size,
			Seeders:     rel.Seeders,
			Status:      string(g.Status),
			Score:       g.Score,
			ImpliedMbps: g.ImpliedMbps,
			FloorMbps:   g.FloorMbps,
			Qualified:   g.Qualified,
		})
	}
	return out
}

// autoGrabTier reads {mode}'s configured quality tier (the SAME per-mode
// setting Search uses — see qualityTierKey), defaulting to quality.Default
// when unset. Adult has no tier key, so it always grades against the default.
func autoGrabTier(ctx context.Context, settingsStore *settings.Store, m mode.Mode) quality.Tier {
	tierStr, err := settingsStore.Get(ctx, qualityTierKey(m))
	if err != nil || tierStr == "" {
		return quality.Default
	}
	return quality.Tier(tierStr)
}

// autoGrabRootFolder resolves {mode}'s configured library root folder — where
// an auto-grabbed download is imported (checkImportHandler relocates into it).
// A missing root folder is a 400, the same guard the old frontend enforced
// client-side before grabbing.
func autoGrabRootFolder(ctx context.Context, settingsStore *settings.Store, m mode.Mode) (string, error) {
	key, ok := libraryRootFolderKey(m)
	if !ok {
		return "", fmt.Errorf("no library root folder applies to %s", m)
	}
	path, err := settingsStore.Get(ctx, key)
	if err != nil && !errors.Is(err, settings.ErrNotFound) {
		return "", err
	}
	if path == "" {
		return "", fmt.Errorf("no root folder configured for %s — set one in Settings first", m)
	}
	return path, nil
}

// toDTOGrab maps an internal grabs.Grab onto the exported apidto.Grab wire DTO
// (field-for-field, since apidto.Grab mirrors grabs.Grab's JSON tags exactly)
// so the auto-grab response and the Grabs view share one generated TypeScript
// type.
func toDTOGrab(g grabs.Grab) apidto.Grab {
	return apidto.Grab{
		ID: g.ID, Mode: string(g.Mode), Title: g.Title, TMDBID: g.TMDBID, TVDBID: g.TVDBID,
		SeasonNumber: g.SeasonNumber, EpisodeNumber: g.EpisodeNumber, SeasonSpecified: g.SeasonSpecified,
		QualityProfileID: g.QualityProfileID, Indexer: g.Indexer, Protocol: g.Protocol,
		DownloadClient: g.DownloadClient, ClientRef: g.ClientRef, Status: string(g.Status),
		RootFolderPath: g.RootFolderPath, FlaggedForReview: g.FlaggedForReview, FlagReason: g.FlagReason,
		CreatedAt: g.CreatedAt, UpdatedAt: g.UpdatedAt,
	}
}

func writeAutoGrabJSON(w http.ResponseWriter, resp apidto.AutoGrabResponse) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}
