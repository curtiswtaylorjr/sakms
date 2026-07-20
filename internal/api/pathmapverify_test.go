package api

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/labbersanon/sakms/internal/nodes"
	"github.com/labbersanon/sakms/internal/nodesettings"
)

// newFakeBrowseRegistry returns a real *nodes.Registry with one connected
// fake node ("node-a") that answers any browse request with entries named
// after names. Built on connectFakeNode (pathmapverify_integration_test.go),
// the single shared implementation of this fake-node pattern.
func newFakeBrowseRegistry(t *testing.T, names []string) *nodes.Registry {
	t.Helper()
	r := nodes.New()
	connectFakeNode(t, r, "node-a", names)
	return r
}

func setOf(names ...string) map[string]bool {
	m := make(map[string]bool, len(names))
	for _, n := range names {
		m[n] = true
	}
	return m
}

func TestContainment_FullMatch(t *testing.T) {
	a := setOf("Foo", "Bar", "Baz")
	b := setOf("Foo", "Bar", "Baz")
	if got := containment(a, b); got != 1.0 {
		t.Errorf("got %v, want 1.0", got)
	}
}

func TestContainment_Asymmetric_ExtraOnLargerSideDoesNotCount(t *testing.T) {
	// The smaller side is fully contained in the larger; the larger side's
	// extra entry (e.g. a transient .partial file) must not reduce the score.
	smaller := setOf("Foo", "Bar")
	larger := setOf("Foo", "Bar", "movie.mkv.partial")
	if got := containment(smaller, larger); got != 1.0 {
		t.Errorf("got %v, want 1.0 (asymmetric containment ignores extras on the larger side)", got)
	}
}

func TestContainment_N5Boundary_4of5PassesAt80Percent(t *testing.T) {
	// Both sets are the same size (5) so neither is favored by the
	// smaller-side selection -- 4 of server's 5 entries appear in node.
	server := setOf("A", "B", "C", "D", "E")
	node := setOf("A", "B", "C", "D", "Z") // missing E, has an unrelated Z
	got := containment(server, node)
	if got != 0.8 {
		t.Fatalf("got %v, want exactly 0.8", got)
	}
	if got < verifyThreshold {
		t.Errorf("0.8 must satisfy the >= 0.8 threshold (boundary is inclusive)")
	}
}

func TestContainment_N5Boundary_3of5FailsAt60Percent(t *testing.T) {
	server := setOf("A", "B", "C", "D", "E")
	node := setOf("A", "B", "C", "Y", "Z") // only 3 of 5 present = 60%
	got := containment(server, node)
	if got >= verifyThreshold {
		t.Errorf("got %v, must be below the 0.8 threshold", got)
	}
}

func TestContainment_NoSmallListingException_SingleCoincidentalMatchFails(t *testing.T) {
	// The removed v2 "≥1 match when ≤3 entries" exception would have passed
	// this at 33% -- confirming it's gone and the uniform threshold applies.
	// Both listings are the same small size (3), sharing exactly one title.
	server := setOf("Inception", "Interstellar", "Tenet")
	node := setOf("Inception", "SomeOtherMovie", "AnotherMovie")
	got := containment(server, node)
	if got >= verifyThreshold {
		t.Errorf("a single coincidental match on a small listing must fail the uniform 80%% gate, got %v", got)
	}
}

func TestContainment_EmptyEitherSide(t *testing.T) {
	if got := containment(setOf(), setOf("A")); got != 0 {
		t.Errorf("empty smaller side: got %v, want 0", got)
	}
}

func TestVerifyNodePathMapping_EmptyServerListing_BootstrapAccept(t *testing.T) {
	serverDir := t.TempDir() // empty
	reg := newFakeBrowseRegistry(t, []string{"Movie A", "Movie B"})

	status, err := verifyNodePathMapping(context.Background(), reg, "node-a", serverDir, "/mnt/movies")
	if err != nil {
		t.Fatalf("expected accept (bootstrap), got error: %v", err)
	}
	if status != nodesettings.VerificationUnverifiedBootstrap {
		t.Errorf("got status %q, want unverified_bootstrap", status)
	}
}

func TestVerifyNodePathMapping_GoodMatch_Verified(t *testing.T) {
	serverDir := t.TempDir()
	for _, name := range []string{"Movie A", "Movie B", "Movie C"} {
		if err := os.Mkdir(filepath.Join(serverDir, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	reg := newFakeBrowseRegistry(t, []string{"Movie A", "Movie B", "Movie C"})

	status, err := verifyNodePathMapping(context.Background(), reg, "node-a", serverDir, "/mnt/movies")
	if err != nil {
		t.Fatalf("expected accept, got error: %v", err)
	}
	if status != nodesettings.VerificationVerified {
		t.Errorf("got status %q, want verified", status)
	}
}

func TestVerifyNodePathMapping_WrongDirectory_Rejected(t *testing.T) {
	serverDir := t.TempDir()
	for _, name := range []string{"Movie A", "Movie B", "Movie C"} {
		if err := os.Mkdir(filepath.Join(serverDir, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// Node reports an entirely unrelated directory listing.
	reg := newFakeBrowseRegistry(t, []string{"Downloads", "Torrents", "Cache"})

	_, err := verifyNodePathMapping(context.Background(), reg, "node-a", serverDir, "/mnt/wrong")
	if err == nil {
		t.Fatal("expected a mismatch error, got nil")
	}
	if _, ok := err.(*errMappingMismatch); !ok {
		t.Errorf("expected *errMappingMismatch, got %T: %v", err, err)
	}
}

func TestVerifyNodePathMapping_NodeUnreachable_DistinctFromMismatch(t *testing.T) {
	serverDir := t.TempDir()
	if err := os.Mkdir(filepath.Join(serverDir, "Movie A"), 0o755); err != nil {
		t.Fatal(err)
	}
	// An empty registry: "some-offline-node" was never connected.
	reg := nodes.New()

	_, err := verifyNodePathMapping(context.Background(), reg, "some-offline-node", serverDir, "/mnt/movies")
	if err == nil {
		t.Fatal("expected an error for an unreachable node")
	}
	var unreachable *errNodeUnreachable
	if !errors.As(err, &unreachable) {
		t.Errorf("expected *errNodeUnreachable, got %T: %v", err, err)
	}
	var mismatch *errMappingMismatch
	if errors.As(err, &mismatch) {
		t.Error("an unreachable-node error must NOT be reported as a mapping mismatch -- these are distinct failure modes (per this addendum's observability requirement)")
	}
}

func TestVerifyNodePathMapping_FlatFileOnlyLibrary_ComparesFilesNotJustDirs(t *testing.T) {
	serverDir := t.TempDir()
	for _, name := range []string{"Movie A.mkv", "Movie B.mkv"} {
		if err := os.WriteFile(filepath.Join(serverDir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	reg := newFakeBrowseRegistry(t, []string{"Movie A.mkv", "Movie B.mkv"})

	status, err := verifyNodePathMapping(context.Background(), reg, "node-a", serverDir, "/mnt/movies")
	if err != nil {
		t.Fatalf("expected accept for a matching flat file-only library, got error: %v", err)
	}
	if status != nodesettings.VerificationVerified {
		t.Errorf("got status %q, want verified (files must be compared, not just dirs)", status)
	}
}
