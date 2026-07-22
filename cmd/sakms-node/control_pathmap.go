//go:build linux

package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
)

// pathmapPayload is the request/response body for the /pathmap control routes.
// Requests carry Key (+ LocalPath on a set); responses echo the resulting local
// state so the tray reflects it without a restart.
type pathmapPayload struct {
	Key       string `json:"key,omitempty"`
	LocalPath string `json:"localPath,omitempty"`
	Error     string `json:"error,omitempty"`
}

// pathmapState is GET /pathmap's response: the node-authored mappings, the
// server-authoritative Remap table, the bounded library-path-key catalog (from
// the last ConnectAck, D4), and the most recent push-failure notice (Stage 3
// surfacing). Enough for the future tray to render configured vs unconfigured
// keys and a "last push failed" line.
type pathmapState struct {
	AuthoredPaths   []AuthoredPathMapping `json:"authoredPaths"`
	PathMap         []PathMapEntry        `json:"pathMap"`
	LibraryPathKeys []string              `json:"libraryPathKeys"`
	LastPushError   string                `json:"lastPushError,omitempty"`
}

// registerPathMapRoutes wires the Stage 2 path-mapping control routes onto mux.
// Same group-gated, browser-unreachable socket as the mediaRoots routes.
func registerPathMapRoutes(mux *http.ServeMux, cfg *NodeConfig, configPath string, pusher *pathmapPusher, sess *nodeSession) {
	mux.HandleFunc("GET /pathmap", func(w http.ResponseWriter, r *http.Request) {
		writeJSONStatus(w, http.StatusOK, pathmapState{
			AuthoredPaths:   cfg.authoredSnapshot(),
			PathMap:         snapshotPathMap(cfg),
			LibraryPathKeys: sess.libraryPathKeys(),
			LastPushError:   pusher.lastError(),
		})
	})
	mux.HandleFunc("POST /pathmap/set", func(w http.ResponseWriter, r *http.Request) {
		handlePathMapSet(w, r, cfg, configPath, pusher)
	})
	mux.HandleFunc("POST /pathmap/clear", func(w http.ResponseWriter, r *http.Request) {
		handlePathMapClear(w, r, cfg, configPath, pusher)
	})
}

// handlePathMapSet authors one library-path-key → local path mapping: it
// validates locally, persists the authored entry to NodeConfig.AuthoredPaths
// under the config lock, then schedules a debounced push. The server remains the
// source of truth — this only records the PROPOSAL; the authoritative Remap
// entry arrives later via the SSE settings echo (Principle 2). A rejected set
// never mutates config.
func handlePathMapSet(w http.ResponseWriter, r *http.Request, cfg *NodeConfig, configPath string, pusher *pathmapPusher) {
	req, err := decodePathMapPayload(r)
	if err != nil {
		rejectPathMap(w, r, "set", err)
		return
	}
	if req.Key == "" {
		rejectPathMap(w, r, "set", fmt.Errorf("key is empty"))
		return
	}

	// Fail fast (D9): a mapping may only be authored once the node has a real,
	// non-trivial mediaRoot — the independent containment boundary the server
	// also hard-gates. Check the LIVE config, not a stale copy.
	_, mediaRoots := cfg.snapshot()
	if err := mediaRootsUsable(mediaRoots); err != nil {
		rejectPathMap(w, r, "set", err)
		return
	}

	// Stage 4: the nodePath itself is validated against those same live
	// mediaRoots — empty/trivial rejection plus a containment check that the
	// chosen path resolves within a configured mediaRoot, all BEFORE the push is
	// scheduled below.
	canonical, err := validatePathMapLocal(req.LocalPath, mediaRoots)
	if err != nil {
		rejectPathMap(w, r, "set", err)
		return
	}

	if saveErr := cfg.mutateAndSave(configPath, func() {
		upsertAuthoredLocked(cfg, req.Key, canonical)
	}); saveErr != nil {
		rejectPathMap(w, r, "set", saveErr)
		return
	}

	pusher.schedule(req.Key, opSet)
	log.Printf("sakms-node: control socket: authored path mapping %q -> %q (%s); push scheduled", req.Key, canonical, peerUID(r.Context()))
	writePathMapState(w, cfg, pusher)
}

