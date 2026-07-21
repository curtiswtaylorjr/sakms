package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os/exec"
	"strings"
	"syscall"
)

// controlClient talks to the daemon's local mediaRoots control endpoint over a
// unix-domain socket. It is an ordinary *http.Client whose transport dials the
// socket path regardless of the request URL's host (the host is a placeholder).
type controlClient struct {
	socketPath string
	http       *http.Client
}

func newControlClient(socketPath string) *controlClient {
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", socketPath)
		},
	}
	return &controlClient{
		socketPath: socketPath,
		http:       &http.Client{Timeout: controlTimeout, Transport: transport},
	}
}

// controlResponse is the daemon's reply shape: a 200 carries the resulting
// mediaRoots list; a 400 carries an error string.
type controlResponse struct {
	MediaRoots []string `json:"mediaRoots"`
	Error      string   `json:"error"`
}

// do issues one control request and returns the resulting mediaRoots list. The
// URL host ("sakms-node") is irrelevant — DialContext always targets the unix
// socket. Dial failures are returned unwrapped-enough for classifyDialError to
// inspect via errors.Is (http.Client wraps them in *url.Error, which unwraps).
func (c *controlClient) do(ctx context.Context, method, path string, body any) ([]string, error) {
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

func (c *controlClient) getRoots(ctx context.Context) ([]string, error) {
	return c.do(ctx, http.MethodGet, "/mediaroots", nil)
}

func (c *controlClient) addRoot(ctx context.Context, path string) ([]string, error) {
	return c.do(ctx, http.MethodPost, "/mediaroots/add", map[string]string{"path": path})
}

func (c *controlClient) removeRoot(ctx context.Context, path string) ([]string, error) {
	return c.do(ctx, http.MethodPost, "/mediaroots/remove", map[string]string{"path": path})
}

// dialErrorKind buckets a control-socket connection failure for user messaging.
type dialErrorKind int

const (
	dialErrOther      dialErrorKind = iota // unexpected — show the raw error
	dialErrPermission                      // EACCES — not yet in the shared group (relogin needed)
	dialErrNotExist                        // ENOENT — socket absent (daemon not running yet)
)

// classifyDialError buckets a control-socket dial failure. The error from
// http.Client wraps as *url.Error → *net.OpError → *os.SyscallError →
// syscall.Errno, so it MUST be inspected with errors.Is, which unwraps the whole
// chain. os.IsPermission / os.IsNotExist are deliberately NOT used here: they do
// not unwrap *url.Error / *net.OpError and would miss the real production error.
func classifyDialError(err error) dialErrorKind {
	switch {
	case err == nil:
		return dialErrOther
	case errors.Is(err, syscall.EACCES):
		return dialErrPermission
	case errors.Is(err, syscall.ENOENT):
		return dialErrNotExist
	default:
		return dialErrOther
	}
}

// reloginMessage is the actionable diagnostic for the group-membership timing
// wrinkle: supplementary group membership is fixed at login, so a session that
// was already running when the RPM added the user to the shared group cannot
// reach the socket until the next login.
const reloginMessage = "Log out and back in (or reboot) to finish enabling local media-root configuration."

func notifyRelogin() { notify("sakms-node", reloginMessage) }

// reportControlError turns a failed control-socket call into an actionable
// notification, distinguishing the not-yet-in-group (EACCES → relogin), the
// daemon-not-running (ENOENT), and the unexpected cases.
func (t *trayUI) reportControlError(action string, err error) {
	switch classifyDialError(err) {
	case dialErrPermission:
		notifyRelogin()
	case dialErrNotExist:
		notify("sakms-node", "Cannot "+action+": the sakms-node daemon does not appear to be running.")
	default:
		notify("sakms-node", "Cannot "+action+": "+err.Error())
	}
}

// probeControlSocket runs once at startup to surface the relogin diagnostic
// early. It stays silent unless the failure is specifically permission-denied:
// a missing socket (ENOENT, daemon simply not up yet) or any other error at
// launch would only add noise — the status poll already reflects daemon-down.
func (t *trayUI) probeControlSocket() {
	ctx, cancel := context.WithTimeout(context.Background(), controlTimeout)
	defer cancel()
	if _, err := t.control.getRoots(ctx); err != nil {
		if classifyDialError(err) == dialErrPermission {
			notifyRelogin()
		}
	}
}

// handleAddRoot pops a native folder picker and adds the chosen directory to the
// node's mediaRoots via the control socket.
func (t *trayUI) handleAddRoot() {
	path, picked, err := pickDirectory()
	if err != nil {
		if errors.Is(err, errNoPicker) {
			notify("sakms-node", "No folder picker found — install zenity (GNOME) or kdialog (KDE) to add a media root.")
		} else {
			notify("sakms-node", "Folder picker failed: "+err.Error())
		}
		return
	}
	if !picked {
		return // cancelled / empty selection — no-op
	}

	ctx, cancel := context.WithTimeout(context.Background(), controlTimeout)
	defer cancel()
	roots, err := t.control.addRoot(ctx, path)
	if err != nil {
		t.reportControlError("add media root", err)
		return
	}
	notify("sakms-node", fmt.Sprintf("Added media root: %s (%d total).", path, len(roots)))
	// App-level enforcement is live immediately; refresh so the new root (and
	// any OS-level drift signal) shows without waiting for the next poll tick.
	t.poll()
}

// handleRemoveRoot removes the root a slot currently represents. Removing the
// last root re-enters the unrestricted grace period, so it is confirmed first
// (or warned about, if no dialog tool exists). This is UX friendliness, not an
// enforcement gate — the daemon accepts an empty set by design.
func (t *trayUI) handleRemoveRoot(rs *rootSlot) {
	t.mu.Lock()
	path := rs.path
	remaining := 0
	for _, r := range t.roots {
		if r.path != "" {
			remaining++
		}
	}
	t.mu.Unlock()

	if path == "" {
		return
	}
	if remaining <= 1 {
		confirmed, hadTool := askConfirm("Remove the last media root?",
			"Removing the last media root returns this node to the unrestricted grace period (no media-root allowlist). Continue?")
		if hadTool && !confirmed {
			return
		}
		if !hadTool {
			notify("sakms-node", "Removing the last media root — the node will re-enter the unrestricted grace period.")
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), controlTimeout)
	defer cancel()
	roots, err := t.control.removeRoot(ctx, path)
	if err != nil {
		t.reportControlError("remove media root", err)
		return
	}
	if len(roots) == 0 {
		notify("sakms-node", "Removed "+path+" — media roots now empty (unrestricted grace period).")
	} else {
		notify("sakms-node", fmt.Sprintf("Removed media root: %s (%d remaining).", path, len(roots)))
	}
	t.poll()
}

// errNoPicker is returned by pickDirectory when no picker tool is installed.
var errNoPicker = errors.New("no folder picker tool found (tried zenity, kdialog)")

// pickerCommand is one rung of the native-dialog fallback ladder.
type pickerCommand struct {
	name string
	args []string
}

// pickerLadder mirrors the clipboard helper's wl-copy → xclip → xsel style:
// zenity (GNOME) first, kdialog (KDE) second, graceful fallthrough.
var pickerLadder = []pickerCommand{
	{"zenity", []string{"--file-selection", "--directory", "--title=Add media root"}},
	{"kdialog", []string{"--getexistingdirectory", "/"}},
}

// runPicker executes one picker command and returns its stdout. It is a package
// var so tests can substitute a fake instead of spawning real GUI processes.
var runPicker = func(name string, args ...string) ([]byte, error) {
	return exec.Command(name, args...).Output()
}

// pickDirectory walks the picker ladder. It returns (path, true, nil) on a
// selection; ("", false, nil) when the user cancels or selects nothing; and
// ("", false, errNoPicker) when no picker tool is installed. A tool that is
// present but exits non-zero (the user cancelled) stops the ladder — a
// deliberate cancel must not pop a second picker.
func pickDirectory() (string, bool, error) {
	for _, pc := range pickerLadder {
		out, err := runPicker(pc.name, pc.args...)
		if err != nil {
			if errors.Is(err, exec.ErrNotFound) {
				continue // tool not installed — try the next rung
			}
			var exitErr *exec.ExitError
			if errors.As(err, &exitErr) {
				return "", false, nil // present but non-zero → cancelled
			}
			return "", false, err // unexpected runtime failure
		}
		path := strings.TrimSpace(string(out))
		if path == "" {
			return "", false, nil
		}
		return path, true, nil
	}
	return "", false, errNoPicker
}

// runConfirm runs one yes/no dialog command; exit 0 == Yes. Package var for tests.
var runConfirm = func(name string, args ...string) error {
	return exec.Command(name, args...).Run()
}

// askConfirm shows a yes/no dialog via the same zenity/kdialog ladder. It
// returns (confirmed, hadTool): when no dialog tool is present hadTool is false
// and the caller decides how to proceed (we fall back to a plain notify).
func askConfirm(title, text string) (confirmed, hadTool bool) {
	for _, pc := range []pickerCommand{
		{"zenity", []string{"--question", "--title=" + title, "--text=" + text}},
		{"kdialog", []string{"--title", title, "--yesno", text}},
	} {
		err := runConfirm(pc.name, pc.args...)
		if err == nil {
			return true, true // exit 0 → Yes
		}
		if errors.Is(err, exec.ErrNotFound) {
			continue // tool not installed — try the next rung
		}
		return false, true // present but non-zero (No/cancel) or runtime error → treat as declined
	}
	return false, false
}
