//go:build opusref

package opus

import (
	"math"
	"testing"

	"github.com/darui3018823/opus/internal/cgoref"
)

// TestOpusSILKStereoABAgainstLibopusEncoder scores our stereo SILK encoder
// against libopus on the same stereo fixtures used by the self-decode baseline.
// Like the mono AB harness it decodes both our and libopus' packets through our
// own decoder (a fair signal-domain comparison) and reports per-channel SNR,
// the stereo image (L/R correlation, side/mid energy) and the byte ratio at a
// bitrate-matched operating point. It only hard-fails on non-finite metrics or
// runaway output; the logged scoreboard drives the individual quality fixes.
func TestOpusSILKStereoABAgainstLibopusEncoder(t *testing.T) {
	t.Logf("libopus version: %s", cgoref.Version())

	const bitrate = 32000
	for _, rate := range []int{16000, 48000} {
		rate := rate
		t.Run(rateName(rate), func(t *testing.T) {
			frameSize := rate * 20 / 1000
			for _, sig := range opusSILKStereoQualitySignals() {
				sig := sig
				t.Run(sig.name, func(t *testing.T) {
					a := newOwnABVoiceEncoder(t, rate, 2, bitrate)
					b := newRefABVoiceEncoder(t, rate, 2, bitrate)
					defer b.Close()

					in, outA, bytesA, cfgA := encodeDecodeOwnAB(t, a, rate, 2, sig.gen)
					_, outB, bytesB, cfgB := encodeDecodeRefAB(t, b, in, rate, 2, frameSize)

					matchedBitrate := matchedBitrateFor(bytesA, rate, frameSize)
					matched := newRefABMatchedEncoder(t, rate, 2, matchedBitrate)
					defer matched.Close()
					_, outM, bytesM, cfgM := encodeDecodeRefAB(t, matched, in, rate, 2, frameSize)

					la, ra := stereoChannelSNR(in, outA, frameSize)
					lb, rb := stereoChannelSNR(in, outB, frameSize)
					lm, rm := stereoChannelSNR(in, outM, frameSize)
					corrA := outStereoCorr(outA)
					corrB := outStereoCorr(outB)
					_, peakA, clipsA := opusSILKQualityStats(outA)
					_, peakB, clipsB := opusSILKQualityStats(outB)
					_, peakM, clipsM := opusSILKQualityStats(outM)

					ratioBytes := float64(bytesA) / float64(bytesB)
					ratioBytesMatched := float64(bytesA) / float64(bytesM)
					gapMatchedL := lm - la
					gapMatchedR := rm - ra

					t.Logf("%s/%s: own cfg=%d bytes=%d L/R SNR=%.2f/%.2fdB corr=%.3f peak=%.4f clips=%d; libopus cfg=%d bytes=%d L/R SNR=%.2f/%.2fdB corr=%.3f peak=%.4f clips=%d; matched bitrate=%d cfg=%d bytes=%d L/R SNR=%.2f/%.2fdB peak=%.4f clips=%d; gap_SNR_matched=L%.2f/R%.2fdB ratio_bytes=%.3f ratio_bytes_matched=%.3f",
						rateName(rate), sig.name,
						cfgA, bytesA, la, ra, corrA, peakA, clipsA,
						cfgB, bytesB, lb, rb, corrB, peakB, clipsB,
						matchedBitrate, cfgM, bytesM, lm, rm, peakM, clipsM,
						gapMatchedL, gapMatchedR, ratioBytes, ratioBytesMatched)

					assertABFinite(t, sig.name, map[string]float64{
						"own L SNR": la, "own R SNR": ra,
						"libopus L SNR": lb, "libopus R SNR": rb,
						"matched L SNR": lm, "matched R SNR": rm,
						"own corr": corrA, "libopus corr": corrB,
						"ratio_bytes": ratioBytes, "ratio_bytes_matched": ratioBytesMatched,
						"own peak": peakA, "libopus peak": peakB, "matched peak": peakM,
					})
					assertABPeak(t, sig.name, peakA, peakB, peakM)
				})
			}
		})
	}
}