// handlePathMapClear removes one key's mapping (D7). It directly deletes the
// authored entry AND its correlated Remap entry from NodeConfig.PathMap under
// the config lock — NEVER via mergePathMap, which is add/replace-only and would
// silently no-op a delete, letting a reconnect resurrect the stale entry. Then
// it pushes the clear up so the server deletes its own row.
func handlePathMapClear(w http.ResponseWriter, r *http.Request, cfg *NodeConfig, configPath string, pusher *pathmapPusher) {
	req, err := decodePathMapPayload(r)
	if err != nil {
		rejectPathMap(w, r, "clear", err)
		return
	}
	if req.Key == "" {
		rejectPathMap(w, r, "clear", fmt.Errorf("key is empty"))
		return
	}

	if saveErr := cfg.mutateAndSave(configPath, func() {
		clearAuthoredLocked(cfg, req.Key)
	}); saveErr != nil {
		rejectPathMap(w, r, "clear", saveErr)
		return
	}

	pusher.schedule(req.Key, opClear)
	log.Printf("sakms-node: control socket: cleared path mapping %q (%s); push scheduled", req.Key, peerUID(r.Context()))
	writePathMapState(w, cfg, pusher)
}

// upsertAuthoredLocked sets key's authored NodePath to local, replacing any
// existing entry for that key. Caller MUST hold cfg.mu (runs inside
// mutateAndSave's mutate closure).
func upsertAuthoredLocked(cfg *NodeConfig, key, local string) {
	for i := range cfg.AuthoredPaths {
		if cfg.AuthoredPaths[i].Key == key {
			cfg.AuthoredPaths[i].NodePath = local
			return
		}
	}
	cfg.AuthoredPaths = append(cfg.AuthoredPaths, AuthoredPathMapping{Key: key, NodePath: local})
}

// clearAuthoredLocked deletes key's authored entry and, using the NodePath it
// authored, directly deletes every matching Remap entry from cfg.PathMap
// (correlated by Local, since the node authors by key but Remap is keyed by
// server prefix and mergePathMap carries no key). This is the direct slice
// deletion D7 requires — no mergePathMap involvement. Caller MUST hold cfg.mu.
//
// Accepted edge case: if two keys authored the SAME local path, clearing one
// drops both Remap entries; the surviving key's authored entry re-pushes on the
// next reconnect echo and is restored. A Remap entry with no corresponding
// authored key (e.g. a legacy operator-authored mapping) cannot be correlated
// and is left alone — in the node-authoritative model every mapping originates
// here, so AuthoredPaths is the authority.
func clearAuthoredLocked(cfg *NodeConfig, key string) {
	local := ""
	kept := make([]AuthoredPathMapping, 0, len(cfg.AuthoredPaths))
	for _, a := range cfg.AuthoredPaths {
		if a.Key == key {
			local = a.NodePath
			continue
		}
		kept = append(kept, a)
	}
	cfg.AuthoredPaths = kept

	if local == "" {
		return
	}
	keptPM := make([]PathMapEntry, 0, len(cfg.PathMap))
	for _, e := range cfg.PathMap {
		if e.Local == local {
			continue
		}
		keptPM = append(keptPM, e)
	}
	cfg.PathMap = keptPM
}

// snapshotPathMap returns a copy of the Remap table for read-only display.
func snapshotPathMap(cfg *NodeConfig) []PathMapEntry {
	pm, _ := cfg.snapshot()
	return pm
}

func decodePathMapPayload(r *http.Request) (pathmapPayload, error) {
	var req pathmapPayload
	if r.Body == nil {
		return req, nil
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return req, fmt.Errorf("decoding request body: %w", err)
	}
	return req, nil
}

// writePathMapState echoes the post-mutation local state after a set/clear. It
// omits the catalog (the tray reads that from GET /pathmap) — only the authored
// mappings, the Remap table, and any last-push error are relevant to the
// caller's immediate confirmation.
func writePathMapState(w http.ResponseWriter, cfg *NodeConfig, pusher *pathmapPusher) {
	writeJSONStatus(w, http.StatusOK, pathmapState{
		AuthoredPaths: cfg.authoredSnapshot(),
		PathMap:       snapshotPathMap(cfg),
		LastPushError: pusher.lastError(),
	})
}

// rejectPathMap mirrors rejectMediaRoots: logs the daemon-side rejection
// attributed to the peer uid and returns 400 with a JSON error body.
func rejectPathMap(w http.ResponseWriter, r *http.Request, op string, err error) {
	log.Printf("sakms-node: control socket: rejected pathmap %s (%s): %v", op, peerUID(r.Context()), err)
	writeJSONStatus(w, http.StatusBadRequest, pathmapPayload{Error: err.Error()})
}

func writeJSONStatus(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v) //nolint:errcheck
}
