//go:build opusref

package opus

import (
	"encoding/hex"
	"testing"

	"github.com/darui3018823/opus/internal/cgoref"
)

func TestOpusrefDecoderDifferentialMinimized12kSILK(t *testing.T) {
	packet, err := hex.DecodeString("28a719ffff0000ed99f1b2d01e2c68b2d7c7dbc3c8770e3353121667b6714f4e8862354a1342517188de4b7677225224")
	if err != nil {
		t.Fatal(err)
	}
	const (
		rate     = SampleRate12kHz
		channels = ChannelsMono
	)
	if mode, err := PacketGetMode(packet); err != nil || mode != ModeSILKOnly {
		t.Fatalf("mode=%d err=%v, want SILK-only", mode, err)
	}
	if samples, err := PacketGetNumSamples(packet, rate); err != nil || samples != rate/50 {
		t.Fatalf("samples=%d err=%v, want 20 ms at 12 kHz", samples, err)
	}

	goDec, err := NewDecoder(rate, channels)
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	refDec, err := cgoref.NewDecoder(rate, channels)
	if err != nil {
		t.Fatalf("cgoref.NewDecoder: %v", err)
	}
	defer refDec.Close()

	goPCM, goErr := goDec.DecodeFloat32(packet)
	refPCM, refErr := refDec.DecodeFloat(packet, MaxFrameSize*rate/SampleRate48kHz)
	if goErr != nil || refErr != nil {
		t.Fatalf("decode errors: go=%v ref=%v", goErr, refErr)
	}
	compareOpusrefAcceptedDecode(t, goDec, refDec, packet, rate, channels, goPCM, refPCM)
}

func TestOpusrefDecoderDifferentialMinimized48kCELTRandom(t *testing.T) {
	packet, err := hex.DecodeString("807fa500ffe5e5a5a5c3")
	if err != nil {
		t.Fatal(err)
	}
	const (
		rate     = SampleRate48kHz
		channels = ChannelsMono
	)
	if mode, err := PacketGetMode(packet); err != nil || mode != ModeCELTOnly {
		t.Fatalf("mode=%d err=%v, want CELT-only", mode, err)
	}
	if samples, err := PacketGetNumSamples(packet, rate); err != nil || samples != FrameSize2_5ms {
		t.Fatalf("samples=%d err=%v, want 2.5 ms at 48 kHz", samples, err)
	}

	goDec, err := NewDecoder(rate, channels)
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	refDec, err := cgoref.NewDecoder(rate, channels)
	if err != nil {
		t.Fatalf("cgoref.NewDecoder: %v", err)
	}
	defer refDec.Close()

	goPCM, goErr := goDec.DecodeFloat32(packet)
	refPCM, refErr := refDec.DecodeFloat(packet, MaxFrameSize*rate/SampleRate48kHz)
	if goErr != nil || refErr != nil {
		t.Fatalf("decode errors: go=%v ref=%v", goErr, refErr)
	}
	compareOpusrefAcceptedDecode(t, goDec, refDec, packet, rate, channels, goPCM, refPCM)

	stats := opusrefOutputStats(goPCM, refPCM)
	refRange, err := refDec.FinalRange()
	if err != nil {
		t.Fatalf("libopus FinalRange: %v", err)
	}
	t.Logf("CELT random-packet diagnostic: rmsGo=%.6g rmsRef=%.6g rmsDiff=%.6g peakGo=%.6g peakRef=%.6g peakDiff=%.6g goRange=%08x refRange=%08x",
		stats.rmsGo, stats.rmsRef, stats.rmsDiff, stats.peakGo, stats.peakRef, stats.peakDiff, goDec.FinalRange(), refRange)
}

func TestOpusrefDecoderDifferentialMinimized16kSILKRandom(t *testing.T) {
	packet, err := hex.DecodeString("0002ff1513c0937f3c114c38863b34d986304075770a1c0bd5")
	if err != nil {
		t.Fatal(err)
	}
	const (
		rate     = SampleRate16kHz
		channels = ChannelsMono
	)
	if mode, err := PacketGetMode(packet); err != nil || mode != ModeSILKOnly {
		t.Fatalf("mode=%d err=%v, want SILK-only", mode, err)
	}
	if samples, err := PacketGetNumSamples(packet, rate); err != nil || samples != rate/100 {
		t.Fatalf("samples=%d err=%v, want 10 ms at 16 kHz", samples, err)
	}

	goDec, err := NewDecoder(rate, channels)
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	refDec, err := cgoref.NewDecoder(rate, channels)
	if err != nil {
		t.Fatalf("cgoref.NewDecoder: %v", err)
	}
	defer refDec.Close()

	goPCM, goErr := goDec.DecodeFloat32(packet)
	refPCM, refErr := refDec.DecodeFloat(packet, MaxFrameSize*rate/SampleRate48kHz)
	if goErr != nil || refErr != nil {
		t.Fatalf("decode errors: go=%v ref=%v", goErr, refErr)
	}
	compareOpusrefAcceptedDecode(t, goDec, refDec, packet, rate, channels, goPCM, refPCM)

	stats := opusrefOutputStats(goPCM, refPCM)
	refRange, err := refDec.FinalRange()
	if err != nil {
		t.Fatalf("libopus FinalRange: %v", err)
	}
	t.Logf("SILK random-packet diagnostic: rmsGo=%.6g rmsRef=%.6g rmsDiff=%.6g peakGo=%.6g peakRef=%.6g peakDiff=%.6g goRange=%08x refRange=%08x",
		stats.rmsGo, stats.rmsRef, stats.rmsDiff, stats.peakGo, stats.peakRef, stats.peakDiff, goDec.FinalRange(), refRange)
}
