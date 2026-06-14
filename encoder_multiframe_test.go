package opus

import (
	"bytes"
	"math"
	"testing"
)

// TestEncodeOpusFrameLengthRoundtrip verifies encodeOpusFrameLength is the exact
// inverse of parseOpusFrameLength across the full 0..1275 range.
func TestEncodeOpusFrameLengthRoundtrip(t *testing.T) {
	for n := 0; n <= 1275; n++ {
		enc, err := encodeOpusFrameLength(n)
		if err != nil {
			t.Fatalf("encode %d: %v", n, err)
		}
		if n < 252 && len(enc) != 1 {
			t.Errorf("n=%d: expected 1 byte, got %d", n, len(enc))
		}
		if n >= 252 && len(enc) != 2 {
			t.Errorf("n=%d: expected 2 bytes, got %d", n, len(enc))
		}
		got, used, err := parseOpusFrameLength(enc)
		if err != nil {
			t.Fatalf("parse %d (bytes %v): %v", n, enc, err)
		}
		if got != n {
			t.Errorf("n=%d: round-trip got %d", n, got)
		}
		if used != len(enc) {
			t.Errorf("n=%d: used %d, want %d", n, used, len(enc))
		}
	}

	// Out-of-range must error.
	if _, err := encodeOpusFrameLength(-1); err == nil {
		t.Error("expected error for negative length")
	}
	if _, err := encodeOpusFrameLength(1276); err == nil {
		t.Error("expected error for length > 1275")
	}
}

// TestPackOpusFramesRoundtrip verifies packOpusFrames → splitOpusFrames is the
// identity for a range of frame counts and size profiles.
func TestPackOpusFramesRoundtrip(t *testing.T) {
	mkFrame := func(b byte, n int) []byte {
		f := make([]byte, n)
		for i := range f {
			f[i] = b
		}
		return f
	}

	cases := []struct {
		name     string
		frames   [][]byte
		vbr      bool
		wantCode int
	}{
		{"single", [][]byte{mkFrame(1, 10)}, false, 0},
		{"single-vbr", [][]byte{mkFrame(1, 10)}, true, 0},
		{"two-equal-cbr", [][]byte{mkFrame(1, 20), mkFrame(2, 20)}, false, 1},
		{"two-equal-vbr", [][]byte{mkFrame(1, 20), mkFrame(2, 20)}, true, 2},
		{"two-unequal-cbr", [][]byte{mkFrame(1, 20), mkFrame(2, 33)}, false, 2},
		{"two-unequal-vbr", [][]byte{mkFrame(1, 20), mkFrame(2, 33)}, true, 2},
		{"three-equal-cbr", [][]byte{mkFrame(1, 15), mkFrame(2, 15), mkFrame(3, 15)}, false, 3},
		{"three-equal-vbr", [][]byte{mkFrame(1, 15), mkFrame(2, 15), mkFrame(3, 15)}, true, 3},
		{"three-unequal", [][]byte{mkFrame(1, 15), mkFrame(2, 40), mkFrame(3, 7)}, true, 3},
		{"six-unequal", [][]byte{mkFrame(1, 5), mkFrame(2, 6), mkFrame(3, 7), mkFrame(4, 8), mkFrame(5, 9), mkFrame(6, 10)}, true, 3},
		{"two-extended-len", [][]byte{mkFrame(1, 300), mkFrame(2, 7)}, true, 2},
		{"three-extended-len", [][]byte{mkFrame(1, 260), mkFrame(2, 5), mkFrame(3, 9)}, true, 3},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			payload, code, err := packOpusFrames(tc.frames, tc.vbr)
			if err != nil {
				t.Fatalf("pack: %v", err)
			}
			if code != tc.wantCode {
				t.Errorf("count code = %d, want %d", code, tc.wantCode)
			}
			got, err := splitOpusFrames(payload, code)
			if err != nil {
				t.Fatalf("split (code %d, payload %d bytes): %v", code, len(payload), err)
			}
			if len(got) != len(tc.frames) {
				t.Fatalf("got %d frames, want %d", len(got), len(tc.frames))
			}
			for i := range tc.frames {
				if !bytes.Equal(got[i], tc.frames[i]) {
					t.Errorf("frame %d mismatch: got %v, want %v", i, got[i], tc.frames[i])
				}
			}
		})
	}
}

