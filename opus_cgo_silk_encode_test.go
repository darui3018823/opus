//go:build opusref

package opus_test

import (
	"math"
	"testing"

	opus "github.com/darui3018823/opus"
	"github.com/darui3018823/opus/internal/cgoref"
)

func TestCGOEncodeRefSILKOnly(t *testing.T) {
	t.Logf("libopus version: %s", cgoref.Version())

	type route struct {
		name   string
		app    opus.Application
		signal opus.SignalType
	}
	routes := []route{
		{name: "voip", app: opus.ApplicationVOIP, signal: opus.SignalAuto},
		{name: "signal-voice", app: opus.ApplicationAudio, signal: opus.SignalVoice},
	}

	cases := []struct {
		rate       int
		channels   int
		configBase int
	}{
		{rate: 8000, channels: 1, configBase: 0},
		{rate: 12000, channels: 1, configBase: 4},
		{rate: 16000, channels: 1, configBase: 8},
		{rate: 48000, channels: 1, configBase: 8},
		{rate: 16000, channels: 2, configBase: 8},
		{rate: 48000, channels: 2, configBase: 8},
	}

	for _, rt := range routes {
		rt := rt
		for _, tc := range cases {
			tc := tc
			for _, packetMs := range []int{20, 40, 60} {
				packetMs := packetMs
				t.Run(rt.name+"/"+silkRefRateName(tc.rate)+"/"+silkRefChannelName(tc.channels)+"/"+silkRefPacketName(packetMs), func(t *testing.T) {
					enc, err := opus.NewEncoder(tc.rate, tc.channels, rt.app)
					if err != nil {
						t.Fatalf("NewEncoder: %v", err)
					}
					if rt.signal != opus.SignalAuto {
						enc.SetSignalType(rt.signal)
					}
					if err := enc.SetBitrate(24000); err != nil {
						t.Fatalf("SetBitrate: %v", err)
					}

					dec, err := opus.NewDecoder(tc.rate, tc.channels)
					if err != nil {
						t.Fatalf("NewDecoder: %v", err)
					}
					ref, err := cgoref.NewDecoder(tc.rate, tc.channels)
					if err != nil {
						t.Fatalf("cgoref.NewDecoder: %v", err)
					}
					defer ref.Close()

					frameSize := tc.rate * packetMs / 1000
					wantCode := 0
					if tc.channels == 2 && packetMs == 60 {
						wantCode = 3
					}
					maxSPC := tc.rate * 120 / 1000
					const nPackets = 10

					var oursAll, refAll []float64
					for p := 0; p < nPackets; p++ {
						in := silkRefSpeechFrame(tc.rate, p*frameSize, frameSize, tc.channels)
						pkt, err := enc.EncodeFloat(in, frameSize)
						if err != nil {
							t.Fatalf("packet %d: EncodeFloat: %v", p, err)
						}
						if len(pkt) < 2 {
							t.Fatalf("packet %d: encoded packet too short: %d bytes", p, len(pkt))
						}

						config := int((pkt[0] >> 3) & 0x1f)
						stereo := (pkt[0] & 0x04) != 0
						code := int(pkt[0] & 0x03)
						wantConfig := tc.configBase + silkRefDurationIndex(packetMs, tc.channels)
						if config != wantConfig {
							t.Fatalf("packet %d: TOC config=%d, want SILK-only %dms config %d (toc=0x%02x)", p, config, packetMs, wantConfig, pkt[0])
						}
						if stereo != (tc.channels == 2) {
							t.Fatalf("packet %d: TOC stereo=%v, want %v (toc=0x%02x)", p, stereo, tc.channels == 2, pkt[0])
						}
						if code != wantCode {
							t.Fatalf("packet %d: count code=%d, want %d for %d ms packet", p, code, wantCode, packetMs)
						}

						ours, err := dec.DecodeFloat(pkt)
						if err != nil {
							t.Fatalf("packet %d: DecodeFloat: %v", p, err)
						}
						refOut, err := ref.DecodeFloat(pkt, maxSPC)
						if err != nil {
							t.Fatalf("packet %d: libopus decode (SILK packet non-conformant): %v", p, err)
						}
						wantSamples := frameSize * tc.channels
						if len(ours) != wantSamples {
							t.Fatalf("packet %d: decoder samples=%d, want %d", p, len(ours), wantSamples)
						}
						if len(refOut) != wantSamples {
							t.Fatalf("packet %d: libopus samples=%d, want %d", p, len(refOut), wantSamples)
						}

						oursAll = append(oursAll, ours...)
						for _, v := range refOut {
							refAll = append(refAll, float64(v))
						}
					}

					oursRMS, oursPeak := silkRefStats(oursAll)
					refRMS, refPeak := silkRefStats(refAll)
					if oursPeak > 1.5 || refPeak > 1.5 {
						t.Fatalf("decoded peak too large: decoder=%g libopus=%g", oursPeak, refPeak)
					}
					if oursRMS < 1e-5 || refRMS < 1e-5 {
						t.Fatalf("decoded output collapsed: decoder RMS=%g libopus RMS=%g", oursRMS, refRMS)
					}
					ratio := oursRMS / refRMS
					if ratio < 0.5 || ratio > 2.0 {
						t.Fatalf("decoder/libopus RMS ratio=%g outside coarse match range (decoder=%g libopus=%g)", ratio, oursRMS, refRMS)
					}

					snr, rmse, delay, scale := silkRefAlignedSNR(refAll, oursAll, tc.rate*tc.channels/100)
					t.Logf("SILK %s %dHz ch=%d %dms: decoder-vs-libopus alignedSNR=%.2fdB rmse=%.5f delay=%d scale=%.4f", rt.name, tc.rate, tc.channels, packetMs, snr, rmse, delay, scale)
					if snr < 10 || rmse > 0.18 {
						t.Fatalf("decoder/libopus output mismatch: alignedSNR=%.2fdB rmse=%.5f delay=%d scale=%.4f", snr, rmse, delay, scale)
					}
				})
			}
		}
	}
}

