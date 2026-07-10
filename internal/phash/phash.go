// Package phash computes a CPU perceptual hash of a video by sampling several
// evenly-spaced interior frames with ffmpeg and hashing each, then
// concatenating them into one scheme-tagged composite. It mirrors
// internal/mediainfo's injected-runner test seam exactly: the ffmpeg
// shell-out is a single injected func, so Hasher is unit-testable with canned
// frame bytes and needs no real ffmpeg binary or video file. Used only by
// Movies Dedup, to refine a same-TMDB duplicate group by perceptual
// similarity (see internal/dedup.ScanLibrary).
//
// No interface is exported — house pattern (no premature abstraction): dedup
// depends on the concrete *phash.Hasher, faked on its own side via a tiny
// local interface exactly as it already does for the ffprobe Prober.
package phash

import (
	"bytes"
	"context"
	"fmt"
	"image"
	_ "image/png" // registers the PNG decoder image.Decode uses on runner output
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// runner extracts `frames` evenly-spaced still frames from the video at path,
// each as encoded PNG bytes, in frame order. Injected so Hasher is
// unit-testable with canned frames without a real ffmpeg binary or video file
// (mirrors mediainfo.runner).
type runner func(ctx context.Context, path string, frames int) ([][]byte, error)

// Hasher computes a composite perceptual hash of a video file.
type Hasher struct {
	run     runner
	frames  int
	timeout time.Duration
}

// New returns a Hasher backed by the real ffmpeg binary.
func New() *Hasher {
	return &Hasher{run: runFFmpeg, frames: Frames, timeout: 2 * time.Minute}
}

// Hash samples h.frames interior frames of the video at path, perceptually
// hashes each, and returns the concatenated per-frame hashes as one
// scheme-tagged hex string (see distance.go's encode). Bounded by an internal
// timeout layered onto ctx (mediainfo pattern). Returns an error if frame
// extraction fails, if the wrong number of frames comes back (guarding the
// composite-length invariant so callers never compare mismatched-length
// composites), or if a frame can't be decoded — a caller treats any error as
// "drop this candidate", never a partial composite.
func (h *Hasher) Hash(ctx context.Context, path string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, h.timeout)
	defer cancel()

	imgs, err := h.run(ctx, path, h.frames)
	if err != nil {
		return "", err
	}
	if len(imgs) != h.frames {
		return "", fmt.Errorf("phash: expected %d frames from %s, got %d", h.frames, path, len(imgs))
	}

	// Construct the algorithm here, not in New — a future PDQ swap uses an
	// error-returning constructor, and Hash already returns an error, so the
	// swap stays isolated to algo.go (see its doc comment).
	algo := newAlgo()
	var composite []byte
	for i, raw := range imgs {
		img, _, err := image.Decode(bytes.NewReader(raw))
		if err != nil {
			return "", fmt.Errorf("phash: decoding frame %d of %s: %w", i, path, err)
		}
		fh, err := hashFrame(algo, img)
		if err != nil {
			return "", fmt.Errorf("phash: hashing frame %d of %s: %w", i, path, err)
		}
		composite = append(composite, fh...)
	}
	return encode(Scheme, composite), nil
}

// runFFmpeg is the real runner: it probes the video's duration once, then
// pulls `frames` evenly-spaced interior still frames — deliberately avoiding
// the exact head/tail where intros, black frames, and credits collide across
// unrelated films — as PNG bytes via ffmpeg. Deterministic: fixed seeks and a
// fixed downscale mean the same file hashes identically twice. Both ffmpeg and
// ffprobe are provided by the image's ffmpeg package (already a proven
// dependency for ffprobe metadata reads); this is ffmpeg's first
// frame-decode use in SAK.
func runFFmpeg(ctx context.Context, path string, frames int) ([][]byte, error) {
	duration, err := ffprobeDuration(ctx, path)
	if err != nil {
		return nil, err
	}
	if duration <= 0 {
		return nil, fmt.Errorf("phash: %s reports no positive duration", path)
	}

	out := make([][]byte, 0, frames)
	for i := 1; i <= frames; i++ {
		t := duration * float64(i) / float64(frames+1)
		png, err := ffmpegFrame(ctx, path, t)
		if err != nil {
			return nil, err
		}
		out = append(out, png)
	}
	return out, nil
}

func ffprobeDuration(ctx context.Context, path string) (float64, error) {
	cmd := exec.CommandContext(ctx, "ffprobe",
		"-v", "error",
		"-show_entries", "format=duration",
		"-of", "default=noprint_wrappers=1:nokey=1",
		path,
	)
	raw, err := cmd.Output()
	if err != nil {
		return 0, fmt.Errorf("phash: ffprobe duration of %s: %w", path, err)
	}
	trimmed := strings.TrimSpace(string(raw))
	d, err := strconv.ParseFloat(trimmed, 64)
	if err != nil {
		return 0, fmt.Errorf("phash: parsing duration %q of %s: %w", trimmed, path, err)
	}
	return d, nil
}

func ffmpegFrame(ctx context.Context, path string, t float64) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "ffmpeg",
		"-ss", strconv.FormatFloat(t, 'f', 3, 64),
		"-i", path,
		"-frames:v", "1",
		"-vf", "scale=32:32",
		"-f", "image2",
		"-vcodec", "png",
		"-",
	)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("phash: extracting frame at %.3fs from %s: %w", t, path, err)
	}
	return buf.Bytes(), nil
}
