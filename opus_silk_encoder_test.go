package opus

import (
	"fmt"
	"math"
	"testing"

	framing "github.com/darui3018823/opus/internal"
	"github.com/darui3018823/opus/internal/entcode"
	"github.com/darui3018823/opus/internal/silk"
)

func TestEncoderSILKOnlyVOIPLowBitrateRoundTrip(t *testing.T) {
	cases := []struct {
		rate          int
		wantConfig    int
		wantBandwidth int
	}{
		{8000, 1, BandwidthNarrowband},
		{12000, 5, BandwidthMediumband},
		{16000, 9, BandwidthWideband},
	}

	for _, tc := range cases {
		t.Run(rateName(tc.rate), func(t *testing.T) {
			enc, err := NewEncoder(tc.rate, 1, ApplicationVOIP)
			if err != nil {
				t.Fatalf("NewEncoder: %v", err)
			}
			if err := enc.SetBitrate(24000); err != nil {
				t.Fatalf("SetBitrate: %v", err)
			}
			if got := enc.Bandwidth(); got != tc.wantBandwidth {
				t.Fatalf("Bandwidth()=%d, want %d", got, tc.wantBandwidth)
			}

			frameSize := tc.rate * 20 / 1000
			pcm := generateSine(200, tc.rate, 1, frameSize)
			var pkt []byte
			for i := 0; i < 10; i++ {
				pkt, err = enc.Encode(pcm, frameSize)
				if err != nil {
					t.Fatalf("Encode: %v", err)
				}
			}

			config := int(pkt[0] >> 3)
			if config != tc.wantConfig {
				t.Fatalf("TOC config=%d, want SILK-only 20ms config %d (toc=0x%02x)", config, tc.wantConfig, pkt[0])
			}
			if code := int(pkt[0] & 0x03); code != 0 {
				t.Fatalf("count code=%d, want 0 for one 20ms SILK frame", code)
			}

			dec, err := NewDecoder(tc.rate, 1)
			if err != nil {
				t.Fatalf("NewDecoder: %v", err)
			}
			decoded, err := dec.DecodeFloat(pkt)
			if err != nil {
				t.Fatalf("DecodeFloat: %v", err)
			}
			if len(decoded) != frameSize {
				t.Fatalf("decoded samples=%d, want %d", len(decoded), frameSize)
			}
		})
	}
}

func TestEncoderSILKOnlyVOIPMultiFrameRoundTrip(t *testing.T) {
	const rate = 8000
	base := rate * 20 / 1000

	for _, mult := range []int{2, 3, 6} {
		t.Run(multName(mult), func(t *testing.T) {
			enc, err := NewEncoder(rate, 1, ApplicationVOIP)
			if err != nil {
				t.Fatalf("NewEncoder: %v", err)
			}
			if err := enc.SetBitrate(18000); err != nil {
				t.Fatalf("SetBitrate: %v", err)
			}

			warmup := generateSine(180, rate, 1, base)
			for i := 0; i < 8; i++ {
				if _, err := enc.Encode(warmup, base); err != nil {
					t.Fatalf("warmup Encode: %v", err)
				}
			}

			frameSize := base * mult
			pcm := generateSine(180, rate, 1, frameSize)
			pkt, err := enc.Encode(pcm, frameSize)
			if err != nil {
				t.Fatalf("Encode: %v", err)
			}

			config := int(pkt[0] >> 3)
			wantConfig := 2
			if mult == 3 || mult == 6 {
				wantConfig = 3
			}
			if config != wantConfig {
				t.Fatalf("TOC config=%d, want SILK NB config %d", config, wantConfig)
			}
			wantCode := 0
			if mult == 6 {
				wantCode = 2
			}
			if code := int(pkt[0] & 0x03); code != wantCode {
				t.Fatalf("count code=%d, want %d", code, wantCode)
			}

			dec, err := NewDecoder(rate, 1)
			if err != nil {
				t.Fatalf("NewDecoder: %v", err)
			}
			decoded, err := dec.DecodeFloat(pkt)
			if err != nil {
				t.Fatalf("DecodeFloat: %v", err)
			}
			if len(decoded) != frameSize {
				t.Fatalf("decoded samples=%d, want %d", len(decoded), frameSize)
			}
		})
	}
}

func TestEncoderSILKOnlyStereoMultiFrameRoundTrip(t *testing.T) {
	const rate = 16000
	base := rate * 20 / 1000

	for _, mult := range []int{2, 3, 6} {
		t.Run(multName(mult), func(t *testing.T) {
			enc, err := NewEncoder(rate, 2, ApplicationVOIP)
			if err != nil {
				t.Fatalf("NewEncoder: %v", err)
			}
			if err := enc.SetBitrate(32000); err != nil {
				t.Fatalf("SetBitrate: %v", err)
			}

			frameSize := base * mult
			pkt, err := enc.Encode(generateSine(180, rate, 2, frameSize), frameSize)
			if err != nil {
				t.Fatalf("Encode: %v", err)
			}
			if config := int(pkt[0] >> 3); config < 8 || config > 10 {
				t.Fatalf("TOC config=%d, want SILK WB 20/40ms packetization", config)
			}
			if stereo := (pkt[0] & 0x04) != 0; !stereo {
				t.Fatalf("TOC stereo bit not set for stereo SILK packet")
			}

			dec, err := NewDecoder(rate, 2)
			if err != nil {
				t.Fatalf("NewDecoder: %v", err)
			}
			decoded, err := dec.DecodeFloat(pkt)
			if err != nil {
				t.Fatalf("DecodeFloat: %v", err)
			}
			if want := frameSize * 2; len(decoded) != want {
				t.Fatalf("decoded samples=%d, want %d", len(decoded), want)
			}
		})
	}
}

