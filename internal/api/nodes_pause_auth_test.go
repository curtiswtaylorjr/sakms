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

// operatorPausePut issues an operator-authenticated PUT /api/nodes/{urlID}/pause.
func operatorPausePut(t *testing.T, srvURL, urlID, apiKey string, paused bool) *http.Response {
	t.Helper()
	raw, _ := json.Marshal(apidto.NodePauseRequest{Paused: paused})
	req, _ := http.NewRequest(http.MethodPut, srvURL+"/api/nodes/"+urlID+"/pause", bytes.NewReader(raw))
	req.Header.Set("X-Api-Key", apiKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("operator pause PUT failed: %v", err)
	}
	return resp
}

// nodePausePut issues a node-bearer PUT /api/nodes/{urlID}/pause.
func nodePausePut(t *testing.T, srvURL, urlID, rawKey string, paused bool) *http.Response {
	t.Helper()
	raw, _ := json.Marshal(apidto.NodePauseRequest{Paused: paused})
	req, _ := http.NewRequest(http.MethodPut, srvURL+"/api/nodes/"+urlID+"/pause", bytes.NewReader(raw))
	req.Header.Set("Authorization", "Bearer "+rawKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("node pause PUT failed: %v", err)
	}
	return resp
}

// TestUpdateNodePause_OperatorAuth_ExcludesFromDispatchAndPersists is
// acceptance (a): an operator PUT /pause {paused:true} takes the node out of
// the Dispatch candidate set (verified against the registry, not just the HTTP
// status) AND persists the bit (verified via a store read).
func TestUpdateNodePause_OperatorAuth_ExcludesFromDispatchAndPersists(t *testing.T) {
	mux, reg, _, _, nodeSettingsStore, _, apiKey := testNodesMux(t)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	ctx := context.Background()
	// A single connected node — if it is excluded from dispatch, selection falls
	// through to the local fallback (ok=false).
	connectFakeNode(t, reg, "node-a", nil)

	// Sanity: before pausing, the node IS a dispatch candidate.
	if _, _, ok := reg.Dispatch(nodes.Job{ID: "job-0", Type: nodes.JobTypePhash, ServerPath: "/x"}); !ok {
		t.Fatal("expected the connected node to be dispatch-eligible before pausing")
	}

	resp := operatorPausePut(t, srv.URL, "node-a", apiKey, true)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", resp.StatusCode)
	}

	// Registry effect: the paused node is no longer a candidate, so Dispatch
	// returns ok=false (local fallback).
	if id, _, ok := reg.Dispatch(nodes.Job{ID: "job-1", Type: nodes.JobTypePhash, ServerPath: "/x"}); ok {
		t.Fatalf("expected the paused node to be excluded from Dispatch, but it selected %q", id)
	}

	// Persistence: the durable bit is set.
	got, ok, err := nodeSettingsStore.Get(ctx, "node-a")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !ok || !got.PauseDispatch {
		t.Errorf("expected persisted PauseDispatch=true, got ok=%v pause=%v", ok, got.PauseDispatch)
	}
}

// TestUpdateNodePause_NodeAuth_URLIdIgnored_CannotPauseAnotherNode is
// acceptance (b), the security-critical test: node A authenticates with its own
// bearer but puts node B's real id in the URL. A's pause must flip; B's must
// NEVER change — the handler keys strictly by the bearer identity (D2).
func TestUpdateNodePause_NodeAuth_URLIdIgnored_CannotPauseAnotherNode(t *testing.T) {
	mux, reg, _, _, nodeSettingsStore, nodeKeyStore, _ := testNodesMux(t)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	ctx := context.Background()
	idA, rawKeyA, err := nodeKeyStore.Create(ctx, "node-a")
	if err != nil {
		t.Fatalf("nodekeys.Create A: %v", err)
	}
	idB, _, err := nodeKeyStore.Create(ctx, "node-b")
	if err != nil {
		t.Fatalf("nodekeys.Create B: %v", err)
	}
	// A is connected so the live pause + echo can land; B is a real but
	// not-connected node.
	connectFakeNode(t, reg, idA, nil)

	// A authenticates with its own key but targets B's id in the URL.
	resp := nodePausePut(t, srv.URL, idB, rawKeyA, true)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("expected 204 (write lands on A's own row), got %d", resp.StatusCode)
	}

	// A's row is paused.
	gotA, okA, err := nodeSettingsStore.Get(ctx, idA)
	if err != nil {
		t.Fatalf("Get A: %v", err)
	}
	if !okA || !gotA.PauseDispatch {
		t.Errorf("expected A's own pause flipped, got ok=%v pause=%v", okA, gotA.PauseDispatch)
	}

	// B's row must NOT exist / must not be paused — a URL-id spoof cannot touch it.
	if gotB, okB, _ := nodeSettingsStore.Get(ctx, idB); okB && gotB.PauseDispatch {
		t.Fatal("SECURITY: node B's pause was flipped via a URL-id spoof — the handler must key strictly by the bearer identity")
	}

	// Live registry effect confirms A is the one excluded: A is the only
	// connected node and it is now paused, so Dispatch falls back to local.
	if id, _, ok := reg.Dispatch(nodes.Job{ID: "job-1", Type: nodes.JobTypePhash, ServerPath: "/x"}); ok {
		t.Fatalf("expected A (the bearer identity) to be the paused/excluded node, but Dispatch selected %q", id)
	}
}

