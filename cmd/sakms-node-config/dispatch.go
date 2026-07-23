//go:build cgo

package main

import (
	fyne "fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"github.com/labbersanon/sakms/internal/nodecontrol"
)

// dispatchPanel is the dispatch-pause configuration surface: a status line
// (nodecontrol.DispatchDisplayTitle) plus a toggle labeled by what a click does
// (nodecontrol.DispatchActionTitle). The toggle preserves the P-series failed-
// push discipline verbatim (plan U3): the widget flips optimistically, and on a
// push failure we roll back to the authoritative value the failed
// SetDispatchPause RETURNS (the daemon's rolled-back bit), not a hardcoded
// negation. The daemon/server still own the bit; this is only a renderer.
type dispatchPanel struct {
	client *nodecontrol.Client
	win    fyne.Window

	content     *fyne.Container
	statusLabel *widget.Label
	check       *widget.Check
	errLabel    *widget.Label

	paused  bool
	fetched bool
	// suppress guards programmatic check.SetChecked from re-entering the toggle
	// handler: SetChecked fires OnChanged, so a rollback SetChecked would otherwise
	// re-issue a toggle (the re-entrancy trap).
	suppress bool
}

func newDispatchPanel(client *nodecontrol.Client, win fyne.Window) *dispatchPanel {
	p := &dispatchPanel{
		client:      client,
		win:         win,
		statusLabel: widget.NewLabel(""),
		errLabel:    widget.NewLabel(""),
	}
	p.errLabel.Hide()
	p.check = widget.NewCheck("", func(checked bool) {
		if p.suppress {
			return
		}
		p.toggle(checked)
	})

	p.content = container.NewVBox(
		p.errLabel,
		p.statusLabel,
		p.check,
	)
	return p
}

// refresh reloads the authoritative dispatch-pause state over the control socket.
func (p *dispatchPanel) refresh() {
	ctx, cancel := ctxTimeout()
	defer cancel()
	view, err := p.client.GetDispatchPause(ctx)
	if err != nil {
		setErr(p.errLabel, "load dispatch pause state", err)
		return
	}
	clearErr(p.errLabel)
	p.fetched = true
	p.paused = view.Paused
	p.updateWidgets()
}

// updateWidgets reflects p.paused onto the status line, the toggle label, and the
// toggle's checked state. The SetChecked is wrapped in the suppress guard so it
// never re-enters the toggle handler.
func (p *dispatchPanel) updateWidgets() {
	p.statusLabel.SetText(nodecontrol.DispatchDisplayTitle(p.paused))
	p.check.SetText(nodecontrol.DispatchActionTitle(p.paused))
	p.suppress = true
	p.check.SetChecked(p.paused)
	p.suppress = false
	p.check.Refresh()
}

// toggle relays a desired dispatch-pause value to the daemon. desired is the
// widget's new (optimistic) checked state. On success it adopts the echoed
// authoritative value; on failure it rolls the UI back to the value the failed
// call returned (the daemon's rolled-back authoritative bit) and surfaces the
// error in-window — preserving the P-series safety behavior exactly.
func (p *dispatchPanel) toggle(desired bool) {
	if !p.fetched {
		return // no authoritative state yet — nothing to toggle from
	}
	ctx, cancel := ctxTimeout()
	defer cancel()
	view, err := p.client.SetDispatchPause(ctx, desired)
	if err != nil {
		setErr(p.errLabel, "change dispatch pause", err)
		p.paused = view.Paused // roll back to the daemon's authoritative value
		p.updateWidgets()
		return
	}
	clearErr(p.errLabel)
	p.paused = view.Paused
	p.updateWidgets()
}