func TestEncoderSILKOnlyAllSupportedDurationsStrict(t *testing.T) {
	cases := []struct {
		rate       int
		channels   int
		configBase int
	}{
		{rate: 8000, channels: 1, configBase: 0},
		{rate: 12000, channels: 1, configBase: 4},
		{rate: 16000, channels: 1, configBase: 8},
		{rate: 16000, channels: 2, configBase: 8},
		{rate: 48000, channels: 1, configBase: 8},
		{rate: 48000, channels: 2, configBase: 8},
	}

	for _, tc := range cases {
		t.Run(rateName(tc.rate)+"/"+channelName(tc.channels), func(t *testing.T) {
			enc, err := NewEncoder(tc.rate, tc.channels, ApplicationVOIP)
			if err != nil {
				t.Fatalf("NewEncoder: %v", err)
			}
			if err := enc.SetBitrate(24000); err != nil {
				t.Fatalf("SetBitrate: %v", err)
			}
			dec, err := NewDecoder(tc.rate, tc.channels)
			if err != nil {
				t.Fatalf("NewDecoder: %v", err)
			}

			base := tc.rate * 20 / 1000
			for mult := 1; mult <= 6; mult++ {
				frameSize := base * mult
				pcm := strictSpeechLikeFrame(tc.rate, tc.channels, mult*frameSize, frameSize)
				pkt, err := enc.EncodeFloat(pcm, frameSize)
				if err != nil {
					t.Fatalf("%dms: EncodeFloat: %v", mult*20, err)
				}
				if len(pkt) < 2 {
					t.Fatalf("%dms: packet too short: %d bytes", mult*20, len(pkt))
				}

				config := int(pkt[0] >> 3)
				wantConfig := tc.configBase + strictSILKDurationIndex(mult, tc.channels)
				if config != wantConfig {
					t.Fatalf("%dms: TOC config=%d, want SILK config %d (toc=0x%02x)", mult*20, config, wantConfig, pkt[0])
				}
				if gotStereo := (pkt[0] & 0x04) != 0; gotStereo != (tc.channels == 2) {
					t.Fatalf("%dms: TOC stereo=%v, want %v", mult*20, gotStereo, tc.channels == 2)
				}
				if code := int(pkt[0] & 0x03); code != strictSILKCountCode(mult, tc.channels) {
					t.Fatalf("%dms: count code=%d, want %d", mult*20, code, strictSILKCountCode(mult, tc.channels))
				}

				decoded, err := dec.DecodeFloat(pkt)
				if err != nil {
					t.Fatalf("%dms: DecodeFloat: %v", mult*20, err)
				}
				if want := frameSize * tc.channels; len(decoded) != want {
					t.Fatalf("%dms: decoded samples=%d, want %d", mult*20, len(decoded), want)
				}
				if got := dec.GetLastPacketDuration(); got != frameSize {
					t.Fatalf("%dms: last packet duration=%d, want %d", mult*20, got, frameSize)
				}
				rms, peak := strictSignalStats(decoded)
				if rms < 1e-5 {
					t.Fatalf("%dms: decoded output collapsed: RMS=%g", mult*20, rms)
				}
				if peak > 1.25 {
					t.Fatalf("%dms: decoded peak runaway: peak=%g", mult*20, peak)
				}
			}
		})
	}
}

func TestEncoderVOIPHighBitrateStaysCELT(t *testing.T) {
	enc, err := NewEncoder(16000, 1, ApplicationVOIP)
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	if err := enc.SetBitrate(64000); err != nil {
		t.Fatalf("SetBitrate: %v", err)
	}

	frameSize := 16000 * 20 / 1000
	pkt, err := enc.Encode(generateSine(200, 16000, 1, frameSize), frameSize)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if config := int(pkt[0] >> 3); config < 16 {
		t.Fatalf("TOC config=%d, want CELT-only config at high bitrate", config)
	}
}

func TestEncoderVoiceModeTransitionsStrict(t *testing.T) {
	const rate = 48000
	const channels = 1
	frameSize := rate * 20 / 1000

	enc, err := NewEncoder(rate, channels, ApplicationVOIP)
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	dec, err := NewDecoder(rate, channels)
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}

	steps := []struct {
		name      string
		configure func(*Encoder) error
		wantMode  string
	}{
		{
			name: "low-bitrate-voip-silk",
			configure: func(e *Encoder) error {
				return e.SetBitrate(24000)
			},
			wantMode: "silk",
		},
		{
			name: "high-bitrate-voip-hybrid",
			configure: func(e *Encoder) error {
				return e.SetBitrate(64000)
			},
			wantMode: "hybrid",
		},
		{
			name: "music-hint-transition-hybrid",
			configure: func(e *Encoder) error {
				e.SetSignalType(SignalMusic)
				return nil
			},
			// A hybrid->CELT switch is deferred by one packet: this transitional
			// frame stays hybrid and carries a trailing redundant CELT frame whose
			// state seeds the next CELT-only packet (libopus opus_encode_native).
			wantMode: "hybrid",
		},
		{
			name: "music-hint-celt",
			configure: func(e *Encoder) error {
				return nil
			},
			wantMode: "celt",
		},
		{
			name: "voice-hint-low-bitrate-back-to-silk",
			configure: func(e *Encoder) error {
				e.SetSignalType(SignalVoice)
				return e.SetBitrate(24000)
			},
			wantMode: "silk",
		},
		{
			name: "restricted-low-delay-forces-celt",
			configure: func(e *Encoder) error {
				e.SetApplication(ApplicationRestrictedLowDelay)
				e.SetSignalType(SignalVoice)
				return nil
			},
			wantMode: "celt",
		},
		{
			name: "voip-after-reset-returns-to-silk",
			configure: func(e *Encoder) error {
				e.SetApplication(ApplicationVOIP)
				if err := e.SetBitrate(24000); err != nil {
					return err
				}
				return e.Reset()
			},
			wantMode: "silk",
		},
	}

	for i, step := range steps {
		t.Run(step.name, func(t *testing.T) {
			if err := step.configure(enc); err != nil {
				t.Fatalf("configure: %v", err)
			}
			pcm := strictSpeechLikeFrame(rate, channels, i*frameSize, frameSize)
			if step.wantMode == "hybrid" {
				pcm = strictHybridWidebandFrame(rate, channels, i*frameSize, frameSize)
			}
			pkt, err := enc.EncodeFloat(pcm, frameSize)
			if err != nil {
				t.Fatalf("EncodeFloat: %v", err)
			}
			config := int(pkt[0] >> 3)
			if got := strictOpusMode(config); got != step.wantMode {
				t.Fatalf("TOC config=%d mode=%s, want %s", config, got, step.wantMode)
			}
			decoded, err := dec.DecodeFloat(pkt)
			if err != nil {
				t.Fatalf("DecodeFloat: %v", err)
			}
			if len(decoded) != frameSize*channels {
				t.Fatalf("decoded samples=%d, want %d", len(decoded), frameSize*channels)
			}
		})
	}
}

// TestEncoderHybridToCELTRedundancy verifies the libopus-faithful hybrid->CELT
// transition: when the encoder would switch from hybrid to CELT-only, it defers
// the switch by one packet. The transitional packet stays hybrid and carries a
// trailing 5 ms redundant CELT frame; the following packet is genuinely CELT.
// Both must decode cleanly to non-trivial output (a corrupt redundant tail would
// truncate the CELT main layer's budget and wreck the reconstruction).
func TestEncoderHybridToCELTRedundancy(t *testing.T) {
	const (
		rate     = 48000
		channels = 1
	)
	frameSize := rate * 20 / 1000

	enc, err := NewEncoder(rate, channels, ApplicationVOIP)
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	if err := enc.SetBitrate(64000); err != nil {
		t.Fatalf("SetBitrate: %v", err)
	}
	dec, err := NewDecoder(rate, channels)
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}

	decodeOK := func(name string, pkt []byte) {
		decoded, err := dec.DecodeFloat(pkt)
		if err != nil {
			t.Fatalf("%s: DecodeFloat: %v", name, err)
		}
		if len(decoded) != frameSize*channels {
			t.Fatalf("%s: decoded samples=%d, want %d", name, len(decoded), frameSize*channels)
		}
		rms, _ := strictSignalStats(decoded)
		if math.IsNaN(rms) || rms < 1e-3 {
			t.Fatalf("%s: decoded output is silent/garbage (rms=%g)", name, rms)
		}
	}

	// Establish a hybrid run so prevMode == hybrid.
	frame := 0
	for ; frame < 3; frame++ {
		pcm := strictHybridWidebandFrame(rate, channels, frame*frameSize, frameSize)
		pkt, err := enc.EncodeFloat(pcm, frameSize)
		if err != nil {
			t.Fatalf("hybrid warmup EncodeFloat: %v", err)
		}
		if got := strictOpusMode(int(pkt[0] >> 3)); got != "hybrid" {
			t.Fatalf("warmup frame %d mode=%s, want hybrid", frame, got)
		}
		decodeOK("hybrid-warmup", pkt)
	}

	// Switch to music: the encoder wants CELT-only, but the transition is deferred
	// one packet, so this packet must still be hybrid (carrying redundancy).
	enc.SetSignalType(SignalMusic)
	transPCM := strictHybridWidebandFrame(rate, channels, frame*frameSize, frameSize)
	transPkt, err := enc.EncodeFloat(transPCM, frameSize)
	if err != nil {
		t.Fatalf("transition EncodeFloat: %v", err)
	}
	if got := strictOpusMode(int(transPkt[0] >> 3)); got != "hybrid" {
		t.Fatalf("transition packet mode=%s, want hybrid (deferred switch)", got)
	}
	decodeOK("transition-hybrid", transPkt)
	frame++

	// The next packet is the genuine CELT-only switch.
	celtPCM := strictSpeechLikeFrame(rate, channels, frame*frameSize, frameSize)
	celtPkt, err := enc.EncodeFloat(celtPCM, frameSize)
	if err != nil {
		t.Fatalf("post-transition EncodeFloat: %v", err)
	}
	if got := strictOpusMode(int(celtPkt[0] >> 3)); got != "celt" {
		t.Fatalf("post-transition packet mode=%s, want celt", got)
	}
	decodeOK("post-transition-celt", celtPkt)
}

