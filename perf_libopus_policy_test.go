package opus

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"math"
	"testing"
)

// The BenchmarkPerf workloads pin ModePolicyLegacy so their TOC mode stays
// fixed. BenchmarkPerfDefault runs the same inputs through the default
// ModePolicyLibopus encoder, which is what NewEncoder users get.

// perfDefaultWorkloads reuses the BenchmarkPerf signals at rates where the
// libopus policy picks each mode. wantMode documents the observed mode.
func perfDefaultWorkloads() []perfWorkload {
	return []perfWorkload{
		{name: "celt/mono/96k", channels: 1, app: ApplicationAudio, bitrate: 96000, signal: SignalMusic, wantMode: "celt", gen: perfMusicFrame},
		{name: "celt/stereo/128k", channels: 2, app: ApplicationAudio, bitrate: 128000, signal: SignalMusic, wantMode: "celt", gen: perfMusicFrame},
		{name: "silk/mono/12k", channels: 1, app: ApplicationVOIP, bitrate: 12000, signal: SignalVoice, wantMode: "silk", gen: perfSpeechFrame},
		{name: "silk/stereo/12k", channels: 2, app: ApplicationVOIP, bitrate: 12000, signal: SignalVoice, wantMode: "silk", gen: perfSpeechFrame},
		{name: "hybrid/mono/24k", channels: 1, app: ApplicationVOIP, bitrate: 24000, signal: SignalVoice, wantMode: "hybrid", gen: perfSpeechFrame},
		{name: "hybrid/stereo/36k", channels: 2, app: ApplicationVOIP, bitrate: 36000, signal: SignalVoice, wantMode: "hybrid", gen: perfSpeechFrame},
	}
}

func newPerfDefaultEncoder(tb testing.TB, wl perfWorkload) *Encoder {
	tb.Helper()
	enc, err := NewEncoder(perfSampleRate, wl.channels, wl.app)
	if err != nil {
		tb.Fatalf("NewEncoder: %v", err)
	}
	if err := enc.SetBitrate(wl.bitrate); err != nil {
		tb.Fatalf("SetBitrate: %v", err)
	}
	enc.SetSignalType(wl.signal)
	return enc
}

func BenchmarkPerfDefault(b *testing.B) {
	for _, wl := range perfDefaultWorkloads() {
		b.Run("encode/"+wl.name, func(b *testing.B) {
			enc := newPerfDefaultEncoder(b, wl)
			frames := perfInputFrames(wl)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				pkt, err := enc.EncodeFloat(frames[i%len(frames)], perfFrameSize)
				if err != nil {
					b.Fatal(err)
				}
				perfPacketSink = pkt
				perfRangeSink = enc.FinalRange()
			}
		})
	}
}

func TestPerfDefaultPolicyPacketRegression(t *testing.T) {
	want := map[string]string{
		"celt/mono/96k":     "eff84d0ddf84d8d2f24727c25fa55883f984f1076042563b45fc554e81eb26c4",
		"celt/stereo/128k":  "16a8defe6e68b3c2354b74343e2782dd683530a210c131e9dcca93692322fe47",
		"silk/mono/12k":     "bfd40c3fe6e1c944bc06f457130d01ec01401ce0d6035ea409b160a7e3443524",
		"silk/stereo/12k":   "86ef4cab99495c0f8711e0b6238c196fb429bc1bfcf7ff7b3eea34c6d09ea52d",
		"hybrid/mono/24k":   "557aac5ae626d7a74f923b75039fcfc643b261a8849426a6d22e62f4f4230b2e",
		"hybrid/stereo/36k": "f57aaf6ccfff38d06f847bbb51f3d7131417fc00fbe84e8d7c90b6c854abbb1b",
	}
	for _, wl := range perfDefaultWorkloads() {
		enc := newPerfDefaultEncoder(t, wl)
		h := sha256.New()
		var word [4]byte
		modes := map[string]int{}
		for i, frame := range perfInputFrames(wl) {
			pkt, err := enc.EncodeFloat(frame, perfFrameSize)
			if err != nil {
				t.Fatalf("%s frame %d: EncodeFloat: %v", wl.name, i, err)
			}
			modes[strictOpusMode(int(pkt[0]>>3))]++
			binary.LittleEndian.PutUint32(word[:], uint32(len(pkt)))
			h.Write(word[:])
			h.Write(pkt)
			binary.LittleEndian.PutUint32(word[:], enc.FinalRange())
			h.Write(word[:])
		}
		if modes[wl.wantMode] != perfFrames {
			t.Errorf("%s: modes %v, want all %s", wl.name, modes, wl.wantMode)
		}
		got := fmt.Sprintf("%x", h.Sum(nil))
		if wantDigest, ok := want[wl.name]; !ok {
			t.Errorf("%s: no golden digest (got %s, modes %v)", wl.name, got, modes)
		} else if got != wantDigest {
			t.Errorf("%s digest = %s, want %s", wl.name, got, wantDigest)
		}
	}
}

