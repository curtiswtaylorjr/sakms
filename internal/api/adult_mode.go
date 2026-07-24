package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/labbersanon/sakms/internal/mode"
	"github.com/labbersanon/sakms/internal/settings"
)

// AdultModeEnabledKey gates whether Adult-related UI is shown anywhere in
// the frontend. This is a pure visibility switch, never a backend access
// boundary — every Adult route, and all 3 shared background schedulers,
// keep working regardless of this setting (see
// .omc/plans/ralplan-adult-disable-switch.md). Stored as "true"/"false".
const AdultModeEnabledKey = "adult_mode_enabled"

// resolveAdultModeEnabled loads the Adult-mode-enabled toggle. Unlike a
// GetBool-with-static-default read, this needs to distinguish "explicitly
// set to false" from "never set" — an explicit false must survive even
// when the Adult library root folder is (still) configured — so it reads
// the raw stored string via Get, not GetBool.
//
// If unset, the default is computed live from whether the Adult library
// root folder is configured (libraryRootFolderKey(mode.Adult)): non-empty
// root folder -> true, empty/unset -> false. If explicitly set, the stored
// value wins regardless of root-folder state. A malformed stored value
// falls through to the computed default rather than erroring the GET,
// mirroring resolveAdultIdentifyEnabled's existing tolerance of garbage
// stored values.
func resolveAdultModeEnabled(ctx context.Context, settingsStore *settings.Store) (bool, error) {
	raw, err := settingsStore.Get(ctx, AdultModeEnabledKey)
	if err != nil && !errors.Is(err, settings.ErrNotFound) {
		return false, err
	}
	if raw != "" {
		if v, err := strconv.ParseBool(raw); err == nil {
			return v, nil
		}
		// malformed stored value -> fall through to the computed default
	}

	rootFolderKey, ok := libraryRootFolderKey(mode.Adult)
	if !ok {
		return false, nil
	}
	rootFolder, err := settingsStore.Get(ctx, rootFolderKey)
	if err != nil && !errors.Is(err, settings.ErrNotFound) {
		return false, err
	}
	return rootFolder != "", nil
}

type adultModeEnabledResponse struct {
	Enabled bool `json:"enabled"`
}

type adultModeEnabledRequest struct {
	Enabled bool `json:"enabled"`
}

// getAdultModeEnabledHandler returns the Adult-mode-enabled toggle (see
// resolveAdultModeEnabled for the unset-default computation).
func getAdultModeEnabledHandler(settingsStore *settings.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		enabled, err := resolveAdultModeEnabled(r.Context(), settingsStore)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(adultModeEnabledResponse{Enabled: enabled})
	}
}

// putAdultModeEnabledHandler stores the Adult-mode-enabled toggle. This is
// a single-key toggle only — it never touches
// adult_newest_scan_interval_seconds, which is a fully separate concern
// fired by the frontend as its own second PUT to the existing interval
// endpoint when needed.
func putAdultModeEnabledHandler(settingsStore *settings.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req adultModeEnabledRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		if err := settingsStore.SetBool(r.Context(), AdultModeEnabledKey, req.Enabled); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