func TestEncoderHybridToCELTWithoutRedundancyKeepsHybridState(t *testing.T) {
	const (
		rate      = 48000
		channels  = 2
		frameSize = rate * 20 / 1000
	)
	enc, err := NewEncoder(rate, channels, ApplicationVOIP)
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	if err := enc.SetBitrate(96000); err != nil {
		t.Fatalf("SetBitrate warmup: %v", err)
	}
	if _, err := enc.EncodeFloat(strictHybridWidebandFrame(rate, channels, 0, frameSize), frameSize); err != nil {
		t.Fatalf("hybrid warmup: %v", err)
	}
	if enc.prevMode != framing.ModeHybrid {
		t.Fatalf("warmup prevMode=%d, want hybrid", enc.prevMode)
	}

	// At this tighter budget the deferred hybrid packet still fits, but its
	// trailing redundancy does not. The emitted packet is therefore plain
	// hybrid and must remain the predecessor for the next mode decision.
	enc.SetSignalType(SignalMusic)
	if err := enc.SetBitrate(10000); err != nil {
		t.Fatalf("SetBitrate transition: %v", err)
	}
	transitionPCM := make([]float64, frameSize*channels)
	pkt, err := enc.EncodeFloat(transitionPCM, frameSize)
	if err != nil {
		t.Fatalf("transition EncodeFloat: %v", err)
	}
	config, _, code := framing.ParseTOC(pkt[0])
	if got := strictOpusMode(config); got != "hybrid" {
		t.Fatalf("transition mode=%s, want deferred hybrid", got)
	}
	streams, err := splitOpusFrames(pkt[1:], code)
	if err != nil || len(streams) != 1 {
		t.Fatalf("split transition packet: streams=%d err=%v", len(streams), err)
	}
	sd, err := silk.NewDecoderWithFrameMs(16000, channels, 20)
	if err != nil {
		t.Fatalf("silk.NewDecoderWithFrameMs: %v", err)
	}
	rangeDec := entcode.NewDecoder(streams[0])
	if _, err := sd.DecodeMultiWithDecoder(rangeDec, 1); err != nil {
		t.Fatalf("decode SILK symbols: %v", err)
	}
	if rangeDec.ECTell()+37 <= len(streams[0])*8 && rangeDec.DecodeBitLogp(12) {
		t.Fatalf("transition unexpectedly emitted redundancy at the constrained budget")
	}
	if enc.prevMode != framing.ModeHybrid {
		t.Fatalf("prevMode=%d after plain hybrid fallback, want hybrid", enc.prevMode)
	}
}

// alignedSNRWindow measures the delay/scale-aligned SNR of out against in over
// the half-open sample window [lo, hi), searching a small decoder delay. Used to
// assert state continuity across a mode transition.
func alignedSNRWindow(in, out []float64, lo, hi, frameSize int) float64 {
	bestErr := math.Inf(1)
	for d := 0; d <= 3*frameSize; d++ {
		var dot, e2 float64
		for i := lo; i < hi; i++ {
			oi := i - d
			if oi < 0 || oi >= len(out) {
				continue
			}
			dot += in[i] * out[oi]
			e2 += out[oi] * out[oi]
		}
		if e2 == 0 {
			continue
		}
		sc := dot / e2
		var e float64
		for i := lo; i < hi; i++ {
			oi := i - d
			if oi < 0 || oi >= len(out) {
				continue
			}
			r := in[i] - sc*out[oi]
			e += r * r
		}
		e = math.Sqrt(e / float64(hi-lo))
		if e < bestErr {
			bestErr = e
		}
	}
	var inRMS float64
	for i := lo; i < hi; i++ {
		inRMS += in[i] * in[i]
	}
	inRMS = math.Sqrt(inRMS / float64(hi-lo))
	if bestErr == 0 {
		return math.Inf(1)
	}
	return 20 * math.Log10(inRMS/bestErr)
}