// TestEncodePaddingCountRoundtrip verifies encodePaddingCount produces a run
// that the decoder's padding-skip loop (in splitOpusFrames) accounts for exactly.
func TestEncodePaddingCountRoundtrip(t *testing.T) {
	for _, pad := range []int{0, 1, 100, 253, 254, 255, 256, 508, 509, 1000, 2540} {
		run := encodePaddingCount(pad)
		// Re-run the decoder's accumulation loop over the run bytes.
		got := 0
		consumed := 0
		for _, b := range run {
			consumed++
			if int(b) == 255 {
				got += 254
				continue
			}
			got += int(b)
			break
		}
		if consumed != len(run) {
			t.Errorf("pad=%d: run %v not fully consumed (%d/%d)", pad, run, consumed, len(run))
		}
		if got != pad {
			t.Errorf("pad=%d: decoder accumulated %d", pad, got)
		}
	}
}

// TestPackOpusFramesPaddedRoundtrip verifies that padded code-3 packets strip
// back to the original frames and that the padding forces code 3 with the flag.
func TestPackOpusFramesPaddedRoundtrip(t *testing.T) {
	mkFrame := func(b byte, n int) []byte {
		f := make([]byte, n)
		for i := range f {
			f[i] = b
		}
		return f
	}

	cases := []struct {
		name   string
		frames [][]byte
		vbr    bool
		pad    int
	}{
		{"single-pad", [][]byte{mkFrame(1, 10)}, false, 5},
		{"two-equal-cbr-pad", [][]byte{mkFrame(1, 20), mkFrame(2, 20)}, false, 3},
		{"two-equal-vbr-pad", [][]byte{mkFrame(1, 20), mkFrame(2, 20)}, true, 300},
		{"two-unequal-pad", [][]byte{mkFrame(1, 20), mkFrame(2, 33)}, false, 1},
		{"three-equal-pad", [][]byte{mkFrame(1, 15), mkFrame(2, 15), mkFrame(3, 15)}, false, 254},
		{"three-unequal-pad", [][]byte{mkFrame(1, 15), mkFrame(2, 40), mkFrame(3, 7)}, true, 255},
		{"six-pad-large", [][]byte{mkFrame(1, 5), mkFrame(2, 6), mkFrame(3, 7), mkFrame(4, 8), mkFrame(5, 9), mkFrame(6, 10)}, true, 600},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			payload, code, err := packOpusFramesPadded(tc.frames, tc.vbr, tc.pad)
			if err != nil {
				t.Fatalf("pack: %v", err)
			}
			if code != 3 {
				t.Fatalf("padding must use code 3, got %d", code)
			}
			if payload[0]&0x40 == 0 {
				t.Errorf("padding flag (0x40) not set in frame-count byte 0x%02x", payload[0])
			}
			got, err := splitOpusFrames(payload, code)
			if err != nil {
				t.Fatalf("split (payload %d bytes): %v", len(payload), err)
			}
			if len(got) != len(tc.frames) {
				t.Fatalf("got %d frames, want %d", len(got), len(tc.frames))
			}
			for i := range tc.frames {
				if !bytes.Equal(got[i], tc.frames[i]) {
					t.Errorf("frame %d mismatch: got %v, want %v", i, got[i], tc.frames[i])
				}
			}
		})
	}

	// padBytes <= 0 must defer to packOpusFrames exactly.
	frames := [][]byte{mkFrame(1, 20), mkFrame(2, 20)}
	p0, c0, err := packOpusFramesPadded(frames, false, 0)
	if err != nil {
		t.Fatal(err)
	}
	p1, c1, err := packOpusFrames(frames, false)
	if err != nil {
		t.Fatal(err)
	}
	if c0 != c1 || !bytes.Equal(p0, p1) {
		t.Errorf("padBytes=0 should equal packOpusFrames: code %d/%d, equal=%v", c0, c1, bytes.Equal(p0, p1))
	}
}

func TestPackOpusFramesErrors(t *testing.T) {
	if _, _, err := packOpusFrames(nil, false); err == nil {
		t.Error("expected error for zero frames")
	}
	too := make([][]byte, 49)
	for i := range too {
		too[i] = []byte{0}
	}
	if _, _, err := packOpusFrames(too, true); err == nil {
		t.Error("expected error for > 48 frames")
	}
}

