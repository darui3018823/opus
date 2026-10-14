package opus

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
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
