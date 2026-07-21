package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"syscall"
	"testing"
	"time"
)

// --- statusResponse decode extension -------------------------------------

func TestStatusResponseDecode_MediaRootScopesAndWarning(t *testing.T) {
	const body = `{
		"state": "connected",
		"serverUrl": "https://sak.example",
		"deviceName": "node-1",
		"nodeId": "abc",
		"warning": "mediaRoots is not configured",
		"mediaRootScopes": [
			{"path": "/mnt/Movies", "scope": "namespace_scoped"},
			{"path": "/mnt/TV Shows", "scope": "app_level_only"}
		]
	}`
	var s statusResponse
	if err := json.Unmarshal([]byte(body), &s); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if s.Warning != "mediaRoots is not configured" {
		t.Errorf("warning = %q", s.Warning)
	}
	want := []mediaRootStatus{
		{Path: "/mnt/Movies", Scope: "namespace_scoped"},
		{Path: "/mnt/TV Shows", Scope: "app_level_only"},
	}
	if !reflect.DeepEqual(s.MediaRootScopes, want) {
		t.Errorf("scopes = %+v, want %+v", s.MediaRootScopes, want)
	}
}

func TestStatusResponseDecode_FieldsAbsent(t *testing.T) {
	// omitempty means an older daemon (or the grace period) omits both fields;
	// decoding must not error and must leave them zero-valued.
	const body = `{"state":"pending","pairingCode":"ABC123","serverUrl":"","deviceName":"n"}`
	var s statusResponse
	if err := json.Unmarshal([]byte(body), &s); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if s.Warning != "" {
		t.Errorf("warning = %q, want empty", s.Warning)
	}
	if s.MediaRootScopes != nil {
		t.Errorf("scopes = %+v, want nil", s.MediaRootScopes)
	}
	if s.PairingCode != "ABC123" {
		t.Errorf("pairingCode = %q", s.PairingCode)
	}
}

// --- picker fallback ladder ----------------------------------------------

func TestPickDirectory(t *testing.T) {
	// A genuine *exec.ExitError (tool present, user cancelled).
	_, realExit := exec.Command("sh", "-c", "exit 1").Output()
	if realExit == nil {
		t.Fatal("expected exit-1 command to error")
	}

	origRun := runPicker
	origLadder := pickerLadder
	t.Cleanup(func() { runPicker = origRun; pickerLadder = origLadder })
	pickerLadder = []pickerCommand{
		{"zenity", nil},
		{"kdialog", nil},
	}

	t.Run("kdialog fallback when zenity absent", func(t *testing.T) {
		var calls []string
		runPicker = func(name string, args ...string) ([]byte, error) {
			calls = append(calls, name)
			if name == "zenity" {
				return nil, exec.ErrNotFound
			}
			return []byte("/mnt/movies\n"), nil
		}
		path, picked, err := pickDirectory()
		if err != nil || !picked || path != "/mnt/movies" {
			t.Fatalf("got (%q, %v, %v), want (/mnt/movies, true, nil)", path, picked, err)
		}
		if !reflect.DeepEqual(calls, []string{"zenity", "kdialog"}) {
			t.Errorf("calls = %v, want [zenity kdialog]", calls)
		}
	})

	t.Run("no picker installed", func(t *testing.T) {
		runPicker = func(name string, args ...string) ([]byte, error) {
			return nil, exec.ErrNotFound
		}
		_, picked, err := pickDirectory()
		if picked || err != errNoPicker {
			t.Fatalf("got (picked=%v, err=%v), want (false, errNoPicker)", picked, err)
		}
	})

	t.Run("cancel stops the ladder", func(t *testing.T) {
		var calls []string
		runPicker = func(name string, args ...string) ([]byte, error) {
			calls = append(calls, name)
			if name == "zenity" {
				return nil, realExit // present but non-zero → cancelled
			}
			return []byte("/should/not/reach"), nil
		}
		path, picked, err := pickDirectory()
		if picked || err != nil || path != "" {
			t.Fatalf("got (%q, %v, %v), want (\"\", false, nil)", path, picked, err)
		}
		if len(calls) != 1 || calls[0] != "zenity" {
			t.Errorf("calls = %v, want [zenity] only (cancel must not try kdialog)", calls)
		}
	})

	t.Run("empty selection is a no-op", func(t *testing.T) {
		runPicker = func(name string, args ...string) ([]byte, error) {
			return []byte("   \n"), nil
		}
		path, picked, err := pickDirectory()
		if picked || err != nil || path != "" {
			t.Fatalf("got (%q, %v, %v), want (\"\", false, nil)", path, picked, err)
		}
	})
}

