package opus

import (
	"errors"
	"math"
	"testing"
)

// Default-build guards for behaviors the libopus parity audit fixed. The
// audit harnesses themselves are opt-in (OPUS_LIBOPUS_AUDIT=1 or the opusref
// tag); these checks need neither.

func auditTone(frameSize, channels, rate int, amp float64) []float64 {
	pcm := make([]float64, frameSize*channels)
	for i := 0; i < frameSize; i++ {
		for c := 0; c < channels; c++ {
			pcm[i*channels+c] = amp * math.Sin(2*math.Pi*float64(440+110*c)*float64(i)/float64(rate))
		}
	}
	return pcm
}

func TestRes2Int24MatchesLibopus(t *testing.T) {
	for _, tc := range []struct {
		in   float64
		want int32
	}{
		{0, 0},
		{0.5, 4194304},
		{-0.5, -4194304},
		{1, 8388608},    // not clamped to 8388607
		{1.5, 12582912}, // positive gain may leave the 24-bit range
		{-1.5, -12582912},
		{0.5 / 8388608, 0}, // lrintf rounds ties to even
		{1.5 / 8388608, 2},
		{2.5 / 8388608, 2},
	} {
		if got := res2Int24(tc.in); got != tc.want {
			t.Errorf("res2Int24(%v)=%d want %d", tc.in, got, tc.want)
		}
	}
}

func TestVBRConstraintIsIndependentOfVBR(t *testing.T) {
	enc, err := NewEncoder(48000, 1, ApplicationAudio)
	if err != nil {
		t.Fatal(err)
	}
	if !enc.VBRConstraint() {
		t.Fatal("the VBR constraint defaults to on")
	}
	enc.SetVBR(false)
	enc.SetVBRConstraint(false)
	if enc.VBR() || enc.VBRConstraint() {
		t.Fatalf("CBR with the constraint off: VBR=%v constraint=%v", enc.VBR(), enc.VBRConstraint())
	}
	enc.SetVBR(true)
	if !enc.VBR() || enc.VBRConstraint() {
		t.Fatalf("VBR must come back unconstrained: VBR=%v constraint=%v", enc.VBR(), enc.VBRConstraint())
	}
	enc.SetVBR(false)
	enc.SetVBRConstraint(true)
	enc.SetVBR(true)
	if !enc.VBR() || !enc.VBRConstraint() {
		t.Fatalf("VBR must come back constrained: VBR=%v constraint=%v", enc.VBR(), enc.VBRConstraint())
	}
}

func TestSetInbandFECModeValues(t *testing.T) {
	enc, err := NewEncoder(48000, 1, ApplicationVOIP)
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []int{-1, 3} {
		if err := enc.SetInbandFECMode(mode); !errors.Is(err, ErrBadArg) {
			t.Fatalf("mode %d error=%v", mode, err)
		}
	}
	if err := enc.SetInbandFECMode(InbandFECNoModeSwitch); err != nil {
		t.Fatal(err)
	}
	if enc.InbandFECMode() != InbandFECNoModeSwitch || !enc.InbandFEC() {
		t.Fatalf("mode 2: mode=%d enabled=%v", enc.InbandFECMode(), enc.InbandFEC())
	}
	enc.SetInbandFEC(false)
	if enc.InbandFECMode() != InbandFECOff || enc.InbandFEC() {
		t.Fatalf("SetInbandFEC(false): mode=%d enabled=%v", enc.InbandFECMode(), enc.InbandFEC())
	}
	enc.SetInbandFEC(true)
	if enc.InbandFECMode() != InbandFECOn {
		t.Fatalf("SetInbandFEC(true): mode=%d", enc.InbandFECMode())
	}
}

func TestSetMaxPacketSizeBoundsLibopusPolicyPackets(t *testing.T) {
	const frameSize = 960
	enc, err := NewEncoder(48000, 2, ApplicationAudio)
	if err != nil {
		t.Fatal(err)
	}
	if enc.MaxPacketSize() != defaultOutDataBytes {
		t.Fatalf("default MaxPacketSize=%d", enc.MaxPacketSize())
	}
	for _, n := range []int{0, -5} {
		if err := enc.SetMaxPacketSize(n); !errors.Is(err, ErrBadArg) {
			t.Fatalf("SetMaxPacketSize(%d) error=%v", n, err)
		}
	}
	if err := enc.SetBitrate(256000); err != nil {
		t.Fatal(err)
	}
	enc.SetVBR(true)
	pcm := auditTone(frameSize, 2, 48000, 0.4)
	// A noisy second channel makes the packet large enough to hit the limit.
	for i := 0; i < frameSize; i++ {
		pcm[i*2+1] += 0.2 * math.Sin(float64(i*i%977))
	}
	free, err := enc.EncodeFloat(pcm, frameSize)
	if err != nil {
		t.Fatal(err)
	}
	if len(free) <= 150 {
		t.Skipf("the unbounded packet is only %d bytes", len(free))
	}
	if err := enc.SetMaxPacketSize(150); err != nil {
		t.Fatal(err)
	}
	if enc.MaxPacketSize() != 150 {
		t.Fatalf("MaxPacketSize=%d", enc.MaxPacketSize())
	}
	bounded, err := enc.EncodeFloat(pcm, frameSize)
	if err != nil {
		t.Fatal(err)
	}
	if len(bounded) > 150 {
		t.Fatalf("packet of %d bytes exceeds MaxPacketSize 150", len(bounded))
	}
}

func TestDecode24IsNotClampedWithPositiveGain(t *testing.T) {
	const frameSize = 960
	enc, err := NewEncoder(48000, 1, ApplicationAudio)
	if err != nil {
		t.Fatal(err)
	}
	if err := enc.SetBitrate(128000); err != nil {
		t.Fatal(err)
	}
	pkt, err := enc.EncodeFloat(auditTone(frameSize, 1, 48000, 0.9), frameSize)
	if err != nil {
		t.Fatal(err)
	}
	dec, err := NewDecoder(48000, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := dec.SetGain(3 * 256); err != nil { // +3 dB
		t.Fatal(err)
	}
	pcm := make([]int32, frameSize)
	if _, err := dec.Decode24(pkt, pcm); err != nil {
		t.Fatal(err)
	}
	var peak int32
	for _, v := range pcm {
		peak = max(peak, v, -v)
	}
	if peak <= 8388607 {
		t.Fatalf("peak %d stayed inside the 24-bit range; RES2INT24 does not clamp", peak)
	}
}
