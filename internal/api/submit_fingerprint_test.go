package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/curtiswtaylorjr/sakms/internal/mode"
	"github.com/curtiswtaylorjr/sakms/internal/proposals"
)

// TestSubmitFingerprintHandler_GivesAppliedProposalBackAfterStashGetsAPHash
// proves the end-to-end retry wiring: an Applied Adult Rename proposal whose
// phash wasn't known at Apply time now has one in Stash, POST
// /api/proposals/{id}/submit-fingerprint reaches the configured stash-box
// give-back target, and FingerprintSubmittedAt persists afterward.
func TestSubmitFingerprintHandler_GivesAppliedProposalBackAfterStashGetsAPHash(t *testing.T) {
	fakeWhisparr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("submit-fingerprint must never call the *arr app, got %s %s", r.Method, r.URL.Path)
	}))
	defer fakeWhisparr.Close()

	fakeStash := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Query     string                     `json:"query"`
			Variables map[string]json.RawMessage `json:"variables"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		w.Header().Set("Content-Type", "application/json")
		if !strings.Contains(req.Query, "FindByPath(") {
			t.Fatalf("expected only a FindByPath query, got: %s", req.Query)
		}
		var path string
		json.Unmarshal(req.Variables["path"], &path)
		if path != "/media/Adult/Some Scene.mp4" {
			fmt.Fprint(w, `{"data":{"findScenes":{"scenes":[]}}}`)
			return
		}
		fmt.Fprint(w, `{"data":{"findScenes":{"scenes":[{
			"id":"scene1","title":"Some Scene","date":"2024",
			"studio":{"name":"Some Studio"},"stash_ids":[],
			"files":[{"path":"/media/Adult/Some Scene.mp4","width":0,"height":0,"duration":1800,
			"video_codec":"","bit_rate":0,"fingerprints":[{"type":"phash","value":"hash1"}]}]
		}]}}}`)
	}))
	defer fakeStash.Close()

	var gotSceneID, gotHash string
	fakeStashDB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Variables struct {
				Input struct {
					SceneID     string `json:"scene_id"`
					Fingerprint struct {
						Hash string `json:"hash"`
					} `json:"fingerprint"`
				} `json:"input"`
			} `json:"variables"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		gotSceneID, gotHash = req.Variables.Input.SceneID, req.Variables.Input.Fingerprint.Hash
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"data":{"submitFingerprint":true}}`)
	}))
	defer fakeStashDB.Close()

	fakeOllama := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("Ollama must not be called by submit-fingerprint")
	}))
	defer fakeOllama.Close()

	connStore, propStore, allowStore, settingsStore, grabsStore, libStore := testStores(t)
	ctx := context.Background()
	for _, c := range []struct{ service, url string }{
		{"whisparr", fakeWhisparr.URL},
		{"stash", fakeStash.URL},
		{"stashdb", fakeStashDB.URL},
		{"ollama", fakeOllama.URL},
	} {
		if err := connStore.Upsert(ctx, c.service, c.url, "test-key"); err != nil {
			t.Fatalf("seeding %s connection: %v", c.service, err)
		}
	}
	if err := settingsStore.Set(ctx, mode.AIModelKey, "test-model"); err != nil {
		t.Fatalf("seeding ollama model: %v", err)
	}

	saved, err := propStore.ReplacePending(ctx, mode.Adult, proposals.Rename, []proposals.Proposal{
		{
			Status: proposals.Pending, SourceName: "Some Scene", SourcePath: "/media/Adult/Some Scene.mp4",
			RootFolderPath: "/media/Adult", Title: "Some Scene",
			ForeignID: "abc-uuid", ItemType: "scene",
			GiveBackBox: "stashdb", GiveBackSceneID: "abc-uuid",
		},
	})
	if err != nil {
		t.Fatalf("seeding proposal: %v", err)
	}
	// Mark it Applied directly (bypassing a real Apply round trip against
	// Whisparr) — submit-fingerprint only cares that the proposal is Applied
	// with a give-back target and no fingerprint submitted yet.
	if err := propStore.MarkApplied(ctx, saved[0].ID, 55); err != nil {
		t.Fatalf("marking proposal applied: %v", err)
	}

	srv := httptest.NewServer(NewMux(testHTTPClient(), connStore, propStore, allowStore, testProber(t), testPHasher(t), settingsStore, grabsStore, libStore))
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/api/proposals/"+strconv.FormatInt(saved[0].ID, 10)+"/submit-fingerprint", "application/json", nil)
	if err != nil {
		t.Fatalf("submit-fingerprint POST failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var updated proposals.Proposal
	if err := json.NewDecoder(resp.Body).Decode(&updated); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if updated.FingerprintSubmittedAt == "" {
		t.Fatalf("expected FingerprintSubmittedAt to persist, got %+v", updated)
	}
	if gotSceneID != "abc-uuid" || gotHash != "hash1" {
		t.Fatalf("expected the freshly-read phash to reach stashdb's give-back mutation, got sceneID=%q hash=%q", gotSceneID, gotHash)
	}
}

// A Pending proposal hasn't been applied yet — nothing to give back.
func TestSubmitFingerprintHandler_RejectsPendingProposal(t *testing.T) {
	fakeWhisparr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("must not call the *arr app")
	}))
	defer fakeWhisparr.Close()

	connStore, propStore, allowStore, settingsStore, grabsStore, libStore := testStores(t)
	ctx := context.Background()
	if err := connStore.Upsert(ctx, "whisparr", fakeWhisparr.URL, "test-key"); err != nil {
		t.Fatalf("seeding whisparr connection: %v", err)
	}

	saved, err := propStore.ReplacePending(ctx, mode.Adult, proposals.Rename, []proposals.Proposal{
		{Status: proposals.Pending, SourceName: "X", Title: "X", ForeignID: "abc", ItemType: "scene"},
	})
	if err != nil {
		t.Fatalf("seeding proposal: %v", err)
	}

	srv := httptest.NewServer(NewMux(testHTTPClient(), connStore, propStore, allowStore, testProber(t), testPHasher(t), settingsStore, grabsStore, libStore))
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/api/proposals/"+strconv.FormatInt(saved[0].ID, 10)+"/submit-fingerprint", "application/json", nil)
	if err != nil {
		t.Fatalf("submit-fingerprint POST failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		t.Fatal("expected submit-fingerprint to reject a Pending proposal")
	}
}
