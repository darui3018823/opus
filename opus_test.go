package opus

import (
	"errors"
	"testing"
)

func TestNewEncoder(t *testing.T) {
	tests := []struct {
		name        string
		sampleRate  int
		channels    int
		application Application
		wantErr     bool
	}{
		{"Valid 48kHz stereo", 48000, 2, ApplicationAudio, false},
		{"Valid 48kHz mono", 48000, 1, ApplicationAudio, false},
		{"Valid 16kHz stereo", 16000, 2, ApplicationVOIP, false},
		{"Valid restricted low delay", 48000, 2, ApplicationRestrictedLowDelay, false},
		{"Invalid sample rate", 44100, 2, ApplicationAudio, true},
		{"Invalid channels", 48000, 5, ApplicationAudio, true},
		{"Invalid application", 48000, 2, Application(9999), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			enc, err := NewEncoder(tt.sampleRate, tt.channels, tt.application)
			if (err != nil) != tt.wantErr {
				t.Errorf("NewEncoder() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && enc == nil {
				t.Error("NewEncoder() returned nil encoder")
			}
		})
	}
}

func TestEncoderApplicationValidation(t *testing.T) {
	if _, err := NewEncoder(48000, 1, Application(9999)); !errors.Is(err, ErrBadArg) {
		t.Fatalf("NewEncoder invalid application error = %v, want ErrBadArg", err)
	}

	enc, err := NewEncoder(48000, 1, ApplicationAudio)
	if err != nil {
		t.Fatal(err)
	}
	if err := enc.SetApplication(Application(9999)); !errors.Is(err, ErrBadArg) {
		t.Fatalf("SetApplication invalid application error = %v, want ErrBadArg", err)
	}
	if got := enc.Application(); got != ApplicationAudio {
		t.Fatalf("application changed after rejected setter: got %d, want %d", got, ApplicationAudio)
	}
	if got := enc.SignalType(); got != SignalMusic {
		t.Fatalf("signal type changed after rejected setter: got %d, want %d", got, SignalMusic)
	}
	for _, application := range []Application{
		ApplicationVOIP,
		ApplicationAudio,
		ApplicationRestrictedLowDelay,
	} {
		if err := enc.SetApplication(application); err != nil {
			t.Fatalf("SetApplication(%d) failed: %v", application, err)
		}
		if got := enc.Application(); got != application {
			t.Fatalf("Application() = %d, want %d", got, application)
		}
	}
}

func TestNewDecoder(t *testing.T) {
	tests := []struct {
		name       string
		sampleRate int
		channels   int
		wantErr    bool
	}{
		{"Valid 48kHz stereo", 48000, 2, false},
		{"Valid 48kHz mono", 48000, 1, false},
		{"Valid 16kHz stereo", 16000, 2, false},
		{"Invalid sample rate", 44100, 2, true},
		{"Invalid channels", 48000, 3, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dec, err := NewDecoder(tt.sampleRate, tt.channels)
			if (err != nil) != tt.wantErr {
				t.Errorf("NewDecoder() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && dec == nil {
				t.Error("NewDecoder() returned nil decoder")
			}
		})
	}
}

