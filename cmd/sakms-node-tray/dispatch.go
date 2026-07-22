package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
)

// node-pause-dispatch Stage 3: the tray's dispatch-pause toggle. It mirrors
// mediaroots.go/pathmap.go's shipped grain exactly — a unix-socket control
// client, a DISABLED display item with an action SUB-item (an enabled
// parent-with-submenu does not reliably emit ClickedCh on Linux DBusMenu), and
// notify() surfacing. There is no folder picker here — pause is a pure toggle.
// The daemon (and ultimately the server) owns the authoritative state; the tray
// reads it fresh via GET /dispatch/pause and relays flips via POST.

// dispatchPauseView decodes the daemon's /dispatch/pause reply. Error carries the
// daemon's non-2xx body (e.g. a failed relay that rolled back), in which case
// Paused reports the rolled-back authoritative value.
type dispatchPauseView struct {
	Paused bool   `json:"paused"`
	Error  string `json:"error"`
}

// doDispatchPause issues one /dispatch/pause control-socket request and decodes
// the reply. Mirrors controlClient.do; dial failures propagate unwrapped so
// classifyDialError can bucket them (EACCES relogin / ENOENT daemon-down).
func (c *controlClient) doDispatchPause(ctx context.Context, method string, body any) (dispatchPauseView, error) {
	var rdr io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return dispatchPauseView{}, err
		}
		rdr = bytes.NewReader(buf)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://sakms-node/dispatch/pause", rdr)
	if err != nil {
		return dispatchPauseView{}, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return dispatchPauseView{}, err
	}
	defer resp.Body.Close()

	var out dispatchPauseView
	if decErr := json.NewDecoder(resp.Body).Decode(&out); decErr != nil && decErr != io.EOF {
		return dispatchPauseView{}, fmt.Errorf("decoding dispatch pause response: %w", decErr)
	}
	if resp.StatusCode != http.StatusOK {
		if out.Error != "" {
			return out, errors.New(out.Error)
		}
		return out, fmt.Errorf("control socket returned %s", resp.Status)
	}
	return out, nil
}

func (c *controlClient) getDispatchPause(ctx context.Context) (dispatchPauseView, error) {
	return c.doDispatchPause(ctx, http.MethodGet, nil)
}

func (c *controlClient) setDispatchPause(ctx context.Context, paused bool) (dispatchPauseView, error) {
	return c.doDispatchPause(ctx, http.MethodPost, dispatchPauseView{Paused: paused})
}

// --- pure display logic (unit-tested; no systray / no I/O) -----------------

// dispatchDisplayTitle formats the disabled display line for the current state.
func dispatchDisplayTitle(paused bool) string {
	if paused {
		return "Dispatch: Paused"
	}
	return "Dispatch: Running"
}

// dispatchActionTitle labels the toggle sub-item by what a click will DO: when
// paused, the action resumes; when running, the action pauses.
func dispatchActionTitle(paused bool) string {
	if paused {
		return "Resume dispatch"
	}
	return "Pause dispatch"
}

// --- tray wiring (holds/uses systray items; lock discipline mirrors siblings) --

// pollDispatchPause fetches GET /dispatch/pause OUTSIDE the lock (a slow/stuck
// socket must never freeze the click handlers or the poll loop — the deadlock
// lesson), then applies it under the lock. On error it silently keeps the
// last-known view: the status poll already signals a down/unreachable daemon, and
// notifying every tick would spam.
func (t *trayUI) pollDispatchPause() {
	ctx, cancel := context.WithTimeout(context.Background(), controlTimeout)
	defer cancel()
	view, err := t.control.getDispatchPause(ctx)

	t.mu.Lock()
	defer t.mu.Unlock()
	if err != nil {
		return
	}
	t.dispatchFetched = true
	t.dispatchPaused = view.Paused
	t.renderDispatch()
}

// renderDispatch updates the dispatch-pause section from the latest fetched
// state. Caller MUST hold t.mu.
func (t *trayUI) renderDispatch() {
	if !t.dispatchFetched {
		t.mDispatchStatus.Hide()
		return
	}
	t.mDispatchStatus.SetTitle(dispatchDisplayTitle(t.dispatchPaused))
	t.mDispatchToggle.SetTitle(dispatchActionTitle(t.dispatchPaused))
	t.mDispatchStatus.Show()
}

// handleDispatchToggle flips the node's dispatch-pause state via the control
// socket. It reads the current state under the lock, releases BEFORE the socket
// call (never hold t.mu across I/O), relays the negated value, then refreshes.
// A failed relay surfaces via notify() (the daemon rolled its optimistic flip
// back to the authoritative value; the subsequent poll reflects that truth).
func (t *trayUI) handleDispatchToggle() {
	t.mu.Lock()
	desired := !t.dispatchPaused
	fetched := t.dispatchFetched
	t.mu.Unlock()

	if !fetched {
		return // no authoritative state yet — nothing to toggle from
	}

	ctx, cancel := context.WithTimeout(context.Background(), controlTimeout)
	defer cancel()
	view, err := t.control.setDispatchPause(ctx, desired)
	if err != nil {
		t.reportControlError("change dispatch pause", err)
		t.pollDispatchPause() // reflect the daemon's rolled-back authoritative state
		return
	}
	if view.Paused {
		notify("sakms-node", "Dispatch paused — this node will receive no new jobs until resumed.")
	} else {
		notify("sakms-node", "Dispatch resumed — this node is eligible for new jobs again.")
	}
	t.pollDispatchPause()
}
