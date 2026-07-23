// Package nodecontrol is the shared, CGO-free control-socket client and pure
// display/decision logic for sakms-node's desktop configuration surfaces. It
// talks to the daemon's local unix-domain control socket (the same transport
// the tray has always used) and holds the mediaRoots / path-mapping /
// dispatch-pause decision helpers so both the tray and the (Stage 1) Fyne
// configuration window render the identical, already-validated behavior.
//
// This package has no build tag and imports nothing CGO — it stays buildable
// and testable under CGO_ENABLED=0 so its pure-logic tests run in the
// `go build ./...` / `go test ./...` wildcards.
package nodecontrol

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"
)

// controlTimeout bounds every control-socket request the client issues. It
// matches the tray's original control-socket timeout value (behavior-identical
// to the pre-extraction newControlClient).
const controlTimeout = 5 * time.Second

// Client talks to the daemon's local control endpoint over a unix-domain
// socket. It is an ordinary *http.Client whose transport dials the socket path
// regardless of the request URL's host (the host is a placeholder).
type Client struct {
	socketPath string
	http       *http.Client
}

// NewClient builds a control-socket client dialing socketPath.
func NewClient(socketPath string) *Client {
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", socketPath)
		},
	}
	return &Client{
		socketPath: socketPath,
		http:       &http.Client{Timeout: controlTimeout, Transport: transport},
	}
}

// controlResponse is the daemon's reply shape for the mediaRoots endpoints: a
// 200 carries the resulting mediaRoots list; a 400 carries an error string.
type controlResponse struct {
	MediaRoots []string `json:"mediaRoots"`
	Error      string   `json:"error"`
}

// do issues one control request and returns the resulting mediaRoots list. The
// URL host ("sakms-node") is irrelevant — DialContext always targets the unix
// socket. Dial failures are returned unwrapped-enough for ClassifyDialError to
// inspect via errors.Is (http.Client wraps them in *url.Error, which unwraps).
func (c *Client) do(ctx context.Context, method, path string, body any) ([]string, error) {
	var rdr io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rdr = bytes.NewReader(buf)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://sakms-node"+path, rdr)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var out controlResponse
	if decErr := json.NewDecoder(resp.Body).Decode(&out); decErr != nil && decErr != io.EOF {
		return nil, fmt.Errorf("decoding control response: %w", decErr)
	}
	if resp.StatusCode != http.StatusOK {
		if out.Error != "" {
			return nil, errors.New(out.Error)
		}
		return nil, fmt.Errorf("control socket returned %s", resp.Status)
	}
	return out.MediaRoots, nil
}

// GetRoots returns the node's current mediaRoots allowlist.
func (c *Client) GetRoots(ctx context.Context) ([]string, error) {
	return c.do(ctx, http.MethodGet, "/mediaroots", nil)
}

// AddRoot adds path to the node's mediaRoots allowlist and returns the result.
func (c *Client) AddRoot(ctx context.Context, path string) ([]string, error) {
	return c.do(ctx, http.MethodPost, "/mediaroots/add", map[string]string{"path": path})
}

// RemoveRoot removes path from the node's mediaRoots allowlist.
func (c *Client) RemoveRoot(ctx context.Context, path string) ([]string, error) {
	return c.do(ctx, http.MethodPost, "/mediaroots/remove", map[string]string{"path": path})
}

// RemapEntry mirrors the daemon's server→local Remap pair from GET /pathmap.
// Key is the library-path key the server derived this pair from (inert display
// metadata carried through the wire — see nodes.PathMapping/PathMapEntry). It
// lets BuildKeyRows show a live, authoritative Remap row for a key even when the
// node has no matching AuthoredPaths record — notably a legacy mapping set via
// the old server-side operator UI before node-side authoring existed.
type RemapEntry struct {
	Server string `json:"server"`
	Local  string `json:"local"`
	Key    string `json:"key,omitempty"`
}

// AuthoredMapping mirrors the daemon's AuthoredPathMapping: one library-path-key
// → node-local path the operator authored on this node.
type AuthoredMapping struct {
	Key      string `json:"key"`
	NodePath string `json:"nodePath"`
}

