package opus

import (
	"errors"
	"fmt"
	"math"
	"os"
	"strings"
	"testing"
)

func TestVersionMetadata(t *testing.T) {
	want := fmt.Sprintf("%d.%d.%d", VersionMajor, VersionMinor, VersionPatch)
	if Version != want {
		t.Fatalf("Version = %q, components produce %q", Version, want)
	}
	raw, err := os.ReadFile("VERSION")
	if err != nil {
		t.Fatal(err)
	}
	if source := strings.TrimSpace(string(raw)); Version != source {
		t.Fatalf("Version = %q, VERSION contains %q; run go generate ./...", Version, source)
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
	if got := enc.SignalType(); got != SignalAuto {
		t.Fatalf("signal type changed after rejected setter: got %d, want %d", got, SignalAuto)
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

func TestEncoderSignalSettingIsIndependentOfApplication(t *testing.T) {
	const (
		rate      = 48000
		frameSize = 960
	)
	input := strictSpeechLikeFrame(rate, 1, 0, frameSize)
	for _, tc := range []struct {
		application Application
		effective   SignalType
	}{
		{ApplicationVOIP, SignalVoice},
		{ApplicationAudio, SignalMusic},
		{ApplicationRestrictedLowDelay, SignalMusic},
	} {
		auto, err := NewEncoder(rate, 1, tc.application)
		if err != nil {
			t.Fatal(err)
		}
		explicit, err := NewEncoder(rate, 1, tc.application)
		if err != nil {
			t.Fatal(err)
		}
		if got := auto.SignalType(); got != SignalAuto {
			t.Fatalf("application %d default SignalType=%d, want Auto", tc.application, got)
		}
		explicit.SetSignalType(tc.effective)
		autoPacket, err := auto.EncodeFloat(input, frameSize)
		if err != nil {
			t.Fatal(err)
		}
		explicitPacket, err := explicit.EncodeFloat(input, frameSize)
		if err != nil {
			t.Fatal(err)
		}
		if string(autoPacket) != string(explicitPacket) || auto.FinalRange() != explicit.FinalRange() {
			t.Fatalf("application %d Auto changed the effective default", tc.application)
		}
	}

	first, err := NewEncoder(rate, 1, ApplicationVOIP)
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewEncoder(rate, 1, ApplicationVOIP)
	if err != nil {
		t.Fatal(err)
	}
	first.SetSignalType(SignalVoice)
	if err := first.SetApplication(ApplicationAudio); err != nil {
		t.Fatal(err)
	}
	if err := second.SetApplication(ApplicationAudio); err != nil {
		t.Fatal(err)
	}
	second.SetSignalType(SignalVoice)
	if got := first.SignalType(); got != SignalVoice {
		t.Fatalf("explicit signal overwritten by application: got %d", got)
	}
	firstPacket, err := first.EncodeFloat(input, frameSize)
	if err != nil {
		t.Fatal(err)
	}
	secondPacket, err := second.EncodeFloat(input, frameSize)
	if err != nil {
		t.Fatal(err)
	}
	if string(firstPacket) != string(secondPacket) || first.FinalRange() != second.FinalRange() {
		t.Fatal("signal/application setter order changed output")
	}

	first.SetSignalType(SignalAuto)
	if err := first.Reset(); err != nil {
		t.Fatal(err)
	}
	if got := first.SignalType(); got != SignalAuto {
		t.Fatalf("SignalType after Auto+Reset=%d, want Auto", got)
	}

	stereo, err := NewEncoder(rate, 2, ApplicationVOIP)
	if err != nil {
		t.Fatal(err)
	}
	if err := stereo.SetForceChannels(ChannelsMono); err != nil {
		t.Fatal(err)
	}
	stereo.SetSignalType(SignalVoice)
	if err := stereo.SetApplication(ApplicationAudio); err != nil {
		t.Fatal(err)
	}
	if _, err := stereo.EncodeFloat(strictSpeechLikeFrame(rate, 2, 0, frameSize), frameSize); err != nil {
		t.Fatal(err)
	}
	if stereo.forcedMono == nil || stereo.forcedMono.SignalType() != SignalVoice {
		t.Fatal("forced-mono child did not retain explicit signal setting")
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

func TestDecoderPLCSILKAndHybrid(t *testing.T) {
	tests := []struct {
		name     string
		rate     int
		channels int
		bitrate  int
		wantMode int
	}{
		{"silk-mono", 16000, 1, 24000, ModeSILKOnly},
		{"silk-stereo", 16000, 2, 32000, ModeSILKOnly},
		{"hybrid-mono", 48000, 1, 64000, ModeHybrid},
		{"hybrid-stereo", 48000, 2, 160000, ModeHybrid},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			frameSize := tc.rate / 50
			enc, err := NewEncoder(tc.rate, tc.channels, ApplicationVOIP)
			if err != nil {
				t.Fatal(err)
			}
			if err := enc.SetBitrate(tc.bitrate); err != nil {
				t.Fatal(err)
			}

			packets := make([][]byte, 8)
			for p := range packets {
				input := strictSpeechLikeFrame(tc.rate, tc.channels, p*frameSize, frameSize)
				if tc.wantMode == ModeHybrid {
					input = strictHybridWidebandFrame(tc.rate, tc.channels, p*frameSize, frameSize)
				}
				packets[p], err = enc.EncodeFloat(input, frameSize)
				if err != nil {
					t.Fatalf("encode packet %d: %v", p, err)
				}
			}
			if mode, err := PacketGetMode(packets[3]); err != nil || mode != tc.wantMode {
				t.Fatalf("packet mode = %d, err=%v, want %d", mode, err, tc.wantMode)
			}

			dec, err := NewDecoder(tc.rate, tc.channels)
			if err != nil {
				t.Fatal(err)
			}
			for p := 0; p < 4; p++ {
				if _, err := dec.Decode(packets[p], make([]int16, frameSize*tc.channels)); err != nil {
					t.Fatalf("prime packet %d: %v", p, err)
				}
			}

			plc := make([]int16, 2*frameSize*tc.channels)
			n, err := dec.DecodePLC(plc, 2*frameSize)
			if err != nil {
				t.Fatalf("DecodePLC: %v", err)
			}
			if n != 2*frameSize {
				t.Fatalf("DecodePLC samples = %d, want %d", n, 2*frameSize)
			}
			firstEnergy := signalEnergyI16(plc[:frameSize*tc.channels])
			secondEnergy := signalEnergyI16(plc[frameSize*tc.channels:])
			if firstEnergy == 0 || secondEnergy == 0 {
				t.Fatalf("PLC returned silence: first=%g second=%g", firstEnergy, secondEnergy)
			}
			if secondEnergy >= firstEnergy {
				t.Fatalf("PLC energy did not decay: first=%g second=%g", firstEnergy, secondEnergy)
			}

			recovered := make([]int16, frameSize*tc.channels)
			if _, err := dec.Decode(packets[6], recovered); err != nil {
				t.Fatalf("normal decode after PLC: %v", err)
			}
			for ch := 0; ch < tc.channels; ch++ {
				last := plc[(2*frameSize-1)*tc.channels+ch]
				jump := math.Abs(float64(recovered[ch]) - float64(last))
				if jump > 6000 {
					t.Fatalf("recovery boundary jump on channel %d is too large: %.0f", ch, jump)
				}
			}
		})
	}
}

func TestDecoderPLCSILKFrameAlignment(t *testing.T) {
	const (
		rate      = 16000
		frameSize = rate / 50
	)
	enc, err := NewEncoder(rate, 1, ApplicationVOIP)
	if err != nil {
		t.Fatal(err)
	}
	if err := enc.SetBitrate(24000); err != nil {
		t.Fatal(err)
	}
	packet, err := enc.EncodeFloat(strictSpeechLikeFrame(rate, 1, 0, frameSize), frameSize)
	if err != nil {
		t.Fatal(err)
	}
	dec, err := NewDecoder(rate, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dec.Decode(packet, make([]int16, frameSize)); err != nil {
		t.Fatal(err)
	}
	if n, err := dec.DecodePLC(make([]int16, frameSize/2), frameSize/2); err != nil || n != frameSize/2 {
		t.Fatalf("10 ms PLC after 20 ms SILK = (%d, %v), want (%d, nil)", n, err, frameSize/2)
	}
}

func TestDecoderPLCSILKLongPacketDurations(t *testing.T) {
	const rate = 16000
	for _, packetMs := range []int{40, 60} {
		t.Run(fmt.Sprintf("%dms", packetMs), func(t *testing.T) {
			frameSize := rate * packetMs / 1000
			enc, err := NewEncoder(rate, 1, ApplicationVOIP)
			if err != nil {
				t.Fatal(err)
			}
			if err := enc.SetBitrate(24000); err != nil {
				t.Fatal(err)
			}
			packet, err := enc.EncodeFloat(strictSpeechLikeFrame(rate, 1, 0, frameSize), frameSize)
			if err != nil {
				t.Fatal(err)
			}
			if mode, err := PacketGetMode(packet); err != nil || mode != ModeSILKOnly {
				t.Fatalf("packet mode = %d, err=%v, want SILK-only", mode, err)
			}
			dec, err := NewDecoder(rate, 1)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := dec.Decode(packet, make([]int16, frameSize)); err != nil {
				t.Fatal(err)
			}
			plc := make([]int16, frameSize)
			if n, err := dec.DecodePLC(plc, frameSize); err != nil || n != frameSize {
				t.Fatalf("DecodePLC = (%d, %v), want (%d, nil)", n, err, frameSize)
			}
			if signalEnergyI16(plc) == 0 {
				t.Fatal("DecodePLC returned silence")
			}
		})
	}
}

func TestDecoderPLCValidation(t *testing.T) {
	dec, err := NewDecoder(48000, 1)
	if err != nil {
		t.Fatal(err)
	}
	initial := make([]int16, 960)
	if n, err := dec.DecodePLC(initial, 960); err != nil || n != 960 {
		t.Fatalf("DecodePLC without history = (%d, %v), want (960, nil)", n, err)
	}
	if signalEnergyI16(initial) != 0 {
		t.Fatal("DecodePLC without history returned non-zero PCM")
	}
	if dec.FinalRange() != 0 {
		t.Fatalf("initial PLC final range = %08x, want 0", dec.FinalRange())
	}
	if _, err := dec.DecodePLC(make([]int16, 959), 960); !errors.Is(err, ErrBufferTooSmall) {
		t.Fatalf("DecodePLC small buffer error = %v, want ErrBufferTooSmall", err)
	}
	if _, err := dec.DecodePLC(make([]int16, 1000), 1000); !errors.Is(err, ErrUnsupportedFrameSize) {
		t.Fatalf("DecodePLC invalid duration error = %v, want ErrUnsupportedFrameSize", err)
	}
}

// A burst loss recovered with FEC is often followed by another loss; the PLC
// history established by DecodeFEC must be usable by DecodePLC.
func TestDecoderPLCAfterFEC(t *testing.T) {
	tests := []struct {
		name     string
		rate     int
		bitrate  int
		wantMode int
	}{
		{"silk-mono", 16000, 24000, ModeSILKOnly},
		{"hybrid-mono", 48000, 64000, ModeHybrid},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			frameSize := tc.rate / 50
			enc, err := NewEncoder(tc.rate, 1, ApplicationVOIP)
			if err != nil {
				t.Fatal(err)
			}
			if err := enc.SetBitrate(tc.bitrate); err != nil {
				t.Fatal(err)
			}
			enc.SetPacketLossPerc(20)
			enc.SetInbandFEC(true)

			packets := make([][]byte, 8)
			for p := range packets {
				input := strictSpeechLikeFrame(tc.rate, 1, p*frameSize, frameSize)
				if tc.wantMode == ModeHybrid {
					input = strictHybridWidebandFrame(tc.rate, 1, p*frameSize, frameSize)
				}
				packets[p], err = enc.EncodeFloat(input, frameSize)
				if err != nil {
					t.Fatalf("packet %d: %v", p, err)
				}
			}
			if mode, err := PacketGetMode(packets[3]); err != nil || mode != tc.wantMode {
				t.Fatalf("packet mode = %d, err=%v, want %d", mode, err, tc.wantMode)
			}

			dec, err := NewDecoder(tc.rate, 1)
			if err != nil {
				t.Fatal(err)
			}
			for p := 0; p < 4; p++ {
				if _, err := dec.Decode(packets[p], make([]int16, frameSize)); err != nil {
					t.Fatalf("prime packet %d: %v", p, err)
				}
			}
			// Packet 4 is lost: recover it from packet 5's LBRR. Packet 5 is
			// then also lost and must be concealed.
			if _, err := dec.DecodeFEC(packets[5], make([]int16, frameSize)); err != nil {
				t.Fatalf("DecodeFEC: %v", err)
			}
			plc := make([]int16, frameSize)
			if n, err := dec.DecodePLC(plc, frameSize); err != nil || n != frameSize {
				t.Fatalf("DecodePLC after DecodeFEC = (%d, %v), want (%d, nil)", n, err, frameSize)
			}
			if signalEnergyI16(plc) == 0 {
				t.Fatal("DecodePLC after DecodeFEC returned silence")
			}
		})
	}
}

// After digital-silence SILK packets, concealment must continue the silence
// instead of replaying stale pre-silence speech from the synthesis history.
func TestDecoderPLCAfterSILKSilence(t *testing.T) {
	tests := []struct {
		name     string
		channels int
		bitrate  int
	}{
		{"mono", 1, 24000},
		{"stereo", 2, 32000},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			const rate = 16000
			frameSize := rate / 50
			enc, err := NewEncoder(rate, tc.channels, ApplicationVOIP)
			if err != nil {
				t.Fatal(err)
			}
			if err := enc.SetBitrate(tc.bitrate); err != nil {
				t.Fatal(err)
			}

			packets := make([][]byte, 4)
			for p := range packets {
				input := strictSpeechLikeFrame(rate, tc.channels, p*frameSize, frameSize)
				packets[p], err = enc.EncodeFloat(input, frameSize)
				if err != nil {
					t.Fatalf("packet %d: %v", p, err)
				}
			}
			if mode, err := PacketGetMode(packets[0]); err != nil || mode != ModeSILKOnly {
				t.Fatalf("packet mode = %d, err=%v, want SILK-only", mode, err)
			}
			if packets[0][0]&0x03 != 0 {
				t.Fatalf("expected a code-0 packet, TOC = %#02x", packets[0][0])
			}

			dec, err := NewDecoder(rate, tc.channels)
			if err != nil {
				t.Fatal(err)
			}
			out := make([]int16, frameSize*tc.channels)
			for p := range packets {
				if _, err := dec.Decode(packets[p], out); err != nil {
					t.Fatalf("prime packet %d: %v", p, err)
				}
			}
			// RFC 6716 digital silence: same TOC, single zero payload byte.
			silence := []byte{packets[0][0], 0x00}
			for i := 0; i < 2; i++ {
				if _, err := dec.Decode(silence, out); err != nil {
					t.Fatalf("silence packet %d: %v", i, err)
				}
			}

			plc := make([]int16, frameSize*tc.channels)
			n, err := dec.DecodePLC(plc, frameSize)
			if err != nil || n != frameSize {
				t.Fatalf("DecodePLC = (%d, %v), want (%d, nil)", n, err, frameSize)
			}
			if energy := signalEnergyI16(plc); energy != 0 {
				t.Fatalf("PLC after digital silence has energy %g, want continued silence", energy)
			}
		})
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

func TestDecodeFECMalformedSILKDoesNotPanic(t *testing.T) {
	dec, err := NewDecoder(16000, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("DecodeFEC panicked on malformed SILK packet: %v", r)
		}
	}()
	pcm := make([]int16, 320)
	if _, err := dec.DecodeFEC([]byte{0x01, 0x00}, pcm); err == nil {
		t.Fatal("DecodeFEC accepted malformed SILK FEC packet")
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

func TestDecodeFECStereoAndHybrid(t *testing.T) {
	tests := []struct {
		name     string
		rate     int
		channels int
		bitrate  int
		wantMode int
	}{
		{"stereo-silk", 16000, 2, 32000, ModeSILKOnly},
		{"mono-hybrid", 48000, 1, 64000, ModeHybrid},
		{"stereo-hybrid", 48000, 2, 160000, ModeHybrid},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			frameSize := tc.rate / 50
			enc, err := NewEncoder(tc.rate, tc.channels, ApplicationVOIP)
			if err != nil {
				t.Fatal(err)
			}
			if err := enc.SetBitrate(tc.bitrate); err != nil {
				t.Fatal(err)
			}
			enc.SetPacketLossPerc(20)
			enc.SetInbandFEC(true)

			packets := make([][]byte, 7)
			for p := range packets {
				input := strictSpeechLikeFrame(tc.rate, tc.channels, p*frameSize, frameSize)
				// Keep the hybrid fixtures broadband enough to retain SWB/FB.
				if tc.wantMode == ModeHybrid {
					for i := 0; i < frameSize; i++ {
						for ch := 0; ch < tc.channels; ch++ {
							input[i*tc.channels+ch] += 0.025 * math.Sin(2*math.Pi*10000*float64(p*frameSize+i)/float64(tc.rate))
						}
					}
				}
				packets[p], err = enc.EncodeFloat(input, frameSize)
				if err != nil {
					t.Fatalf("packet %d: %v", p, err)
				}
			}
			if mode, err := PacketGetMode(packets[5]); err != nil || mode != tc.wantMode {
				t.Fatalf("packet mode = %d, err=%v, want %d", mode, err, tc.wantMode)
			}

			dec, err := NewDecoder(tc.rate, tc.channels)
			if err != nil {
				t.Fatal(err)
			}
			const lost = 4
			for p := 0; p < lost; p++ {
				if _, err := dec.Decode(packets[p], make([]int16, frameSize*tc.channels)); err != nil {
					t.Fatalf("prime packet %d: %v", p, err)
				}
			}
			recovered := make([]int16, frameSize*tc.channels)
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
			if _, err := dec.Decode(packets[lost+1], make([]int16, frameSize*tc.channels)); err != nil {
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
