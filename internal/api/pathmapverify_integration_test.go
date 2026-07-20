package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/labbersanon/sakms/internal/apidto"
	"github.com/labbersanon/sakms/internal/nodes"
	"github.com/labbersanon/sakms/internal/nodesettings"
)

// connectFakeNode connects a fake node into reg that answers every browse
// request with entries named after names, and returns its durable id.
func connectFakeNode(t *testing.T, reg *nodes.Registry, nodeID string, names []string) {
	t.Helper()
	_, _, browse, disconnect := reg.Connect(nodeID, nodeID, nil)
	t.Cleanup(disconnect)

	go func() {
		for req := range browse {
			entries := make([]nodes.BrowseEntry, 0, len(names))
			for _, n := range names {
				entries = append(entries, nodes.BrowseEntry{Name: n, Path: filepath.Join(req.Path, n)})
			}
			reg.ReportBrowseResult(nodes.BrowseResult{RequestID: req.ID, Entries: entries})
		}
	}()
}

func TestUpdateNodeSettings_MismatchedMapping_RejectedAndNotPersisted(t *testing.T) {
	mux, reg, _, settingsStore, nodeSettingsStore, _, apiKey := testNodesMux(t)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	ctx := context.Background()
	if err := settingsStore.Set(ctx, string(apidto.LibraryPathMoviesRoot), "/data/movies"); err != nil {
		t.Fatalf("settingsStore.Set: %v", err)
	}
	connectFakeNode(t, reg, "node-a", []string{"Downloads", "Torrents", "Cache"}) // unrelated to server's Movie A/B/C

	serverDir := t.TempDir()
	for _, name := range []string{"Movie A", "Movie B", "Movie C"} {
		if err := os.Mkdir(filepath.Join(serverDir, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := settingsStore.Set(ctx, string(apidto.LibraryPathMoviesRoot), serverDir); err != nil {
		t.Fatalf("settingsStore.Set: %v", err)
	}

	// Pre-existing good state, to confirm it survives a rejected save untouched.
	if err := nodeSettingsStore.Set(ctx, "node-a", nodesettings.Settings{
		PathMappings: []nodesettings.PathMappingEntry{
			{LibraryPathKey: string(apidto.LibraryPathMoviesRoot), NodePath: "/mnt/original-good-value", VerificationStatus: nodesettings.VerificationVerified},
		},
		MaxJobs: 3,
	}); err != nil {
		t.Fatalf("pre-seed nodeSettingsStore.Set: %v", err)
	}

	body, _ := json.Marshal(apidto.NodeSettingsRequest{
		PathMap: []apidto.NodePathMappingInput{
			{Key: apidto.LibraryPathMoviesRoot, NodePath: "/mnt/wrong-directory"},
		},
		MaxJobs: 5,
	})
	req, _ := http.NewRequest(http.MethodPut, srv.URL+"/api/nodes/node-a/settings", bytes.NewReader(body))
	req.Header.Set("X-Api-Key", apiKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 for a mismatched mapping, got %d", resp.StatusCode)
	}

	got, ok, err := nodeSettingsStore.Get(ctx, "node-a")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !ok || len(got.PathMappings) != 1 {
		t.Fatalf("expected the pre-existing record to survive, got ok=%v %+v", ok, got)
	}
	if got.PathMappings[0].NodePath != "/mnt/original-good-value" {
		t.Errorf("expected the rejected save to leave the old value in place, got %q", got.PathMappings[0].NodePath)
	}
	if got.MaxJobs != 3 {
		t.Errorf("expected MaxJobs to remain 3 (rejected save's MaxJobs=5 must not persist either), got %d", got.MaxJobs)
	}
}

func TestUpdateNodeSettings_GoodMapping_SavesAndPersistsVerified(t *testing.T) {
	mux, reg, _, settingsStore, nodeSettingsStore, _, apiKey := testNodesMux(t)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	ctx := context.Background()
	serverDir := t.TempDir()
	for _, name := range []string{"Movie A", "Movie B", "Movie C"} {
		if err := os.Mkdir(filepath.Join(serverDir, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := settingsStore.Set(ctx, string(apidto.LibraryPathMoviesRoot), serverDir); err != nil {
		t.Fatalf("settingsStore.Set: %v", err)
	}
	connectFakeNode(t, reg, "node-a", []string{"Movie A", "Movie B", "Movie C"})

	body, _ := json.Marshal(apidto.NodeSettingsRequest{
		PathMap: []apidto.NodePathMappingInput{
			{Key: apidto.LibraryPathMoviesRoot, NodePath: "/mnt/movies"},
		},
		MaxJobs: 2,
	})
	req, _ := http.NewRequest(http.MethodPut, srv.URL+"/api/nodes/node-a/settings", bytes.NewReader(body))
	req.Header.Set("X-Api-Key", apiKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("expected 204 for a matching mapping, got %d", resp.StatusCode)
	}

	got, ok, err := nodeSettingsStore.Get(ctx, "node-a")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !ok || len(got.PathMappings) != 1 {
		t.Fatalf("expected the new mapping to be persisted, got ok=%v %+v", ok, got)
	}
	if got.PathMappings[0].NodePath != "/mnt/movies" {
		t.Errorf("got NodePath %q, want /mnt/movies", got.PathMappings[0].NodePath)
	}
	if got.PathMappings[0].VerificationStatus != nodesettings.VerificationVerified {
		t.Errorf("got VerificationStatus %q, want verified", got.PathMappings[0].VerificationStatus)
	}
	if got.PathMappings[0].VerifiedAt == nil {
		t.Error("expected VerifiedAt to be set for a verified row")
	}
}
