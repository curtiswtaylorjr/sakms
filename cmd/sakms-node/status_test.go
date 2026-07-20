package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestStatusServer_SetWarning_ReflectedInSnapshot(t *testing.T) {
	cfg := &NodeConfig{ServerURL: "https://example.test", NodeName: "test-node"}
	srv := newStatusServer(cfg)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /status", func(w http.ResponseWriter, r *http.Request) {
		snap := srv.snap
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(snap) //nolint:errcheck
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	// Before any warning is set, the field is absent (omitempty).
	resp, err := http.Get(ts.URL + "/status")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var got statusSnapshot
	json.NewDecoder(resp.Body).Decode(&got) //nolint:errcheck
	resp.Body.Close()
	if got.Warning != "" {
		t.Fatalf("expected no warning initially, got %q", got.Warning)
	}

	srv.setWarning("mediaRoots is not configured -- settings pushes are applied unrestricted (grace period)")

	resp2, err := http.Get(ts.URL + "/status")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp2.Body.Close()
	var got2 statusSnapshot
	if err := json.NewDecoder(resp2.Body).Decode(&got2); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if got2.Warning == "" {
		t.Fatal("expected the status endpoint to reflect the set warning, got empty")
	}
}

func TestStatusServer_SetWarning_ClearedByEmptyString(t *testing.T) {
	cfg := &NodeConfig{}
	srv := newStatusServer(cfg)

	srv.setWarning("rejected settings push: some reason")
	srv.setWarning("")

	srv.mu.RLock()
	warning := srv.snap.Warning
	srv.mu.RUnlock()
	if warning != "" {
		t.Errorf("expected setWarning(\"\") to clear the warning, got %q", warning)
	}
}

func TestStatusServer_Update_DoesNotClearWarning(t *testing.T) {
	cfg := &NodeConfig{ServerURL: "https://example.test", NodeName: "test-node"}
	srv := newStatusServer(cfg)

	srv.setWarning("mediaRoots is not configured")
	srv.update(stateConnected, "", "node-abc")

	srv.mu.RLock()
	warning := srv.snap.Warning
	srv.mu.RUnlock()
	if warning == "" {
		t.Error("expected a warning to persist across a connection-state update (e.g. a reconnect), not be silently cleared")
	}
}
