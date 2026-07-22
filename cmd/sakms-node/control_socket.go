//go:build linux

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/user"
	"strconv"
	"syscall"
)

// Production locations for the local mediaRoots control socket. The runtime dir
// is provisioned by systemd (RuntimeDirectory=sakms-node, RuntimeDirectoryMode=
// 0750) and the shared group by the RPM (sysusers.d "g sakms-media-config -");
// see packaging/rpm/sakms-node.service and the mediaRoots-UI plan's
// socket-perms/Option-A section. Kept as vars (not consts) only so tests can
// point serveControlSocket at a temp dir + the test user's own group.
const (
	controlRuntimeDir  = "/run/sakms-node"
	controlSocketPath  = "/run/sakms-node/control.sock"
	controlSocketGroup = "sakms-media-config"
)

// peerCredKey is the context key under which the accepted connection's real
// peer credentials (captured via SO_PEERCRED at Accept time) are threaded into
// each HTTP handler for attribution logging. Group membership is the
// authorization boundary (the kernel admits only sakms-media-config members to
// connect() the 0660/group socket); this captured uid is NOT an access-control
// gate — it is recorded on every accepted write purely as a queryable
// drift/misconfiguration tripwire (see the plan's "Attribution value" section).
type peerCredKey struct{}

// startControlSocket launches the local mediaRoots control socket for the
// process lifetime. It MUST be started once in main() alongside statusSrv (NOT
// nested inside run()/connect(), which re-enter on every reconnect) and is
// governed by the top-level process context: on SIGTERM the context cancels and
// the listener drains in-flight writes. The socket file itself is left to /run
// tmpfs teardown on graceful stop and unlink()ed before the next Listen to
// cover unclean exits.
func startControlSocket(ctx context.Context, cfg *NodeConfig, configPath string, pusher *pathmapPusher, sess *nodeSession) {
	serveControlSocket(ctx, cfg, configPath, controlRuntimeDir, controlSocketPath, controlSocketGroup, pusher, sess)
}

// serveControlSocket is the testable core: it provisions the socket at
// socketPath inside runtimeDir, group-owned by groupName, then serves the
// control HTTP API until ctx is cancelled. It never crashes the daemon — the
// control socket is an optional local-config convenience, so any provisioning
// failure is logged and the function returns, leaving the node's core hashing
// path unaffected. All failure modes here are fail-closed: the socket only ever
// becomes reachable to shared-group members AFTER the chown/chmod lands.
func serveControlSocket(ctx context.Context, cfg *NodeConfig, configPath, runtimeDir, socketPath, groupName string, pusher *pathmapPusher, sess *nodeSession) {
	grp, err := user.LookupGroup(groupName)
	if err != nil {
		log.Printf("sakms-node: control socket disabled: group %q not found (%v) — set mediaRoots by editing the config file", groupName, err)
		return
	}
	gid, err := strconv.Atoi(grp.Gid)
	if err != nil {
		log.Printf("sakms-node: control socket disabled: group %q has non-numeric gid %q", groupName, grp.Gid)
		return
	}

	// systemd creates runtimeDir with RuntimeDirectory=, but a manual/dev run
	// may not have it; create it best-effort. RuntimeDirectoryMode=0750 carries
	// no setgid bit, so the daemon must chgrp the dir to the shared group itself
	// for group members to traverse into it (Option A step ii). This succeeds
	// unprivileged because the daemon owns the dir and is a member of the group
	// (SupplementaryGroups=sakms-media-config).
	if err := os.MkdirAll(runtimeDir, 0o750); err != nil {
		log.Printf("sakms-node: control socket: creating runtime dir %s: %v", runtimeDir, err)
		return
	}
	if err := os.Chown(runtimeDir, -1, gid); err != nil {
		log.Printf("sakms-node: control socket: chgrp runtime dir %s to gid %d: %v", runtimeDir, gid, err)
	}

	// Remove any stale socket left by an unclean crash (SIGKILL), a dev run, or
	// a RuntimeDirectoryPreserve edge case, so Listen does not fail with
	// "address already in use" — do not rely on systemd's graceful-stop cleanup.
	if err := os.Remove(socketPath); err != nil && !os.IsNotExist(err) {
		log.Printf("sakms-node: control socket: removing stale socket %s: %v", socketPath, err)
	}

	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		log.Printf("sakms-node: control socket: listen on %s: %v", socketPath, err)
		return
	}
	unixLn, ok := ln.(*net.UnixListener)
	if !ok {
		log.Printf("sakms-node: control socket: unexpected listener type %T", ln)
		ln.Close() //nolint:errcheck
		return
	}

	// net.Listen creates the socket owned sakms-node:sakms-node with a
	// umask-derived mode — NOT 0660 group=shared. Produce that authorization
	// state explicitly (Option A step iii): chgrp to the shared group and chmod
	// 0660 so exactly shared-group members can connect(). The brief window
	// between Listen and here is fail-closed (default mode has no group-write
	// bit for a non-sakms-node-group user).
	if err := os.Chown(socketPath, -1, gid); err != nil {
		log.Printf("sakms-node: control socket: chgrp socket to gid %d: %v", gid, err)
	}
	if err := os.Chmod(socketPath, 0o660); err != nil {
		log.Printf("sakms-node: control socket: chmod socket 0660: %v", err)
	}

	srv := &http.Server{
		Handler: controlMux(cfg, configPath, pusher, sess),
		ConnContext: func(connCtx context.Context, c net.Conn) context.Context {
			if pc, ok := c.(*peerCredConn); ok && pc.ucred != nil {
				return context.WithValue(connCtx, peerCredKey{}, pc.ucred)
			}
			return connCtx
		},
	}

	go func() {
		<-ctx.Done()
		srv.Shutdown(context.Background()) //nolint:errcheck
	}()

	log.Printf("sakms-node: control socket on %s (group %s, gid %d)", socketPath, groupName, gid)
	if err := srv.Serve(peerCredListener{unixLn}); err != nil && err != http.ErrServerClosed {
		log.Printf("sakms-node: control socket: serve: %v", err)
	}
}

