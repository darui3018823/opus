package opus

import (
	"errors"
	"fmt"
	"math"
	"testing"
)

func TestVersionMetadata(t *testing.T) {
	want := fmt.Sprintf("%d.%d.%d", VersionMajor, VersionMinor, VersionPatch)
	if Version != want {
		t.Fatalf("Version = %q, components produce %q", Version, want)
	}
}

func TestPublicMaximumConstants(t *testing.T) {
	if MaxFrameSize != 5760 {
		t.Fatalf("MaxFrameSize = %d, want 5760", MaxFrameSize)
	}
	if MaxFrameSize != FrameSize120ms {
		t.Fatalf("MaxFrameSize = %d, want FrameSize120ms (%d)", MaxFrameSize, FrameSize120ms)
	}
	if MaxFrameBytes != 1275 {
		t.Fatalf("MaxFrameBytes = %d, want 1275", MaxFrameBytes)
	}
	if MaxPacketFrames != 48 {
		t.Fatalf("MaxPacketFrames = %d, want 48", MaxPacketFrames)
	}
	if MaxPacketSize != (MaxFrameBytes+2)*MaxPacketFrames {
		t.Fatalf("MaxPacketSize = %d, want %d", MaxPacketSize, (MaxFrameBytes+2)*MaxPacketFrames)
	}
}

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

func TestNewEncoderWithProfile(t *testing.T) {
	legacy, err := NewEncoderWithProfile(48000, 1, ApplicationAudio, EncoderProfileLegacy)
	if err != nil {
		t.Fatal(err)
	}
	if legacy.Bitrate() != 64000 || legacy.Complexity() != 5 || legacy.VBR() {
		t.Fatalf("legacy defaults = bitrate %d complexity %d VBR %v", legacy.Bitrate(), legacy.Complexity(), legacy.VBR())
	}

	compatible, err := NewEncoderWithProfile(48000, 1, ApplicationAudio, EncoderProfileLibopus)
	if err != nil {
		t.Fatal(err)
	}
	if compatible.Bitrate() != BitrateAuto {
		t.Fatalf("libopus profile bitrate = %d, want BitrateAuto", compatible.Bitrate())
	}
	if compatible.Complexity() != ComplexityDefault {
		t.Fatalf("libopus profile complexity = %d, want %d", compatible.Complexity(), ComplexityDefault)
	}
	if !compatible.VBR() {
		t.Fatal("libopus profile VBR = false")
	}
	packet, err := compatible.Encode(make([]int16, 960), 960)
	if err != nil {
		t.Fatal(err)
	}
	if len(packet) == 0 {
		t.Fatal("libopus profile produced empty packet")
	}

	if _, err := NewEncoderWithProfile(48000, 1, ApplicationAudio, EncoderProfile(99)); !errors.Is(err, ErrBadArg) {
		t.Fatalf("invalid profile error = %v, want ErrBadArg", err)
	}
}

func TestCommonEncoderDecoderGetters(t *testing.T) {
	enc, err := NewEncoder(48000, 2, ApplicationAudio)
	if err != nil {
		t.Fatal(err)
	}
	dec, err := NewDecoder(48000, 2)
	if err != nil {
		t.Fatal(err)
	}

	if enc.SampleRate() != 48000 || dec.SampleRate() != 48000 {
		t.Fatalf("sample rates = encoder %d decoder %d", enc.SampleRate(), dec.SampleRate())
	}
	if enc.Channels() != 2 || dec.Channels() != 2 {
		t.Fatalf("channels = encoder %d decoder %d", enc.Channels(), dec.Channels())
	}
	if enc.Lookahead() != 120 {
		t.Fatalf("lookahead = %d, want 120", enc.Lookahead())
	}
	if enc.VBRConstraint() {
		t.Fatal("legacy CBR encoder reports constrained VBR")
	}
	enc.SetVBR(true)
	if !enc.VBRConstraint() {
		t.Fatal("constrained VBR getter did not track SetVBR(true)")
	}
	enc.SetVBRConstraint(false)
	if enc.VBRConstraint() {
		t.Fatal("VBR constraint getter did not track SetVBRConstraint(false)")
	}
	if err := enc.SetMaxBandwidth(BandwidthWideband); err != nil {
		t.Fatal(err)
	}
	if enc.MaxBandwidth() != BandwidthWideband {
		t.Fatalf("max bandwidth = %d, want %d", enc.MaxBandwidth(), BandwidthWideband)
	}

	pcm := make([]int16, 960*2)
	packet, err := enc.Encode(pcm, 960)
	if err != nil {
		t.Fatal(err)
	}
	out := make([]int16, len(pcm))
	if _, err := dec.Decode(packet, out); err != nil {
		t.Fatal(err)
	}
	if enc.FinalRange() != dec.FinalRange() {
		t.Fatalf("final range mismatch: encoder=%08x decoder=%08x", enc.FinalRange(), dec.FinalRange())
	}
	if dec.Pitch() < 0 {
		t.Fatalf("pitch = %d, want non-negative", dec.Pitch())
	}

	enc.SetDTX(true)
	if _, err := enc.Encode(pcm, 960); err != nil {
		t.Fatal(err)
	}
	if !enc.InDTX() {
		t.Fatal("silent DTX packet did not set InDTX")
	}
	if err := enc.Reset(); err != nil {
		t.Fatal(err)
	}
	if enc.FinalRange() != 0 || enc.InDTX() {
		t.Fatalf("encoder observable state survived reset: range=%08x dtx=%v", enc.FinalRange(), enc.InDTX())
	}
	if err := dec.Reset(); err != nil {
		t.Fatal(err)
	}
	if dec.FinalRange() != 0 || dec.Pitch() != 0 {
		t.Fatalf("decoder observable state survived reset: range=%08x pitch=%d", dec.FinalRange(), dec.Pitch())
	}
}

