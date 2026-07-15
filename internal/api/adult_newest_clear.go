package api

// TEMPORARY one-off migration endpoint — see
// adultnewest.ReleaseStore.ClearAll's doc comment. Wipes the entire
// adult_newest_releases/adult_newest_seen cache so the next scan cycle
// repopulates everything with FirstSeenReleaseTitle correctly set. Remove
// this file and its one route registration in internal/api/handler.go once
// run successfully against production.

import (
	"encoding/json"
	"net/http"

	"github.com/curtiswtaylorjr/sakms/internal/adultnewest"
)

// clearAdultNewestHandler is POST /api/modes/adult/newest-rows/clear-all.
func clearAdultNewestHandler(releaseStore *adultnewest.ReleaseStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := releaseStore.ClearAll(r.Context()); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]bool{"cleared": true})
	}
}
