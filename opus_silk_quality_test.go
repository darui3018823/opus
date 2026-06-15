package opus

import (
	"math"
	"math/rand"
	"testing"
)

const opusSILKQualityFrames = 12

type opusSILKQualitySignal struct {
	name          string
	gen           func(rate, start, n int) []float64
	silent        bool
	pitchTracked  bool
	minSNR        float64
	maxSilenceRMS float64
}

type opusSILKQualityMetrics struct {
	avgPacketBytes float64
	outRMS         float64
	outPeak        float64
	clipCount      int
	alignedSNR     float64
	alignedRMSE    float64
	delay          int
	scale          float64
	pitchMean      float64
	pitchMaxDelta  float64
}

func TestEncoderSILKOnlyQualityBaseline(t *testing.T) {
	for _, rate := range []int{8000, 12000, 16000} {
		rate := rate
		t.Run(rateName(rate), func(t *testing.T) {
			frameSize := rate * 20 / 1000
			for _, sig := range opusSILKQualitySignals() {
				sig := sig
				t.Run(sig.name, func(t *testing.T) {
					enc, err := NewEncoder(rate, 1, ApplicationVOIP)
					if err != nil {
						t.Fatalf("NewEncoder: %v", err)
					}
					if err := enc.SetBitrate(24000); err != nil {
						t.Fatalf("SetBitrate: %v", err)
					}
					dec, err := NewDecoder(rate, 1)
					if err != nil {
						t.Fatalf("NewDecoder: %v", err)
					}

					var in, out []float64
					var totalPacketBytes int
					for frame := 0; frame < opusSILKQualityFrames; frame++ {
						pcm := sig.gen(rate, frame*frameSize, frameSize)
						pkt, err := enc.EncodeFloat(pcm, frameSize)
						if err != nil {
							t.Fatalf("frame %d: EncodeFloat: %v", frame, err)
						}
						if len(pkt) < 2 {
							t.Fatalf("frame %d: packet too short: %d bytes", frame, len(pkt))
						}
						if config := int(pkt[0] >> 3); config >= 12 {
							t.Fatalf("frame %d: TOC config=%d, want SILK-only", frame, config)
						}
						if code := int(pkt[0] & 0x03); code != 0 {
							t.Fatalf("frame %d: count code=%d, want 0 for 20ms SILK packet", frame, code)
						}
						decoded, err := dec.DecodeFloat(pkt)
						if err != nil {
							t.Fatalf("frame %d: DecodeFloat: %v", frame, err)
						}
						if len(decoded) != frameSize {
							t.Fatalf("frame %d: decoded samples=%d, want %d", frame, len(decoded), frameSize)
						}
						if got := dec.GetLastPacketDuration(); got != frameSize {
							t.Fatalf("frame %d: last packet duration=%d, want %d", frame, got, frameSize)
						}
						totalPacketBytes += len(pkt)
						in = append(in, pcm...)
						out = append(out, decoded...)
					}

					m := measureOpusSILKQuality(in, out, frameSize, totalPacketBytes, sig.pitchTracked)
					t.Logf("%s/%s: packet=%.1fB outRMS=%.5f peak=%.4f clips=%d alignedSNR=%.2fdB rmse=%.5f delay=%d scale=%.4f pitchMean=%.1f pitchMaxDelta=%.1f",
						rateName(rate), sig.name, m.avgPacketBytes, m.outRMS, m.outPeak, m.clipCount, m.alignedSNR, m.alignedRMSE, m.delay, m.scale, m.pitchMean, m.pitchMaxDelta)
					assertOpusSILKQuality(t, sig, m)
				})
			}
		})
	}
}

func opusSILKQualitySignals() []opusSILKQualitySignal {
	return []opusSILKQualitySignal{
		{
			name:          "silence",
			silent:        true,
			maxSilenceRMS: 0.015,
			gen: func(rate, start, n int) []float64 {
				return make([]float64, n)
			},
		},
		{
			name:   "unvoiced-noise",
			minSNR: -22,
			gen: func(rate, start, n int) []float64 {
				out := make([]float64, n)
				rng := rand.New(rand.NewSource(0x61515 + int64(start/rate)))
				prev := 0.0
				for i := range out {
					white := rng.Float64()*2 - 1
					out[i] = 0.28*white - 0.18*prev
					prev = white
				}
				return out
			},
		},
		{
			name:         "steady-voiced",
			pitchTracked: true,
			minSNR:       -20,
			gen: func(rate, start, n int) []float64 {
				return opusSILKHarmonicFrame(rate, start, n, 180, 0.20)
			},
		},
		{
			name:   "speech-like-harmonic",
			minSNR: -20,
			gen: func(rate, start, n int) []float64 {
				out := make([]float64, n)
				for i := range out {
					tm := float64(start+i) / float64(rate)
					f0 := 145 + 24*math.Sin(2*math.Pi*1.7*tm)
					env := 0.18 + 0.10*math.Sin(2*math.Pi*3.1*tm+0.2)
					out[i] = env * (0.58*math.Sin(2*math.Pi*f0*tm) +
						0.24*math.Sin(2*math.Pi*2*f0*tm+0.35) +
						0.11*math.Sin(2*math.Pi*3*f0*tm+0.85))
				}
				return out
			},
		},
		{
			name:   "onset",
			minSNR: -24,
			gen: func(rate, start, n int) []float64 {
				out := opusSILKHarmonicFrame(rate, start, n, 220, 0.20)
				for i := range out {
					global := start + i
					switch {
					case global < 2*n:
						out[i] = 0
					case global < 3*n:
						out[i] *= float64(global-2*n) / float64(n)
					}
				}
				return out
			},
		},
	}
}

