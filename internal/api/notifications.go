package api

import (
	"fmt"
	"net/http"

	"github.com/labbersanon/sakms/internal/webhooks"
)

// notificationsStreamHandler streams live notification events (rename.applied,
// purge.applied, dedup.applied, grab.completed) as server-sent events. It
// subscribes to the webhooks Store's in-process broadcaster, which publishes on
// every Dispatch regardless of whether any outbound webhook is configured.
//
// Unlike downloadsStreamHandler there is NO initial-snapshot paint: this is a
// pure event stream with no current state to render on connect. Events fired
// during a client disconnect/reconnect window are lost — there is no replay
// buffer — an accepted limitation for this best-effort, foreground-only
// feature (the browser's EventSource auto-reconnects regardless).
func notificationsStreamHandler(whStore *webhooks.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming not supported", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")

		// Flush an SSE comment immediately so the 200 + headers are sent on
		// connect. Unlike downloadsStreamHandler there is no initial snapshot to
		// paint (this is a pure event stream), so without this the response would
		// send no bytes until the first event — the browser's EventSource would
		// stay CONNECTING and an intermediary proxy could reap an idle stream that
		// never sent headers. The comment doubles as an initial keepalive.
		fmt.Fprint(w, ": connected\n\n")
		flusher.Flush()

		ctx := r.Context()

		// A nil whStore yields a nil channel + no-op unsubscribe (see
		// webhooks.Store.Subscribe); a nil channel blocks forever in the select,
		// so the loop simply waits on ctx.Done() — never busy-spins.
		ch, unsubscribe := whStore.Subscribe()
		defer unsubscribe()

		for {
			select {
			case <-ctx.Done():
				return
			case ev := <-ch:
				writeSSEData(w, flusher, ev)
			}
		}
	}
}
