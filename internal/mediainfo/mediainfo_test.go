package mediainfo

import (
	"context"
	"errors"
	"testing"
	"time"
)

func fakeProber(out []byte, err error) *Prober {
	return &Prober{
		run: func(ctx context.Context, path string) ([]byte, error) {
			return out, err
		},
		timeout: 5 * time.Second,
	}
}

func TestProbe_ParsesFields(t *testing.T) {
	raw := []byte(`{"streams":[{"codec_name":"av1","width":1920,"height":1080,"bit_rate":"4416482"}]}`)
	p := fakeProber(raw, nil)

	got, err := p.Probe(context.Background(), "/fake/path.mp4")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.CodecName != "av1" || got.Width != 1920 || got.Height != 1080 || got.BitRate != 4416482 {
		t.Errorf("unexpected probe result: %+v", got)
	}
}

func TestProbe_MissingBitRateDefaultsToZero(t *testing.T) {
	raw := []byte(`{"streams":[{"codec_name":"h264","width":1280,"height":720,"bit_rate":""}]}`)
	p := fakeProber(raw, nil)

	got, err := p.Probe(context.Background(), "/fake/path.mp4")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.BitRate != 0 {
		t.Errorf("expected 0 bitrate fallback, got %d", got.BitRate)
	}
}

func TestProbe_NoVideoStreamErrors(t *testing.T) {
	raw := []byte(`{"streams":[]}`)
	p := fakeProber(raw, nil)

	_, err := p.Probe(context.Background(), "/fake/path.mp4")
	if err == nil {
		t.Error("expected an error for a file with no video stream")
	}
}

func TestProbe_RunnerErrorPropagates(t *testing.T) {
	p := fakeProber(nil, errors.New("ffprobe: no such file"))

	_, err := p.Probe(context.Background(), "/fake/path.mp4")
	if err == nil {
		t.Error("expected the runner's error to propagate")
	}
}

func TestProbe_MalformedJSONErrors(t *testing.T) {
	p := fakeProber([]byte("not json"), nil)

	_, err := p.Probe(context.Background(), "/fake/path.mp4")
	if err == nil {
		t.Error("expected an error for malformed JSON output")
	}
}