// TestEncoderHybridToCELTRedundancyStateContinuity guards the SILK->CELT trailing
// redundancy state sync (T3): the first genuine CELT-only packet after a deferred
// hybrid->CELT transition inter-predicts its coarse energy from the trailing
// redundant frame's state. The decoder adopts that state (celtDec.CopyStateFrom(
// redDec)); the encoder must seed celtEncoder from the same redundant frame state.
// If it does not, the per-band prediction baseline diverges, leaving a persistent
// energy offset across the whole CELT-only run, which this test detects as a
// collapsed aligned SNR.
func TestEncoderHybridToCELTRedundancyStateContinuity(t *testing.T) {
	const (
		rate     = 48000
		channels = 1
		bitrate  = 64000
	)
	frameSize := rate * 20 / 1000

	enc, err := NewEncoder(rate, channels, ApplicationVOIP)
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	if err := enc.SetBitrate(bitrate); err != nil {
		t.Fatalf("SetBitrate: %v", err)
	}
	dec, err := NewDecoder(rate, channels)
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}

	tone := func(start, n int) []float64 {
		out := make([]float64, n)
		for i := 0; i < n; i++ {
			tt := float64(start+i) / float64(rate)
			out[i] = 0.3 * math.Sin(2*math.Pi*1000*tt)
		}
		return out
	}

	const (
		warmup     = 5
		postFrames = 14
	)
	var decoded []float64
	frame := 0

	// Hybrid warmup so prevMode == hybrid.
	enc.SetSignalType(SignalVoice)
	for ; frame < warmup; frame++ {
		pkt, err := enc.EncodeFloat(strictHybridWidebandFrame(rate, channels, frame*frameSize, frameSize), frameSize)
		if err != nil {
			t.Fatalf("hybrid warmup %d: %v", frame, err)
		}
		if got := strictOpusMode(int(pkt[0] >> 3)); got != "hybrid" {
			t.Fatalf("warmup %d mode=%s, want hybrid", frame, got)
		}
		out, err := dec.DecodeFloat(pkt)
		if err != nil {
			t.Fatalf("warmup decode %d: %v", frame, err)
		}
		decoded = append(decoded, out...)
	}

	// Deferred transition: this packet stays hybrid and carries the trailing
	// redundant CELT frame.
	enc.SetSignalType(SignalMusic)
	transPkt, err := enc.EncodeFloat(strictHybridWidebandFrame(rate, channels, frame*frameSize, frameSize), frameSize)
	if err != nil {
		t.Fatalf("transition: %v", err)
	}
	if got := strictOpusMode(int(transPkt[0] >> 3)); got != "hybrid" {
		t.Fatalf("transition mode=%s, want hybrid (deferred switch)", got)
	}
	out, err := dec.DecodeFloat(transPkt)
	if err != nil {
		t.Fatalf("transition decode: %v", err)
	}
	decoded = append(decoded, out...)
	frame++

	// Genuine CELT-only run carrying a steady tone.
	celtStart := frame
	for ; frame < celtStart+postFrames; frame++ {
		pkt, err := enc.EncodeFloat(tone(frame*frameSize, frameSize), frameSize)
		if err != nil {
			t.Fatalf("post-transition celt %d: %v", frame, err)
		}
		if got := strictOpusMode(int(pkt[0] >> 3)); got != "celt" {
			t.Fatalf("post-transition %d mode=%s, want celt", frame, got)
		}
		out, err := dec.DecodeFloat(pkt)
		if err != nil {
			t.Fatalf("post-transition decode %d: %v", frame, err)
		}
		decoded = append(decoded, out...)
	}

	totalFrames := frame
	ref := make([]float64, totalFrames*frameSize)
	for f := celtStart; f < totalFrames; f++ {
		copy(ref[f*frameSize:(f+1)*frameSize], tone(f*frameSize, frameSize))
	}

	// Skip the first few CELT-only frames (transition crossfade) and measure to the
	// end of the run.
	lo := (celtStart + 3) * frameSize
	hi := totalFrames * frameSize
	snr := alignedSNRWindow(ref, decoded, lo, hi, frameSize)
	t.Logf("post-transition CELT-only run aligned SNR=%.2fdB", snr)
	// With the state sync the steady tone reconstructs at ~48 dB; dropping the
	// sync leaves a persistent per-band energy offset that collapses it to ~31 dB.
	// The 42 dB gate sits with margin between the two.
	if snr < 42.0 {
		t.Fatalf("post-transition CELT-only run SNR=%.2fdB too low: trailing redundancy state not inherited by the next CELT encoder (T3)", snr)
	}
}

func TestEncoderCELTToSILKRedundancy(t *testing.T) {
	const (
		rate     = 48000
		channels = 1
	)
	frameSize := rate * 20 / 1000
	enc, err := NewEncoder(rate, channels, ApplicationVOIP)
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	dec, err := NewDecoder(rate, channels)
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}

	enc.SetSignalType(SignalMusic)
	if err := enc.SetBitrate(64000); err != nil {
		t.Fatalf("SetBitrate CELT: %v", err)
	}
	celtPkt, err := enc.EncodeFloat(strictSpeechLikeFrame(rate, channels, 0, frameSize), frameSize)
	if err != nil {
		t.Fatalf("CELT EncodeFloat: %v", err)
	}
	if got := strictOpusMode(int(celtPkt[0] >> 3)); got != "celt" {
		t.Fatalf("warmup mode=%s, want celt", got)
	}
	if _, err := dec.DecodeFloat(celtPkt); err != nil {
		t.Fatalf("CELT DecodeFloat: %v", err)
	}

	enc.SetSignalType(SignalVoice)
	if err := enc.SetBitrate(24000); err != nil {
		t.Fatalf("SetBitrate SILK: %v", err)
	}
	silkPkt, err := enc.EncodeFloat(strictSpeechLikeFrame(rate, channels, frameSize, frameSize), frameSize)
	if err != nil {
		t.Fatalf("SILK transition EncodeFloat: %v", err)
	}
	config, _, code := framing.ParseTOC(silkPkt[0])
	if got := strictOpusMode(config); got != "silk" {
		t.Fatalf("transition mode=%s, want silk", got)
	}
	streams, err := splitOpusFrames(silkPkt[1:], code)
	if err != nil || len(streams) != 1 {
		t.Fatalf("split transition packet: streams=%d err=%v", len(streams), err)
	}
	sd, err := silk.NewDecoderWithFrameMs(16000, channels, 20)
	if err != nil {
		t.Fatalf("silk.NewDecoderWithFrameMs: %v", err)
	}
	rangeDec := entcode.NewDecoder(streams[0])
	if _, err := sd.DecodeMultiWithDecoder(rangeDec, 1); err != nil {
		t.Fatalf("decode SILK symbols: %v", err)
	}
	if rangeDec.ECTell()+17 > len(streams[0])*8 {
		t.Fatalf("transition stream has no room for inferred redundancy: tell=%d bytes=%d packet=%d", rangeDec.ECTell(), len(streams[0]), len(silkPkt))
	}
	if !rangeDec.DecodeBitLogp(1) {
		t.Fatalf("celt_to_silk=0, want 1")
	}
	redBytes := len(streams[0]) - ((rangeDec.ECTell() + 7) >> 3)
	if redBytes < 2 {
		t.Fatalf("redundancy bytes=%d, want >=2", redBytes)
	}

	out, err := dec.DecodeFloat(silkPkt)
	if err != nil {
		t.Fatalf("transition DecodeFloat: %v", err)
	}
	rms, peak := strictSignalStats(out)
	if len(out) != frameSize*channels || math.IsNaN(rms) || rms < 1e-3 || peak > 1.5 {
		t.Fatalf("transition output invalid: len=%d rms=%g peak=%g", len(out), rms, peak)
	}
}

func TestEncoderCELTToHybridRedundancy(t *testing.T) {
	const (
		rate     = 48000
		channels = 1
	)
	frameSize := rate * 20 / 1000
	enc, err := NewEncoder(rate, channels, ApplicationVOIP)
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	dec, err := NewDecoder(rate, channels)
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}

	enc.SetSignalType(SignalMusic)
	if err := enc.SetBitrate(64000); err != nil {
		t.Fatalf("SetBitrate: %v", err)
	}
	celtPkt, err := enc.EncodeFloat(strictHybridWidebandFrame(rate, channels, 0, frameSize), frameSize)
	if err != nil {
		t.Fatalf("CELT EncodeFloat: %v", err)
	}
	if _, err := dec.DecodeFloat(celtPkt); err != nil {
		t.Fatalf("CELT DecodeFloat: %v", err)
	}

	enc.SetSignalType(SignalVoice)
	hybridPkt, err := enc.EncodeFloat(strictHybridWidebandFrame(rate, channels, frameSize, frameSize), frameSize)
	if err != nil {
		t.Fatalf("hybrid transition EncodeFloat: %v", err)
	}
	config, _, code := framing.ParseTOC(hybridPkt[0])
	if got := strictOpusMode(config); got != "hybrid" {
		t.Fatalf("transition mode=%s, want hybrid", got)
	}
	streams, err := splitOpusFrames(hybridPkt[1:], code)
	if err != nil || len(streams) != 1 {
		t.Fatalf("split transition packet: streams=%d err=%v", len(streams), err)
	}
	sd, err := silk.NewDecoderWithFrameMs(16000, channels, 20)
	if err != nil {
		t.Fatalf("silk.NewDecoderWithFrameMs: %v", err)
	}
	rangeDec := entcode.NewDecoder(streams[0])
	if _, err := sd.DecodeMultiWithDecoder(rangeDec, 1); err != nil {
		t.Fatalf("decode SILK symbols: %v", err)
	}
	if !rangeDec.DecodeBitLogp(12) {
		t.Fatalf("hybrid redundancy flag=false, want true")
	}
	if !rangeDec.DecodeBitLogp(1) {
		t.Fatalf("celt_to_silk=0, want 1")
	}
	redBytes := int(rangeDec.DecodeUint(256)) + 2
	if redBytes < 2 || redBytes >= len(streams[0]) {
		t.Fatalf("redundancy bytes=%d, stream=%d", redBytes, len(streams[0]))
	}

	out, err := dec.DecodeFloat(hybridPkt)
	if err != nil {
		t.Fatalf("transition DecodeFloat: %v", err)
	}
	rms, peak := strictSignalStats(out)
	if len(out) != frameSize*channels || math.IsNaN(rms) || rms < 1e-3 || peak > 1.5 {
		t.Fatalf("transition output invalid: len=%d rms=%g peak=%g", len(out), rms, peak)
	}
}

