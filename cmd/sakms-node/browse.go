package main

import (
	"os"
	"path/filepath"
	"sort"

	"github.com/labbersanon/sakms/internal/nodes"
)

// browseDirectory lists path's contents on this node's own filesystem, for
// the operator to pick a node-local path against a library root, or for
// Safeguard 1's mapping-verification comparison. Mirrors the server's own
// GET /api/browse (internal/api/browse.go) response shape (alpha-sorted).
//
// This function itself has no allowlist — the allowlist boundary is
// enforced by its callers (executeBrowse, executeJob) via
// withinMediaRoots/cfg.MediaRoots (mediaroots.go), the security-hardening
// addendum's node-side containment (Safeguard 2), not here. That
// separation is deliberate: this function is a pure filesystem read, and
// the scope check is a single, independently-testable gate applied before
// any caller reaches it, rather than duplicated logic baked into every
// reader of the filesystem.
//
// includeFiles=false (the operator-facing folder picker's request) keeps
// the original dirs-only behavior; includeFiles=true (used only by the
// security-hardening addendum's mapping-verification safeguard) also
// includes files, so a flat, file-only library doesn't compare as empty on
// every save.
func browseDirectory(path string, includeFiles bool) ([]nodes.BrowseEntry, error) {
	infos, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}

	entries := make([]nodes.BrowseEntry, 0, len(infos))
	for _, info := range infos {
		if !includeFiles && !info.IsDir() {
			continue
		}
		entries = append(entries, nodes.BrowseEntry{
			Name: info.Name(),
			Path: filepath.Join(path, info.Name()),
		})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
	return entries, nil
}
