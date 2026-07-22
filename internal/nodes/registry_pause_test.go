package nodes

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

// captureInfoLogs redirects slog's default logger to a buffer at Info level for
// the duration of fn, then restores it, returning everything logged.
func captureInfoLogs(t *testing.T, fn func()) string {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})))
	defer slog.SetDefault(prev)
	fn()
	return buf.String()
}

// TestDispatchPausedNodeSkipped_FallsBackAndLogs covers a paused node that is
// the only candidate: Dispatch returns ok=false (the caller's local-hash
// fallback), and both required INFO lines fire — the per-node skip and the
// distinct pause-caused local-fallback line.
func TestDispatchPausedNodeSkipped_FallsBackAndLogs(t *testing.T) {
	r := New()
	nodeID := "node-a-id"
	_, _, _, disconnect := r.Connect(nodeID, "node-a", nil)
	defer disconnect()

	r.SetNodePaused(nodeID, true)

	var ok bool
	logs := captureInfoLogs(t, func() {
		_, _, ok = r.Dispatch(Job{ID: "j1", Type: JobTypePhash})
	})
	if ok {
		t.Fatal("Dispatch selected a paused node")
	}
	if !strings.Contains(logs, "skipping paused node during dispatch selection") {
		t.Errorf("missing per-node pause-skip INFO log; got:\n%s", logs)
	}
	if !strings.Contains(logs, "pause excluded a candidate, no eligible node remained") {
		t.Errorf("missing pause-caused local-fallback INFO log; got:\n%s", logs)
	}
}

// TestDispatchPausedFallsThroughToEligible confirms a paused node is stepped
// over in favour of an eligible one — dispatch succeeds and targets the
// eligible node, never the paused one. (No log assertion here: map iteration
// order is random, so the paused node may or may not be visited before the
// eligible one is chosen.)
func TestDispatchPausedFallsThroughToEligible(t *testing.T) {
	r := New()
	pausedID := "node-paused-id"
	eligibleID := "node-eligible-id"
	_, _, _, dc1 := r.Connect(pausedID, "paused", nil)
	defer dc1()
	eligibleJobs, _, _, dc2 := r.Connect(eligibleID, "eligible", nil)
	defer dc2()

	r.SetNodePaused(pausedID, true)

	nodeID, _, ok := r.Dispatch(Job{ID: "j1", Type: JobTypePhash})
	if !ok {
		t.Fatal("Dispatch returned ok=false despite an eligible node")
	}
	if nodeID != eligibleID {
		t.Fatalf("Dispatch selected %q, want the eligible node %q", nodeID, eligibleID)
	}
	drainOneJob(t, eligibleJobs)
	r.ClearPending("j1")
}

// TestSetNodePaused_ResumeReenables confirms unpausing restores dispatch
// eligibility.
func TestSetNodePaused_ResumeReenables(t *testing.T) {
	r := New()
	nodeID := "node-a-id"
	jobs, _, _, disconnect := r.Connect(nodeID, "node-a", nil)
	defer disconnect()

	r.SetNodePaused(nodeID, true)
	if _, _, ok := r.Dispatch(Job{ID: "j1", Type: JobTypePhash}); ok {
		t.Fatal("Dispatch selected a paused node")
	}

	r.SetNodePaused(nodeID, false)
	if _, _, ok := r.Dispatch(Job{ID: "j2", Type: JobTypePhash}); !ok {
		t.Fatal("Dispatch skipped a resumed node")
	}
	drainOneJob(t, jobs)
	r.ClearPending("j2")
}

// TestSetNodePaused_UnknownID_NoOp confirms SetNodePaused on a disconnected /
// unknown id neither panics nor errors.
func TestSetNodePaused_UnknownID_NoOp(t *testing.T) {
	r := New()
	r.SetNodePaused("never-connected", true) // must not panic
}