func TestComputeRedundancyBytes(t *testing.T) {
	for _, tc := range []struct {
		name      string
		frameRate int
		channels  int
	}{
		{name: "zero-frame-rate", frameRate: 0, channels: 1},
		{name: "negative-frame-rate", frameRate: -1, channels: 1},
		{name: "zero-channels", frameRate: 50, channels: 0},
		{name: "negative-channels", frameRate: 50, channels: -1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := computeRedundancyBytes(160, 64000, tc.frameRate, tc.channels); got != 0 {
				t.Fatalf("computeRedundancyBytes invalid inputs=%d, want 0", got)
			}
		})
	}
	// 48 kHz mono 20 ms @ 64 kbps: frameRate=50, maxDataBytes=160. Redundancy must
	// engage (the transition smoothing relies on it) and stay within [2,257].
	got := computeRedundancyBytes(160, 64000, 50, 1)
	if got < 5 || got > 257 {
		t.Fatalf("computeRedundancyBytes(160,64000,50,1)=%d, want a usable size in [5,257]", got)
	}
	// At a tiny budget redundancy is not worthwhile and must return 0 so the
	// encoder falls back to a plain hybrid frame (decoder relies on PLC).
	if z := computeRedundancyBytes(8, 64000, 50, 1); z != 0 {
		t.Fatalf("computeRedundancyBytes with tiny budget=%d, want 0", z)
	}
	// Larger budgets must never exceed the 257-byte cap.
	if hi := computeRedundancyBytes(1275, 510000, 50, 2); hi > 257 {
		t.Fatalf("computeRedundancyBytes high=%d, want <=257", hi)
	}
}

func TestHybridHighBandActivityRejectsInvalidChannels(t *testing.T) {
	for _, channels := range []int{0, -1} {
		if got := hybridHighBandActivity([]float64{0.1, -0.1}, channels); got != 0 {
			t.Fatalf("channels=%d activity=%v, want 0", channels, got)
		}
	}
}

func TestEncoderHybridSelectionBoundariesStrict(t *testing.T) {
	cases := []struct {
		name       string
		rate       int
		channels   int
		app        Application
		configure  func(*Encoder) error
		wantMode   string
		wantConfig int
		wantBW     int
	}{
		{
			name:       "48k-voip-fullband-hybrid",
			rate:       48000,
			channels:   1,
			app:        ApplicationVOIP,
			wantMode:   "hybrid",
			wantConfig: 15,
			wantBW:     BandwidthFullband,
		},
		{
			name:       "48k-voip-lowband-broadband-auto-falls-back-celt",
			rate:       48000,
			channels:   1,
			app:        ApplicationVOIP,
			wantMode:   "celt",
			wantConfig: 19,
			wantBW:     BandwidthFullband,
		},
		{
			name:     "48k-voip-forced-swb-hybrid",
			rate:     48000,
			channels: 1,
			app:      ApplicationVOIP,
			configure: func(e *Encoder) error {
				return e.SetBandwidth(BandwidthSuperWideband)
			},
			wantMode:   "hybrid",
			wantConfig: 13,
			wantBW:     BandwidthSuperWideband,
		},
		{
			name:     "48k-voip-forced-swb-ignores-max-wideband",
			rate:     48000,
			channels: 1,
			app:      ApplicationVOIP,
			configure: func(e *Encoder) error {
				if err := e.SetMaxBandwidth(BandwidthWideband); err != nil {
					return err
				}
				return e.SetBandwidth(BandwidthSuperWideband)
			},
			wantMode:   "hybrid",
			wantConfig: 13,
			wantBW:     BandwidthSuperWideband,
		},
		{
			name:     "24k-voip-forced-fullband-clamps-swb-hybrid",
			rate:     24000,
			channels: 1,
			app:      ApplicationVOIP,
			configure: func(e *Encoder) error {
				return e.SetBandwidth(BandwidthFullband)
			},
			wantMode:   "hybrid",
			wantConfig: 13,
			wantBW:     BandwidthSuperWideband,
		},
		{
			name:     "48k-voice-with-max-wideband-falls-back-celt",
			rate:     48000,
			channels: 1,
			app:      ApplicationVOIP,
			configure: func(e *Encoder) error {
				return e.SetMaxBandwidth(BandwidthWideband)
			},
			wantMode: "celt",
			wantBW:   BandwidthWideband,
		},
		{
			name:     "audio-with-voice-hint-can-hybrid",
			rate:     48000,
			channels: 2,
			app:      ApplicationAudio,
			configure: func(e *Encoder) error {
				e.SetSignalType(SignalVoice)
				return nil
			},
			wantMode:   "hybrid",
			wantConfig: 15,
			wantBW:     BandwidthFullband,
		},
		{
			name:     "voip-with-music-hint-stays-celt",
			rate:     48000,
			channels: 1,
			app:      ApplicationVOIP,
			configure: func(e *Encoder) error {
				e.SetSignalType(SignalMusic)
				return nil
			},
			wantMode: "celt",
			wantBW:   BandwidthFullband,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			enc, err := NewEncoder(tc.rate, tc.channels, tc.app)
			if err != nil {
				t.Fatalf("NewEncoder: %v", err)
			}
			if err := enc.SetBitrate(64000); err != nil {
				t.Fatalf("SetBitrate: %v", err)
			}
			if tc.configure != nil {
				if err := tc.configure(enc); err != nil {
					t.Fatalf("configure: %v", err)
				}
			}
			if got := enc.Bandwidth(); got != tc.wantBW {
				t.Fatalf("Bandwidth()=%d, want %d", got, tc.wantBW)
			}

			frameSize := tc.rate * 20 / 1000
			pcm := strictSpeechLikeFrame(tc.rate, tc.channels, 0, frameSize)
			if tc.name == "48k-voip-lowband-broadband-auto-falls-back-celt" {
				for i := range pcm {
					pcm[i] = 0
					for k := 3; k <= 67; k++ {
						phase := float64((k*37)%101) * 0.061
						pcm[i] += 0.01 * math.Sin(2*math.Pi*float64(k*i)/1024+phase)
					}
				}
			}
			if tc.wantMode == "hybrid" {
				pcm = strictHybridWidebandFrame(tc.rate, tc.channels, 0, frameSize)
			}
			pkt, err := enc.EncodeFloat(pcm, frameSize)
			if err != nil {
				t.Fatalf("EncodeFloat: %v", err)
			}
			config := int(pkt[0] >> 3)
			if got := strictOpusMode(config); got != tc.wantMode {
				t.Fatalf("TOC config=%d mode=%s, want %s", config, got, tc.wantMode)
			}
			if tc.wantConfig != 0 && config != tc.wantConfig {
				t.Fatalf("TOC config=%d, want %d", config, tc.wantConfig)
			}
		})
	}
}

