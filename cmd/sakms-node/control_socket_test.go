//go:build linux

package main

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// testGroup returns a real local group the test process belongs to (its own
// primary group), plus that group's gid. serveControlSocket chgrp's the runtime
// dir + socket to this gid; using the process's own group means the unprivileged
// chown succeeds without root, exactly as the production daemon's chown to
// sakms-media-config succeeds via SupplementaryGroups.
func testGroup(t *testing.T) (name string, gid int) {
	t.Helper()
	u, err := user.Current()
	if err != nil {
		t.Fatalf("user.Current: %v", err)
	}
	g, err := user.LookupGroupId(u.Gid)
	if err != nil {
		t.Fatalf("LookupGroupId(%s): %v", u.Gid, err)
	}
	gidInt, err := strconv.Atoi(u.Gid)
	if err != nil {
		t.Fatalf("parsing gid %q: %v", u.Gid, err)
	}
	return g.Name, gidInt
}

// startTestSocket launches serveControlSocket on a temp path with the test
// user's own group, waits until the socket is connectable, and returns an HTTP
// client that dials it plus the socket path. The listener is torn down via the
// returned context cancel (t.Cleanup).
func startTestSocket(t *testing.T, cfg *NodeConfig, configPath string) (*http.Client, string, string) {
	t.Helper()
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "control.sock")
	groupName, _ := testGroup(t)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go serveControlSocket(ctx, cfg, configPath, dir, sockPath, groupName)

	client := &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", sockPath)
		},
	}}

	// Poll until the socket accepts a connection (serveControlSocket provisions
	// asynchronously in a goroutine).
	deadline := time.Now().Add(3 * time.Second)
	for {
		c, err := net.Dial("unix", sockPath)
		if err == nil {
			c.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("control socket never became connectable: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	return client, sockPath, dir
}

func getRoots(t *testing.T, client *http.Client) []string {
	t.Helper()
	resp, err := client.Get("http://unix/mediaroots")
	if err != nil {
		t.Fatalf("GET /mediaroots: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /mediaroots status = %d, want 200", resp.StatusCode)
	}
	var out mediaRootsPayload
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decoding GET response: %v", err)
	}
	return out.MediaRoots
}

func postJSON(t *testing.T, client *http.Client, method, url string, body mediaRootsPayload) (int, mediaRootsPayload) {
	t.Helper()
	buf, _ := json.Marshal(body)
	req, err := http.NewRequest(method, url, strings.NewReader(string(buf)))
	if err != nil {
		t.Fatalf("building request: %v", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer resp.Body.Close()
	var out mediaRootsPayload
	json.NewDecoder(resp.Body).Decode(&out) //nolint:errcheck
	return resp.StatusCode, out
}

func TestControlSocket_AddGetRemove_RoundTrip(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(t.TempDir(), "config.json")
	cfg := &NodeConfig{ServerURL: "https://example.test", NodeName: "n"}
	client, _, _ := startTestSocket(t, cfg, configPath)

	if roots := getRoots(t, client); len(roots) != 0 {
		t.Fatalf("expected empty mediaRoots initially, got %v", roots)
	}

	status, out := postJSON(t, client, http.MethodPost, "http://unix/mediaroots/add", mediaRootsPayload{Path: root})
	if status != http.StatusOK {
		t.Fatalf("add valid dir: status %d (%s)", status, out.Error)
	}
	wantResolved, _ := filepath.EvalSymlinks(root)
	if len(out.MediaRoots) != 1 || out.MediaRoots[0] != wantResolved {
		t.Fatalf("expected canonicalized root %q in response, got %v", wantResolved, out.MediaRoots)
	}

	// Live in-memory cfg reflects it immediately (no restart).
	if _, live := cfg.snapshot(); len(live) != 1 || live[0] != wantResolved {
		t.Fatalf("expected cfg.MediaRoots updated in-memory, got %v", live)
	}

	// Persisted to config.json.
	persisted := readPersistedRoots(t, configPath)
	if len(persisted) != 1 || persisted[0] != wantResolved {
		t.Fatalf("expected config.json to carry %q, got %v", wantResolved, persisted)
	}

	// Adding the same root again is idempotent.
	status, out = postJSON(t, client, http.MethodPost, "http://unix/mediaroots/add", mediaRootsPayload{Path: root})
	if status != http.StatusOK || len(out.MediaRoots) != 1 {
		t.Fatalf("re-adding same root should be idempotent, got status %d roots %v", status, out.MediaRoots)
	}

	// Remove it (tray echoes back the stored canonical form).
	status, out = postJSON(t, client, http.MethodPost, "http://unix/mediaroots/remove", mediaRootsPayload{Path: wantResolved})
	if status != http.StatusOK || len(out.MediaRoots) != 0 {
		t.Fatalf("remove: status %d roots %v", status, out.MediaRoots)
	}
}

func TestControlSocket_RejectsInvalidPaths(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	cfg := &NodeConfig{ServerURL: "https://example.test", NodeName: "n"}
	client, _, _ := startTestSocket(t, cfg, configPath)

	file := filepath.Join(t.TempDir(), "afile")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	cases := map[string]string{
		"relative":      "not/absolute",
		"nonexistent":   filepath.Join(t.TempDir(), "missing"),
		"non-directory": file,
		"empty":         "",
	}
	for name, path := range cases {
		status, out := postJSON(t, client, http.MethodPost, "http://unix/mediaroots/add", mediaRootsPayload{Path: path})
		if status != http.StatusBadRequest {
			t.Errorf("%s: expected 400, got %d (%s)", name, status, out.Error)
		}
		if out.Error == "" {
			t.Errorf("%s: expected a non-empty error message", name)
		}
	}
	// None of the rejects mutated anything.
	if _, live := cfg.snapshot(); len(live) != 0 {
		t.Fatalf("rejected paths must not mutate mediaRoots, got %v", live)
	}
}

func TestControlSocket_SetWholeList_AllOrNothing(t *testing.T) {
	a := t.TempDir()
	b := t.TempDir()
	configPath := filepath.Join(t.TempDir(), "config.json")
	cfg := &NodeConfig{ServerURL: "https://example.test", NodeName: "n", MediaRoots: []string{"/pre/existing"}}
	client, _, _ := startTestSocket(t, cfg, configPath)

	// One bad entry rejects the whole set — pre-existing list stays intact.
	status, _ := postJSON(t, client, http.MethodPut, "http://unix/mediaroots", mediaRootsPayload{Roots: []string{a, "relative"}})
	if status != http.StatusBadRequest {
		t.Fatalf("expected a set with one bad path to be rejected, got %d", status)
	}
	if _, live := cfg.snapshot(); len(live) != 1 || live[0] != "/pre/existing" {
		t.Fatalf("a rejected set must leave the prior list intact, got %v", live)
	}

	// All valid → applied as canonicalized list.
	status, out := postJSON(t, client, http.MethodPut, "http://unix/mediaroots", mediaRootsPayload{Roots: []string{a, b}})
	if status != http.StatusOK || len(out.MediaRoots) != 2 {
		t.Fatalf("expected a fully-valid set to apply, got status %d roots %v", status, out.MediaRoots)
	}
}

// TestControlSocket_PermsAndGroup asserts Option A actually produced the
// load-bearing authorization state: the socket is mode 0660 group = shared, the
// runtime dir is group = shared, and config.json's group is untouched.
func TestControlSocket_PermsAndGroup(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	cfg := &NodeConfig{ServerURL: "https://example.test", NodeName: "n"}
	client, sockPath, runtimeDir := startTestSocket(t, cfg, configPath)

	// Force a config.json to exist so we can assert its group is undisturbed.
	if _, out := postJSON(t, client, http.MethodPost, "http://unix/mediaroots/add", mediaRootsPayload{Path: t.TempDir()}); out.Error != "" {
		t.Fatalf("add for perms setup: %s", out.Error)
	}

	_, wantGID := testGroup(t)

	sockInfo, err := os.Stat(sockPath)
	if err != nil {
		t.Fatalf("stat socket: %v", err)
	}
	if perm := sockInfo.Mode().Perm(); perm != 0o660 {
		t.Errorf("socket mode = %o, want 0660", perm)
	}
	if gid := int(sockInfo.Sys().(*syscall.Stat_t).Gid); gid != wantGID {
		t.Errorf("socket gid = %d, want %d (the shared group)", gid, wantGID)
	}

	dirInfo, err := os.Stat(runtimeDir)
	if err != nil {
		t.Fatalf("stat runtime dir: %v", err)
	}
	if gid := int(dirInfo.Sys().(*syscall.Stat_t).Gid); gid != wantGID {
		t.Errorf("runtime dir gid = %d, want %d (the shared group)", gid, wantGID)
	}

	// config.json (written by cfg.save via mutateAndSave) keeps its 0600 mode —
	// Option A never touches it (it lives outside the runtime dir and the daemon
	// primary group is unchanged).
	cfgInfo, err := os.Stat(configPath)
	if err != nil {
		t.Fatalf("stat config.json: %v", err)
	}
	if perm := cfgInfo.Mode().Perm(); perm != 0o600 {
		t.Errorf("config.json mode = %o, want 0600 (Option A must not perturb it)", perm)
	}
}

// TestControlSocket_UnlinkStaleSocketOnRestart proves the unlink-before-Listen
// step makes the socket immediately usable after an unclean shutdown left a
// stale socket file (the SIGKILL / dev-run / RuntimeDirectoryPreserve case) —
// rather than net.Listen failing with "address already in use".
func TestControlSocket_UnlinkStaleSocketOnRestart(t *testing.T) {
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "control.sock")
	groupName, _ := testGroup(t)

	// Simulate a stale socket left behind: bind, disable unlink-on-close, close.
	stale, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("creating stale socket: %v", err)
	}
	stale.(*net.UnixListener).SetUnlinkOnClose(false)
	stale.Close()

	// Sanity: a naive Listen at the same path now fails (the file is still there).
	if l, err := net.Listen("unix", sockPath); err == nil {
		l.Close()
		t.Fatal("expected the stale socket file to block a naive net.Listen")
	}

	configPath := filepath.Join(t.TempDir(), "config.json")
	cfg := &NodeConfig{ServerURL: "https://example.test", NodeName: "n"}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go serveControlSocket(ctx, cfg, configPath, dir, sockPath, groupName)

	client := &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", sockPath)
		},
	}}

	// The daemon must have unlinked the stale file and rebound; the endpoint is
	// immediately usable.
	deadline := time.Now().Add(3 * time.Second)
	for {
		resp, err := client.Get("http://unix/mediaroots")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("socket not usable after unlink-before-Listen restart: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestControlSocket_AddDoesNotHoldLockDuringSlowResolve is the regression
// test for the hang risk a critic review found: containsPath/removePath used
// to call resolvePathBestEffort (which wraps filepath.EvalSymlinks) on every
// EXISTING stored mediaRoots entry from *inside* mutateAndSave's write-lock
// section. mediaRoots are typically CIFS mounts on this deployment (mounted
// hard, not soft), so resolving a dead one can block in uninterruptible
// sleep indefinitely — and since that happened under cfg.mu.Lock(), every
// other lock user (executeJob, executeBrowse, GET /status — all via
// cfg.snapshot(), an RLock) would hang too, freezing the whole daemon.
//
// This test overrides the resolveRootPath seam so resolving one pre-existing
// stored root ("blocks" until released) simulates that dead mount, while
// adding a DIFFERENT, unrelated root. It asserts a concurrent cfg.snapshot()
// read completes immediately while the resolve is still stuck — proving the
// lock is not held during path resolution — then releases the resolve and
// confirms the add still completes successfully.
func TestControlSocket_AddDoesNotHoldLockDuringSlowResolve(t *testing.T) {
	const slowRoot = "/simulated/dead-cifs-mount"

	release := make(chan struct{})
	entered := make(chan struct{})
	var enteredOnce sync.Once
	orig := resolveRootPath
	resolveRootPath = func(path string) string {
		if path == slowRoot {
			enteredOnce.Do(func() { close(entered) })
			<-release
		}
		return orig(path)
	}
	t.Cleanup(func() { resolveRootPath = orig })

	configPath := filepath.Join(t.TempDir(), "config.json")
	cfg := &NodeConfig{ServerURL: "https://example.test", NodeName: "n", MediaRoots: []string{slowRoot}}
	client, _, _ := startTestSocket(t, cfg, configPath)

	newRoot := t.TempDir()

	addDone := make(chan struct{})
	go func() {
		defer close(addDone)
		buf, _ := json.Marshal(mediaRootsPayload{Path: newRoot})
		req, err := http.NewRequest(http.MethodPost, "http://unix/mediaroots/add", strings.NewReader(string(buf)))
		if err != nil {
			t.Errorf("building add request: %v", err)
			return
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Errorf("add request: %v", err)
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("add: status %d", resp.StatusCode)
		}
	}()

	// Wait until the add request is actually blocked resolving the slow root
	// (the pre-mutation snapshot resolve, which must happen before any lock
	// is taken).
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("add never reached the slow resolve — test seam not exercised")
	}

	// While that resolve is still stuck, a concurrent cfg.snapshot() — what
	// executeJob/executeBrowse/GET-/status all call — must return immediately.
	// If it doesn't, the resolve is still happening inside the config lock.
	snapshotDone := make(chan struct{})
	go func() {
		defer close(snapshotDone)
		cfg.snapshot()
	}()
	select {
	case <-snapshotDone:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("cfg.snapshot() blocked while an existing root's resolution was stuck — the config lock is held during path resolution")
	}

	close(release)

	select {
	case <-addDone:
	case <-time.After(3 * time.Second):
		t.Fatal("add never completed after releasing the slow resolve")
	}

	if _, live := cfg.snapshot(); len(live) != 2 {
		t.Fatalf("expected both the slow pre-existing root and the newly added root, got %v", live)
	}
}

func readPersistedRoots(t *testing.T, configPath string) []string {
	t.Helper()
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("reading config.json: %v", err)
	}
	var persisted NodeConfig
	if err := json.Unmarshal(data, &persisted); err != nil {
		t.Fatalf("unmarshalling config.json: %v", err)
	}
	return persisted.MediaRoots
}
