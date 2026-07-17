package opus

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"math"
	"testing"
)

const (
	perfSampleRate = 48000
	perfFrameSize  = perfSampleRate * 20 / 1000
	perfFrames     = 64
)

type perfWorkload struct {
	name     string
	channels int
	app      Application
	bitrate  int
	signal   SignalType
	wantMode string
	gen      func(start, n, channels int) []float64
}

var (
	perfPacketSink []byte
	perfSampleSink int
	perfRangeSink  uint32
)

func BenchmarkPerf(b *testing.B) {
	for _, wl := range perfWorkloads() {
		wl := wl
		b.Run("encode/"+wl.name, func(b *testing.B) {
			benchmarkPerfEncode(b, wl)
		})
		b.Run("decode/"+wl.name, func(b *testing.B) {
			benchmarkPerfDecode(b, wl)
		})
	}
}

func TestPerfPredictivePacketRegression(t *testing.T) {
	want := map[string]string{
		"silk/mono/48k/20ms":     "9283a266eb02e57c13033cdcf88f00bf15a4ce720d42a97db0b92e63f5ba22f4",
		"silk/stereo/48k/20ms":   "b549162a152534ca8f20485fe4f6daa59086a2a97648fa55fb57096224b58ff6",
		"hybrid/mono/48k/20ms":   "a02e6ec4569c0811f33123dcdefd65f00b09ab14364c6c57d54d6454a9636183",
		"hybrid/stereo/48k/20ms": "ea2513c3685530f438660e63d2163fa67c12de34fd2e015327267ef394ef2bf4",
	}
	for _, wl := range perfWorkloads() {
		wantDigest, ok := want[wl.name]
		if !ok {
			continue
		}
		enc := newPerfEncoder(t, wl)
		frames := perfInputFrames(wl)
		h := sha256.New()
		var word [4]byte
		for i, frame := range frames {
			pkt, err := enc.EncodeFloat(frame, perfFrameSize)
			if err != nil {
				t.Fatalf("%s frame %d: EncodeFloat: %v", wl.name, i, err)
			}
			binary.LittleEndian.PutUint32(word[:], uint32(len(pkt)))
			h.Write(word[:])
			h.Write(pkt)
			binary.LittleEndian.PutUint32(word[:], enc.FinalRange())
			h.Write(word[:])
		}
		got := fmt.Sprintf("%x", h.Sum(nil))
		if got != wantDigest {
			t.Errorf("%s digest = %s, want %s", wl.name, got, wantDigest)
		}
	}
}

func benchmarkPerfEncode(b *testing.B, wl perfWorkload) {
	enc := newPerfEncoder(b, wl)
	frames := perfInputFrames(wl)
	if err := validatePerfPacket(enc, wl, frames[0]); err != nil {
		b.Fatal(err)
	}

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
}

func benchmarkPerfDecode(b *testing.B, wl perfWorkload) {
	packets := perfPackets(b, wl)
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
}

func perfWorkloads() []perfWorkload {
	return []perfWorkload{
		{
			name:     "celt/mono/48k/20ms",
			channels: 1,
			app:      ApplicationAudio,
			bitrate:  96000,
			signal:   SignalMusic,
			wantMode: "celt",
			gen:      perfMusicFrame,
		},
		{
			name:     "celt/stereo/48k/20ms",
			channels: 2,
			app:      ApplicationAudio,
			bitrate:  128000,
			signal:   SignalMusic,
			wantMode: "celt",
			gen:      perfMusicFrame,
		},
		{
			name:     "silk/mono/48k/20ms",
			channels: 1,
			app:      ApplicationVOIP,
			bitrate:  24000,
			signal:   SignalVoice,
			wantMode: "silk",
			gen:      perfSpeechFrame,
		},
		{
			name:     "silk/stereo/48k/20ms",
			channels: 2,
			app:      ApplicationVOIP,
			bitrate:  36000,
			signal:   SignalVoice,
			wantMode: "silk",
			gen:      perfSpeechFrame,
		},
		{
			name:     "hybrid/mono/48k/20ms",
			channels: 1,
			app:      ApplicationVOIP,
			bitrate:  64000,
			signal:   SignalVoice,
			wantMode: "hybrid",
			gen:      perfHybridFrame,
		},
		{
			name:     "hybrid/stereo/48k/20ms",
			channels: 2,
			app:      ApplicationVOIP,
			bitrate:  96000,
			signal:   SignalVoice,
			wantMode: "hybrid",
			gen:      perfHybridFrame,
		},
	}
}