func TestEncoderHybridKeepsSteadyAndHarmonicVoice(t *testing.T) {
	for _, tc := range []struct {
		name       string
		rate       int
		gen        func(int) []float64
		wantConfig int
	}{
		{
			name: "48k-pure-tone",
			rate: 48000,
			gen: func(n int) []float64 {
				return genTone(n, 1000, 48000)
			},
			wantConfig: 15,
		},
		{
			name: "48k-speech-harmonic",
			rate: 48000,
			gen: func(n int) []float64 {
				return strictSpeechLikeFrame(48000, 1, 0, n)
			},
			wantConfig: 15,
		},
		{
			name: "24k-pure-tone",
			rate: 24000,
			gen: func(n int) []float64 {
				return genTone(n, 1000, 24000)
			},
			wantConfig: 13,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			enc, err := NewEncoder(tc.rate, 1, ApplicationVOIP)
			if err != nil {
				t.Fatalf("NewEncoder: %v", err)
			}
			if err := enc.SetBitrate(64000); err != nil {
				t.Fatalf("SetBitrate: %v", err)
			}
			frameSize := tc.rate * 20 / 1000
			pkt, err := enc.EncodeFloat(tc.gen(frameSize), frameSize)
			if err != nil {
				t.Fatalf("EncodeFloat: %v", err)
			}
			if config := int(pkt[0] >> 3); config != tc.wantConfig {
				t.Fatalf("TOC config=%d, want hybrid config %d", config, tc.wantConfig)
			}
		})
	}
}

func TestEncoderSILKOnlyModeSelectionMatrix(t *testing.T) {
	cases := []struct {
		name       string
		rate       int
		channels   int
		app        Application
		bitrate    int
		configure  func(*Encoder) error
		wantSILK   bool
		wantBW     int
		wantConfig int
	}{
		{
			name:       "voip_at_40kbps_selects_silk",
			rate:       16000,
			channels:   1,
			app:        ApplicationVOIP,
			bitrate:    40000,
			wantSILK:   true,
			wantBW:     BandwidthWideband,
			wantConfig: 9,
		},
		{
			name:     "voip_above_40kbps_stays_celt",
			rate:     16000,
			channels: 1,
			app:      ApplicationVOIP,
			bitrate:  40001,
			wantSILK: false,
			wantBW:   BandwidthWideband,
		},
		{
			name:     "audio_default_stays_celt",
			rate:     16000,
			channels: 1,
			app:      ApplicationAudio,
			bitrate:  24000,
			wantSILK: false,
			wantBW:   BandwidthWideband,
		},
		{
			name:     "audio_signal_voice_selects_silk",
			rate:     16000,
			channels: 1,
			app:      ApplicationAudio,
			bitrate:  24000,
			configure: func(enc *Encoder) error {
				enc.SetSignalType(SignalVoice)
				return nil
			},
			wantSILK:   true,
			wantBW:     BandwidthWideband,
			wantConfig: 9,
		},
		{
			name:     "voip_signal_music_stays_celt",
			rate:     16000,
			channels: 1,
			app:      ApplicationVOIP,
			bitrate:  24000,
			configure: func(enc *Encoder) error {
				enc.SetSignalType(SignalMusic)
				return nil
			},
			wantSILK: false,
			wantBW:   BandwidthWideband,
		},
		{
			name:     "voip_signal_auto_selects_silk",
			rate:     16000,
			channels: 1,
			app:      ApplicationVOIP,
			bitrate:  24000,
			configure: func(enc *Encoder) error {
				enc.SetSignalType(SignalAuto)
				return nil
			},
			wantSILK:   true,
			wantBW:     BandwidthWideband,
			wantConfig: 9,
		},
		{
			name:     "restricted_low_delay_voice_stays_celt",
			rate:     16000,
			channels: 1,
			app:      ApplicationRestrictedLowDelay,
			bitrate:  24000,
			configure: func(enc *Encoder) error {
				enc.SetSignalType(SignalVoice)
				return nil
			},
			wantSILK: false,
			wantBW:   BandwidthWideband,
		},
		{
			name:       "stereo_voice_selects_silk",
			rate:       16000,
			channels:   2,
			app:        ApplicationVOIP,
			bitrate:    24000,
			wantSILK:   true,
			wantBW:     BandwidthWideband,
			wantConfig: 9,
		},
		{
			name:       "non_native_48k_voice_downsamples_to_silk",
			rate:       48000,
			channels:   1,
			app:        ApplicationVOIP,
			bitrate:    24000,
			wantSILK:   true,
			wantBW:     BandwidthWideband,
			wantConfig: 9,
		},
		{
			name:       "non_native_24k_voice_downsamples_to_silk",
			rate:       24000,
			channels:   1,
			app:        ApplicationVOIP,
			bitrate:    24000,
			wantSILK:   true,
			wantBW:     BandwidthWideband,
			wantConfig: 9,
		},
		{
			name:     "forced_bandwidth_below_native_stays_celt",
			rate:     16000,
			channels: 1,
			app:      ApplicationVOIP,
			bitrate:  24000,
			configure: func(enc *Encoder) error {
				return enc.SetBandwidth(BandwidthNarrowband)
			},
			wantSILK: false,
			wantBW:   BandwidthNarrowband,
		},
		{
			name:     "max_bandwidth_below_native_stays_celt",
			rate:     16000,
			channels: 1,
			app:      ApplicationVOIP,
			bitrate:  24000,
			configure: func(enc *Encoder) error {
				return enc.SetMaxBandwidth(BandwidthNarrowband)
			},
			wantSILK: false,
			wantBW:   BandwidthNarrowband,
		},
		{
			name:     "forced_downsampled_native_bandwidth_keeps_silk",
			rate:     48000,
			channels: 1,
			app:      ApplicationVOIP,
			bitrate:  24000,
			configure: func(enc *Encoder) error {
				return enc.SetBandwidth(BandwidthWideband)
			},
			wantSILK:   true,
			wantBW:     BandwidthWideband,
			wantConfig: 9,
		},
		{
			name:     "forced_native_bandwidth_ignores_lower_max",
			rate:     48000,
			channels: 1,
			app:      ApplicationVOIP,
			bitrate:  24000,
			configure: func(enc *Encoder) error {
				if err := enc.SetMaxBandwidth(BandwidthNarrowband); err != nil {
					return err
				}
				return enc.SetBandwidth(BandwidthWideband)
			},
			wantSILK:   true,
			wantBW:     BandwidthWideband,
			wantConfig: 9,
		},
		{
			name:     "forced_fullband_48k_stays_celt",
			rate:     48000,
			channels: 1,
			app:      ApplicationVOIP,
			bitrate:  24000,
			configure: func(enc *Encoder) error {
				return enc.SetBandwidth(BandwidthFullband)
			},
			wantSILK: false,
			wantBW:   BandwidthFullband,
		},
		{
			name:     "max_native_bandwidth_keeps_silk",
			rate:     16000,
			channels: 1,
			app:      ApplicationVOIP,
			bitrate:  24000,
			configure: func(enc *Encoder) error {
				return enc.SetMaxBandwidth(BandwidthWideband)
			},
			wantSILK:   true,
			wantBW:     BandwidthWideband,
			wantConfig: 9,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			enc, err := NewEncoder(tc.rate, tc.channels, tc.app)
			if err != nil {
				t.Fatalf("NewEncoder: %v", err)
			}
			if err := enc.SetBitrate(tc.bitrate); err != nil {
				t.Fatalf("SetBitrate: %v", err)
			}
			if tc.configure != nil {
				if err := tc.configure(enc); err != nil {
					t.Fatalf("configure: %v", err)
				}
			}
			if got := enc.Bandwidth(); got != tc.wantBW {
				t.Fatalf("Bandwidth()=%d, want %d", got, tc.wantBW)
			}

			frameSize := tc.rate * 20 / 1000
			pkt, err := enc.Encode(generateSine(220, tc.rate, tc.channels, frameSize), frameSize)
			if err != nil {
				t.Fatalf("Encode: %v", err)
			}
			config := int(pkt[0] >> 3)
			gotSILK := config < 12
			if gotSILK != tc.wantSILK {
				t.Fatalf("TOC config=%d, SILK=%v, want SILK=%v", config, gotSILK, tc.wantSILK)
			}
			if tc.wantConfig != 0 && config != tc.wantConfig {
				t.Fatalf("TOC config=%d, want %d", config, tc.wantConfig)
			}
			if !tc.wantSILK && config < 16 {
				t.Fatalf("TOC config=%d, want CELT-only fallback rather than hybrid/SILK", config)
			}
		})
	}
}

