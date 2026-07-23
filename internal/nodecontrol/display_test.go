package nodecontrol

import (
	"reflect"
	"testing"
)

// --- mediaRoots drift + scope --------------------------------------------

func TestContainmentDrift(t *testing.T) {
	cases := []struct {
		name   string
		scopes []MediaRootStatus
		want   bool
	}{
		{"empty", nil, false},
		{"pure app-level (no containment on node)", []MediaRootStatus{
			{Path: "/a", Scope: "app_level_only"},
			{Path: "/b", Scope: "app_level_only"},
		}, false},
		{"all namespace_scoped (in sync)", []MediaRootStatus{
			{Path: "/a", Scope: "namespace_scoped"},
		}, false},
		{"mixed → drift", []MediaRootStatus{
			{Path: "/a", Scope: "namespace_scoped"},
			{Path: "/b", Scope: "app_level_only"},
		}, true},
		{"unbound counts as active → drift", []MediaRootStatus{
			{Path: "/a", Scope: "namespace_scoped_but_unbound"},
			{Path: "/b", Scope: "app_level_only"},
		}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ContainmentDrift(c.scopes); got != c.want {
				t.Errorf("ContainmentDrift = %v, want %v", got, c.want)
			}
		})
	}
}

// --- path-mapping pure logic ---------------------------------------------

func TestBuildKeyRows(t *testing.T) {
	catalog := []string{"movies_library_root_folder", "series_library_root_folder", "adult_library_root_folder"}
	authored := []AuthoredMapping{
		{Key: "movies_library_root_folder", NodePath: "/mnt/movies"},
		{Key: "series_library_root_folder", NodePath: ""}, // blank = skip, treated as unset
	}
	got := BuildKeyRows(catalog, authored, nil)
	want := []KeyRow{
		{Key: "movies_library_root_folder", NodePath: "/mnt/movies", Mapped: true, HasAuthoredKey: true},
		{Key: "series_library_root_folder", NodePath: "", Mapped: false},
		{Key: "adult_library_root_folder", NodePath: "", Mapped: false},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("BuildKeyRows = %+v, want %+v", got, want)
	}
}

