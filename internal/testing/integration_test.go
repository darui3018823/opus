package testing

import (
	"math"
	"testing"
)

// TestSignalType represents different signal types for testing.
type TestSignalType int

const (
	SignalSineWave TestSignalType = iota
	SignalSilence
	SignalWhiteNoise
)

// GenerateTestSignal creates test PCM data for integration testing.
// Returns 16-bit signed PCM samples as float64 slice.
func GenerateTestSignal(signalType TestSignalType, sampleRate, durationMs, channels int) []float64 {
	numSamples := sampleRate * durationMs / 1000 * channels
	samples := make([]float64, numSamples)

	switch signalType {
	case SignalSineWave:
		// 440Hz sine wave at 0.5 amplitude
		freq := 440.0
		for i := 0; i < numSamples; i += channels {
			t := float64(i/channels) / float64(sampleRate)
			value := 0.5 * math.Sin(2.0*math.Pi*freq*t)
			for ch := 0; ch < channels; ch++ {
				samples[i+ch] = value
			}
		}

	case SignalSilence:
		// All zeros (already initialized)

	case SignalWhiteNoise:
		// Simple LCG pseudo-random noise
		seed := uint32(12345)
		for i := 0; i < numSamples; i++ {
			seed = seed*1103515245 + 12345
			// Convert to float in range [-0.5, 0.5]
			samples[i] = (float64(seed)/float64(^uint32(0)) - 0.5)
		}
	}

	return samples
}

// Float64ToPCM16 converts float64 samples to 16-bit PCM bytes.
func Float64ToPCM16(samples []float64) []byte {
	pcm := make([]byte, len(samples)*2)
	for i, s := range samples {
		// Clamp to [-1, 1]
		if s > 1.0 {
			s = 1.0
		} else if s < -1.0 {
			s = -1.0
		}
		// Convert to int16
		val := int16(s * 32767)
		pcm[i*2] = byte(val)
		pcm[i*2+1] = byte(val >> 8)
	}
	return pcm
}

// PCM16ToFloat64 converts 16-bit PCM bytes to float64 samples.
func PCM16ToFloat64(pcm []byte) []float64 {
	samples := make([]float64, len(pcm)/2)
	for i := 0; i < len(samples); i++ {
		val := int16(pcm[i*2]) | int16(pcm[i*2+1])<<8
		samples[i] = float64(val) / 32768.0
	}
	return samples
}

// ComputeSNR calculates Signal-to-Noise Ratio in dB.
// original: original signal
// reconstructed: signal after encode/decode
func ComputeSNR(original, reconstructed []float64) float64 {
	if len(original) != len(reconstructed) {
		return -math.MaxFloat64
	}

	signalPower := 0.0
	noisePower := 0.0

	for i := range original {
		signalPower += original[i] * original[i]
		diff := original[i] - reconstructed[i]
		noisePower += diff * diff
	}

	if noisePower < 1e-20 {
		return 100.0 // Essentially perfect
	}

	return 10.0 * math.Log10(signalPower/noisePower)
}

// IntegrationTestCase defines a single integration test.
type IntegrationTestCase struct {
	Name       string
	SignalType TestSignalType
	SampleRate int
	Channels   int
	DurationMs int
	Bitrate    int
	MinSNR     float64 // Minimum acceptable SNR in dB
}

// DefaultIntegrationTests returns standard test cases.
func DefaultIntegrationTests() []IntegrationTestCase {
	return []IntegrationTestCase{
		{
			Name:       "Sine440Hz_48k_Mono",
			SignalType: SignalSineWave,
			SampleRate: 48000,
			Channels:   1,
			DurationMs: 100,
			Bitrate:    64000,
			MinSNR:     20.0,
		},
		{
			Name:       "Sine1kHz_48k_Stereo",
			SignalType: SignalSineWave,
			SampleRate: 48000,
			Channels:   2,
			DurationMs: 100,
			Bitrate:    128000,
			MinSNR:     20.0,
		},
		{
			Name:       "Silence_48k_Mono",
			SignalType: SignalSilence,
			SampleRate: 48000,
			Channels:   1,
			DurationMs: 100,
			Bitrate:    64000,
			MinSNR:     40.0, // Silence should be very accurate
		},
		{
			Name:       "WhiteNoise_48k_Mono",
			SignalType: SignalWhiteNoise,
			SampleRate: 48000,
			Channels:   1,
			DurationMs: 100,
			Bitrate:    96000,
			MinSNR:     10.0, // Noise is harder to compress
		},
	}
}

// RunIntegrationTest executes a single integration test case.
// encoder: function that encodes PCM to Opus frame
// decoder: function that decodes Opus frame to PCM
func RunIntegrationTest(
	t *testing.T,
	tc IntegrationTestCase,
	encoder func(samples []float64) ([]byte, error),
	decoder func(frame []byte) ([]float64, error),
) {
	t.Helper()

	// Generate test signal
	original := GenerateTestSignal(tc.SignalType, tc.SampleRate, tc.DurationMs, tc.Channels)

	// Encode
	encoded, err := encoder(original)
	if err != nil {
		t.Fatalf("Encode failed: %v", err)
	}

	if len(encoded) == 0 {
		t.Fatal("Encoded output is empty")
	}

	// Decode
	decoded, err := decoder(encoded)
	if err != nil {
		t.Fatalf("Decode failed: %v", err)
	}

	if len(decoded) != len(original) {
		t.Errorf("Length mismatch: original=%d, decoded=%d", len(original), len(decoded))
		return
	}

	// Compute SNR
	snr := ComputeSNR(original, decoded)

	t.Logf("Signal: %s, Encoded size: %d bytes, SNR: %.2f dB", tc.Name, len(encoded), snr)

	if snr < tc.MinSNR {
		t.Errorf("SNR too low: %.2f dB (minimum: %.2f dB)", snr, tc.MinSNR)
	}
}

// RunAllIntegrationTests runs all default integration tests.
func RunAllIntegrationTests(
	t *testing.T,
	encoder func(samples []float64) ([]byte, error),
	decoder func(frame []byte) ([]float64, error),
) {
	for _, tc := range DefaultIntegrationTests() {
		t.Run(tc.Name, func(t *testing.T) {
			RunIntegrationTest(t, tc, encoder, decoder)
		})
	}
}
