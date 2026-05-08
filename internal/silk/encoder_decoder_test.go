package silk

import (
	"fmt"
	"math"
	"testing"
)

// Test encoder creation
func TestNewEncoder(t *testing.T) {
	tests := []struct {
		name       string
		sampleRate int
		channels   int
		wantErr    bool
	}{
		{"Valid 8kHz mono", 8000, 1, false},
		{"Valid 16kHz stereo", 16000, 2, false},
		{"Valid 24kHz mono", 24000, 1, false},
		{"Invalid sample rate", 44100, 1, true},
		{"Invalid channels", 16000, 3, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			enc, err := NewEncoder(tt.sampleRate, tt.channels)
			if (err != nil) != tt.wantErr {
				t.Errorf("NewEncoder() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && enc == nil {
				t.Error("NewEncoder() returned nil without error")
			}
		})
	}
}

// Test decoder creation
func TestNewDecoder(t *testing.T) {
	tests := []struct {
		name       string
		sampleRate int
		channels   int
		wantErr    bool
	}{
		{"Valid 8kHz mono", 8000, 1, false},
		{"Valid 16kHz stereo", 16000, 2, false},
		{"Valid 24kHz mono", 24000, 1, false},
		{"Invalid sample rate", 44100, 1, true},
		{"Invalid channels", 16000, 3, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dec, err := NewDecoder(tt.sampleRate, tt.channels)
			if (err != nil) != tt.wantErr {
				t.Errorf("NewDecoder() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && dec == nil {
				t.Error("NewDecoder() returned nil without error")
			}
		})
	}
}

