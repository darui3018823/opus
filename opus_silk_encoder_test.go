package opus

import "testing"

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
			pkt, err := enc.Encode(generateSine(220, tc.rate, tc.channels, frameSize), frameSize)
			if err != nil {
				t.Fatalf("Encode: %v", err)
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
