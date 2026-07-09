package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/curtiswtaylorjr/sakms/internal/connections"
	"github.com/curtiswtaylorjr/sakms/internal/dedup"
	"github.com/curtiswtaylorjr/sakms/internal/library"
	"github.com/curtiswtaylorjr/sakms/internal/mode"
	"github.com/curtiswtaylorjr/sakms/internal/proposals"
	"github.com/curtiswtaylorjr/sakms/internal/settings"
)

// dedupScanHandler runs the Dedup workflow's propose-phase for {mode}:
// identifies every unmapped file, groups it with any already-tracked item
// sharing the same identifier (TMDB ID for Movies, foreignID for Adult),
// ffprobes every candidate, and replaces the live Dedup queue with whatever
// duplicate groups it found. prober takes dedup.Prober's interface, not the
// concrete *mediainfo.Prober, so tests can inject a fake instead of
// depending on a real ffprobe binary. Movies dispatches to
// dedup.ScanLibrary (libStore, no Radarr involved); Series/Adult use the
// existing Servarr-backed dedup.Scan, unchanged.
func dedupScanHandler(httpClient *http.Client, connStore *connections.Store, settingsStore *settings.Store, propStore *proposals.Store, prober dedup.Prober, libStore *library.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		m := mode.Mode(r.PathValue("mode"))
		ctx := r.Context()

		sess, err := mode.Build(ctx, connStore, settingsStore, httpClient, m)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		var found []proposals.Proposal
		if m == mode.Movies {
			rootPath, rpErr := settingsStore.Get(ctx, moviesLibraryRootFolderKey)
			if rpErr != nil && !errors.Is(rpErr, settings.ErrNotFound) {
				http.Error(w, rpErr.Error(), http.StatusInternalServerError)
				return
			}
			found, err = dedup.ScanLibrary(ctx, sess, libStore, rootPath, prober)
		} else {
			found, err = dedup.Scan(ctx, sess, prober)
		}
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}

		saved, err := propStore.ReplacePending(ctx, m, proposals.Dedup, found)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(saved)
	}
}
