//go:build opusref

package opus

import (
	"fmt"
	"math"
	"testing"

	"github.com/darui3018823/opus/internal/cgoref"
)

const opusrefFuzzMaxPacketBytes = MaxFrameBytes

var opusrefFuzzRates = [...]int{
	SampleRate8kHz,
	SampleRate12kHz,
	SampleRate16kHz,
	SampleRate24kHz,
	SampleRate48kHz,
}

// FuzzOpusrefDecoderDifferential is a local-only diagnostic target. It compares
// one bounded single-stream packet decode between the Pure Go decoder and
// libopus, focusing on accept/reject, duration, and finite output. It logs gross
// reconstruction divergence as a diagnostic. It intentionally avoids sample-exact PCM checks
// because random accepted packets can exercise PLC-adjacent and mode-transition
// behavior where byte-for-byte waveform identity is not the initial oracle.
func FuzzOpusrefDecoderDifferential(f *testing.F) {
	f.Add([]byte{})
	f.Add(opusrefFuzzInput(SampleRate48kHz, ChannelsStereo, nil))
	f.Add(opusrefFuzzInput(SampleRate48kHz, ChannelsStereo, []byte{0xfc}))
	f.Add(opusrefFuzzInput(SampleRate48kHz, ChannelsStereo, []byte{0xfc, 0xff, 0xff}))
	f.Add(opusrefFuzzInput(SampleRate16kHz, ChannelsMono, []byte{0x00}))
	f.Add(opusrefFuzzInput(SampleRate48kHz, ChannelsMono, []byte{0x80, 0x7f, 0xa5, 0x00, 0xff, 0xe5, 0xe5, 0xa5, 0xa5, 0xc3}))
	f.Add(opusrefFuzzInput(SampleRate16kHz, ChannelsMono, []byte{0x00, 0x02, 0xff, 0x15, 0x13, 0xc0, 0x93, 0x7f, 0x3c, 0x11, 0x4c, 0x38, 0x86, 0x3b, 0x34, 0xd9, 0x86, 0x30, 0x40, 0x75, 0x77, 0x0a, 0x1c, 0x0b, 0xd5}))

	for _, channels := range []int{ChannelsMono, ChannelsStereo} {
		f.Add(opusrefFuzzMustEncode(f, SampleRate48kHz, channels, ApplicationAudio, 64000, FrameSize20ms, opusrefTonePCM))
	}
	for _, rate := range []int{SampleRate8kHz, SampleRate12kHz, SampleRate16kHz} {
		f.Add(opusrefFuzzMustEncode(f, rate, ChannelsMono, ApplicationVOIP, 16000, rate/50, opusrefSpeechPCM))
	}
	for _, rate := range []int{SampleRate24kHz, SampleRate48kHz} {
		f.Add(opusrefFuzzMustEncode(f, rate, ChannelsMono, ApplicationVOIP, 96000, rate/50, opusrefBroadbandSpeechPCM))
	}
	f.Add(opusrefFuzzMustEncode(f, SampleRate48kHz, ChannelsStereo, ApplicationAudio, 96000, 3*FrameSize20ms, opusrefTonePCM))

	f.Fuzz(func(t *testing.T, data []byte) {
		rate, channels, packet, ok := opusrefFuzzDecodeInput(data)
		if !ok {
			return
		}
		maxSPC := MaxFrameSize * rate / SampleRate48kHz

		goDec, err := NewDecoder(rate, channels)
		if err != nil {
			t.Fatalf("NewDecoder(rate=%d, channels=%d): %v", rate, channels, err)
		}
		refDec, err := cgoref.NewDecoder(rate, channels)
		if err != nil {
			t.Fatalf("cgoref.NewDecoder(rate=%d, channels=%d): %v", rate, channels, err)
		}
		defer refDec.Close()

		goPCM, goErr := goDec.DecodeFloat32(packet)
		refPCM, refErr := refDec.DecodeFloat(packet, maxSPC)

		switch {
		case goErr != nil && refErr != nil:
			return
		case goErr == nil && refErr != nil:
			t.Fatalf("Pure Go accepted packet rejected by libopus: rate=%d channels=%d len=%d goSamples=%d refErr=%v packet=%x",
				rate, channels, len(packet), len(goPCM)/channels, refErr, packet)
		case goErr != nil && refErr == nil:
			t.Fatalf("libopus accepted packet rejected by Pure Go: rate=%d channels=%d len=%d refSamples=%d goErr=%v packet=%x",
				rate, channels, len(packet), len(refPCM)/channels, goErr, packet)
		}

		compareOpusrefAcceptedDecode(t, goDec, refDec, packet, rate, channels, goPCM, refPCM)
	})
}

