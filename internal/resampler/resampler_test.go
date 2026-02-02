package resampler

import (
	"math"
	"testing"
)

func TestNewResampler(t *testing.T) {
	tests := []struct {
		name        string
		inRate      int
		outRate     int
		numChannels int
		quality     int
		shouldError bool
	}{
		{"48k to 16k mono", Rate48kHz, Rate16kHz, 1, QualityDefault, false},
		{"16k to 48k stereo", Rate16kHz, Rate48kHz, 2, QualityDefault, false},
		{"8k to 48k mono", Rate8kHz, Rate48kHz, 1, QualityMax, false},
		{"Invalid input rate", 44100, Rate48kHz, 1, QualityDefault, true},
		{"Invalid output rate", Rate48kHz, 44100, 1, QualityDefault, true},
		{"Invalid channels", Rate48kHz, Rate16kHz, 0, QualityDefault, true},
		{"Invalid quality", Rate48kHz, Rate16kHz, 1, 20, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, err := NewResampler(tt.inRate, tt.outRate, tt.numChannels, tt.quality)
			if tt.shouldError {
				if err == nil {
					t.Error("Expected error but got none")
				}
			} else {
				if err != nil {
					t.Errorf("Unexpected error: %v", err)
				}
				if r == nil {
					t.Error("Resampler should not be nil")
				}
			}
		})
	}
}

func TestResamplerIdentity(t *testing.T) {
	// Test same rate (should be near-identity)
	r, err := NewResampler(Rate48kHz, Rate48kHz, 1, QualityDefault)
	if err != nil {
		t.Fatalf("Failed to create resampler: %v", err)
	}

	// Generate test signal
	input := make([]float64, 960) // 20ms at 48kHz
	for i := range input {
		input[i] = math.Sin(2 * math.Pi * 1000 * float64(i) / 48000) // 1kHz tone
	}

	output := r.Process(input)

	// Should produce similar length and values
	if len(output) < len(input)-10 || len(output) > len(input)+10 {
		t.Errorf("Output length %d significantly different from input length %d", len(output), len(input))
	}

	// Check signal preservation (allowing for filter delay and edge effects)
	if len(output) > 100 {
		sumDiff := 0.0
		count := 0
		for i := 50; i < len(output)-50 && i < len(input); i++ {
			diff := math.Abs(output[i] - input[i])
			sumDiff += diff
			count++
		}
		avgDiff := sumDiff / float64(count)
		if avgDiff > 0.1 {
			t.Errorf("Average difference too large: %f", avgDiff)
		}
	}
}

func TestResamplerDownsample(t *testing.T) {
	// Test 48kHz to 16kHz (3:1 downsampling)
	r, err := NewResampler(Rate48kHz, Rate16kHz, 1, QualityDefault)
	if err != nil {
		t.Fatalf("Failed to create resampler: %v", err)
	}

	// Generate test signal: 960 samples at 48kHz (20ms)
	input := make([]float64, 960)
	for i := range input {
		// 1kHz tone (well below Nyquist for 16kHz)
		input[i] = math.Sin(2 * math.Pi * 1000 * float64(i) / 48000)
	}

	output := r.Process(input)

	// Should produce approximately 320 samples (20ms at 16kHz)
	expectedLen := 320
	if len(output) < expectedLen-10 || len(output) > expectedLen+10 {
		t.Errorf("Output length %d not near expected %d", len(output), expectedLen)
	}

	// Check that signal is preserved (accounting for group delay)
	if len(output) > 20 {
		hasSignal := false
		for i := 10; i < len(output)-10; i++ {
			if math.Abs(output[i]) > 0.5 {
				hasSignal = true
				break
			}
		}
		if !hasSignal {
			t.Error("Output signal appears to be zero or heavily attenuated")
		}
	}
}

func TestResamplerUpsample(t *testing.T) {
	// Test 16kHz to 48kHz (1:3 upsampling)
	r, err := NewResampler(Rate16kHz, Rate48kHz, 1, QualityDefault)
	if err != nil {
		t.Fatalf("Failed to create resampler: %v", err)
	}

	// Generate test signal: 320 samples at 16kHz (20ms)
	input := make([]float64, 320)
	for i := range input {
		// 1kHz tone
		input[i] = math.Sin(2 * math.Pi * 1000 * float64(i) / 16000)
	}

	output := r.Process(input)

	// Should produce approximately 960 samples (20ms at 48kHz)
	expectedLen := 960
	if len(output) < expectedLen-20 || len(output) > expectedLen+20 {
		t.Errorf("Output length %d not near expected %d", len(output), expectedLen)
	}

	// Check that signal is preserved
	if len(output) > 20 {
		hasSignal := false
		for i := 10; i < len(output)-10; i++ {
			if math.Abs(output[i]) > 0.5 {
				hasSignal = true
				break
			}
		}
		if !hasSignal {
			t.Error("Output signal appears to be zero or heavily attenuated")
		}
	}
}

