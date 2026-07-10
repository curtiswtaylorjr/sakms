package phash

import (
	"encoding/hex"
	"fmt"
	"strings"
)

// DefaultThreshold is the starting per-frame average Hamming distance under
// which two composite hashes are treated as the same content. It is a
// STARTING DEFAULT and an algorithm-sanity regression guard — NOT a
// real-world-validated constant. It is exposed as a per-mode tunable
// (GET/PUT /api/modes/{mode}/phash-threshold); real-world confidence comes
// from the build-tagged integration test and the manual live walkthrough
// against actual movie files, not from this value being provably correct on
// arbitrary movie frames (see calibrate_test.go's doc comment).
const DefaultThreshold = 10

// encode returns the DB/candidate-JSON storage form of a composite hash:
// "<scheme>:<hex>", e.g. "phash64/5f:1a2b...". The scheme tag makes a hash
// self-describing, so a value cached under an OLD algorithm or frame count is
// detectably incomparable to a freshly computed one.
func encode(scheme string, composite []byte) string {
	return scheme + ":" + hex.EncodeToString(composite)
}

// decode splits an encoded hash back into its scheme tag and raw composite
// bytes. Returns an error for a malformed string (no scheme separator or a
// non-hex payload).
func decode(s string) (scheme string, composite []byte, err error) {
	i := strings.IndexByte(s, ':')
	if i < 0 {
		return "", nil, fmt.Errorf("phash: malformed encoded hash %q (no scheme separator)", s)
	}
	composite, err = hex.DecodeString(s[i+1:])
	if err != nil {
		return "", nil, fmt.Errorf("phash: decoding hash payload of %q: %w", s, err)
	}
	return s[:i], composite, nil
}

// SimilarityWithin reports whether a and b are within perFrameThreshold average
// Hamming bits per frame. It returns (false, nil) — NOT an error — when the two
// hashes have different schemes or unequal lengths, so a stale-scheme cache
// entry can never wrongly assert similarity; it returns an error only when a
// value is structurally undecodable. Expressing the threshold as a per-frame
// average keeps the tunable a clean 0–64 number independent of frame count.
func SimilarityWithin(a, b string, frames, perFrameThreshold int) (bool, error) {
	schemeA, compositeA, err := decode(a)
	if err != nil {
		return false, err
	}
	schemeB, compositeB, err := decode(b)
	if err != nil {
		return false, err
	}
	if schemeA != schemeB || len(compositeA) != len(compositeB) {
		return false, nil
	}
	return hammingBits(compositeA, compositeB) <= perFrameThreshold*frames, nil
}
