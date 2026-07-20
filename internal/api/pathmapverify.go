package api

import (
	"context"
	"fmt"
	"os"
	"sort"

	"github.com/labbersanon/sakms/internal/nodes"
	"github.com/labbersanon/sakms/internal/nodesettings"
)

// verifyThreshold is the minimum fraction of the smaller listing's entries
// that must also appear in the larger listing for a node path mapping to be
// accepted. Applied uniformly regardless of listing size — deliberately no
// small-listing exception (an earlier "≥1 match when ≤3 entries" carve-out
// was found to be a real security hole: a single coincidentally-shared name
// passed at 33% containment). 0.8 is justified by this daemon's own
// same-physical-files invariant: a correct mapping is the same tree mounted
// at a different point, not a reorganized copy, so a correct mapping should
// show near-total containment.
const verifyThreshold = 0.8

// errMappingMismatch is returned by verifyNodePathMapping when the
// comparison falls below verifyThreshold. Its message carries concrete
// evidence (what was compared, what didn't match) so the operator can fix
// the actual mistake rather than receive a bare "verification failed".
type errMappingMismatch struct{ msg string }

func (e *errMappingMismatch) Error() string { return e.msg }

// errNodeUnreachable is returned by verifyNodePathMapping when the node
// itself couldn't be reached (offline, timed out) — distinct from
// errMappingMismatch so callers report a "node unreachable" condition, not
// "your mapping looks wrong," per this addendum's own observability
// requirement (distinguish "safeguard blocked this" from "node down").
type errNodeUnreachable struct{ msg string }

func (e *errNodeUnreachable) Error() string { return e.msg }

// listServerPathEntries lists path's immediate contents (files and
// directories) directly via os.ReadDir — an internal call against a path
// Library settings itself already owns and the server already manages, not
// through the operator-facing /api/browse endpoint's allowlist (which
// exists for a different trust boundary: validating operator-typed input,
// not reading a value this server itself already trusts).
func listServerPathEntries(path string) (map[string]bool, error) {
	infos, err := os.ReadDir(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]bool{}, nil
		}
		return nil, err
	}
	names := make(map[string]bool, len(infos))
	for _, info := range infos {
		names[info.Name()] = true
	}
	return names, nil
}

// containment computes the asymmetric containment of the smaller set within
// the larger set: |intersection| / |smaller|. This is NOT symmetric
// Jaccard/union overlap — the asymmetric form is what tolerates one-sided
// transient state (an in-progress download, a file mid-transcode present on
// only one side at listing time) without needing threshold slack to
// compensate, since those extra entries land on the larger side and never
// count against containment.
func containment(a, b map[string]bool) float64 {
	smaller, larger := a, b
	if len(b) < len(a) {
		smaller, larger = b, a
	}
	if len(smaller) == 0 {
		return 0
	}
	matches := 0
	for name := range smaller {
		if larger[name] {
			matches++
		}
	}
	return float64(matches) / float64(len(smaller))
}

// verifyNodePathMapping is Safeguard 1: it compares the server's own
// listing of serverPath against the node's live listing of nodePath (via
// the existing isolated browse lane, requested with IncludeFiles so a flat,
// file-only library doesn't compare as empty on every save) and returns the
// resulting VerificationStatus, or an error if the mapping looks wrong.
//
// Either listing being empty is the bootstrap case (a freshly created
// library root, or a brand-new node mount) — accepted, but recorded as
// unverified_bootstrap rather than verified, since there was nothing to
// meaningfully compare. This is a HARD GATE, not advisory: a non-empty
// comparison below verifyThreshold returns an error, and the caller must
// not persist or push that row.
//
// Residual, structurally accepted limitation: no directory-listing
// comparison can distinguish a correct mapping from a different-but-similar
// sibling library that happens to score above threshold. That risk is
// bounded by the node's own independent mediaRoots allowlist, not by this
// function — this function's job is catching gross mismatches (wrong
// mount, wrong depth, an unrelated directory), not proving byte-for-byte
// identity.
func verifyNodePathMapping(ctx context.Context, reg *nodes.Registry, nodeID, serverPath, nodePath string) (nodesettings.VerificationStatus, error) {
	serverEntries, err := listServerPathEntries(serverPath)
	if err != nil {
		return "", fmt.Errorf("reading server path %q: %w", serverPath, err)
	}

	result, err := reg.RequestBrowse(nodeID, nodePath, true)
	if err != nil {
		// Distinct type from errMappingMismatch: this is "the node didn't
		// answer" (offline, timed out), not "the mapping looks wrong" — the
		// caller must not conflate the two into a single "fix your mapping"
		// response.
		return "", &errNodeUnreachable{msg: fmt.Sprintf("listing node path %q: %s", nodePath, err)}
	}
	nodeEntries := make(map[string]bool, len(result.Entries))
	for _, e := range result.Entries {
		nodeEntries[e.Name] = true
	}

	if len(serverEntries) == 0 || len(nodeEntries) == 0 {
		return nodesettings.VerificationUnverifiedBootstrap, nil
	}

	c := containment(serverEntries, nodeEntries)
	if c < verifyThreshold {
		return "", &errMappingMismatch{msg: fmt.Sprintf(
			"node path %q does not look like a match for %q: server sees %v; node sees %v; %.0f%% containment, below the %.0f%% threshold",
			nodePath, serverPath, sortedKeys(serverEntries), sortedKeys(nodeEntries), c*100, verifyThreshold*100,
		)}
	}
	return nodesettings.VerificationVerified, nil
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
