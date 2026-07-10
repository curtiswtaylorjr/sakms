package phash

import "testing"

func TestSimilarityWithin_IdenticalCompositesWithinAnyThreshold(t *testing.T) {
	a := encode(Scheme, []byte{0x00, 0xff, 0x0f, 0xaa, 0x55})
	within, err := SimilarityWithin(a, a, 5, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !within {
		t.Error("expected identical composites to be within even a zero threshold")
	}
}

func TestSimilarityWithin_OneBitDiffWithinLooseThreshold(t *testing.T) {
	base := []byte{0x00, 0x00, 0x00, 0x00, 0x00}
	flipped := []byte{0x01, 0x00, 0x00, 0x00, 0x00} // exactly one bit differs
	a := encode(Scheme, base)
	b := encode(Scheme, flipped)

	// perFrameThreshold 1 over 5 frames = a budget of 5 Hamming bits total;
	// one differing bit is comfortably within it.
	within, err := SimilarityWithin(a, b, 5, 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !within {
		t.Error("expected a one-bit difference to be within a loose threshold")
	}

	// A zero budget must reject even a single differing bit.
	within, err = SimilarityWithin(a, b, 5, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if within {
		t.Error("expected a one-bit difference to be rejected by a zero threshold")
	}
}

func TestSimilarityWithin_SchemeMismatchIsFalseNotError(t *testing.T) {
	comp := []byte{0x12, 0x34, 0x56, 0x78, 0x9a}
	a := encode(Scheme, comp)
	b := encode("otherscheme/5f", comp)

	within, err := SimilarityWithin(a, b, 5, 64)
	if err != nil {
		t.Fatalf("expected no error on a scheme mismatch, got %v", err)
	}
	if within {
		t.Error("expected a scheme mismatch to report not-within (a stale-scheme cache entry must never assert similarity)")
	}
}

func TestSimilarityWithin_UnequalLengthIsFalseNotError(t *testing.T) {
	a := encode(Scheme, []byte{0x00, 0x00, 0x00, 0x00, 0x00})
	b := encode(Scheme, []byte{0x00}) // shorter composite

	within, err := SimilarityWithin(a, b, 5, 64)
	if err != nil {
		t.Fatalf("expected no error on unequal length, got %v", err)
	}
	if within {
		t.Error("expected unequal-length composites to report not-within")
	}
}

func TestSimilarityWithin_UndecodableInputErrors(t *testing.T) {
	valid := encode(Scheme, []byte{0x00})
	if _, err := SimilarityWithin("no-scheme-separator", valid, 5, 64); err == nil {
		t.Error("expected an error for a structurally undecodable hash")
	}
	if _, err := SimilarityWithin(valid, Scheme+":nothexZZ", 5, 64); err == nil {
		t.Error("expected an error for a non-hex payload")
	}
}
