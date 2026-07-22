package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
)

const defaultStatusPort = 7810

// PathMapEntry maps one server-absolute path prefix to a local prefix. The
// node replaces the server prefix with the local prefix before opening a file.
type PathMapEntry struct {
	Server string `json:"server"`
	Local  string `json:"local"`
}

// AuthoredPathMapping records ONE library-path-key → node-local path the
// operator authored on THIS node (Stage 2 of the node-path-config-ui plan).
//
// Why this is a separate field from PathMap rather than being folded into it
// (a deliberate deviation from the plan's literal "persist to NodeConfig.
// PathMap" wording, kept on the record so a reviewer reads it as intentional):
// PathMap is the server-authoritative Remap table, keyed by SERVER prefix and
// populated exclusively by the SSE settings echo through mergePathMap
// (add/replace-only, keyed by Server — remap.go). The node authors by
// library-path KEY, and the server derives the server prefix; the node never
// learns that prefix until the echo comes back. Folding the two into one slice
// would force mergePathMap to correlate echoed Server/Local pairs back to a key
// — exactly the "make merge delete-on-absence / key-aware" change D7 forbids,
// because it is what protects the add/replace reconnect-merge invariant. So the
// node keeps its own authored record here (the proposal / source of truth for
// what it asked for and for re-pushes) and leaves PathMap + mergePathMap
// pristine. On a clear, the authored NodePath is used to locate and directly
// delete the matching PathMap Remap entry (control_pathmap.go), never via
// mergePathMap.
type AuthoredPathMapping struct {
	Key      string `json:"key"`      // apidto.LibraryPathKey value, e.g. movies_library_root_folder
	NodePath string `json:"nodePath"` // node-local absolute path (canonicalized on set)
}

// NodeConfig is loaded from and saved to the JSON config file.
type NodeConfig struct {
	ServerURL  string         `json:"serverUrl"`  // e.g. https://media-admin.zaena.us
	APIKey     string         `json:"apiKey"`     // per-node bearer key; empty = needs pairing
	NodeName   string         `json:"nodeName"`   // e.g. wade-pc-4070
	PathMap    []PathMapEntry `json:"pathMap"`    // applied longest-prefix-first
	StatusPort int            `json:"statusPort"` // port for GET /status; 0 → defaultStatusPort
	MaxJobs    int            `json:"maxJobs"`    // 0 = unlimited

	// MediaRoots is the security-hardening addendum's node-side allowlist
	// (Safeguard 2): the top-level directory tree(s) on this machine that
	// legitimately contain media. Every browse request and every hash job's
	// remapped local path must resolve within one of these, independent of
	// whatever the server asks for — this is what makes the check
	// adversarially meaningful (a compromised server credential cannot
	// expand it) rather than the server checking itself.
	//
	// Explicitly operator-set only, NEVER auto-derived from PathMap (an
	// auto-derive-from-common-ancestor approach was considered and rejected
	// as unsound — it can collapse toward "/" for media on separate mounts,
	// silently producing no real protection). Strictly local-only: this
	// field has no counterpart on the wire NodeSettings type and must never
	// be settable via any SSE/EventSettings push — it is set only by
	// editing this config file directly.
	//
	// Empty (the default, and the state of every node that predates this
	// addendum) means "not yet configured" — a grace period during which
	// every check below is a no-op and the node behaves exactly as it did
	// before this addendum, so upgrading an already-working node never
	// silently breaks it. A prominent warning is logged repeatedly while
	// this is empty. Enforcement begins the moment an operator sets this.
	MediaRoots []string `json:"mediaRoots,omitempty"`

	// AuthoredPaths is the node's own record of the library-path-key → node
	// path mappings the operator authored on THIS node (Stage 2). It is the
	// proposal source of truth (drives the debounced push and the tray's
	// "configured vs unconfigured keys" view); the server-authoritative Remap
	// table lives separately in PathMap. See AuthoredPathMapping's doc comment
	// for why the two are kept apart. Mutated at runtime by the control socket,
	// so it is guarded by mu exactly like PathMap/MediaRoots.
	AuthoredPaths []AuthoredPathMapping `json:"authoredPaths,omitempty"`

	// mu guards the fields that are mutated after startup — MediaRoots,
	// PathMap, MaxJobs, APIKey, and AuthoredPaths — and serializes them against
	// save(). It is
	// unexported, so encoding/json ignores it. Every writer of those fields
	// holds the write lock across BOTH the field mutation AND the subsequent
	// save (via mutateAndSave), because save() marshals the whole struct: a
	// locked save that raced an unlocked field write would still read a field
	// mid-write. Concurrent readers take snapshots under the read lock.
	mu sync.RWMutex
}

// statusPort returns the effective status listener port.
func (cfg *NodeConfig) statusPort() int {
	if cfg.StatusPort > 0 {
		return cfg.StatusPort
	}
	return defaultStatusPort
}

