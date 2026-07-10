package rename

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/curtiswtaylorjr/sakms/internal/identify"
	"github.com/curtiswtaylorjr/sakms/internal/mode"
	"github.com/curtiswtaylorjr/sakms/internal/proposals"
	"github.com/curtiswtaylorjr/sakms/internal/servarr"
	"github.com/curtiswtaylorjr/sakms/internal/stashapi"
	"github.com/curtiswtaylorjr/sakms/internal/stashbox"
	"github.com/curtiswtaylorjr/sakms/internal/throttle"
)

// countingAI counts ChatJSON calls and always returns resp — lets tests
// assert whether the legacy AI/text pipeline actually ran.
type countingAI struct {
	calls int
	resp  map[string]any
}

func (a *countingAI) ChatJSON(ctx context.Context, prompt string) (map[string]any, error) {
	a.calls++
	return a.resp, nil
}

// sceneJSON renders a StashFile fixture into the raw shape Stash's own
// findScenes query returns, for fakeStash below.
func sceneJSON(path string, f *stashapi.StashFile) map[string]any {
	fps := []map[string]any{}
	if f.PHash != "" {
		fps = append(fps, map[string]any{"type": "phash", "value": f.PHash})
	}
	return map[string]any{
		"id": f.SceneID, "title": f.Title, "date": f.Date,
		"studio":    map[string]any{"name": f.Studio},
		"stash_ids": []any{},
		"files": []map[string]any{{
			"path": path, "width": f.Width, "height": f.Height, "duration": f.Duration,
			"video_codec": f.VideoCodec, "bit_rate": f.BitRate, "fingerprints": fps,
		}},
	}
}

// fakeStash stands in for a local Stash instance: FindSceneInfoByPath(s),
// ScanPaths, and JobStatus/WaitJob. files is keyed by path — a missing or
// PHash=="" entry means "no phash yet". onScan (if set) runs synchronously
// with whatever paths a targeted rescan was triggered for, letting tests
// simulate Stash discovering a phash mid-run. failLoad makes every
// findScenes-shaped query return a GraphQL error, simulating an unreachable
// Stash instance.
type fakeStash struct {
	t         *testing.T
	files     map[string]*stashapi.StashFile
	failLoad  bool
	scanCalls [][]string
	onScan    func(paths []string)
}

func newFakeStash(t *testing.T, f *fakeStash) *stashapi.Client {
	t.Helper()
	f.t = t
	srv := httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(srv.Close)
	return stashapi.New(stashapi.Config{URL: srv.URL, APIKey: "k"}, srv.Client())
}

func (f *fakeStash) handle(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Query     string                     `json:"query"`
		Variables map[string]json.RawMessage `json:"variables"`
	}
	json.NewDecoder(r.Body).Decode(&req)
	w.Header().Set("Content-Type", "application/json")

	switch {
	case strings.Contains(req.Query, "ScanPaths"):
		var input struct {
			Paths []string `json:"paths"`
		}
		json.Unmarshal(req.Variables["input"], &input)
		f.scanCalls = append(f.scanCalls, input.Paths)
		if f.onScan != nil {
			f.onScan(input.Paths)
		}
		fmt.Fprint(w, `{"data":{"metadataScan":"job1"}}`)
	case strings.Contains(req.Query, "FindJob"):
		fmt.Fprint(w, `{"data":{"findJob":{"status":"FINISHED"}}}`)
	case f.failLoad:
		fmt.Fprint(w, `{"errors":[{"message":"stash unreachable"}]}`)
	case strings.Contains(req.Query, "BatchFindByPath"):
		data := map[string]any{}
		for key, raw := range req.Variables {
			if !strings.HasPrefix(key, "p") {
				continue
			}
			var path string
			json.Unmarshal(raw, &path)
			scenes := []any{}
			if file := f.files[path]; file != nil {
				scenes = append(scenes, sceneJSON(path, file))
			}
			data["s"+strings.TrimPrefix(key, "p")] = map[string]any{"scenes": scenes}
		}
		body, _ := json.Marshal(map[string]any{"data": data})
		w.Write(body)
	case strings.Contains(req.Query, "FindByPath("):
		var path string
		json.Unmarshal(req.Variables["path"], &path)
		scenes := []any{}
		if file := f.files[path]; file != nil {
			scenes = append(scenes, sceneJSON(path, file))
		}
		body, _ := json.Marshal(map[string]any{"data": map[string]any{"findScenes": map[string]any{"scenes": scenes}}})
		w.Write(body)
	default:
		f.t.Fatalf("unexpected stash query: %s", req.Query)
	}
}

