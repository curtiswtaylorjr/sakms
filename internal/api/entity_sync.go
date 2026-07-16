package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/curtiswtaylorjr/sakms/internal/connections"
	"github.com/curtiswtaylorjr/sakms/internal/parseentity"
	"github.com/curtiswtaylorjr/sakms/internal/settings"
	"github.com/curtiswtaylorjr/sakms/internal/stashapi"
	"github.com/curtiswtaylorjr/sakms/internal/stashbox"
	"github.com/curtiswtaylorjr/sakms/internal/tpdbrest"
)

type entitySyncStatusResponse struct {
	StudioCount    int                      `json:"studioCount"`
	PerformerCount int                      `json:"performerCount"`
	Sources        []entitySyncSourceStatus `json:"sources"`
}

type entitySyncSourceStatus struct {
	Source   string `json:"source"`
	SyncedAt string `json:"syncedAt"`
	Cursor   string `json:"cursor"`
}

// entitySyncStatusHandler returns the current entity cache counts and per-source
// sync state (last synced timestamp + cursor).
func entitySyncStatusHandler(store parseentity.EntityStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		studioCount, err := store.StudioCount(ctx)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		performerCount, err := store.PerformerCount(ctx)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		sources := make([]entitySyncSourceStatus, 0, 4)
		for _, src := range []string{"stash", "tpdb", "stashdb", "fansdb"} {
			cursor, syncedAt, err := store.GetSyncCursor(ctx, src)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			syncedAtStr := ""
			if !syncedAt.IsZero() {
				syncedAtStr = syncedAt.UTC().Format("2006-01-02T15:04:05Z")
			}
			sources = append(sources, entitySyncSourceStatus{
				Source:   src,
				SyncedAt: syncedAtStr,
				Cursor:   cursor,
			})
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(entitySyncStatusResponse{
			StudioCount:    studioCount,
			PerformerCount: performerCount,
			Sources:        sources,
		})
	}
}

// triggerEntitySyncHandler fires an on-demand entity cache sync for one source
// ("stash", "tpdb", "stashdb", or "fansdb"). The sync runs in a background
// goroutine; the handler returns 202 Accepted immediately. The caller may poll
// GET /api/admin/entity-sync to observe progress via the updatedAt timestamp.
func triggerEntitySyncHandler(store parseentity.EntityStore, connStore *connections.Store, _ *settings.Store, httpClient *http.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		source := r.PathValue("source")
		ctx := r.Context()
		switch source {
		case "stash":
			conn, err := connStore.Get(ctx, "stash")
			if errors.Is(err, connections.ErrNotFound) {
				http.Error(w, "stash connection not configured", http.StatusBadRequest)
				return
			}
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			client := stashapi.New(stashapi.Config{URL: conn.URL, APIKey: conn.APIKey}, httpClient)
			go func() { _ = parseentity.SyncFromStash(context.Background(), store, client) }()
		case "tpdb":
			conn, err := connStore.Get(ctx, "tpdb")
			if errors.Is(err, connections.ErrNotFound) {
				http.Error(w, "tpdb connection not configured", http.StatusBadRequest)
				return
			}
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			// TPDB REST base is fixed and public — hardcoded, never conn.URL.
			client := tpdbrest.New(tpdbrest.DefaultBaseURL, conn.APIKey, httpClient)
			go func() {
				_ = parseentity.SyncFromTPDB(context.Background(), store, client, parseentity.DefaultSyncPages)
			}()
		case "stashdb", "fansdb":
			conn, err := connStore.Get(ctx, source)
			if errors.Is(err, connections.ErrNotFound) {
				http.Error(w, source+" connection not configured", http.StatusBadRequest)
				return
			}
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			// StashDB/FansDB endpoints are fixed public constants, never conn.URL.
			endpoint, _ := stashbox.URLForBox(source)
			client := stashbox.New(stashbox.Config{
				Endpoint: endpoint, APIKey: conn.APIKey, IsBearer: false, HasVoteField: true,
			}, httpClient)
			go func() {
				_ = parseentity.SyncFromStashBox(context.Background(), store, client, source, parseentity.DefaultSyncPages)
			}()
		default:
			http.Error(w, "source must be one of: stash, tpdb, stashdb, fansdb", http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusAccepted)
	}
}
