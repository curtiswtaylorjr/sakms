package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/curtiswtaylorjr/sakms/internal/sysinfo"
)

// sysinfoStreamHandler streams live resource metrics as server-sent events.
// It takes the first sample on connect, then every tick (2s by default)
// samples again and emits the rate snapshot computed against the previous
// sample. The response is always 200 with Content-Type text/event-stream;
// individual sample errors are sent as named SSE "sampleError" events (not
// HTTP errors, and deliberately NOT the reserved "error" event name so they
// don't collide with EventSource's transport-level onerror handler) so the
// client can display a transient warning without losing the connection.
//
// tickInterval is variadic purely so tests can inject a short interval; a
// production call (handler.go) passes only sampleFn and gets the 2s default,
// keeping the registration a clean one-arg call.
func sysinfoStreamHandler(sampleFn func() (sysinfo.RawSample, error), tickInterval ...time.Duration) http.HandlerFunc {
	interval := 2 * time.Second
	if len(tickInterval) > 0 && tickInterval[0] > 0 {
		interval = tickInterval[0]
	}
	return func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming not supported", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")

		ctx := r.Context()
		prev, err := sampleFn()
		if err != nil {
			fmt.Fprintf(w, "event: sampleError\ndata: %s\n\n", err.Error())
			flusher.Flush()
			return
		}

		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				curr, err := sampleFn()
				if err != nil {
					fmt.Fprintf(w, "event: sampleError\ndata: %s\n\n", err.Error())
					flusher.Flush()
					continue
				}
				snap := sysinfo.ComputeRates(prev, curr)
				prev = curr
				data, _ := json.Marshal(snap)
				fmt.Fprintf(w, "data: %s\n\n", data)
				flusher.Flush()
			}
		}
	}
}