func silkRefSpeechFrame(rate, start, n, channels int) []float64 {
	out := make([]float64, n*channels)
	for i := 0; i < n; i++ {
		t := float64(start+i) / float64(rate)
		env := 0.55 + 0.35*math.Sin(2*math.Pi*3*t)
		s := 0.32*math.Sin(2*math.Pi*180*t) +
			0.12*math.Sin(2*math.Pi*360*t+0.4) +
			0.06*math.Sin(2*math.Pi*720*t+0.9) +
			0.025*math.Sin(2*math.Pi*1100*t+1.7)
		out[i*channels] = env * s
		if channels == 2 {
			r := 0.30*math.Sin(2*math.Pi*185*t+0.2) +
				0.10*math.Sin(2*math.Pi*370*t+0.7) +
				0.05*math.Sin(2*math.Pi*740*t+1.1)
			out[i*channels+1] = env * r
		}
	}
	return out
}

func silkRefStats(x []float64) (rms, peak float64) {
	for _, v := range x {
		rms += v * v
		if a := math.Abs(v); a > peak {
			peak = a
		}
	}
	if len(x) > 0 {
		rms = math.Sqrt(rms / float64(len(x)))
	}
	return rms, peak
}

func silkRefAlignedSNR(ref, out []float64, maxDelay int) (snr, rmse float64, delay int, scale float64) {
	bestErr := math.Inf(1)
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
		if outEnergy == 0 {
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
		if thisRMSE < bestErr {
			bestErr = thisRMSE
			bestRefRMS = math.Sqrt(ref2 / float64(end-start))
			bestDelay = d
			bestScale = sc
		}
	}
	if bestErr == 0 {
		return math.Inf(1), 0, bestDelay, bestScale
	}
	return 20 * math.Log10(bestRefRMS/bestErr), bestErr, bestDelay, bestScale
}

func silkRefRateName(rate int) string {
	switch rate {
	case 8000:
		return "8k"
	case 12000:
		return "12k"
	case 48000:
		return "48k"
	default:
		return "16k"
	}
}

func silkRefChannelName(channels int) string {
	if channels == 2 {
		return "stereo"
	}
	return "mono"
}

func silkRefPacketName(ms int) string {
	switch ms {
	case 20:
		return "20ms"
	case 40:
		return "40ms"
	default:
		return "60ms"
	}
}

func silkRefDurationIndex(ms, channels int) int {
	if channels == 2 && ms == 60 {
		return 1
	}
	switch ms {
	case 20:
		return 1
	case 40:
		return 2
	default:
		return 3
	}
}
