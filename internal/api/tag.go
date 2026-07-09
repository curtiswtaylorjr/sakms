package api

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/curtiswtaylorjr/sakms/internal/connections"
	"github.com/curtiswtaylorjr/sakms/internal/library"
	"github.com/curtiswtaylorjr/sakms/internal/mode"
	"github.com/curtiswtaylorjr/sakms/internal/settings"
	"github.com/curtiswtaylorjr/sakms/internal/tag"
)

// libraryTagEntry is Movies' vocabulary shape — a local tag is just a
// string with no numeric id, so ID and Label are always the same value.
// This keeps the response shape compatible with the frontend's existing
// {id, label} handling (id-keyed lookups, matching against
// libraryTrackedItem.Tags) regardless of which mode it's browsing.
type libraryTagEntry struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

// listTagsHandler returns {mode}'s current tag vocabulary. For Movies this
// is entirely local (libStore.TagVocabulary — distinct tags already in use,
// imported live from usage); Series/Adult still go straight to the live
// *arr app, unchanged.
func listTagsHandler(httpClient *http.Client, connStore *connections.Store, settingsStore *settings.Store, libStore *library.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		m := mode.Mode(r.PathValue("mode"))
		ctx := r.Context()

		if m == mode.Movies {
			vocab, err := libStore.TagVocabulary(ctx, m)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			out := make([]libraryTagEntry, len(vocab))
			for i, label := range vocab {
				out[i] = libraryTagEntry{ID: label, Label: label}
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

		tags, err := tag.Vocabulary(ctx, sess)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(tags)
	}
}

type addItemTagRequest struct {
	Label string `json:"label"`
}

// addItemTagHandler assigns a tag to one tracked item — a single,
// immediately-committed action, not staged through the proposals queue (see
// internal/tag's doc comment for why Tag doesn't follow the Scan/Apply
// shape the other three workflows do). For Movies, itemId is a library
// item's own id and this writes straight to libStore — there's no upstream
// "create the tag first" step the way Servarr's Tags resource needs, since
// a local tag is just a string.
func addItemTagHandler(httpClient *http.Client, connStore *connections.Store, settingsStore *settings.Store, libStore *library.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		m := mode.Mode(r.PathValue("mode"))
		itemID, ok := parseIntPathValue(w, r, "itemId")
		if !ok {
			return
		}
		var req addItemTagRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		if req.Label == "" {
			http.Error(w, "label is required", http.StatusBadRequest)
			return
		}
		ctx := r.Context()

		if m == mode.Movies {
			if err := libStore.AddTag(ctx, int64(itemID), req.Label); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}

		sess, err := mode.Build(ctx, connStore, settingsStore, httpClient, m)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := tag.Add(ctx, sess, itemID, req.Label); err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// removeItemTagHandler unassigns a tag from one tracked item. The route's
// {tagId} path segment means different things per mode: a numeric Servarr
// tag id for Series/Adult, or the tag string itself for Movies (a local tag
// has no numeric id at all — it's just a string in library_tags).
func removeItemTagHandler(httpClient *http.Client, connStore *connections.Store, settingsStore *settings.Store, libStore *library.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		m := mode.Mode(r.PathValue("mode"))
		itemID, ok := parseIntPathValue(w, r, "itemId")
		if !ok {
			return
		}
		ctx := r.Context()

		if m == mode.Movies {
			tagLabel := r.PathValue("tagId") // string label for Movies, not a numeric Servarr tag id
			if err := libStore.RemoveTag(ctx, int64(itemID), tagLabel); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}

		tagID, ok := parseIntPathValue(w, r, "tagId")
		if !ok {
			return
		}

		sess, err := mode.Build(ctx, connStore, settingsStore, httpClient, m)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := tag.Remove(ctx, sess, itemID, tagID); err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func parseIntPathValue(w http.ResponseWriter, r *http.Request, name string) (int, bool) {
	v, err := strconv.Atoi(r.PathValue(name))
	if err != nil {
		http.Error(w, "invalid "+name, http.StatusBadRequest)
		return 0, false
	}
	return v, true
}
