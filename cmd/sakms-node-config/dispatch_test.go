//go:build cgo

package main

import (
	"encoding/json"
	"net/http"
	"testing"

	"fyne.io/fyne/v2/test"

	"github.com/labbersanon/sakms/internal/nodecontrol"
)

// dispatchFakeDaemon models the daemon's failed-push rollback: GET reports the
// authoritative state (running); a POST to pause is REJECTED (502 + the daemon's
// rolled-back running value), mirroring internal/nodecontrol's own dispatch test.
func dispatchFakeDaemon() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /dispatch/pause", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, nodecontrol.DispatchPauseView{Paused: false})
	})
	mux.HandleFunc("POST /dispatch/pause", func(w http.ResponseWriter, r *http.Request) {
		var req nodecontrol.DispatchPauseView
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.Paused {
			// Failed relay: daemon rolled the optimistic flip back to running.
			writeJSON(w, http.StatusBadGateway, nodecontrol.DispatchPauseView{Paused: false, Error: "server rejected pause: status 500"})
			return
		}
		writeJSON(w, http.StatusOK, nodecontrol.DispatchPauseView{Paused: req.Paused})
	})
	return mux
}

// TestDispatchToggleRollsBackOnFailedPush proves the U3 safety discipline: after
// an optimistic flip to paused whose push fails, the toggle rolls back to the
// pre-toggle (running) state rather than staying on the optimistic value.
func TestDispatchToggleRollsBackOnFailedPush(t *testing.T) {
	a := test.NewTempApp(t)
	win := a.NewWindow("t")
	client := startFakeDaemon(t, dispatchFakeDaemon())

	p := newDispatchPanel(client, win)
	p.refresh() // authoritative state: running (paused=false), toggle unchecked
	if p.paused || p.check.Checked {
		t.Fatalf("precondition: expected running/unchecked, got paused=%v checked=%v", p.paused, p.check.Checked)
	}

	// Simulate the operator checking the toggle (optimistic flip to paused). The
	// widget flips itself and fires OnChanged → toggle(true) → push fails → the
	// panel must roll the checked state back to running.
	p.check.SetChecked(true)

	if p.paused {
		t.Error("failed push: panel state should have rolled back to running (paused=false)")
	}
	if p.check.Checked {
		t.Error("failed push: toggle should have visually rolled back to unchecked, not the optimistic checked state")
	}
	if !p.errLabel.Visible() {
		t.Error("failed push: expected the in-window error to be shown")
	}
}