func TestEncoderSetBitrate(t *testing.T) {
	enc, err := NewEncoder(48000, 2, ApplicationAudio)
	if err != nil {
		t.Fatalf("Failed to create encoder: %v", err)
	}

	tests := []struct {
		name    string
		bitrate int
		wantErr bool
	}{
		{"Valid 64kbps", 64000, false},
		{"Valid 128kbps", 128000, false},
		{"Too low", 5000, true},
		{"Too high", 600000, true},
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

func TestEncoderSetComplexity(t *testing.T) {
	enc, err := NewEncoder(48000, 2, ApplicationAudio)
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
		{"Too low", -1, true},
		{"Too high", 11, true},
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

func TestEncodeDecodeRoundtrip(t *testing.T) {
	const sampleRate = 48000
	const channels = 2
	const frameSize = 960 // 20ms at 48kHz

	// Create encoder and decoder
	enc, err := NewEncoder(sampleRate, channels, ApplicationAudio)
	if err != nil {
		t.Fatalf("Failed to create encoder: %v", err)
	}

	dec, err := NewDecoder(sampleRate, channels)
	if err != nil {
		t.Fatalf("Failed to create decoder: %v", err)
	}

	// Create test signal (sine wave)
	pcm := make([]int16, frameSize*channels)
	for i := 0; i < frameSize; i++ {
		// 440 Hz sine wave
		sample := int16(10000.0 * 0.5) // Reduced amplitude for testing
		pcm[i*channels] = sample
		pcm[i*channels+1] = sample
	}

	// Encode
	compressed, err := enc.Encode(pcm, frameSize)
	if err != nil {
		t.Fatalf("Encode failed: %v", err)
	}

	if len(compressed) == 0 {
		t.Fatal("Encoded data is empty")
	}

	// Decode
	decoded := make([]int16, frameSize*channels)
	n, err := dec.Decode(compressed, decoded)
	if err != nil {
		t.Fatalf("Decode failed: %v", err)
	}

	if n != frameSize {
		t.Errorf("Decoded %d samples, expected %d", n, frameSize)
	}
}

func TestEncodeFloatDecodeFloat(t *testing.T) {
	const sampleRate = 48000
	const channels = 1
	const frameSize = 960

	enc, err := NewEncoder(sampleRate, channels, ApplicationAudio)
	if err != nil {
		t.Fatalf("Failed to create encoder: %v", err)
	}

	dec, err := NewDecoder(sampleRate, channels)
	if err != nil {
		t.Fatalf("Failed to create decoder: %v", err)
	}

	// Create test signal
	pcm := make([]float64, frameSize*channels)
	for i := 0; i < frameSize; i++ {
		pcm[i] = 0.1 // Constant value
	}

	// Encode
	compressed, err := enc.EncodeFloat(pcm, frameSize)
	if err != nil {
		t.Fatalf("EncodeFloat failed: %v", err)
	}

	// Decode
	decoded, err := dec.DecodeFloat(compressed)
	if err != nil {
		t.Fatalf("DecodeFloat failed: %v", err)
	}

	if len(decoded) != frameSize*channels {
		t.Errorf("Decoded %d samples, expected %d", len(decoded), frameSize*channels)
	}
}

func TestDecoderPLC(t *testing.T) {
	dec, err := NewDecoder(48000, 2)
	if err != nil {
		t.Fatalf("Failed to create decoder: %v", err)
	}

	// Test packet loss concealment
	pcm := make([]int16, 960*2)
	n, err := dec.DecodeFEC(nil, pcm)
	if err != nil {
		t.Fatalf("DecodeFEC failed: %v", err)
	}

	if n != 960 {
		t.Errorf("PLC returned %d samples, expected 960", n)
	}
}

func TestEncoderReset(t *testing.T) {
	enc, err := NewEncoder(48000, 2, ApplicationAudio)
	if err != nil {
		t.Fatalf("Failed to create encoder: %v", err)
	}

	err = enc.Reset()
	if err != nil {
		t.Errorf("Reset() error = %v", err)
	}
}

func TestDecoderReset(t *testing.T) {
	dec, err := NewDecoder(48000, 2)
	if err != nil {
		t.Fatalf("Failed to create decoder: %v", err)
	}

	err = dec.Reset()
	if err != nil {
		t.Errorf("Reset() error = %v", err)
	}
}

func BenchmarkEncode(b *testing.B) {
	enc, err := NewEncoder(48000, 2, ApplicationAudio)
	if err != nil {
		b.Fatal(err)
	}

	pcm := make([]int16, 960*2)
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, err := enc.Encode(pcm, 960)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkDecode(b *testing.B) {
	enc, _ := NewEncoder(48000, 2, ApplicationAudio)
	dec, err := NewDecoder(48000, 2)
	if err != nil {
		b.Fatal(err)
	}

	pcm := make([]int16, 960*2)
	compressed, _ := enc.Encode(pcm, 960)
	output := make([]int16, 960*2)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := dec.Decode(compressed, output)
		if err != nil {
			b.Fatal(err)
		}
	}
}
