package main

// Claude 2026-07-22: Stage 3 — the tray's sole launcher for the on-demand Fyne
// configuration window (cmd/sakms-node-config). Replaces the three deleted
// interactive DBusMenu sections (media roots / path mappings / dispatch pause),
// which now live in that window.
// Reason: the DBusMenu submenu shape was unreachable on KDE Plasma; a real GUI
// window hosts those interactions instead. The tray stays CGO-free and read-only.
// Context: see .omc/plans/node-tray-windowed-config-ui.md Stage 3.

import (
	"errors"
	"io/fs"
	"os"
	"os/exec"
)

// configBinaryPath is the absolute path to the on-demand configuration window.
// It ships in its own RPM subpackage (sakms-node-config) that the tray only
// Recommends (not Requires), so it may be absent — handleOpenConfig stats it
// first (see below).
const configBinaryPath = "/usr/bin/sakms-node-config"

// Test injection points: spawning the real GUI or firing real notify-send in a
// unit test is neither hermetic nor possible in CI.
var (
	configStat       = os.Stat
	configRun        = runConfigBinary
	openConfigNotify = notify
)

// runConfigBinary launches the configuration window and BLOCKS until it exits.
//
// cmd.Run() (NOT cmd.Start()+go cmd.Wait()) is deliberate and load-bearing:
//   - cmd.Run() auto-reaps the child on exit. The tray is a long-lived autostart
//     process across an entire login session; a cmd.Start() without a matching
//     cmd.Wait() would leak one zombie per open, growing unbounded over a session.
//   - Blocking the single handler goroutine gives free single-instance-ish
//     behavior for repeated TRAY clicks: a second "Open configuration…" click
//     while a window is open queues in that item's ClickedCh until the first
//     window closes. (This does NOT coordinate with a SEPARATE launch from the
//     applications menu — a plan-acknowledged, accepted residual, not fixed here.)
//     No menu-state lock is held while this blocks (the earlier deadlock lesson).
func runConfigBinary() error {
	return exec.Command(configBinaryPath).Run()
}

// handleOpenConfig launches the sakms-node-config window, handling the two
// distinct failure modes the plan keeps separate:
//
//   - Absence: the config subpackage is only a Recommends (removable), so the
//     binary may not be installed. os.Stat matching fs.ErrNotExist is the correct
//     predicate for an ABSOLUTE-path target — a missing binary surfaces as a
//     *fs.PathError matching fs.ErrNotExist. Do NOT use errors.Is(err,
//     exec.ErrNotFound) here: that predicate is produced only by exec.LookPath for
//     a BARE command name (as the retired zenity/kdialog picker ladder correctly
//     used it) and would never match an absolute path — using it here would
//     silently fail to detect the absent binary, the exact bug this guards against.
//   - Launch failure: any non-zero exit from cmd.Run() — a GL-init failure before
//     any window renders, a crash, a kill — surfaces the fallback notice. A clean
//     exit 0 (normal window close) notifies nothing. No "did a window ever render"
//     timing heuristic (the plan rejected that as unreliable): the single
//     non-zero → notify rule is what makes a GL-unavailable host operator-visible.
func (t *trayUI) handleOpenConfig() {
	if _, err := configStat(configBinaryPath); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			openConfigNotify("sakms-node",
				"Configuration UI not installed — install the sakms-node-config package")
			return
		}
		// Any other stat error falls through to the launch attempt; if it too
		// fails, the launch-failure branch below surfaces it.
	}
	if err := configRun(); err != nil {
		openConfigNotify("sakms-node",
			"Configuration UI failed to start — check display/GL availability; use the server-side Settings → Nodes UI")
	}
}
