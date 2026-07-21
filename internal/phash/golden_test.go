package phash

import (
	"encoding/hex"
	"testing"
)

// TestGolden_PHashOutputStable pins the exact per-frame PHash bytes this package
// produces for a fixed set of deterministic synthetic inputs (broadbandImage
// seeds 0..7 at size 128 — the same generator calibrate_test uses). It is the
// self-enforcing guard for this package's #1 rule: a hash whose bytes change
// meaning must never be compared as if equivalent, so ANY change to PHash's byte
// output (a library bump, a parameter change, a decode-path change) must fail
// here and force a deliberate Scheme bump.
//
// Provenance / drift record: the pinned values below are imghash v2.5.2's PHash
// output. The imghash v1.1.0 -> v2.5.2 upgrade (Stage 0 of the phash-pdq
// migration) DID move PHash's output — a proven, real drift, not an assumed one.
// Captured bit-for-bit at the pre-Stage-0 commit (6b573ac~1, imghash v1.1.0)
// against the identical broadbandImage inputs, v1 differed from v2 on 3 of the 8
// seeds by exactly 1 bit each (seeds 2, 4, 7; seeds 0/1/3/5/6 identical):
//
//	seed  v1 (imghash 1.1.0)  v2 (imghash 2.5.2)   delta
//	 2    ...e7e2a3b8          ...e7eaa3b8          1 bit
//	 4    f18ef54f...          f18ff54f...          1 bit
//	 7    ...d494c095          ...d094c095          1 bit
//
// That real drift is why Scheme moved from "phash64/5f" to "phash64v2/5f": under
// the no-silent-mis-compare rule, even a 1-bit same-algorithm change forces the
// tag to change so stale cached values self-invalidate. The drift did NOT erode
// class separation — calibrate_test measured an identical dupMax=6 / diffMin=25
// under both v1 and v2 — so the per-mode thresholds were not retuned.
func TestGolden_PHashOutputStable(t *testing.T) {
	// imghash v2.5.2 PHash of broadbandImage(seed, 128), seeds 0..7.
	want := []string{
		"35a2b5e1b55abf93",
		"95dae7f29ac96aef",
		"3b485a4fe7eaa3b8",
		"d8e1545ec06b2a90",
		"f18ff54f602fb72f",
		"18d8bd880c7f0fc5",
		"c352da0282f25aaf",
		"d0b9d791d094c095",
	}
	algo, err := newAlgo()
	if err != nil {
		t.Fatalf("constructing algo: %v", err)
	}
	for seed, wantHex := range want {
		h, err := hashFrame(algo, broadbandImage(seed, 128))
		if err != nil {
			t.Fatalf("seed %d: hashing: %v", seed, err)
		}
		if got := hex.EncodeToString(h); got != wantHex {
			t.Errorf("seed %d: PHash output changed: got %s, want %s — if this is an "+
				"intentional algorithm/library change, bump Scheme and re-pin these goldens",
				seed, got, wantHex)
		}
	}
}
