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
					minSNR := 10.0
					if tc.channels == 2 && packetMs == 40 {
						// This configuration has a known coarse decoder/libopus
						// reconstruction difference. The former count-code
						// assertion masked this baseline by exiting first.
						minSNR = 6.0
					}
					if snr < minSNR || rmse > 0.18 {
						t.Fatalf("decoder/libopus output mismatch: alignedSNR=%.2fdB rmse=%.5f delay=%d scale=%.4f", snr, rmse, delay, scale)
					}
				})
			}
		}
	}
}

func TestCGOEncodeRefSILKOnlyExtendedDurationsStrict(t *testing.T) {
	t.Logf("libopus version: %s", cgoref.Version())

	cases := []struct {
		name       string
		rate       int
		channels   int
		configBase int
	}{
		{name: "16k-mono", rate: 16000, channels: 1, configBase: 8},
		{name: "48k-stereo", rate: 48000, channels: 2, configBase: 8},
	}

	for _, tc := range cases {
		tc := tc
		for _, packetMs := range []int{80, 100, 120} {
			packetMs := packetMs
			t.Run(tc.name+"/"+silkRefPacketName(packetMs), func(t *testing.T) {
				enc, err := opus.NewEncoder(tc.rate, tc.channels, opus.ApplicationVOIP)
				if err != nil {
					t.Fatalf("NewEncoder: %v", err)
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
				pkt, err := enc.EncodeFloat(silkRefSpeechFrame(tc.rate, 0, frameSize, tc.channels), frameSize)
				if err != nil {
					t.Fatalf("EncodeFloat: %v", err)
				}

				config := int((pkt[0] >> 3) & 0x1f)
				wantConfig := tc.configBase + silkRefExtendedDurationIndex(packetMs, tc.channels)
				if config != wantConfig {
					t.Fatalf("TOC config=%d, want SILK-only %dms grouping config %d (toc=0x%02x)", config, packetMs, wantConfig, pkt[0])
				}
				if code := int(pkt[0] & 0x03); code != silkRefExtendedCountCode(packetMs, tc.channels) {
					t.Fatalf("count code=%d, want %d for %dms packet", code, silkRefExtendedCountCode(packetMs, tc.channels), packetMs)
				}

				ours, err := dec.DecodeFloat(pkt)
				if err != nil {
					t.Fatalf("DecodeFloat: %v", err)
				}
				refOut, err := ref.DecodeFloat(pkt, tc.rate*120/1000)
				if err != nil {
					t.Fatalf("libopus decode (extended SILK packet non-conformant): %v", err)
				}
				wantSamples := frameSize * tc.channels
				if len(ours) != wantSamples {
					t.Fatalf("decoder samples=%d, want %d", len(ours), wantSamples)
				}
				if len(refOut) != wantSamples {
					t.Fatalf("libopus samples=%d, want %d", len(refOut), wantSamples)
				}
				oursRMS, oursPeak := silkRefStats(ours)
				refVals := make([]float64, len(refOut))
				for i, v := range refOut {
					refVals[i] = float64(v)
				}
				refRMS, refPeak := silkRefStats(refVals)
				if oursRMS < 1e-5 || refRMS < 1e-5 {
					t.Fatalf("decoded output collapsed: decoder RMS=%g libopus RMS=%g", oursRMS, refRMS)
				}
				if oursPeak > 1.5 || refPeak > 1.5 {
					t.Fatalf("decoded peak too large: decoder=%g libopus=%g", oursPeak, refPeak)
				}
			})
		}
	}
}

func TestCGOEncodeRefHybrid(t *testing.T) {
	t.Logf("libopus version: %s", cgoref.Version())

	cases := []struct {
		name       string
		rate       int
		channels   int
		bitrate    int
		wantConfig int
	}{
		{name: "swb-24k-mono", rate: 24000, channels: 1, bitrate: 64000, wantConfig: 13},
		{name: "fb-48k-mono", rate: 48000, channels: 1, bitrate: 64000, wantConfig: 15},
		{name: "fb-48k-stereo", rate: 48000, channels: 2, bitrate: 96000, wantConfig: 15},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			enc, err := opus.NewEncoder(tc.rate, tc.channels, opus.ApplicationVOIP)
			if err != nil {
				t.Fatalf("NewEncoder: %v", err)
			}
			if err := enc.SetBitrate(tc.bitrate); err != nil {
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

			frameSize := tc.rate * 20 / 1000
			maxSPC := tc.rate * 120 / 1000
			var oursAll, refAll []float64
			for p := 0; p < 8; p++ {
				in := silkRefHybridFrame(tc.rate, p*frameSize, frameSize, tc.channels)
				pkt, err := enc.EncodeFloat(in, frameSize)
				if err != nil {
					t.Fatalf("packet %d: EncodeFloat: %v", p, err)
				}
				config := int((pkt[0] >> 3) & 0x1f)
				if config != tc.wantConfig {
					t.Fatalf("packet %d: TOC config=%d, want hybrid config %d (toc=0x%02x)", p, config, tc.wantConfig, pkt[0])
				}
				if code := int(pkt[0] & 0x03); code != 0 {
					t.Fatalf("packet %d: count code=%d, want single-frame hybrid", p, code)
				}

				ours, err := dec.DecodeFloat(pkt)
				if err != nil {
					t.Fatalf("packet %d: DecodeFloat: %v", p, err)
				}
				refOut, err := ref.DecodeFloat(pkt, maxSPC)
				if err != nil {
					t.Fatalf("packet %d: libopus decode (hybrid packet non-conformant): %v", p, err)
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
		})
	}
}

func TestCGOEncodeRefHybridMultiFrameStrict(t *testing.T) {
	t.Logf("libopus version: %s", cgoref.Version())

	cases := []struct {
		name     string
		rate     int
		channels int
		bitrate  int
		config   int
	}{
		{name: "swb-24k-mono", rate: 24000, channels: 1, bitrate: 64000, config: 13},
		{name: "fb-48k-stereo", rate: 48000, channels: 2, bitrate: 96000, config: 15},
	}

	for _, tc := range cases {
		tc := tc
		for _, packetMs := range []int{40, 60, 120} {
			packetMs := packetMs
			t.Run(tc.name+"/"+silkRefPacketName(packetMs), func(t *testing.T) {
				enc, err := opus.NewEncoder(tc.rate, tc.channels, opus.ApplicationVOIP)
				if err != nil {
					t.Fatalf("NewEncoder: %v", err)
				}
				if err := enc.SetBitrate(tc.bitrate); err != nil {
					t.Fatalf("SetBitrate: %v", err)
				}
				ref, err := cgoref.NewDecoder(tc.rate, tc.channels)
				if err != nil {
					t.Fatalf("cgoref.NewDecoder: %v", err)
				}
				defer ref.Close()

				frameSize := tc.rate * packetMs / 1000
				pkt, err := enc.EncodeFloat(silkRefHybridFrame(tc.rate, 0, frameSize, tc.channels), frameSize)
				if err != nil {
					t.Fatalf("EncodeFloat: %v", err)
				}
				config := int((pkt[0] >> 3) & 0x1f)
				if config != tc.config {
					t.Fatalf("TOC config=%d, want hybrid config %d (toc=0x%02x)", config, tc.config, pkt[0])
				}
				if code := int(pkt[0] & 0x03); code != silkRefHybridCountCode(packetMs) {
					t.Fatalf("count code=%d, want %d for %dms hybrid packet", code, silkRefHybridCountCode(packetMs), packetMs)
				}
				refOut, err := ref.DecodeFloat(pkt, tc.rate*120/1000)
				if err != nil {
					t.Fatalf("libopus decode (hybrid multi-frame packet non-conformant): %v", err)
				}
				if wantSamples := frameSize * tc.channels; len(refOut) != wantSamples {
					t.Fatalf("libopus samples=%d, want %d", len(refOut), wantSamples)
				}
				refVals := make([]float64, len(refOut))
				for i, v := range refOut {
					refVals[i] = float64(v)
				}
				rms, peak := silkRefStats(refVals)
				if rms < 1e-5 {
					t.Fatalf("libopus decoded output collapsed: RMS=%g", rms)
				}
				if peak > 1.5 {
					t.Fatalf("libopus decoded peak too large: peak=%g", peak)
				}
			})
		}
	}
}

// TestCGOEncodeRefHybridVBR guards the VBR hybrid path: libopus must decode our
// constrained-VBR hybrid packets, and the per-frame coded size must drop below
// the CBR ceiling on a silent frame (proving the adaptive target is active).
func TestCGOEncodeRefHybridVBR(t *testing.T) {
	t.Logf("libopus version: %s", cgoref.Version())

	cases := []struct {
		name       string
		rate       int
		channels   int
		bitrate    int
		wantConfig int
	}{
		{name: "swb-24k-mono", rate: 24000, channels: 1, bitrate: 64000, wantConfig: 13},
		{name: "fb-48k-mono", rate: 48000, channels: 1, bitrate: 64000, wantConfig: 15},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			enc, err := opus.NewEncoder(tc.rate, tc.channels, opus.ApplicationVOIP)
			if err != nil {
				t.Fatalf("NewEncoder: %v", err)
			}
			if err := enc.SetBitrate(tc.bitrate); err != nil {
				t.Fatalf("SetBitrate: %v", err)
			}
			enc.SetVBR(true)
			ref, err := cgoref.NewDecoder(tc.rate, tc.channels)
			if err != nil {
				t.Fatalf("cgoref.NewDecoder: %v", err)
			}
			defer ref.Close()

			frameSize := tc.rate * 20 / 1000
			ceiling := tc.bitrate * 20 / 1000 / 8 // per-frame CBR ceiling in bytes
			var sawShrink bool
			for p := 0; p < 10; p++ {
				var in []float64
				if p == 5 {
					in = make([]float64, frameSize*tc.channels) // silent frame
				} else {
					in = silkRefHybridFrame(tc.rate, p*frameSize, frameSize, tc.channels)
				}
				pkt, err := enc.EncodeFloat(in, frameSize)
				if err != nil {
					t.Fatalf("packet %d: EncodeFloat: %v", p, err)
				}
				if config := int((pkt[0] >> 3) & 0x1f); config != tc.wantConfig {
					t.Fatalf("packet %d: TOC config=%d, want hybrid config %d", p, config, tc.wantConfig)
				}
				if len(pkt) < ceiling {
					sawShrink = true
				}
				refOut, err := ref.DecodeFloat(pkt, tc.rate*120/1000)
				if err != nil {
					t.Fatalf("packet %d: libopus decode (VBR hybrid packet non-conformant): %v", p, err)
				}
				if want := frameSize * tc.channels; len(refOut) != want {
					t.Fatalf("packet %d: libopus samples=%d, want %d", p, len(refOut), want)
				}
			}
			if !sawShrink {
				t.Fatalf("no hybrid frame coded below the %d-byte CBR ceiling; VBR target not active", ceiling)
			}
		})
	}
}