func TestProductionControls(t *testing.T) {
	enc, err := NewEncoder(48000, 2, ApplicationVOIP)
	if err != nil {
		t.Fatal(err)
	}
	if err := enc.SetForceChannels(ChannelsMono); err != nil {
		t.Fatal(err)
	}
	if enc.ForceChannels() != ChannelsMono {
		t.Fatalf("force channels = %d", enc.ForceChannels())
	}
	if err := enc.SetLSBDepth(16); err != nil {
		t.Fatal(err)
	}
	if enc.LSBDepth() != 16 {
		t.Fatalf("LSB depth = %d", enc.LSBDepth())
	}
	if err := enc.SetLSBDepth(7); !errors.Is(err, ErrBadArg) {
		t.Fatalf("invalid LSB depth error = %v", err)
	}

	pcm := make([]int16, 960*2)
	for i := 0; i < 960; i++ {
		pcm[2*i] = int16(8000 * math.Sin(2*math.Pi*440*float64(i)/48000))
		pcm[2*i+1] = int16(4000 * math.Sin(2*math.Pi*440*float64(i)/48000))
	}
	packet, err := enc.Encode(pcm, 960)
	if err != nil {
		t.Fatal(err)
	}
	if channels, err := PacketGetNumChannels(packet); err != nil || channels != 1 {
		t.Fatalf("forced-mono packet channels = %d, err=%v", channels, err)
	}

	enc.SetPredictionDisabled(true)
	if !enc.PredictionDisabled() {
		t.Fatal("prediction-disabled getter = false")
	}
	packet, err = enc.Encode(pcm, 960)
	if err != nil {
		t.Fatal(err)
	}
	if mode, err := PacketGetMode(packet); err != nil || mode != ModeCELTOnly {
		t.Fatalf("prediction-disabled mode = %d, err=%v", mode, err)
	}

	mono, err := NewEncoder(48000, 1, ApplicationAudio)
	if err != nil {
		t.Fatal(err)
	}
	tone := make([]int16, 960)
	for i := range tone {
		tone[i] = int16(5000 * math.Sin(2*math.Pi*1000*float64(i)/48000))
	}
	packet, err = mono.Encode(tone, 960)
	if err != nil {
		t.Fatal(err)
	}
	base, _ := NewDecoder(48000, 1)
	boosted, _ := NewDecoder(48000, 1)
	if err := boosted.SetGain(6 * 256); err != nil {
		t.Fatal(err)
	}
	if boosted.Gain() != 6*256 {
		t.Fatalf("gain = %d", boosted.Gain())
	}
	basePCM, err := base.DecodeFloat(packet)
	if err != nil {
		t.Fatal(err)
	}
	boostedPCM, err := boosted.DecodeFloat(packet)
	if err != nil {
		t.Fatal(err)
	}
	var baseEnergy, boostedEnergy float64
	for i := range basePCM {
		baseEnergy += basePCM[i] * basePCM[i]
		boostedEnergy += boostedPCM[i] * boostedPCM[i]
	}
	if boostedEnergy <= baseEnergy*3.5 {
		t.Fatalf("decoder gain did not boost energy: base=%g boosted=%g", baseEnergy, boostedEnergy)
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
		{"Automatic", BitrateAuto, false},
		{"Maximum", BitrateMax, false},
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

func TestEncoderBitratePolicies(t *testing.T) {
	mono, err := NewEncoder(48000, 1, ApplicationAudio)
	if err != nil {
		t.Fatal(err)
	}
	if err := mono.SetBitrate(BitrateAuto); err != nil {
		t.Fatal(err)
	}
	if got := mono.Bitrate(); got != BitrateAuto {
		t.Fatalf("Bitrate() = %d, want BitrateAuto", got)
	}
	if _, err := mono.Encode(make([]int16, 960), 960); err != nil {
		t.Fatal(err)
	}
	if got, want := mono.EffectiveBitrate(), 51000; got != want {
		t.Fatalf("mono automatic effective bitrate = %d, want %d", got, want)
	}

	stereo, err := NewEncoder(48000, 2, ApplicationAudio)
	if err != nil {
		t.Fatal(err)
	}
	if err := stereo.SetBitrate(BitrateAuto); err != nil {
		t.Fatal(err)
	}
	if _, err := stereo.Encode(make([]int16, 960*2), 960); err != nil {
		t.Fatal(err)
	}
	if got, want := stereo.EffectiveBitrate(), 99000; got != want {
		t.Fatalf("stereo automatic effective bitrate = %d, want %d", got, want)
	}

	if err := mono.SetBitrate(BitrateMax); err != nil {
		t.Fatal(err)
	}
	packet, err := mono.Encode(make([]int16, 960), 960)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := mono.EffectiveBitrate(), 510000; got != want {
		t.Fatalf("maximum effective bitrate = %d, want %d", got, want)
	}
	if len(packet) != MaxFrameBytes+1 {
		t.Fatalf("maximum bitrate packet size = %d, want %d", len(packet), MaxFrameBytes+1)
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

func TestEncodeFloat32DecodeFloat32(t *testing.T) {
	const (
		sampleRate = 48000
		frameSize  = 960
	)
	enc, err := NewEncoder(sampleRate, 2, ApplicationAudio)
	if err != nil {
		t.Fatal(err)
	}
	input := make([]float32, frameSize*2)
	for i := 0; i < frameSize; i++ {
		input[2*i] = float32(0.4 * math.Sin(2*math.Pi*440*float64(i)/sampleRate))
		input[2*i+1] = float32(0.3 * math.Sin(2*math.Pi*880*float64(i)/sampleRate))
	}
	packet, err := enc.EncodeFloat32(input, frameSize)
	if err != nil {
		t.Fatal(err)
	}
	dec, err := NewDecoder(sampleRate, 2)
	if err != nil {
		t.Fatal(err)
	}
	output, err := dec.DecodeFloat32(packet)
	if err != nil {
		t.Fatal(err)
	}
	if len(output) != len(input) {
		t.Fatalf("DecodeFloat32 length = %d, want %d", len(output), len(input))
	}
	var energy float64
	for _, sample := range output {
		energy += float64(sample) * float64(sample)
	}
	if energy == 0 {
		t.Fatal("DecodeFloat32 returned silent output for non-silent input")
	}
}

func TestEncodeFloat32Errors(t *testing.T) {
	enc, err := NewEncoder(48000, 1, ApplicationAudio)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := enc.EncodeFloat32(make([]float32, 959), 960); !errors.Is(err, ErrBadArg) {
		t.Fatalf("short float32 PCM error = %v, want ErrBadArg", err)
	}
}

func TestDecoderPLC(t *testing.T) {
	const (
		sampleRate = 48000
		channels   = 2
		frameSize  = 960
	)
	enc, err := NewEncoder(sampleRate, channels, ApplicationAudio)
	if err != nil {
		t.Fatal(err)
	}
	pcm := make([]int16, frameSize*channels)
	for i := 0; i < frameSize; i++ {
		pcm[2*i] = int16(12000 * i / frameSize)
		pcm[2*i+1] = -pcm[2*i]
	}
	packet, err := enc.Encode(pcm, frameSize)
	if err != nil {
		t.Fatal(err)
	}

	dec, err := NewDecoder(sampleRate, channels)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dec.Decode(packet, make([]int16, frameSize*channels)); err != nil {
		t.Fatal(err)
	}
	plc := make([]int16, 2*frameSize*channels)
	n, err := dec.DecodePLC(plc, 2*frameSize)
	if err != nil {
		t.Fatalf("DecodePLC failed: %v", err)
	}
	if n != 2*frameSize {
		t.Errorf("DecodePLC returned %d samples, want %d", n, 2*frameSize)
	}
	if got := dec.GetLastPacketDuration(); got != 2*frameSize {
		t.Errorf("last packet duration = %d, want %d", got, 2*frameSize)
	}
}

func TestDecoderPLCValidation(t *testing.T) {
	dec, err := NewDecoder(48000, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dec.DecodePLC(make([]int16, 960), 960); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("DecodePLC without history error = %v, want ErrInvalidState", err)
	}
	if _, err := dec.DecodePLC(make([]int16, 959), 960); !errors.Is(err, ErrBufferTooSmall) {
		t.Fatalf("DecodePLC small buffer error = %v, want ErrBufferTooSmall", err)
	}
	if _, err := dec.DecodePLC(make([]int16, 1000), 1000); !errors.Is(err, ErrUnsupportedFrameSize) {
		t.Fatalf("DecodePLC invalid duration error = %v, want ErrUnsupportedFrameSize", err)
	}
}

func TestDecodeFECRejectsUnsupportedModes(t *testing.T) {
	dec, err := NewDecoder(48000, 1)
	if err != nil {
		t.Fatal(err)
	}
	pcm := []int16{1234}
	if _, err := dec.DecodeFEC([]byte{byte(31 << 3), 0}, pcm); !errors.Is(err, ErrUnimplemented) {
		t.Fatalf("CELT DecodeFEC error = %v, want ErrUnimplemented", err)
	}
	if pcm[0] != 1234 {
		t.Fatalf("DecodeFEC modified output buffer: got %d", pcm[0])
	}
}

func TestDecodeFECMonoSILK(t *testing.T) {
	const (
		rate    = 16000
		bitrate = 24000
	)
	for _, packetMs := range []int{20, 40, 60} {
		t.Run(fmt.Sprintf("%dms", packetMs), func(t *testing.T) {
			frameSize := rate * packetMs / 1000
			enc, err := NewEncoder(rate, 1, ApplicationVOIP)
			if err != nil {
				t.Fatal(err)
			}
			if err := enc.SetBitrate(bitrate); err != nil {
				t.Fatal(err)
			}
			enc.SetPacketLossPerc(20)
			enc.SetInbandFEC(true)

			packets := make([][]byte, 8)
			for p := range packets {
				input := strictSpeechLikeFrame(rate, 1, p*frameSize, frameSize)
				packets[p], err = enc.EncodeFloat(input, frameSize)
				if err != nil {
					t.Fatalf("packet %d: %v", p, err)
				}
			}

			dec, err := NewDecoder(rate, 1)
			if err != nil {
				t.Fatal(err)
			}
			const lost = 4
			for p := 0; p < lost; p++ {
				if _, err := dec.Decode(packets[p], make([]int16, frameSize)); err != nil {
					t.Fatalf("prime packet %d: %v", p, err)
				}
			}
			recovered := make([]int16, frameSize)
			n, err := dec.DecodeFEC(packets[lost+1], recovered)
			if err != nil {
				t.Fatal(err)
			}
			if n != frameSize {
				t.Fatalf("DecodeFEC samples = %d, want %d", n, frameSize)
			}
			var energy int64
			for _, sample := range recovered {
				energy += int64(sample) * int64(sample)
			}
			if energy == 0 {
				t.Fatal("DecodeFEC returned silent recovery")
			}
			if _, err := dec.Decode(packets[lost+1], make([]int16, frameSize)); err != nil {
				t.Fatalf("normal decode after FEC: %v", err)
			}
		})
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
	b.ReportAllocs()
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

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := dec.Decode(compressed, output)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func TestCoreAllocationRegression(t *testing.T) {
	enc, err := NewEncoder(48000, 2, ApplicationAudio)
	if err != nil {
		t.Fatal(err)
	}
	pcm := make([]int16, 960*2)
	if _, err := enc.Encode(pcm, 960); err != nil {
		t.Fatal(err)
	}
	encodeAllocs := testing.AllocsPerRun(20, func() {
		if _, err := enc.Encode(pcm, 960); err != nil {
			panic(err)
		}
	})
	if encodeAllocs > 35 {
		t.Fatalf("Encode allocations = %.1f, want <= 35", encodeAllocs)
	}

	packet, err := enc.Encode(pcm, 960)
	if err != nil {
		t.Fatal(err)
	}
	dec, err := NewDecoder(48000, 2)
	if err != nil {
		t.Fatal(err)
	}
	output := make([]int16, 960*2)
	if _, err := dec.Decode(packet, output); err != nil {
		t.Fatal(err)
	}
	decodeAllocs := testing.AllocsPerRun(20, func() {
		if _, err := dec.Decode(packet, output); err != nil {
			panic(err)
		}
	})
	if decodeAllocs > 38 {
		t.Fatalf("Decode allocations = %.1f, want <= 38", decodeAllocs)
	}
}
