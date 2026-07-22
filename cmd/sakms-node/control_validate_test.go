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

// TestValidatePathMapLocal_Stage4 exercises the node-side half of Stage 4's
// nodePath guardrails: validatePathMapLocal must reject an empty path, a
// filesystem-root/too-shallow path (via the shared nodepath.Trivial rule), and
// a path that resolves OUTSIDE every configured mediaRoot (withinMediaRoots),
// while accepting a real directory contained within a configured mediaRoot and
// returning its canonical form. This is where the node's local rejection happens
// BEFORE any push is scheduled.
func TestValidatePathMapLocal_Stage4(t *testing.T) {
	mediaRoot := t.TempDir()
	within := filepath.Join(mediaRoot, "movies")
	if err := os.MkdirAll(within, 0o755); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir() // a real dir, but not under mediaRoot

	t.Run("empty is rejected", func(t *testing.T) {
		if _, err := validatePathMapLocal("", []string{mediaRoot}); err == nil {
			t.Error("expected an empty nodePath to be rejected")
		}
	})
	t.Run("filesystem root is rejected (trivial)", func(t *testing.T) {
		// "/" exists and is a directory, so it clears validateMediaRootPath and is
		// rejected specifically by the shared triviality rule.
		if _, err := validatePathMapLocal("/", []string{mediaRoot}); err == nil {
			t.Error("expected \"/\" to be rejected as too shallow")
		}
	})
	t.Run("outside all mediaRoots is rejected", func(t *testing.T) {
		if _, err := validatePathMapLocal(outside, []string{mediaRoot}); err == nil {
			t.Errorf("expected a path outside every mediaRoot (%q not under %q) to be rejected", outside, mediaRoot)
		}
	})
	t.Run("within a mediaRoot is accepted", func(t *testing.T) {
		got, err := validatePathMapLocal(within, []string{mediaRoot})
		if err != nil {
			t.Fatalf("expected a contained real dir to be accepted, got: %v", err)
		}
		wantResolved, _ := filepath.EvalSymlinks(within)
		if got != wantResolved {
			t.Errorf("expected canonical %q, got %q", wantResolved, got)
		}
	})
}

// sharedTrivialCases mirrors the canonical table in internal/nodepath (which
// owns the single implementation). Running it against BOTH node validators here
// proves they agree with that shared rule rather than re-deriving depth
// independently — the structural single-source-of-truth Stage 4 consolidates to.
var sharedTrivialCases = []struct {
	path    string
	trivial bool
}{
	{"", true},
	{"/", true},
	{"//", true},
	{"/mnt", true},
	{"/mnt/", true},
	{"/mnt/media", false},
	{"/srv/tank/movies", false},
}

// TestMediaRootsUsable_SharedRule proves mediaRootsUsable rejects exactly the
// self-reported mediaRoot entries the shared nodepath.Trivial rule calls trivial
// (empty/root/too-shallow) and accepts the rest — the node-side presence gate
// (D9) built on the same consolidated rule the server uses.
func TestMediaRootsUsable_SharedRule(t *testing.T) {
	// Empty list is always rejected.
	if err := mediaRootsUsable(nil); err == nil {
		t.Error("expected an empty mediaRoots list to be rejected")
	}
	for _, tc := range sharedTrivialCases {
		if tc.path == "" {
			continue // "" as a lone entry is covered by the empty-list case above
		}
		err := mediaRootsUsable([]string{tc.path})
		if tc.trivial && err == nil {
			t.Errorf("mediaRootsUsable([%q]) = nil, want a rejection (trivial per shared rule)", tc.path)
		}
		if !tc.trivial && err != nil {
			t.Errorf("mediaRootsUsable([%q]) = %v, want nil (non-trivial per shared rule)", tc.path, err)
		}
	}
	// A trivial entry mixed in with valid ones still rejects the whole list.
	if err := mediaRootsUsable([]string{"/mnt/media", "/mnt"}); err == nil {
		t.Error("expected a list containing a trivial entry to be rejected")
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
