//go:build opusref

package opus

import (
	"math"
	"testing"

	framing "github.com/darui3018823/opus/internal"
	"github.com/darui3018823/opus/internal/cgoref"
)

func TestOpusSILKABAgainstLibopusEncoder(t *testing.T) {
	t.Logf("libopus version: %s", cgoref.Version())

	for _, rate := range []int{8000, 12000, 16000} {
		rate := rate
		t.Run(rateName(rate), func(t *testing.T) {
			frameSize := rate * 20 / 1000
			for _, sig := range opusSILKQualitySignals() {
				sig := sig
				t.Run(sig.name, func(t *testing.T) {
					a, err := NewEncoder(rate, 1, ApplicationVOIP)
					if err != nil {
						t.Fatalf("NewEncoder: %v", err)
					}
					if err := a.SetBitrate(24000); err != nil {
						t.Fatalf("SetBitrate: %v", err)
					}
					if err := a.SetComplexity(5); err != nil {
						t.Fatalf("SetComplexity: %v", err)
					}
					a.SetVBR(true)
					a.SetVBRConstraint(true)
					a.SetSignalType(SignalVoice)

					b, err := cgoref.NewEncoder(rate, 1, ApplicationVOIP)
					if err != nil {
						t.Fatalf("cgoref.NewEncoder: %v", err)
					}
					defer b.Close()
					if err := b.SetBitrate(24000); err != nil {
						t.Fatalf("cgoref.SetBitrate: %v", err)
					}
					if err := b.SetComplexity(5); err != nil {
						t.Fatalf("cgoref.SetComplexity: %v", err)
					}
					setCGORefCVBR(t, b)
					if err := b.SetVoiceMode(); err != nil {
						t.Fatalf("cgoref.SetVoiceMode: %v", err)
					}

					decA, err := NewDecoder(rate, 1)
					if err != nil {
						t.Fatalf("NewDecoder A: %v", err)
					}
					decB, err := NewDecoder(rate, 1)
					if err != nil {
						t.Fatalf("NewDecoder B: %v", err)
					}

					var in, outA, outB []float64
					var bytesA, bytesB int
					var firstConfigA, firstConfigB int
					for frame := 0; frame < opusSILKQualityFrames; frame++ {
						pcm := sig.gen(rate, frame*frameSize, frameSize)

						pktA, err := a.EncodeFloat(pcm, frameSize)
						if err != nil {
							t.Fatalf("frame %d: own EncodeFloat: %v", frame, err)
						}
						pktB, err := b.Encode(float64ToFloat32(pcm), frameSize)
						if err != nil {
							t.Fatalf("frame %d: libopus Encode: %v", frame, err)
						}
						if len(pktA) == 0 || len(pktB) == 0 {
							t.Fatalf("frame %d: empty packet: own=%d libopus=%d", frame, len(pktA), len(pktB))
						}
						if frame == 0 {
							firstConfigA = int((pktA[0] >> 3) & 0x1f)
							firstConfigB = int((pktB[0] >> 3) & 0x1f)
						}

						decodedA, err := decA.DecodeFloat(pktA)
						if err != nil {
							t.Fatalf("frame %d: own packet DecodeFloat: %v", frame, err)
						}
						decodedB, err := decB.DecodeFloat(pktB)
						if err != nil {
							t.Fatalf("frame %d: libopus packet DecodeFloat: %v", frame, err)
						}
						if len(decodedA) != frameSize || len(decodedB) != frameSize {
							t.Fatalf("frame %d: decoded samples own=%d libopus=%d want %d", frame, len(decodedA), len(decodedB), frameSize)
						}

						bytesA += len(pktA)
						bytesB += len(pktB)
						in = append(in, pcm...)
						outA = append(outA, decodedA...)
						outB = append(outB, decodedB...)
					}

					snrA, rmseA, delayA, scaleA := opusSILKABAlignedSNR(in, outA, frameSize)
					snrB, rmseB, delayB, scaleB := opusSILKABAlignedSNR(in, outB, frameSize)
					gapSNR := snrB - snrA
					ratioBytes := float64(bytesA) / float64(bytesB)

					matchedBitrate := int(math.Round(float64(bytesA*8*rate) / float64(opusSILKQualityFrames*frameSize)))
					matched, err := cgoref.NewEncoder(rate, 1, ApplicationVOIP)
					if err != nil {
						t.Fatalf("cgoref.NewEncoder matched: %v", err)
					}
					defer matched.Close()
					if err := matched.SetBitrate(matchedBitrate); err != nil {
						t.Fatalf("cgoref.SetBitrate matched: %v", err)
					}
					if err := matched.SetComplexity(5); err != nil {
						t.Fatalf("cgoref.SetComplexity matched: %v", err)
					}
					setCGORefCVBR(t, matched)
					if err := matched.SetVoiceMode(); err != nil {
						t.Fatalf("cgoref.SetVoiceMode matched: %v", err)
					}
					matchedBandwidth := opusSILKABBandwidthForConfig(t, firstConfigA)
					if err := matched.SetBandwidth(matchedBandwidth); err != nil {
						t.Fatalf("cgoref.SetBandwidth matched: %v", err)
					}
					decMatched, err := NewDecoder(rate, 1)
					if err != nil {
						t.Fatalf("NewDecoder matched: %v", err)
					}

					var outMatched []float64
					var bytesMatched int
					var firstConfigMatched int
					for frame := 0; frame < opusSILKQualityFrames; frame++ {
						pcm := in[frame*frameSize : (frame+1)*frameSize]
						pkt, err := matched.Encode(float64ToFloat32(pcm), frameSize)
						if err != nil {
							t.Fatalf("frame %d: libopus matched Encode: %v", frame, err)
						}
						if len(pkt) == 0 {
							t.Fatalf("frame %d: empty matched packet", frame)
						}
						if frame == 0 {
							firstConfigMatched = int((pkt[0] >> 3) & 0x1f)
						}
						decoded, err := decMatched.DecodeFloat(pkt)
						if err != nil {
							t.Fatalf("frame %d: libopus matched packet DecodeFloat: %v", frame, err)
						}
						if len(decoded) != frameSize {
							t.Fatalf("frame %d: matched decoded samples=%d want %d", frame, len(decoded), frameSize)
						}
						bytesMatched += len(pkt)
						outMatched = append(outMatched, decoded...)
					}
					snrMatched, rmseMatched, delayMatched, scaleMatched := opusSILKABAlignedSNR(in, outMatched, frameSize)
					gapSNRMatched := snrMatched - snrA
					ratioBytesMatched := float64(bytesA) / float64(bytesMatched)
					if sig.name != "silence" {
						matchedPacketBandwidth := opusSILKABBandwidthForConfig(t, firstConfigMatched)
						if matchedPacketBandwidth != matchedBandwidth {
							t.Fatalf("%s: matched libopus bandwidth=%d, want own packet bandwidth=%d",
								sig.name, matchedPacketBandwidth, matchedBandwidth)
						}
					}

					rmsA, peakA, clipsA := opusSILKQualityStats(outA)
					rmsB, peakB, clipsB := opusSILKQualityStats(outB)
					rmsMatched, peakMatched, clipsMatched := opusSILKQualityStats(outMatched)
					t.Logf("%s/%s: own cfg=%d bytes=%d rms=%.5f peak=%.4f clips=%d SNR=%.2fdB rmse=%.5f delay=%d scale=%.4f; libopus cfg=%d bytes=%d rms=%.5f peak=%.4f clips=%d SNR=%.2fdB rmse=%.5f delay=%d scale=%.4f; libopus_matched bitrate=%d bandwidth=%d cfg=%d bytes=%d rms=%.5f peak=%.4f clips=%d SNR=%.2fdB rmse=%.5f delay=%d scale=%.4f; gap_SNR=%.2fdB gap_SNR_matched=%.2fdB ratio_bytes=%.3f ratio_bytes_matched=%.3f",
						rateName(rate), sig.name,
						firstConfigA, bytesA, rmsA, peakA, clipsA, snrA, rmseA, delayA, scaleA,
						firstConfigB, bytesB, rmsB, peakB, clipsB, snrB, rmseB, delayB, scaleB,
						matchedBitrate, matchedBandwidth, firstConfigMatched, bytesMatched, rmsMatched, peakMatched, clipsMatched, snrMatched, rmseMatched, delayMatched, scaleMatched,
						gapSNR, gapSNRMatched, ratioBytes, ratioBytesMatched)

					if sig.name == "speech-like-harmonic" {
						loudnessDiffDB := opusSILKABRMSDiffDB(rmsA, rmsB)
						loudnessDiffMatchedDB := opusSILKABRMSDiffDB(rmsA, rmsMatched)
						t.Logf("%s/%s: RMS loudness own-minus-libopus=%.2fdB, own-minus-libopus-matched=%.2fdB (positive means own is louder)",
							rateName(rate), sig.name, loudnessDiffDB, loudnessDiffMatchedDB)
						if math.Abs(loudnessDiffMatchedDB) > 1.5 {
							t.Fatalf("%s: matched RMS loudness difference %.2fdB outside ±1.5dB", sig.name, loudnessDiffMatchedDB)
						}
					}

					for name, value := range map[string]float64{
						"own SNR":             snrA,
						"libopus SNR":         snrB,
						"matched libopus SNR": snrMatched,
						"gap_SNR":             gapSNR,
						"gap_SNR_matched":     gapSNRMatched,
						"ratio_bytes":         ratioBytes,
						"ratio_bytes_matched": ratioBytesMatched,
						"own peak":            peakA,
						"libopus peak":        peakB,
						"matched peak":        peakMatched,
					} {
						if math.IsNaN(value) || math.IsInf(value, 0) {
							t.Fatalf("%s: non-finite %s: %g", sig.name, name, value)
						}
					}
					if peakA > 1.25 || peakB > 1.25 || peakMatched > 1.25 {
						t.Fatalf("%s: decoded peak too large: own=%.4f libopus=%.4f matched=%.4f", sig.name, peakA, peakB, peakMatched)
					}
				})
			}
		})
	}
}