// TestEncoderMultiFrameRoundTrip verifies that requesting a 40 ms or 60 ms frame
// produces a multi-frame packet whose count code reflects the duration, and that
// the packet decodes to the right number of samples.
func TestEncoderMultiFrameRoundTrip(t *testing.T) {
	const (
		sampleRate = 48000
		channels   = 1
		base       = sampleRate / 50 // 20 ms = 960
	)

	for _, mult := range []int{2, 3} {
		for _, cbr := range []bool{true, false} {
			name := "x" + string(rune('0'+mult))
			if cbr {
				name += "-cbr"
			} else {
				name += "-vbr"
			}
			t.Run(name, func(t *testing.T) {
				enc, err := NewEncoder(sampleRate, channels, ApplicationAudio)
				if err != nil {
					t.Fatal(err)
				}
				_ = enc.SetBitrate(64000)
				enc.SetVBR(!cbr)

				dec, err := NewDecoder(sampleRate, channels)
				if err != nil {
					t.Fatal(err)
				}

				frameSize := base * mult
				pcm := make([]float64, frameSize*channels)
				for i := range pcm {
					tsec := float64(i) / float64(sampleRate)
					pcm[i] = 0.3*math.Sin(2*math.Pi*440*tsec) +
						0.2*math.Sin(2*math.Pi*2000*tsec)
				}

				pkt, err := enc.EncodeFloat(pcm, frameSize)
				if err != nil {
					t.Fatalf("encode: %v", err)
				}

				// Count code must indicate a multi-frame packet (low 2 TOC bits).
				code := int(pkt[0] & 0x03)
				if mult == 2 {
					if code != 1 && code != 2 {
						t.Errorf("2-frame: count code = %d, want 1 or 2", code)
					}
				} else {
					if code != 3 {
						t.Errorf("3-frame: count code = %d, want 3", code)
					}
				}

				decoded, err := dec.DecodeFloat(pkt)
				if err != nil {
					t.Fatalf("decode (%d bytes, code %d): %v", len(pkt), code, err)
				}
				if len(decoded) != frameSize {
					t.Errorf("decoded %d samples, want %d", len(decoded), frameSize)
				}
			})
		}
	}
}

// TestEncoderPacketPaddingRoundTrip verifies that SetPacketPadding produces a
// larger code-3 packet that still decodes to exactly the same audio as the
// unpadded one (the decoder strips the padding).
func TestEncoderPacketPaddingRoundTrip(t *testing.T) {
	const (
		sampleRate = 48000
		channels   = 1
		frameSize  = 960 // 20 ms, single frame
	)

	pcm := make([]float64, frameSize*channels)
	for i := range pcm {
		tsec := float64(i) / float64(sampleRate)
		pcm[i] = 0.3*math.Sin(2*math.Pi*440*tsec) + 0.2*math.Sin(2*math.Pi*2000*tsec)
	}

	for _, pad := range []int{4, 50, 300} {
		t.Run(padLabel(pad), func(t *testing.T) {
			// Unpadded reference (code 0).
			ref, err := NewEncoder(sampleRate, channels, ApplicationAudio)
			if err != nil {
				t.Fatal(err)
			}
			refPkt, err := ref.EncodeFloat(pcm, frameSize)
			if err != nil {
				t.Fatal(err)
			}
			if code := int(refPkt[0] & 0x03); code != 0 {
				t.Fatalf("reference packet should be code 0, got %d", code)
			}

			// Padded encoder.
			enc, err := NewEncoder(sampleRate, channels, ApplicationAudio)
			if err != nil {
				t.Fatal(err)
			}
			enc.SetPacketPadding(pad)
			padPkt, err := enc.EncodeFloat(pcm, frameSize)
			if err != nil {
				t.Fatal(err)
			}
			if code := int(padPkt[0] & 0x03); code != 3 {
				t.Errorf("padded packet should be code 3, got %d", code)
			}
			if padPkt[1]&0x40 == 0 {
				t.Errorf("padding flag not set (frame-count byte 0x%02x)", padPkt[1])
			}
			if len(padPkt) <= len(refPkt) {
				t.Errorf("padded packet (%d) not larger than reference (%d)", len(padPkt), len(refPkt))
			}

			// Both must decode to the same audio.
			dr, err := NewDecoder(sampleRate, channels)
			if err != nil {
				t.Fatal(err)
			}
			refDec, err := dr.DecodeFloat(refPkt)
			if err != nil {
				t.Fatalf("decode reference: %v", err)
			}
			dp, err := NewDecoder(sampleRate, channels)
			if err != nil {
				t.Fatal(err)
			}
			padDec, err := dp.DecodeFloat(padPkt)
			if err != nil {
				t.Fatalf("decode padded (%d bytes): %v", len(padPkt), err)
			}
			if len(padDec) != len(refDec) {
				t.Fatalf("padded decoded %d samples, reference %d", len(padDec), len(refDec))
			}
			for i := range refDec {
				if padDec[i] != refDec[i] {
					t.Fatalf("sample %d differs: padded=%v reference=%v", i, padDec[i], refDec[i])
				}
			}
		})
	}
}

func padLabel(pad int) string {
	return "pad" + string(rune('0'+pad/100)) + string(rune('0'+(pad/10)%10)) + string(rune('0'+pad%10))
}
