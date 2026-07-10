package phash

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"
	"time"
)

// fakeHasher builds a Hasher with an injected runner, mirroring
// mediainfo_test.go's fakeProber — no real ffmpeg binary or video file.
func fakeHasher(frames int, run runner) *Hasher {
	return &Hasher{run: run, frames: frames, timeout: 5 * time.Second}
}

// gradientPNG renders a tiny deterministic non-uniform image as PNG bytes, so
// the perceptual hash has real (non-all-zero) structure to hash. seed shifts
// the pattern so two calls with different seeds produce visibly different
// frames.
func gradientPNG(t *testing.T, seed int) []byte {
	t.Helper()
	img := image.NewGray(image.Rect(0, 0, 16, 16))
	for y := 0; y < 16; y++ {
		for x := 0; x < 16; x++ {
			img.SetGray(x, y, color.Gray{Y: uint8((x*13 + y*7 + seed*29) % 256)})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encoding png: %v", err)
	}
	return buf.Bytes()
}

func cannedFrames(t *testing.T, n int) [][]byte {
	t.Helper()
	out := make([][]byte, n)
	for i := range out {
		out[i] = gradientPNG(t, i)
	}
	return out
}

func TestHash_ComputesDeterministicCompositeFromFrames(t *testing.T) {
	frames := cannedFrames(t, 5)
	h := fakeHasher(5, func(ctx context.Context, path string, n int) ([][]byte, error) {
		if n != 5 {
			t.Fatalf("expected runner asked for 5 frames, got %d", n)
		}
		return frames, nil
	})

	got, err := h.Hash(context.Background(), "/fake/movie.mkv")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasPrefix(got, Scheme+":") || len(got) <= len(Scheme)+1 {
		t.Fatalf("expected a non-empty scheme-tagged hash, got %q", got)
	}

	// The same frames must hash identically twice — the determinism the
	// build-tagged integration test later proves end-to-end through real
	// ffmpeg decode.
	again, err := h.Hash(context.Background(), "/fake/movie.mkv")
	if err != nil {
		t.Fatalf("unexpected error on second hash: %v", err)
	}
	if got != again {
		t.Errorf("expected identical frames to hash identically, got %q then %q", got, again)
	}
}

func TestHash_WrongFrameCountErrors(t *testing.T) {
	h := fakeHasher(5, func(ctx context.Context, path string, n int) ([][]byte, error) {
		return cannedFrames(t, 4), nil // one short of the 5 expected
	})
	if _, err := h.Hash(context.Background(), "/fake/movie.mkv"); err == nil {
		t.Fatal("expected an error when the runner returns the wrong number of frames")
	}
}

func TestHash_RunnerErrorPropagates(t *testing.T) {
	h := fakeHasher(5, func(ctx context.Context, path string, n int) ([][]byte, error) {
		return nil, errors.New("ffmpeg: no such file")
	})
	if _, err := h.Hash(context.Background(), "/fake/movie.mkv"); err == nil {
		t.Fatal("expected the runner's error to propagate")
	}
}

func TestHash_UndecodableFrameErrors(t *testing.T) {
	h := fakeHasher(5, func(ctx context.Context, path string, n int) ([][]byte, error) {
		frames := cannedFrames(t, 5)
		frames[2] = []byte("not a png") // one frame the PNG decoder can't read
		return frames, nil
	})
	if _, err := h.Hash(context.Background(), "/fake/movie.mkv"); err == nil {
		t.Fatal("expected an error when a frame can't be decoded")
	}
}
