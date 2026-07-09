package api

import (
	"encoding/json"
	"net/http"

	"github.com/curtiswtaylorjr/sakms/internal/connections"
	"github.com/curtiswtaylorjr/sakms/internal/library"
	"github.com/curtiswtaylorjr/sakms/internal/mode"
	"github.com/curtiswtaylorjr/sakms/internal/settings"
)

// libraryTrackedItem is Movies' shape for the Tag workflow's item picker —
// Tags is a list of label strings (a local tag has no numeric id), matching
// the label-as-id shape listTagsHandler's Movies branch returns for its
// vocabulary, so the frontend's existing id-keyed matching logic works
// unchanged for either mode.
type libraryTrackedItem struct {
	ID    int64    `json:"id"`
	Title string   `json:"title"`
	Tags  []string `json:"tags"`
}

// listTrackedHandler returns every item {mode} currently tracks — for
// Movies, straight from libStore (no Radarr involved); for Series/Adult,
// straight from the live *arr app, unchanged. Backs the Tag workflow's item
// picker (there's no other way to browse what's trackable to assign/remove
// a tag on) and is generically useful anywhere a UI needs real item
// context instead of guessing an ID.
func listTrackedHandler(httpClient *http.Client, connStore *connections.Store, settingsStore *settings.Store, libStore *library.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		m := mode.Mode(r.PathValue("mode"))
		ctx := r.Context()

		if m == mode.Movies {
			items, err := libStore.List(ctx, mode.Movies)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			out := make([]libraryTrackedItem, len(items))
			for i, item := range items {
				tags, err := libStore.Tags(ctx, item.ID)
				if err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}
				out[i] = libraryTrackedItem{ID: item.ID, Title: item.Title, Tags: tags}
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(out)
			return
		}

		sess, err := mode.Build(ctx, connStore, settingsStore, httpClient, m)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		tracked, err := sess.Servarr.AllTracked(ctx)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(tracked)
	}
}