func TestBuildKeyRows_PreservesCatalogOrderAndIgnoresUnknownAuthored(t *testing.T) {
	catalog := []string{"b_key", "a_key"}
	authored := []AuthoredMapping{
		{Key: "a_key", NodePath: "/a"},
		{Key: "ghost_key", NodePath: "/ghost"}, // not in catalog → not rendered
	}
	got := BuildKeyRows(catalog, authored, nil)
	want := []KeyRow{
		{Key: "b_key", NodePath: "", Mapped: false},
		{Key: "a_key", NodePath: "/a", Mapped: true, HasAuthoredKey: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("BuildKeyRows = %+v, want %+v", got, want)
	}
}

// TestKeyDisplayLabel_KnownKeys proves all 5 known catalog keys render as their
// human-friendly label (none of which contain an underscore to mangle).
func TestKeyDisplayLabel_KnownKeys(t *testing.T) {
	cases := map[string]string{
		"movies_library_root_folder": "Movies",
		"series_library_root_folder": "Series",
		"adult_library_root_folder":  "Adult",
		"movies_kids_root_path":      "Movies (Kids)",
		"series_kids_root_path":      "Series (Kids)",
	}
	for key, want := range cases {
		if got := KeyDisplayLabel(key); got != want {
			t.Errorf("KeyDisplayLabel(%q) = %q, want %q", key, got, want)
		}
	}
}

// TestKeyDisplayLabel_UnknownKeyFallsBackToEscapedRaw proves an unrecognized
// catalog key (a future addition) falls back to the mnemonic-escaped raw key
// rather than crashing or rendering blank. (Stage 0 preserves the doubled-
// underscore fallback verbatim; Stage 1 changes it to the raw key per plan U1.)
func TestKeyDisplayLabel_UnknownKeyFallsBackToEscapedRaw(t *testing.T) {
	if got := KeyDisplayLabel("future_unknown_key"); got != "future__unknown__key" {
		t.Errorf("unknown key = %q, want escaped raw %q", got, "future__unknown__key")
	}
	if got := KeyDisplayLabel(""); got != "" {
		t.Errorf("empty key = %q, want %q (no crash, no blank-label panic)", got, "")
	}
}

// TestBuildKeyRows_LegacyPathMapOnly is the primary regression test for Bug 2:
// a key that has a live PathMap (Remap) entry but NO AuthoredPaths record — the
// exact shape of a mapping set via the OLD server-side operator UI — must render
// as Mapped=true with the PathMap's real Local path, not "not set". Before the
// fix, BuildKeyRows consulted only AuthoredPaths and reported these as unset.
func TestBuildKeyRows_LegacyPathMapOnly(t *testing.T) {
	catalog := []string{"movies_library_root_folder", "series_library_root_folder", "adult_library_root_folder"}
	authored := []AuthoredMapping(nil) // node never authored these — legacy operator mappings
	pathMap := []RemapEntry{
		{Server: "/srv/movies", Local: "/mnt/movies", Key: "movies_library_root_folder"},
		{Server: "/srv/series", Local: "/mnt/series", Key: "series_library_root_folder"},
		{Server: "/srv/adult", Local: "/mnt/adult", Key: "adult_library_root_folder"},
	}
	got := BuildKeyRows(catalog, authored, pathMap)
	want := []KeyRow{
		{Key: "movies_library_root_folder", NodePath: "/mnt/movies", Mapped: true},
		{Key: "series_library_root_folder", NodePath: "/mnt/series", Mapped: true},
		{Key: "adult_library_root_folder", NodePath: "/mnt/adult", Mapped: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("BuildKeyRows (legacy PathMap-only) = %+v, want %+v", got, want)
	}
}

// TestBuildKeyRows_PendingAuthoredFallback proves the pending case still works:
// a key with an AuthoredPaths record but no matching PathMap entry yet (the set
// was pushed but the server hasn't echoed it back into the Remap table) still
// renders as mapped, using the authored value.
func TestBuildKeyRows_PendingAuthoredFallback(t *testing.T) {
	catalog := []string{"movies_library_root_folder"}
	authored := []AuthoredMapping{{Key: "movies_library_root_folder", NodePath: "/mnt/just-picked"}}
	pathMap := []RemapEntry(nil) // server hasn't echoed the Remap pair back yet
	got := BuildKeyRows(catalog, authored, pathMap)
	want := []KeyRow{{Key: "movies_library_root_folder", NodePath: "/mnt/just-picked", Mapped: true, HasAuthoredKey: true}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("BuildKeyRows (pending authored fallback) = %+v, want %+v", got, want)
	}
}

// TestBuildKeyRows_LivePathMapWinsOverAuthored proves precedence: when both a
// live PathMap entry and an AuthoredPaths record exist for a key and disagree
// (a re-pick whose new value the server hasn't echoed yet), the PathMap Local —
// what is actually in effect for dispatch right now — wins.
func TestBuildKeyRows_LivePathMapWinsOverAuthored(t *testing.T) {
	catalog := []string{"movies_library_root_folder"}
	authored := []AuthoredMapping{{Key: "movies_library_root_folder", NodePath: "/mnt/new-repick"}}
	pathMap := []RemapEntry{{Server: "/srv/movies", Local: "/mnt/live-in-effect", Key: "movies_library_root_folder"}}
	got := BuildKeyRows(catalog, authored, pathMap)
	want := []KeyRow{{Key: "movies_library_root_folder", NodePath: "/mnt/live-in-effect", Mapped: true, HasAuthoredKey: true}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("BuildKeyRows (live wins) = %+v, want %+v", got, want)
	}
}

// TestBuildKeyRows_UnkeyedPathMapIgnored proves a Remap entry with no Key (a
// pre-Key-field entry, or one whose Local is blank) does not spuriously mark a
// key mapped — matching by Key requires a real Key AND a non-blank Local.
func TestBuildKeyRows_UnkeyedPathMapIgnored(t *testing.T) {
	catalog := []string{"movies_library_root_folder"}
	pathMap := []RemapEntry{
		{Server: "/srv/movies", Local: "/mnt/movies", Key: ""},               // no key → cannot correlate
		{Server: "/srv/other", Local: "", Key: "movies_library_root_folder"}, // blank local → treated unset
	}
	got := BuildKeyRows(catalog, nil, pathMap)
	want := []KeyRow{{Key: "movies_library_root_folder", NodePath: "", Mapped: false}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("BuildKeyRows (unkeyed/blank ignored) = %+v, want %+v", got, want)
	}
}

func TestSetItemTitle(t *testing.T) {
	if got := SetItemTitle(true); got != "Change folder…" {
		t.Errorf("mapped = %q", got)
	}
	if got := SetItemTitle(false); got != "Set folder…" {
		t.Errorf("unset = %q", got)
	}
}

// TestRemoveItemVisible_LegacyVsAuthored proves the follow-up rule: a
// legacy-only row (Mapped via a live PathMap correlation, no AuthoredPaths
// record → HasAuthoredKey=false) hides its Remove control, while a node-authored
// row (HasAuthoredKey=true, whether or not PathMap has echoed back yet) keeps
// Remove available. An unmapped row never shows Remove.
func TestRemoveItemVisible_LegacyVsAuthored(t *testing.T) {
	cases := []struct {
		name string
		row  KeyRow
		want bool
	}{
		{"legacy-only mapping hides Remove", KeyRow{Key: "movies_library_root_folder", NodePath: "/mnt/movies", Mapped: true, HasAuthoredKey: false}, false},
		{"node-authored mapping shows Remove", KeyRow{Key: "movies_library_root_folder", NodePath: "/mnt/movies", Mapped: true, HasAuthoredKey: true}, true},
		{"unmapped row hides Remove", KeyRow{Key: "series_library_root_folder", Mapped: false}, false},
		{"unmapped-but-flagged is still hidden (needs Mapped too)", KeyRow{Key: "series_library_root_folder", Mapped: false, HasAuthoredKey: true}, false},
	}
	for _, tc := range cases {
		if got := RemoveItemVisible(tc.row); got != tc.want {
			t.Errorf("%s: RemoveItemVisible = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// TestBuildKeyRows_HasAuthoredKeyFlag proves BuildKeyRows sets HasAuthoredKey to
// discriminate a legacy-only mapping (PathMap-correlated, no AuthoredPaths) from
// a node-authored one, driving RemoveItemVisible above end-to-end.
func TestBuildKeyRows_HasAuthoredKeyFlag(t *testing.T) {
	catalog := []string{"movies_library_root_folder", "series_library_root_folder"}
	authored := []AuthoredMapping{{Key: "series_library_root_folder", NodePath: "/mnt/series"}}
	pathMap := []RemapEntry{
		{Server: "/srv/movies", Local: "/mnt/movies", Key: "movies_library_root_folder"}, // legacy: PathMap only
		{Server: "/srv/series", Local: "/mnt/series", Key: "series_library_root_folder"}, // node-authored: also in authored
	}
	got := BuildKeyRows(catalog, authored, pathMap)
	want := []KeyRow{
		{Key: "movies_library_root_folder", NodePath: "/mnt/movies", Mapped: true, HasAuthoredKey: false},
		{Key: "series_library_root_folder", NodePath: "/mnt/series", Mapped: true, HasAuthoredKey: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("BuildKeyRows = %+v, want %+v", got, want)
	}
	if RemoveItemVisible(got[0]) {
		t.Error("legacy-only movies row must hide Remove")
	}
	if !RemoveItemVisible(got[1]) {
		t.Error("node-authored series row must keep Remove")
	}
}

func TestPathMappingGateOpen(t *testing.T) {
	if PathMappingGateOpen(0) {
		t.Error("gate should be CLOSED with zero media roots")
	}
	if !PathMappingGateOpen(1) {
		t.Error("gate should be OPEN with one media root")
	}
	if !PathMappingGateOpen(3) {
		t.Error("gate should be OPEN with three media roots")
	}
}

func TestPathPushWarningLine(t *testing.T) {
	// Empty error → hidden (the daemon clears lastPushError on a successful echo,
	// so the line disappears on its own).
	if text, show := PathPushWarningLine(""); show || text != "" {
		t.Errorf("empty error: got (%q, %v), want (\"\", false)", text, show)
	}
	text, show := PathPushWarningLine(`push for "movies_library_root_folder" failed: status 422`)
	if !show {
		t.Fatal("non-empty error should show the warning line")
	}
	if want := `⚠ Path mapping: last push failed — push for "movies_library_root_folder" failed: status 422 (re-pick a folder to retry)`; text != want {
		t.Errorf("warning text = %q, want %q", text, want)
	}
}

// --- dispatch-pause pure logic -------------------------------------------

func TestDispatchDisplayTitle(t *testing.T) {
	if got := DispatchDisplayTitle(true); got != "Dispatch: Paused" {
		t.Errorf("paused = %q", got)
	}
	if got := DispatchDisplayTitle(false); got != "Dispatch: Running" {
		t.Errorf("running = %q", got)
	}
}

func TestDispatchActionTitle(t *testing.T) {
	// The action label describes what a click DOES: paused → resume, running → pause.
	if got := DispatchActionTitle(true); got != "Resume dispatch" {
		t.Errorf("paused action = %q, want \"Resume dispatch\"", got)
	}
	if got := DispatchActionTitle(false); got != "Pause dispatch" {
		t.Errorf("running action = %q, want \"Pause dispatch\"", got)
	}
}