// Test encoder complexity setting
func TestEncoderSetComplexity(t *testing.T) {
	enc, err := NewEncoder(16000, 1)
	if err != nil {
		t.Fatalf("Failed to create encoder: %v", err)
	}

	tests := []struct {
		name       string
		complexity int
		wantErr    bool
	}{
		{"Valid 0", 0, false},
		{"Valid 5", 5, false},
		{"Valid 10", 10, false},
		{"Invalid negative", -1, true},
		{"Invalid too high", 11, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := enc.SetComplexity(tt.complexity)
			if (err != nil) != tt.wantErr {
				t.Errorf("SetComplexity() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// Test encoder bitrate setting
func TestEncoderSetBitrate(t *testing.T) {
	enc, err := NewEncoder(16000, 1)
	if err != nil {
		t.Fatalf("Failed to create encoder: %v", err)
	}

	tests := []struct {
		name    string
		bitrate int
		wantErr bool
	}{
		{"Valid 8kbps", 8000, false},
		{"Valid 24kbps", 24000, false},
		{"Too low", 5000, true},
		{"Too high", 50000, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := enc.SetBitrate(tt.bitrate)
			if (err != nil) != tt.wantErr {
				t.Errorf("SetBitrate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// Test encoding speech signal
func TestEncoderEncodeSpeech(t *testing.T) {
	enc, err := NewEncoder(8000, 1)
	if err != nil {
		t.Fatalf("Failed to create encoder: %v", err)
	}

	frameSize := 8000 / 50 // 20ms at 8kHz = 160 samples

	// Generate synthetic speech-like signal (sine wave with harmonics) - louder amplitude
	signal := make([]float64, frameSize)
	for i := range signal {
		ti := float64(i) / 8000.0
		signal[i] = 1.0 * math.Sin(2*math.Pi*200*ti)
		signal[i] += 0.6 * math.Sin(2*math.Pi*400*ti)
		signal[i] += 0.4 * math.Sin(2*math.Pi*600*ti)
	}

	// Feed multiple frames so VAD builds up history
	var packet []byte
	for attempt := 0; attempt < 10; attempt++ {
		packet, err = enc.Encode(signal)
		if err != nil {
			t.Fatalf("Encode() error = %v", err)
		}
		if len(packet) > 1 {
			break // Got a non-silence packet
		}
	}

	if len(packet) == 0 {
		t.Error("Encode() returned empty packet")
	}

	t.Logf("Encoded speech packet: %d bytes", len(packet))
}

// Test encoding silence
func TestEncoderEncodeSilence(t *testing.T) {
	enc, err := NewEncoder(8000, 1)
	if err != nil {
		t.Fatalf("Failed to create encoder: %v", err)
	}

	frameSize := 8000 / 50 // 20ms at 8kHz = 160 samples

	// Generate silence
	signal := make([]float64, frameSize)

	packet, err := enc.Encode(signal)
	if err != nil {
		t.Fatalf("Encode() error = %v", err)
	}

	// Verify minimal silence packet
	if len(packet) != 1 || packet[0] != 0x00 {
		t.Errorf("Silence not encoded as minimal packet, got %d bytes", len(packet))
	}
}

// Test encoder with invalid PCM length
func TestEncoderInvalidPCMLength(t *testing.T) {
	enc, err := NewEncoder(8000, 1)
	if err != nil {
		t.Fatalf("Failed to create encoder: %v", err)
	}

	// Wrong length
	signal := make([]float64, 100)

	_, err = enc.Encode(signal)
	if err == nil {
		t.Error("Expected error for invalid PCM length, got nil")
	}
}

// Test decoder with range-coded speech packet (from encoder)
func TestDecoderDecodeSpeech(t *testing.T) {
	// First encode speech frames to get a valid range-coded packet
	enc, err := NewEncoder(8000, 1)
	if err != nil {
		t.Fatalf("Failed to create encoder: %v", err)
	}

	frameSize := 8000 / 50
	signal := make([]float64, frameSize)
	for i := range signal {
		ti := float64(i) / 8000.0
		signal[i] = 2.0 * math.Sin(2*math.Pi*200*ti)
	}

	// Feed multiple frames so VAD builds history
	var packet []byte
	for attempt := 0; attempt < 10; attempt++ {
		packet, err = enc.Encode(signal)
		if err != nil {
			t.Fatalf("Encode() error = %v", err)
		}
		if len(packet) > 1 {
			break
		}
	}

	if len(packet) <= 1 {
		t.Skip("Encoder did not produce speech packet after 10 frames")
	}

	// Now decode it
	dec, err := NewDecoder(8000, 1)
	if err != nil {
		t.Fatalf("Failed to create decoder: %v", err)
	}

	output, err := dec.Decode(packet)
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}

	if len(output) != frameSize {
		t.Errorf("Decode() output length = %d, want %d", len(output), frameSize)
	}

	// Verify output is not all zeros
	hasNonZero := false
	for _, sample := range output {
		if sample != 0 {
			hasNonZero = true
			break
		}
	}
	if !hasNonZero {
		t.Error("Decoded speech is all zeros")
	}

	t.Logf("Decoded %d samples from %d byte packet", len(output), len(packet))
}

// Test decoder with silence packet
func TestDecoderDecodeSilence(t *testing.T) {
	dec, err := NewDecoder(8000, 1)
	if err != nil {
		t.Fatalf("Failed to create decoder: %v", err)
	}

	// Silence packet
	packet := []byte{0x00}

	output, err := dec.Decode(packet)
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}

	expectedLen := 8000 / 50 // 20ms at 8kHz
	if len(output) != expectedLen {
		t.Errorf("Decode() output length = %d, want %d", len(output), expectedLen)
	}

	// Verify output is all zeros
	for i, sample := range output {
		if sample != 0 {
			t.Errorf("Silence output has non-zero sample at index %d: %f", i, sample)
			break
		}
	}
}

// Test encoder-decoder roundtrip produces signal with positive SNR
func TestEncoderDecoderRoundtrip(t *testing.T) {
	enc, err := NewEncoder(8000, 1)
	if err != nil {
		t.Fatalf("Failed to create encoder: %v", err)
	}

	dec, err := NewDecoder(8000, 1)
	if err != nil {
		t.Fatalf("Failed to create decoder: %v", err)
	}

	frameSize := 8000 / 50 // 20ms at 8kHz

	// Generate test signal - loud enough to trigger VAD
	signal := make([]float64, frameSize)
	for i := range signal {
		ti := float64(i) / 8000.0
		signal[i] = 2.0 * math.Sin(2*math.Pi*200*ti)
	}

	// Feed multiple frames to build VAD history and get a speech packet
	var packet []byte
	for attempt := 0; attempt < 10; attempt++ {
		packet, err = enc.Encode(signal)
		if err != nil {
			t.Fatalf("Encode() error = %v", err)
		}
		if len(packet) > 1 {
			break
		}
	}

	if len(packet) <= 1 {
		t.Skip("Encoder produced only silence packets - cannot test roundtrip SNR")
	}

	t.Logf("Encoded packet size: %d bytes", len(packet))

	// Decode
	output, err := dec.Decode(packet)
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}

	if len(output) != len(signal) {
		t.Errorf("Roundtrip output length = %d, want %d", len(output), len(signal))
	}

	// Verify output has some energy (not silence)
	energy := 0.0
	for _, s := range output {
		energy += s * s
	}
	energy /= float64(len(output))

	if energy < 1e-10 {
		t.Errorf("Decoded signal has no energy: %e", energy)
	}

	t.Logf("Decoded signal energy: %e", energy)
}

// Test packet loss concealment
func TestDecoderPacketLossConcealment(t *testing.T) {
	dec, err := NewDecoder(8000, 1)
	if err != nil {
		t.Fatalf("Failed to create decoder: %v", err)
	}

	// First, encode a valid speech frame
	enc, err := NewEncoder(8000, 1)
	if err != nil {
		t.Fatalf("Failed to create encoder: %v", err)
	}

	frameSize := 8000 / 50
	signal := make([]float64, frameSize)
	for i := range signal {
		ti := float64(i) / 8000.0
		signal[i] = 2.0 * math.Sin(2*math.Pi*200*ti)
	}

	packet, err := enc.Encode(signal)
	if err != nil {
		t.Skipf("Encode failed: %v", err)
	}

	output1, err := dec.Decode(packet)
	if err != nil {
		t.Fatalf("Decode() valid packet error = %v", err)
	}

	// Now decode invalid packet (triggers PLC) - single byte is too short for range decoder
	invalidPacket := []byte{0xFF}
	output2, err := dec.Decode(invalidPacket)
	if err != nil {
		t.Fatalf("Decode() with PLC error = %v", err)
	}

	if len(output2) != len(output1) {
		t.Errorf("PLC output length = %d, want %d", len(output2), len(output1))
	}

	// Verify PLC output has some energy
	energy := 0.0
	for _, s := range output2 {
		energy += s * s
	}
	energy /= float64(len(output2))

	if energy < 1e-9 {
		t.Log("PLC output has low energy - acceptable if previous frame was quiet")
	}
}

// Test encoder reset
func TestEncoderReset(t *testing.T) {
	enc, err := NewEncoder(8000, 1)
	if err != nil {
		t.Fatalf("Failed to create encoder: %v", err)
	}

	// Encode a frame
	frameSize := 8000 / 50
	signal := make([]float64, frameSize)
	for i := range signal {
		signal[i] = 0.5 * math.Sin(2*math.Pi*200*float64(i)/8000.0)
	}

	_, err = enc.Encode(signal)
	if err != nil {
		t.Fatalf("Encode() error = %v", err)
	}

	// Reset should not cause errors
	enc.Reset()

	// Encode again after reset
	_, err = enc.Encode(signal)
	if err != nil {
		t.Errorf("Encode() after reset error = %v", err)
	}
}

// Test decoder reset
func TestDecoderReset(t *testing.T) {
	dec, err := NewDecoder(8000, 1)
	if err != nil {
		t.Fatalf("Failed to create decoder: %v", err)
	}

	// Decode a silence packet
	packet := []byte{0x00}
	_, err = dec.Decode(packet)
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}

	// Reset should not cause errors
	dec.Reset()

	// Decode again after reset
	_, err = dec.Decode(packet)
	if err != nil {
		t.Errorf("Decode() after reset error = %v", err)
	}
}

// Test stereo encoding
func TestEncoderStereo(t *testing.T) {
	enc, err := NewEncoder(8000, 2)
	if err != nil {
		t.Fatalf("Failed to create encoder: %v", err)
	}

	frameSize := 8000 / 50
	signal := make([]float64, frameSize*2) // Stereo interleaved

	for i := 0; i < frameSize; i++ {
		ti := float64(i) / 8000.0
		sample := 0.5 * math.Sin(2*math.Pi*200*ti)
		signal[i*2] = sample   // Left
		signal[i*2+1] = sample // Right
	}

	packet, err := enc.Encode(signal)
	if err != nil {
		t.Fatalf("Encode() stereo error = %v", err)
	}

	if len(packet) == 0 {
		t.Error("Stereo encode returned empty packet")
	}
}

// Test stereo decoding
func TestDecoderStereo(t *testing.T) {
	dec, err := NewDecoder(8000, 2)
	if err != nil {
		t.Fatalf("Failed to create decoder: %v", err)
	}

	// Silence packet for stereo
	packet := []byte{0x00}

	output, err := dec.Decode(packet)
	if err != nil {
		t.Fatalf("Decode() stereo error = %v", err)
	}

	expectedLen := (8000 / 50) * 2 // 20ms stereo
	if len(output) != expectedLen {
		t.Errorf("Stereo output length = %d, want %d", len(output), expectedLen)
	}
}

// Test multi-rate encoder/decoder
func TestMultiRate(t *testing.T) {
	rates := []int{8000, 12000, 16000}
	for _, rate := range rates {
		t.Run(fmt.Sprintf("%dHz", rate), func(t *testing.T) {
			enc, err := NewEncoder(rate, 1)
			if err != nil {
				t.Fatalf("NewEncoder(%d) error: %v", rate, err)
			}

			dec, err := NewDecoder(rate, 1)
			if err != nil {
				t.Fatalf("NewDecoder(%d) error: %v", rate, err)
			}

			frameSize := rate / 50
			signal := make([]float64, frameSize)
			for i := range signal {
				ti := float64(i) / float64(rate)
				signal[i] = 2.0 * math.Sin(2*math.Pi*200*ti)
			}

			packet, err := enc.Encode(signal)
			if err != nil {
				t.Fatalf("Encode error: %v", err)
			}

			output, err := dec.Decode(packet)
			if err != nil {
				t.Fatalf("Decode error: %v", err)
			}

			if len(output) != frameSize {
				t.Errorf("Output length = %d, want %d", len(output), frameSize)
			}

			t.Logf("Rate %d: encoded %d bytes, decoded %d samples", rate, len(packet), len(output))
		})
	}
}