// newFakeAdultBox stands in for one stash-box's findScenesBySceneFingerprints
// endpoint, keyed by phash. A missing key means no match. Reimplemented here
// (rather than shared with internal/identify's own fingerprint test fake)
// since that one is unexported to its own package.
func newFakeAdultBox(t *testing.T, results map[string]struct{ id, title string }) *stashbox.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Variables struct {
				FPs [][]map[string]string `json:"fps"`
			} `json:"variables"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		w.Header().Set("Content-Type", "application/json")
		matches := make([][]map[string]any, len(req.Variables.FPs))
		for i, fp := range req.Variables.FPs {
			hash := fp[0]["hash"]
			if scene, ok := results[hash]; ok {
				matches[i] = []map[string]any{{"id": scene.id, "title": scene.title, "release_date": "", "studio": map[string]any{"name": ""}}}
			} else {
				matches[i] = []map[string]any{}
			}
		}
		body, _ := json.Marshal(map[string]any{"data": map[string]any{"findScenesBySceneFingerprints": matches}})
		w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return stashbox.New(stashbox.Config{Endpoint: srv.URL, APIKey: "k", HasVoteField: true}, srv.Client())
}

// adultTestSession builds a Whisparr *mode.Session wired for the phash-first
// pipeline. The fake Servarr handler fails the test if it's ever called —
// scanAdultPhashFirst and its legacy fallback (proposeOneAdult) never touch
// the *arr app; that's Apply's job, not Scan's.
func adultTestSession(t *testing.T, stash *stashapi.Client, ai *countingAI, boxes map[string]*stashbox.Client) *mode.Session {
	t.Helper()
	sess := newTestSession(t, servarr.Whisparr, func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("must never call the *arr app during Scan, got %s %s", r.Method, r.URL.Path)
	})
	sess.Stash = stash
	var aiClient identify.AIClient
	if ai != nil {
		aiClient = ai
	}
	sess.Identify = &identify.Identifier{
		AI:       aiClient,
		GiveBack: identify.NewGiveBack(boxes),
		Throttle: throttle.New(0),
	}
	return sess
}

func TestScanAdultPhashFirst_CascadeHit_SkipsAIEntirely(t *testing.T) {
	stash := newFakeStash(t, &fakeStash{files: map[string]*stashapi.StashFile{
		"/media/Adult/scene1.mp4": {PHash: "hash1", Duration: 1800},
	}})
	stashdb := newFakeAdultBox(t, map[string]struct{ id, title string }{
		"hash1": {id: "box-scene-1", title: "Cascade Scene"},
	})
	ai := &countingAI{}
	sess := adultTestSession(t, stash, ai, map[string]*stashbox.Client{"stashdb": stashdb})

	candidates := []adultCandidate{{
		root: servarr.RootFolder{Path: "/media/Adult"},
		uf:   servarr.UnmappedFolder{Name: "scene1.mp4", Path: "/media/Adult/scene1.mp4"},
	}}
	out := scanAdultPhashFirst(context.Background(), sess, candidates, nil, []servarr.QualityProfile{{ID: 4}})
	if len(out) != 1 {
		t.Fatalf("expected 1 proposal, got %d: %+v", len(out), out)
	}
	p := out[0]
	if p.Status != proposals.Pending || p.Title != "Cascade Scene" || p.ForeignID != "box-scene-1" {
		t.Fatalf("expected a fingerprint-cascade hit, got %+v", p)
	}
	if p.GiveBackBox != "stashdb" || p.GiveBackSceneID != "box-scene-1" {
		t.Errorf("expected give-back target captured from the cascade match, got box=%q scene=%q", p.GiveBackBox, p.GiveBackSceneID)
	}
	if p.PHash != "hash1" || p.DurationSeconds != 1800 {
		t.Errorf("expected phash/duration captured from Stash, got phash=%q duration=%d", p.PHash, p.DurationSeconds)
	}
	if ai.calls != 0 {
		t.Errorf("expected the AI/text pipeline to never run on a cascade hit, got %d calls", ai.calls)
	}
}

func TestScanAdultPhashFirst_CascadeMiss_FallsThroughToProposeOneAdult(t *testing.T) {
	stash := newFakeStash(t, &fakeStash{files: map[string]*stashapi.StashFile{
		"/media/Adult/scene1.mp4": {PHash: "hash1", Duration: 1800},
	}})
	stashdb := newFakeAdultBox(t, nil) // no match anywhere
	ai := &countingAI{resp: map[string]any{"studio": nil, "title": nil, "year": nil, "performers": nil}}
	sess := adultTestSession(t, stash, ai, map[string]*stashbox.Client{"stashdb": stashdb})

	candidates := []adultCandidate{{
		root: servarr.RootFolder{Path: "/media/Adult"},
		uf:   servarr.UnmappedFolder{Name: "scene1.mp4", Path: "/media/Adult/scene1.mp4"},
	}}
	out := scanAdultPhashFirst(context.Background(), sess, candidates, nil, nil)
	if len(out) != 1 {
		t.Fatalf("expected 1 proposal, got %d: %+v", len(out), out)
	}
	if out[0].Status != proposals.Unmatched {
		t.Fatalf("expected a cascade miss to fall through to the legacy pipeline and end up Unmatched, got %+v", out[0])
	}
	if ai.calls == 0 {
		t.Error("expected the legacy AI/text pipeline to actually run on a cascade miss")
	}
}

func TestScanAdultPhashFirst_NoPhashPromotedViaForceGenerate(t *testing.T) {
	f := &fakeStash{files: map[string]*stashapi.StashFile{
		"/media/Adult/scene1.mp4": {}, // no phash yet
	}}
	f.onScan = func(paths []string) {
		// Simulate Stash finishing phash generation mid-scan.
		f.files["/media/Adult/scene1.mp4"] = &stashapi.StashFile{PHash: "hash1", Duration: 900}
	}
	stash := newFakeStash(t, f)
	stashdb := newFakeAdultBox(t, map[string]struct{ id, title string }{
		"hash1": {id: "box-scene-2", title: "Promoted Scene"},
	})
	ai := &countingAI{}
	sess := adultTestSession(t, stash, ai, map[string]*stashbox.Client{"stashdb": stashdb})

	candidates := []adultCandidate{{
		root: servarr.RootFolder{Path: "/media/Adult"},
		uf:   servarr.UnmappedFolder{Name: "scene1.mp4", Path: "/media/Adult/scene1.mp4"},
	}}
	out := scanAdultPhashFirst(context.Background(), sess, candidates, nil, nil)
	if len(out) != 1 || out[0].Status != proposals.Pending || out[0].Title != "Promoted Scene" {
		t.Fatalf("expected a force-generated phash to resolve via the cascade, got %+v", out)
	}
	if len(f.scanCalls) != 1 || len(f.scanCalls[0]) != 1 || f.scanCalls[0][0] != "/media/Adult/scene1.mp4" {
		t.Fatalf("expected exactly one targeted rescan covering the missing path, got %+v", f.scanCalls)
	}
	if ai.calls != 0 {
		t.Errorf("expected no legacy fallback once the force-generated phash resolved, got %d AI calls", ai.calls)
	}
}

func TestScanAdultPhashFirst_StashLoadError_FailsOpenToLegacyPath(t *testing.T) {
	stash := newFakeStash(t, &fakeStash{failLoad: true})
	ai := &countingAI{resp: map[string]any{"studio": nil, "title": nil, "year": nil, "performers": nil}}
	sess := adultTestSession(t, stash, ai, nil)

	candidates := []adultCandidate{{
		root: servarr.RootFolder{Path: "/media/Adult"},
		uf:   servarr.UnmappedFolder{Name: "scene1.mp4", Path: "/media/Adult/scene1.mp4"},
	}}
	out := scanAdultPhashFirst(context.Background(), sess, candidates, nil, nil)
	if len(out) != 1 || out[0].Status != proposals.Unmatched {
		t.Fatalf("expected Stash being unreachable to fail open to the legacy pipeline, got %+v", out)
	}
	if ai.calls == 0 {
		t.Error("expected the legacy pipeline to actually run when Stash is unreachable")
	}
}