// TestOpusSILKHybridABAgainstLibopusEncoder scores our hybrid (SILK+CELT)
// encoder against libopus. Hybrid is reached at >40 kbps voice on SWB/FB input,
// so this runs at 24 kHz (SWB) and 48 kHz (FB), mono, 64 kbps. Both encoders'
// packets are decoded through our decoder and compared by aligned SNR and bytes.
func TestOpusSILKHybridABAgainstLibopusEncoder(t *testing.T) {
	t.Logf("libopus version: %s", cgoref.Version())

	const bitrate = 64000
	for _, rate := range []int{24000, 48000} {
		rate := rate
		t.Run(rateName(rate), func(t *testing.T) {
			frameSize := rate * 20 / 1000
			for _, sig := range opusSILKQualitySignals() {
				sig := sig
				t.Run(sig.name, func(t *testing.T) {
					a := newOwnABVoiceEncoder(t, rate, 1, bitrate)
					b := newRefABVoiceEncoder(t, rate, 1, bitrate)
					defer b.Close()

					in, outA, bytesA, cfgA := encodeDecodeOwnAB(t, a, rate, 1, sig.gen)
					_, outB, bytesB, cfgB := encodeDecodeRefAB(t, b, in, rate, 1, frameSize)

					matchedBitrate := matchedBitrateFor(bytesA, rate, frameSize)
					matched := newRefABMatchedEncoder(t, rate, 1, matchedBitrate)
					defer matched.Close()
					_, outM, bytesM, cfgM := encodeDecodeRefAB(t, matched, in, rate, 1, frameSize)

					snrA, _, _, scaleA := opusSILKABAlignedSNR(in, outA, frameSize)
					snrB, _, _, scaleB := opusSILKABAlignedSNR(in, outB, frameSize)
					snrM, _, _, scaleM := opusSILKABAlignedSNR(in, outM, frameSize)
					_, peakA, clipsA := opusSILKQualityStats(outA)
					_, peakB, clipsB := opusSILKQualityStats(outB)
					_, peakM, clipsM := opusSILKQualityStats(outM)

					gapMatched := snrM - snrA
					ratioBytes := float64(bytesA) / float64(bytesB)
					ratioBytesMatched := float64(bytesA) / float64(bytesM)

					t.Logf("%s/%s: own cfg=%d bytes=%d SNR=%.2fdB scale=%.4f peak=%.4f clips=%d; libopus cfg=%d bytes=%d SNR=%.2fdB scale=%.4f peak=%.4f clips=%d; matched bitrate=%d cfg=%d bytes=%d SNR=%.2fdB scale=%.4f peak=%.4f clips=%d; gap_SNR_matched=%.2fdB ratio_bytes=%.3f ratio_bytes_matched=%.3f",
						rateName(rate), sig.name,
						cfgA, bytesA, snrA, scaleA, peakA, clipsA,
						cfgB, bytesB, snrB, scaleB, peakB, clipsB,
						matchedBitrate, cfgM, bytesM, snrM, scaleM, peakM, clipsM,
						gapMatched, ratioBytes, ratioBytesMatched)

					if sig.name != "silence" && cfgA < 12 {
						t.Errorf("%s: own TOC config=%d, expected hybrid (12-15)", sig.name, cfgA)
					}

					assertABFinite(t, sig.name, map[string]float64{
						"own SNR": snrA, "libopus SNR": snrB, "matched SNR": snrM,
						"gap_SNR_matched": gapMatched,
						"ratio_bytes":     ratioBytes, "ratio_bytes_matched": ratioBytesMatched,
						"own peak":        peakA, "libopus peak": peakB, "matched peak": peakM,
					})
					assertABPeak(t, sig.name, peakA, peakB, peakM)
				})
			}
		})
	}
}

func newOwnABVoiceEncoder(t *testing.T, rate, channels, bitrate int) *Encoder {
	t.Helper()
	enc, err := NewEncoder(rate, channels, ApplicationVOIP)
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	if err := enc.SetBitrate(bitrate); err != nil {
		t.Fatalf("SetBitrate: %v", err)
	}
	if err := enc.SetComplexity(5); err != nil {
		t.Fatalf("SetComplexity: %v", err)
	}
	// libopus runs constrained VBR by default, so match it for a fair byte
	// comparison (and to exercise the hybrid VBR target).
	enc.SetVBR(true)
	enc.SetSignalType(SignalVoice)
	return enc
}

func newRefABVoiceEncoder(t *testing.T, rate, channels, bitrate int) *cgoref.Encoder {
	t.Helper()
	enc, err := cgoref.NewEncoder(rate, channels, ApplicationVOIP)
	if err != nil {
		t.Fatalf("cgoref.NewEncoder: %v", err)
	}
	if err := enc.SetBitrate(bitrate); err != nil {
		t.Fatalf("cgoref.SetBitrate: %v", err)
	}
	if err := enc.SetComplexity(5); err != nil {
		t.Fatalf("cgoref.SetComplexity: %v", err)
	}
	if err := enc.SetVoiceMode(); err != nil {
		t.Fatalf("cgoref.SetVoiceMode: %v", err)
	}
	return enc
}

