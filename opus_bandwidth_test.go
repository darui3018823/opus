package opus

import (
	"math"
	"testing"
)

// TestBandwidthSelection exercises the encoder's coded-bandwidth selection:
// sample-rate Nyquist limit, the max-bandwidth cap, an explicit forced bandwidth,
// and the bitrate-based reduction.
func TestBandwidthSelection(t *testing.T) {
	// Nyquist limit from the input sample rate (default 64 kbps stays at the cap).
	rateBW := map[int]int{
		8000:  BandwidthNarrowband,
		12000: BandwidthWideband,
		16000: BandwidthWideband,
		24000: BandwidthSuperWideband,
		48000: BandwidthFullband,
	}
	for rate, want := range rateBW {
		enc, err := NewEncoder(rate, 1, ApplicationAudio)
		if err != nil {
			t.Fatal(err)
		}
		if got := enc.Bandwidth(); got != want {
			t.Errorf("rate=%d: auto bandwidth = %d, want %d", rate, got, want)
		}
	}

	// Max-bandwidth cap (at 48 kHz so Nyquist is not the binding limit).
	enc, _ := NewEncoder(48000, 2, ApplicationAudio)
	if err := enc.SetMaxBandwidth(BandwidthWideband); err != nil {
		t.Fatal(err)
	}
	if got := enc.Bandwidth(); got != BandwidthWideband {
		t.Errorf("max=WB: bandwidth = %d, want WB", got)
	}

	// Explicit force overrides the cap.
	if err := enc.SetBandwidth(BandwidthSuperWideband); err != nil {
		t.Fatal(err)
	}
	if got := enc.Bandwidth(); got != BandwidthSuperWideband {
		t.Errorf("forced=SWB: bandwidth = %d, want SWB", got)
	}
	// ...but stays clamped to the input Nyquist limit.
	enc8, _ := NewEncoder(8000, 1, ApplicationAudio)
	if err := enc8.SetBandwidth(BandwidthFullband); err != nil {
		t.Fatal(err)
	}
	if got := enc8.Bandwidth(); got != BandwidthNarrowband {
		t.Errorf("forced=FB at 8kHz: bandwidth = %d, want NB (Nyquist clamp)", got)
	}

	// Returning to automatic selection still honours the max-bandwidth cap (WB,
	// set above on this encoder).
	if err := enc.SetBandwidth(BandwidthAuto); err != nil {
		t.Fatal(err)
	}
	if got := enc.Bandwidth(); got != BandwidthWideband {
		t.Errorf("auto after force (max=WB): bandwidth = %d, want WB", got)
	}
	// Lifting the cap restores fullband.
	if err := enc.SetMaxBandwidth(BandwidthFullband); err != nil {
		t.Fatal(err)
	}
	if got := enc.Bandwidth(); got != BandwidthFullband {
		t.Errorf("auto, cap lifted: bandwidth = %d, want FB", got)
	}

	// Bitrate-based reduction (48 kHz input, automatic selection).
	for _, tc := range []struct {
		bitrate int
		want    int
	}{
		{12000, BandwidthNarrowband},
		{24000, BandwidthWideband},
		{40000, BandwidthSuperWideband},
		{64000, BandwidthFullband},
	} {
		e, _ := NewEncoder(48000, 1, ApplicationAudio)
		if err := e.SetBitrate(tc.bitrate); err != nil {
			t.Fatal(err)
		}
		if got := e.Bandwidth(); got != tc.want {
			t.Errorf("bitrate=%d: bandwidth = %d, want %d", tc.bitrate, got, tc.want)
		}
	}

	// Invalid inputs are rejected.
	if err := enc.SetMaxBandwidth(12345); err == nil {
		t.Error("SetMaxBandwidth accepted an invalid value")
	}
	if err := enc.SetBandwidth(12345); err == nil {
		t.Error("SetBandwidth accepted an invalid value")
	}
}

