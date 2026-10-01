//go:build opusref

package opus

import (
	"testing"

	"github.com/darui3018823/opus/internal/cgoref"
)

// BenchmarkPerfVsLibopus times the BenchmarkPerf workloads through this
// library (both mode policies) and through the linked libopus (the system
// build, with its SIMD kernels). Every encoder takes the same float32 input
// and every decoder the same libopus-encoded packets.
//
//	go test -tags opusref -run '^$' -bench '^BenchmarkPerfVsLibopus/' -benchtime=200ms -count=5 .
func BenchmarkPerfVsLibopus(b *testing.B) {
	for _, wl := range perfWorkloads() {
		frames := perfInputFrames32(wl)
		b.Run("encode/"+wl.name+"/go-legacy", func(b *testing.B) {
			benchmarkPerfGoEncode32(b, wl, frames, ModePolicyLegacy)
		})
		b.Run("encode/"+wl.name+"/go-libopus", func(b *testing.B) {
			benchmarkPerfGoEncode32(b, wl, frames, ModePolicyLibopus)
		})
		b.Run("encode/"+wl.name+"/libopus", func(b *testing.B) {
			enc := newPerfLibopusEncoder(b, wl)
			defer enc.Close()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				pkt, err := enc.Encode(frames[i%len(frames)], perfFrameSize)
				if err != nil {
					b.Fatal(err)
				}
				perfPacketSink = pkt
			}
		})

		packets := perfLibopusPackets(b, wl, frames)
		b.Run("decode/"+wl.name+"/go", func(b *testing.B) {
			dec, err := NewDecoder(perfSampleRate, wl.channels)
			if err != nil {
				b.Fatal(err)
			}
			out := make([]int16, perfFrameSize*wl.channels)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				n, err := dec.Decode(packets[i%len(packets)], out)
				if err != nil {
					b.Fatal(err)
				}
				perfSampleSink = n
			}
		})
		b.Run("decode/"+wl.name+"/libopus", func(b *testing.B) {
			dec, err := cgoref.NewDecoder(perfSampleRate, wl.channels)
			if err != nil {
				b.Fatal(err)
			}
			defer dec.Close()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				pcm, err := dec.Decode(packets[i%len(packets)], perfFrameSize)
				if err != nil {
					b.Fatal(err)
				}
				perfSampleSink = len(pcm)
			}
		})
	}
}

func perfInputFrames32(wl perfWorkload) [][]float32 {
	frames := perfInputFrames(wl)
	out := make([][]float32, len(frames))
	for i, f := range frames {
		out[i] = make([]float32, len(f))
		for j, v := range f {
			out[i][j] = float32(v)
		}
	}
	return out
}

func benchmarkPerfGoEncode32(b *testing.B, wl perfWorkload, frames [][]float32, policy ModePolicy) {
	enc := newPerfEncoder(b, wl)
	if err := enc.SetModePolicy(policy); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		pkt, err := enc.EncodeFloat32(frames[i%len(frames)], perfFrameSize)
		if err != nil {
			b.Fatal(err)
		}
		perfPacketSink = pkt
	}
}

func newPerfLibopusEncoder(tb testing.TB, wl perfWorkload) *cgoref.Encoder {
	tb.Helper()
	enc, err := cgoref.NewEncoder(perfSampleRate, wl.channels, int(wl.app))
	if err != nil {
		tb.Fatal(err)
	}
	if err := enc.SetBitrate(wl.bitrate); err != nil {
		tb.Fatal(err)
	}
	if wl.signal == SignalMusic {
		err = enc.SetMusicMode()
	} else {
		err = enc.SetVoiceMode()
	}
	if err != nil {
		tb.Fatal(err)
	}
	return enc
}

func perfLibopusPackets(tb testing.TB, wl perfWorkload, frames [][]float32) [][]byte {
	tb.Helper()
	enc := newPerfLibopusEncoder(tb, wl)
	defer enc.Close()
	packets := make([][]byte, len(frames))
	for i, f := range frames {
		pkt, err := enc.Encode(f, perfFrameSize)
		if err != nil {
			tb.Fatal(err)
		}
		packets[i] = pkt
	}
	return packets
}