func setCGORefCVBR(t *testing.T, enc *cgoref.Encoder) {
	t.Helper()
	if err := enc.SetVBR(true); err != nil {
		t.Fatalf("cgoref.SetVBR: %v", err)
	}
	if err := enc.SetVBRConstraint(true); err != nil {
		t.Fatalf("cgoref.SetVBRConstraint: %v", err)
	}
}

func opusSILKABBandwidthForConfig(t *testing.T, config int) int {
	t.Helper()
	mode, bandwidth, _ := framing.ParseTOCConfig(config)
	if mode != framing.ModeSILKOnly {
		t.Fatalf("own TOC config=%d is not SILK-only", config)
	}
	switch bandwidth {
	case framing.BandwidthNarrowband:
		return BandwidthNarrowband
	case framing.BandwidthMediumband:
		return BandwidthMediumband
	case framing.BandwidthWideband:
		return BandwidthWideband
	default:
		t.Fatalf("own SILK TOC config=%d has unsupported bandwidth=%d", config, bandwidth)
		return BandwidthAuto
	}
}

func opusSILKABRMSDiffDB(ownRMS, libopusRMS float64) float64 {
	const eps = 1e-12
	return 20 * math.Log10(math.Max(ownRMS, eps)/math.Max(libopusRMS, eps))
}

