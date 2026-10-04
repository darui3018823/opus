package audit

import (
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"testing"
)

// loadWav returns interleaved float32 samples at 48 kHz and channel count.
func loadWav(t testing.TB, rel string) ([]float32, int) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, "real_corpus", rel))
	if err != nil {
		t.Skip(err)
	}
	var ch, bits, format int
	pos := 12
	for pos+8 <= len(b) {
		id := string(b[pos : pos+4])
		sz := int(binary.LittleEndian.Uint32(b[pos+4:]))
		body := b[pos+8 : min(len(b), pos+8+sz)]
		switch id {
		case "fmt ":
			format = int(binary.LittleEndian.Uint16(body))
			ch = int(binary.LittleEndian.Uint16(body[2:]))
			bits = int(binary.LittleEndian.Uint16(body[14:]))
		case "data":
			var out []float32
			if bits == 16 {
				for i := 0; i+2 <= len(body); i += 2 {
					out = append(out, float32(int16(binary.LittleEndian.Uint16(body[i:])))/32768)
				}
			} else if bits == 32 && (format == 3 || format == 0xFFFE) {
				for i := 0; i+4 <= len(body); i += 4 {
					out = append(out, math.Float32frombits(binary.LittleEndian.Uint32(body[i:])))
				}
			} else {
				t.Fatalf("unsupported wav %d bits fmt %d", bits, format)
			}
			return out, ch
		}
		pos += 8 + sz + sz&1
	}
	t.Fatal("no data chunk")
	return nil, 0
}

// audio returns seconds of interleaved PCM at rate with ch channels, quantized to int16 grid.
func audio(t testing.TB, rel string, rate, ch int, seconds float64, gain float32) []float32 {
	src, sch := loadWav(t, rel)
	n48 := len(src) / sch
	dec := 48000 / rate
	n := int(seconds * float64(rate))
	out := make([]float32, n*ch)
	for i := 0; i < n; i++ {
		for c := 0; c < ch; c++ {
			sc := c % sch
			var acc float32
			for k := 0; k < dec; k++ {
				idx := ((i*dec + k) % n48)
				acc += src[idx*sch+sc]
			}
			v := acc / float32(dec) * gain
			v = float32(math.Floor(float64(v)*32768+0.5) / 32768)
			if v > 1 {
				v = 1
			} else if v < -1 {
				v = -1
			}
			out[i*ch+c] = v
		}
	}
	return out
}