func TestEncoderSILKOnlyDownsampledVoiceRoundTrip(t *testing.T) {
	for _, tc := range []struct {
		rate     int
		channels int
	}{
		{24000, 1},
		{48000, 1},
		{48000, 2},
	} {
		t.Run(rateName(tc.rate)+"/"+channelName(tc.channels), func(t *testing.T) {
			enc, err := NewEncoder(tc.rate, tc.channels, ApplicationVOIP)
			if err != nil {
				t.Fatalf("NewEncoder: %v", err)
			}
			if err := enc.SetBitrate(24000); err != nil {
				t.Fatalf("SetBitrate: %v", err)
			}
			if got := enc.Bandwidth(); got != BandwidthWideband {
				t.Fatalf("Bandwidth()=%d, want wideband SILK", got)
			}

			frameSize := tc.rate * 20 / 1000
			pcm := generateSine(220, tc.rate, tc.channels, frameSize)
			pkt, err := enc.Encode(pcm, frameSize)
			if err != nil {
				t.Fatalf("Encode: %v", err)
			}
			if config := int(pkt[0] >> 3); config != 9 {
				t.Fatalf("TOC config=%d, want SILK WB 20ms config 9 (toc=0x%02x)", config, pkt[0])
			}
			if gotStereo := (pkt[0] & 0x04) != 0; gotStereo != (tc.channels == 2) {
				t.Fatalf("TOC stereo=%v, want %v", gotStereo, tc.channels == 2)
			}

			dec, err := NewDecoder(tc.rate, tc.channels)
			if err != nil {
				t.Fatalf("NewDecoder: %v", err)
			}
			decoded, err := dec.DecodeFloat(pkt)
			if err != nil {
				t.Fatalf("DecodeFloat: %v", err)
			}
			want := frameSize * tc.channels
			if len(decoded) != want {
				t.Fatalf("decoded samples=%d, want %d", len(decoded), want)
			}
		})
	}
}

func TestEncoderSILKOnlyVBRDTXAndPaddingStillSelectSILK(t *testing.T) {
	const rate = 16000
	frameSize := rate * 20 / 1000

	enc, err := NewEncoder(rate, 1, ApplicationVOIP)
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	if err := enc.SetBitrate(24000); err != nil {
		t.Fatalf("SetBitrate: %v", err)
	}
	enc.SetVBR(true)
	enc.SetVBRConstraint(false)
	enc.SetDTX(true)
	enc.SetPacketPadding(5)

	pkt, err := enc.Encode(make([]int16, frameSize), frameSize)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if config := int(pkt[0] >> 3); config != 9 {
		t.Fatalf("TOC config=%d, want SILK WB 20ms config 9", config)
	}
	if code := int(pkt[0] & 0x03); code != 3 {
		t.Fatalf("count code=%d, want code 3 when padding is requested", code)
	}
}

func TestEncoderSILKOnlyCBRPacketSizeTracksBitrateAndDuration(t *testing.T) {
	t.Setenv("OPUS_SILK_RC_SNR", "0")
	const rate = 16000
	base := rate * 20 / 1000

	for _, tc := range []struct {
		bitrate int
		mult    int
	}{
		{bitrate: 12000, mult: 1},
		{bitrate: 24000, mult: 1},
		{bitrate: 40000, mult: 1},
		{bitrate: 24000, mult: 3},
	} {
		t.Run(fmt.Sprintf("%dbps/%dms", tc.bitrate, tc.mult*20), func(t *testing.T) {
			enc, err := NewEncoder(rate, 1, ApplicationVOIP)
			if err != nil {
				t.Fatalf("NewEncoder: %v", err)
			}
			if err := enc.SetBitrate(tc.bitrate); err != nil {
				t.Fatalf("SetBitrate: %v", err)
			}

			frameSize := base * tc.mult
			pkt, err := enc.Encode(generateSine(220, rate, 1, frameSize), frameSize)
			if err != nil {
				t.Fatalf("Encode: %v", err)
			}
			wantPayload := tc.bitrate * (20 * tc.mult) / 1000 / 8
			if want := 1 + wantPayload; len(pkt) < want {
				t.Fatalf("packet bytes=%d, want at least %d for active %d bps/%d ms CBR SILK", len(pkt), want, tc.bitrate, 20*tc.mult)
			}
			if config := int(pkt[0] >> 3); config != 9 && config != 11 {
				t.Fatalf("TOC config=%d, want SILK WB 20/60ms config", config)
			}
		})
	}
}

func TestEncoderSILKOnlyStereoSingleFrameCBRKeepsCode0(t *testing.T) {
	const (
		rate      = 16000
		channels  = 2
		bitrate   = 32000
		frameSize = rate * 20 / 1000
	)
	enc, err := NewEncoder(rate, channels, ApplicationVOIP)
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	if err := enc.SetBitrate(bitrate); err != nil {
		t.Fatalf("SetBitrate: %v", err)
	}
	enc.SetVBR(false)

	signals := make([][]float64, 0, 3)
	for _, tc := range []struct {
		freq float64
		amp  float64
	}{
		{freq: 180, amp: 0.18},
		{freq: 240, amp: 0.25},
		{freq: 360, amp: 0.12},
	} {
		pcm := make([]float64, frameSize*channels)
		for i := 0; i < frameSize; i++ {
			v := tc.amp * math.Sin(2*math.Pi*tc.freq*float64(i)/rate)
			pcm[2*i], pcm[2*i+1] = v, v
		}
		signals = append(signals, pcm)
	}

	dec, err := NewDecoder(rate, channels)
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	for i, pcm := range signals {
		pkt, err := enc.EncodeFloat(pcm, frameSize)
		if err != nil {
			t.Fatalf("frame %d EncodeFloat: %v", i, err)
		}
		if code := int(pkt[0] & 0x03); code != 0 {
			t.Fatalf("frame %d count code=%d, want compact single-frame code 0", i, code)
		}
		if _, err := dec.DecodeFloat(pkt); err != nil {
			t.Fatalf("frame %d DecodeFloat: %v", i, err)
		}
	}
}