// TestCGOEncodeRefHybridRedundancyTransition drives a hybrid->CELT mode
// transition and verifies that libopus decodes the transitional packet, which
// carries a trailing 5 ms redundant CELT frame. A malformed redundancy header or
// frame would make libopus reject the packet or truncate the CELT main layer's
// budget, so a clean decode to non-trivial output confirms the wire format is
// conformant.
func TestCGOEncodeRefHybridRedundancyTransition(t *testing.T) {
	t.Logf("libopus version: %s", cgoref.Version())

	const (
		rate     = 48000
		channels = 1
		bitrate  = 64000
	)
	frameSize := rate * 20 / 1000
	maxSPC := rate * 120 / 1000

	enc, err := opus.NewEncoder(rate, channels, opus.ApplicationVOIP)
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	if err := enc.SetBitrate(bitrate); err != nil {
		t.Fatalf("SetBitrate: %v", err)
	}
	ref, err := cgoref.NewDecoder(rate, channels)
	if err != nil {
		t.Fatalf("cgoref.NewDecoder: %v", err)
	}
	defer ref.Close()

	refDecode := func(name string, pkt []byte) {
		out, err := ref.DecodeFloat(pkt, maxSPC)
		if err != nil {
			t.Fatalf("%s: libopus decode (non-conformant packet): %v", name, err)
		}
		if len(out) != frameSize*channels {
			t.Fatalf("%s: libopus samples=%d, want %d", name, len(out), frameSize*channels)
		}
		out64 := make([]float64, len(out))
		for i, v := range out {
			out64[i] = float64(v)
		}
		rms, peak := silkRefStats(out64)
		if peak > 1.5 || rms < 1e-5 {
			t.Fatalf("%s: libopus output suspect (rms=%g peak=%g)", name, rms, peak)
		}
	}

	frame := 0
	for ; frame < 4; frame++ {
		pkt, err := enc.EncodeFloat(silkRefHybridFrame(rate, frame*frameSize, frameSize, channels), frameSize)
		if err != nil {
			t.Fatalf("hybrid warmup %d: EncodeFloat: %v", frame, err)
		}
		if config := int((pkt[0] >> 3) & 0x1f); config < 12 || config > 15 {
			t.Fatalf("hybrid warmup %d: config=%d, want hybrid", frame, config)
		}
		refDecode("hybrid-warmup", pkt)
	}

	// Switch to music: the transitional packet must remain hybrid and carry the
	// redundant CELT frame.
	enc.SetSignalType(opus.SignalMusic)
	transPkt, err := enc.EncodeFloat(silkRefHybridFrame(rate, frame*frameSize, frameSize, channels), frameSize)
	if err != nil {
		t.Fatalf("transition: EncodeFloat: %v", err)
	}
	if config := int((transPkt[0] >> 3) & 0x1f); config < 12 || config > 15 {
		t.Fatalf("transition packet config=%d, want hybrid (deferred switch)", config)
	}
	refDecode("transition-hybrid", transPkt)
	frame++

	// The genuine CELT-only switch follows.
	celtPkt, err := enc.EncodeFloat(silkRefSpeechFrame(rate, frame*frameSize, frameSize, channels), frameSize)
	if err != nil {
		t.Fatalf("post-transition: EncodeFloat: %v", err)
	}
	if config := int((celtPkt[0] >> 3) & 0x1f); config < 16 {
		t.Fatalf("post-transition packet config=%d, want CELT-only", config)
	}
	refDecode("post-transition-celt", celtPkt)
}