// loadConfig reads the JSON file at path and validates required fields.
// APIKey is intentionally optional: an empty value means the node will enter
// pairing mode on startup and acquire a per-node key from the server.
func loadConfig(path string) (*NodeConfig, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("sakms-node: opening config %s: %w", path, err)
	}
	defer f.Close()

	var cfg NodeConfig
	if err := json.NewDecoder(f).Decode(&cfg); err != nil {
		return nil, fmt.Errorf("sakms-node: decoding config %s: %w", path, err)
	}

	if cfg.ServerURL == "" {
		return nil, fmt.Errorf("sakms-node: config %s: serverUrl is required", path)
	}
	if cfg.NodeName == "" {
		return nil, fmt.Errorf("sakms-node: config %s: nodeName is required", path)
	}
	return &cfg, nil
}

// snapshot returns copies of the concurrently-mutated fields under the read
// lock, so a reader (executeJob, executeBrowse, the status handler) gets a
// consistent, non-torn view without holding the lock across its own work.
func (cfg *NodeConfig) snapshot() (pathMap []PathMapEntry, mediaRoots []string) {
	cfg.mu.RLock()
	defer cfg.mu.RUnlock()
	pathMap = append([]PathMapEntry(nil), cfg.PathMap...)
	mediaRoots = append([]string(nil), cfg.MediaRoots...)
	return pathMap, mediaRoots
}

// authoredSnapshot returns a copy of the node-authored path mappings under the
// read lock, mirroring snapshot()'s non-torn-view contract for the Stage 2
// control-socket read path (GET /pathmap) and the tray.
func (cfg *NodeConfig) authoredSnapshot() []AuthoredPathMapping {
	cfg.mu.RLock()
	defer cfg.mu.RUnlock()
	return append([]AuthoredPathMapping(nil), cfg.AuthoredPaths...)
}

// pushInputs reads, under a single read-lock acquisition, everything the
// debounced pusher needs to build one node-auth settings push for key: the
// authored NodePath for that key (nodePath/ok), the node's current MediaRoots
// self-report (D9 — read fresh at push time, never a stale cached copy), and
// the transport identity (serverURL/apiKey). Taking them together avoids a torn
// read across a concurrent config mutation.
func (cfg *NodeConfig) pushInputs(key string) (nodePath string, ok bool, mediaRoots []string, serverURL, apiKey string) {
	cfg.mu.RLock()
	defer cfg.mu.RUnlock()
	for _, a := range cfg.AuthoredPaths {
		if a.Key == key {
			nodePath = a.NodePath
			ok = true
			break
		}
	}
	mediaRoots = append([]string(nil), cfg.MediaRoots...)
	return nodePath, ok, mediaRoots, cfg.ServerURL, cfg.APIKey
}

// mutateAndSave runs mutate and then persists the config, both under a single
// write-lock acquisition, so the field mutations and the save that follows them
// are one atomic critical section. This is the sole write entry point for the
// post-startup mutable fields: serializing save() alone would be insufficient,
// because save() marshals the whole struct and a locked save could still read a
// field that an unlocked writer is mid-mutation on. The future control-socket
// handler plugs its writes in here too.
func (cfg *NodeConfig) mutateAndSave(path string, mutate func()) error {
	cfg.mu.Lock()
	defer cfg.mu.Unlock()
	mutate()
	return cfg.saveLocked(path)
}

// clearAPIKey clears the API key and persists it in one critical section. Used
// by the 401 re-pair path; folded under the lock because Step 2's main()-level
// control-socket save can marshal APIKey concurrently with this write.
func (cfg *NodeConfig) clearAPIKey(path string) error {
	return cfg.mutateAndSave(path, func() { cfg.APIKey = "" })
}

// applyPairConfig persists a freshly received pairing result (API key + pushed
// settings) atomically: the APIKey/MaxJobs/PathMap writes and the save happen
// under one lock so a concurrent config writer (e.g. the control socket) can
// never observe or marshal a half-applied pairing.
func (cfg *NodeConfig) applyPairConfig(path, apiKey string, maxJobs int, pathMap []PathMapEntry) error {
	return cfg.mutateAndSave(path, func() {
		cfg.APIKey = apiKey
		cfg.MaxJobs = maxJobs
		cfg.PathMap = pathMap
	})
}

// saveLocked atomically writes cfg to path using a write-then-rename pattern so
// a crash mid-write cannot leave a partial or empty config file. The caller
// MUST hold cfg.mu (write lock): it marshals the whole struct, so it must be
// serialized against every field mutation.
func (cfg *NodeConfig) saveLocked(path string) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("sakms-node: marshalling config: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		return fmt.Errorf("sakms-node: writing config tmp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp) //nolint:errcheck
		return fmt.Errorf("sakms-node: renaming config: %w", err)
	}
	return nil
}
