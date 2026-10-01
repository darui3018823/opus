//go:build opusref

package silk

import (
	"math"
	"testing"

	"github.com/darui3018823/opus/internal/cgoref"
)

// TestSILKVADOracle drives the Go fixed-point VAD port and a verbatim replay
// of libopus silk_VAD_GetSA_Q8_c with the same int16 frame sequences and
// requires speech_activity_Q8, input_tilt_Q15, and input_quality_bands_Q15
// to be identical for every frame, including the noise-level adaptation and
// the smoothed band SNR state.
func TestSILKVADOracle(t *testing.T) {
	type fixture struct {
		name    string
		fsKHz   int
		frameMs int
		gen     func(fs float64, i int, rnd func() float64) float64
	}
	fixtures := []fixture{
		{name: "nb-20ms-speechlike", fsKHz: 8, frameMs: 20, gen: func(fs float64, i int, rnd func() float64) float64 {
			tm := float64(i) / fs
			env := 0.5 + 0.5*math.Sin(2*math.Pi*2.3*tm)
			return 6000*env*math.Sin(2*math.Pi*140*tm) + 1500*env*math.Sin(2*math.Pi*420*tm) + 300*rnd()
		}},
		{name: "mb-10ms-noise-then-tone", fsKHz: 12, frameMs: 10, gen: func(fs float64, i int, rnd func() float64) float64 {
			tm := float64(i) / fs
			if tm < 0.2 {
				return 800 * rnd()
			}
			return 5000*math.Sin(2*math.Pi*220*tm) + 200*rnd()
		}},
		{name: "wb-20ms-speechlike", fsKHz: 16, frameMs: 20, gen: func(fs float64, i int, rnd func() float64) float64 {
			tm := float64(i) / fs
			env := 0.5 + 0.5*math.Sin(2*math.Pi*3.1*tm+0.4)
			return 7000*env*math.Sin(2*math.Pi*180*tm) + 2500*env*math.Sin(2*math.Pi*2400*tm) + 400*rnd()
		}},
		{name: "wb-10ms-quiet", fsKHz: 16, frameMs: 10, gen: func(fs float64, i int, rnd func() float64) float64 {
			return 40 * rnd()
		}},
		{name: "wb-20ms-loud-noise", fsKHz: 16, frameMs: 20, gen: func(fs float64, i int, rnd func() float64) float64 {
			return 12000 * rnd()
		}},
	}
	const nFrames = 40

	for _, fx := range fixtures {
		fx := fx
		t.Run(fx.name, func(t *testing.T) {
			frameLen := fx.fsKHz * fx.frameMs
			var goVAD silkVADState
			goVAD.reset()
			ref := cgoref.NewSILKVAD()
			state := uint32(fx.fsKHz*1000 + fx.frameMs)
			rnd := func() float64 {
				state = state*1664525 + 1013904223
				return float64(int32(state)) / 2147483648.0
			}
			fs := float64(fx.fsKHz * 1000)
			for f := 0; f < nFrames; f++ {
				pcm := make([]int16, frameLen)
				for i := range pcm {
					pcm[i] = silkSAT16(int32(math.Round(fx.gen(fs, f*frameLen+i, rnd))))
				}
				want, err := ref.Process(pcm, fx.fsKHz)
				if err != nil {
					t.Fatalf("frame %d: libopus VAD: %v", f, err)
				}
				saQ8, tiltQ15, qualityQ15 := goVAD.getSAQ8(pcm, frameLen, fx.fsKHz)
				if saQ8 != want.SpeechActivityQ8 {
					t.Fatalf("frame %d: speech_activity_Q8 C=%d Go=%d", f, want.SpeechActivityQ8, saQ8)
				}
				if tiltQ15 != want.InputTiltQ15 {
					t.Fatalf("frame %d: input_tilt_Q15 C=%d Go=%d", f, want.InputTiltQ15, tiltQ15)
				}
				for b := 0; b < silkVADNBands; b++ {
					if qualityQ15[b] != want.InputQualityBandQ15[b] {
						t.Fatalf("frame %d: input_quality_bands_Q15[%d] C=%d Go=%d", f, b, want.InputQualityBandQ15[b], qualityQ15[b])
					}
				}
				if f == nFrames-1 {
					t.Logf("frame %d: SA_Q8=%d tilt_Q15=%d quality=%v", f, saQ8, tiltQ15, qualityQ15)
				}
			}
		})
	}
}
