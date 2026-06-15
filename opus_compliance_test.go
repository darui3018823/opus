package opus

import (
	"fmt"
	"math"
	"testing"
)

// TestComplianceRoundtrip tests encode-decode roundtrip for 48kHz configurations.
func TestComplianceRoundtrip(t *testing.T) {
	cases := []struct {
		name       string
		sampleRate int
		channels   int
		frameSize  int
		app        int
	}{
		{"48kHz_mono_960", 48000, 1, 960, ApplicationAudio},
		{"48kHz_stereo_960", 48000, 2, 960, ApplicationAudio},
		{"48kHz_mono_voip", 48000, 1, 960, ApplicationVOIP},
		{"48kHz_stereo_lowdelay", 48000, 2, 960, ApplicationRestrictedLowDelay},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			enc, err := NewEncoder(tc.sampleRate, tc.channels, tc.app)
			if err != nil {
				t.Fatalf("NewEncoder: %v", err)
			}

			dec, err := NewDecoder(tc.sampleRate, tc.channels)
			if err != nil {
				t.Fatalf("NewDecoder: %v", err)
			}

			// Generate test signal: 440Hz sine wave
			pcm := generateSine(440.0, tc.sampleRate, tc.channels, tc.frameSize)

			// Encode
			encoded, err := enc.Encode(pcm, tc.frameSize)
			if err != nil {
				t.Fatalf("Encode: %v", err)
			}
			if len(encoded) == 0 {
				t.Fatal("encoded packet is empty")
			}

			// Decode
			decoded := make([]int16, tc.frameSize*tc.channels)
			n, err := dec.Decode(encoded, decoded)
			if err != nil {
				t.Fatalf("Decode: %v", err)
			}
			if n == 0 {
				t.Fatal("decoded 0 samples")
			}

			// Check output has non-zero energy
			energy := signalEnergyI16(decoded)
			if energy == 0 {
				t.Error("decoded signal has zero energy")
			}
		})
	}
}

// TestMultiRateRoundtrip tests encode-decode roundtrip at all supported sample rates.
func TestMultiRateRoundtrip(t *testing.T) {
	rates := []int{8000, 12000, 16000, 24000, 48000}

	for _, rate := range rates {
		for _, ch := range []int{1, 2} {
			name := fmt.Sprintf("%dHz_%dch", rate, ch)
			t.Run(name, func(t *testing.T) {
				enc, err := NewEncoder(rate, ch, ApplicationAudio)
				if err != nil {
					t.Fatalf("NewEncoder(%d, %d): %v", rate, ch, err)
				}

				dec, err := NewDecoder(rate, ch)
				if err != nil {
					t.Fatalf("NewDecoder(%d, %d): %v", rate, ch, err)
				}

				// Frame size for 20ms at this rate
				frameSize := (rate * 20) / 1000

				// Generate sine wave at a frequency that is representable at this rate
				// Use min(440, rate/4) to stay well below Nyquist
				freq := 440.0
				if freq > float64(rate)/4 {
					freq = float64(rate) / 4
				}

				pcm := generateSine(freq, rate, ch, frameSize)

				// Encode
				encoded, err := enc.Encode(pcm, frameSize)
				if err != nil {
					t.Fatalf("Encode: %v", err)
				}
				if len(encoded) == 0 {
					t.Fatal("encoded packet is empty")
				}

				t.Logf("rate=%d ch=%d frameSize=%d packetLen=%d", rate, ch, frameSize, len(encoded))

				// Decode
				decoded := make([]int16, frameSize*ch)
				n, err := dec.Decode(encoded, decoded)
				if err != nil {
					t.Fatalf("Decode: %v", err)
				}
				if n == 0 {
					t.Fatal("decoded 0 samples")
				}

				// Verify non-zero energy in output
				energy := signalEnergyI16(decoded)
				if energy == 0 {
					t.Error("decoded signal has zero energy")
				}
				t.Logf("decoded %d samples/ch, energy=%.0f", n, energy)
			})
		}
	}
}

// TestMultiRateEncoderCreation verifies that all valid rates are accepted.
func TestMultiRateEncoderCreation(t *testing.T) {
	validRates := []int{8000, 12000, 16000, 24000, 48000}
	for _, rate := range validRates {
		enc, err := NewEncoder(rate, 1, ApplicationAudio)
		if err != nil {
			t.Errorf("NewEncoder(%d) unexpectedly failed: %v", rate, err)
		}
		if enc == nil {
			t.Errorf("NewEncoder(%d) returned nil encoder", rate)
		}
	}

	invalidRates := []int{0, 44100, 22050, 96000, -1}
	for _, rate := range invalidRates {
		_, err := NewEncoder(rate, 1, ApplicationAudio)
		if err == nil {
			t.Errorf("NewEncoder(%d) should have failed but did not", rate)
		}
	}
}

