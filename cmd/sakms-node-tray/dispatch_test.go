package main

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"path/filepath"
	"testing"
	"time"
)

// --- pure display logic ----------------------------------------------------

func TestDispatchDisplayTitle(t *testing.T) {
	if got := dispatchDisplayTitle(true); got != "Dispatch: Paused" {
		t.Errorf("paused = %q", got)
	}
	if got := dispatchDisplayTitle(false); got != "Dispatch: Running" {
		t.Errorf("running = %q", got)
	}
}

func TestDispatchActionTitle(t *testing.T) {
	// The action label describes what a click DOES: paused → resume, running → pause.
	if got := dispatchActionTitle(true); got != "Resume dispatch" {
		t.Errorf("paused action = %q, want \"Resume dispatch\"", got)
	}
	if got := dispatchActionTitle(false); got != "Pause dispatch" {
		t.Errorf("running action = %q, want \"Pause dispatch\"", got)
	}
}

// --- control client round-trip over a real unix socket ---------------------

func TestDispatchPauseControlClient_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	socketPath := filepath.Join(dir, "control.sock")

	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	// paused holds the fake daemon's authoritative state; the fake also models the
	// failed-push rollback: a set of `true` here is rejected (500 + rolled-back
	// value) to exercise the tray's error path.
	paused := false
	mux := http.NewServeMux()
	mux.HandleFunc("GET /dispatch/pause", func(w http.ResponseWriter, r *http.Request) {
		writeDispatch(w, http.StatusOK, dispatchPauseView{Paused: paused})
	})
	mux.HandleFunc("POST /dispatch/pause", func(w http.ResponseWriter, r *http.Request) {
		var req dispatchPauseView
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.Paused {
			// Simulate a failed relay: the daemon rolled the optimistic flip back
			// to the authoritative (running) value and returns a non-2xx + error.
			writeDispatch(w, http.StatusBadGateway, dispatchPauseView{Paused: false, Error: "server rejected pause: status 500"})
			return
		}
		paused = req.Paused
		writeDispatch(w, http.StatusOK, dispatchPauseView{Paused: paused})
	})

	srv := &http.Server{Handler: mux}
	go srv.Serve(ln) //nolint:errcheck
	t.Cleanup(func() { _ = srv.Close() })

	client := newControlClient(socketPath)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// GET reflects the daemon's current state.
	view, err := client.getDispatchPause(ctx)
	if err != nil || view.Paused {
		t.Fatalf("getDispatchPause = (%+v, %v), want (paused=false, nil)", view, err)
	}

	// A successful set (resume, i.e. paused=false) returns the new value.
	view, err = client.setDispatchPause(ctx, false)
	if err != nil {
		t.Fatalf("setDispatchPause(false): %v", err)
	}
	if view.Paused {
		t.Errorf("set echo = %+v, want paused=false", view)
	}

	// A failed set surfaces the daemon's error AND the rolled-back value, so the
	// tray can notify() and display the true (running) state.
	view, err = client.setDispatchPause(ctx, true)
	if err == nil || err.Error() != "server rejected pause: status 500" {
		t.Fatalf("setDispatchPause(true) error = %v, want the daemon's rejection message", err)
	}
	if view.Paused {
		t.Errorf("failed set should still carry the rolled-back paused=false, got %+v", view)
	}
}

func writeDispatch(w http.ResponseWriter, status int, v dispatchPauseView) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
