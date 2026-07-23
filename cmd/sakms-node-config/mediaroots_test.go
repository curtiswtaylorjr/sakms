//go:build cgo

package main

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"sync"
	"testing"

	"fyne.io/fyne/v2/test"

	"github.com/labbersanon/sakms/internal/nodecontrol"
)

// recordedRequest captures one control-socket request the fake daemon received,
// so a test can assert the correct endpoint + arguments were issued.
type recordedRequest struct {
	Method string
	Path   string
	Body   string
}

// recorder is a concurrency-safe log of the requests a fake daemon received.
type recorder struct {
	mu   sync.Mutex
	reqs []recordedRequest
}

func (r *recorder) add(req recordedRequest) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reqs = append(r.reqs, req)
}

func (r *recorder) all() []recordedRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]recordedRequest, len(r.reqs))
	copy(out, r.reqs)
	return out
}

// startFakeDaemon spins up an http.Server on a temp unix socket and returns a
// control client dialing it, mirroring internal/nodecontrol's own test pattern.
func startFakeDaemon(t *testing.T, handler http.Handler) *nodecontrol.Client {
	t.Helper()
	dir := t.TempDir()
	sock := filepath.Join(dir, "control.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := &http.Server{Handler: handler}
	go srv.Serve(ln) //nolint:errcheck
	t.Cleanup(func() { _ = srv.Close() })
	return nodecontrol.NewClient(sock)
}

// recordingMux is a fake daemon that records every mediaRoots/pathmap request
// and returns valid-shaped responses so the panels' post-mutation refresh works.
func recordingMux(rec *recorder) http.Handler {
	record := func(w http.ResponseWriter, r *http.Request) map[string]string {
		body, _ := io.ReadAll(r.Body)
		rec.add(recordedRequest{Method: r.Method, Path: r.URL.Path, Body: string(body)})
		var m map[string]string
		_ = json.Unmarshal(body, &m)
		return m
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /mediaroots", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"mediaRoots": []string{}})
	})
	mux.HandleFunc("POST /mediaroots/add", func(w http.ResponseWriter, r *http.Request) {
		m := record(w, r)
		writeJSON(w, http.StatusOK, map[string]any{"mediaRoots": []string{m["path"]}})
	})
	mux.HandleFunc("POST /mediaroots/remove", func(w http.ResponseWriter, r *http.Request) {
		record(w, r)
		writeJSON(w, http.StatusOK, map[string]any{"mediaRoots": []string{}})
	})
	mux.HandleFunc("GET /pathmap", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, nodecontrol.PathMapView{LibraryPathKeys: []string{"movies_library_root_folder"}})
	})
	mux.HandleFunc("POST /pathmap/set", func(w http.ResponseWriter, r *http.Request) {
		m := record(w, r)
		writeJSON(w, http.StatusOK, nodecontrol.PathMapView{
			AuthoredPaths: []nodecontrol.AuthoredMapping{{Key: m["key"], NodePath: m["localPath"]}},
		})
	})
	mux.HandleFunc("POST /pathmap/clear", func(w http.ResponseWriter, r *http.Request) {
		record(w, r)
		writeJSON(w, http.StatusOK, nodecontrol.PathMapView{})
	})
	return mux
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// TestControlCallsIssueCorrectArgs proves the mediaRoots add/remove and the
// path-mapping set/clear panel actions each issue the correct control-socket
// call with the correct arguments, against a recording fake daemon over a real
// unix socket.
func TestControlCallsIssueCorrectArgs(t *testing.T) {
	a := test.NewTempApp(t)
	rec := &recorder{}
	client := startFakeDaemon(t, recordingMux(rec))
	win := a.NewWindow("t")

	roots := newRootsPanel(client, win, func() ([]nodecontrol.MediaRootStatus, error) {
		return nil, nil
	})
	pathmap := newPathmapPanel(client, win)

	roots.doAddRoot("/new/movies")
	roots.doRemoveRoot("/old/series")
	pathmap.doSet("movies_library_root_folder", "/mnt/movies")
	pathmap.doClear("movies_library_root_folder")

	got := rec.all()
	want := []recordedRequest{
		{Method: "POST", Path: "/mediaroots/add", Body: `{"path":"/new/movies"}`},
		{Method: "POST", Path: "/mediaroots/remove", Body: `{"path":"/old/series"}`},
		{Method: "POST", Path: "/pathmap/set", Body: `{"key":"movies_library_root_folder","localPath":"/mnt/movies"}`},
		{Method: "POST", Path: "/pathmap/clear", Body: `{"key":"movies_library_root_folder"}`},
	}
	if len(got) != len(want) {
		t.Fatalf("recorded %d requests, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i].Method != want[i].Method || got[i].Path != want[i].Path {
			t.Errorf("request %d = %s %s, want %s %s", i, got[i].Method, got[i].Path, want[i].Method, want[i].Path)
		}
		if got[i].Body != want[i].Body {
			t.Errorf("request %d body = %s, want %s", i, got[i].Body, want[i].Body)
		}
	}
}
