package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// scopeFor returns the scope reported for a given raw configured path in a
// result slice, failing the test if that path isn't present.
func scopeFor(t *testing.T, got []mediaRootStatus, path string) mediaRootScope {
	t.Helper()
	for _, s := range got {
		if s.Path == path {
			return s.Scope
		}
	}
	t.Fatalf("path %q not present in result %+v", path, got)
	return ""
}

// writeTemp writes content to a uniquely-named file in t.TempDir and returns
// its path.
func writeTemp(t *testing.T, name, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatalf("writing %s: %v", name, err)
	}
	return p
}

func TestMediaRootScopes_NoMarkerFile_AllAppLevelOnly(t *testing.T) {
	roots := []string{"/mnt/Media-NAS/Movies", "/mnt/Media-NAS/TV Shows"}
	// A marker path that doesn't exist must NOT be an error — it's the steady
	// state of a Phase-1-only node; every root reports app_level_only.
	got := mediaRootScopesFrom(roots, filepath.Join(t.TempDir(), "absent.json"), "/proc/self/mountinfo")
	if len(got) != len(roots) {
		t.Fatalf("expected %d entries, got %d", len(roots), len(got))
	}
	for _, s := range got {
		if s.Scope != scopeAppLevelOnly {
			t.Errorf("root %q: expected app_level_only with no marker, got %q", s.Path, s.Scope)
		}
	}
}

func TestMediaRootScopes_EmptyMediaRoots_Nil(t *testing.T) {
	if got := mediaRootScopesFrom(nil, "/nonexistent", "/nonexistent"); got != nil {
		t.Errorf("expected nil for empty mediaRoots (grace period), got %+v", got)
	}
}

func TestMediaRootScopes_RootNotInMarker_AppLevelOnly(t *testing.T) {
	// Marker records a different root than the one configured now (the
	// "mediaRoots changed since apply" case).
	marker := writeTemp(t, "mediaroots-applied.json", `{
		"appliedAt": "2026-07-20T12:00:00Z",
		"roots": [ { "path": "/mnt/Media-NAS/OldMovies" } ]
	}`)
	mountinfo := writeTemp(t, "mountinfo", "36 35 0:32 / /mnt/Media-NAS/OldMovies rw - nfs4 host:/x rw\n")

	got := mediaRootScopesFrom([]string{"/mnt/Media-NAS/Movies"}, marker, mountinfo)
	if s := scopeFor(t, got, "/mnt/Media-NAS/Movies"); s != scopeAppLevelOnly {
		t.Errorf("expected app_level_only for a root absent from the marker, got %q", s)
	}
}

func TestMediaRootScopes_InMarkerAndMounted_NamespaceScoped(t *testing.T) {
	root := "/mnt/Media-NAS/Movies"
	marker := writeTemp(t, "mediaroots-applied.json", `{
		"appliedAt": "2026-07-20T12:00:00Z",
		"roots": [ { "path": "`+root+`" } ]
	}`)
	// A real, matching mountinfo entry for the root.
	mountinfo := writeTemp(t, "mountinfo",
		"22 21 0:20 / / rw shared:1 - ext4 /dev/root rw\n"+
			"36 35 0:32 / "+root+" rw - nfs4 host:/movies rw\n")

	got := mediaRootScopesFrom([]string{root}, marker, mountinfo)
	if s := scopeFor(t, got, root); s != scopeNamespaceScoped {
		t.Errorf("expected namespace_scoped for a marked+mounted root, got %q", s)
	}
}

func TestMediaRootScopes_InMarkerNotMounted_NamespaceScopedButUnbound(t *testing.T) {
	root := "/mnt/Media-NAS/Movies"
	marker := writeTemp(t, "mediaroots-applied.json", `{
		"appliedAt": "2026-07-20T12:00:00Z",
		"roots": [ { "path": "`+root+`" } ]
	}`)
	// mountinfo has NO entry for the root — the down-at-start case.
	mountinfo := writeTemp(t, "mountinfo",
		"22 21 0:20 / / rw shared:1 - ext4 /dev/root rw\n"+
			"36 35 0:32 / /some/other/mount rw - ext4 /dev/sdb rw\n")

	got := mediaRootScopesFrom([]string{root}, marker, mountinfo)
	if s := scopeFor(t, got, root); s != scopeNamespaceScopedButUnbound {
		t.Errorf("expected namespace_scoped_but_unbound for a marked-but-unmounted root, got %q", s)
	}
}

