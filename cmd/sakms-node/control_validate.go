package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/labbersanon/sakms/internal/nodepath"
)

// validateMediaRootPath is the daemon-side trust boundary for the control
// socket (see the mediaRoots-UI plan's Must-Have guardrails): the socket — not
// the tray's native picker — is where an untrusted or buggy local caller could
// submit anything, so every path headed for MediaRoots is validated here
// regardless of source. It (a) requires an absolute path, (b) canonicalizes it
// (EvalSymlinks resolves symlinks/`..` and cleans), and (c) verifies it stat's
// as an existing directory, returning the fully-resolved path to store (never
// the raw submitted string, so the persisted allowlist is already normalized).
// Pure filepath/os logic with no Linux-specific syscalls, so it lives in an
// untagged file: it compiles on every GOOS and is unit-testable everywhere.
func validateMediaRootPath(raw string) (string, error) {
	if raw == "" {
		return "", errors.New("path is empty")
	}
	if !filepath.IsAbs(raw) {
		return "", fmt.Errorf("path %q is not absolute", raw)
	}
	// EvalSymlinks resolves every symlink component and cleans the result; it
	// errors on a nonexistent path, which also covers the "nonexistent"
	// rejection case. The input is already absolute, so the output is too.
	canonical, err := filepath.EvalSymlinks(raw)
	if err != nil {
		return "", fmt.Errorf("path %q cannot be resolved (does it exist?): %w", raw, err)
	}
	info, err := os.Stat(canonical)
	if err != nil {
		return "", fmt.Errorf("path %q cannot be stat'd: %w", canonical, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("path %q is not a directory", canonical)
	}
	return canonical, nil
}

// validatePathMapLocal is the daemon-side validation for a control-socket
// /pathmap/set localPath, and the node-side half of Stage 4's blast-radius
// guardrails. It:
//   - rejects empty / relative / nonexistent / non-directory paths and returns
//     the fully-resolved canonical form (via validateMediaRootPath);
//   - rejects a trivially-shallow path ("/", "/mnt", …) using the single
//     canonical rule in internal/nodepath (shared with the server-side gate),
//     so a filesystem root can never be authored as a mapping value; and
//   - rejects a nodePath that resolves OUTSIDE every configured mediaRoot
//     (withinMediaRoots), so a mapping is contained to a locally-asserted media
//     subtree before it is ever pushed (D9 / §3a compensating control).
//
// The caller (handlePathMapSet) has already rejected an empty/trivial mediaRoots
// list via mediaRootsUsable, so mediaRoots here is guaranteed non-empty and
// non-trivial — withinMediaRoots is therefore a meaningful containment check,
// never the empty-list grace-period no-op. Running the check here, before the
// pusher schedules anything, is what makes the node's local rejection happen
// BEFORE any push is attempted (not just server-side).
func validatePathMapLocal(raw string, mediaRoots []string) (string, error) {
	canonical, err := validateMediaRootPath(raw)
	if err != nil {
		return "", err
	}
	if nodepath.Trivial(canonical) {
		return "", fmt.Errorf("path %q is too shallow (need at least %d path segments) to be a media path mapping", canonical, nodepath.MinDepth)
	}
	if err := withinMediaRoots(mediaRoots, canonical); err != nil {
		return "", err
	}
	return canonical, nil
}

// mediaRootsUsable reports whether the node's current mediaRoots list can back a
// path-mapping write: it must be non-empty and every entry must be non-trivial,
// the same presence rule the server hard-gates (422) before verifying any
// node-auth PathMap set/clear (D9). Checked node-side so the control socket
// fails fast and the pusher never wastes a round trip that the server would
// reject anyway.
func mediaRootsUsable(mediaRoots []string) error {
	if len(mediaRoots) == 0 {
		return errors.New("node has no configured mediaRoots; add a media root before authoring a path mapping")
	}
	for _, root := range mediaRoots {
		if nodepath.Trivial(root) {
			return fmt.Errorf("configured mediaRoot %q is too shallow (need at least %d path segments); set a real media root before authoring a path mapping", root, nodepath.MinDepth)
		}
	}
	return nil
}
