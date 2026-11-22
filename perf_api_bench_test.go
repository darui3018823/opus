package opus

import (
	"testing"
)

// Benchmarks for the API paths the 2026-11-22 performance audits found
// outside the codec benchmarks: construction, the FEC and PLC entry points,
// float32 input, multichannel, and packet transformation.

var (
	perfDecoderSink *Decoder
	perfEncoderSink *Encoder
	perfMSDecSink   *SurroundDecoder
)

func BenchmarkPerfConstruct(b *testing.B) {
	b.Run("NewDecoder/48k/stereo", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			d, err := NewDecoder(48000, 2)
			if err != nil {
				b.Fatal(err)
			}
			perfDecoderSink = d
		}
	})
	b.Run("NewEncoder/48k/stereo", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			e, err := NewEncoder(48000, 2, ApplicationAudio)
			if err != nil {
				b.Fatal(err)
			}
			perfEncoderSink = e
		}
	})
	b.Run("NewSurroundDecoder/48k/5.1", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			d, err := NewSurroundDecoder(48000, 6, MappingFamilyVorbis)
			if err != nil {
				b.Fatal(err)
			}
			perfMSDecSink = d
		}
	})
}

func perfFloat32Frames(frames [][]float64) [][]float32 {
	out := make([][]float32, len(frames))
	for i, f := range frames {
		out[i] = make([]float32, len(f))
		for j, v := range f {
			out[i][j] = float32(v)
		}
	}
	return out
}

func BenchmarkPerfEncodeFloat32(b *testing.B) {
	wl := perfWorkload{name: "celt/stereo/128k", channels: 2, app: ApplicationAudio, bitrate: 128000, signal: SignalMusic, gen: perfMusicFrame}
	enc := newPerfDefaultEncoder(b, wl)
	frames := perfFloat32Frames(perfInputFrames(wl))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		pkt, err := enc.EncodeFloat32(frames[i%len(frames)], perfFrameSize)
		if err != nil {
			b.Fatal(err)
		}
		perfPacketSink = pkt
	}
}

// BenchmarkPerfDecodeFECAPI alternates a normal Decode with a DecodeFEC call
// that recovers the next packet from its LBRR data.
func BenchmarkPerfDecodeFECAPI(b *testing.B) {
	wl := perfWorkload{name: "voip/mono/24k", channels: 1, app: ApplicationVOIP, bitrate: 24000, signal: SignalVoice, gen: perfSpeechFrame}
	enc := newPerfDefaultEncoder(b, wl)
	enc.SetInbandFEC(true)
	enc.SetPacketLossPerc(20)
	frames := perfInputFrames(wl)
	packets := make([][]byte, len(frames))
	for i, frame := range frames {
		pkt, err := enc.EncodeFloat(frame, perfFrameSize)
		if err != nil {
			b.Fatal(err)
		}
		packets[i] = append([]byte(nil), pkt...)
	}
	dec, err := NewDecoder(perfSampleRate, wl.channels)
	if err != nil {
		b.Fatal(err)
	}
	out := make([]int16, perfFrameSize*wl.channels)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		p := packets[(2*i)%len(packets)]
		next := packets[(2*i+1)%len(packets)]
		if _, err := dec.Decode(p, out); err != nil {
			b.Fatal(err)
		}
		if _, err := dec.DecodeFEC(next, out); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkPerfDecodePLC conceals one lost frame after every decoded CELT
// packet.
func BenchmarkPerfDecodePLC(b *testing.B) {
	wl := perfWorkload{name: "celt/stereo/128k", channels: 2, app: ApplicationAudio, bitrate: 128000, signal: SignalMusic, gen: perfMusicFrame}
	enc := newPerfDefaultEncoder(b, wl)
	frames := perfInputFrames(wl)
	packets := make([][]byte, len(frames))
	for i, frame := range frames {
		pkt, err := enc.EncodeFloat(frame, perfFrameSize)
		if err != nil {
			b.Fatal(err)
		}
		packets[i] = append([]byte(nil), pkt...)
	}
	dec, err := NewDecoder(perfSampleRate, wl.channels)
	if err != nil {
		b.Fatal(err)
	}
	out := make([]int16, perfFrameSize*wl.channels)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := dec.Decode(packets[i%len(packets)], out); err != nil {
			b.Fatal(err)
		}
		if _, err := dec.DecodePLC(out, perfFrameSize); err != nil {
			b.Fatal(err)
		}
	}
}