// peerCredListener wraps a unix listener so each accepted connection carries the
// peer's SO_PEERCRED credentials, captured at Accept time (the only moment the
// connecting peer's identity is unambiguous).
type peerCredListener struct {
	*net.UnixListener
}

func (l peerCredListener) Accept() (net.Conn, error) {
	c, err := l.AcceptUnix()
	if err != nil {
		return nil, err
	}
	ucred, credErr := readPeerCred(c)
	if credErr != nil {
		// Attribution is best-effort: a failure to read the peer credential must
		// not drop an otherwise-authorized (group-gated) connection.
		log.Printf("sakms-node: control socket: reading peer credentials: %v", credErr)
	}
	return &peerCredConn{UnixConn: c, ucred: ucred}, nil
}

// peerCredConn carries the captured peer credentials alongside the connection so
// ConnContext can thread them into handlers.
type peerCredConn struct {
	*net.UnixConn
	ucred *syscall.Ucred
}

// readPeerCred reads the connecting peer's real uid/gid/pid from the kernel via
// SO_PEERCRED — unspoofable, unlike anything the peer could send in-band.
func readPeerCred(c *net.UnixConn) (*syscall.Ucred, error) {
	raw, err := c.SyscallConn()
	if err != nil {
		return nil, err
	}
	var ucred *syscall.Ucred
	var sockErr error
	if ctrlErr := raw.Control(func(fd uintptr) {
		ucred, sockErr = syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED)
	}); ctrlErr != nil {
		return nil, ctrlErr
	}
	return ucred, sockErr
}

// peerUID renders the attributed peer uid for logging, or "unknown" if the
// credential could not be captured.
func peerUID(ctx context.Context) string {
	if uc, ok := ctx.Value(peerCredKey{}).(*syscall.Ucred); ok && uc != nil {
		return fmt.Sprintf("uid=%d", uc.Uid)
	}
	return "uid=unknown"
}

// mediaRootsPayload is the request/response body for the control API. Requests
// use Path (add/remove) or Roots (set whole list); responses always carry the
// resulting MediaRoots so the tray can reflect the new state without a restart.
type mediaRootsPayload struct {
	Path       string   `json:"path,omitempty"`
	Roots      []string `json:"roots,omitempty"`
	MediaRoots []string `json:"mediaRoots,omitempty"`
	Error      string   `json:"error,omitempty"`
}

// controlMux wires the mediaRoots control routes. GET /status deliberately does
// NOT live here — it stays on the existing TCP status server; only the
// security-sensitive write path (and its companion get) moves to this
// group-gated, browser-unreachable socket.
func controlMux(cfg *NodeConfig, configPath string, pusher *pathmapPusher, sess *nodeSession) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /mediaroots", func(w http.ResponseWriter, r *http.Request) {
		_, roots := cfg.snapshot()
		writeMediaRoots(w, http.StatusOK, roots)
	})
	mux.HandleFunc("POST /mediaroots/add", func(w http.ResponseWriter, r *http.Request) {
		handleMediaRootsAdd(w, r, cfg, configPath)
	})
	mux.HandleFunc("POST /mediaroots/remove", func(w http.ResponseWriter, r *http.Request) {
		handleMediaRootsRemove(w, r, cfg, configPath)
	})
	mux.HandleFunc("PUT /mediaroots", func(w http.ResponseWriter, r *http.Request) {
		handleMediaRootsSet(w, r, cfg, configPath)
	})
	// Stage 2 node-authored path-mapping routes (control_pathmap.go).
	registerPathMapRoutes(mux, cfg, configPath, pusher, sess)
	return mux
}