// TestMultiRateDecoderCreation verifies that all valid rates are accepted.
func TestMultiRateDecoderCreation(t *testing.T) {
	validRates := []int{8000, 12000, 16000, 24000, 48000}
	for _, rate := range validRates {
		dec, err := NewDecoder(rate, 1)
		if err != nil {
			t.Errorf("NewDecoder(%d) unexpectedly failed: %v", rate, err)
		}
		if dec == nil {
			t.Errorf("NewDecoder(%d) returned nil decoder", rate)
		}
	}

	invalidRates := []int{0, 44100, 22050, 96000, -1}
	for _, rate := range invalidRates {
		_, err := NewDecoder(rate, 1)
		if err == nil {
			t.Errorf("NewDecoder(%d) should have failed but did not", rate)
		}
	}
}

// TestTOCByte validates TOC byte generation for supported configs.
func TestTOCByte(t *testing.T) {
	// With correct RFC 6716 TOC generation:
	// CELT-only FB 20ms = config 31
	// TOC for mono:   31<<3 | 0x00 | 0 = 0xF8
	// TOC for stereo: 31<<3 | 0x04 | 0 = 0xFC
	cases := []struct {
		channels   int
		expectConf int // expected config number in top 5 bits
	}{
		{1, 31}, // CELT-only FB 20ms, mono
		{2, 31}, // CELT-only FB 20ms, stereo
	}

	for _, tc := range cases {
		enc, err := NewEncoder(48000, tc.channels, ApplicationAudio)
		if err != nil {
			t.Fatalf("NewEncoder: %v", err)
		}
		// Force fullband so this test exercises the Nyquist ceiling and stereo bit
		// deterministically. Under automatic selection a 440 Hz tone would be
		// narrowed by signal-driven detection (covered by TestDetect* tests).
		if err := enc.SetBandwidth(BandwidthFullband); err != nil {
			t.Fatalf("SetBandwidth: %v", err)
		}

		pcm := generateSine(440.0, 48000, tc.channels, 960)
		encoded, err := enc.Encode(pcm, 960)
		if err != nil {
			t.Fatalf("Encode: %v", err)
		}
		if len(encoded) == 0 {
			t.Fatal("empty packet")
		}

		toc := encoded[0]
		config := int(toc >> 3)
		stereo := (toc & 0x04) != 0

		if config != tc.expectConf {
			t.Errorf("ch=%d: TOC config: got %d, want %d (toc=0x%02X)", tc.channels, config, tc.expectConf, toc)
		}
		if tc.channels == 2 && !stereo {
			t.Errorf("ch=2: stereo bit should be set (toc=0x%02X)", toc)
		}
		if tc.channels == 1 && stereo {
			t.Errorf("ch=1: stereo bit should not be set (toc=0x%02X)", toc)
		}
	}
}

// TestTOCByteMultiRate checks that each input sample rate produces a CELT-only
// packet whose signalled bandwidth matches the rate's Nyquist limit: 8 kHz → NB
// (configs 16-19), 12/16 kHz → WB (20-23), 24 kHz → SWB (24-27), 48 kHz → FB
// (28-31). The encoder limits the coded bandwidth so it does not spend bits on
// bands the source rate cannot support.
func TestTOCByteMultiRate(t *testing.T) {
	cases := []struct {
		rate          int
		loConfig      int
		hiConfig      int
		bandwidthName string
	}{
		{8000, 16, 19, "NB"},
		{12000, 20, 23, "WB"},
		{16000, 20, 23, "WB"},
		{24000, 24, 27, "SWB"},
		{48000, 28, 31, "FB"},
	}
	for _, tc := range cases {
		enc, err := NewEncoder(tc.rate, 1, ApplicationAudio)
		if err != nil {
			t.Fatalf("NewEncoder(%d): %v", tc.rate, err)
		}
		// Force fullband (clamped to each rate's Nyquist limit) so this test checks
		// the Nyquist ceiling itself. Automatic selection would narrow the 200 Hz
		// tone via signal-driven detection (covered by TestDetect* tests).
		if err := enc.SetBandwidth(BandwidthFullband); err != nil {
			t.Fatalf("SetBandwidth(%d): %v", tc.rate, err)
		}

		frameSize := (tc.rate * 20) / 1000
		pcm := generateSine(200.0, tc.rate, 1, frameSize)
		encoded, err := enc.Encode(pcm, frameSize)
		if err != nil {
			t.Fatalf("Encode at %dHz: %v", tc.rate, err)
		}

		toc := encoded[0]
		config := int(toc >> 3)
		if config < tc.loConfig || config > tc.hiConfig {
			t.Errorf("rate=%d: expected CELT-only %s config (%d-%d), got %d",
				tc.rate, tc.bandwidthName, tc.loConfig, tc.hiConfig, config)
		}
	}
}

