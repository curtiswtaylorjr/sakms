package nodecontrol

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

// --- mediaRoots control client round-trip over a real unix socket ---------

func TestControlClient_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	socketPath := filepath.Join(dir, "control.sock")

	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /mediaroots", func(w http.ResponseWriter, r *http.Request) {
		writeRoots(w, http.StatusOK, []string{"/a"})
	})
	mux.HandleFunc("POST /mediaroots/add", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Path string `json:"path"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.Path == "/bad" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(controlResponse{Error: "not a directory"})
			return
		}
		writeRoots(w, http.StatusOK, []string{"/a", req.Path})
	})
	mux.HandleFunc("POST /mediaroots/remove", func(w http.ResponseWriter, r *http.Request) {
		writeRoots(w, http.StatusOK, []string{})
	})

	srv := &http.Server{Handler: mux}
	go srv.Serve(ln) //nolint:errcheck
	t.Cleanup(func() { _ = srv.Close() })

	client := NewClient(socketPath)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	roots, err := client.GetRoots(ctx)
	if err != nil || !reflect.DeepEqual(roots, []string{"/a"}) {
		t.Fatalf("GetRoots = (%v, %v), want ([/a], nil)", roots, err)
	}

	roots, err = client.AddRoot(ctx, "/b")
	if err != nil || !reflect.DeepEqual(roots, []string{"/a", "/b"}) {
		t.Fatalf("AddRoot = (%v, %v), want ([/a /b], nil)", roots, err)
	}

	roots, err = client.RemoveRoot(ctx, "/a")
	if err != nil || len(roots) != 0 {
		t.Fatalf("RemoveRoot = (%v, %v), want ([], nil)", roots, err)
	}

	_, err = client.AddRoot(ctx, "/bad")
	if err == nil || err.Error() != "not a directory" {
		t.Fatalf("AddRoot(/bad) error = %v, want \"not a directory\"", err)
	}
}

// TestControlClient_DialFailureClassifiable proves a real dial failure through
// the client is still classifiable via ClassifyDialError (the whole point of
// returning the error unwrapped from do()).
func TestControlClient_DialFailureClassifiable(t *testing.T) {
	dir := t.TempDir()
	client := NewClient(filepath.Join(dir, "does-not-exist.sock"))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := client.GetRoots(ctx)
	if err == nil {
		t.Fatal("expected dial error against a nonexistent socket")
	}
	if got := ClassifyDialError(err); got != DialErrNotExist {
		t.Errorf("ClassifyDialError(real ENOENT dial) = %v, want DialErrNotExist", got)
	}
}

func writeRoots(w http.ResponseWriter, status int, roots []string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(controlResponse{MediaRoots: roots})
}

// --- pathmap control client round-trip over a real unix socket ------------

func TestPathMapControlClient_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	socketPath := filepath.Join(dir, "control.sock")

	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /pathmap", func(w http.ResponseWriter, r *http.Request) {
		writePathMap(w, http.StatusOK, PathMapView{
			AuthoredPaths:   []AuthoredMapping{{Key: "movies_library_root_folder", NodePath: "/mnt/movies"}},
			LibraryPathKeys: []string{"movies_library_root_folder", "series_library_root_folder"},
			LastPushError:   "",
		})
	})
	mux.HandleFunc("POST /pathmap/set", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Key       string `json:"key"`
			LocalPath string `json:"localPath"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.LocalPath == "/" {
			writePathMap(w, http.StatusBadRequest, PathMapView{Error: "path is too shallow"})
			return
		}
		// set echo omits the catalog, like the daemon's writePathMapState.
		writePathMap(w, http.StatusOK, PathMapView{
			AuthoredPaths: []AuthoredMapping{{Key: req.Key, NodePath: req.LocalPath}},
		})
	})
	mux.HandleFunc("POST /pathmap/clear", func(w http.ResponseWriter, r *http.Request) {
		writePathMap(w, http.StatusOK, PathMapView{AuthoredPaths: nil})
	})

	srv := &http.Server{Handler: mux}
	go srv.Serve(ln) //nolint:errcheck
	t.Cleanup(func() { _ = srv.Close() })

	client := NewClient(socketPath)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	view, err := client.GetPathMap(ctx)
	if err != nil {
		t.Fatalf("GetPathMap: %v", err)
	}
	if !reflect.DeepEqual(view.LibraryPathKeys, []string{"movies_library_root_folder", "series_library_root_folder"}) {
		t.Errorf("catalog = %v", view.LibraryPathKeys)
	}
	if len(view.AuthoredPaths) != 1 || view.AuthoredPaths[0].NodePath != "/mnt/movies" {
		t.Errorf("authored = %+v", view.AuthoredPaths)
	}

	view, err = client.SetPathMap(ctx, "movies_library_root_folder", "/mnt/movies")
	if err != nil {
		t.Fatalf("SetPathMap: %v", err)
	}
	if len(view.AuthoredPaths) != 1 || view.AuthoredPaths[0].Key != "movies_library_root_folder" {
		t.Errorf("set echo authored = %+v", view.AuthoredPaths)
	}

	if _, err = client.ClearPathMap(ctx, "movies_library_root_folder"); err != nil {
		t.Fatalf("ClearPathMap: %v", err)
	}

	// A daemon-side rejection (400 with an error body) surfaces as an error.
	_, err = client.SetPathMap(ctx, "movies_library_root_folder", "/")
	if err == nil || err.Error() != "path is too shallow" {
		t.Fatalf("SetPathMap(/) error = %v, want \"path is too shallow\"", err)
	}
}

