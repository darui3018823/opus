//go:build opusref

package opus

import (
	"math"
	"strconv"
	"testing"

	"github.com/darui3018823/opus/internal/cgoref"
)

// The libopus decoders linked by cgoref use SIMD kernels, so float output
// can differ from this library in the last bit; int16 output may then differ
// by one step. Concealed frames (and the frame after, through the overlap)
// carry the small PLC difference tracked separately. A hard clip instead of
// libopus's soft clip, or a saturating demix instead of the wrapping one,
// differs by thousands of steps.
const (
	outputConversionTolerance = 2
	concealmentTolerance      = 64
)

func frameTolerance(lossFrame bool) int {
	if lossFrame {
		return concealmentTolerance
	}
	return outputConversionTolerance
}

// hotFrame is a near-full-scale square-ish signal that overshoots after
// decoding, so the int16 soft clip and the unclamped 24-bit conversion are
// exercised.
func hotFrame(channels, offset, frameSize int) []float64 {
	pcm := make([]float64, frameSize*channels)
	for i := 0; i < frameSize; i++ {
		t := float64(offset+i) / 48000
		for c := 0; c < channels; c++ {
			f := 220.0 * float64(c+1)
			v := math.Sin(2*math.Pi*f*t) + math.Sin(6*math.Pi*f*t)/3 + math.Sin(10*math.Pi*f*t)/5
			pcm[i*channels+c] = 0.98 * v
		}
	}
	return pcm
}

func compareInt16(t *testing.T, what string, got, want []int16, tol int) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: %d samples, libopus %d", what, len(got), len(want))
	}
	for i := range got {
		if d := int(got[i]) - int(want[i]); d > tol || d < -tol {
			t.Fatalf("%s: sample %d = %d, libopus %d", what, i, got[i], want[i])
		}
	}
}

func compareInt32(t *testing.T, what string, got, want []int32, tol int64) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: %d samples, libopus %d", what, len(got), len(want))
	}
	for i := range got {
		if d := int64(got[i]) - int64(want[i]); d > tol || d < -tol {
			t.Fatalf("%s: sample %d = %d, libopus %d", what, i, got[i], want[i])
		}
	}
}

// TestCGODecoderOutputConversionMatchesLibopus checks the int16 soft clip
// (with its memory carried across packets and concealment) and the 24-bit
// conversion of opus_decode / opus_decode24 at a positive decoder gain.
func TestCGODecoderOutputConversionMatchesLibopus(t *testing.T) {
	const (
		rate      = 48000
		channels  = 2
		frameSize = 960
		gainQ8    = 6 * 256
	)
	enc, err := NewEncoder(rate, channels, ApplicationAudio)
	if err != nil {
		t.Fatal(err)
	}
	if err := enc.SetBitrate(128000); err != nil {
		t.Fatal(err)
	}
	dec, _ := NewDecoder(rate, channels)
	dec24, _ := NewDecoder(rate, channels)
	ref, err := cgoref.NewDecoder(rate, channels)
	if err != nil {
		t.Fatal(err)
	}
	defer ref.Close()
	ref24, err := cgoref.NewDecoder(rate, channels)
	if err != nil {
		t.Fatal(err)
	}
	defer ref24.Close()
	for _, d := range []*Decoder{dec, dec24} {
		if err := d.SetGain(gainQ8); err != nil {
			t.Fatal(err)
		}
	}
	for _, d := range []*cgoref.Decoder{ref, ref24} {
		if err := d.SetGain(gainQ8); err != nil {
			t.Fatal(err)
		}
	}
	clipped := 0
	for f := 0; f < 20; f++ {
		packet, err := enc.EncodeFloat(hotFrame(channels, f*frameSize, frameSize), frameSize)
		if err != nil {
			t.Fatal(err)
		}
		loss := f%7 == 5 || f%7 == 6
		if f%7 == 5 {
			packet = nil // concealment leaves the soft-clip memory alone
		}
		got := make([]int16, frameSize*channels)
		var want []int16
		if packet == nil {
			_, err = dec.DecodePLC(got, frameSize)
		} else {
			_, err = dec.Decode(packet, got)
		}
		if err != nil {
			t.Fatal(err)
		}
		if want, err = ref.DecodeInt16(packet, frameSize, false); err != nil {
			t.Fatal(err)
		}
		compareInt16(t, "int16 frame "+strconv.Itoa(f), got, want, frameTolerance(loss))

		got24 := make([]int32, frameSize*channels)
		var want24 []int32
		if packet == nil {
			_, err = dec24.DecodePLC24(got24, frameSize)
		} else {
			_, err = dec24.Decode24(packet, got24)
		}
		if err != nil {
			t.Fatal(err)
		}
		want24, err = ref24.Decode24(packet, frameSize, false)
		if err != nil {
			t.Fatal(err)
		}
		compareInt32(t, "int24 frame "+strconv.Itoa(f), got24, want24, int64(frameTolerance(loss))<<8)
		for _, v := range want24 {
			if v > 8388607 || v < -8388608 {
				clipped++
			}
		}
	}
	if clipped == 0 {
		t.Fatal("test signal never exceeded full scale; the conversions were not exercised")
	}
}

