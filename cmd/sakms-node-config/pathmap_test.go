//go:build cgo

package main

import (
	"testing"

	"fyne.io/fyne/v2/test"

	"github.com/labbersanon/sakms/internal/nodecontrol"
)

// TestPathMapGateClosedDisablesSet proves that with zero media roots the
// path-mapping gate is closed: the "Set folder…" button is disabled and the
// "Add a media root first" message is shown (plan U2 / PathMappingGateOpen).
func TestPathMapGateClosedDisablesSet(t *testing.T) {
	a := test.NewTempApp(t)
	win := a.NewWindow("t")

	p := newPathmapPanel(nil, win) // nil client: this is a pure render assertion
	p.mediaRootCount = 0           // gate closed
	p.view = nodecontrol.PathMapView{LibraryPathKeys: []string{"movies_library_root_folder"}}
	p.render()

	if !p.gateMsg.Visible() {
		t.Error("gate-closed: expected the 'Add a media root first' message to be visible")
	}
	if len(p.rows) != 1 {
		t.Fatalf("expected 1 key row, got %d", len(p.rows))
	}
	if !p.rows[0].setBtn.Disabled() {
		t.Error("gate-closed: expected the Set folder button to be disabled")
	}
}

// TestPathMapLegacyRowHidesRemove proves that a legacy-only mapping (present in
// the live PathMap but with no AuthoredPaths record) hides its Remove button,
// per nodecontrol.RemoveItemVisible / D7. The gate is open (one media root) so
// the row's Set button stays enabled.
func TestPathMapLegacyRowHidesRemove(t *testing.T) {
	a := test.NewTempApp(t)
	win := a.NewWindow("t")

	p := newPathmapPanel(nil, win)
	p.mediaRootCount = 1 // gate open
	p.view = nodecontrol.PathMapView{
		LibraryPathKeys: []string{"movies_library_root_folder"},
		// Legacy: a live server-authored Remap with NO matching AuthoredPaths.
		PathMap: []nodecontrol.RemapEntry{
			{Key: "movies_library_root_folder", Server: "/srv/movies", Local: "/mnt/movies"},
		},
	}
	p.render()

	if len(p.rows) != 1 {
		t.Fatalf("expected 1 key row, got %d", len(p.rows))
	}
	if p.rows[0].removeBtn.Visible() {
		t.Error("legacy-only row: expected the Remove mapping button to be hidden")
	}
	if p.rows[0].setBtn.Disabled() {
		t.Error("gate-open: expected the Set/Change button to be enabled")
	}
}