func handleMediaRootsAdd(w http.ResponseWriter, r *http.Request, cfg *NodeConfig, configPath string) {
	req, err := decodeMediaRootsPayload(r)
	if err != nil {
		rejectMediaRoots(w, r, "add", err)
		return
	}
	canonical, err := validateMediaRootPath(req.Path)
	if err != nil {
		rejectMediaRoots(w, r, "add", err)
		return
	}
	// Resolve the pre-mutation snapshot's entries via resolveRootsCache OUTSIDE
	// cfg.mu — see its doc for why: mediaRoots are typically CIFS mounts on
	// this deployment (mounted hard, not soft), and resolving one whose
	// backing mount is down can block in uninterruptible sleep indefinitely.
	// Doing that inside mutateAndSave's write-lock section would freeze every
	// other lock user too (executeJob/executeBrowse/GET-/status all call
	// cfg.snapshot(), which RLocks the same mutex). containsPathCached then
	// re-checks the LIVE cfg.MediaRoots inside the lock using this cache, so a
	// concurrent add/set landing between this snapshot and the lock below is
	// still handled correctly — see containsPathCached's doc.
	_, existing := cfg.snapshot()
	cache := resolveRootsCache(existing)
	target := resolveRootPath(canonical)

	var result []string
	if saveErr := cfg.mutateAndSave(configPath, func() {
		if !containsPathCached(cfg.MediaRoots, canonical, target, cache) {
			cfg.MediaRoots = append(append([]string(nil), cfg.MediaRoots...), canonical)
		}
	}); saveErr != nil {
		rejectMediaRoots(w, r, "add", saveErr)
		return
	}
	_, result = cfg.snapshot()
	log.Printf("sakms-node: control socket: added mediaRoot %q (%s) — now %d root(s)", canonical, peerUID(r.Context()), len(result))
	writeMediaRoots(w, http.StatusOK, result)
}

func handleMediaRootsRemove(w http.ResponseWriter, r *http.Request, cfg *NodeConfig, configPath string) {
	req, err := decodeMediaRootsPayload(r)
	if err != nil {
		rejectMediaRoots(w, r, "remove", err)
		return
	}
	if req.Path == "" {
		rejectMediaRoots(w, r, "remove", fmt.Errorf("path is empty"))
		return
	}
	// Same rationale as handleMediaRootsAdd above: resolve the pre-mutation
	// snapshot outside cfg.mu, then re-check the live list inside it.
	_, existing := cfg.snapshot()
	cache := resolveRootsCache(existing)
	resolvedTarget := resolveRootPath(req.Path)

	var result []string
	if saveErr := cfg.mutateAndSave(configPath, func() {
		cfg.MediaRoots = removePathCached(cfg.MediaRoots, req.Path, resolvedTarget, cache)
	}); saveErr != nil {
		rejectMediaRoots(w, r, "remove", saveErr)
		return
	}
	_, result = cfg.snapshot()
	log.Printf("sakms-node: control socket: removed mediaRoot %q (%s) — now %d root(s)", req.Path, peerUID(r.Context()), len(result))
	writeMediaRoots(w, http.StatusOK, result)
}

func handleMediaRootsSet(w http.ResponseWriter, r *http.Request, cfg *NodeConfig, configPath string) {
	req, err := decodeMediaRootsPayload(r)
	if err != nil {
		rejectMediaRoots(w, r, "set", err)
		return
	}
	// Validate and canonicalize the WHOLE list before touching cfg: a set is
	// all-or-nothing, so one bad path rejects the entire request rather than
	// leaving a partially-applied allowlist.
	canonical := make([]string, 0, len(req.Roots))
	for _, raw := range req.Roots {
		c, verr := validateMediaRootPath(raw)
		if verr != nil {
			rejectMediaRoots(w, r, "set", verr)
			return
		}
		if !containsPath(canonical, c) {
			canonical = append(canonical, c)
		}
	}
	var result []string
	if saveErr := cfg.mutateAndSave(configPath, func() {
		cfg.MediaRoots = canonical
	}); saveErr != nil {
		rejectMediaRoots(w, r, "set", saveErr)
		return
	}
	_, result = cfg.snapshot()
	log.Printf("sakms-node: control socket: set mediaRoots to %d root(s) (%s)", len(result), peerUID(r.Context()))
	writeMediaRoots(w, http.StatusOK, result)
}

