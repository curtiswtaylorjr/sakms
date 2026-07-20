package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
)

// nodeState is the current lifecycle state of the node daemon.
type nodeState string

const (
	stateDisconnected nodeState = "disconnected"
	statePending      nodeState = "pending"
	stateConnected    nodeState = "connected"
)

// statusSnapshot is the JSON payload returned by GET /status.
type statusSnapshot struct {
	State       nodeState `json:"state"`
	PairingCode string    `json:"pairingCode,omitempty"` // non-empty only when pending
	ServerURL   string    `json:"serverURL"`
	DeviceName  string    `json:"deviceName"`
	NodeID      string    `json:"nodeID,omitempty"` // non-empty when connected

	// Warning is the security-hardening addendum's Safeguard 2 surfacing:
	// the mediaRoots grace-period notice ("mediaRoots is not configured")
	// or the most recent rejected settings push's reason. Net-new field —
	// this reaches only an operator with local or tray access to this
	// specific node; it does NOT reach an operator working from the
	// server's web UI on a headless node (a container, a remote/borrowed
	// GPU box) — that gap is closed by the fuller correlation-ID ack, a
	// deferred Follow-up, not this field.
	Warning string `json:"warning,omitempty"`
}

// statusServer exposes GET /status on localhost:port so the tray app can poll
// the daemon's current lifecycle state without any auth or file I/O.
type statusServer struct {
	mu   sync.RWMutex
	snap statusSnapshot
	cfg  *NodeConfig
}

func newStatusServer(cfg *NodeConfig) *statusServer {
	return &statusServer{
		cfg: cfg,
		snap: statusSnapshot{
			State:      stateDisconnected,
			ServerURL:  cfg.ServerURL,
			DeviceName: cfg.NodeName,
		},
	}
}

// update transitions the daemon state and updates the snapshot atomically.
// pairingCode is only meaningful when state == statePending.
// nodeID is only meaningful when state == stateConnected.
func (s *statusServer) update(state nodeState, pairingCode, nodeID string) {
	s.mu.Lock()
	s.snap.State = state
	s.snap.PairingCode = pairingCode
	s.snap.NodeID = nodeID
	s.snap.ServerURL = s.cfg.ServerURL
	s.snap.DeviceName = s.cfg.NodeName
	s.mu.Unlock()
}

// setWarning records the security-hardening addendum's most recent
// Safeguard 2 notice (the mediaRoots grace-period warning, or a rejected
// settings push's reason) for display via GET /status. Independent of
// update() — a warning persists across connection-state transitions (e.g.
// a reconnect) until superseded by a newer call, rather than being cleared
// on every state change.
func (s *statusServer) setWarning(msg string) {
	s.mu.Lock()
	s.snap.Warning = msg
	s.mu.Unlock()
}

// ListenAndServe starts the local HTTP server and blocks until ctx is
// cancelled.
func (s *statusServer) ListenAndServe(ctx context.Context) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /status", func(w http.ResponseWriter, r *http.Request) {
		s.mu.RLock()
		snap := s.snap
		s.mu.RUnlock()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(snap) //nolint:errcheck
	})

	addr := fmt.Sprintf("127.0.0.1:%d", s.cfg.statusPort())
	srv := &http.Server{Addr: addr, Handler: mux}

	go func() {
		<-ctx.Done()
		srv.Shutdown(context.Background()) //nolint:errcheck
	}()

	log.Printf("sakms-node: status server on %s", addr)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Printf("sakms-node: status server: %v", err)
	}
}