func writePathMap(w http.ResponseWriter, status int, v PathMapView) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// --- dispatch-pause control client round-trip over a real unix socket -----

func TestDispatchPauseControlClient_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	socketPath := filepath.Join(dir, "control.sock")

	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	// paused holds the fake daemon's authoritative state; the fake also models the
	// failed-push rollback: a set of `true` here is rejected (500 + rolled-back
	// value) to exercise the error path.
	paused := false
	mux := http.NewServeMux()
	mux.HandleFunc("GET /dispatch/pause", func(w http.ResponseWriter, r *http.Request) {
		writeDispatch(w, http.StatusOK, DispatchPauseView{Paused: paused})
	})
	mux.HandleFunc("POST /dispatch/pause", func(w http.ResponseWriter, r *http.Request) {
		var req DispatchPauseView
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.Paused {
			// Simulate a failed relay: the daemon rolled the optimistic flip back
			// to the authoritative (running) value and returns a non-2xx + error.
			writeDispatch(w, http.StatusBadGateway, DispatchPauseView{Paused: false, Error: "server rejected pause: status 500"})
			return
		}
		paused = req.Paused
		writeDispatch(w, http.StatusOK, DispatchPauseView{Paused: paused})
	})

	srv := &http.Server{Handler: mux}
	go srv.Serve(ln) //nolint:errcheck
	t.Cleanup(func() { _ = srv.Close() })

	client := NewClient(socketPath)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// GET reflects the daemon's current state.
	view, err := client.GetDispatchPause(ctx)
	if err != nil || view.Paused {
		t.Fatalf("GetDispatchPause = (%+v, %v), want (paused=false, nil)", view, err)
	}

	// A successful set (resume, i.e. paused=false) returns the new value.
	view, err = client.SetDispatchPause(ctx, false)
	if err != nil {
		t.Fatalf("SetDispatchPause(false): %v", err)
	}
	if view.Paused {
		t.Errorf("set echo = %+v, want paused=false", view)
	}

	// A failed set surfaces the daemon's error AND the rolled-back value, so the
	// caller can notify and display the true (running) state.
	view, err = client.SetDispatchPause(ctx, true)
	if err == nil || err.Error() != "server rejected pause: status 500" {
		t.Fatalf("SetDispatchPause(true) error = %v, want the daemon's rejection message", err)
	}
	if view.Paused {
		t.Errorf("failed set should still carry the rolled-back paused=false, got %+v", view)
	}
}

func writeDispatch(w http.ResponseWriter, status int, v DispatchPauseView) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
