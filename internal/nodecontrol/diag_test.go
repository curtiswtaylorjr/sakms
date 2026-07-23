package nodecontrol

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"syscall"
	"testing"
)

// wrapDial reproduces the exact wrapping http.Client produces for a failed
// unix-socket dial: *url.Error → *net.OpError → *os.SyscallError → syscall.Errno.
func wrapDial(errno syscall.Errno) error {
	return &url.Error{
		Op:  "Get",
		URL: "http://sakms-node/mediaroots",
		Err: &net.OpError{
			Op:  "dial",
			Net: "unix",
			Err: os.NewSyscallError("connect", errno),
		},
	}
}

func TestClassifyDialError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want DialErrorKind
	}{
		{"nil", nil, DialErrOther},
		{"permission denied (EACCES)", wrapDial(syscall.EACCES), DialErrPermission},
		{"socket absent (ENOENT)", wrapDial(syscall.ENOENT), DialErrNotExist},
		{"connection refused is other", wrapDial(syscall.ECONNREFUSED), DialErrOther},
		{"plain error is other", fmt.Errorf("boom"), DialErrOther},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ClassifyDialError(c.err); got != c.want {
				t.Errorf("ClassifyDialError = %v, want %v", got, c.want)
			}
		})
	}
}

// TestControlErrorMessage pins the exact (title, body) each dial-error bucket
// renders — the messaging the tray's reportControlError previously produced
// inline, now returned as data for the caller to notify() with. Byte-for-byte
// parity with the pre-extraction messages is the point of this test.
func TestControlErrorMessage(t *testing.T) {
	cases := []struct {
		name      string
		action    string
		err       error
		wantTitle string
		wantBody  string
	}{
		{
			"permission → relogin",
			"add media root",
			wrapDial(syscall.EACCES),
			"sakms-node",
			ReloginMessage,
		},
		{
			"not-exist → daemon not running",
			"remove media root",
			wrapDial(syscall.ENOENT),
			"sakms-node",
			"Cannot remove media root: the sakms-node daemon does not appear to be running.",
		},
		{
			"other → raw error",
			"set path mapping",
			errors.New("boom"),
			"sakms-node",
			"Cannot set path mapping: boom",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			title, body := ControlErrorMessage(c.action, c.err)
			if title != c.wantTitle || body != c.wantBody {
				t.Errorf("ControlErrorMessage = (%q, %q), want (%q, %q)", title, body, c.wantTitle, c.wantBody)
			}
		})
	}
}
