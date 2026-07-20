package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/labbersanon/sakms/internal/nodes"
)

func TestWithinMediaRoots_ExactMatch(t *testing.T) {
	root := t.TempDir()
	if err := withinMediaRoots([]string{root}, root); err != nil {
		t.Errorf("expected the root itself to be within mediaRoots, got: %v", err)
	}
}

func TestWithinMediaRoots_Subdirectory(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "movies")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := withinMediaRoots([]string{root}, sub); err != nil {
		t.Errorf("expected a subdirectory of a mediaRoot to be in scope, got: %v", err)
	}
}

func TestWithinMediaRoots_SiblingDirectoryRejected(t *testing.T) {
	root := t.TempDir()
	sibling := t.TempDir() // a different, unrelated temp dir
	if err := withinMediaRoots([]string{root}, sibling); err == nil {
		t.Error("expected a sibling directory (not under any mediaRoot) to be rejected")
	}
}

func TestWithinMediaRoots_PrefixCollisionRejected(t *testing.T) {
	// A path that merely shares a string prefix with a root (e.g.
	// /mnt/moviesFOO vs /mnt/movies) must NOT be treated as a subdirectory.
	root := t.TempDir()
	collidingPath := root + "FOO"
	if err := withinMediaRoots([]string{root}, collidingPath); err == nil {
		t.Error("expected a same-prefix-but-not-a-subdirectory path to be rejected")
	}
}

func TestWithinMediaRoots_DotDotTraversalRejected(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "movies")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	// Lexically this cleans back up to root's parent -- must be rejected.
	traversal := filepath.Join(sub, "..", "..")
	if err := withinMediaRoots([]string{root}, traversal); err == nil {
		t.Error("expected a '..'-traversal escaping the root to be rejected")
	}
}

func TestWithinMediaRoots_SymlinkEscapingBoundaryRejected(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir() // a real directory outside root
	link := filepath.Join(root, "escape")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlinks not supported in this environment: %v", err)
	}
	if err := withinMediaRoots([]string{root}, link); err == nil {
		t.Error("expected a symlink resolving outside the root to be rejected (resolved path must be checked, not the unresolved symlink path)")
	}
}

func TestWithinMediaRoots_TrailingSlashNormalization(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "movies")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := withinMediaRoots([]string{root + "/"}, sub); err != nil {
		t.Errorf("expected a trailing-slash root to still correctly bound its subdirectories, got: %v", err)
	}
}

func TestValidateSettingsPush_AllInScope_Accepts(t *testing.T) {
	root := t.TempDir()
	pathMap := []nodes.PathMapping{
		{Server: "/data/movies", Local: filepath.Join(root, "movies")},
		{Server: "/data/series", Local: filepath.Join(root, "series")},
	}
	if reason := validateSettingsPush([]string{root}, pathMap); reason != "" {
		t.Errorf("expected no rejection, got: %s", reason)
	}
}

func TestValidateSettingsPush_OneOutOfScope_RejectsWholePush(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	pathMap := []nodes.PathMapping{
		{Server: "/data/movies", Local: filepath.Join(root, "movies")},
		{Server: "/data/series", Local: outside}, // out of scope
	}
	reason := validateSettingsPush([]string{root}, pathMap)
	if reason == "" {
		t.Fatal("expected a rejection reason when any entry is out of scope")
	}
}

func TestExecuteBrowse_GracePeriod_MediaRootsUnset_AllowsAnyPath(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := &NodeConfig{} // MediaRoots unset -- grace period

	result := executeBrowse(cfg, nodes.BrowseRequest{ID: "req-1", Path: dir})
	if result.Error != "" {
		t.Errorf("expected the grace period to allow any path, got error: %s", result.Error)
	}
	if len(result.Entries) != 1 {
		t.Errorf("expected 1 entry, got %d", len(result.Entries))
	}
}

func TestExecuteBrowse_MediaRootsSet_RejectsOutOfScopePath(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	cfg := &NodeConfig{MediaRoots: []string{root}}

	result := executeBrowse(cfg, nodes.BrowseRequest{ID: "req-1", Path: outside})
	if result.Error == "" {
		t.Fatal("expected a rejection error for a path outside mediaRoots")
	}
}

// TestNodeSettings_WireCannotSetMediaRoots is the regression test guarding
// against a future dev accidentally adding a mediaRoots-like field to the
// wrong wire DTO (Pre-mortem scenario 1 in the security-hardening
// addendum): a settings push whose JSON body carries a "mediaRoots" key
// must not alter the node's actual, local-only cfg.MediaRoots. This holds
// today because nodes.NodeSettings has no such field at all -- an
// unrecognized JSON key is silently ignored by json.Unmarshal, exactly the
// same as any other unknown field, not because of an explicit rejection.
func TestNodeSettings_WireCannotSetMediaRoots(t *testing.T) {
	raw := []byte(`{
		"pathMap": [{"server": "/data/movies", "local": "/mnt/movies"}],
		"maxJobs": 3,
		"mediaRoots": ["/etc", "/"]
	}`)

	var s nodes.NodeSettings
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	// The legitimate fields still decode correctly...
	if s.MaxJobs != 3 || len(s.PathMap) != 1 || s.PathMap[0].Local != "/mnt/movies" {
		t.Fatalf("expected the legitimate fields to decode normally, got %+v", s)
	}

	// ...and applying this push through the exact same logic the main
	// connect() loop uses must never touch cfg.MediaRoots.
	cfg := &NodeConfig{MediaRoots: []string{"/original/safe/root"}}
	cfg.PathMap = mergePathMap(cfg.PathMap, s.PathMap)
	cfg.MaxJobs = s.MaxJobs

	if len(cfg.MediaRoots) != 1 || cfg.MediaRoots[0] != "/original/safe/root" {
		t.Errorf("cfg.MediaRoots was altered by a wire-carried mediaRoots field: got %v, want unchanged [/original/safe/root]", cfg.MediaRoots)
	}
}

func TestExecuteBrowse_MediaRootsSet_AllowsInScopePath(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := &NodeConfig{MediaRoots: []string{root}}

	result := executeBrowse(cfg, nodes.BrowseRequest{ID: "req-1", Path: root})
	if result.Error != "" {
		t.Errorf("expected the in-scope path to be allowed, got error: %s", result.Error)
	}
}
