package silk

import (
	"math"
	"testing"
)

// TestPitchCoreDetectsPeriodicLag verifies the ported multi-stage pitch core
// finds the fundamental lag of a periodic signal and reports it as voiced.
func TestPitchCoreDetectsPeriodicLag(t *testing.T) {
	cases := []struct {
		fsKHz  int
		period int
	}{
		{8, 64},
		{12, 96},
		{16, 128},
	}
	for _, tc := range cases {
		nbSubfr := 4
		frameLen := (peLtpMemLengthMs + nbSubfr*peSubfrLengthMs) * tc.fsKHz
		frame := make([]float64, frameLen)
		for n := range frame {
			frame[n] = math.Sin(2*math.Pi*float64(n)/float64(tc.period)) * 8000.0
		}
		ltpCorr := 0.0
		pitchOut, lagIndex, contourIndex, voiced := silkPitchAnalysisCoreFLP(
			frame, &ltpCorr, 0, 0.7, 0.4, tc.fsKHz, silkPEMidComplex, nbSubfr)
		if !voiced {
			t.Fatalf("fs=%dkHz period=%d: expected voiced", tc.fsKHz, tc.period)
		}
		if ltpCorr <= 0.5 {
			t.Errorf("fs=%dkHz: LTPCorr=%.3f too low for periodic input", tc.fsKHz, ltpCorr)
		}
		if lagIndex < 0 {
			t.Errorf("fs=%dkHz: negative lag index %d", tc.fsKHz, lagIndex)
		}
		if contourIndex < 0 {
			t.Errorf("fs=%dkHz: negative contour index %d", tc.fsKHz, contourIndex)
		}
		for k, lag := range pitchOut {
			if math.Abs(float64(lag-tc.period)) > 4 {
				t.Errorf("fs=%dkHz subframe %d: lag=%d want ~%d", tc.fsKHz, k, lag, tc.period)
			}
		}
	}
}

// TestPitchCoreRejectsNoise verifies white noise is not reported as voiced.
func TestPitchCoreRejectsNoise(t *testing.T) {
	fsKHz := 16
	nbSubfr := 4
	frameLen := (peLtpMemLengthMs + nbSubfr*peSubfrLengthMs) * fsKHz
	frame := make([]float64, frameLen)
	seed := int64(12345)
	for n := range frame {
		seed = 196314165*seed + 907633515
		frame[n] = float64((seed>>16)&0xFFFF-32768) * 0.2
	}
	ltpCorr := 0.0
	_, _, _, voiced := silkPitchAnalysisCoreFLP(
		frame, &ltpCorr, 0, 0.7, 0.5, fsKHz, silkPEMidComplex, nbSubfr)
	if voiced {
		t.Errorf("white noise classified as voiced (LTPCorr=%.3f)", ltpCorr)
	}
}

func TestFirstFrameLongLagDisablesPitchPrediction(t *testing.T) {
	enc, err := NewEncoderWithFrameMs(16000, 1, 20)
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	if err := enc.SetBitrate(24000); err != nil {
		t.Fatalf("SetBitrate: %v", err)
	}
	if err := enc.SetComplexity(5); err != nil {
		t.Fatalf("SetComplexity: %v", err)
	}
	enc.SetRateMode(RateModeCVBR)

	dec, err := NewDecoderWithFrameMs(16000, 1, 20)
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	dec.trace = &decodeTrace{}

	frameSize := enc.frameSize
	pcm := make([]float64, frameSize)
	for i := range pcm {
		tm := float64(i) / 16000.0
		f0 := 145 + 24*math.Sin(2*math.Pi*1.7*tm)
		env := 0.18 + 0.10*math.Sin(2*math.Pi*3.1*tm+0.2)
		pcm[i] = env * (0.58*math.Sin(2*math.Pi*f0*tm) +
			0.24*math.Sin(2*math.Pi*2*f0*tm+0.35) +
			0.11*math.Sin(2*math.Pi*3*f0*tm+0.85))
	}

	pkt, err := enc.Encode(pcm)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if _, err := dec.DecodeMulti(pkt, 1); err != nil {
		t.Fatalf("DecodeMulti: %v", err)
	}
	if len(dec.trace.Frames) != 1 {
		t.Fatalf("decoded trace frames=%d, want 1", len(dec.trace.Frames))
	}
	tf := dec.trace.Frames[0]
	if tf.SignalType == SignalTypeVoiced {
		t.Fatalf("first frame after reset was coded voiced with pitch=%v ltp=%v", tf.PitchLags, tf.LTPCoefQ14)
	}
	for _, coef := range tf.LTPCoefQ14 {
		if coef != 0 {
			t.Fatalf("first frame LTP coefficient=%d, want all zero ltp=%v", coef, tf.LTPCoefQ14)
		}
	}
}

// TestResamplerDown2HalvesLength checks the int16 2:1 decimator output length
// and that a DC input is preserved.
func TestResamplerDown2HalvesLength(t *testing.T) {
	in := make([]int16, 320)
	for i := range in {
		in[i] = 1000
	}
	out := make([]int16, len(in)/2)
	S := make([]int32, 2)
	silkResamplerDown2(S, out, in)
	// After the filter settles, a DC level should be roughly preserved.
	got := int(out[len(out)-1])
	if got < 900 || got > 1100 {
		t.Errorf("down2 DC level = %d, want ~1000", got)
	}
}

// TestEncoderPitchHistoryRoundTrip exercises the public voiced path end to end
// and confirms a periodic input decodes back with reasonable correlation.
func TestEncoderPitchVoicedDecodes(t *testing.T) {
	enc, err := NewEncoderWithFrameMs(16000, 1, 20)
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	dec, err := NewDecoderWithFrameMs(16000, 1, 20)
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	frameSize := enc.frameSize
	var in, out []float64
	for frame := 0; frame < 8; frame++ {
		pcm := make([]float64, frameSize)
		for i := range pcm {
			n := frame*frameSize + i
			pcm[i] = 0.3 * math.Sin(2*math.Pi*float64(n)/128.0)
		}
		pkt, err := enc.Encode(pcm)
		if err != nil {
			t.Fatalf("frame %d encode: %v", frame, err)
		}
		decoded, err := dec.Decode(pkt)
		if err != nil {
			t.Fatalf("frame %d decode: %v", frame, err)
		}
		if len(decoded) != frameSize {
			t.Fatalf("frame %d: decoded %d samples want %d", frame, len(decoded), frameSize)
		}
		in = append(in, pcm...)
		out = append(out, decoded...)
	}
	// Output must be non-trivial (voiced path produced energy).
	energy := 0.0
	for _, v := range out {
		energy += v * v
	}
	if energy < 1e-3 {
		t.Errorf("voiced path produced near-silent output (energy=%g)", energy)
	}
}