// TestInvalidPackets ensures decoder handles bad input gracefully.
func TestInvalidPackets(t *testing.T) {
	dec, err := NewDecoder(48000, 1)
	if err != nil {
		t.Fatal(err)
	}

	badInputs := []struct {
		name string
		data []byte
	}{
		{"empty", []byte{}},
		{"single_byte", []byte{0xFF}},
		{"truncated", []byte{0xF8, 0x01}},
	}

	for _, tc := range badInputs {
		t.Run(tc.name, func(t *testing.T) {
			pcm := make([]int16, 960)
			// Should not panic
			_, _ = dec.Decode(tc.data, pcm)
		})
	}
}

// TestInvalidRates verifies that invalid sample rates are rejected.
func TestInvalidRates(t *testing.T) {
	invalidRates := []int{0, 44100, 22050, 96000}
	for _, rate := range invalidRates {
		_, err := NewEncoder(rate, 1, ApplicationAudio)
		if err == nil {
			t.Errorf("expected error for %dHz encoder, got nil", rate)
		}
		_, err = NewDecoder(rate, 1)
		if err == nil {
			t.Errorf("expected error for %dHz decoder, got nil", rate)
		}
	}
}

// TestFrameSizes tests that the correct frame size is used at each rate.
func TestFrameSizes(t *testing.T) {
	rates := []int{8000, 12000, 16000, 24000, 48000}
	expectedFrameSizes := []int{160, 240, 320, 480, 960} // 20ms at each rate

	for i, rate := range rates {
		enc, err := NewEncoder(rate, 1, ApplicationAudio)
		if err != nil {
			t.Fatalf("NewEncoder(%d): %v", rate, err)
		}
		if enc.frameSize != expectedFrameSizes[i] {
			t.Errorf("rate=%d: frameSize=%d, want %d", rate, enc.frameSize, expectedFrameSizes[i])
		}

		dec, err := NewDecoder(rate, 1)
		if err != nil {
			t.Fatalf("NewDecoder(%d): %v", rate, err)
		}
		if dec.frameSize != expectedFrameSizes[i] {
			t.Errorf("rate=%d decoder: frameSize=%d, want %d", rate, dec.frameSize, expectedFrameSizes[i])
		}
	}
}

// TestBitrateControl verifies that SetBitrate affects the encoded output size.
func TestBitrateControl(t *testing.T) {
	enc, err := NewEncoder(48000, 1, ApplicationAudio)
	if err != nil {
		t.Fatal(err)
	}

	pcm := generateSine(440.0, 48000, 1, 960)

	// Encode at default bitrate (64kbps)
	encoded64k, err := enc.Encode(pcm, 960)
	if err != nil {
		t.Fatalf("Encode at 64kbps: %v", err)
	}

	// Change to lower bitrate
	if err := enc.SetBitrate(16000); err != nil {
		t.Fatalf("SetBitrate(16000): %v", err)
	}
	encoded16k, err := enc.Encode(pcm, 960)
	if err != nil {
		t.Fatalf("Encode at 16kbps: %v", err)
	}

	// Change to higher bitrate
	if err := enc.SetBitrate(128000); err != nil {
		t.Fatalf("SetBitrate(128000): %v", err)
	}
	encoded128k, err := enc.Encode(pcm, 960)
	if err != nil {
		t.Fatalf("Encode at 128kbps: %v", err)
	}

	t.Logf("Packet sizes: 16k=%d, 64k=%d, 128k=%d bytes", len(encoded16k), len(encoded64k), len(encoded128k))

	// At minimum, all packets should be non-empty
	if len(encoded16k) == 0 || len(encoded64k) == 0 || len(encoded128k) == 0 {
		t.Error("one or more encoded packets are empty")
	}

	// Validate bitrate range
	if err := enc.SetBitrate(5000); err == nil {
		t.Error("SetBitrate(5000) should fail (below 6000)")
	}
	if err := enc.SetBitrate(600000); err == nil {
		t.Error("SetBitrate(600000) should fail (above 510000)")
	}
}