// --- dial-error classification -------------------------------------------

// wrapDial reproduces the exact wrapping http.Client produces for a failed
// unix-socket dial: *url.Error → *net.OpError → *os.SyscallError → syscall.Errno.
func wrapDial(errno syscall.Errno) error {
	return &url.Error{
		Op:  "Get",
		URL: "http://sakms-node/mediaroots",
		Err: &net.OpError{
			Op:  "dial",
			Net: "unix",
			Err: os.NewSyscallError("connect", errno),
		},
	}
}

func TestClassifyDialError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want dialErrorKind
	}{
		{"nil", nil, dialErrOther},
		{"permission denied (EACCES)", wrapDial(syscall.EACCES), dialErrPermission},
		{"socket absent (ENOENT)", wrapDial(syscall.ENOENT), dialErrNotExist},
		{"connection refused is other", wrapDial(syscall.ECONNREFUSED), dialErrOther},
		{"plain error is other", fmt.Errorf("boom"), dialErrOther},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := classifyDialError(c.err); got != c.want {
				t.Errorf("classifyDialError = %v, want %v", got, c.want)
			}
		})
	}
}

// --- drift signal ---------------------------------------------------------

func TestContainmentDrift(t *testing.T) {
	cases := []struct {
		name   string
		scopes []mediaRootStatus
		want   bool
	}{
		{"empty", nil, false},
		{"pure app-level (no containment on node)", []mediaRootStatus{
			{Path: "/a", Scope: "app_level_only"},
			{Path: "/b", Scope: "app_level_only"},
		}, false},
		{"all namespace_scoped (in sync)", []mediaRootStatus{
			{Path: "/a", Scope: "namespace_scoped"},
		}, false},
		{"mixed → drift", []mediaRootStatus{
			{Path: "/a", Scope: "namespace_scoped"},
			{Path: "/b", Scope: "app_level_only"},
		}, true},
		{"unbound counts as active → drift", []mediaRootStatus{
			{Path: "/a", Scope: "namespace_scoped_but_unbound"},
			{Path: "/b", Scope: "app_level_only"},
		}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := containmentDrift(c.scopes); got != c.want {
				t.Errorf("containmentDrift = %v, want %v", got, c.want)
			}
		})
	}
}

// --- control client round-trip over a real unix socket -------------------

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

	client := newControlClient(socketPath)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	roots, err := client.getRoots(ctx)
	if err != nil || !reflect.DeepEqual(roots, []string{"/a"}) {
		t.Fatalf("getRoots = (%v, %v), want ([/a], nil)", roots, err)
	}

	roots, err = client.addRoot(ctx, "/b")
	if err != nil || !reflect.DeepEqual(roots, []string{"/a", "/b"}) {
		t.Fatalf("addRoot = (%v, %v), want ([/a /b], nil)", roots, err)
	}

	roots, err = client.removeRoot(ctx, "/a")
	if err != nil || len(roots) != 0 {
		t.Fatalf("removeRoot = (%v, %v), want ([], nil)", roots, err)
	}

	_, err = client.addRoot(ctx, "/bad")
	if err == nil || err.Error() != "not a directory" {
		t.Fatalf("addRoot(/bad) error = %v, want \"not a directory\"", err)
	}
}

// TestControlClient_DialFailureClassifiable proves a real dial failure through
// the client is still classifiable via classifyDialError (the whole point of
// returning the error unwrapped from do()).
func TestControlClient_DialFailureClassifiable(t *testing.T) {
	dir := t.TempDir()
	client := newControlClient(filepath.Join(dir, "does-not-exist.sock"))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := client.getRoots(ctx)
	if err == nil {
		t.Fatal("expected dial error against a nonexistent socket")
	}
	if got := classifyDialError(err); got != dialErrNotExist {
		t.Errorf("classifyDialError(real ENOENT dial) = %v, want dialErrNotExist", got)
	}
}

func writeRoots(w http.ResponseWriter, status int, roots []string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(controlResponse{MediaRoots: roots})
}
