package celt

import (
	"math"
	"testing"
)

// TestHybridPrefillUsesHybridDecisions: celt_encode_with_ec takes its hybrid
// decisions from `start != 0`, so the 2-byte hybrid prefill (start band 17,
// private range coder) uses the hybrid spread rule (aggressive for a
// non-transient frame) rather than the CELT-only small-budget rule.
func TestHybridPrefillUsesHybridDecisions(t *testing.T) {
	e, err := NewEncoder(FrameSize2_5ms, 48000, 1, &EncoderConfig{Bitrate: 32000, Complexity: 7, RateMode: RateModeCBR})
	if err != nil {
		t.Fatal(err)
	}
	// The first frames spend the 16 bits on coarse energy; once the band
	// energies settle the spread symbol fits.
	for k := 0; k < 4; k++ {
		pcm := make([]float64, FrameSize2_5ms)
		for i := range pcm {
			pcm[i] = 0.3 * math.Sin(2*math.Pi*float64(i+k*FrameSize2_5ms)*9000/48000)
		}
		if err := e.EncodePrefill(pcm, 17); err != nil {
			t.Fatal(err)
		}
	}
	if e.lastSpread != spreadAggressive {
		t.Fatalf("hybrid prefill spread = %d, want %d (aggressive)", e.lastSpread, spreadAggressive)
	}
}