func float64ToFloat32(in []float64) []float32 {
	out := make([]float32, len(in))
	for i, v := range in {
		out[i] = float32(v)
	}
	return out
}

func opusSILKABAlignedSNR(ref, out []float64, maxDelay int) (snr, rmse float64, delay int, scale float64) {
	bestRMSE := math.Inf(1)
	bestRefRMS := 0.0
	bestDelay := 0
	bestScale := 1.0
	for d := -maxDelay; d <= maxDelay; d++ {
		start := 0
		if d > 0 {
			start = d
		}
		end := len(ref)
		if len(out)+d < end {
			end = len(out) + d
		}
		if end-start < maxDelay {
			continue
		}

		var dot, outEnergy float64
		for i := start; i < end; i++ {
			b := out[i-d]
			dot += ref[i] * b
			outEnergy += b * b
		}
		sc := 1.0
		if outEnergy > 0 {
			sc = dot / outEnergy
		}

		var err2, ref2 float64
		for i := start; i < end; i++ {
			r := ref[i] - sc*out[i-d]
			err2 += r * r
			ref2 += ref[i] * ref[i]
		}
		thisRMSE := math.Sqrt(err2 / float64(end-start))
		if thisRMSE < bestRMSE {
			bestRMSE = thisRMSE
			bestRefRMS = math.Sqrt(ref2 / float64(end-start))
			bestDelay = d
			bestScale = sc
		}
	}
	const eps = 1e-12
	if math.IsInf(bestRMSE, 1) {
		return math.Inf(-1), bestRMSE, bestDelay, bestScale
	}
	if bestRMSE < eps {
		bestRMSE = eps
	}
	if bestRefRMS < eps {
		bestRefRMS = eps
	}
	return 20 * math.Log10(bestRefRMS/bestRMSE), bestRMSE, bestDelay, bestScale
}
