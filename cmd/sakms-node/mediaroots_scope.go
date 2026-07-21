package main

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// mediaRootScope is the Phase 2 (OS-level namespace containment) observability
// state of a single configured mediaRoots entry.
type mediaRootScope string

const (
	// scopeAppLevelOnly means this root is not part of a successfully-applied
	// Phase 2 namespace scoping: either Phase 2 was never applied on this node
	// (no marker file), or mediaRoots has changed since the last successful
	// apply and this specific root isn't recorded in the marker. The node
	// still enforces the app-level allowlist (Safeguard 2, mediaroots.go); it
	// just has no OS-level containment baked in for this root yet.
	scopeAppLevelOnly mediaRootScope = "app_level_only"

	// scopeNamespaceScoped means this root IS recorded in the marker (was
	// baked into the last successful drop-in apply) AND the daemon can confirm,
	// from its own /proc/self/mountinfo view inside its running sandbox, that
	// something is actually mounted at that path right now.
	scopeNamespaceScoped mediaRootScope = "namespace_scoped"

	// scopeNamespaceScopedButUnbound means this root IS recorded in the marker
	// but the daemon's own mount-presence check finds nothing mounted there
	// right now — the "mount was down at unit-start" case, where a
	// `-`-prefixed non-mandatory bind didn't actually bind. Actionable:
	// re-run apply-mediaroots or restart the daemon once the mount is back.
	scopeNamespaceScopedButUnbound mediaRootScope = "namespace_scoped_but_unbound"
)

// mediaRootStatus reports the Phase 2 containment state of one configured
// mediaRoots entry. Path is the raw configured value (as it appears in the
// node config), so an operator can line it up against their config directly.
type mediaRootStatus struct {
	Path  string         `json:"path"`
	Scope mediaRootScope `json:"scope"`
}

// mediaRootsMarkerPath is where apply-mediaroots.sh records the resolved root
// list it baked into the systemd drop-in on a successful apply. It lives under
// /etc/sakms-node deliberately: statusSnapshot runs as code INSIDE the very
// sandbox it reports on, and under TemporaryFileSystem=/ the daemon can only
// read paths re-exposed via BindPaths=; /etc/sakms-node is the one directory
// already bind-mounted for the daemon's own config, so a marker anywhere else
// (/var/lib, /run) would be invisible to the code meant to read it.
const mediaRootsMarkerPath = "/etc/sakms-node/mediaroots-applied.json"

// selfMountinfoPath is the daemon's own view of its mount namespace. Reading
// /proc/self/mountinfo (not os.Stat) is deliberate: os.Stat tests EXISTENCE,
// but under TemporaryFileSystem=/ systemd leaves an empty placeholder
// directory for a skipped `-`-prefixed bind, so os.Stat would false-pass
// namespace_scoped in exactly the down-at-start case this check exists to
// catch. A genuinely bound root has a mountinfo entry; a placeholder does not.
const selfMountinfoPath = "/proc/self/mountinfo"

// mediaRootsMarker is the parsed apply marker. It is read leniently (see
// parseMarker) because it is produced by a separate packaging script and the
// exact field names can't be coordinated live.
//
// Expected canonical shape written by apply-mediaroots.sh:
//
//	{
//	  "appliedAt": "2026-07-20T12:00:00Z",
//	  "roots": [
//	    { "path": "/mnt/Media-NAS/Movies" },
//	    { "path": "/mnt/Media-NAS/TV Shows" }
//	  ]
//	}
//
// Roots holds the resolved (realpath'd) paths the drop-in was generated from —
// the same values that appear as mount points in /proc/self/mountinfo — which
// is why comparison keys off the marker rather than an independent in-sandbox
// re-resolution of the raw config value (which is unreliable under
// TemporaryFileSystem=/, and would touch the filesystem — the very hang
// the mountinfo approach avoids).
type mediaRootsMarker struct {
	AppliedAt string
	Roots     []string // resolved, filepath.Clean'd
}

// mediaRootScopes classifies each configured mediaRoots entry against the
// production marker and the daemon's own /proc/self/mountinfo. An empty
// mediaRoots list yields nil (nothing to report; the grace period).
func mediaRootScopes(mediaRoots []string) []mediaRootStatus {
	return mediaRootScopesFrom(mediaRoots, mediaRootsMarkerPath, selfMountinfoPath)
}

// mediaRootScopesFrom is the injectable core of mediaRootScopes: the marker
// and mountinfo source paths are parameters so tests can supply synthetic
// content. A missing marker file is NOT an error — it's the expected steady
// state for a Phase-1-only node, and every configured root then reports
// app_level_only.
func mediaRootScopesFrom(mediaRoots []string, markerPath, mountinfoPath string) []mediaRootStatus {
	if len(mediaRoots) == 0 {
		return nil
	}
	marker := readMarker(markerPath)

	var mountPoints map[string]bool
	if marker != nil {
		if f, err := os.Open(mountinfoPath); err == nil {
			mountPoints, _ = mountPointsFromMountinfo(f)
			f.Close() //nolint:errcheck
		}
	}
	return computeMediaRootScopes(mediaRoots, marker, mountPoints)
}

// computeMediaRootScopes is the pure classifier. marker may be nil (no marker
// file) and mountPoints may be nil (unreadable mountinfo); both degrade
// safely to app_level_only / namespace_scoped_but_unbound respectively.
func computeMediaRootScopes(mediaRoots []string, marker *mediaRootsMarker, mountPoints map[string]bool) []mediaRootStatus {
	out := make([]mediaRootStatus, 0, len(mediaRoots))
	for _, raw := range mediaRoots {
		st := mediaRootStatus{Path: raw, Scope: scopeAppLevelOnly}
		if marker != nil {
			if resolved, ok := marker.match(raw); ok {
				if mountPoints[resolved] {
					st.Scope = scopeNamespaceScoped
				} else {
					st.Scope = scopeNamespaceScopedButUnbound
				}
			}
		}
		out = append(out, st)
	}
	return out
}

