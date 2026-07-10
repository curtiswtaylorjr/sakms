package rename

import (
	"context"
	"time"

	"github.com/curtiswtaylorjr/sakms/internal/mode"
	"github.com/curtiswtaylorjr/sakms/internal/proposals"
	"github.com/curtiswtaylorjr/sakms/internal/servarr"
	"github.com/curtiswtaylorjr/sakms/internal/stashapi"
)

// forceGenerate* bound how long Scan waits for Stash to compute a phash it
// didn't already have (scanGeneratePhashes: true, triggered via
// stash.ScanPaths). Phash generation decodes video frames, so it's not
// instant — the timeout scales with how many files are missing one rather
// than using a single fixed bound.
const (
	forceGenerateBaseTimeout  = 30 * time.Second
	forceGeneratePerFileScale = 5 * time.Second
	forceGeneratePollInterval = 2 * time.Second
)

// adultCandidate pairs one unmapped folder with the root it was found under
// — the unit scanAdultPhashFirst batches through the phash-first pipeline.
type adultCandidate struct {
	root servarr.RootFolder
	uf   servarr.UnmappedFolder
}

// scanAdultPhashFirst resolves candidates via Stash's own phash fingerprints
// first — a batched StashDB->FansDB->TPDB cascade lookup (identify.GiveBack's
// configured boxes) — falling back to the legacy AI/text identification
// pipeline (proposeOneAdult) for anything the cascade can't resolve,
// including candidates Stash has no phash for at all even after a targeted
// force-generate rescan.
//
// This restores phash as Adult's PRIMARY identification signal (matching the
// prior CLI this was ported from), tried before AI/web-search rather than as
// a supplementary check — see docs/ROADMAP.md's phash decision entry. Only
// called when sess.Stash != nil (see Scan); sess.Identify is already
// guaranteed non-nil for Adult by Scan's own upfront check.
func scanAdultPhashFirst(
	ctx context.Context, sess *mode.Session,
	candidates []adultCandidate, tracked []servarr.TrackedItem, profiles []servarr.QualityProfile,
) []proposals.Proposal {
	paths := make([]string, len(candidates))
	for i, c := range candidates {
		paths[i] = c.uf.Path
	}

	files, err := sess.Stash.FindSceneInfoByPaths(ctx, paths)
	if err != nil {
		// Fail open: Stash being unreachable shouldn't block the whole Adult
		// scan — every candidate still gets identified, just via the slower
		// legacy path.
		return legacyProposeAll(ctx, sess, candidates, tracked, profiles)
	}

	var missing []string
	for _, path := range paths {
		if f, ok := files[path]; !ok || f.PHash == "" {
			missing = append(missing, path)
		}
	}
	if len(missing) > 0 {
		for path, f := range refreshMissingPhashes(ctx, sess.Stash, missing) {
			files[path] = f
		}
	}

	phashByPath := make(map[string]string, len(files))
	var phashes []string
	for path, f := range files {
		if f != nil && f.PHash != "" {
			phashByPath[path] = f.PHash
			phashes = append(phashes, f.PHash)
		}
	}

	matches, err := sess.Identify.LookupFingerprints(ctx, phashes)
	if err != nil {
		return legacyProposeAll(ctx, sess, candidates, tracked, profiles)
	}

	var out []proposals.Proposal
	var fallback []adultCandidate
	for _, c := range candidates {
		phash := phashByPath[c.uf.Path]
		match, hit := matches[phash]
		if phash == "" || !hit {
			fallback = append(fallback, c)
			continue
		}
		p := buildAdultProposal(sess.Mode, c.root, c.uf, match, nil, tracked, profiles)
		p.PHash = phash
		if f := files[c.uf.Path]; f != nil {
			p.DurationSeconds = int(f.Duration)
		}
		out = append(out, p)
	}

	return append(out, legacyProposeAll(ctx, sess, fallback, tracked, profiles)...)
}

// legacyProposeAll runs the AI/text identification pipeline (proposeOneAdult)
// for every candidate the phash cascade didn't resolve.
func legacyProposeAll(
	ctx context.Context, sess *mode.Session,
	candidates []adultCandidate, tracked []servarr.TrackedItem, profiles []servarr.QualityProfile,
) []proposals.Proposal {
	out := make([]proposals.Proposal, 0, len(candidates))
	for _, c := range candidates {
		out = append(out, proposeOneAdult(ctx, sess.Identify, sess.Mode, c.root, c.uf, tracked, profiles))
	}
	return out
}

// refreshMissingPhashes triggers one targeted Stash rescan covering every
// path in missing, waits for the job to finish (bounded by a size-scaled
// timeout), then re-reads whatever Stash now reports for those paths.
// Best-effort: a scan-trigger, job-wait, or re-read failure just means these
// paths stay without a phash for this Scan run — the caller falls through to
// the legacy pipeline for them rather than blocking the whole Scan. Returns
// nil (not an error) in every failure case.
func refreshMissingPhashes(ctx context.Context, stash *stashapi.Client, missing []string) map[string]*stashapi.StashFile {
	jobID, err := stash.ScanPaths(ctx, missing, false)
	if err != nil {
		return nil
	}

	timeout := forceGenerateBaseTimeout + time.Duration(len(missing))*forceGeneratePerFileScale
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if _, err := stash.WaitJob(waitCtx, jobID, forceGeneratePollInterval); err != nil {
		return nil
	}

	refreshed, err := stash.FindSceneInfoByPaths(ctx, missing)
	if err != nil {
		return nil
	}
	return refreshed
}