// TestMediaRootScopes_SpacePathOctalRoundTrip is the load-bearing octal
// un-escape test: a root containing a space, present in the marker in its
// literal (space) form, with a mountinfo line carrying the \040-escaped form
// of that same path, MUST report namespace_scoped — not the false
// namespace_scoped_but_unbound a parser that skips un-escaping would produce.
// It flows through mediaRootScopesFrom (real file parse) so the escape/
// un-escape step actually runs rather than being bypassed by a hand-built map.
func TestMediaRootScopes_SpacePathOctalRoundTrip(t *testing.T) {
	root := "/mnt/Media-NAS/TV Shows"
	marker := writeTemp(t, "mediaroots-applied.json", `{
		"appliedAt": "2026-07-20T12:00:00Z",
		"roots": [ { "path": "/mnt/Media-NAS/TV Shows" } ]
	}`)
	// Field 5 carries the octal-escaped form the kernel actually emits.
	mountinfo := writeTemp(t, "mountinfo",
		`36 35 0:32 / /mnt/Media-NAS/TV\040Shows rw - nfs4 host:/tv rw`+"\n")

	got := mediaRootScopesFrom([]string{root}, marker, mountinfo)
	if s := scopeFor(t, got, root); s != scopeNamespaceScoped {
		t.Errorf("expected namespace_scoped for a space-containing root whose mountinfo entry is \\040-escaped, got %q", s)
	}
}

func TestUnescapeMountinfo(t *testing.T) {
	cases := map[string]string{
		`/plain/path`:                `/plain/path`,
		`/mnt/Media-NAS/TV\040Shows`: `/mnt/Media-NAS/TV Shows`,
		`/a\011b`:                    "/a\tb",
		`/a\012b`:                    "/a\nb",
		`/back\134slash`:             `/back\slash`,
		`/trailing\`:                 `/trailing\`, // lone trailing backslash, not a valid escape
		`/short\04`:                  `/short\04`,  // too few octal digits, left as-is
		`/mnt/A\040B\040C`:           `/mnt/A B C`, // multiple escapes
	}
	for in, want := range cases {
		if got := unescapeMountinfo(in); got != want {
			t.Errorf("unescapeMountinfo(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestMountPointsFromMountinfo_ParsesField5AndUnescapes(t *testing.T) {
	content := "22 21 0:20 / / rw shared:1 - ext4 /dev/root rw\n" +
		`36 35 0:32 / /mnt/Media-NAS/TV\040Shows rw master:2 - nfs4 host:/tv rw` + "\n" +
		"malformed line with too few\n" +
		"\n"
	points, err := mountPointsFromMountinfo(strings.NewReader(content))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !points["/"] {
		t.Error("expected root mount / to be parsed")
	}
	if !points["/mnt/Media-NAS/TV Shows"] {
		t.Errorf("expected un-escaped space path to be parsed, got %+v", points)
	}
}

func TestParseMarker_LenientFieldNames(t *testing.T) {
	// Alternative key names + a bare-string roots element must still parse.
	m := parseMarker([]byte(`{
		"timestamp": "2026-07-20T00:00:00Z",
		"mediaRoots": [ "/mnt/a", { "resolvedPath": "/mnt/b" } ]
	}`))
	if m == nil {
		t.Fatal("expected a parsed marker, got nil")
	}
	if m.AppliedAt == "" {
		t.Error("expected timestamp alias to populate AppliedAt")
	}
	if len(m.Roots) != 2 || m.Roots[0] != "/mnt/a" || m.Roots[1] != "/mnt/b" {
		t.Errorf("expected [/mnt/a /mnt/b], got %+v", m.Roots)
	}
}

func TestParseMarker_MalformedReturnsNil(t *testing.T) {
	if m := parseMarker([]byte(`not json`)); m != nil {
		t.Errorf("expected nil for non-JSON marker, got %+v", m)
	}
}

func TestReadMarker_AbsentFileReturnsNil(t *testing.T) {
	if m := readMarker(filepath.Join(t.TempDir(), "nope.json")); m != nil {
		t.Errorf("expected nil for absent marker file, got %+v", m)
	}
}