// PathMapView decodes the daemon's pathmapState (GET /pathmap and the set/clear
// echoes). Error carries the daemon's 400 body ("error"); set/clear echoes omit
// LibraryPathKeys (the caller reads the catalog from GET /pathmap).
type PathMapView struct {
	AuthoredPaths   []AuthoredMapping `json:"authoredPaths"`
	PathMap         []RemapEntry      `json:"pathMap"`
	LibraryPathKeys []string          `json:"libraryPathKeys"`
	LastPushError   string            `json:"lastPushError"`
	Error           string            `json:"error"`
}

// doPathMap issues one /pathmap control-socket request and decodes the
// pathmapState reply. It mirrors Client.do but for the path-mapping response
// shape; dial failures propagate unwrapped so ClassifyDialError can bucket them
// (EACCES relogin / ENOENT daemon-down).
func (c *Client) doPathMap(ctx context.Context, method, path string, body any) (PathMapView, error) {
	var rdr io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return PathMapView{}, err
		}
		rdr = bytes.NewReader(buf)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://sakms-node"+path, rdr)
	if err != nil {
		return PathMapView{}, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return PathMapView{}, err
	}
	defer resp.Body.Close()

	var out PathMapView
	if decErr := json.NewDecoder(resp.Body).Decode(&out); decErr != nil && decErr != io.EOF {
		return PathMapView{}, fmt.Errorf("decoding pathmap response: %w", decErr)
	}
	if resp.StatusCode != http.StatusOK {
		if out.Error != "" {
			return PathMapView{}, errors.New(out.Error)
		}
		return PathMapView{}, fmt.Errorf("control socket returned %s", resp.Status)
	}
	return out, nil
}

// GetPathMap returns the node's current path-mapping view.
func (c *Client) GetPathMap(ctx context.Context) (PathMapView, error) {
	return c.doPathMap(ctx, http.MethodGet, "/pathmap", nil)
}

// SetPathMap authors a key→localPath mapping on the node.
func (c *Client) SetPathMap(ctx context.Context, key, localPath string) (PathMapView, error) {
	return c.doPathMap(ctx, http.MethodPost, "/pathmap/set", map[string]string{"key": key, "localPath": localPath})
}

// ClearPathMap clears the node-authored mapping for key (D7).
func (c *Client) ClearPathMap(ctx context.Context, key string) (PathMapView, error) {
	return c.doPathMap(ctx, http.MethodPost, "/pathmap/clear", map[string]string{"key": key})
}

// DispatchPauseView decodes the daemon's /dispatch/pause reply. Error carries
// the daemon's non-2xx body (e.g. a failed relay that rolled back), in which
// case Paused reports the rolled-back authoritative value.
type DispatchPauseView struct {
	Paused bool   `json:"paused"`
	Error  string `json:"error"`
}

// doDispatchPause issues one /dispatch/pause control-socket request and decodes
// the reply. Mirrors Client.do; dial failures propagate unwrapped so
// ClassifyDialError can bucket them (EACCES relogin / ENOENT daemon-down).
func (c *Client) doDispatchPause(ctx context.Context, method string, body any) (DispatchPauseView, error) {
	var rdr io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return DispatchPauseView{}, err
		}
		rdr = bytes.NewReader(buf)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://sakms-node/dispatch/pause", rdr)
	if err != nil {
		return DispatchPauseView{}, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return DispatchPauseView{}, err
	}
	defer resp.Body.Close()

	var out DispatchPauseView
	if decErr := json.NewDecoder(resp.Body).Decode(&out); decErr != nil && decErr != io.EOF {
		return DispatchPauseView{}, fmt.Errorf("decoding dispatch pause response: %w", decErr)
	}
	if resp.StatusCode != http.StatusOK {
		if out.Error != "" {
			return out, errors.New(out.Error)
		}
		return out, fmt.Errorf("control socket returned %s", resp.Status)
	}
	return out, nil
}

// GetDispatchPause returns the node's current dispatch-pause state.
func (c *Client) GetDispatchPause(ctx context.Context) (DispatchPauseView, error) {
	return c.doDispatchPause(ctx, http.MethodGet, nil)
}

// SetDispatchPause relays a new dispatch-pause value to the daemon.
func (c *Client) SetDispatchPause(ctx context.Context, paused bool) (DispatchPauseView, error) {
	return c.doDispatchPause(ctx, http.MethodPost, DispatchPauseView{Paused: paused})
}