func TestEncoderSILKOnlyVBRAndDTXDoNotUseCBRPadding(t *testing.T) {
	const (
		rate     = 16000
		bitrate  = 24000
		cbrBytes = 1 + bitrate*20/1000/8
	)
	frameSize := rate * 20 / 1000

	for _, tc := range []struct {
		name      string
		configure func(*Encoder)
	}{
		{
			name: "cvbr",
			configure: func(e *Encoder) {
				e.SetVBR(true)
			},
		},
		{
			name: "dtx",
			configure: func(e *Encoder) {
				e.SetDTX(true)
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			enc, err := NewEncoder(rate, 1, ApplicationVOIP)
			if err != nil {
				t.Fatalf("NewEncoder: %v", err)
			}
			if err := enc.SetBitrate(bitrate); err != nil {
				t.Fatalf("SetBitrate: %v", err)
			}
			tc.configure(enc)

			pcm := generateSine(220, rate, 1, frameSize)
			if tc.name == "dtx" {
				pcm = make([]int16, frameSize)
			}
			pkt, err := enc.Encode(pcm, frameSize)
			if err != nil {
				t.Fatalf("Encode: %v", err)
			}
			if len(pkt) >= cbrBytes {
				t.Fatalf("%s packet bytes=%d, want less than CBR padded size %d", tc.name, len(pkt), cbrBytes)
			}
			if config := int(pkt[0] >> 3); config != 9 {
				t.Fatalf("TOC config=%d, want SILK WB 20ms config 9", config)
			}
		})
	}
}

func TestEncoderHybridVoiceRoundTrip(t *testing.T) {
	for _, tc := range []struct {
		name       string
		rate       int
		channels   int
		bitrate    int
		packetMs   int
		wantConfig int
		wantCode   int
	}{
		{name: "swb_24k_mono", rate: 24000, channels: 1, bitrate: 64000, packetMs: 20, wantConfig: 13, wantCode: 0},
		{name: "fb_48k_mono", rate: 48000, channels: 1, bitrate: 64000, packetMs: 20, wantConfig: 15, wantCode: 0},
		{name: "fb_48k_stereo_multiframe", rate: 48000, channels: 2, bitrate: 96000, packetMs: 40, wantConfig: 15, wantCode: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			enc, err := NewEncoder(tc.rate, tc.channels, ApplicationVOIP)
			if err != nil {
				t.Fatalf("NewEncoder: %v", err)
			}
			if err := enc.SetBitrate(tc.bitrate); err != nil {
				t.Fatalf("SetBitrate: %v", err)
			}

			frameSize := tc.rate * tc.packetMs / 1000
			pkt, err := enc.EncodeFloat(strictHybridWidebandFrame(tc.rate, tc.channels, 0, frameSize), frameSize)
			if err != nil {
				t.Fatalf("EncodeFloat: %v", err)
			}
			config := int(pkt[0] >> 3)
			if config != tc.wantConfig {
				t.Fatalf("TOC config=%d, want hybrid config %d (toc=0x%02x)", config, tc.wantConfig, pkt[0])
			}
			if code := int(pkt[0] & 0x03); code != tc.wantCode {
				t.Fatalf("count code=%d, want %d", code, tc.wantCode)
			}
			if gotStereo := (pkt[0] & 0x04) != 0; gotStereo != (tc.channels == 2) {
				t.Fatalf("TOC stereo=%v, want %v", gotStereo, tc.channels == 2)
			}

			dec, err := NewDecoder(tc.rate, tc.channels)
			if err != nil {
				t.Fatalf("NewDecoder: %v", err)
			}
			decoded, err := dec.DecodeFloat(pkt)
			if err != nil {
				t.Fatalf("DecodeFloat: %v", err)
			}
			want := frameSize * tc.channels
			if len(decoded) != want {
				t.Fatalf("decoded samples=%d, want %d", len(decoded), want)
			}
		})
	}
}

func rateName(rate int) string {
	switch rate {
	case 8000:
		return "8k"
	case 12000:
		return "12k"
	case 24000:
		return "24k"
	case 48000:
		return "48k"
	default:
		return "16k"
	}
}

func channelName(channels int) string {
	if channels == 2 {
		return "stereo"
	}
	return "mono"
}

func multName(mult int) string {
	switch mult {
	case 2:
		return "40ms"
	case 3:
		return "60ms"
	default:
		return "120ms"
	}
}

func strictSILKDurationIndex(mult, channels int) int {
	if channels == 2 {
		switch mult {
		case 2:
			return 2
		default:
			return 1
		}
	}
	switch mult {
	case 2, 4:
		return 2
	case 3, 6:
		return 3
	default:
		return 1
	}
}

func strictSILKCountCode(mult, channels int) int {
	if channels == 2 {
		switch mult {
		case 1, 2:
			return 0
		default:
			return 3
		}
	}
	switch mult {
	case 1, 2, 3:
		return 0
	case 4, 6:
		return 2
	default:
		return 3
	}
}

func strictOpusMode(config int) string {
	switch {
	case config < 12:
		return "silk"
	case config < 16:
		return "hybrid"
	default:
		return "celt"
	}
}

func strictSpeechLikeFrame(rate, channels, start, n int) []float64 {
	out := make([]float64, n*channels)
	for i := 0; i < n; i++ {
		t := float64(start+i) / float64(rate)
		env := 0.42 + 0.18*math.Sin(2*math.Pi*2.7*t+0.3)
		left := env * (0.34*math.Sin(2*math.Pi*175*t) +
			0.13*math.Sin(2*math.Pi*350*t+0.4) +
			0.07*math.Sin(2*math.Pi*700*t+0.8))
		out[i*channels] = left
		if channels == 2 {
			right := env * (0.31*math.Sin(2*math.Pi*183*t+0.2) +
				0.11*math.Sin(2*math.Pi*366*t+0.7) +
				0.06*math.Sin(2*math.Pi*732*t+1.0))
			out[i*channels+1] = right
		}
	}
	return out
}

func strictHybridWidebandFrame(rate, channels, start, n int) []float64 {
	out := strictSpeechLikeFrame(rate, channels, start, n)
	highFreq := 10000.0
	if rate >= 48000 {
		highFreq = 16000.0
	}
	for i := 0; i < n; i++ {
		t := float64(start+i) / float64(rate)
		left := 0.045 * math.Sin(2*math.Pi*highFreq*t+0.11)
		out[i*channels] += left
		if channels == 2 {
			right := 0.04 * math.Sin(2*math.Pi*highFreq*t+0.73)
			out[i*channels+1] += right
		}
	}
	return out
}

func strictSignalStats(x []float64) (rms, peak float64) {
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