func opusrefFuzzDecodeInput(data []byte) (rate, channels int, packet []byte, ok bool) {
	if len(data) < 2 {
		return 0, 0, nil, false
	}
	rate = opusrefFuzzRates[int(data[0]&0x7f)%len(opusrefFuzzRates)]
	channels = ChannelsMono + int((data[0]>>7)&1)
	packet = data[1:]
	if len(packet) == 0 || len(packet) > opusrefFuzzMaxPacketBytes {
		return 0, 0, nil, false
	}
	return rate, channels, packet, true
}

func compareOpusrefAcceptedDecode(t *testing.T, goDec *Decoder, refDec *cgoref.Decoder, packet []byte, rate, channels int, goPCM, refPCM []float32) {
	t.Helper()
	if len(goPCM)%channels != 0 {
		t.Fatalf("Pure Go output length %d is not divisible by channels=%d", len(goPCM), channels)
	}
	if len(refPCM)%channels != 0 {
		t.Fatalf("libopus output length %d is not divisible by channels=%d", len(refPCM), channels)
	}
	if len(goPCM) != len(refPCM) {
		t.Fatalf("decoded sample count mismatch: rate=%d channels=%d len=%d goSPC=%d refSPC=%d packet=%x",
			rate, channels, len(packet), len(goPCM)/channels, len(refPCM)/channels, packet)
	}
	if len(goPCM) == 0 || len(goPCM)/channels > MaxFrameSize*rate/SampleRate48kHz {
		t.Fatalf("decoded sample count outside Opus bounds: rate=%d channels=%d spc=%d packet=%x",
			rate, channels, len(goPCM)/channels, packet)
	}

	stats := opusrefOutputStats(goPCM, refPCM)
	if !stats.finite {
		t.Fatalf("non-finite decoder output: rate=%d channels=%d len=%d packet=%x", rate, channels, len(packet), packet)
	}
	// This is deliberately a coarse diagnostic, not an audio-quality oracle.
	// Random accepted packets are unstable across non-bit-exact decoders, even
	// when both decoders agree on validity, duration, and finite output. Keep
	// those structural checks hard, and log large waveform deltas for manual
	// triage instead of making random-packet sample equivalence a fuzz oracle.
	mode, _ := PacketGetMode(packet)
	if stats.rmsDiff > 2.0 && stats.peakDiff > 8.0 {
		t.Logf("waveform diagnostic: mode=%d rate=%d channels=%d len=%d rmsGo=%.6g rmsRef=%.6g rmsDiff=%.6g peakGo=%.6g peakRef=%.6g peakDiff=%.6g packet=%x",
			mode, rate, channels, len(packet), stats.rmsGo, stats.rmsRef, stats.rmsDiff, stats.peakGo, stats.peakRef, stats.peakDiff, packet)
	}
	if mode == ModeCELTOnly && stats.rmsDiff > 2.0 && stats.peakDiff > 8.0 {
		t.Logf("CELT waveform diagnostic: rate=%d channels=%d len=%d rmsGo=%.6g rmsRef=%.6g rmsDiff=%.6g peakGo=%.6g peakRef=%.6g peakDiff=%.6g packet=%x",
			rate, channels, len(packet), stats.rmsGo, stats.rmsRef, stats.rmsDiff, stats.peakGo, stats.peakRef, stats.peakDiff, packet)
	}

	compareOpusrefFinalRange(t, goDec, refDec, packet, rate, channels)
}

type opusrefDecodeStats struct {
	finite   bool
	rmsGo    float64
	rmsRef   float64
	rmsDiff  float64
	peakDiff float64
	peakGo   float64
	peakRef  float64
}

func opusrefOutputStats(goPCM, refPCM []float32) opusrefDecodeStats {
	stats := opusrefDecodeStats{finite: true}
	var go2, ref2, diff2 float64
	for i := range goPCM {
		goV := float64(goPCM[i])
		refV := float64(refPCM[i])
		if math.IsNaN(goV) || math.IsInf(goV, 0) || math.IsNaN(refV) || math.IsInf(refV, 0) {
			stats.finite = false
		}
		goAbs := math.Abs(goV)
		refAbs := math.Abs(refV)
		diffAbs := math.Abs(goV - refV)
		if goAbs > stats.peakGo {
			stats.peakGo = goAbs
		}
		if refAbs > stats.peakRef {
			stats.peakRef = refAbs
		}
		if diffAbs > stats.peakDiff {
			stats.peakDiff = diffAbs
		}
		go2 += goV * goV
		ref2 += refV * refV
		diff2 += diffAbs * diffAbs
	}
	if len(goPCM) > 0 {
		stats.rmsGo = math.Sqrt(go2 / float64(len(goPCM)))
		stats.rmsRef = math.Sqrt(ref2 / float64(len(goPCM)))
		stats.rmsDiff = math.Sqrt(diff2 / float64(len(goPCM)))
	}
	return stats
}

