//go:build cgo

// Command sakms-node-config is the on-demand Fyne configuration window for
// sakms-node. It is the windowed replacement for the tray's three interactive
// DBusMenu sections (mediaRoots removal, path-mapping set/change/remove, and
// dispatch-pause toggle) that are unreachable on KDE Plasma — see
// .omc/plans/node-tray-windowed-config-ui.md.
//
// It reuses the shared, already-validated decision logic and control-socket
// client in internal/nodecontrol verbatim; this binary is only a new *renderer*
// over that logic, never a redesign of it. CGO+OpenGL (Fyne's GLFW driver) is
// quarantined to this one binary via the //go:build cgo constraint on every
// file, so the CGO_ENABLED=0 `go build ./...` / `go test ./...` wildcards skip
// it and the daemon/tray/server stay CGO-free.
//
// It accepts the same -status-port / -control-socket flags as the tray so both
// point at the same daemon: writes go over the group-gated unix control socket
// (the sole write-authorization boundary), and the mediaRoots scope labels +
// containment-drift signal are read from the daemon's loopback status server.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"time"

	fyne "fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"github.com/labbersanon/sakms/internal/nodecontrol"
)

const (
	defaultStatusPort    = 7810
	defaultControlSocket = "/run/sakms-node/control.sock"
	// httpTimeout bounds the loopback status fetch; controlTimeout bounds each
	// unix control-socket call (mirroring the tray's values).
	httpTimeout    = 2 * time.Second
	controlTimeout = 5 * time.Second
)

func main() {
	statusPort := flag.Int("status-port", defaultStatusPort,
		"port of the sakms-node status server")
	controlSocket := flag.String("control-socket", defaultControlSocket,
		"path to the sakms-node local control socket")
	flag.Parse()

	client := nodecontrol.NewClient(*controlSocket)
	statusURL := fmt.Sprintf("http://127.0.0.1:%d/status", *statusPort)

	a := app.New()
	w := a.NewWindow("sakms-node configuration")

	pathmap := newPathmapPanel(client, w)
	dispatch := newDispatchPanel(client, w)
	roots := newRootsPanel(client, w, fetchScopesFunc(statusURL))
	// The mediaRoots count drives the path-mapping gate; a root added/removed in
	// the roots panel has to reopen/close the gate in the path-mapping panel.
	roots.onCountChange = pathmap.setCount

	tabs := container.NewAppTabs(
		container.NewTabItem("Media Roots", roots.content),
		container.NewTabItem("Path Mappings", pathmap.content),
		container.NewTabItem("Dispatch", dispatch.content),
	)

	// Fetch all three views on window open; each panel refetches after its own
	// mutations so the UI reflects server-confirmed state, not optimistic state.
	roots.refresh()
	pathmap.refresh()
	dispatch.refresh()

	w.SetContent(tabs)
	w.Resize(fyne.Size{Width: 640, Height: 480})
	w.ShowAndRun()
}

// statusResponse is the minimal decode of the daemon's GET /status reply this
// window needs: only the per-root containment scopes (for scope labels + the
// containment-drift signal + the path-mapping gate count). The scope decision
// logic itself lives in internal/nodecontrol; this only carries the wire type.
type statusResponse struct {
	MediaRootScopes []nodecontrol.MediaRootStatus `json:"mediaRootScopes,omitempty"`
}

// fetchScopesFunc returns a closure the roots panel calls to load the current
// per-root containment scopes from the daemon's loopback status server. Scope
// labels and containment drift exist only in GET /status (the control socket's
// GetRoots returns bare paths), so this is a status-server read, not a control
// call.
func fetchScopesFunc(statusURL string) func() ([]nodecontrol.MediaRootStatus, error) {
	return func() ([]nodecontrol.MediaRootStatus, error) {
		httpClient := &http.Client{Timeout: httpTimeout}
		resp, err := httpClient.Get(statusURL)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, err
		}
		var s statusResponse
		if err := json.Unmarshal(body, &s); err != nil {
			return nil, err
		}
		return s.MediaRootScopes, nil
	}
}

// ctxTimeout returns a control-socket-bounded context and its cancel.
func ctxTimeout() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), controlTimeout)
}

// setErr renders a failed control-socket call in-window using the reused
// nodecontrol diagnostics (EACCES → relogin, ENOENT → daemon-down, else raw),
// so the operator sees the same actionable copy the tray surfaces via notify().
func setErr(l *widget.Label, action string, err error) {
	_, body := nodecontrol.ControlErrorMessage(action, err)
	l.SetText(body)
	l.Show()
}

// clearErr hides the in-window error line after a successful call.
func clearErr(l *widget.Label) {
	l.SetText("")
	l.Hide()
}