func newRefABMatchedEncoder(t *testing.T, rate, channels, bitrate int) *cgoref.Encoder {
	t.Helper()
	return newRefABVoiceEncoder(t, rate, channels, bitrate)
}

func encodeDecodeOwnAB(t *testing.T, enc *Encoder, rate, channels int, gen func(rate, start, n int) []float64) (in, out []float64, totalBytes, firstConfig int) {
	t.Helper()
	frameSize := rate * 20 / 1000
	dec, err := NewDecoder(rate, channels)
	if err != nil {
		t.Fatalf("NewDecoder own: %v", err)
	}
	for frame := 0; frame < opusSILKQualityFrames; frame++ {
		pcm := gen(rate, frame*frameSize, frameSize)
		pkt, err := enc.EncodeFloat(pcm, frameSize)
		if err != nil {
			t.Fatalf("frame %d: own EncodeFloat: %v", frame, err)
		}
		if len(pkt) == 0 {
			t.Fatalf("frame %d: own empty packet", frame)
		}
		if frame == 0 {
			firstConfig = int((pkt[0] >> 3) & 0x1f)
		}
		decoded, err := dec.DecodeFloat(pkt)
		if err != nil {
			t.Fatalf("frame %d: own DecodeFloat: %v", frame, err)
		}
		if len(decoded) != frameSize*channels {
			t.Fatalf("frame %d: own decoded=%d want %d", frame, len(decoded), frameSize*channels)
		}
		totalBytes += len(pkt)
		in = append(in, pcm...)
		out = append(out, decoded...)
	}
	return in, out, totalBytes, firstConfig
}

func encodeDecodeRefAB(t *testing.T, enc *cgoref.Encoder, in []float64, rate, channels, frameSize int) (_, out []float64, totalBytes, firstConfig int) {
	t.Helper()
	dec, err := NewDecoder(rate, channels)
	if err != nil {
		t.Fatalf("NewDecoder ref: %v", err)
	}
	stride := frameSize * channels
	for frame := 0; frame < opusSILKQualityFrames; frame++ {
		pcm := in[frame*stride : (frame+1)*stride]
		pkt, err := enc.Encode(float64ToFloat32(pcm), frameSize)
		if err != nil {
			t.Fatalf("frame %d: libopus Encode: %v", frame, err)
		}
		if len(pkt) == 0 {
			t.Fatalf("frame %d: libopus empty packet", frame)
		}
		if frame == 0 {
			firstConfig = int((pkt[0] >> 3) & 0x1f)
		}
		decoded, err := dec.DecodeFloat(pkt)
		if err != nil {
			t.Fatalf("frame %d: libopus DecodeFloat: %v", frame, err)
		}
		if len(decoded) != frameSize*channels {
			t.Fatalf("frame %d: libopus decoded=%d want %d", frame, len(decoded), frameSize*channels)
		}
		totalBytes += len(pkt)
		out = append(out, decoded...)
	}
	return nil, out, totalBytes, firstConfig
}

func matchedBitrateFor(bytesA, rate, frameSize int) int {
	return int(math.Round(float64(bytesA*8*rate) / float64(opusSILKQualityFrames*frameSize)))
}

func stereoChannelSNR(in, out []float64, frameSize int) (left, right float64) {
	inL, inR := opusSILKSplitStereo(in)
	outL, outR := opusSILKSplitStereo(out)
	left, _, _, _ = opusSILKABAlignedSNR(inL, outL, frameSize)
	right, _, _, _ = opusSILKABAlignedSNR(inR, outR, frameSize)
	return left, right
}

func outStereoCorr(out []float64) float64 {
	l, r := opusSILKSplitStereo(out)
	return opusSILKCorrelation(l, r)
}

func assertABFinite(t *testing.T, name string, values map[string]float64) {
	t.Helper()
	for k, v := range values {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			t.Fatalf("%s: non-finite %s: %g", name, k, v)
		}
	}
}

func assertABPeak(t *testing.T, name string, peaks ...float64) {
	t.Helper()
	for _, p := range peaks {
		if p > 1.25 {
			t.Fatalf("%s: decoded peak too large: %.4f", name, p)
		}
	}
}