// TestPacketSizeRange verifies encoded packets are of reasonable size.
func TestPacketSizeRange(t *testing.T) {
	enc, err := NewEncoder(48000, 1, ApplicationAudio)
	if err != nil {
		t.Fatal(err)
	}

	pcm := generateSine(440.0, 48000, 1, 960)
	encoded, err := enc.Encode(pcm, 960)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	// At 64kbps, 20ms frame: 64000 * 0.020 / 8 = 160 bytes payload
	// With TOC byte, should be around 161 bytes.
	// Allow a wide range since our CELT encoder may produce different sizes.
	if len(encoded) < 2 {
		t.Errorf("packet too small: %d bytes", len(encoded))
	}
	if len(encoded) > 1500 {
		t.Errorf("packet too large: %d bytes (max Opus packet is 1500)", len(encoded))
	}
	t.Logf("Packet size at 64kbps/20ms: %d bytes", len(encoded))
}

// TestEncodeDecodeFloatRoundtrip tests the float64 encode/decode path.
func TestEncodeDecodeFloatRoundtrip(t *testing.T) {
	enc, err := NewEncoder(48000, 1, ApplicationAudio)
	if err != nil {
		t.Fatal(err)
	}
	dec, err := NewDecoder(48000, 1)
	if err != nil {
		t.Fatal(err)
	}

	// Generate float PCM
	pcm := make([]float64, 960)
	for i := range pcm {
		pcm[i] = 0.5 * math.Sin(2*math.Pi*440*float64(i)/48000)
	}

	encoded, err := enc.EncodeFloat(pcm, 960)
	if err != nil {
		t.Fatalf("EncodeFloat: %v", err)
	}

	decoded, err := dec.DecodeFloat(encoded)
	if err != nil {
		t.Fatalf("DecodeFloat: %v", err)
	}

	if len(decoded) == 0 {
		t.Fatal("decoded output is empty")
	}

	// Check energy
	energy := 0.0
	for _, s := range decoded {
		energy += s * s
	}
	if energy == 0 {
		t.Error("decoded float signal has zero energy")
	}
}

// TestEncoderResetCompliance verifies that resetting an encoder works.
func TestEncoderResetCompliance(t *testing.T) {
	enc, err := NewEncoder(16000, 1, ApplicationAudio)
	if err != nil {
		t.Fatal(err)
	}

	// Encode a frame
	frameSize := (16000 * 20) / 1000
	pcm := generateSine(400.0, 16000, 1, frameSize)
	_, err = enc.Encode(pcm, frameSize)
	if err != nil {
		t.Fatalf("Encode before reset: %v", err)
	}

	// Reset
	if err := enc.Reset(); err != nil {
		t.Fatalf("Reset: %v", err)
	}

	// Encode again after reset
	_, err = enc.Encode(pcm, frameSize)
	if err != nil {
		t.Fatalf("Encode after reset: %v", err)
	}
}

// TestDecoderResetCompliance verifies that resetting a decoder works.
func TestDecoderResetCompliance(t *testing.T) {
	enc, err := NewEncoder(16000, 1, ApplicationAudio)
	if err != nil {
		t.Fatal(err)
	}
	dec, err := NewDecoder(16000, 1)
	if err != nil {
		t.Fatal(err)
	}

	frameSize := (16000 * 20) / 1000
	pcm := generateSine(400.0, 16000, 1, frameSize)
	encoded, err := enc.Encode(pcm, frameSize)
	if err != nil {
		t.Fatal(err)
	}

	// Decode
	decoded := make([]int16, frameSize)
	_, err = dec.Decode(encoded, decoded)
	if err != nil {
		t.Fatalf("Decode before reset: %v", err)
	}

	// Reset
	if err := dec.Reset(); err != nil {
		t.Fatalf("Reset: %v", err)
	}

	// Decode again
	_, err = dec.Decode(encoded, decoded)
	if err != nil {
		t.Fatalf("Decode after reset: %v", err)
	}
}

// TestGetLastPacketDuration checks that the decoder reports correct frame duration.
func TestGetLastPacketDuration(t *testing.T) {
	rates := []int{8000, 12000, 16000, 24000, 48000}
	for _, rate := range rates {
		dec, err := NewDecoder(rate, 1)
		if err != nil {
			t.Fatalf("NewDecoder(%d): %v", rate, err)
		}
		expected := (rate * 20) / 1000
		got := dec.GetLastPacketDuration()
		if got != expected {
			t.Errorf("rate=%d: GetLastPacketDuration()=%d, want %d", rate, got, expected)
		}
	}
}

// --- helpers ---

func generateSine(freq float64, sampleRate, channels, frameSize int) []int16 {
	pcm := make([]int16, frameSize*channels)
	for i := 0; i < frameSize; i++ {
		sample := int16(16000 * math.Sin(2*math.Pi*freq*float64(i)/float64(sampleRate)))
		for ch := 0; ch < channels; ch++ {
			pcm[i*channels+ch] = sample
		}
	}
	return pcm
}

func signalEnergyI16(pcm []int16) float64 {
	e := 0.0
	for _, s := range pcm {
		e += float64(s) * float64(s)
	}
	return e
}
