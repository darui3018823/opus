package opus

import (
	"errors"
	"math"
	"testing"
)

func TestPacketInspectionHelpers(t *testing.T) {
	tests := []struct {
		name            string
		packet          []byte
		config          int
		mode            int
		bandwidth       int
		channels        int
		frames          int
		samplesPerFrame int
		totalSamples    int
	}{
		{"CELT fullband stereo 10ms", []byte{byte(30<<3) | 0x04, 0}, 30, ModeCELTOnly, BandwidthFullband, 2, 1, 480, 480},
		{"SILK wideband mono 60ms", []byte{byte(11 << 3), 0}, 11, ModeSILKOnly, BandwidthWideband, 1, 1, 2880, 2880},
		{"hybrid superwideband two 20ms frames", []byte{byte(13<<3) | 0x01, 0, 0}, 13, ModeHybrid, BandwidthSuperWideband, 1, 2, 960, 1920},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			checkPacketValue(t, "config", PacketGetConfig, tc.packet, tc.config)
			checkPacketValue(t, "mode", PacketGetMode, tc.packet, tc.mode)
			checkPacketValue(t, "bandwidth", PacketGetBandwidth, tc.packet, tc.bandwidth)
			checkPacketValue(t, "channels", PacketGetNumChannels, tc.packet, tc.channels)
			checkPacketValue(t, "frames", PacketGetNumFrames, tc.packet, tc.frames)

			got, err := PacketGetSamplesPerFrame(tc.packet, SampleRate48kHz)
			if err != nil || got != tc.samplesPerFrame {
				t.Fatalf("samples per frame = %d, %v; want %d", got, err, tc.samplesPerFrame)
			}
			got, err = PacketGetNumSamples(tc.packet, SampleRate48kHz)
			if err != nil || got != tc.totalSamples {
				t.Fatalf("total samples = %d, %v; want %d", got, err, tc.totalSamples)
			}
		})
	}
}

func checkPacketValue(t *testing.T, name string, fn func([]byte) (int, error), packet []byte, want int) {
	t.Helper()
	got, err := fn(packet)
	if err != nil || got != want {
		t.Fatalf("%s = %d, %v; want %d", name, got, err, want)
	}
}

func TestPacketInspectionErrors(t *testing.T) {
	badPackets := [][]byte{
		nil,
		{byte(31<<3) | 0x01, 0},
		{byte(31<<3) | 0x02, 252},
		{byte(31<<3) | 0x03, 0},
		{byte(31<<3) | 0x03, 7, 0, 0},
	}
	for _, packet := range badPackets {
		if _, err := PacketGetNumSamples(packet, SampleRate48kHz); !errors.Is(err, ErrInvalidPacket) {
			t.Fatalf("PacketGetNumSamples(%x) error = %v, want ErrInvalidPacket", packet, err)
		}
	}
	if _, err := PacketGetNumSamples([]byte{byte(31 << 3), 0}, 44100); !errors.Is(err, ErrUnsupportedSampleRate) || !errors.Is(err, ErrBadArg) {
		t.Fatalf("invalid sample rate error = %v, want ErrUnsupportedSampleRate and ErrBadArg", err)
	}
}

func TestPublicArgumentSentinelErrors(t *testing.T) {
	if _, err := NewEncoder(44100, 1, ApplicationAudio); !errors.Is(err, ErrUnsupportedSampleRate) || !errors.Is(err, ErrBadArg) {
		t.Fatalf("NewEncoder sample rate error = %v", err)
	}
	if _, err := NewDecoder(48000, 3); !errors.Is(err, ErrUnsupportedChannels) || !errors.Is(err, ErrBadArg) {
		t.Fatalf("NewDecoder channels error = %v", err)
	}
	enc, err := NewEncoder(48000, 1, ApplicationAudio)
	if err != nil {
		t.Fatal(err)
	}
	if err := enc.SetBitrate(1); !errors.Is(err, ErrBadArg) {
		t.Fatalf("SetBitrate error = %v, want ErrBadArg", err)
	}
	if err := enc.SetComplexity(11); !errors.Is(err, ErrBadArg) {
		t.Fatalf("SetComplexity error = %v, want ErrBadArg", err)
	}
	if err := enc.SetBandwidth(123); !errors.Is(err, ErrUnsupportedBandwidth) || !errors.Is(err, ErrBadArg) {
		t.Fatalf("SetBandwidth error = %v", err)
	}
	if _, err := enc.Encode(nil, 960); !errors.Is(err, ErrBadArg) {
		t.Fatalf("Encode PCM error = %v, want ErrBadArg", err)
	}
}

func TestPacketHasLBRR(t *testing.T) {
	celtPacket := []byte{byte(31 << 3), 0}
	if got, err := PacketHasLBRR(celtPacket); err != nil || got {
		t.Fatalf("CELT PacketHasLBRR = %v, %v; want false, nil", got, err)
	}

	enc, err := NewEncoder(16000, 1, ApplicationVOIP)
	if err != nil {
		t.Fatal(err)
	}
	if err := enc.SetBitrate(24000); err != nil {
		t.Fatal(err)
	}
	enc.SetInbandFEC(true)
	enc.SetPacketLossPerc(15)
	pcm := make([]int16, 320)
	for i := range pcm {
		pcm[i] = int16(12000 * math.Sin(2*math.Pi*220*float64(i)/16000))
	}
	first, err := enc.Encode(pcm, 320)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := PacketHasLBRR(first); err != nil || got {
		t.Fatalf("first PacketHasLBRR = %v, %v; want false, nil", got, err)
	}
	second, err := enc.Encode(pcm, 320)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := PacketHasLBRR(second); err != nil || !got {
		t.Fatalf("second PacketHasLBRR = %v, %v; want true, nil", got, err)
	}
}

func TestBandwidthGetters(t *testing.T) {
	enc, err := NewEncoder(48000, 1, ApplicationAudio)
	if err != nil {
		t.Fatal(err)
	}
	if err := enc.SetBandwidth(BandwidthWideband); err != nil {
		t.Fatal(err)
	}
	if got := enc.GetBandwidth(); got != BandwidthWideband {
		t.Fatalf("encoder GetBandwidth = %d, want %d", got, BandwidthWideband)
	}
	packet, err := enc.Encode(make([]int16, 960), 960)
	if err != nil {
		t.Fatal(err)
	}
	dec, err := NewDecoder(48000, 1)
	if err != nil {
		t.Fatal(err)
	}
	if got := dec.GetBandwidth(); got != BandwidthAuto {
		t.Fatalf("fresh decoder GetBandwidth = %d, want BandwidthAuto", got)
	}
	pcm := make([]int16, 960)
	if _, err := dec.Decode(packet, pcm); err != nil {
		t.Fatal(err)
	}
	if got := dec.Bandwidth(); got != BandwidthWideband {
		t.Fatalf("decoder Bandwidth = %d, want %d", got, BandwidthWideband)
	}
	if err := dec.Reset(); err != nil {
		t.Fatal(err)
	}
	if got := dec.GetBandwidth(); got != BandwidthAuto {
		t.Fatalf("reset decoder GetBandwidth = %d, want BandwidthAuto", got)
	}
}
