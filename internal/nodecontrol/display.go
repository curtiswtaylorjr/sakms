package nodecontrol

import "strings"

// MediaRootStatus mirrors the daemon's per-root containment record from
// GET /status. Scope is one of the daemon's mediaRootScope string values.
type MediaRootStatus struct {
	Path  string `json:"path"`
	Scope string `json:"scope"`
}

// ContainmentDrift reports whether the app-level allowlist has diverged from the
// last-applied OS-level (Phase 2) sandbox: true only when containment is active
// on this node (some root is namespace_scoped / namespace_scoped_but_unbound)
// AND at least one root is app_level_only (added/changed but not yet re-applied).
// It is deliberately false on a node where containment was never applied (no root
// is namespace_scoped*), so the drift hint never false-alarms the common
// app-level-only case.
func ContainmentDrift(scopes []MediaRootStatus) bool {
	active, appOnly := false, false
	for _, s := range scopes {
		switch s.Scope {
		case "namespace_scoped", "namespace_scoped_but_unbound":
			active = true
		case "app_level_only":
			appOnly = true
		}
	}
	return active && appOnly
}

// ScopeLabel renders a mediaRootScope value as a short human-readable tag.
func ScopeLabel(scope string) string {
	switch scope {
	case "namespace_scoped":
		return "OS-contained"
	case "namespace_scoped_but_unbound":
		return "OS-contained, mount missing"
	case "app_level_only":
		return "app-level only"
	case "":
		return "unknown"
	default:
		return scope
	}
}

// KeyRow is one library-path-key's render state: the key, its effective node
// path (empty when unset), and whether a mapping exists.
//
// HasAuthoredKey distinguishes a node-authored mapping (this node holds an
// AuthoredPaths record for the key — Remove is safe, the D7 clear path can
// correlate and delete the Remap entry) from a legacy-only mapping (Mapped
// solely via a live PathMap correlation, no AuthoredPaths record — a mapping
// set through the OLD server-side operator UI). Remove on a legacy-only row is a
// near-no-op on the node: clearAuthoredLocked finds no correlation and leaves
// cfg.PathMap intact, so the node keeps remapping while only the server's record
// is dropped. The surface therefore hides Remove for legacy-only rows;
// re-picking via "Change folder…" authors a real node-side key, after which
// Remove works.
type KeyRow struct {
	Key            string
	NodePath       string
	Mapped         bool
	HasAuthoredKey bool
}

// escapeMenuLabel neutralizes the DBusMenu/GTK mnemonic-accelerator convention
// so an underscore in a menu-item title renders literally instead of being
// swallowed (see the tray's own escapeMenuLabel for the full cited reasoning).
//
// This is a temporary duplicate of the tray-resident escapeMenuLabel, kept here
// only so KeyDisplayLabel's unknown-key fallback doubles underscores exactly as
// before the extraction (zero behavior change). It is removed in Stage 1 when
// the Fyne window renders raw paths and the fallback returns the raw key
// directly (plan U1) — the tray keeps its own copy until Stage 3.
func escapeMenuLabel(s string) string {
	return strings.ReplaceAll(s, "_", "__")
}

// keyLabels maps each known library-path key to a short human-friendly label.
// Unknown/future keys fall back to escapeMenuLabel(rawKey) via KeyDisplayLabel,
// so an unrecognized catalog addition never crashes or renders blank.
var keyLabels = map[string]string{
	"movies_library_root_folder": "Movies",
	"series_library_root_folder": "Series",
	"adult_library_root_folder":  "Adult",
	"movies_kids_root_path":      "Movies (Kids)",
	"series_kids_root_path":      "Series (Kids)",
}

// KeyDisplayLabel returns the human-friendly label for a known library-path key,
// or the mnemonic-escaped raw key for an unknown one (never blank, never a
// mangled underscore).
func KeyDisplayLabel(key string) string {
	if label, ok := keyLabels[key]; ok {
		return label
	}
	return escapeMenuLabel(key)
}

