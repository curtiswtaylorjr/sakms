package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/curtiswtaylorjr/sakms/internal/settings"
)

// adultNewestScanIntervalKey is the settings key for the background
// adultnewest scan cadence, in whole seconds. Mirrors
// recheckIntervalKey's import-avoidance rationale by value rather than
// importing internal/adultnewest's own copy of this constant, for the same
// reason: this endpoint's build shouldn't depend on that package.
//
// Unlike recheck, an UNSET key here means the job's actual default
// (adultNewestScanDefaultSeconds, 24h — an explicit operator directive,
// 2026-07-15), not off — see adultnewest.IntervalSettingKey's doc comment
// for the full rationale. This GET handler must mirror
// adultnewest.LoadInterval's unset-vs-explicit-zero distinction exactly, or
// Settings would show "0" while the background job is actually running
// every 24h — a real bug caught during this feature's own live deploy
// verification, not a hypothetical.
const adultNewestScanIntervalKey = "adult_newest_scan_interval_seconds"

// adultNewestScanDefaultSeconds duplicates adultnewest.defaultIntervalHours
// (in seconds) for the same import-avoidance reason as the key above.
const adultNewestScanDefaultSeconds = 24 * 60 * 60

type adultNewestScanIntervalResponse struct {
	IntervalSeconds int `json:"intervalSeconds"`
}

type adultNewestScanIntervalRequest struct {
	IntervalSeconds int `json:"intervalSeconds"`
}

// getAdultNewestScanIntervalHandler returns the configured scan interval in
// seconds — adultNewestScanDefaultSeconds when the key was never explicitly
// saved, 0 when an operator explicitly saved "0" (turning the job off), and
// whatever positive value was last saved otherwise. See this file's package
// doc for why the unset case can't just return 0 here.
func getAdultNewestScanIntervalHandler(settingsStore *settings.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		v, err := settingsStore.Get(r.Context(), adultNewestScanIntervalKey)
		secs := adultNewestScanDefaultSeconds
		switch {
		case errors.Is(err, settings.ErrNotFound):
			// secs already holds the default.
		case err != nil:
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		default:
			secs = 0 // a stored-but-invalid value degrades to off, matching LoadInterval
			if n, convErr := strconv.Atoi(v); convErr == nil && n > 0 {
				secs = n
			}
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(adultNewestScanIntervalResponse{IntervalSeconds: secs})
	}
}

// putAdultNewestScanIntervalHandler stores the scan interval in seconds. 0
// disables the job; a negative value is rejected. Mirrors
// putRecheckIntervalHandler exactly.
func putAdultNewestScanIntervalHandler(settingsStore *settings.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req adultNewestScanIntervalRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		if req.IntervalSeconds < 0 {
			http.Error(w, "intervalSeconds must be zero (off) or a positive number of seconds", http.StatusBadRequest)
			return
		}
		if err := settingsStore.Set(r.Context(), adultNewestScanIntervalKey, strconv.Itoa(req.IntervalSeconds)); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
