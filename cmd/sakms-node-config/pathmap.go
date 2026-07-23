//go:build cgo

package main

import (
	fyne "fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"github.com/labbersanon/sakms/internal/nodecontrol"
)

// pathmapPanel is the path-mapping configuration surface: one row per catalog
// key (from nodecontrol.BuildKeyRows) showing the raw node path or "not set", a
// "Set/Change folder…" button gated by nodecontrol.PathMappingGateOpen, a
// "Remove mapping" button shown only when nodecontrol.RemoveItemVisible is true,
// the "Add a media root first" gate message, and the last-push-failure banner.
// Fyne labels have no DBusMenu mnemonic bug, so paths render raw (plan U1).
type pathmapPanel struct {
	client *nodecontrol.Client
	win    fyne.Window

	content  *fyne.Container
	list     *fyne.Container
	banner   *widget.Label // last-push-failure line
	gateMsg  *widget.Label // "Add a media root first"
	errLabel *widget.Label

	mediaRootCount int
	view           nodecontrol.PathMapView
	rows           []*keyRow
}

// keyRow keeps handles on one rendered row's widgets for tests.
type keyRow struct {
	key       string
	label     *widget.Label
	setBtn    *widget.Button
	removeBtn *widget.Button
}

func newPathmapPanel(client *nodecontrol.Client, win fyne.Window) *pathmapPanel {
	p := &pathmapPanel{
		client:   client,
		win:      win,
		list:     container.NewVBox(),
		banner:   widget.NewLabel(""),
		gateMsg:  widget.NewLabel(""),
		errLabel: widget.NewLabel(""),
	}
	p.banner.Wrapping = fyne.TextWrapWord
	p.banner.Hide()
	p.gateMsg.Hide()
	p.errLabel.Hide()

	p.content = container.NewVBox(
		p.errLabel,
		p.banner,
		p.gateMsg,
		widget.NewSeparator(),
		p.list,
	)
	return p
}

// setCount updates the mediaRoots count (from the roots panel) and re-renders so
// the gate opens/closes without needing a fresh GET /pathmap.
func (p *pathmapPanel) setCount(n int) {
	p.mediaRootCount = n
	p.render()
}

// refresh reloads the full path-mapping view (catalog + authored + live pathmap
// + last-push-error) over the control socket and re-renders.
func (p *pathmapPanel) refresh() {
	ctx, cancel := ctxTimeout()
	defer cancel()
	view, err := p.client.GetPathMap(ctx)
	if err != nil {
		setErr(p.errLabel, "load path mappings", err)
		return
	}
	clearErr(p.errLabel)
	p.view = view
	p.render()
}

// render rebuilds the key rows, the gate message, and the push-failure banner
// from the current view and mediaRootCount. It reuses BuildKeyRows /
// PathMappingGateOpen / RemoveItemVisible / SetItemTitle / PathPushWarningLine
// verbatim — only the widget layer is new (plan U1/U2).
func (p *pathmapPanel) render() {
	gateOpen := nodecontrol.PathMappingGateOpen(p.mediaRootCount)
	if gateOpen {
		p.gateMsg.Hide()
	} else {
		p.gateMsg.SetText("Add a media root first — a path mapping cannot be set until this node has at least one media root.")
		p.gateMsg.Show()
	}

	if text, show := nodecontrol.PathPushWarningLine(p.view.LastPushError); show {
		p.banner.SetText(text)
		p.banner.Show()
	} else {
		p.banner.Hide()
	}

	p.list.Objects = nil
	p.rows = nil
	rows := nodecontrol.BuildKeyRows(p.view.LibraryPathKeys, p.view.AuthoredPaths, p.view.PathMap)
	for _, r := range rows {
		r := r
		title := nodecontrol.KeyDisplayLabel(r.Key) + "  →  "
		if r.Mapped {
			title += r.NodePath
		} else {
			title += "not set"
		}
		lbl := widget.NewLabel(title)

		setBtn := widget.NewButton(nodecontrol.SetItemTitle(r.Mapped), func() { p.setClicked(r.Key) })
		if !gateOpen {
			setBtn.Disable()
		}

		removeBtn := widget.NewButton("Remove mapping", func() { p.doClear(r.Key) })
		if !nodecontrol.RemoveItemVisible(r) {
			removeBtn.Hide()
		}

		actions := container.NewHBox(setBtn, removeBtn)
		p.list.Add(container.NewBorder(nil, nil, nil, actions, lbl))
		p.rows = append(p.rows, &keyRow{key: r.Key, label: lbl, setBtn: setBtn, removeBtn: removeBtn})
	}
	p.list.Refresh()
}

// setClicked pops Fyne's native folder picker for a key and sets the mapping.
func (p *pathmapPanel) setClicked(key string) {
	dialog.ShowFolderOpen(func(uri fyne.ListableURI, err error) {
		if err != nil {
			setErr(p.errLabel, "set path mapping", err)
			return
		}
		if uri == nil {
			return // cancelled — no-op
		}
		p.doSet(key, uri.Path())
	}, p.win)
}

// doSet issues POST /pathmap/set and refreshes on success.
func (p *pathmapPanel) doSet(key, path string) {
	ctx, cancel := ctxTimeout()
	defer cancel()
	if _, err := p.client.SetPathMap(ctx, key, path); err != nil {
		setErr(p.errLabel, "set path mapping", err)
		return
	}
	clearErr(p.errLabel)
	p.refresh()
}

// doClear issues POST /pathmap/clear (D7) and refreshes on success.
func (p *pathmapPanel) doClear(key string) {
	ctx, cancel := ctxTimeout()
	defer cancel()
	if _, err := p.client.ClearPathMap(ctx, key); err != nil {
		setErr(p.errLabel, "remove path mapping", err)
		return
	}
	clearErr(p.errLabel)
	p.refresh()
}
