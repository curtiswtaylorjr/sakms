//go:build cgo

package main

import (
	fyne "fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"github.com/labbersanon/sakms/internal/nodecontrol"
)

// rootsPanel is the mediaRoots configuration surface: it lists each configured
// root with its OS-level containment scope label, offers an "Add media root…"
// picker, a per-row "Remove" button (with a last-root confirmation mirroring the
// tray's handleRemoveRoot), and the containment-drift banner. Reads scopes from
// the daemon status server (fetchScopes); writes over the control socket.
type rootsPanel struct {
	client      *nodecontrol.Client
	win         fyne.Window
	fetchScopes func() ([]nodecontrol.MediaRootStatus, error)

	content  *fyne.Container
	list     *fyne.Container
	banner   *widget.Label
	errLabel *widget.Label

	// onCountChange fires after every successful refresh so the path-mapping
	// panel's gate can react to the current mediaRoots count.
	onCountChange func(int)

	scopes []nodecontrol.MediaRootStatus
	rows   []*rootRow
}

// rootRow keeps a handle on one rendered row's Remove button for tests.
type rootRow struct {
	path      string
	removeBtn *widget.Button
}

func newRootsPanel(client *nodecontrol.Client, win fyne.Window, fetchScopes func() ([]nodecontrol.MediaRootStatus, error)) *rootsPanel {
	p := &rootsPanel{
		client:      client,
		win:         win,
		fetchScopes: fetchScopes,
		list:        container.NewVBox(),
		banner:      widget.NewLabel(""),
		errLabel:    widget.NewLabel(""),
	}
	p.banner.Wrapping = fyne.TextWrapWord
	p.banner.Hide()
	p.errLabel.Hide()

	addBtn := widget.NewButton("Add media root…", p.addClicked)
	p.content = container.NewVBox(
		p.errLabel,
		p.banner,
		addBtn,
		widget.NewSeparator(),
		p.list,
	)
	return p
}

// refresh reloads the scopes from the status server and re-renders. It also
// reports the current count so the path-mapping gate stays in sync.
func (p *rootsPanel) refresh() {
	scopes, err := p.fetchScopes()
	if err != nil {
		setErr(p.errLabel, "load media roots", err)
		return
	}
	clearErr(p.errLabel)
	p.scopes = scopes
	p.render()
	if p.onCountChange != nil {
		p.onCountChange(len(scopes))
	}
}

// render rebuilds the root rows and the containment-drift banner from p.scopes.
func (p *rootsPanel) render() {
	p.list.Objects = nil
	p.rows = nil
	for _, s := range p.scopes {
		s := s
		rm := widget.NewButton("Remove", func() { p.removeClicked(s.Path) })
		lbl := widget.NewLabel("• " + s.Path + "  [" + nodecontrol.ScopeLabel(s.Scope) + "]")
		p.list.Add(container.NewBorder(nil, nil, nil, rm, lbl))
		p.rows = append(p.rows, &rootRow{path: s.Path, removeBtn: rm})
	}

	if nodecontrol.ContainmentDrift(p.scopes) {
		p.banner.SetText("⚠ OS-level containment out of sync — a root operator must re-run apply-mediaroots.sh and restart the daemon")
		p.banner.Show()
	} else {
		p.banner.Hide()
	}
	p.list.Refresh()
}

// addClicked pops Fyne's native folder picker and adds the chosen directory.
func (p *rootsPanel) addClicked() {
	dialog.ShowFolderOpen(func(uri fyne.ListableURI, err error) {
		if err != nil {
			setErr(p.errLabel, "add media root", err)
			return
		}
		if uri == nil {
			return // cancelled — no-op
		}
		p.doAddRoot(uri.Path())
	}, p.win)
}

// doAddRoot issues POST /mediaroots/add and refreshes on success.
func (p *rootsPanel) doAddRoot(path string) {
	ctx, cancel := ctxTimeout()
	defer cancel()
	if _, err := p.client.AddRoot(ctx, path); err != nil {
		setErr(p.errLabel, "add media root", err)
		return
	}
	clearErr(p.errLabel)
	p.refresh()
}

// removeClicked confirms before removing the LAST media root (which re-enters
// the unrestricted grace period), mirroring the tray's handleRemoveRoot; any
// other root removes directly. The daemon accepts an empty set by design — the
// confirmation is UX friendliness, not an enforcement gate.
func (p *rootsPanel) removeClicked(path string) {
	if len(p.scopes) <= 1 {
		dialog.ShowConfirm("Remove the last media root?",
			"Removing the last media root returns this node to the unrestricted grace period (no media-root allowlist). Continue?",
			func(ok bool) {
				if ok {
					p.doRemoveRoot(path)
				}
			}, p.win)
		return
	}
	p.doRemoveRoot(path)
}

// doRemoveRoot issues POST /mediaroots/remove and refreshes on success.
func (p *rootsPanel) doRemoveRoot(path string) {
	ctx, cancel := ctxTimeout()
	defer cancel()
	if _, err := p.client.RemoveRoot(ctx, path); err != nil {
		setErr(p.errLabel, "remove media root", err)
		return
	}
	clearErr(p.errLabel)
	p.refresh()
}