// match reports whether a configured root corresponds to an entry in the
// marker, returning the marker's resolved path (the mountinfo lookup key) if
// so. Matching is a pure lexical filepath.Clean comparison — it deliberately
// does NOT EvalSymlinks/os.Stat the config value: that would be an independent
// in-sandbox re-resolution (against acceptance criterion #5) and would touch
// the filesystem, reintroducing the CIFS-hang risk the mountinfo approach
// exists to avoid. A symlinked root therefore reports app_level_only — the
// documented, accepted limitation; direct paths (the common case) are exact.
func (m *mediaRootsMarker) match(configRoot string) (string, bool) {
	want := filepath.Clean(configRoot)
	for _, r := range m.Roots {
		if r == want {
			return r, true
		}
	}
	return "", false
}

// readMarker reads and parses the marker file. Any failure — the file being
// absent (Phase 2 never applied), unreadable, or unparseable — returns nil,
// which callers treat as "no Phase 2 apply on record" rather than an error.
func readMarker(path string) *mediaRootsMarker {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	return parseMarker(data)
}

// parseMarker parses the marker leniently. Because apply-mediaroots.sh is
// authored separately and its exact field names can't be coordinated live,
// the reader accepts several plausible key spellings (case-insensitively) and
// tolerates each roots element being either a bare path string or an object
// carrying a path-like field. Returns nil only if the payload isn't a JSON
// object at all.
func parseMarker(data []byte) *mediaRootsMarker {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil
	}

	m := &mediaRootsMarker{}

	for _, k := range []string{"appliedAt", "applied_at", "timestamp", "time", "at"} {
		if v, ok := lookupCI(raw, k); ok {
			var s string
			if json.Unmarshal(v, &s) == nil && s != "" {
				m.AppliedAt = s
				break
			}
		}
	}

	var rootsRaw json.RawMessage
	for _, k := range []string{"roots", "mediaRoots", "resolved", "resolvedRoots", "paths", "applied"} {
		if v, ok := lookupCI(raw, k); ok {
			rootsRaw = v
			break
		}
	}
	if rootsRaw == nil {
		return m
	}

	var elems []json.RawMessage
	if json.Unmarshal(rootsRaw, &elems) != nil {
		return m
	}
	for _, e := range elems {
		if p := extractMarkerPath(e); p != "" {
			m.Roots = append(m.Roots, filepath.Clean(p))
		}
	}
	return m
}

// extractMarkerPath pulls a path out of one roots-array element, which may be
// a bare string or an object carrying a path-like field under any of several
// plausible names.
func extractMarkerPath(e json.RawMessage) string {
	var s string
	if json.Unmarshal(e, &s) == nil {
		return s
	}
	var obj map[string]json.RawMessage
	if json.Unmarshal(e, &obj) != nil {
		return ""
	}
	for _, k := range []string{"path", "resolved", "resolvedPath", "local", "root", "dir"} {
		if v, ok := lookupCI(obj, k); ok {
			var ps string
			if json.Unmarshal(v, &ps) == nil && ps != "" {
				return ps
			}
		}
	}
	return ""
}

// lookupCI does a case-insensitive key lookup against a decoded JSON object.
// Keys in a map[string]json.RawMessage are preserved verbatim from the JSON,
// so this reproduces encoding/json's own case-insensitive field matching for
// the dynamic-key case.
func lookupCI(obj map[string]json.RawMessage, key string) (json.RawMessage, bool) {
	if v, ok := obj[key]; ok {
		return v, true
	}
	for k, v := range obj {
		if strings.EqualFold(k, key) {
			return v, true
		}
	}
	return nil, false
}

// mountPointsFromMountinfo parses mount points (field 5, 1-indexed) out of a
// /proc/*/mountinfo stream into a set. Field 5 is positional — it precedes the
// optional-fields section and its `-` separator — and the kernel octal-escapes
// space/tab/newline/backslash within it, so strings.Fields never over-splits
// it. Each mount point is un-escaped (unescapeMountinfo) before it goes in the
// set, so a bound root containing a space matches correctly.
func mountPointsFromMountinfo(r io.Reader) (map[string]bool, error) {
	points := map[string]bool{}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 5 {
			continue
		}
		points[unescapeMountinfo(fields[4])] = true
	}
	return points, sc.Err()
}

// unescapeMountinfo reverses the octal escaping /proc/*/mountinfo applies to
// space (\040), tab (\011), newline (\012), and backslash (\134) in its path
// fields, per proc_pid_mountinfo(5). Comparing an escaped mountinfo path
// against an un-escaped configured/marker path would silently fail to match a
// root containing a space (e.g. "/mnt/Media-NAS/TV Shows"), falsely reporting
// a correctly-bound root as namespace_scoped_but_unbound — so every field-5
// value MUST pass through here before comparison.
func unescapeMountinfo(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+3 < len(s) &&
			isOctalDigit(s[i+1]) && isOctalDigit(s[i+2]) && isOctalDigit(s[i+3]) {
			b.WriteByte((s[i+1]-'0')<<6 | (s[i+2]-'0')<<3 | (s[i+3] - '0'))
			i += 3
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func isOctalDigit(c byte) bool { return c >= '0' && c <= '7' }
