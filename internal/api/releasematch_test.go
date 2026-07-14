package api

import (
	"context"
	"fmt"
	"testing"

	"github.com/curtiswtaylorjr/sakms/internal/mode"
	"github.com/curtiswtaylorjr/sakms/internal/prowlarr"
)

// fakeReleaseMatchAI is a minimal identify.AIClient fake — counts calls and
// delegates each response to fn, so tests can script exactly what the
// (simulated) AI extracts per prompt without a real Ollama/OpenAI/etc. round
// trip. Mirrors the shape of internal/rename's countingAI fake.
type fakeReleaseMatchAI struct {
	fn    func(prompt string) (map[string]any, error)
	calls int
}

func (f *fakeReleaseMatchAI) ChatJSON(ctx context.Context, prompt string) (map[string]any, error) {
	f.calls++
	if f.fn != nil {
		return f.fn(prompt)
	}
	return nil, nil
}

// TestFilterReleases_FastPathTitleAndLanguage covers the plan's four
// deterministic (no-AI) cases: exact match, a heavily noisy scene-release
// title that still contains every target-title token, a foreign-language
// tag rejecting an otherwise-matching title, and an ambiguous/partial title
// match (shares only a generic word) that the fast path correctly rejects.
func TestFilterReleases_FastPathTitleAndLanguage(t *testing.T) {
	cases := []struct {
		name        string
		targetTitle string
		release     prowlarr.Release
		wantKept    bool
	}{
		{
			name:        "exact match",
			targetTitle: "The Dark Knight",
			release:     prowlarr.Release{GUID: "1", Title: "The.Dark.Knight.2008.1080p.BluRay.x264-GROUP"},
			wantKept:    true,
		},
		{
			name:        "noisy scene-release title (heavy tags, every target token still present)",
			targetTitle: "The Matrix Resurrections",
			release:     prowlarr.Release{GUID: "1", Title: "The.Matrix.Resurrections.2021.2160p.WEB-DL.DDP5.1.Atmos.HDR.DV.x265-GROUP"},
			wantKept:    true,
		},
		{
			name:        "foreign-language tag rejection (title matches, but FRENCH tag present)",
			targetTitle: "The Dark Knight",
			release:     prowlarr.Release{GUID: "1", Title: "The.Dark.Knight.2008.FRENCH.1080p.BluRay.x264-GROUP"},
			wantKept:    false,
		},
		{
			name:        "ambiguous/partial title match (shares only one generic word) rejected",
			targetTitle: "Show One Two Three",
			release:     prowlarr.Release{GUID: "1", Title: "Show.Four.Five.Six.2020-GROUP"},
			wantKept:    false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := FilterReleases(context.Background(), []prowlarr.Release{tc.release}, tc.targetTitle, mode.Movies, nil)
			gotKept := len(got) == 1
			if gotKept != tc.wantKept {
				t.Errorf("FilterReleases(%q, %q) kept=%v, want kept=%v", tc.release.Title, tc.targetTitle, gotKept, tc.wantKept)
			}
		})
	}
}

// TestFilterReleases_AIEscalation_NilClientDegradesCleanly is the plan's
// explicit nil-safety requirement: when the fast path keeps nothing AND no
// AI client is configured, the filter must return zero candidates cleanly —
// never panic, never error.
func TestFilterReleases_AIEscalation_NilClientDegradesCleanly(t *testing.T) {
	releases := []prowlarr.Release{
		{GUID: "1", Title: "xXx.RandomRelease.Whatever.2020-GROUP"},
	}
	got := FilterReleases(context.Background(), releases, "Obscure Title Nobody Knows", mode.Movies, nil)
	if len(got) != 0 {
		t.Fatalf("expected zero candidates with a nil AI client, got %+v", got)
	}
}

// TestFilterReleases_AIEscalation_SkippedWhenFastPathMatches proves the
// "only escalate when the fast path kept ZERO" rule: a configured AI client
// must never be called when the deterministic pass already found a match —
// keeps the common case fast (no AI round-trip per candidate), per the plan.
func TestFilterReleases_AIEscalation_SkippedWhenFastPathMatches(t *testing.T) {
	ai := &fakeReleaseMatchAI{fn: func(prompt string) (map[string]any, error) {
		return map[string]any{"title": "should not be used"}, nil
	}}
	releases := []prowlarr.Release{
		{GUID: "1", Title: "The.Dark.Knight.2008.1080p.BluRay.x264-GROUP"},
	}
	got := FilterReleases(context.Background(), releases, "The Dark Knight", mode.Movies, ai)
	if len(got) != 1 {
		t.Fatalf("expected the fast-path match to survive, got %+v", got)
	}
	if ai.calls != 0 {
		t.Errorf("expected zero AI calls when the fast path already matched, got %d", ai.calls)
	}
}