func TestResamplerStereo(t *testing.T) {
	// Test stereo resampling
	r, err := NewResampler(Rate48kHz, Rate16kHz, 2, QualityDefault)
	if err != nil {
		t.Fatalf("Failed to create resampler: %v", err)
	}

	// Generate stereo test signal (interleaved)
	inputLen := 960 // samples per channel
	input := make([]float64, inputLen*2)
	for i := 0; i < inputLen; i++ {
		// Left channel: 1kHz
		input[i*2] = math.Sin(2 * math.Pi * 1000 * float64(i) / 48000)
		// Right channel: 2kHz
		input[i*2+1] = math.Sin(2 * math.Pi * 2000 * float64(i) / 48000)
	}

	output := r.Process(input)

	// Check output length
	expectedLen := 320 * 2 // samples per channel * channels
	if len(output) < expectedLen-20 || len(output) > expectedLen+20 {
		t.Errorf("Stereo output length %d not near expected %d", len(output), expectedLen)
	}

	// Verify both channels are present
	if len(output) >= 2 {
		leftSignal := false
		rightSignal := false
		for i := 20; i < len(output)/2-20; i++ {
			if math.Abs(output[i*2]) > 0.5 {
				leftSignal = true
			}
			if math.Abs(output[i*2+1]) > 0.5 {
				rightSignal = true
			}
		}
		if !leftSignal || !rightSignal {
			t.Error("One or both stereo channels missing signal")
		}
	}
}

func TestResamplerReset(t *testing.T) {
	r, err := NewResampler(Rate48kHz, Rate16kHz, 1, QualityDefault)
	if err != nil {
		t.Fatalf("Failed to create resampler: %v", err)
	}

	// Process some data
	input := make([]float64, 480)
	for i := range input {
		input[i] = 1.0
	}
	_ = r.Process(input)

	// Reset
	r.Reset()

	// Check that state is cleared
	for i := range r.mem {
		for j := range r.mem[i] {
			if r.mem[i][j] != 0 {
				t.Error("Memory not cleared after reset")
			}
		}
	}
	for i := range r.lastSample {
		if r.lastSample[i] != 0 {
			t.Error("Last sample not cleared after reset")
		}
	}
}

func TestKaiserWindow(t *testing.T) {
	// Test Kaiser window properties
	beta := 5.0
	
	// Should be 1.0 at center
	center := kaiserWindow(0.5, beta)
	if math.Abs(center-1.0) > 0.01 {
		t.Errorf("Kaiser window at center = %f, want ~1.0", center)
	}
	
	// Should be symmetric
	left := kaiserWindow(0.25, beta)
	right := kaiserWindow(0.75, beta)
	if math.Abs(left-right) > 0.01 {
		t.Errorf("Kaiser window not symmetric: %f vs %f", left, right)
	}
	
	// Should be near zero at edges
	edge := kaiserWindow(0.0, beta)
	if edge > 0.1 {
		t.Errorf("Kaiser window at edge = %f, should be near 0", edge)
	}
}

func TestBesselI0(t *testing.T) {
	// Test modified Bessel function
	// I0(0) should be 1
	val := besselI0(0)
	if math.Abs(val-1.0) > 1e-10 {
		t.Errorf("I0(0) = %f, want 1.0", val)
	}
	
	// I0(1) should be approximately 1.266
	val = besselI0(1)
	if math.Abs(val-1.266) > 0.01 {
		t.Errorf("I0(1) = %f, want ~1.266", val)
	}
}

func TestGCD(t *testing.T) {
	tests := []struct {
		a, b, want int
	}{
		{48000, 16000, 16000},
		{48000, 12000, 12000},
		{16000, 8000, 8000},
		{48000, 48000, 48000},
		{13, 17, 1},
	}
	
	for _, tt := range tests {
		got := gcd(tt.a, tt.b)
		if got != tt.want {
			t.Errorf("gcd(%d, %d) = %d, want %d", tt.a, tt.b, got, tt.want)
		}
	}
}

func BenchmarkResampler48to16(b *testing.B) {
	r, err := NewResampler(Rate48kHz, Rate16kHz, 1, QualityDefault)
	if err != nil {
		b.Fatalf("Failed to create resampler: %v", err)
	}

	input := make([]float64, 960)
	for i := range input {
		input[i] = math.Sin(2 * math.Pi * 1000 * float64(i) / 48000)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r.Process(input)
	}
}

func BenchmarkResampler16to48(b *testing.B) {
	r, err := NewResampler(Rate16kHz, Rate48kHz, 1, QualityDefault)
	if err != nil {
		b.Fatalf("Failed to create resampler: %v", err)
	}

	input := make([]float64, 320)
	for i := range input {
		input[i] = math.Sin(2 * math.Pi * 1000 * float64(i) / 16000)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r.Process(input)
	}
}
