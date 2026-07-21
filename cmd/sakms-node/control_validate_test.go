package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/labbersanon/sakms/internal/nodes"
)

func TestValidateMediaRootPath_AcceptsExistingAbsoluteDir(t *testing.T) {
	dir := t.TempDir()
	canonical, err := validateMediaRootPath(dir)
	if err != nil {
		t.Fatalf("expected a valid absolute directory to be accepted, got: %v", err)
	}
	// The stored form is the fully-resolved path (EvalSymlinks) — on macOS/CI a
	// TempDir often lives under a symlinked /tmp, so compare against the
	// resolved dir, not the raw input.
	wantResolved, _ := filepath.EvalSymlinks(dir)
	if canonical != wantResolved {
		t.Errorf("expected canonicalized path %q, got %q", wantResolved, canonical)
	}
}

func TestValidateMediaRootPath_RejectsRelative(t *testing.T) {
	if _, err := validateMediaRootPath("relative/path"); err == nil {
		t.Error("expected a relative path to be rejected")
	}
}

func TestValidateMediaRootPath_RejectsEmpty(t *testing.T) {
	if _, err := validateMediaRootPath(""); err == nil {
		t.Error("expected an empty path to be rejected")
	}
}

func TestValidateMediaRootPath_RejectsNonexistent(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "does-not-exist")
	if _, err := validateMediaRootPath(missing); err == nil {
		t.Error("expected a nonexistent path to be rejected")
	}
}

func TestValidateMediaRootPath_RejectsFile(t *testing.T) {
	file := filepath.Join(t.TempDir(), "a-file")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := validateMediaRootPath(file); err == nil {
		t.Error("expected a regular file (non-directory) to be rejected")
	}
}

func TestValidateMediaRootPath_ResolvesSymlinkToRealDir(t *testing.T) {
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks not supported in this environment: %v", err)
	}
	canonical, err := validateMediaRootPath(link)
	if err != nil {
		t.Fatalf("expected a symlink to a real dir to resolve and be accepted, got: %v", err)
	}
	wantResolved, _ := filepath.EvalSymlinks(real)
	if canonical != wantResolved {
		t.Errorf("expected the resolved target %q to be stored, got %q", wantResolved, canonical)
	}
}

// TestNodeSettings_HasNoMediaRootsField is the Step 3 wire-invariant regression
// test, driven against the REAL wire DTO type (nodes.NodeSettings) via
// reflection rather than a hand-copied replica of connect()'s settings-push
// lines. The hard invariant (see the plan's "Hard invariant carried over"): the
// control socket adds a LOCAL write path for mediaRoots, and this must not open
// a wire path — structurally guaranteed by NodeSettings having no MediaRoots
// field, so a settings push literally cannot carry one. If a future dev adds
// such a field (or a json tag that decodes "mediaRoots") to the DTO, this fails
// immediately, at the source of the invariant, with the local endpoint present.
func TestNodeSettings_HasNoMediaRootsField(t *testing.T) {
	typ := reflect.TypeOf(nodes.NodeSettings{})
	if _, ok := typ.FieldByName("MediaRoots"); ok {
		t.Fatal("nodes.NodeSettings must NEVER carry a MediaRoots field: mediaRoots is local-node-only and must not be settable via the wire")
	}
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		tag := strings.ToLower(f.Tag.Get("json"))
		if strings.Contains(tag, "mediaroots") {
			t.Fatalf("nodes.NodeSettings field %q has a json tag %q that would decode a wire mediaRoots key", f.Name, f.Tag.Get("json"))
		}
	}
}
