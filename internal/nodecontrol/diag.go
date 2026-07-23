package nodecontrol

import (
	"errors"
	"syscall"
)

// DialErrorKind buckets a control-socket connection failure for user messaging.
type DialErrorKind int

const (
	DialErrOther      DialErrorKind = iota // unexpected — show the raw error
	DialErrPermission                      // EACCES — not yet in the shared group (relogin needed)
	DialErrNotExist                        // ENOENT — socket absent (daemon not running yet)
)

// ClassifyDialError buckets a control-socket dial failure. The error from
// http.Client wraps as *url.Error → *net.OpError → *os.SyscallError →
// syscall.Errno, so it MUST be inspected with errors.Is, which unwraps the whole
// chain. os.IsPermission / os.IsNotExist are deliberately NOT used here: they do
// not unwrap *url.Error / *net.OpError and would miss the real production error.
func ClassifyDialError(err error) DialErrorKind {
	switch {
	case err == nil:
		return DialErrOther
	case errors.Is(err, syscall.EACCES):
		return DialErrPermission
	case errors.Is(err, syscall.ENOENT):
		return DialErrNotExist
	default:
		return DialErrOther
	}
}

// ReloginMessage is the actionable diagnostic for the group-membership timing
// wrinkle: supplementary group membership is fixed at login, so a session that
// was already running when the RPM added the user to the shared group cannot
// reach the socket until the next login.
const ReloginMessage = "Log out and back in (or reboot) to finish enabling local media-root configuration."

// ControlErrorMessage turns a failed control-socket call into the notification
// title and body the caller should surface, distinguishing the not-yet-in-group
// (EACCES → relogin), the daemon-not-running (ENOENT), and the unexpected cases.
// It returns the message rather than firing a notification itself so each
// surface (tray notify(), config window in-widget) renders it its own way.
func ControlErrorMessage(action string, err error) (title, body string) {
	switch ClassifyDialError(err) {
	case DialErrPermission:
		return "sakms-node", ReloginMessage
	case DialErrNotExist:
		return "sakms-node", "Cannot " + action + ": the sakms-node daemon does not appear to be running."
	default:
		return "sakms-node", "Cannot " + action + ": " + err.Error()
	}
}
