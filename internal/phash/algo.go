package phash

import (
	"fmt"
	"image"

	"github.com/ajdnik/imghash/v2"
	"github.com/ajdnik/imghash/v2/hashtype"
)

// Scheme tags every hash this package produces with the algorithm and frame
// count that produced it. A value cached under a different algorithm or
// sampling count is therefore self-identifying and treated as incomparable by
// SimilarityWithin, never silently mis-distanced. It is embedded in the stored
// hash string (see internal/db migration 0017 + internal/library threading),
// so a scheme change makes every cached value self-invalidating. Exported so
// the dedup cache layer can cheaply reject a stale-scheme cached hash by prefix
// before trusting a size+mtime identity match.
//
// This file is the SINGLE algorithm swap point. It ships imghash/v2's PHash
// (64 bits/frame); swapping to PDQ changes only newAlgo, hashFrame, and this
// Scheme constant — nothing downstream, which is algorithm-agnostic (hashes
// are compared as scheme-tagged byte composites by Hamming distance regardless
// of which algorithm produced them).
//
// The tag is "phash64v2/5f", not the pre-v2 "phash64/5f": the imghash v1->v2.5.2
// bump (Stage 0) changed PHash's byte output for some inputs — a proven 1-bit
// drift on 3 of 8 fixed golden frames, same algorithm, from v2's internal
// "Refactor for Maintainability" (see golden_test.go). Per this package's
// no-silent-mis-compare rule the tag MUST change on ANY output change, so old
// "phash64/5f" cached values self-invalidate and recompute. The scale is still
// 64 bits/frame, so per-mode stored thresholds (keyed on scale, not scheme)
// stay valid.
const Scheme = "phash64v2/5f"

// Frames is the fixed number of evenly-spaced interior frames sampled per
// video to form one composite hash. Exported so the dedup layer can express
// its similarity threshold as a per-frame average, independent of this count.
const Frames = 5

// newAlgo constructs the perceptual-hash algorithm. Called from Hash (not
// New) deliberately: v2's constructor returns an error (NewPHash() (PHash,
// error)), and Hash already returns an error, so the swap stays isolated to
// this file instead of rippling into New's signature. Constructed with zero
// options, which yields imghash/v2's default PHash (32x32 downscale, 8x8 DCT
// block → 64-bit hash).
func newAlgo() (imghash.PHash, error) {
	return imghash.NewPHash()
}

// hashFrame returns the per-frame perceptual hash bytes for one decoded image.
// v2's PHash.Calculate returns a hashtype.Hash interface plus an error; for
// PHash the concrete value is a hashtype.Binary (type Binary []byte), so the
// result is type-asserted to it before extracting the raw bytes.
func hashFrame(a imghash.PHash, img image.Image) ([]byte, error) {
	h, err := a.Calculate(img)
	if err != nil {
		return nil, err
	}
	b, ok := h.(hashtype.Binary)
	if !ok {
		return nil, fmt.Errorf("phash: expected hashtype.Binary, got %T", h)
	}
	return []byte(b), nil
}

// hammingBits is a plain popcount over the XOR of two equal-length byte
// slices — deliberately NOT imghash's similarity.Hamming, whose return
// semantics (raw bit count vs. a normalized fraction) could not be confirmed
// from its docs. A popcount is algorithm-agnostic and correct regardless of
// what any third-party helper returns, so distance.go never depends on
// unverified upstream semantics. Callers guarantee len(a) == len(b).
func hammingBits(a, b []byte) int {
	d := 0
	for i := range a {
		x := a[i] ^ b[i]
		for x != 0 {
			d += int(x & 1)
			x >>= 1
		}
	}
	return d
}