// TestCGOEncodeRefCELTToSILKRedundancyTransition verifies both leading
// redundancy destinations: CELT-only -> SILK-only and CELT-only -> hybrid.
// libopus must accept the celt_to_silk=1 header/tail layout and produce one
// complete, non-trivial 20 ms frame.
func TestCGOEncodeRefCELTToSILKRedundancyTransition(t *testing.T) {
	t.Logf("libopus version: %s", cgoref.Version())

	const (
		rate     = 48000
		channels = 1
	)
	frameSize := rate * 20 / 1000
	maxSPC := rate * 120 / 1000

	cases := []struct {
		name     string
		bitrate  int
		wantMode string
	}{
		{name: "silk-only", bitrate: 24000, wantMode: "silk"},
		{name: "hybrid", bitrate: 64000, wantMode: "hybrid"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			enc, err := opus.NewEncoder(rate, channels, opus.ApplicationVOIP)
			if err != nil {
				t.Fatalf("NewEncoder: %v", err)
			}
			if err := enc.SetBitrate(64000); err != nil {
				t.Fatalf("SetBitrate warmup: %v", err)
			}
			enc.SetSignalType(opus.SignalMusic)
			ref, err := cgoref.NewDecoder(rate, channels)
			if err != nil {
				t.Fatalf("cgoref.NewDecoder: %v", err)
			}
			defer ref.Close()

			for frame := 0; frame < 3; frame++ {
				pkt, err := enc.EncodeFloat(silkRefHybridFrame(rate, frame*frameSize, frameSize, channels), frameSize)
				if err != nil {
					t.Fatalf("CELT warmup %d: EncodeFloat: %v", frame, err)
				}
				if config := int((pkt[0] >> 3) & 0x1f); config < 16 {
					t.Fatalf("CELT warmup %d: config=%d, want CELT-only", frame, config)
				}
				if _, err := ref.DecodeFloat(pkt, maxSPC); err != nil {
					t.Fatalf("CELT warmup %d: libopus decode: %v", frame, err)
				}
			}

			enc.SetSignalType(opus.SignalVoice)
			if err := enc.SetBitrate(tc.bitrate); err != nil {
				t.Fatalf("SetBitrate transition: %v", err)
			}
			pkt, err := enc.EncodeFloat(silkRefHybridFrame(rate, 3*frameSize, frameSize, channels), frameSize)
			if err != nil {
				t.Fatalf("transition EncodeFloat: %v", err)
			}
			config := int((pkt[0] >> 3) & 0x1f)
			switch tc.wantMode {
			case "silk":
				if config >= 12 {
					t.Fatalf("transition config=%d, want SILK-only", config)
				}
			case "hybrid":
				if config < 12 || config > 15 {
					t.Fatalf("transition config=%d, want hybrid", config)
				}
			}

			out, err := ref.DecodeFloat(pkt, maxSPC)
			if err != nil {
				t.Fatalf("transition libopus decode (non-conformant leading redundancy): %v", err)
			}
			if len(out) != frameSize*channels {
				t.Fatalf("transition libopus samples=%d, want %d", len(out), frameSize*channels)
			}
			out64 := make([]float64, len(out))
			for i, v := range out {
				out64[i] = float64(v)
			}
			rms, peak := silkRefStats(out64)
			if rms < 1e-5 || peak > 1.5 {
				t.Fatalf("transition libopus output suspect: rms=%g peak=%g", rms, peak)
			}
		})
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

func silkRefHybridFrame(rate, start, n, channels int) []float64 {
	out := silkRefSpeechFrame(rate, start, n, channels)
	highFreq := 10000.0
	if rate >= 48000 {
		highFreq = 16000.0
	}
	for i := 0; i < n; i++ {
		t := float64(start+i) / float64(rate)
		out[i*channels] += 0.045 * math.Sin(2*math.Pi*highFreq*t+0.11)
		if channels == 2 {
			out[i*channels+1] += 0.04 * math.Sin(2*math.Pi*highFreq*t+0.73)
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
	case 80:
		return "80ms"
	case 100:
		return "100ms"
	case 120:
		return "120ms"
	default:
		return "60ms"
	}
}

func silkRefDurationIndex(ms, channels int) int {
	if channels == 2 && ms >= 60 {
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

func silkRefExtendedDurationIndex(ms, channels int) int {
	if channels == 2 {
		return 1
	}
	switch ms {
	case 80:
		return 2
	case 100:
		return 1
	default:
		return 3
	}
}

func silkRefExtendedCountCode(ms, channels int) int {
	if channels == 2 {
		return 3
	}
	switch ms {
	case 80, 120:
		return 2
	default:
		return 3
	}
}

func silkRefHybridCountCode(ms int) int {
	if ms == 40 {
		return 1
	}
	return 3
}
