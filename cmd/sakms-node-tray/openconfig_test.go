package main

import (
	"errors"
	"io/fs"
	"testing"
)

// captureNotify swaps openConfigNotify for a recorder for the duration of a test.
func captureNotify(t *testing.T) *[]string {
	t.Helper()
	var msgs []string
	orig := openConfigNotify
	openConfigNotify = func(title, body string) { msgs = append(msgs, body) }
	t.Cleanup(func() { openConfigNotify = orig })
	return &msgs
}

// TestHandleOpenConfig_GracefulAbsence: an os.Stat that reports the binary is not
// installed (fs.ErrNotExist, as a *fs.PathError does for an absolute path) fires
// the "not installed" notice and MUST NOT attempt to exec.
func TestHandleOpenConfig_GracefulAbsence(t *testing.T) {
	msgs := captureNotify(t)

	origStat, origRun := configStat, configRun
	t.Cleanup(func() { configStat, configRun = origStat, origRun })

	configStat = func(string) (fs.FileInfo, error) {
		return nil, &fs.PathError{Op: "stat", Path: configBinaryPath, Err: fs.ErrNotExist}
	}
	ran := false
	configRun = func() error { ran = true; return nil }

	(&trayUI{}).handleOpenConfig()

	if ran {
		t.Fatal("handleOpenConfig exec'd the config binary despite it being absent")
	}
	if len(*msgs) != 1 || (*msgs)[0] != "Configuration UI not installed — install the sakms-node-config package" {
		t.Fatalf("notify messages = %v, want the not-installed notice", *msgs)
	}
}

// TestHandleOpenConfig_LaunchFailure: a present binary whose cmd.Run() returns a
// non-zero exit fires the launch-failure notice (distinct from the absence one).
func TestHandleOpenConfig_LaunchFailure(t *testing.T) {
	msgs := captureNotify(t)

	origStat, origRun := configStat, configRun
	t.Cleanup(func() { configStat, configRun = origStat, origRun })

	configStat = func(string) (fs.FileInfo, error) { return nil, nil } // present
	configRun = func() error { return errors.New("exit status 1") }

	(&trayUI{}).handleOpenConfig()

	if len(*msgs) != 1 || (*msgs)[0] != "Configuration UI failed to start — check display/GL availability; use the server-side Settings → Nodes UI" {
		t.Fatalf("notify messages = %v, want the launch-failure notice", *msgs)
	}
}

// TestHandleOpenConfig_CleanExit: a clean exit 0 (normal window close) notifies
// nothing at all.
func TestHandleOpenConfig_CleanExit(t *testing.T) {
	msgs := captureNotify(t)

	origStat, origRun := configStat, configRun
	t.Cleanup(func() { configStat, configRun = origStat, origRun })

	configStat = func(string) (fs.FileInfo, error) { return nil, nil } // present
	configRun = func() error { return nil }                            // exit 0

	(&trayUI{}).handleOpenConfig()

	if len(*msgs) != 0 {
		t.Fatalf("notify messages = %v, want none on a clean exit", *msgs)
	}
}
