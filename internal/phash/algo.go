package phash

import (
	"image"

	"github.com/ajdnik/imghash"
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
// This file is the SINGLE algorithm swap point. It ships imghash's released
// v1.1.0 PHash (64 bits/frame) per the confirmed Option B decision; swapping
// to PDQ once imghash tags a release containing it changes only newAlgo,
// hashFrame, and this Scheme constant — nothing downstream, which is
// algorithm-agnostic (hashes are compared as scheme-tagged byte composites by
// Hamming distance regardless of which algorithm produced them).
const Scheme = "phash64/5f"

// Frames is the fixed number of evenly-spaced interior frames sampled per
// video to form one composite hash. Exported so the dedup layer can express
// its similarity threshold as a per-frame average, independent of this count.
const Frames = 5

// newAlgo constructs the perceptual-hash algorithm. Called from Hash (not
// New) deliberately: a future PDQ swap uses an error-returning constructor
// (NewPDQ() (PDQ, error)), and Hash already returns an error, so the swap
// stays isolated to this file instead of rippling into New's signature.
func newAlgo() *imghash.PHash {
	h := imghash.NewPHash()
	return &h
}

// hashFrame returns the per-frame perceptual hash bytes for one decoded image.
// imghash's PHash.Calculate returns a hashtype.Binary (a []byte-underlying
// type); the explicit conversion keeps this correct even if a future
// algorithm returns a differently-named []byte type.
func hashFrame(a *imghash.PHash, img image.Image) ([]byte, error) {
	return []byte(a.Calculate(img)), nil
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