// BenchmarkPerfDefaultDecodeFEC decodes default-policy VoIP packets that
// carry in-band FEC (LBRR) data, which the decoder has to parse past on
// every packet.
func BenchmarkPerfDefaultDecodeFEC(b *testing.B) {
	// 12 kb/s is too low for LBRR, so these run above the defaults' rates.
	workloads := []perfWorkload{
		{name: "voip/mono/20k", channels: 1, app: ApplicationVOIP, bitrate: 20000, signal: SignalVoice, gen: perfSpeechFrame},
		{name: "voip/stereo/24k", channels: 2, app: ApplicationVOIP, bitrate: 24000, signal: SignalVoice, gen: perfSpeechFrame},
		{name: "voip/mono/32k", channels: 1, app: ApplicationVOIP, bitrate: 32000, signal: SignalVoice, gen: perfSpeechFrame},
	}
	for _, wl := range workloads {
		b.Run(wl.name, func(b *testing.B) {
			enc := newPerfDefaultEncoder(b, wl)
			enc.SetInbandFEC(true)
			enc.SetPacketLossPerc(20)
			frames := perfInputFrames(wl)
			packets := make([][]byte, len(frames))
			withLBRR := 0
			for i, frame := range frames {
				pkt, err := enc.EncodeFloat(frame, perfFrameSize)
				if err != nil {
					b.Fatalf("packet %d: EncodeFloat: %v", i, err)
				}
				if has, err := PacketHasLBRR(pkt); err == nil && has {
					withLBRR++
				}
				packets[i] = append([]byte(nil), pkt...)
			}
			if withLBRR == 0 {
				b.Fatal("no packet carries LBRR data")
			}
			dec, err := NewDecoder(perfSampleRate, wl.channels)
			if err != nil {
				b.Fatal(err)
			}
			out := make([]int16, perfFrameSize*wl.channels)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				n, err := dec.Decode(packets[i%len(packets)], out)
				if err != nil {
					b.Fatal(err)
				}
				perfSampleSink = n
				perfRangeSink = dec.FinalRange()
			}
		})
	}
}

// TestEncodeInt16AllocsMatchFloat guards the int16 Encode path: it must not
// allocate more per call than EncodeFloat on the same signal (its float64
// conversion buffer is reused across calls).
func TestEncodeInt16AllocsMatchFloat(t *testing.T) {
	wl := perfWorkload{name: "celt/stereo/128k", channels: 2, app: ApplicationAudio, bitrate: 128000, gen: perfMusicFrame}
	frames := perfInputFrames(wl)
	frames16 := make([][]int16, len(frames))
	for i, f := range frames {
		frames16[i] = make([]int16, len(f))
		for j, v := range f {
			frames16[i][j] = int16(math.Round(math.Max(-32768, math.Min(32767, v*32768))))
		}
	}
	encF := newPerfDefaultEncoder(t, wl)
	enc16 := newPerfDefaultEncoder(t, wl)
	for i := 0; i < 50; i++ {
		if _, err := encF.EncodeFloat(frames[i%len(frames)], perfFrameSize); err != nil {
			t.Fatal(err)
		}
		if _, err := enc16.Encode(frames16[i%len(frames16)], perfFrameSize); err != nil {
			t.Fatal(err)
		}
	}
	var iF, i16 int
	floatAllocs := testing.AllocsPerRun(100, func() {
		if _, err := encF.EncodeFloat(frames[iF%len(frames)], perfFrameSize); err != nil {
			t.Fatal(err)
		}
		iF++
	})
	int16Allocs := testing.AllocsPerRun(100, func() {
		if _, err := enc16.Encode(frames16[i16%len(frames16)], perfFrameSize); err != nil {
			t.Fatal(err)
		}
		i16++
	})
	if int16Allocs > floatAllocs {
		t.Fatalf("Encode(int16) allocates %.1f per call, EncodeFloat %.1f", int16Allocs, floatAllocs)
	}
}
