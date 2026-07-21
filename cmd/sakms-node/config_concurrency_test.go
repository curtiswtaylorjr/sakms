package main

import (
	"context"
	"path/filepath"
	"sync"
	"testing"

	"github.com/labbersanon/sakms/internal/nodes"
)

// fastHasher is a no-op hasher so executeJob's concurrency can be exercised
// without invoking a real (slow, FS/ffmpeg-dependent) hash.
type fastHasher struct{}

func (fastHasher) Hash(_ context.Context, _ string) (string, error) { return "deadbeef", nil }

// TestConfigLock_SocketWriteRaces401ClearAPIKey exercises the race the Step 2
// main()-level control-socket listener introduces against the 401 re-pair path
// (main.go:106, cfg.APIKey = ""). The socket writer is simulated by the exact
// mutateAndSave discipline that handler will use. Under -race this passes only
// because both writers hold cfg.mu across their field write AND their save; if
// the lock in mutateAndSave/clearAPIKey is removed, the socket save's whole-
// struct marshal reads APIKey/MediaRoots while the other goroutine writes them,
// and the detector fires — proving the test guards a real race.
func TestConfigLock_SocketWriteRaces401ClearAPIKey(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	cfg := &NodeConfig{ServerURL: "https://example.test", NodeName: "n", APIKey: "initial-key", MediaRoots: []string{"/a"}}

	const iters = 200
	var wg sync.WaitGroup
	wg.Add(2)

	// Control-socket write path (future Step 2 handler): mutates MediaRoots.
	go func() {
		defer wg.Done()
		for i := 0; i < iters; i++ {
			_ = cfg.mutateAndSave(path, func() { cfg.MediaRoots = []string{"/media/root"} })
		}
	}()

	// 401 re-pair path: clears APIKey (main.go:106).
	go func() {
		defer wg.Done()
		for i := 0; i < iters; i++ {
			_ = cfg.clearAPIKey(path)
		}
	}()

	wg.Wait()
}

// TestConfigLock_SocketWriteRacesPairApply exercises the second race site added
// in iteration 7: pair()'s persist step (pairing.go:78-84, now applyPairConfig)
// writes APIKey/MaxJobs/PathMap then saves, and runs on initial pairing and
// every 401 re-pair. A concurrent control-socket save marshals those same
// fields. Under -race this passes only because both paths are one locked
// critical section; removing the lock makes the detector fire.
func TestConfigLock_SocketWriteRacesPairApply(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	cfg := &NodeConfig{ServerURL: "https://example.test", NodeName: "n"}
	pathMap := []PathMapEntry{{Server: "/data/movies", Local: "/mnt/movies"}}

	const iters = 200
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		for i := 0; i < iters; i++ {
			_ = cfg.mutateAndSave(path, func() { cfg.MediaRoots = []string{"/media/root"} })
		}
	}()

	go func() {
		defer wg.Done()
		for i := 0; i < iters; i++ {
			_ = cfg.applyPairConfig(path, "paired-key", 4, pathMap)
		}
	}()

	wg.Wait()
}

// TestConfigLock_ConcurrentReadWriteMediaRoots is the Step 1 acceptance case:
// a writer mutating MediaRoots concurrently with executeJob/executeBrowse
// reading it must show no race and no torn read (both readers go through
// cfg.snapshot()).
func TestConfigLock_ConcurrentReadWriteMediaRoots(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	root := t.TempDir()
	cfg := &NodeConfig{ServerURL: "https://example.test", NodeName: "n", MediaRoots: []string{root}}

	const iters = 200
	var wg sync.WaitGroup
	wg.Add(3)

	go func() {
		defer wg.Done()
		for i := 0; i < iters; i++ {
			_ = cfg.mutateAndSave(path, func() { cfg.MediaRoots = []string{root} })
		}
	}()

	go func() {
		defer wg.Done()
		for i := 0; i < iters; i++ {
			_ = executeJob(context.Background(), cfg, nodes.Job{ID: "j", Type: nodes.JobTypePhash, ServerPath: root}, fastHasher{}, fastHasher{})
		}
	}()

	go func() {
		defer wg.Done()
		for i := 0; i < iters; i++ {
			_ = executeBrowse(cfg, nodes.BrowseRequest{ID: "b", Path: root})
		}
	}()

	wg.Wait()
}