// TestFilterReleases_AIEscalation_MoviesGuessTitleFindsMatch is the
// AI-escalation path for Movies/Series: a release title too abbreviated for
// the deterministic fast path is recovered once identify.GuessTitle cleans
// it.
func TestFilterReleases_AIEscalation_MoviesGuessTitleFindsMatch(t *testing.T) {
	ai := &fakeReleaseMatchAI{fn: func(prompt string) (map[string]any, error) {
		return map[string]any{"title": "The Dark Knight"}, nil
	}}
	releases := []prowlarr.Release{
		{GUID: "1", Title: "tdk.2008.rip-XYZ"}, // too abbreviated for the fast path
	}
	got := FilterReleases(context.Background(), releases, "The Dark Knight", mode.Movies, ai)
	if len(got) != 1 {
		t.Fatalf("expected AI-escalation (GuessTitle) to recover the match, got %+v", got)
	}
	if ai.calls != 1 {
		t.Errorf("expected exactly one AI call (one candidate), got %d", ai.calls)
	}
}

// TestFilterReleases_AIEscalation_AdultParseFilenameFindsMatch is the
// AI-escalation path for Adult: identify.ParseFilename (the same
// scene-filename-parse prompt already used elsewhere) cleans the release
// title instead of GuessTitle.
func TestFilterReleases_AIEscalation_AdultParseFilenameFindsMatch(t *testing.T) {
	ai := &fakeReleaseMatchAI{fn: func(prompt string) (map[string]any, error) {
		return map[string]any{"studio": "Some Studio", "title": "Wild Scene Title", "performers": []any{}}, nil
	}}
	releases := []prowlarr.Release{
		{GUID: "1", Title: "somestudio.wld.scn.ttl.2020.mp4"},
	}
	got := FilterReleases(context.Background(), releases, "Wild Scene Title", mode.Adult, ai)
	if len(got) != 1 {
		t.Fatalf("expected AI-escalation (ParseFilename) to recover the match, got %+v", got)
	}
}

// TestFilterReleases_AIEscalation_PerCandidateErrorSkipsOnlyThatCandidate
// proves a single candidate's AI failure never fails the whole filter — it
// just drops that one candidate, matching the "degrade cleanly" requirement
// even for the multi-candidate escalation case.
func TestFilterReleases_AIEscalation_PerCandidateErrorSkipsOnlyThatCandidate(t *testing.T) {
	calls := 0
	ai := &fakeReleaseMatchAI{fn: func(prompt string) (map[string]any, error) {
		calls++
		if calls == 1 {
			return nil, fmt.Errorf("simulated AI failure")
		}
		return map[string]any{"title": "The Dark Knight"}, nil
	}}
	releases := []prowlarr.Release{
		{GUID: "1", Title: "bad-release-XYZ"},
		{GUID: "2", Title: "good-release-XYZ"},
	}
	got := FilterReleases(context.Background(), releases, "The Dark Knight", mode.Movies, ai)
	if len(got) != 1 || got[0].GUID != "2" {
		t.Fatalf("expected only the second (successfully-cleaned) candidate to survive, got %+v", got)
	}
}

// TestHasLanguageTag is a direct table-driven check of the deterministic
// language-tag token list, independent of title-similarity scoring.
func TestHasLanguageTag(t *testing.T) {
	cases := []struct {
		title string
		want  bool
	}{
		{"Some.Movie.2020.1080p.BluRay.x264-GROUP", false},
		{"Some.Movie.2020.FRENCH.1080p.BluRay.x264-GROUP", true},
		{"Some.Movie.2020.GERMAN.1080p-GROUP", true},
		{"Some.Movie.2020.MULTI.1080p-GROUP", true},
		{"Some.Movie.2020.VOSTFR.1080p-GROUP", true},
		{"FrenchConnection.2020.1080p-GROUP", false}, // "French" is not a whole word here
		{"Some.Movie.2020.JAPANESE.1080p-GROUP", true},
		{"Some.Movie.2020.KOREAN.1080p-GROUP", true},
		{"Some.Movie.2020.HINDI.1080p-GROUP", true},
		{"Some.Movie.2020.RUSSIAN.1080p-GROUP", true},
		{"Some.Movie.2020.ITALIAN.1080p-GROUP", true},
		{"Some.Movie.2020.SPANISH.1080p-GROUP", true},
	}
	for _, tc := range cases {
		if got := hasLanguageTag(tc.title); got != tc.want {
			t.Errorf("hasLanguageTag(%q) = %v, want %v", tc.title, got, tc.want)
		}
	}
}