func decodeMediaRootsPayload(r *http.Request) (mediaRootsPayload, error) {
	var req mediaRootsPayload
	if r.Body == nil {
		return req, nil
	}
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(&req); err != nil {
		return req, fmt.Errorf("decoding request body: %w", err)
	}
	return req, nil
}

// rejectMediaRoots logs a daemon-side validation rejection attributed to the
// captured peer uid (distinct from a successful mutation, per the plan's
// observability requirements) and returns 400.
func rejectMediaRoots(w http.ResponseWriter, r *http.Request, op string, err error) {
	log.Printf("sakms-node: control socket: rejected mediaRoots %s (%s): %v", op, peerUID(r.Context()), err)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	json.NewEncoder(w).Encode(mediaRootsPayload{Error: err.Error()}) //nolint:errcheck
}

func writeMediaRoots(w http.ResponseWriter, status int, roots []string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(mediaRootsPayload{MediaRoots: roots}) //nolint:errcheck
}

// resolveRootPath is resolvePathBestEffort (mediaroots.go) by indirection
// through a var rather than a direct call, purely as a test seam: tests
// override it to simulate a slow/blocking resolution (e.g. a dead CIFS
// mount) to prove the resolve work below runs outside cfg.mu.
var resolveRootPath = resolvePathBestEffort

// containsPath reports whether canonical is already present in roots,
// comparing on the best-effort resolved form so a symlinked or
// trailing-slash duplicate of an existing root is not stored twice. Used
// only for deduping within a single not-yet-stored candidate list (the
// PUT /mediaroots "set" path, handleMediaRootsSet) — never against
// cfg.MediaRoots directly, so it is safe to resolve inline here without a
// lock-holding concern.
func containsPath(roots []string, canonical string) bool {
	target := resolveRootPath(canonical)
	for _, r := range roots {
		if r == canonical || resolveRootPath(r) == target {
			return true
		}
	}
	return false
}

// resolveRootsCache resolves every entry of roots to its best-effort-resolved
// form via resolveRootPath. Callers MUST invoke this on a pre-mutation
// snapshot (cfg.snapshot()) OUTSIDE cfg.mu: mediaRoots are typically CIFS
// mounts on this deployment, mounted `hard` not `soft`, so resolving one
// whose backing mount is down can block in uninterruptible sleep
// indefinitely. Doing that while holding cfg.mu.Lock() (as the add/remove
// handlers used to, resolving inside their mutateAndSave closures) would
// freeze every other lock user too — executeJob, executeBrowse, and the GET
// /status handler all call cfg.snapshot(), which RLocks the same mutex.
func resolveRootsCache(roots []string) map[string]string {
	cache := make(map[string]string, len(roots))
	for _, r := range roots {
		cache[r] = resolveRootPath(r)
	}
	return cache
}

// containsPathCached reports whether canonical (whose already-computed
// resolved form is target) is already present in roots. cache holds the
// resolved form of every entry that existed in the pre-mutation snapshot
// passed to resolveRootsCache. An entry in roots absent from cache can only
// have been appended, after that snapshot was taken, by a concurrent
// add/set request landing between the snapshot and the mutateAndSave lock —
// and both of those write paths always store an already
// EvalSymlinks-canonicalized path (validateMediaRootPath), so an
// exact-string compare against it correctly detects a match without doing
// any new filesystem work while cfg.mu is held.
func containsPathCached(roots []string, canonical, target string, cache map[string]string) bool {
	for _, r := range roots {
		if r == canonical {
			return true
		}
		if resolved, ok := cache[r]; ok && resolved == target {
			return true
		}
	}
	return false
}

// removePathCached drops every entry matching target either exactly (the
// stored canonical form the tray echoes back) or, via cache, by resolved
// form — mirroring removePath's old semantics but without resolving any
// filesystem path itself. See containsPathCached's doc for why consulting
// cache (rather than re-resolving) is still correct against a concurrent
// writer landing between the pre-mutation snapshot and the mutateAndSave
// critical section.
func removePathCached(roots []string, target, resolvedTarget string, cache map[string]string) []string {
	out := make([]string, 0, len(roots))
	for _, r := range roots {
		if r == target {
			continue
		}
		if resolved, ok := cache[r]; ok && resolved == resolvedTarget {
			continue
		}
		out = append(out, r)
	}
	return out
}
