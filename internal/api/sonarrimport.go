package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/curtiswtaylorjr/sakms/internal/connections"
	"github.com/curtiswtaylorjr/sakms/internal/library"
	"github.com/curtiswtaylorjr/sakms/internal/servarr"
	"github.com/curtiswtaylorjr/sakms/internal/sonarrimport"
	"github.com/curtiswtaylorjr/sakms/internal/tmdb"
)

// sonarrImportHandler runs the one-time Sonarr library importer (see
// internal/sonarrimport's package doc) against whatever Sonarr and TMDB
// connections are currently configured — a manual, human-triggered action
// (there's a button for it in Settings, not a background job), safe to
// re-run since every write it makes is an idempotent upsert. Deliberately
// builds its own *servarr.Client/*tmdb.Client straight from connStore
// rather than going through mode.Build/sess.Servarr: this is meant to keep
// working even after Series stops requiring a Sonarr connection at all
// (mode.Build would then refuse to construct one), for as long as a user
// still has Sonarr around to migrate from.
func sonarrImportHandler(httpClient *http.Client, connStore *connections.Store, libStore *library.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		sonarrConn, err := connStore.Get(ctx, "sonarr")
		if err != nil {
			if errors.Is(err, connections.ErrNotFound) {
				http.Error(w, "sonarr isn't configured — there's nothing to import from", http.StatusBadRequest)
			} else {
				http.Error(w, err.Error(), http.StatusInternalServerError)
			}
			return
		}
		tmdbConn, err := connStore.Get(ctx, "tmdb")
		if err != nil {
			if errors.Is(err, connections.ErrNotFound) {
				http.Error(w, "tmdb isn't configured yet — add it in Settings first", http.StatusBadRequest)
			} else {
				http.Error(w, err.Error(), http.StatusInternalServerError)
			}
			return
		}

		sonarrClient := servarr.New(servarr.Config{BaseURL: sonarrConn.URL, APIKey: sonarrConn.APIKey, App: servarr.Sonarr}, httpClient)
		tmdbClient := tmdb.New(tmdb.Config{BaseURL: tmdbConn.URL, APIKey: tmdbConn.APIKey}, httpClient)

		result, err := sonarrimport.Import(ctx, sonarrClient, tmdbClient, libStore)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(result)
	}
}
