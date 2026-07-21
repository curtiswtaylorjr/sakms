package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
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