// TestApplicationBandwidthCoupling verifies the application (VOIP vs Audio)
// shifts public bandwidth selection. At low speech bitrates VOIP now enters the
// WB SILK-only path; above the SILK ceiling it falls back to the CELT voice
// thresholds, which widen more slowly than Audio.
func TestApplicationBandwidthCoupling(t *testing.T) {
	cases := []struct {
		bitrate   int
		wantVoIP  int
		wantAudio int
	}{
		{18000, BandwidthWideband, BandwidthWideband},      // voice uses WB SILK, music WB CELT
		{30000, BandwidthWideband, BandwidthSuperWideband}, // voice uses WB SILK, music SWB
		{48000, BandwidthSuperWideband, BandwidthFullband}, // voice SWB, music FB
		{64000, BandwidthFullband, BandwidthFullband},      // both FB at default
	}
	for _, tc := range cases {
		voip, _ := NewEncoder(48000, 1, ApplicationVOIP)
		if err := voip.SetBitrate(tc.bitrate); err != nil {
			t.Fatal(err)
		}
		audio, _ := NewEncoder(48000, 1, ApplicationAudio)
		if err := audio.SetBitrate(tc.bitrate); err != nil {
			t.Fatal(err)
		}
		if got := voip.Bandwidth(); got != tc.wantVoIP {
			t.Errorf("bitrate=%d VOIP bandwidth = %d, want %d", tc.bitrate, got, tc.wantVoIP)
		}
		if got := audio.Bandwidth(); got != tc.wantAudio {
			t.Errorf("bitrate=%d Audio bandwidth = %d, want %d", tc.bitrate, got, tc.wantAudio)
		}
	}

	// SetApplication re-derives the coupling on an existing encoder.
	enc, _ := NewEncoder(48000, 1, ApplicationAudio)
	if err := enc.SetBitrate(30000); err != nil {
		t.Fatal(err)
	}
	if got := enc.Bandwidth(); got != BandwidthSuperWideband {
		t.Errorf("Audio@30k bandwidth = %d, want SWB", got)
	}
	enc.SetApplication(ApplicationVOIP)
	if got := enc.Bandwidth(); got != BandwidthWideband {
		t.Errorf("after SetApplication(VOIP)@30k bandwidth = %d, want WB", got)
	}

	// RestrictedLowDelay uses the music (audio) thresholds.
	rld, _ := NewEncoder(48000, 1, ApplicationRestrictedLowDelay)
	if err := rld.SetBitrate(30000); err != nil {
		t.Fatal(err)
	}
	if got := rld.Bandwidth(); got != BandwidthSuperWideband {
		t.Errorf("RestrictedLowDelay@30k bandwidth = %d, want SWB (music thresholds)", got)
	}
}

// TestBandwidthRoundTrip encodes a low-frequency tone (well within every CELT
// bandwidth) at each forced bandwidth and checks the packet carries the matching
// config and decodes back to the tone with reasonable delay-aligned SNR. This
// confirms the narrowed CELT end-band produces a valid, decodable stream.
func TestBandwidthRoundTrip(t *testing.T) {
	const sampleRate = 48000
	const frameSize = 960
	const nFrames = 16

	cases := []struct {
		name     string
		bw       int
		loConfig int
		hiConfig int
	}{
		{"NB", BandwidthNarrowband, 16, 19},
		{"WB", BandwidthWideband, 20, 23},
		{"SWB", BandwidthSuperWideband, 24, 27},
		{"FB", BandwidthFullband, 28, 31},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			enc, err := NewEncoder(sampleRate, 1, ApplicationAudio)
			if err != nil {
				t.Fatal(err)
			}
			if err := enc.SetBandwidth(tc.bw); err != nil {
				t.Fatal(err)
			}
			dec, err := NewDecoder(sampleRate, 1)
			if err != nil {
				t.Fatal(err)
			}

			var in, out []float64
			for f := 0; f < nFrames; f++ {
				frame := make([]float64, frameSize)
				for i := 0; i < frameSize; i++ {
					frame[i] = 0.5 * math.Sin(2*math.Pi*1000*float64(f*frameSize+i)/sampleRate)
				}
				pkt, err := enc.EncodeFloat(frame, frameSize)
				if err != nil {
					t.Fatalf("frame %d: %v", f, err)
				}
				config := int(pkt[0] >> 3)
				if config < tc.loConfig || config > tc.hiConfig {
					t.Fatalf("frame %d: config %d not in CELT %s range %d-%d",
						f, config, tc.name, tc.loConfig, tc.hiConfig)
				}
				dout, err := dec.DecodeFloat(pkt)
				if err != nil {
					t.Fatalf("frame %d: decode: %v", f, err)
				}
				in = append(in, frame...)
				out = append(out, dout...)
			}

			snr := bandwidthAlignedSNR(in, out, frameSize)
			t.Logf("%s: 1kHz aligned SNR = %.2f dB", tc.name, snr)
			if snr < 20 {
				t.Errorf("%s: in-band tone aligned SNR %.2f dB below 20 dB", tc.name, snr)
			}
		})
	}
}

// bandwidthAlignedSNR returns the best delay/gain-aligned SNR (dB) of out vs in
// (mono) over a steady middle region.
func bandwidthAlignedSNR(in, out []float64, frameSize int) float64 {
	lo, hi := 4*frameSize, 12*frameSize
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
	return 20 * math.Log10(inRMS/bestErr)
}