// TestCGOMultistreamProjectionOutputMatchesLibopus checks the per-stream
// soft clip of opus_multistream_decode and the integer demixing of
// opus_projection_decode / opus_projection_decode24, including concealment.
func TestCGOMultistreamProjectionOutputMatchesLibopus(t *testing.T) {
	const (
		rate      = 48000
		frameSize = 960
		gainQ8    = 6 * 256
	)
	t.Run("surround5.1", func(t *testing.T) {
		enc, err := NewSurroundEncoder(rate, 6, 1, ApplicationAudio)
		if err != nil {
			t.Fatal(err)
		}
		dec, err := NewMultistreamDecoder(rate, 6, enc.Streams(), enc.CoupledStreams(), enc.Mapping())
		if err != nil {
			t.Fatal(err)
		}
		dec24, _ := NewMultistreamDecoder(rate, 6, enc.Streams(), enc.CoupledStreams(), enc.Mapping())
		ref, err := cgoref.NewMultistreamDecoder(rate, 6, enc.Streams(), enc.CoupledStreams(), enc.Mapping())
		if err != nil {
			t.Fatal(err)
		}
		defer ref.Close()
		ref24, _ := cgoref.NewMultistreamDecoder(rate, 6, enc.Streams(), enc.CoupledStreams(), enc.Mapping())
		defer ref24.Close()
		for _, d := range []*MultistreamDecoder{dec, dec24} {
			if err := d.SetGain(gainQ8); err != nil {
				t.Fatal(err)
			}
		}
		for _, d := range []*cgoref.MultistreamDecoder{ref, ref24} {
			if err := d.SetGain(gainQ8); err != nil {
				t.Fatal(err)
			}
		}
		for f := 0; f < 12; f++ {
			packet, err := enc.EncodeFloat(hotFrame(6, f*frameSize, frameSize), frameSize)
			if err != nil {
				t.Fatal(err)
			}
			// Concealment drifts slightly from libopus (tracked separately)
			// and the drift persists, so only the last frame is lost.
			loss := f == 11
			if loss {
				packet = nil
			}
			got := make([]int16, frameSize*6)
			got24 := make([]int32, frameSize*6)
			if packet == nil {
				_, err = dec.DecodePLC(got, frameSize)
				if err == nil {
					_, err = dec24.DecodePLC24(got24, frameSize)
				}
			} else {
				_, err = dec.Decode(packet, got)
				if err == nil {
					_, err = dec24.Decode24(packet, got24)
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			want, err := ref.Decode(packet, frameSize, false)
			if err != nil {
				t.Fatal(err)
			}
			want24, err := ref24.Decode24(packet, frameSize, false)
			if err != nil {
				t.Fatal(err)
			}
			compareInt16(t, "int16 frame "+strconv.Itoa(f), got, want, frameTolerance(loss))
			compareInt32(t, "int24 frame "+strconv.Itoa(f), got24, want24, int64(frameTolerance(loss))<<8)
		}
	})
	t.Run("projectionFOA", func(t *testing.T) {
		enc, err := NewProjectionEncoder(rate, 4, 3, ApplicationAudio)
		if err != nil {
			t.Fatal(err)
		}
		matrix := enc.DemixingMatrixBytes()
		dec, err := NewProjectionDecoder(rate, 4, enc.Streams(), enc.CoupledStreams(), matrix)
		if err != nil {
			t.Fatal(err)
		}
		dec24, _ := NewProjectionDecoder(rate, 4, enc.Streams(), enc.CoupledStreams(), matrix)
		decF, _ := NewProjectionDecoder(rate, 4, enc.Streams(), enc.CoupledStreams(), matrix)
		ref, err := cgoref.NewProjectionDecoder(rate, 4, enc.Streams(), enc.CoupledStreams(), matrix)
		if err != nil {
			t.Fatal(err)
		}
		defer ref.Close()
		ref24, _ := cgoref.NewProjectionDecoder(rate, 4, enc.Streams(), enc.CoupledStreams(), matrix)
		defer ref24.Close()
		refF, _ := cgoref.NewProjectionDecoder(rate, 4, enc.Streams(), enc.CoupledStreams(), matrix)
		defer refF.Close()
		for _, d := range []*ProjectionDecoder{dec, dec24, decF} {
			if err := d.SetGain(gainQ8); err != nil {
				t.Fatal(err)
			}
		}
		for _, d := range []*cgoref.ProjectionDecoder{ref, ref24, refF} {
			if err := d.SetGain(gainQ8); err != nil {
				t.Fatal(err)
			}
		}
		for f := 0; f < 12; f++ {
			packet, err := enc.EncodeFloat(hotFrame(4, f*frameSize, frameSize), frameSize)
			if err != nil {
				t.Fatal(err)
			}
			// Concealment drifts slightly from libopus (tracked separately)
			// and the drift persists, so only the last frame is lost.
			loss := f == 11
			if loss {
				packet = nil
			}
			got := make([]int16, frameSize*4)
			got24 := make([]int32, frameSize*4)
			var gotF []float64
			if packet == nil {
				_, err = dec.DecodePLC(got, frameSize)
				if err == nil {
					_, err = dec24.DecodePLC24(got24, frameSize)
				}
				if err == nil {
					gotF, err = decF.DecodePLCFloat(frameSize)
				}
			} else {
				_, err = dec.Decode(packet, got)
				if err == nil {
					_, err = dec24.Decode24(packet, got24)
				}
				if err == nil {
					gotF, err = decF.DecodeFloat(packet)
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			want, err := ref.Decode(packet, frameSize, false)
			if err != nil {
				t.Fatal(err)
			}
			want24, err := ref24.Decode24(packet, frameSize, false)
			if err != nil {
				t.Fatal(err)
			}
			wantF, err := refF.DecodeFloatPacket(packet, frameSize, false)
			if err != nil {
				t.Fatal(err)
			}
			compareInt16(t, "int16 frame "+strconv.Itoa(f), got, want, frameTolerance(loss))
			compareInt32(t, "int24 frame "+strconv.Itoa(f), got24, want24, int64(frameTolerance(loss))<<8)
			for i := range wantF {
				if d := math.Abs(gotF[i] - float64(wantF[i])); d > float64(frameTolerance(loss))/32768 {
					t.Fatalf("float frame %d sample %d = %g, libopus %g", f, i, gotF[i], wantF[i])
				}
			}
		}
	})
	t.Run("projectionChannelValidation", func(t *testing.T) {
		matrix := make([]byte, 2*8*2)
		if _, err := NewProjectionDecoder(rate, 8, 2, 0, matrix); err == nil {
			t.Fatal("8 output channels from 2 coded channels accepted")
		}
		if _, err := cgoref.NewProjectionDecoder(rate, 8, 2, 0, matrix); err == nil {
			t.Fatal("libopus accepted 8 output channels from 2 coded channels")
		}
	})
}
