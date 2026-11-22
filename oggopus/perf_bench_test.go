package oggopus

import (
	"bytes"
	"io"
	"testing"
)

// benchPagePackets returns one Ogg page that carries 255 packets of 200 bytes,
// about 51 KB, the large-page case of the 2026-11-22 performance audit.
func benchLargePage(tb testing.TB) []byte {
	tb.Helper()
	var buf bytes.Buffer
	w := NewPacketWriter(&buf, 9)
	payload := bytes.Repeat([]byte{0xf8, 0x55}, 100)
	for i := 0; i < 255; i++ {
		opts := PacketWriteOptions{GranulePosition: int64((i + 1) * 960)}
		if i == 254 {
			opts.Flush = true
		}
		if err := w.WritePacket(payload, opts); err != nil {
			tb.Fatal(err)
		}
	}
	return buf.Bytes()
}

// benchSeekStream returns an Opus stream of 128 pages with 64 packets of
// 200 bytes each, so SeekPCM bisects over several megabytes.
func benchSeekStream(tb testing.TB) []byte {
	tb.Helper()
	var stream bytes.Buffer
	w, err := NewWriter(&stream, 77, Head{Version: 1, Channels: 1, PreSkip: 312}, Tags{Vendor: "bench"})
	if err != nil {
		tb.Fatal(err)
	}
	payload := append([]byte{0xf8}, bytes.Repeat([]byte{0x33}, 199)...)
	const pages, perPage = 128, 64
	for i := 0; i < pages*perPage; i++ {
		opts := PacketWriteOptions{GranulePosition: int64((i + 1) * 960), Flush: (i+1)%perPage == 0}
		if i == pages*perPage-1 {
			opts.Flush = false
			opts.EOS = true
		}
		if err := w.WritePacket(payload, opts); err != nil {
			tb.Fatal(err)
		}
	}
	return stream.Bytes()
}

func BenchmarkReadPage(b *testing.B) {
	data := benchLargePage(b)
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := ReadPage(bytes.NewReader(data)); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkPageMarshalBinary(b *testing.B) {
	page, _, err := ParsePage(benchLargePage(b))
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := page.MarshalBinary(); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkPacketReader(b *testing.B) {
	data := benchLargePage(b)
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r := NewPacketReader(bytes.NewReader(data))
		for {
			if _, err := r.Next(); err == io.EOF {
				break
			} else if err != nil {
				b.Fatal(err)
			}
		}
	}
}

func BenchmarkPacketWriter(b *testing.B) {
	payload := bytes.Repeat([]byte{0xf8, 0x55}, 100)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		w := NewPacketWriter(io.Discard, 9)
		for j := 0; j < 4*255; j++ {
			opts := PacketWriteOptions{GranulePosition: int64((j + 1) * 960), Flush: (j+1)%255 == 0}
			if err := w.WritePacket(payload, opts); err != nil {
				b.Fatal(err)
			}
		}
	}
}

func BenchmarkSeekPCM(b *testing.B) {
	data := benchSeekStream(b)
	r, err := NewReader(bytes.NewReader(data))
	if err != nil {
		b.Fatal(err)
	}
	if err := r.SeekPCM(0); err != nil {
		b.Fatal(err)
	}
	targets := []int64{960 * 1000, 960 * 4000, 960 * 6500, 960 * 200}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := r.SeekPCM(targets[i%len(targets)]); err != nil {
			b.Fatal(err)
		}
	}
}