// TestListNodes_ReportsStoredPauseDispatch is acceptance (c): GET /api/nodes
// surfaces each node's current stored pause state so the frontend can preload
// the toggle and render the "Paused" badge (same preload contract as MaxJobs).
func TestListNodes_ReportsStoredPauseDispatch(t *testing.T) {
	mux, reg, _, _, nodeSettingsStore, _, apiKey := testNodesMux(t)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	connectFakeNode(t, reg, "node-a", nil)

	ctx := context.Background()
	if err := nodeSettingsStore.SetPauseDispatch(ctx, "node-a", true); err != nil {
		t.Fatalf("SetPauseDispatch: %v", err)
	}

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/nodes", nil)
	req.Header.Set("X-Api-Key", apiKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var got apidto.NodesResponse
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if len(got.Nodes) != 1 {
		t.Fatalf("expected 1 node, got %d", len(got.Nodes))
	}
	if !got.Nodes[0].PauseDispatch {
		t.Errorf("expected PauseDispatch=true (the stored value) in GET /api/nodes, got false")
	}
}

// TestUpdateNodeSettings_MaxJobsSave_LeavesPauseUntouched is acceptance (d),
// direction 1: an operator MaxJobs-only PUT /settings must not disturb the
// stored pause bit (the storage-layer footgun, P2 — Set is column-scoped and
// never writes pause_dispatch).
func TestUpdateNodeSettings_MaxJobsSave_LeavesPauseUntouched(t *testing.T) {
	mux, _, _, _, nodeSettingsStore, _, apiKey := testNodesMux(t)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	ctx := context.Background()
	// Node is paused, with an existing MaxJobs.
	if err := nodeSettingsStore.Set(ctx, "node-a", nodesettings.Settings{MaxJobs: 2}); err != nil {
		t.Fatalf("pre-seed MaxJobs: %v", err)
	}
	if err := nodeSettingsStore.SetPauseDispatch(ctx, "node-a", true); err != nil {
		t.Fatalf("pre-seed pause: %v", err)
	}

	body, _ := json.Marshal(apidto.NodeSettingsRequest{MaxJobs: 7})
	req, _ := http.NewRequest(http.MethodPut, srv.URL+"/api/nodes/node-a/settings", bytes.NewReader(body))
	req.Header.Set("X-Api-Key", apiKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", resp.StatusCode)
	}

	got, _, err := nodeSettingsStore.Get(ctx, "node-a")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.MaxJobs != 7 {
		t.Errorf("MaxJobs: got %d, want 7 (operator change)", got.MaxJobs)
	}
	if !got.PauseDispatch {
		t.Errorf("a MaxJobs-only save must leave pause untouched, but PauseDispatch was reset to false")
	}
}

// TestUpdateNodePause_LeavesMaxJobsAndPathMapUntouched is acceptance (d),
// direction 2: a PUT /pause must not disturb the stored MaxJobs or PathMap
// (SetPauseDispatch is column-scoped and touches only pause_dispatch).
func TestUpdateNodePause_LeavesMaxJobsAndPathMapUntouched(t *testing.T) {
	mux, _, _, settingsStore, nodeSettingsStore, _, apiKey := testNodesMux(t)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	ctx := context.Background()
	if err := settingsStore.Set(ctx, string(apidto.LibraryPathMoviesRoot), "/data/movies"); err != nil {
		t.Fatalf("settingsStore.Set: %v", err)
	}
	// Existing MaxJobs + a verified path mapping.
	if err := nodeSettingsStore.Set(ctx, "node-a", nodesettings.Settings{
		PathMappings: []nodesettings.PathMappingEntry{
			{LibraryPathKey: string(apidto.LibraryPathMoviesRoot), NodePath: "/mnt/node-owned", VerificationStatus: nodesettings.VerificationVerified},
		},
		MaxJobs: 4,
	}); err != nil {
		t.Fatalf("pre-seed: %v", err)
	}

	resp := operatorPausePut(t, srv.URL, "node-a", apiKey, true)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", resp.StatusCode)
	}

	got, _, err := nodeSettingsStore.Get(ctx, "node-a")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !got.PauseDispatch {
		t.Errorf("expected PauseDispatch=true after the pause PUT")
	}
	if got.MaxJobs != 4 {
		t.Errorf("MaxJobs: got %d, want 4 (a pause save must not touch MaxJobs)", got.MaxJobs)
	}
	if len(got.PathMappings) != 1 || got.PathMappings[0].NodePath != "/mnt/node-owned" {
		t.Errorf("PathMap must survive a pause save untouched, got %+v", got.PathMappings)
	}
}