// BuildKeyRows pairs each catalog key with its effective node path, preserving
// catalog order. It consults BOTH sources, in priority order:
//
//   - The live PathMap (server-authoritative Remap table) by Key: this is what
//     is actually in effect for dispatch right now, so its Local wins whenever a
//     matching entry exists — including legacy mappings authored via the old
//     server-side operator UI, which the node never recorded in AuthoredPaths.
//   - AuthoredPaths (the node's own record) is the fallback for the pending
//     case: a just-authored key whose set the server has not yet echoed back
//     into PathMap still shows as mapped, using the authored value.
//
// A blank path from either source is treated as unset (blank means "skip"
// everywhere in the daemon, never "mapped").
func BuildKeyRows(catalog []string, authored []AuthoredMapping, pathMap []RemapEntry) []KeyRow {
	liveByKey := make(map[string]string, len(pathMap))
	for _, e := range pathMap {
		if e.Key != "" && e.Local != "" {
			liveByKey[e.Key] = e.Local
		}
	}
	authoredByKey := make(map[string]string, len(authored))
	for _, a := range authored {
		if a.NodePath != "" {
			authoredByKey[a.Key] = a.NodePath
		}
	}
	rows := make([]KeyRow, 0, len(catalog))
	for _, k := range catalog {
		_, hasAuthored := authoredByKey[k]
		if live, ok := liveByKey[k]; ok {
			rows = append(rows, KeyRow{Key: k, NodePath: live, Mapped: true, HasAuthoredKey: hasAuthored})
			continue
		}
		if p, ok := authoredByKey[k]; ok {
			rows = append(rows, KeyRow{Key: k, NodePath: p, Mapped: true, HasAuthoredKey: true})
			continue
		}
		rows = append(rows, KeyRow{Key: k, NodePath: "", Mapped: false})
	}
	return rows
}

// RemoveItemVisible reports whether a key row's "Remove mapping" control should
// be shown. Only a node-authored mapping (Mapped AND HasAuthoredKey) is
// removable: a legacy-only mapping (Mapped via a live PathMap correlation with
// no AuthoredPaths record) hides Remove because clearAuthoredLocked cannot
// correlate it — Remove would be a near-no-op on the node while dropping only
// the server's record. Re-picking via "Change folder…" authors a real node-side
// key, after which Remove works.
func RemoveItemVisible(kr KeyRow) bool {
	return kr.Mapped && kr.HasAuthoredKey
}

// SetItemTitle labels the picker control by whether a mapping already exists.
func SetItemTitle(mapped bool) string {
	if mapped {
		return "Change folder…"
	}
	return "Set folder…"
}

// PathMappingGateOpen reports whether path-mapping edits are allowed: the node
// must have at least one configured media root first (Stage 4 ruling, surfaced
// here as a UX gate — the daemon and server enforce the real safety boundary).
func PathMappingGateOpen(mediaRootCount int) bool {
	return mediaRootCount > 0
}

// PathPushWarningLine formats the persistent push-failure status line from the
// daemon's last-push-error state. It shows iff a failure is recorded; the daemon
// clears lastPushError to "" on the next successful echo (pathmap_push.go fire),
// so this line disappears on its own once a push succeeds. Stage 2 records-and-
// surfaces only (no auto-retry backoff), so the copy tells the operator to re-pick
// to force a retry rather than promising an automatic one.
func PathPushWarningLine(lastPushError string) (string, bool) {
	if lastPushError == "" {
		return "", false
	}
	return "⚠ Path mapping: last push failed — " + lastPushError + " (re-pick a folder to retry)", true
}

// DispatchDisplayTitle formats the display line for the current dispatch state.
func DispatchDisplayTitle(paused bool) string {
	if paused {
		return "Dispatch: Paused"
	}
	return "Dispatch: Running"
}

// DispatchActionTitle labels the toggle by what a click will DO: when paused,
// the action resumes; when running, the action pauses.
func DispatchActionTitle(paused bool) string {
	if paused {
		return "Resume dispatch"
	}
	return "Pause dispatch"
}
