package api

import (
	"context"
	"regexp"

	"github.com/curtiswtaylorjr/sakms/internal/identify"
	"github.com/curtiswtaylorjr/sakms/internal/mode"
	"github.com/curtiswtaylorjr/sakms/internal/prowlarr"
)

// titleSimilarityFloor mirrors internal/identify.ExtractFromSearch's own
// reject threshold (< 0.2) — the same deterministic similarity cut already
// tuned for exactly this "messy scene-name vs. known canonical title"
// comparison, not a new number invented here.
const titleSimilarityFloor = 0.2

// languageTagPattern is a small, explicit, deterministic token list marking
// a release title as carrying a non-English language tag — English is the
// assumed unmarked default (the same convention scene-release naming already
// uses), so a release is only rejected when one of these tags is actually
// present in the title, never guessed absent. This is NOT a user-facing
// preference/setting (the plan is explicit: "don't build speculative config
// ahead of proven need") — easy to make configurable later if it's ever
// wrong for someone. Word-boundary matched, case-insensitive, mirroring
// internal/release.Parse's own regexp convention for title-token matching.
var languageTagPattern = regexp.MustCompile(`(?i)\b(french|german|spanish|italian|vostfr|russian|hindi|korean|japanese|multi)\b`)

// hasLanguageTag reports whether title carries one of languageTagPattern's
// non-English tags.
func hasLanguageTag(title string) bool {
	return languageTagPattern.MatchString(title)
}

// FilterReleases applies the Discover detail-popup plan's title-match +
// language filter pass to raw Prowlarr releases before any tier/protocol
// grading — the popup's search needs this because a raw title/ID-scoped
// Prowlarr search returns "widely varied" results (wrong language, loosely-
// matched titles), and without this pass the availability signal would be
// noisy garbage (see the plan's Context section).
//
// Two-stage title match, built on internal/identify's already-existing
// pieces rather than a fresh regex title-cleaner:
//
//  1. Fast path (always runs, no AI call): internal/identify.TitleSimilarity
//     against targetTitle vs. each candidate's raw release title — already
//     tested, already tuned for this exact comparison.
//  2. AI-escalation path (only reached when the fast path kept ZERO
//     candidates): mirrors internal/identify.Identify's own cheap-first,
//     AI-as-escalation structure. Runs ONLY when aiClient is non-nil — a nil
//     client (AI features not configured, the tolerant-nil convention every
//     mode.Session client already follows) degrades cleanly to "no
//     candidates," never an error.
//
// Argument order for TitleSimilarity calls follows
// internal/identify.ExtractFromSearch's own established convention
// (TitleSimilarity(extracted.Title, stem)): the shorter, canonical/clean
// title first, the longer/noisier scene-release-style title second — this
// is what lets TitleSimilarity's containment shortcut (see its doc comment)
// recognize "every token of the canonical title appears somewhere in this
// noisy release title" rather than falling back to a plain Jaccard score
// that a short canonical title against a long noisy one would otherwise
// score low on.
//
// Then, regardless of which title-match stage produced the surviving set, a
// deterministic language-tag filter (hasLanguageTag) drops any release
// carrying a non-English tag. Order preserved; prowlarr.Release fields are
// passed through unchanged so the caller can still pair filtered releases'
// indices 1:1 with a derived []autograb.Candidate slice (see
// buildAutoGrabCandidates's existing index-pairing convention, which this
// filter's output feeds into unchanged).
func FilterReleases(ctx context.Context, releases []prowlarr.Release, targetTitle string, m mode.Mode, aiClient identify.AIClient) []prowlarr.Release {
	fastMatched := make([]prowlarr.Release, 0, len(releases))
	for _, rel := range releases {
		if identify.TitleSimilarity(targetTitle, rel.Title) >= titleSimilarityFloor {
			fastMatched = append(fastMatched, rel)
		}
	}

	matched := fastMatched
	if len(matched) == 0 && aiClient != nil {
		matched = aiEscalateTitleMatch(ctx, releases, targetTitle, m, aiClient)
	}

	out := make([]prowlarr.Release, 0, len(matched))
	for _, rel := range matched {
		if !hasLanguageTag(rel.Title) {
			out = append(out, rel)
		}
	}
	return out
}

// aiEscalateTitleMatch is FilterReleases' AI-assisted fallback, only ever
// reached when the deterministic fast path kept nothing. Each release title
// is cleaned by AI (internal/identify.GuessTitle for Movies/Series,
// internal/identify.ParseFilename for Adult — the SAME prompt already used
// for scene-release filenames, which is exactly what a Prowlarr release
// title also looks like) and the cleaned title is re-compared via
// TitleSimilarity. A per-candidate AI failure (a real error, OR
// GuessTitle/ParseFilename's own "declined to guess" empty-title result)
// just drops that one candidate — it never fails the whole filter, matching
// the "no candidates" degrade-cleanly requirement.
func aiEscalateTitleMatch(ctx context.Context, releases []prowlarr.Release, targetTitle string, m mode.Mode, aiClient identify.AIClient) []prowlarr.Release {
	out := make([]prowlarr.Release, 0)
	for _, rel := range releases {
		cleaned, err := cleanReleaseTitle(ctx, rel.Title, m, aiClient)
		if err != nil || cleaned == "" {
			continue
		}
		if identify.TitleSimilarity(targetTitle, cleaned) >= titleSimilarityFloor {
			out = append(out, rel)
		}
	}
	return out
}

// cleanReleaseTitle asks AI to extract a cleaned title from a raw release
// title — GuessTitle for Movies/Series (the mainstream title-guess prompt),
// ParseFilename for Adult (the scene-filename parse prompt; releaseTitle
// plays the role of the filename stem, with no parent-folder context since a
// Prowlarr release has none).
func cleanReleaseTitle(ctx context.Context, releaseTitle string, m mode.Mode, aiClient identify.AIClient) (string, error) {
	if m == mode.Adult {
		parsed, err := identify.ParseFilename(ctx, aiClient, releaseTitle, "")
		if err != nil {
			return "", err
		}
		return parsed.Title, nil
	}
	return identify.GuessTitle(ctx, aiClient, releaseTitle)
}