func newPerfEncoder(tb testing.TB, wl perfWorkload) *Encoder {
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

func perfInputFrames(wl perfWorkload) [][]float64 {
	frames := make([][]float64, perfFrames)
	for i := range frames {
		frames[i] = wl.gen(i*perfFrameSize, perfFrameSize, wl.channels)
	}
	return frames
}

func perfPackets(tb testing.TB, wl perfWorkload) [][]byte {
	tb.Helper()
	enc := newPerfEncoder(tb, wl)
	frames := perfInputFrames(wl)
	packets := make([][]byte, len(frames))
	for i, frame := range frames {
		pkt, err := enc.EncodeFloat(frame, perfFrameSize)
		if err != nil {
			tb.Fatalf("packet %d: EncodeFloat: %v", i, err)
		}
		if err := validatePerfPacketBytes(wl, pkt); err != nil {
			tb.Fatalf("packet %d: %v", i, err)
		}
		packets[i] = pkt
	}
	return packets
}

func validatePerfPacket(enc *Encoder, wl perfWorkload, frame []float64) error {
	pkt, err := enc.EncodeFloat(frame, perfFrameSize)
	if err != nil {
		return err
	}
	return validatePerfPacketBytes(wl, pkt)
}

func validatePerfPacketBytes(wl perfWorkload, pkt []byte) error {
	if len(pkt) == 0 {
		return fmt.Errorf("empty packet")
	}
	config := int(pkt[0] >> 3)
	if got := strictOpusMode(config); got != wl.wantMode {
		return fmt.Errorf("TOC config=%d mode=%s, want %s", config, got, wl.wantMode)
	}
	samples, err := PacketGetNumSamples(pkt, perfSampleRate)
	if err != nil {
		return fmt.Errorf("PacketGetNumSamples: %w", err)
	}
	if samples != perfFrameSize {
		return fmt.Errorf("packet samples=%d, want %d", samples, perfFrameSize)
	}
	return nil
}

func perfMusicFrame(start, n, channels int) []float64 {
	out := make([]float64, n*channels)
	for i := 0; i < n; i++ {
		t := float64(start+i) / perfSampleRate
		env := 0.55 + 0.08*math.Sin(2*math.Pi*3.1*t)
		left := env * (0.28*math.Sin(2*math.Pi*440*t) +
			0.18*math.Sin(2*math.Pi*1760*t+0.2) +
			0.10*math.Sin(2*math.Pi*6200*t+0.7) +
			0.05*math.Sin(2*math.Pi*15500*t+1.1))
		out[i*channels] = left
		if channels == 2 {
			right := env * (0.25*math.Sin(2*math.Pi*554.37*t+0.3) +
				0.16*math.Sin(2*math.Pi*2217.46*t+0.5) +
				0.09*math.Sin(2*math.Pi*7600*t+0.9) +
				0.05*math.Sin(2*math.Pi*14200*t+1.4))
			out[i*channels+1] = right
		}
	}
	return out
}

func perfSpeechFrame(start, n, channels int) []float64 {
	return strictSpeechLikeFrame(perfSampleRate, channels, start, n)
}

func perfHybridFrame(start, n, channels int) []float64 {
	return strictHybridWidebandFrame(perfSampleRate, channels, start, n)
}