func opusSILKHarmonicFrame(rate, start, n int, f0, amp float64) []float64 {
	out := make([]float64, n)
	for i := range out {
		tm := float64(start+i) / float64(rate)
		out[i] = amp * (0.72*math.Sin(2*math.Pi*f0*tm) +
			0.22*math.Sin(2*math.Pi*2*f0*tm+0.3) +
			0.09*math.Sin(2*math.Pi*3*f0*tm+0.7))
	}
	return out
}

func measureOpusSILKQuality(in, out []float64, frameSize, totalPacketBytes int, pitchTracked bool) opusSILKQualityMetrics {
	m := opusSILKQualityMetrics{
		avgPacketBytes: float64(totalPacketBytes) / opusSILKQualityFrames,
	}
	m.outRMS, m.outPeak, m.clipCount = opusSILKQualityStats(out)
	m.alignedSNR, m.alignedRMSE, m.delay, m.scale = opusSILKAlignedSNR(in, out, frameSize)
	if pitchTracked {
		m.pitchMean, m.pitchMaxDelta = opusSILKPitchContinuity(out, frameSize, rateIndependentMinLag(frameSize), rateIndependentMaxLag(frameSize))
	}
	return m
}

func assertOpusSILKQuality(t *testing.T, sig opusSILKQualitySignal, m opusSILKQualityMetrics) {
	t.Helper()
	if math.IsNaN(m.outRMS) || math.IsInf(m.outRMS, 0) || math.IsNaN(m.outPeak) || math.IsInf(m.outPeak, 0) {
		t.Fatalf("%s: non-finite output metrics: rms=%g peak=%g", sig.name, m.outRMS, m.outPeak)
	}
	if m.outPeak > 1.25 {
		t.Fatalf("%s: decoded peak %.4f indicates runaway synthesis", sig.name, m.outPeak)
	}
	if sig.silent {
		if m.outRMS > sig.maxSilenceRMS {
			t.Fatalf("%s: decoded silence RMS %.5f above %.5f", sig.name, m.outRMS, sig.maxSilenceRMS)
		}
		return
	}
	if m.outRMS < 0.002 {
		t.Fatalf("%s: decoded output collapsed: RMS %.6f", sig.name, m.outRMS)
	}
	if m.outRMS > 0.9 {
		t.Fatalf("%s: decoded output energy exploded: RMS %.6f", sig.name, m.outRMS)
	}
	if m.alignedSNR < sig.minSNR {
		t.Fatalf("%s: aligned SNR %.2fdB below %.2fdB", sig.name, m.alignedSNR, sig.minSNR)
	}
	if sig.pitchTracked {
		if m.pitchMean <= 0 {
			t.Fatalf("%s: pitch tracker found no stable pitch in decoded output", sig.name)
		}
		if m.pitchMaxDelta > 70 {
			t.Fatalf("%s: decoded pitch lag jumps too far: max delta %.1f samples", sig.name, m.pitchMaxDelta)
		}
	}
}

func opusSILKQualityStats(x []float64) (rms, peak float64, clipCount int) {
	for _, v := range x {
		rms += v * v
		a := math.Abs(v)
		if a > peak {
			peak = a
		}
		if a >= 1.0 {
			clipCount++
		}
	}
	if len(x) > 0 {
		rms = math.Sqrt(rms / float64(len(x)))
	}
	return rms, peak, clipCount
}

func opusSILKAlignedSNR(ref, out []float64, maxDelay int) (snr, rmse float64, delay int, scale float64) {
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
		if outEnergy <= 0 {
			continue
		}
		sc := dot / outEnergy

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
	if bestRMSE == 0 {
		return math.Inf(1), 0, bestDelay, bestScale
	}
	if math.IsInf(bestRMSE, 1) || bestRefRMS == 0 {
		return -math.Inf(1), bestRMSE, bestDelay, bestScale
	}
	return 20 * math.Log10(bestRefRMS/bestRMSE), bestRMSE, bestDelay, bestScale
}

func opusSILKPitchContinuity(x []float64, frameSize, minLag, maxLag int) (mean, maxDelta float64) {
	var lags []float64
	for start := 0; start+frameSize <= len(x); start += frameSize {
		lag, corr := opusSILKBestPitchLag(x[start:start+frameSize], minLag, maxLag)
		if corr >= 0.25 {
			lags = append(lags, float64(lag))
		}
	}
	if len(lags) == 0 {
		return 0, 0
	}
	for i, lag := range lags {
		mean += lag
		if i > 0 {
			d := math.Abs(lag - lags[i-1])
			if d > maxDelta {
				maxDelta = d
			}
		}
	}
	mean /= float64(len(lags))
	return mean, maxDelta
}

func opusSILKBestPitchLag(x []float64, minLag, maxLag int) (lag int, corr float64) {
	bestLag, bestCorr := minLag, 0.0
	for l := minLag; l <= maxLag && l < len(x); l++ {
		var xy, x2, y2 float64
		for i := l; i < len(x); i++ {
			a := x[i]
			b := x[i-l]
			xy += a * b
			x2 += a * a
			y2 += b * b
		}
		if x2 <= 0 || y2 <= 0 {
			continue
		}
		c := xy / math.Sqrt(x2*y2)
		if c > bestCorr {
			bestCorr = c
			bestLag = l
		}
	}
	return bestLag, bestCorr
}

func rateIndependentMinLag(frameSize int) int {
	// Frame size is exactly 20 ms, so these constants map to about 400 Hz and 80 Hz.
	minLag := frameSize / 8
	if minLag < 1 {
		return 1
	}
	return minLag
}

func rateIndependentMaxLag(frameSize int) int {
	return frameSize / 2
}