func perfMultichannelFrames(channels int) [][]float32 {
	wl := perfWorkload{channels: channels, gen: perfMusicFrame}
	return perfFloat32Frames(perfInputFrames(wl))
}

func BenchmarkPerfSurround51(b *testing.B) {
	enc, err := NewSurroundEncoder(48000, 6, MappingFamilyVorbis, ApplicationAudio)
	if err != nil {
		b.Fatal(err)
	}
	frames := perfMultichannelFrames(6)
	packets := make([][]byte, len(frames))
	for i, f := range frames {
		pkt, err := enc.EncodeFloat32(f, perfFrameSize)
		if err != nil {
			b.Fatal(err)
		}
		packets[i] = append([]byte(nil), pkt...)
	}
	b.Run("encode", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			pkt, err := enc.EncodeFloat32(frames[i%len(frames)], perfFrameSize)
			if err != nil {
				b.Fatal(err)
			}
			perfPacketSink = pkt
		}
	})
	b.Run("decode", func(b *testing.B) {
		dec, err := NewSurroundDecoder(48000, 6, MappingFamilyVorbis)
		if err != nil {
			b.Fatal(err)
		}
		out := make([]int16, perfFrameSize*6)
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			if _, err := dec.Decode(packets[i%len(packets)], out); err != nil {
				b.Fatal(err)
			}
		}
	})
}

func BenchmarkPerfProjectionFOA(b *testing.B) {
	enc, err := NewProjectionEncoder(48000, 4, 3, ApplicationAudio)
	if err != nil {
		b.Fatal(err)
	}
	frames := perfMultichannelFrames(4)
	packets := make([][]byte, len(frames))
	for i, f := range frames {
		pkt, err := enc.EncodeFloat32(f, perfFrameSize)
		if err != nil {
			b.Fatal(err)
		}
		packets[i] = append([]byte(nil), pkt...)
	}
	b.Run("encode", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			pkt, err := enc.EncodeFloat32(frames[i%len(frames)], perfFrameSize)
			if err != nil {
				b.Fatal(err)
			}
			perfPacketSink = pkt
		}
	})
	b.Run("decode", func(b *testing.B) {
		dec, err := NewProjectionDecoder(48000, 4, enc.Streams(), enc.CoupledStreams(), enc.DemixingMatrixBytes())
		if err != nil {
			b.Fatal(err)
		}
		out := make([]int16, perfFrameSize*4)
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			if _, err := dec.Decode(packets[i%len(packets)], out); err != nil {
				b.Fatal(err)
			}
		}
	})
}

func BenchmarkPerfRepacketizer(b *testing.B) {
	wl := perfWorkload{name: "celt/stereo/64k", channels: 2, app: ApplicationAudio, bitrate: 64000, signal: SignalMusic, gen: perfMusicFrame}
	enc := newPerfDefaultEncoder(b, wl)
	frames := perfInputFrames(wl)
	var p0, p1 []byte
	for i := 0; i < 2; i++ {
		pkt, err := enc.EncodeFloat(frames[i], perfFrameSize)
		if err != nil {
			b.Fatal(err)
		}
		if i == 0 {
			p0 = append([]byte(nil), pkt...)
		} else {
			p1 = append([]byte(nil), pkt...)
		}
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r := NewRepacketizer()
		if err := r.Cat(p0); err != nil {
			b.Fatal(err)
		}
		if err := r.Cat(p1); err != nil {
			b.Fatal(err)
		}
		out, err := r.Out()
		if err != nil {
			b.Fatal(err)
		}
		perfPacketSink = out
	}
}
