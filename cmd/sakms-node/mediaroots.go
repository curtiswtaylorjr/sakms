package main

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/labbersanon/sakms/internal/nodes"
)

// withinMediaRoots reports whether path resolves within one of mediaRoots —
// the security-hardening addendum's node-side allowlist (Safeguard 2).
// Resolves symlinks before comparing (not a string-prefix check on the
// unresolved input), so a symlink pointing outside mediaRoots cannot be
// used to escape the boundary.
//
// Callers must check len(mediaRoots) == 0 themselves and treat that as the
// grace period (skip this check entirely) — an empty mediaRoots here would
// otherwise mean "matches no root," which is the opposite of the intended
// no-op-during-grace-period behavior.
func withinMediaRoots(mediaRoots []string, path string) error {
	resolved := resolvePathBestEffort(path)
	for _, root := range mediaRoots {
		resolvedRoot := resolvePathBestEffort(root)
		if resolved == resolvedRoot || strings.HasPrefix(resolved, resolvedRoot+string(filepath.Separator)) {
			return nil
		}
	}
	return fmt.Errorf("path %q is outside this node's configured mediaRoots", path)
}

// resolvePathBestEffort resolves symlinks in path. If the path doesn't
// exist yet (EvalSymlinks fails — e.g. a directory being browsed into that
// was just created), it falls back to a lexical Clean rather than erroring,
// since a nonexistent path has nothing to read regardless.
func resolvePathBestEffort(path string) string {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved
	}
	return filepath.Clean(path)
}

// validateSettingsPush checks every entry's Local value in an incoming
// settings push against mediaRoots, returning a non-empty rejection reason
// if ANY entry is out of scope. The whole push must be rejected in that
// case — not partially applied — so a compromised server cannot smuggle one
// bad entry alongside several legitimate ones hoping only the bad one gets
// dropped unnoticed. Callers must skip this validation entirely (never
// reject) while mediaRoots is empty — the grace period.
func validateSettingsPush(mediaRoots []string, pathMap []nodes.PathMapping) string {
	for _, pm := range pathMap {
		if err := withinMediaRoots(mediaRoots, pm.Local); err != nil {
			return err.Error()
		}
	}
	return ""
}