func compareOpusrefFinalRange(t *testing.T, goDec *Decoder, refDec *cgoref.Decoder, packet []byte, rate, channels int) {
	t.Helper()
	mode, err := PacketGetMode(packet)
	if err != nil || mode != ModeCELTOnly {
		return
	}
	frames, err := PacketGetNumFrames(packet)
	if err != nil || frames != 1 || len(packet) <= 1 {
		return
	}
	refRange, err := refDec.FinalRange()
	if err != nil {
		t.Fatalf("libopus FinalRange: %v", err)
	}
	if goRange := goDec.FinalRange(); goRange != refRange {
		t.Logf("CELT final range diagnostic mismatch: rate=%d channels=%d len=%d go=%08x ref=%08x packet=%x",
			rate, channels, len(packet), goRange, refRange, packet)
	}
}

func opusrefFuzzInput(rate, channels int, packet []byte) []byte {
	rateIdx := 0
	for i, r := range opusrefFuzzRates {
		if r == rate {
			rateIdx = i
			break
		}
	}
	descriptor := byte(rateIdx)
	if channels == ChannelsStereo {
		descriptor |= 0x80
	}
	out := make([]byte, 1, 1+len(packet))
	out[0] = descriptor
	out = append(out, packet...)
	return out
}

func opusrefFuzzMustEncode(f *testing.F, rate, channels, application, bitrate, frameSize int, gen func(sample, rate, channels int) float32) []byte {
	f.Helper()
	enc, err := NewEncoder(rate, channels, application)
	if err != nil {
		f.Fatalf("NewEncoder(%d, %d, %d): %v", rate, channels, application, err)
	}
	if bitrate > 0 {
		if err := enc.SetBitrate(bitrate); err != nil {
			f.Fatalf("SetBitrate(%d): %v", bitrate, err)
		}
	}
	pcm := make([]float32, frameSize*channels)
	for i := 0; i < frameSize; i++ {
		for ch := 0; ch < channels; ch++ {
			pcm[i*channels+ch] = gen(i+ch*97, rate, channels)
		}
	}
	packet, err := enc.EncodeFloat32(pcm, frameSize)
	if err != nil {
		f.Fatalf("EncodeFloat32(rate=%d channels=%d frameSize=%d): %v", rate, channels, frameSize, err)
	}
	if len(packet) == 0 || len(packet) > opusrefFuzzMaxPacketBytes {
		f.Fatalf("generated seed packet has invalid length %d", len(packet))
	}
	if gotRate, gotChannels, gotPacket, ok := opusrefFuzzDecodeInput(opusrefFuzzInput(rate, channels, packet)); !ok || gotRate != rate || gotChannels != channels || len(gotPacket) != len(packet) {
		f.Fatalf("generated seed input did not round-trip descriptor: %s", fmt.Sprintf("rate=%d channels=%d len=%d", gotRate, gotChannels, len(gotPacket)))
	}
	return opusrefFuzzInput(rate, channels, packet)
}

func opusrefTonePCM(sample, rate, channels int) float32 {
	phase := 2 * math.Pi * 440 * float64(sample) / float64(rate)
	return float32(0.45 * math.Sin(phase))
}

func opusrefSpeechPCM(sample, rate, channels int) float32 {
	t := float64(sample) / float64(rate)
	carrier := math.Sin(2 * math.Pi * 180 * t)
	formant := 0.35 * math.Sin(2*math.Pi*720*t+0.2)
	envelope := 0.55 + 0.45*math.Sin(2*math.Pi*3*t)
	return float32(0.38 * envelope * (carrier + formant))
}

func opusrefBroadbandSpeechPCM(sample, rate, channels int) float32 {
	t := float64(sample) / float64(rate)
	low := 0.28 * math.Sin(2*math.Pi*210*t)
	mid := 0.20 * math.Sin(2*math.Pi*1300*t+0.3)
	high := 0.15 * math.Sin(2*math.Pi*6400*t+0.7)
	if channels == ChannelsStereo && sample%2 == 1 {
		high *= -0.8
	}
	return float32(low + mid + high)
}