// TestUpdateNodePause_PushedFrameCarriesPause is acceptance (e): the
// NodeSettings frame echoed to the node after a pause change carries the new
// pause value, so the tray can display it.
func TestUpdateNodePause_PushedFrameCarriesPause(t *testing.T) {
	mux, reg, _, _, _, _, apiKey := testNodesMux(t)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	settings := connectCapturingNode(t, reg, "node-a", nil)

	resp := operatorPausePut(t, srv.URL, "node-a", apiKey, true)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", resp.StatusCode)
	}

	push := readSettingsPush(t, settings)
	if !push.PauseDispatch {
		t.Errorf("the SSE frame pushed after a pause change must carry PauseDispatch=true, got false")
	}
}

// TestNodeAuthPathMapRepush_OnPausedNode_FrameCarriesStoredPause is acceptance
// (f), the P7 invariant test: on a node that is ALREADY paused, a node-authored
// path-map set triggers the hand-built re-push (the sibling path-mapping
// feature's frame). That frame MUST carry the STORED pause value (true), never
// a zero-value false — otherwise a path-map change would silently clear the
// node's cached pause display. This test fails if that one sender forgets to
// populate PauseDispatch.
func TestNodeAuthPathMapRepush_OnPausedNode_FrameCarriesStoredPause(t *testing.T) {
	mux, reg, _, settingsStore, nodeSettingsStore, nodeKeyStore, _ := testNodesMux(t)
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

	id, rawKey, err := nodeKeyStore.Create(ctx, "node-a")
	if err != nil {
		t.Fatalf("nodekeys.Create: %v", err)
	}
	// The node is ALREADY paused before it authors a path-map change.
	if err := nodeSettingsStore.SetPauseDispatch(ctx, id, true); err != nil {
		t.Fatalf("pre-seed pause: %v", err)
	}

	settings := connectCapturingNode(t, reg, id, []string{"Movie A", "Movie B", "Movie C"})

	// Node authors a single-key path-map set — this runs the verification gate
	// and then the hand-built re-push at the P7 sender site.
	resp := nodeAuthPut(t, srv.URL, id, rawKey, apidto.NodeSettingsRequest{
		PathMap: []apidto.NodePathMappingInput{
			{Key: apidto.LibraryPathMoviesRoot, NodePath: "/mnt/media/movies"},
		},
		MediaRoots: []string{"/mnt/media"},
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", resp.StatusCode)
	}

	push := readSettingsPush(t, settings)
	if !push.PauseDispatch {
		t.Errorf("P7 REGRESSION: the node-auth path-map re-push emitted PauseDispatch=false for a paused node — the stored pause was dropped, which would clear the node's cached pause after any path-map change")
	}
	// Sanity: the path-map change itself still went out on the same frame.
	if len(push.PathMap) != 1 || push.PathMap[0].Local != "/mnt/media/movies" {
		t.Errorf("expected the path-map set to be present in the same frame, got %+v", push.PathMap)
	}
}
