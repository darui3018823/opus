package silk

import (
	"math"
	"testing"
)

// TestLBRRRegularPathDeterminism asserts that enabling inband FEC does not alter
// the regular (non-LBRR) encode: an FEC encoder and a non-FEC encoder fed the
// same PCM must keep identical internal state after every packet, and produce
// identical regular-frame symbols. (The FEC stream only has extra LBRR bytes at
// the front.)
func TestLBRRRegularPathDeterminism(t *testing.T) {
	const (
		rate     = 16000
		nFrames  = 3 // 60 ms packets, exercising the multi-frame LBRR path
		nPackets = 12
	)
	encFEC, err := NewEncoder(rate, 1)
	if err != nil {
		t.Fatalf("NewEncoder FEC: %v", err)
	}
	encNo, err := NewEncoder(rate, 1)
	if err != nil {
		t.Fatalf("NewEncoder no-FEC: %v", err)
	}
	encFEC.SetPacketLossPerc(20)
	encFEC.SetInbandFEC(true)

	frameSize := rate * 20 / 1000
	for p := 0; p < nPackets; p++ {
		pcm := make([]float64, frameSize*nFrames)
		for i := range pcm {
			tt := float64(p*frameSize*nFrames+i) / float64(rate)
			env := 0.55 + 0.35*math.Sin(2*math.Pi*3*tt)
			pcm[i] = env * (0.32*math.Sin(2*math.Pi*180*tt) + 0.12*math.Sin(2*math.Pi*360*tt+0.4))
		}

		// Independent copies so an in-place modification cannot cross-contaminate.
		a := append([]float64(nil), pcm...)
		b := append([]float64(nil), pcm...)
		if _, err := encFEC.EncodeMulti(a, nFrames); err != nil {
			t.Fatalf("packet %d FEC EncodeMulti: %v", p, err)
		}
		if _, err := encNo.EncodeMulti(b, nFrames); err != nil {
			t.Fatalf("packet %d no-FEC EncodeMulti: %v", p, err)
		}
		if fa, fb := encFEC.leakFingerprint(), encNo.leakFingerprint(); fa != fb {
			t.Fatalf("encoder state diverged after packet %d:\n  FEC   = %s\n  noFEC = %s", p, fa, fb)
		}
	}
}

// TestLBRRNormalDecodeConsumesRedundancy verifies that normal decoding skips the
// LBRR bodies and lands on the same regular-frame symbols as a no-FEC stream.
// The redundant frames must not be synthesized or alter the regular decoder
// state unless the caller explicitly requests FEC recovery.
func TestLBRRNormalDecodeConsumesRedundancy(t *testing.T) {
	const (
		rate     = 16000
		nFrames  = 3
		nPackets = 8
	)
	encFEC, err := NewEncoder(rate, 1)
	if err != nil {
		t.Fatalf("NewEncoder FEC: %v", err)
	}
	encNo, err := NewEncoder(rate, 1)
	if err != nil {
		t.Fatalf("NewEncoder no-FEC: %v", err)
	}
	encFEC.SetPacketLossPerc(20)
	encFEC.SetInbandFEC(true)

	decFEC, err := NewDecoder(rate, 1)
	if err != nil {
		t.Fatalf("NewDecoder FEC: %v", err)
	}
	decNo, err := NewDecoder(rate, 1)
	if err != nil {
		t.Fatalf("NewDecoder no-FEC: %v", err)
	}

	frameSize := rate * 20 / 1000
	for p := 0; p < nPackets; p++ {
		pcm := make([]float64, frameSize*nFrames)
		for i := range pcm {
			tt := float64(p*frameSize*nFrames+i) / float64(rate)
			pcm[i] = 0.28*math.Sin(2*math.Pi*190*tt) +
				0.09*math.Sin(2*math.Pi*570*tt+0.3)
		}
		pktFEC, err := encFEC.EncodeMulti(append([]float64(nil), pcm...), nFrames)
		if err != nil {
			t.Fatalf("packet %d FEC encode: %v", p, err)
		}
		pktNo, err := encNo.EncodeMulti(append([]float64(nil), pcm...), nFrames)
		if err != nil {
			t.Fatalf("packet %d no-FEC encode: %v", p, err)
		}
		outFEC, err := decFEC.DecodeMulti(pktFEC, nFrames)
		if err != nil {
			t.Fatalf("packet %d FEC normal decode: %v", p, err)
		}
		outNo, err := decNo.DecodeMulti(pktNo, nFrames)
		if err != nil {
			t.Fatalf("packet %d no-FEC normal decode: %v", p, err)
		}
		if len(outFEC) != len(outNo) {
			t.Fatalf("packet %d decoded lengths differ: FEC=%d no-FEC=%d", p, len(outFEC), len(outNo))
		}
		for i := range outFEC {
			if outFEC[i] != outNo[i] {
				t.Fatalf("packet %d sample %d differs: FEC=%g no-FEC=%g", p, i, outFEC[i], outNo[i])
			}
		}
	}
}
