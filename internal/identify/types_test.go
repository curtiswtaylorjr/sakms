package identify

import "testing"

// TestWhisparrForeignID pins the single normalized-foreignID derivation both
// rename (classifyAdultMatch) and dedup (adultForeignID) now delegate to, so a
// future change here surfaces as a failure rather than a silent cross-package
// divergence.
func TestWhisparrForeignID(t *testing.T) {
	cases := []struct {
		name   string
		res    MatchResult
		wantID string
		wantOK bool
	}{
		{"stashdb raw uuid", MatchResult{Box: "stashdb", SceneID: "u1"}, "u1", true},
		{"fansdb raw uuid", MatchResult{Box: "fansdb", SceneID: "u2"}, "u2", true},
		{"tpdb gets prefix", MatchResult{Box: "tpdb", SceneID: "77"}, "tpdbId:77", true},
		{"empty scene id", MatchResult{Box: "stashdb", SceneID: ""}, "", false},
		{"empty box (web-only)", MatchResult{Box: "", SceneID: "u1"}, "", false},
		{"empty both", MatchResult{}, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			id, ok := tc.res.WhisparrForeignID()
			if id != tc.wantID || ok != tc.wantOK {
				t.Errorf("got (%q,%v), want (%q,%v)", id, ok, tc.wantID, tc.wantOK)
			}
		})
	}
}
